package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type emptyInput struct{}

type panicStatusOutput struct {
	Panicked  bool   `json:"panicked"`
	PanicTime string `json:"panic_time,omitempty"`
}

func (s *Server) registerSafetyTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "panic",
		Description: "Emergency stop: cancel all in-flight requests and pause the scheduler. " +
			"Use resume to restore normal operation. Requires 'admin' scope.",
	}, s.handlePanic)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "resume",
		Description: "Resume normal operation after a panic. Clears panic state and resumes " +
			"the scheduler. Requires 'admin' scope.",
	}, s.handleResume)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "panic_status",
		Description: "Check whether the system is in panic state. Returns panicked (bool) " +
			"and panic_time if active. Requires 'admin' scope.",
	}, s.handlePanicStatus)
}

func (s *Server) handlePanic(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "admin"); err != nil {
		return err, nil, nil
	}
	s.deps.Dispatcher.Panic()
	return toolText("panic activated — all processing paused"), nil, nil
}

func (s *Server) handleResume(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "admin"); err != nil {
		return err, nil, nil
	}
	s.deps.Dispatcher.Resume()
	return toolText("resumed — processing restored"), nil, nil
}

func (s *Server) handlePanicStatus(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	if err := requireScope(ctx, "admin"); err != nil {
		return err, nil, nil
	}

	out := panicStatusOutput{Panicked: s.deps.Dispatcher.IsPanicked()}
	if out.Panicked {
		out.PanicTime = s.deps.Dispatcher.PanicTime().Format("2006-01-02T15:04:05Z07:00")
	}

	r, err := toolJSON(out)
	return r, nil, err
}
