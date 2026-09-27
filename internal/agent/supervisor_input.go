package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Temikus/denkeeper/internal/agentctx"
	"github.com/Temikus/denkeeper/internal/llm"
)

// supervisorReviewInput is everything a tool-call review is judged on. It is
// gathered once per call and rendered twice — markdown for the LLM supervisor,
// JSON state for the decider — so the two views cannot drift.
type supervisorReviewInput struct {
	agent           string
	tool            string
	toolDescription string // truncated to supervisorToolDescLen
	toolGuidance    string // verbatim; load caps it at config.MaxToolGuidanceBytes
	arguments       string
	skill           *agentctx.SkillSummary
	bodyExcerptLen  int
	hasUserRequest  bool
	userRequest     string // last user message in recent, truncated
	recent          []StoredMessage
}

const (
	supervisorUserRequestLen = 500
	supervisorRecentMsgLen   = 200
)

func (e *Engine) gatherSupervisorInput(ctx context.Context, tc llm.ToolCall, convID string) *supervisorReviewInput {
	in := &supervisorReviewInput{
		agent:          e.name,
		tool:           tc.Function.Name,
		arguments:      tc.Function.Arguments,
		skill:          agentctx.SkillContext(ctx),
		bodyExcerptLen: e.supervisorBodyExcerptLen,
	}
	if e.tools != nil {
		if desc := e.tools.ToolDescription(in.tool); desc != "" {
			in.toolDescription = truncateForSupervisor(desc, e.supervisorToolDescLen)
		}
		in.toolGuidance = e.tools.GuidanceForTool(in.tool)
	}

	recent, err := e.memory.GetMessages(ctx, convID, e.supervisorContextMessages)
	if err != nil {
		// Proceed without context rather than blocking.
		e.logger.Warn("supervisor: failed to load conversation context", "error", err)
		recent = nil
	}
	in.recent = recent
	for i := len(recent) - 1; i >= 0; i-- {
		if recent[i].Role == "user" {
			in.hasUserRequest = true
			in.userRequest = truncateForSupervisor(recent[i].Content, supervisorUserRequestLen)
			break
		}
	}
	return in
}

// markdown renders the LLM supervisor's review prompt.
func (in *supervisorReviewInput) markdown() string {
	var b strings.Builder
	b.WriteString("## Tool Call Review Request\n\n")
	fmt.Fprintf(&b, "**Agent**: %s\n", in.agent)
	fmt.Fprintf(&b, "**Tool**: %s\n", in.tool)
	if in.toolDescription != "" {
		fmt.Fprintf(&b, "**Tool description**: %s\n", in.toolDescription)
	}
	// Verbatim, not truncated: guidance is the operator's argument rules —
	// the reviewer approving a fabricated ID is the failure this exists to
	// catch.
	if in.toolGuidance != "" {
		fmt.Fprintf(&b, "**Operator guidance for this tool's server** (arguments must conform):\n%s\n", in.toolGuidance)
	}
	fmt.Fprintf(&b, "**Arguments**:\n```json\n%s\n```\n\n", in.arguments)

	if in.skill != nil {
		writeSupervisorSkillContext(&b, in.skill, in.bodyExcerptLen)
	}

	if len(in.recent) > 0 {
		if in.hasUserRequest {
			fmt.Fprintf(&b, "**User's request**: %q\n\n", in.userRequest)
		}
		fmt.Fprintf(&b, "**Recent conversation** (last %d messages):\n", len(in.recent))
		for _, m := range in.recent {
			fmt.Fprintf(&b, "- [%s]: %s\n", m.Role, truncateForSupervisor(m.Content, supervisorRecentMsgLen))
		}
		b.WriteString("\n")
	}

	writeSupervisorEvalCriteria(&b, in.skill)
	return b.String()
}

// deciderState is the decider's view of the review. Questions reference its
// fields by path (`tool.arguments`, `user_request`, `skill.description`).
type deciderState struct {
	Agent          string           `json:"agent"`
	Tool           deciderStateTool `json:"tool"`
	UserRequest    string           `json:"user_request,omitempty"`
	Skill          *deciderSkill    `json:"skill,omitempty"`
	RecentMessages []deciderMessage `json:"recent_messages,omitempty"`
}

type deciderStateTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Guidance    string          `json:"guidance,omitempty"`
	Arguments   json.RawMessage `json:"arguments"`
}

type deciderSkill struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Instructions string `json:"instructions_excerpt,omitempty"`
	Scheduled    bool   `json:"scheduled"`
	Schedule     string `json:"schedule,omitempty"`
}

type deciderMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (in *supervisorReviewInput) deciderState() deciderState {
	s := deciderState{
		Agent: in.agent,
		Tool: deciderStateTool{
			Name:        in.tool,
			Description: in.toolDescription,
			Guidance:    in.toolGuidance,
			Arguments:   rawJSONOrString(in.arguments),
		},
		UserRequest: in.userRequest,
	}
	if sk := in.skill; sk != nil {
		s.Skill = &deciderSkill{
			Name:        sk.Name,
			Description: sk.Description,
			Scheduled:   sk.IsScheduled,
			Schedule:    sk.ScheduleName,
		}
		if in.bodyExcerptLen > 0 {
			s.Skill.Instructions = truncateForSupervisor(sk.Body, in.bodyExcerptLen)
		}
	}
	for _, m := range in.recent {
		s.RecentMessages = append(s.RecentMessages, deciderMessage{
			Role:    m.Role,
			Content: truncateForSupervisor(m.Content, supervisorRecentMsgLen),
		})
	}
	return s
}

// rawJSONOrString embeds valid JSON as-is so `tool.arguments.<field>` paths
// resolve, and anything else as a JSON string.
func rawJSONOrString(s string) json.RawMessage {
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	b, _ := json.Marshal(s)
	return b
}
