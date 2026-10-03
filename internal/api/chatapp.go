package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/onboarding/chatapp"
)

// Chat-app setup timeouts. A pairing call is a long poll the client repeats.
const (
	chatAppVerifyTimeout = 10 * time.Second
	pairDefaultSeconds   = 25
	pairMaxSeconds       = 50
)

var chatAppLabels = map[string]string{"telegram": "Telegram", "discord": "Discord"}

// telegramTokenRe matches BotFather's "<bot id>:<secret>" format.
var telegramTokenRe = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{30,}$`)

type chatAppVerifyInput struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

// chatAppVerifyResponse reports a token check. Status is ok, rejected,
// unreachable or error; Bot is set when ok.
type chatAppVerifyResponse struct {
	Status  string           `json:"status"`
	Message string           `json:"message"`
	Bot     *chatapp.BotInfo `json:"bot,omitempty"`
}

type chatAppPairInput struct {
	Type           string `json:"type"`
	Token          string `json:"token"`
	Cursor         string `json:"cursor,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// chatAppPairResponse is one pairing poll. Status is found (Sender set),
// timeout (poll again with Cursor), conflict, rejected, unreachable, manual
// (the platform needs the ID entered by hand) or error.
type chatAppPairResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message,omitempty"`
	Sender  *chatapp.Sender `json:"sender,omitempty"`
	Cursor  string          `json:"cursor,omitempty"`
}

type chatAppSaveInput struct {
	Type         string   `json:"type"`
	Token        string   `json:"token"`
	AllowedUsers []string `json:"allowed_users"`
	// Agent defaults to the primary agent.
	Agent string `json:"agent,omitempty"`
	// NotifyChatID, when set, gets a best-effort "connected" message.
	NotifyChatID string `json:"notify_chat_id,omitempty"`
}

type chatAppSaveResponse struct {
	Status          string        `json:"status"`
	Agent           string        `json:"agent"`
	RestartRequired bool          `json:"restart_required"`
	Restart         restartStatus `json:"restart"`
}

// validateChatAppToken returns an error message for a bad type or token.
func validateChatAppToken(typ, token string) string {
	if _, ok := chatAppLabels[typ]; !ok {
		return "type must be telegram or discord"
	}
	if token == "" {
		return "token is required"
	}
	if typ == "telegram" && !telegramTokenRe.MatchString(token) {
		return "that doesn't look like a Telegram bot token; BotFather's tokens look like 123456789:AA…"
	}
	return ""
}

// chatAppProber returns the prober for typ, or writes 503/400 and returns nil.
func (s *Server) chatAppProber(w http.ResponseWriter, typ, token string) chatapp.Prober {
	if msg := validateChatAppToken(typ, token); msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
		return nil
	}
	p := s.deps.ChatApps[typ]
	if p == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "chat app setup not available"})
		return nil
	}
	return p
}

// handleChatAppVerify godoc
// @Summary      Verify a chat bot token
// @Description  Checks a Telegram or Discord bot token with the platform and returns the bot's identity. Nothing is saved. Always answers 200 with a status; the token is never logged or echoed.
// @Tags         onboarding
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  chatAppVerifyInput  true  "Token to verify"
// @Success      200  {object}  chatAppVerifyResponse
// @Failure      400  {object}  map[string]string
// @Failure      503  {object}  map[string]string
// @Router       /onboarding/chat-app/verify [post]
func (s *Server) handleChatAppVerify(w http.ResponseWriter, r *http.Request) {
	var in chatAppVerifyInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	p := s.chatAppProber(w, in.Type, in.Token)
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), chatAppVerifyTimeout)
	defer cancel()

	bot, err := p.Verify(ctx, in.Token)
	label := chatAppLabels[in.Type]
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, chatAppVerifyResponse{Status: "ok", Message: "Connected as @" + bot.Username, Bot: &bot})
	case errors.Is(err, chatapp.ErrRejected):
		writeJSON(w, http.StatusOK, chatAppVerifyResponse{Status: "rejected", Message: label + " doesn't recognise this token. Copy it again from " + botSourceFor(in.Type) + "."})
	case errors.Is(err, chatapp.ErrUnreachable):
		writeJSON(w, http.StatusOK, chatAppVerifyResponse{Status: "unreachable", Message: "Couldn't reach " + label + ". Check this server's network."})
	default:
		s.logger.Warn("chat app verify failed", "type", in.Type, "error", err)
		writeJSON(w, http.StatusOK, chatAppVerifyResponse{Status: "error", Message: label + " returned an unexpected answer. Try again."})
	}
}

func botSourceFor(typ string) string {
	if typ == "telegram" {
		return "@BotFather"
	}
	return "the Discord developer portal"
}

// handleChatAppPair godoc
// @Summary      Wait for the bot owner's first message
// @Description  Long-polls the bot for up to timeout_seconds (default 25, max 50) for the first private message from a person, so the wizard can fill allowed_users without asking for a numeric ID. Messages sent before the first call are ignored. Answer timeout means poll again with the returned cursor. Discord answers manual: enter the ID by hand.
// @Tags         onboarding
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  chatAppPairInput  true  "Bot to listen on"
// @Success      200  {object}  chatAppPairResponse
// @Failure      400  {object}  map[string]string
// @Failure      409  {object}  map[string]string  "The adapter is already running, or another pairing is waiting"
// @Failure      503  {object}  map[string]string
// @Router       /onboarding/chat-app/pair [post]
func (s *Server) handleChatAppPair(w http.ResponseWriter, r *http.Request) {
	var in chatAppPairInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	p := s.chatAppProber(w, in.Type, in.Token)
	if p == nil {
		return
	}
	if s.deps.Dispatcher != nil && s.deps.Dispatcher.HasAdapter(in.Type) {
		// The running adapter would compete for the same updates.
		writeJSON(w, http.StatusConflict, map[string]string{"error": chatAppLabels[in.Type] + " is already connected on this server; add users in the config instead"})
		return
	}
	if !s.tryStartPairing(in.Type) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "another pairing is already waiting for a message"})
		return
	}
	defer s.endPairing(in.Type)

	secs := in.TimeoutSeconds
	if secs <= 0 {
		secs = pairDefaultSeconds
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(min(secs, pairMaxSeconds))*time.Second)
	defer cancel()

	sender, cursor, err := p.WaitForSender(ctx, in.Token, in.Cursor)
	writeJSON(w, http.StatusOK, pairResult(in.Type, sender, cursor, err))
}

func pairResult(typ string, sender chatapp.Sender, cursor string, err error) chatAppPairResponse {
	label := chatAppLabels[typ]
	switch {
	case err == nil:
		return chatAppPairResponse{Status: "found", Sender: &sender, Cursor: cursor}
	case errors.Is(err, chatapp.ErrNoSender):
		return chatAppPairResponse{Status: "timeout", Cursor: cursor}
	case errors.Is(err, chatapp.ErrConflict):
		return chatAppPairResponse{Status: "conflict", Message: "Another program is reading this bot's messages (a webhook or a second server). Stop it, or enter your user ID by hand."}
	case errors.Is(err, chatapp.ErrRejected):
		return chatAppPairResponse{Status: "rejected", Message: label + " doesn't recognise this token any more."}
	case errors.Is(err, chatapp.ErrUnsupported):
		return chatAppPairResponse{Status: "manual", Message: "Enter your " + label + " user ID: turn on Developer Mode, then right-click your name and choose Copy User ID."}
	case errors.Is(err, chatapp.ErrUnreachable):
		return chatAppPairResponse{Status: "unreachable", Message: "Couldn't reach " + label + "."}
	default:
		return chatAppPairResponse{Status: "error", Message: label + " returned an unexpected answer. Try again."}
	}
}

// tryStartPairing claims the single pairing slot for typ.
func (s *Server) tryStartPairing(typ string) bool {
	s.pairingMu.Lock()
	defer s.pairingMu.Unlock()
	if s.pairing == nil {
		s.pairing = make(map[string]bool)
	}
	if s.pairing[typ] {
		return false
	}
	s.pairing[typ] = true
	return true
}

func (s *Server) endPairing(typ string) {
	s.pairingMu.Lock()
	defer s.pairingMu.Unlock()
	delete(s.pairing, typ)
}

// handleChatAppSave godoc
// @Summary      Save a chat app connection
// @Description  Writes the bot token and allowed_users and binds the adapter to the agent (default: the primary agent) in one validated config write. The adapter starts on the next restart: the response says whether a process manager will bring the server back after POST /server/restart.
// @Tags         onboarding
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  chatAppSaveInput  true  "Connection to save"
// @Success      200  {object}  chatAppSaveResponse
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string  "Agent not in config"
// @Failure      409  {object}  map[string]string  "An env var overrides the token, or the binding conflicts with another agent"
// @Failure      503  {object}  map[string]string
// @Router       /onboarding/chat-app/save [post]
func (s *Server) handleChatAppSave(w http.ResponseWriter, r *http.Request) {
	var in chatAppSaveInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	snap := s.appConfig()
	agent, status, msg := s.validateChatAppSave(&in, snap)
	if status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}

	err := config.SaveChatAdapter(s.deps.ConfigPath, config.ChatAdapterBinding{
		Section: in.Type, Token: in.Token, AllowedUsers: in.AllowedUsers, Agent: agent,
	})
	switch {
	case errors.Is(err, config.ErrAgentNotInConfig):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent " + agent + " is not in the config file"})
		return
	case errors.Is(err, config.ErrInvalidCandidate):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "saving chat app: " + err.Error()})
		return
	}

	// Reload from disk so memory matches the merged allowed_users exactly.
	if fresh, loadErr := config.Load(s.deps.ConfigPath); loadErr == nil {
		s.deps.Config.Update(func(c *config.Config) {
			c.Telegram, c.Discord = fresh.Telegram, fresh.Discord
			for i := range c.Agents {
				if c.Agents[i].Name == agent && !slices.Contains(c.Agents[i].Adapters, in.Type) {
					c.Agents[i].Adapters = append(c.Agents[i].Adapters, in.Type)
				}
			}
		})
	}
	s.notifyPaired(r.Context(), in)

	writeJSON(w, http.StatusOK, chatAppSaveResponse{
		Status:          "saved",
		Agent:           agent,
		RestartRequired: true,
		Restart:         restartStatus{Available: s.deps.RestartFunc != nil, Managed: s.deps.RestartManaged},
	})
}

// validateChatAppSave resolves the target agent and checks the input. A
// non-zero status means reject.
func (s *Server) validateChatAppSave(in *chatAppSaveInput, snap *config.Config) (string, int, string) {
	if msg := validateChatAppToken(in.Type, in.Token); msg != "" {
		return "", http.StatusBadRequest, msg
	}
	if s.deps.ConfigPath == "" {
		return "", http.StatusServiceUnavailable, "config persistence not available"
	}
	if len(in.AllowedUsers) == 0 {
		return "", http.StatusBadRequest, "allowed_users needs at least one user ID"
	}
	envVar := "DENKEEPER_TELEGRAM_TOKEN"
	if in.Type == "discord" {
		envVar = "DENKEEPER_DISCORD_TOKEN"
	}
	if os.Getenv(envVar) != "" {
		return "", http.StatusConflict, envVar + " is set and would override the saved token; set the token there instead"
	}
	agent := in.Agent
	if agent == "" {
		if p := primaryAgent(snap); p != nil {
			agent = p.Name
		}
	}
	if agent == "" {
		return "", http.StatusNotFound, "create an agent before connecting a chat app"
	}
	return agent, 0, ""
}

// notifyPaired tells the paired user the bot is set up. Best effort.
func (s *Server) notifyPaired(ctx context.Context, in chatAppSaveInput) {
	p := s.deps.ChatApps[in.Type]
	if p == nil || in.NotifyChatID == "" {
		return
	}
	nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), chatAppVerifyTimeout)
	defer cancel()
	if err := p.Notify(nctx, in.Token, in.NotifyChatID, "Connected to Denkeeper. I'll reply here once the server restarts."); err != nil {
		s.logger.Info("pairing confirmation not sent", "type", in.Type, "error", err)
	}
}
