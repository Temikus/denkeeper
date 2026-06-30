package scriptmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) registerTools() {
	if !s.deps.Enabled {
		return
	}
	s.mcpServer.AddTool(&mcp.Tool{
		Name: "run_javascript",
		Description: "Run a short JavaScript snippet to transform, format, classify, " +
			"count, or bucket data deterministically — instead of computing it in your " +
			"reply. The JSON you pass as `input` is available as the variable `input`. " +
			"Use `return` to produce the result (a string, or an object/array which is " +
			"returned as JSON). No network or filesystem access is available. " +
			"Target ECMAScript 5.1-compatible syntax.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"code": {"type": "string", "description": "JavaScript to execute. Use return to produce the result."},
				"input": {"description": "JSON data bound to the variable ` + "`input`" + ` inside the snippet."}
			},
			"required": ["code"]
		}`),
	}, s.handleRunJavaScript)
}

func (s *Server) handleRunJavaScript(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.deps.PermissionTier() == "restricted" {
		return toolError("run_javascript is not available in restricted mode"), nil
	}

	var input struct {
		Code  string          `json:"code"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return toolError("invalid arguments: " + err.Error()), nil
	}
	if input.Code == "" {
		return toolError("code is required"), nil
	}
	if s.deps.MaxInputBytes > 0 && len(input.Input) > s.deps.MaxInputBytes {
		return toolError(fmt.Sprintf("input exceeds %d bytes", s.deps.MaxInputBytes)), nil
	}
	if len(input.Input) == 0 {
		input.Input = json.RawMessage("null")
	}

	release, errResult := s.acquireSlot(ctx)
	if errResult != nil {
		return errResult, nil
	}
	defer release()

	vm := goja.New()
	// Bind the raw JSON as a string and parse it inside the VM, which keeps
	// nested data predictable rather than relying on Go→JS value coercion.
	if err := vm.Set("__denkeeper_input_json", string(input.Input)); err != nil {
		return toolError("failed to bind input: " + err.Error()), nil
	}

	// Wall-clock guard: interrupt the VM after the timeout. Honors ctx cancellation too.
	done := make(chan struct{})
	timer := time.AfterFunc(s.deps.Timeout, func() { vm.Interrupt("execution timed out") })
	defer timer.Stop()
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt("context cancelled")
		case <-done:
		}
	}()

	// Wrap user code in an IIFE so `return` works and `input` is scoped.
	wrapped := "(function(input){" + input.Code + "})(JSON.parse(__denkeeper_input_json))"
	val, err := vm.RunString(wrapped)
	close(done)
	if err != nil {
		// goja surfaces JS exceptions and interrupts as Go errors; feed back to the LLM.
		return toolError("javascript error: " + err.Error()), nil
	}

	out := exportResult(val)
	if s.deps.MaxOutputChars > 0 && len(out) > s.deps.MaxOutputChars {
		out = out[:s.deps.MaxOutputChars]
	}
	return toolText(out), nil
}

// acquireSlot bounds concurrency before a VM is allocated. A fresh goja VM has
// no heap cap and shares the host process heap, so without this N concurrent
// snippets multiply allocation toward an OOM that would take down the whole
// single-process binary. It acquires the per-agent slot then the global slot
// (fixed order across all callers, so no deadlock; released in reverse),
// blocking until both free or ctx is cancelled. Returns a release func (call
// when done) or, on cancellation, an error result.
func (s *Server) acquireSlot(ctx context.Context) (func(), *mcp.CallToolResult) {
	relAgent, errResult := acquireOne(ctx, s.deps.AgentSem)
	if errResult != nil {
		return nil, errResult
	}
	relGlobal, errResult := acquireOne(ctx, s.deps.Sem)
	if errResult != nil {
		relAgent() // release the per-agent slot already held
		return nil, errResult
	}
	return func() { relGlobal(); relAgent() }, nil
}

// acquireOne blocks for a single semaphore slot or returns an error result if
// ctx is cancelled first. A nil semaphore is treated as unbounded.
func acquireOne(ctx context.Context, sem chan struct{}) (func(), *mcp.CallToolResult) {
	if sem == nil {
		return func() {}, nil
	}
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, toolError("run_javascript cancelled while waiting for a concurrency slot: " + ctx.Err().Error())
	}
}

// exportResult renders a goja value: strings pass through as-is; everything else is JSON.
func exportResult(val goja.Value) string {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return ""
	}
	exported := val.Export()
	if str, ok := exported.(string); ok {
		return str
	}
	b, err := json.Marshal(exported)
	if err != nil {
		return fmt.Sprintf("%v", exported)
	}
	return string(b)
}

func toolText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: true}
}
