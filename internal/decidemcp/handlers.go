package decidemcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolName is the advertised name of the decide tool.
const ToolName = "decide"

func (s *Server) registerTools() {
	if s.deps.Resolve == nil {
		return
	}
	s.mcpServer.AddTool(&mcp.Tool{
		Name: ToolName,
		Description: fmt.Sprintf("Ask the decision model %s typed questions about a JSON `state` and "+
			"get probabilities back, instead of classifying, triaging, routing or filtering in your reply. "+
			"Each question has a `type`: `noul` answers with the probability (0-1) that the statement in "+
			"`instructions` is true; `choice` picks one of 2-255 `choices` (option id -> what it means) "+
			"and returns the probability of each; `score` rates on 2-10 ordered `levels` (worst first). "+
			"Instructions may reference state fields by path, e.g. `message.subject`. The model reads "+
			"text only, does no arithmetic and gives no explanation. Inputs over the model's size cap "+
			"are refused, not truncated: send only the state the question needs.", s.deps.DeciderName),
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"state": {"description": "The data to decide about: any JSON value or a string."},
				"questions": {
					"type": "object",
					"description": "Questions keyed by a short id of your choosing.",
					"additionalProperties": {
						"type": "object",
						"properties": {
							"type": {"type": "string", "enum": ["choice", "noul", "score"]},
							"instructions": {"type": "string", "description": "The question, as a statement for noul."},
							"choices": {"type": "object", "additionalProperties": {"type": "string"}, "description": "choice: option id -> description. noul: optional \"true\"/\"false\" criteria."},
							"levels": {"type": "array", "items": {"type": "string"}, "description": "score: level descriptions, worst to best."}
						},
						"required": ["type", "instructions"]
					}
				}
			},
			"required": ["state", "questions"]
		}`),
	}, s.handleDecide)
}

type questionArg struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Choices      map[string]string `json:"choices"`
	Levels       []string          `json:"levels"`
}

type decideResult struct {
	Answers map[string]llm.Answer `json:"answers"`
	Model   string                `json:"model"`
	CostUSD float64               `json:"cost_usd"`
}

func (s *Server) handleDecide(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input struct {
		State     json.RawMessage        `json:"state"`
		Questions map[string]questionArg `json:"questions"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if len(input.State) == 0 {
		return toolError("state is required"), nil
	}
	qs := make(map[string]llm.Question, len(input.Questions))
	for id, q := range input.Questions {
		qs[id] = llm.Question{
			Type:         llm.QuestionType(q.Type),
			Instructions: q.Instructions,
			Choices:      q.Choices,
			Levels:       q.Levels,
		}
	}

	d := s.deps.Resolve()
	if d == nil {
		return toolError(fmt.Sprintf("decide unavailable: decision model %q is not configured", s.deps.DeciderName)), nil
	}
	sessionID := s.sessionKey(d)
	// Bill the calling agent: its limits apply and its spend shows the cost.
	if ct := d.CostTracker(); ct != nil {
		ct.RegisterSessionAgent(sessionID, s.deps.AgentName)
	}
	resp, err := d.Decide(ctx, sessionID, input.State, qs)
	if err != nil {
		s.deps.Logger.Warn("decide tool failed", "agent", s.deps.AgentName, "decider", d.Name(),
			"cause", llm.DecisionErrorCause(err), "error", err)
		return toolError(describeError(err)), nil
	}

	out, err := json.Marshal(decideResult{Answers: resp.Answers, Model: resp.Model, CostUSD: resp.CostUSD})
	if err != nil {
		return toolError("encoding answers: " + err.Error()), nil
	}
	return toolText(string(out)), nil
}

// sessionKey is the cost-tracker session a call bills. A tool call carries no
// conversation id across the MCP boundary, so the key rotates daily instead:
// a per-session hard limit then bounds a day of decide spend rather than the
// agent's lifetime, after which the tool would be refused until restart.
func (s *Server) sessionKey(d *llm.Decider) string {
	return "decide:" + d.Name() + ":" + s.deps.AgentName + ":" + s.deps.Now().UTC().Format("2006-01-02")
}

// describeError turns a failed call into text the model can act on.
func describeError(err error) string {
	switch llm.DecisionErrorCause(err) {
	case "too_large":
		return "decide refused: the state and questions exceed the decider's size cap; send only the fields the questions need"
	case "cost_limit":
		return "decide refused: the agent's cost limit is reached"
	case "timeout":
		return "decide failed: the decider timed out"
	}
	return "decide failed: " + err.Error()
}

func toolText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: true}
}
