package scriptmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
				"input": {"description": "JSON data bound to the variable ` + "`input`" + ` inside the snippet. Pass the value itself (object, array, string, number), not a JSON-encoded string of it. ` + "`code`" + ` is a separate top-level argument, not a key inside ` + "`input`" + `."}
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
	if s.deps.MaxInputBytes > 0 && len(input.Input) > s.deps.MaxInputBytes {
		return toolError(fmt.Sprintf("input exceeds %d bytes", s.deps.MaxInputBytes)), nil
	}
	args, err := normalizeArgs(input.Code, input.Input)
	if err != nil {
		return toolError(err.Error()), nil
	}
	// Decoding can grow input (invalid UTF-8 becomes U+FFFD), so check again.
	if s.deps.MaxInputBytes > 0 && len(args.input) > s.deps.MaxInputBytes {
		return toolError(fmt.Sprintf("input exceeds %d bytes", s.deps.MaxInputBytes)), nil
	}

	release, errResult := s.acquireSlot(ctx)
	if errResult != nil {
		return errResult, nil
	}
	defer release()

	vm := goja.New()
	// Bind the raw JSON as a string and parse it inside the VM, which keeps
	// nested data predictable rather than relying on Go→JS value coercion.
	if err := vm.Set("__denkeeper_input_json", string(args.input)); err != nil {
		return toolError("failed to bind input: " + err.Error()), nil
	}

	// Wall-clock guard: interrupt the VM after the timeout. Honors ctx cancellation too.
	done := make(chan struct{})
	// Closed on return, not after RunString: formatting the result runs script
	// JS too, and the ctx watcher must still be able to interrupt it.
	defer close(done)
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
	wrapped := "(function(input){" + args.code + "})(JSON.parse(__denkeeper_input_json))"
	val, err := vm.RunString(wrapped)
	return s.formatRun(val, err, args.decoded), nil
}

// formatRun builds the tool result. Exception.Error() and Value.Export() run
// script JS (toString, getters) after RunString has returned, outside goja's
// exception handling, on a goroutine with no recover. A panic there would kill
// the process, so it is recovered here. Never format the recovered value: a
// *goja.Exception's Error() would run the same script JS again.
func (s *Server) formatRun(val goja.Value, runErr error, decoded bool) (res *mcp.CallToolResult) {
	defer func() {
		if r := recover(); r != nil {
			s.deps.Logger.Warn("run_javascript: formatting the result panicked", "panic_type", fmt.Sprintf("%T", r))
			res = toolError("javascript error: the script's error or result could not be converted to text")
		}
	}()
	limit := s.deps.MaxOutputChars
	if runErr != nil {
		// goja surfaces JS exceptions and interrupts as Go errors; feed back to the LLM.
		return toolError(scriptErrorText(runErr, decoded, limit))
	}
	out, err := exportResult(val)
	if err != nil {
		return toolError(capOutput(err.Error(), limit))
	}
	return toolText(capOutput(out, limit))
}

// runArgs holds the arguments after normalizeArgs.
type runArgs struct {
	code    string
	input   json.RawMessage
	decoded bool // input arrived as a JSON-encoded string and was decoded
}

// normalizeArgs repairs two argument shapes models send (#607): `code` nested
// inside `input`, and `input` double-encoded as a JSON string. It returns an
// error whose text names the fix when the shape cannot be repaired.
func normalizeArgs(code string, raw json.RawMessage) (runArgs, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	args := runArgs{code: code, input: raw}
	decodedInput, decodeErr := decodeStringInput(raw)
	if decodedInput != nil {
		args.input, args.decoded = decodedInput, true
	}
	if args.code == "" {
		var badKey bool
		args.code, args.input, badKey = hoistCode(args.input)
		if args.code == "" {
			return args, missingCodeError(badKey)
		}
	}
	if decodeErr != nil {
		return args, decodeErr
	}
	return args, nil
}

// decodeStringInput returns the decoded bytes when raw is a JSON string that
// holds an object or array. A string that starts with { or [ but does not
// decode returns an error; any other value returns nil, nil and stays as sent.
func decodeStringInput(raw json.RawMessage) (json.RawMessage, error) {
	var str string
	if json.Unmarshal(raw, &str) != nil {
		return nil, nil // not a JSON string
	}
	trimmed := strings.TrimSpace(str)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		pos := ""
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			pos = fmt.Sprintf(" at byte %d", syn.Offset)
		}
		return nil, fmt.Errorf("`input` must be passed as a JSON value, not a string: "+
			"the string does not parse as JSON%s: %w", pos, err)
	}
	return json.RawMessage(trimmed), nil
}

// hoistCode moves a non-empty string `code` key out of a JSON object. It
// returns the code, the object without that key (other keys keep their
// order), and badKey when a `code` key exists but is not a non-empty string.
func hoistCode(raw json.RawMessage) (code string, rest json.RawMessage, badKey bool) {
	codeVal, rest, ok := splitObjectKey(raw, "code")
	if !ok || codeVal == nil {
		return "", raw, false
	}
	if json.Unmarshal(codeVal, &code) != nil || code == "" {
		return "", raw, true
	}
	return code, rest, false
}

// splitObjectKey returns the value under key and the object without it. ok is
// false when raw is not a JSON object. With duplicate keys the last value wins,
// matching encoding/json. Kept keys and values are copied as raw bytes:
// re-encoding a key can grow it past max_input_bytes ('<' becomes <).
func splitObjectKey(raw json.RawMessage, key string) (val, rest json.RawMessage, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, nil, false
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for dec.More() {
		start := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, false
		}
		// Only a comma and whitespace can precede the key's opening quote.
		keyRaw := raw[start:dec.InputOffset()]
		keyRaw = keyRaw[bytes.IndexByte(keyRaw, '"'):]
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, false
		}
		if k, _ := tok.(string); k == key {
			val = v
			continue
		}
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		out.Write(keyRaw)
		out.WriteByte(':')
		out.Write(v)
	}
	out.WriteByte('}')
	return val, out.Bytes(), true
}

func missingCodeError(badKey bool) error {
	msg := "code is required: pass `code` as a top-level string argument, next to `input`, not inside it"
	if badKey {
		msg += "; `input.code` was found but is not a non-empty string"
	}
	return errors.New(msg)
}

// scriptErrorText formats a script failure. After a decoded string input, a
// script that throws most likely still calls JSON.parse(input) on the object.
// The script controls err.Error() (`throw input`), so it is capped; the hint is not.
func scriptErrorText(err error, decoded bool, limit int) string {
	text := "javascript error: " + capOutput(err.Error(), limit)
	var interrupted *goja.InterruptedError
	if decoded && !errors.As(err, &interrupted) {
		text += "\n`input` was sent as a JSON-encoded string and was decoded to an object " +
			"or array before the script ran; remove JSON.parse(input) and use `input` directly."
	}
	return text
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

// exportResult renders a goja value: strings pass through as-is; everything
// else is JSON. There is no %v fallback: on a cyclic value it recurses until
// the runtime aborts with a stack overflow, which recover cannot catch.
func exportResult(val goja.Value) (string, error) {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return "", nil
	}
	exported := val.Export()
	if str, ok := exported.(string); ok {
		return str, nil
	}
	b, err := json.Marshal(exported)
	if err != nil {
		return "", fmt.Errorf("result could not be encoded as JSON: %w", err)
	}
	return string(b), nil
}

// capOutput cuts s to at most limit bytes, backing up to a rune boundary, and
// appends truncationMarker. limit <= 0 means no cap.
func capOutput(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	n := limit
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + truncationMarker(limit)
}

// truncationMarker follows text cut by capOutput, so the model knows it was cut.
func truncationMarker(limit int) string {
	return fmt.Sprintf("\n[truncated: over max_output_chars (%d bytes)]", limit)
}

func toolText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: true}
}
