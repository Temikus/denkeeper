package configmcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/approval"
)

// registerBrowserTools adds the four browser profile MCP tools to the server.
// Called from registerTools when a BrowserProfiles service is available.
func (s *Server) registerBrowserTools() {
	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "browser_profile_list",
		Description: "List all browser profiles. Returns agent name, size, domain count, and last used time for each profile.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}, s.handleBrowserProfileList)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "browser_profile_info",
		Description: "Get detailed information about a browser profile including cookie domains, storage size, and last used time.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"agent": {"type": "string", "description": "Agent name whose profile to inspect. Defaults to your own agent name if omitted."}
			}
		}`),
	}, s.handleBrowserProfileInfo)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "browser_profile_clear",
		Description: "Clear all browser state (cookies, localStorage, cache) for a profile. The empty profile directory is preserved so the browser can reuse it immediately. In supervised mode this requires approval.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"agent": {"type": "string", "description": "Agent name whose profile to clear. Defaults to your own agent name if omitted."}
			}
		}`),
	}, s.handleBrowserProfileClear)

	s.mcpServer.AddTool(&mcp.Tool{
		Name:        "browser_profile_delete",
		Description: "Permanently delete a browser profile directory and all its data. Always requires approval regardless of permission tier.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"agent": {"type": "string", "description": "Agent name whose profile to delete. Defaults to your own agent name if omitted."}
			}
		}`),
	}, s.handleBrowserProfileDelete)
}

func (s *Server) handleBrowserProfileList(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	profiles, err := s.deps.BrowserProfiles.List(ctx)
	if err != nil {
		return toolError(fmt.Sprintf("browser_profile_list failed: %v", err)), nil
	}
	resp, _ := json.Marshal(map[string]any{"profiles": profiles})
	return toolText(string(resp)), nil
}

func (s *Server) handleBrowserProfileInfo(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agent := s.resolveAgent(req)
	info, err := s.deps.BrowserProfiles.Info(ctx, agent)
	if err != nil {
		return toolError(fmt.Sprintf("browser_profile_info failed: %v", err)), nil
	}
	resp, _ := json.Marshal(info)
	return toolText(string(resp)), nil
}

func (s *Server) handleBrowserProfileClear(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("browser_profile_clear is not available in restricted mode"), nil
	}

	agent := s.resolveAgent(req)
	summary := fmt.Sprintf("Clear browser profile for agent %q", agent)

	applyFn := approval.ActionFunc(func(ctx context.Context, _ string) error {
		return s.deps.BrowserProfiles.Clear(ctx, agent)
	})

	return applyOrSubmit(ctx, s.deps, approval.ActionKindBrowserProfile, summary, agent, applyFn)
}

func (s *Server) handleBrowserProfileDelete(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tier := s.deps.PermissionTier()
	if tier == "restricted" {
		return toolError("browser_profile_delete is not available in restricted mode"), nil
	}

	agent := s.resolveAgent(req)
	summary := fmt.Sprintf("Delete browser profile for agent %q", agent)

	applyFn := approval.ActionFunc(func(ctx context.Context, _ string) error {
		return s.deps.BrowserProfiles.Delete(ctx, agent)
	})

	// Delete always requires approval regardless of tier.
	if s.deps.Approvals == nil {
		// No approval manager — execute immediately as fallback.
		if err := applyFn(ctx, agent); err != nil {
			return toolError(fmt.Sprintf("action failed: %v", err)), nil
		}
		return toolText("Done: " + summary), nil
	}

	_, submitErr := s.deps.Approvals.Submit(
		ctx,
		s.deps.AgentName,
		approval.ActionKindBrowserProfile,
		summary,
		agent,
		"", // externalID
		"", // adapterName
		"", // conversationID
		applyFn,
	)
	if submitErr != nil {
		return toolError(fmt.Sprintf("approval submit failed: %v", submitErr)), nil
	}
	return toolText("Submitted for approval: " + summary), nil
}

// resolveAgent extracts the "agent" field from the request arguments,
// defaulting to the server's own agent name if omitted.
func (s *Server) resolveAgent(req *mcp.CallToolRequest) string {
	if req.Params.Arguments != nil {
		var input struct {
			Agent string `json:"agent"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &input); err == nil && input.Agent != "" {
			return input.Agent
		}
	}
	return s.deps.AgentName
}
