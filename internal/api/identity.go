package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Temikus/denkeeper/internal/persona"
)

// Identity field limits. The theme is free text the model reads every turn,
// so it is capped to keep the system prompt small.
const (
	identityNameMax  = 64  // characters
	identityEmojiMax = 32  // bytes; a flag or ZWJ sequence runs to ~28
	identityThemeMax = 500 // characters
	identityBodyMax  = 16 << 10
)

// identityInput is the body of PUT /api/v1/agents/{name}/identity.
type identityInput struct {
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
	Theme string `json:"theme"`
}

// handleUpdateIdentity godoc
// @Summary      Update agent identity
// @Description  Sets the display name, emoji and theme in the agent's IDENTITY.md frontmatter, keeping its markdown body. The server encodes the YAML, so callers send plain values rather than building frontmatter.
// @Tags         persona
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        name  path  string         true  "Agent name"
// @Param        body  body  identityInput  true  "Identity fields"
// @Success      200  {object}  identityInput
// @Failure      400  {object}  map[string]string  "Invalid field"
// @Failure      404  {object}  map[string]string  "Agent not found"
// @Failure      409  {object}  map[string]string  "IDENTITY.md frontmatter does not parse"
// @Failure      500  {object}  map[string]string  "Save failed"
// @Router       /agents/{name}/identity [put]
func (s *Server) handleUpdateIdentity(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	e := s.deps.Dispatcher.Agent(agentName)
	if e == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	var input identityInput
	r.Body = http.MaxBytesReader(w, r.Body, identityBodyMax)
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Emoji = strings.TrimSpace(input.Emoji)
	input.Theme = strings.TrimSpace(input.Theme)
	if msg := validateIdentity(input); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return
	}

	// Keep the body: only the frontmatter is the wizard's to set.
	current, _, _, _ := e.PersonaSection("identity")
	existing, err := persona.ParseIdentity(current)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "IDENTITY.md has invalid frontmatter; fix it before updating the identity"})
		return
	}
	content, err := persona.FormatIdentity(persona.Identity{
		Name: input.Name, Emoji: input.Emoji, Theme: input.Theme, Body: existing.Body,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := e.SavePersonaSection("identity", content); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("saving identity: %v", err)})
		return
	}

	s.logger.Info("agent identity updated via API", "agent", agentName)
	writeJSON(w, http.StatusOK, input)
}

func validateIdentity(in identityInput) string {
	switch {
	case strings.ContainsAny(in.Name, "\r\n") || strings.ContainsAny(in.Emoji, "\r\n"):
		return "name and emoji must be a single line"
	case utf8.RuneCountInString(in.Name) > identityNameMax:
		return fmt.Sprintf("name must be at most %d characters", identityNameMax)
	case len(in.Emoji) > identityEmojiMax:
		return "emoji is too long; use a single emoji"
	case utf8.RuneCountInString(in.Theme) > identityThemeMax:
		return fmt.Sprintf("theme must be at most %d characters", identityThemeMax)
	}
	return ""
}
