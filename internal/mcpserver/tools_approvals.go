package mcpserver

import (
	"context"
	"strings"

	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type approvalListInput struct{}

type approvalResolveInput struct {
	ID     string `json:"id" jsonschema:"Approval request ID"`
	Action string `json:"action" jsonschema:"Action: approve or deny"`
}

func (s *Server) registerApprovalTools() {
	if s.deps.Approvals == nil {
		return
	}

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "approval_list",
		Description: "List pending approval requests. Returns ID, agent, kind, and summary " +
			"for each pending request. Requires 'approvals:read' scope.",
	}, s.handleApprovalList)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "approval_resolve",
		Description: "Approve or deny a pending approval request by ID. " +
			"Action must be 'approve' or 'deny'. Requires 'approvals:write' scope.",
	}, s.handleApprovalResolve)
}

func (s *Server) handleApprovalList(ctx context.Context, _ *mcp.CallToolRequest, _ approvalListInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "approvals:read"); err != nil {
		return err, nil, nil
	}

	reqs, err := s.deps.Approvals.List(ctx, approval.StatusPending)
	if err != nil {
		return toolError("listing approvals: " + err.Error()), nil, nil
	}

	type approvalSummary struct {
		ID      string `json:"id"`
		Agent   string `json:"agent"`
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
	}

	result := make([]approvalSummary, len(reqs))
	for i, r := range reqs {
		result[i] = approvalSummary{
			ID:      r.ID,
			Agent:   r.AgentName,
			Kind:    string(r.Kind),
			Summary: r.Summary,
		}
	}

	r, jsonErr := toolJSON(result)
	return r, nil, jsonErr
}

func (s *Server) handleApprovalResolve(ctx context.Context, _ *mcp.CallToolRequest, input approvalResolveInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "approvals:write"); err != nil {
		return err, nil, nil
	}

	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action != "approve" && action != "deny" {
		return toolError("action must be 'approve' or 'deny'"), nil, nil
	}

	_, err := s.deps.Approvals.Resolve(ctx, input.ID, action == "approve", "mcp:"+keyNameFromCtx(ctx))
	if err != nil {
		return toolError("resolve failed: " + err.Error()), nil, nil
	}

	return toolText("approval " + action + "d"), nil, nil
}
