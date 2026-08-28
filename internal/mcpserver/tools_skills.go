package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/skilleffect"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type skillListInput struct {
	Agent string `json:"agent" jsonschema:"Agent name"`
}

type skillGetInput struct {
	Agent string `json:"agent" jsonschema:"Agent name"`
	Name  string `json:"name" jsonschema:"Skill name"`
}

type skillCreateInput struct {
	Agent         string   `json:"agent" jsonschema:"Agent name"`
	Name          string   `json:"name" jsonschema:"Skill name"`
	Description   string   `json:"description,omitempty" jsonschema:"Skill description"`
	Version       string   `json:"version,omitempty" jsonschema:"Skill version (e.g. 1.0.0)"`
	Triggers      []string `json:"triggers,omitempty" jsonschema:"Trigger keywords"`
	Body          string   `json:"body" jsonschema:"Skill content/instructions"`
	MaxToolRounds int      `json:"max_tool_rounds,omitempty" jsonschema:"Optional cap on tool-call ROUNDS (not calls) for turns this skill drives; 0 = no cap. Only lowers the agent's budget, never raises it."`
	RequiresTools []string `json:"requires_tools,omitempty" jsonschema:"Optional tool names this skill depends on (frontmatter [requires] tools)."`
}

type skillUpdateInput struct {
	Agent         string   `json:"agent" jsonschema:"Agent name"`
	Name          string   `json:"name" jsonschema:"Skill name to update"`
	NewName       *string  `json:"new_name,omitempty" jsonschema:"New skill name (rename)"`
	Description   *string  `json:"description,omitempty" jsonschema:"New description"`
	Version       *string  `json:"version,omitempty" jsonschema:"New version (e.g. 1.0.0)"`
	Triggers      []string `json:"triggers,omitempty" jsonschema:"New triggers"`
	Body          *string  `json:"body,omitempty" jsonschema:"New content"`
	MaxToolRounds *int     `json:"max_tool_rounds,omitempty" jsonschema:"New cap on tool-call ROUNDS (not calls); 0 removes the cap. Omit to keep current."`
	RequiresTools []string `json:"requires_tools,omitempty" jsonschema:"New required tool names; omit to keep current, pass [] to clear."`
}

type skillDeleteInput struct {
	Agent string `json:"agent" jsonschema:"Agent name"`
	Name  string `json:"name" jsonschema:"Skill name to delete"`
}

type skillRevertInput struct {
	Agent        string `json:"agent" jsonschema:"Agent name"`
	Skill        string `json:"skill,omitempty" jsonschema:"Revert the most recent change to this skill. Omit both skill and transition_id to revert the agent's most recent skill change."`
	TransitionID string `json:"transition_id,omitempty" jsonschema:"Revert every change in one edit, newest first. Mutually exclusive with skill."`
}

// validSkillName delegates to the shared validator in configmcp so the same
// path-safety rule applies on every surface. It runs here too for an early,
// friendly error before any payload is built; configmcp re-checks the exact
// name used for IO.
func validSkillName(name string) error {
	return configmcp.ValidateSkillName(name)
}

// skillMaxBytes returns the configured per-skill size cap, or 0 (no limit) when
// no config is wired.
func (s *Server) skillMaxBytes() int {
	cfg := s.deps.Config.Get()
	if cfg == nil {
		return 0
	}
	return cfg.Skills.MaxBytes
}

// skillWriter returns a writer that journals each mutation before performing
// it, so a skill change made over MCP can be reverted with skill_revert. A
// memory store without the revision methods — or none at all — yields an
// untracked passthrough that behaves exactly like the direct write helpers it
// replaced.
//
// Actor is "user": this surface is reached with an operator's API key, as
// opposed to the agent editing its own skills through config MCP.
func (s *Server) skillWriter(agentName string, e *agent.Engine) *skilleffect.Binding {
	store, _ := s.deps.Memory.(skilleffect.Store)
	return skilleffect.New(store, agentName, s.deps.Logger).Bind(e, agent.SkillActorUser, s.skillMaxBytes())
}

func (s *Server) registerSkillTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "skill_list",
		Description: "List all skills for an agent. Returns name, description, and triggers. " +
			"Requires 'skills:read' scope.",
	}, s.handleSkillList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "skill_get",
		Description: "Get full details of a skill including its body content. " +
			"Requires 'skills:read' scope.",
	}, s.handleSkillGet)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "skill_create",
		Description: "Create a new skill for an agent. Requires name and body at minimum. " +
			"Requires 'skills:write' scope.",
	}, s.handleSkillCreate)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "skill_update",
		Description: "Update an existing skill. Only provided fields are changed. " +
			"Requires 'skills:write' scope.",
	}, s.handleSkillUpdate)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "skill_delete",
		Description: "Delete a skill by name. Requires 'skills:write' scope.",
	}, s.handleSkillDelete)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "skill_revert",
		Description: "Revert a skill to its state before the most recent change. " +
			"Undoes a create, update, rename or delete exactly once, restoring the " +
			"file byte for byte. A revert is itself a recorded change, so calling " +
			"this twice in a row REDOES the original change rather than stepping " +
			"further back. Restores the skill file only: messages already sent, " +
			"tool calls already made and KV keys already written while the changed " +
			"skill was live are not undone. Requires 'skills:write' scope.",
	}, s.handleSkillRevert)
}

func (s *Server) handleSkillList(ctx context.Context, _ *mcp.CallToolRequest, input skillListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:read"); err != nil {
		return err, nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	type skillSummary struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Triggers    []string `json:"triggers,omitempty"`
	}

	skills := e.Skills()
	result := make([]skillSummary, len(skills))
	for i, sk := range skills {
		result[i] = skillSummary{Name: sk.Name, Description: sk.Description, Triggers: sk.Triggers}
	}

	r, err := toolJSON(result)
	return r, nil, err
}

func (s *Server) handleSkillGet(ctx context.Context, _ *mcp.CallToolRequest, input skillGetInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:read"); err != nil {
		return err, nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	sk, found := e.GetSkill(input.Name)
	if !found {
		return toolError(fmt.Sprintf("skill %q not found on agent %q", input.Name, input.Agent)), nil, nil
	}

	r, err := toolJSON(sk)
	return r, nil, err
}

func (s *Server) handleSkillCreate(ctx context.Context, _ *mcp.CallToolRequest, input skillCreateInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:write"); err != nil {
		return err, nil, nil
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Body) == "" {
		return toolError("name and body are required"), nil, nil
	}
	if err := validSkillName(input.Name); err != nil {
		return toolError(err.Error()), nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	skillsDir := e.SkillsDir()
	if skillsDir == "" {
		return toolError("skill management is not available for this agent"), nil, nil
	}

	if _, exists := e.GetSkill(input.Name); exists {
		return toolError(fmt.Sprintf("skill %q already exists", input.Name)), nil, nil
	}

	payload := configmcp.BuildSkillPayload(input.Name, input.Description, input.Version, input.Triggers, input.Body, input.MaxToolRounds, input.RequiresTools)
	if err := s.skillWriter(input.Agent, e).Create(ctx, payload); err != nil {
		return toolError("creating skill: " + err.Error()), nil, nil
	}

	return toolText("skill created: " + input.Name), nil, nil
}

func (s *Server) handleSkillUpdate(ctx context.Context, _ *mcp.CallToolRequest, input skillUpdateInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:write"); err != nil {
		return err, nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	skillsDir := e.SkillsDir()
	if skillsDir == "" {
		return toolError("skill management is not available for this agent"), nil, nil
	}

	if err := validSkillName(input.Name); err != nil {
		return toolError(err.Error()), nil, nil
	}

	existing, found := e.GetSkill(input.Name)
	if !found {
		return toolError(fmt.Sprintf("skill %q not found on agent %q", input.Name, input.Agent)), nil, nil
	}

	// Determine effective name (rename or keep).
	newName := input.Name
	isRename := false
	if input.NewName != nil && strings.TrimSpace(*input.NewName) != "" && *input.NewName != input.Name {
		newName = strings.TrimSpace(*input.NewName)
		if err := validSkillName(newName); err != nil {
			return toolError(err.Error()), nil, nil
		}
		isRename = true
		if _, exists := e.GetSkill(newName); exists {
			return toolError(fmt.Sprintf("skill %q already exists", newName)), nil, nil
		}
	}

	payload := configmcp.MergeSkillFields(newName, existing, input.Description, input.Version, input.Triggers, input.Body, input.MaxToolRounds, input.RequiresTools)

	writer := s.skillWriter(input.Agent, e)
	if isRename {
		if err := writer.Rename(ctx, input.Name, payload); err != nil {
			return toolError("renaming skill: " + err.Error()), nil, nil
		}
		return toolText("skill renamed: " + input.Name + " → " + newName), nil, nil
	}

	if err := writer.Update(ctx, input.Name, payload); err != nil {
		return toolError("updating skill: " + err.Error()), nil, nil
	}

	return toolText("skill updated: " + input.Name), nil, nil
}

func (s *Server) handleSkillDelete(ctx context.Context, _ *mcp.CallToolRequest, input skillDeleteInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:write"); err != nil {
		return err, nil, nil
	}

	if err := validSkillName(input.Name); err != nil {
		return toolError(err.Error()), nil, nil
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}

	skillsDir := e.SkillsDir()
	if skillsDir == "" {
		return toolError("skill management is not available for this agent"), nil, nil
	}

	if _, ok := e.GetSkill(input.Name); !ok {
		return toolError(fmt.Sprintf("skill %q not found on agent %q", input.Name, input.Agent)), nil, nil
	}

	// Disk-first: the writer removes the file before mutating memory, so a real
	// IO error leaves the skill intact (matching create/update/rename's
	// semantics).
	if err := s.skillWriter(input.Agent, e).Delete(ctx, input.Name); err != nil {
		return toolError("deleting skill file: " + err.Error()), nil, nil
	}

	return toolText("skill deleted: " + input.Name), nil, nil
}

func (s *Server) handleSkillRevert(ctx context.Context, _ *mcp.CallToolRequest, input skillRevertInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "skills:write"); err != nil {
		return err, nil, nil
	}

	skillName := strings.TrimSpace(input.Skill)
	transitionID := strings.TrimSpace(input.TransitionID)
	if skillName != "" && transitionID != "" {
		return toolError("provide at most one of skill or transition_id"), nil, nil
	}
	if skillName != "" {
		if err := validSkillName(skillName); err != nil {
			return toolError(err.Error()), nil, nil
		}
	}

	e := s.deps.Dispatcher.Agent(input.Agent)
	if e == nil {
		return toolError("agent not found: " + input.Agent), nil, nil
	}
	if e.SkillsDir() == "" {
		return toolError("skill management is not available for this agent"), nil, nil
	}

	reverted, err := runSkillRevert(ctx, s.skillWriter(input.Agent, e), skillName, transitionID)

	// Nothing was touched in these two cases, so they are not worth an audit
	// event — they are questions answered, not changes made.
	switch {
	case errors.Is(err, skilleffect.ErrNoRevision):
		return toolError("nothing to revert: no armed skill revision found"), nil, nil
	case errors.Is(err, skilleffect.ErrJournalDisabled):
		return toolError("skill revert is unavailable: no revision journal is configured"), nil, nil
	}

	// Past here disk state changed, or failed part-way through changing —
	// either way the revisions that did land are already applied.
	s.emitSkillRevertAudit(ctx, input.Agent, reverted, err)
	if err != nil {
		return toolError("reverting skill: " + err.Error()), nil, nil
	}
	return toolText(describeReverted(reverted)), nil, nil
}

// runSkillRevert picks the revert flavour: a whole transition, or the newest
// armed revision for one skill (or for the agent when skillName is empty).
func runSkillRevert(ctx context.Context, writer *skilleffect.Binding, skillName, transitionID string) ([]agent.SkillRevision, error) {
	if transitionID != "" {
		return writer.RevertTransition(ctx, transitionID)
	}
	rev, err := writer.Revert(ctx, skillName)
	if rev == nil {
		return nil, err
	}
	return []agent.SkillRevision{*rev}, err
}

// describeReverted renders one line per undone revision, naming the revision
// id, the op it undid, the skill, and the version now on disk.
func describeReverted(reverted []agent.SkillRevision) string {
	lines := make([]string, 0, len(reverted))
	for _, rev := range reverted {
		line := fmt.Sprintf("reverted revision %d (%s) on skill %q: ", rev.ID, rev.Op, rev.SkillName)
		switch {
		case rev.PriorPayload == nil:
			line += "skill removed — it did not exist before that change"
		case rev.PriorName != nil:
			line += fmt.Sprintf("renamed back to %q at version %s", *rev.PriorName, versionLabel(rev.PriorVersion))
		default:
			line += "restored version " + versionLabel(rev.PriorVersion)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// versionLabel keeps an unparseable or version-less skill readable.
func versionLabel(v string) string {
	if v == "" {
		return "(unversioned)"
	}
	return v
}

// emitSkillRevertAudit records the revert when an auditor is wired. The summary
// states the limit of the guarantee deliberately: the journal restores skill
// files, and nothing else the skill did while it was live.
func (s *Server) emitSkillRevertAudit(ctx context.Context, agentName string, reverted []agent.SkillRevision, revertErr error) {
	if s.deps.Auditor == nil {
		return
	}

	type revertedRevision struct {
		ID           int64  `json:"id"`
		Op           string `json:"op"`
		Skill        string `json:"skill"`
		TransitionID string `json:"transition_id"`
	}
	payload := struct {
		Reverted []revertedRevision `json:"reverted"`
		Error    string             `json:"error,omitempty"`
	}{Reverted: make([]revertedRevision, 0, len(reverted))}
	for _, rev := range reverted {
		payload.Reverted = append(payload.Reverted, revertedRevision{
			ID: rev.ID, Op: rev.Op, Skill: rev.SkillName, TransitionID: rev.TransitionID,
		})
	}

	status := audit.StatusOK
	summary := fmt.Sprintf("Reverted %d skill revision(s) on agent %q. This restores skill files only — messages sent, tool calls made and KV keys written while the change was live are not undone.", len(reverted), agentName)
	if revertErr != nil {
		status = audit.StatusError
		payload.Error = revertErr.Error()
		summary = fmt.Sprintf("Skill revert on agent %q failed after %d revision(s)", agentName, len(reverted))
	}
	detail, _ := json.Marshal(payload)

	s.deps.Auditor.Emit(ctx, audit.Event{
		Category: audit.CategorySkill,
		Action:   "skill_revert",
		Agent:    agentName,
		Summary:  summary,
		Detail:   string(detail),
		Status:   status,
		Source:   "mcp",
	})
}
