package agent

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/llm"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Binding maps an adapter pattern to an agent name.
// Pattern is either a wildcard ("telegram") or specific ("telegram:12345").
type Binding struct {
	Pattern   string // "telegram" or "telegram:12345"
	AgentName string
}

// Dispatcher routes incoming messages to the correct agent Engine based on
// adapter bindings. It owns the adapter lifecycle and the shared incoming channel.
type Dispatcher struct {
	mu       sync.RWMutex
	agents   map[string]*Engine         // agent name → engine
	specific map[string]string          // "adapter:externalID" → agent name
	wildcard map[string]string          // "adapter" → agent name
	adapters map[string]adapter.Adapter // adapter name → adapter instance
	incoming chan adapter.IncomingMessage
	logger   *slog.Logger

	// OnBroadcast, when set, is called after an adapter message (Telegram,
	// Discord, etc.) is successfully processed. The server uses this to
	// notify WebSocket clients of conversation activity from other adapters.
	OnBroadcast func(agentName, convID, adapterName, summary string)

	// OTel instrumentation.
	tracer    trace.Tracer
	mDispatch metric.Int64Counter
}

// NewDispatcher creates a Dispatcher from a set of named engines, bindings,
// and adapters. Bindings are processed in order; specific bindings
// ("telegram:12345") take priority over wildcard bindings ("telegram").
func NewDispatcher(
	agents map[string]*Engine,
	bindings []Binding,
	adapters []adapter.Adapter,
	logger *slog.Logger,
) *Dispatcher {
	specific := make(map[string]string)
	wildcard := make(map[string]string)

	for _, b := range bindings {
		if strings.Contains(b.Pattern, ":") {
			specific[b.Pattern] = b.AgentName
		} else {
			wildcard[b.Pattern] = b.AgentName
		}
	}

	adapterMap := make(map[string]adapter.Adapter, len(adapters))
	for _, a := range adapters {
		adapterMap[a.Name()] = a
	}

	meter := otel.Meter("denkeeper.dispatcher")
	tracer := otel.Tracer("denkeeper.dispatcher")
	dispatch, _ := meter.Int64Counter("denkeeper.dispatch",
		metric.WithDescription("Messages dispatched to agents"))

	return &Dispatcher{
		agents:    agents,
		specific:  specific,
		wildcard:  wildcard,
		adapters:  adapterMap,
		incoming:  make(chan adapter.IncomingMessage, 64),
		tracer:    tracer,
		mDispatch: dispatch,
		logger:    logger,
	}
}

// resolveAgent finds the Engine that should handle the given message.
// Priority: specific binding > wildcard binding > "default" agent.
func (d *Dispatcher) resolveAgent(msg adapter.IncomingMessage) *Engine {
	d.mu.RLock()
	defer d.mu.RUnlock()

	key := msg.Adapter + ":" + msg.ExternalID
	if name, ok := d.specific[key]; ok {
		if e, ok := d.agents[name]; ok {
			return e
		}
	}
	if name, ok := d.wildcard[msg.Adapter]; ok {
		if e, ok := d.agents[name]; ok {
			return e
		}
	}
	return d.agents["default"]
}

// SendFor returns a SendFunc that routes outgoing messages through the
// adapter matching the incoming message's adapter name.
func (d *Dispatcher) SendFor(adapterName string) SendFunc {
	return func(ctx context.Context, msg adapter.OutgoingMessage) error {
		a, ok := d.adapters[adapterName]
		if !ok {
			return fmt.Errorf("no adapter %q registered", adapterName)
		}
		return a.Send(ctx, msg)
	}
}

// Dispatch sends a message to a specific agent by name. Used by the scheduler.
func (d *Dispatcher) Dispatch(ctx context.Context, agentName string, msg adapter.IncomingMessage) error {
	d.mu.RLock()
	e, ok := d.agents[agentName]
	d.mu.RUnlock()
	if !ok {
		return fmt.Errorf("agent %q not found", agentName)
	}
	return e.HandleMessage(ctx, msg)
}

// SendVia sends a message through the adapter registered under adapterName.
// Returns an error if no adapter with that name is registered.
func (d *Dispatcher) SendVia(ctx context.Context, adapterName string, msg adapter.OutgoingMessage) error {
	return d.SendFor(adapterName)(ctx, msg)
}

// Agents returns the names of all registered agents.
func (d *Dispatcher) Agents() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	names := make([]string, 0, len(d.agents))
	for name := range d.agents {
		names = append(names, name)
	}
	return names
}

// Agent returns the Engine for the named agent, or nil if not found.
func (d *Dispatcher) Agent(name string) *Engine {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.agents[name]
}

// RenameAgent atomically renames an agent, updating all routing maps.
func (d *Dispatcher) RenameAgent(oldName, newName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	e, ok := d.agents[oldName]
	if !ok {
		return fmt.Errorf("agent %q not found", oldName)
	}
	if _, exists := d.agents[newName]; exists {
		return fmt.Errorf("agent %q already exists", newName)
	}

	d.agents[newName] = e
	delete(d.agents, oldName)

	for k, v := range d.specific {
		if v == oldName {
			d.specific[k] = newName
		}
	}
	for k, v := range d.wildcard {
		if v == oldName {
			d.wildcard[k] = newName
		}
	}

	e.SetName(newName)
	return nil
}

// ListModels returns available LLM models by querying the default agent's router.
func (d *Dispatcher) ListModels(ctx context.Context) []string {
	if e := d.agents["default"]; e != nil {
		return e.ListModels(ctx)
	}
	// Fall back to first available agent.
	for _, e := range d.agents {
		return e.ListModels(ctx)
	}
	return nil
}

// ListModelDetails returns enriched model metadata by querying the default agent's router.
// When providerFilter is non-empty only the named provider is queried.
func (d *Dispatcher) ListModelDetails(ctx context.Context, providerFilter string) []llm.ModelInfo {
	if e := d.agents["default"]; e != nil {
		return e.ListModelDetails(ctx, providerFilter)
	}
	for _, e := range d.agents {
		return e.ListModelDetails(ctx, providerFilter)
	}
	return nil
}

// Run starts all adapters and processes incoming messages until ctx is cancelled.
// Each message is handled in its own goroutine so slow LLM calls do not block
// the dispatch loop.
func (d *Dispatcher) Run(ctx context.Context) error {
	var adapterWg sync.WaitGroup
	for _, a := range d.adapters {
		a := a
		adapterWg.Add(1)
		go func() {
			defer adapterWg.Done()
			if err := a.Start(ctx, d.incoming); err != nil && ctx.Err() == nil {
				d.logger.Error("adapter stopped with error", "adapter", a.Name(), "error", err)
			}
		}()
	}

	if len(d.adapters) == 0 {
		d.logger.Warn("dispatcher started with no adapters — messages can only arrive via API/WebSocket")
	}
	d.logger.Info("dispatcher started", "agents", len(d.agents), "adapters", len(d.adapters))

	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("dispatcher shutting down, waiting for in-flight messages")
			wg.Wait()

			// Wait for adapter goroutines to finish, then drain
			// any straggling messages so they don't block on send.
			adapterWg.Wait()
			for len(d.incoming) > 0 {
				<-d.incoming
			}

			return ctx.Err()
		case msg := <-d.incoming:
			e := d.resolveAgent(msg)
			if e == nil {
				d.logger.Warn("no agent found for message, dropping", "adapter", msg.Adapter, "external_id", msg.ExternalID)
				continue
			}
			d.mDispatch.Add(ctx, 1, metric.WithAttributes(
				attribute.String("adapter", msg.Adapter),
				attribute.String("agent", e.Name())))

			wg.Add(1)
			go d.handleMessage(ctx, &wg, e, msg)
		}
	}
}

// handleMessage processes a single incoming message. It runs the engine
// pipeline and sends an error message back to the user if the pipeline fails.
func (d *Dispatcher) handleMessage(ctx context.Context, wg *sync.WaitGroup, e *Engine, msg adapter.IncomingMessage) {
	defer wg.Done()

	msgCtx, span := d.tracer.Start(ctx, "dispatcher.route",
		trace.WithAttributes(
			attribute.String("adapter", msg.Adapter),
			attribute.String("agent", e.Name())))
	defer span.End()

	// Keep the typing indicator alive for the entire duration of processing.
	// Telegram's sendChatAction expires after ~5s, so we resend every 4s.
	stopTyping := d.startTypingTicker(msgCtx, msg)
	defer stopTyping()

	onEvent := d.buildEventHandler(msgCtx, msg)
	if err := e.HandleMessageWithEvents(msgCtx, msg, onEvent); err != nil {
		d.logger.Error("handling message", "error", err, "agent", e.Name(), "adapter", msg.Adapter, "user", msg.UserName)
		span.RecordError(err)
		d.sendErrorFeedback(msgCtx, msg)
		return
	}

	// Notify WebSocket clients of adapter activity so the web UI can
	// refresh its session list or reload the active conversation.
	if d.OnBroadcast != nil && msg.Adapter != "ws" && msg.Adapter != "api" {
		convID := e.Name() + ":" + msg.Adapter + ":" + msg.ExternalID
		d.OnBroadcast(e.Name(), convID, msg.Adapter, "New message processed")
	}
}

// typingInterval is the interval at which the typing indicator is refreshed.
// Telegram's sendChatAction expires after ~5s; we resend at 4s to stay visible.
// Declared as a var so tests can override it.
var typingInterval = 4 * time.Second

// startTypingTicker spawns a goroutine that sends typing indicators every 4s
// until the returned stop function is called. This keeps the indicator alive
// for the full duration of message processing.
func (d *Dispatcher) startTypingTicker(ctx context.Context, msg adapter.IncomingMessage) (stop func()) {
	a, ok := d.adapters[msg.Adapter]
	if !ok {
		return func() {}
	}

	ticker := time.NewTicker(typingInterval)
	done := make(chan struct{})

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = a.SendTyping(ctx, msg.ExternalID)
			}
		}
	}()

	return func() { close(done) }
}

// buildEventHandler returns a ChatEventFunc that refreshes typing indicators,
// updates the activity log, and renders approval prompts for a single message.
func (d *Dispatcher) buildEventHandler(ctx context.Context, msg adapter.IncomingMessage) ChatEventFunc {
	a, aOK := d.adapters[msg.Adapter]
	if !aOK {
		return func(ChatEvent) {} // no adapter — no-op
	}

	debug := false
	if dc, ok := a.(adapter.DebugChecker); ok {
		debug = dc.IsDebugByExternalID(msg.ExternalID)
	}

	var alog *activityLog
	if !debug {
		if me, ok := a.(adapter.MessageEditor); ok {
			alog = &activityLog{editor: me, externalID: msg.ExternalID, adapter: msg.Adapter, logger: d.logger}
		}
	}

	return func(evt ChatEvent) {
		defer func() {
			if r := recover(); r != nil {
				d.logger.Error("panic in onEvent callback", "recover", r, "adapter", msg.Adapter, "event", evt.Type)
			}
		}()

		switch evt.Type {
		case "thinking":
			_ = a.SendTyping(ctx, msg.ExternalID)
		case "tool_start":
			_ = a.SendTyping(ctx, msg.ExternalID)
			if alog != nil {
				alog.toolStart(ctx, evt.Tool)
			}
		case "tool_end":
			if alog != nil {
				alog.toolEnd(ctx, evt.Tool, evt.Duration, evt.Error)
			}
		case "tool_approval":
			d.handleToolApproval(ctx, a, msg, evt, debug, alog)
		}
	}
}

// handleToolApproval processes a tool_approval ChatEvent, choosing between
// debug (verbose) and compact (activity log / expandable blockquote) rendering.
func (d *Dispatcher) handleToolApproval(ctx context.Context, a adapter.Adapter, msg adapter.IncomingMessage, evt ChatEvent, debug bool, alog *activityLog) {
	if evt.ApprovalStatus == "auto_approved" {
		if debug {
			_ = a.Send(ctx, adapter.OutgoingMessage{
				Text:       fmt.Sprintf("Tool **%s** auto-approved", evt.Tool),
				ExternalID: msg.ExternalID,
				Adapter:    msg.Adapter,
			})
		} else if alog != nil {
			alog.autoApproved(ctx, evt.Tool)
		}
		return
	}

	if debug {
		_ = a.Send(ctx, adapter.OutgoingMessage{
			Text:       fmt.Sprintf("Agent wants to execute tool **%s**\n\n```\n%s\n```\n\nApprove?", evt.Tool, evt.Text),
			ExternalID: msg.ExternalID,
			Adapter:    msg.Adapter,
			Buttons: []adapter.KeyboardButton{
				{Label: "✅ Approve", CallbackData: evt.ApprovalCallback + ":approve"},
				{Label: "❌ Deny", CallbackData: evt.ApprovalCallback + ":deny"},
				{Label: "🔄 Auto (15 min)", CallbackData: evt.ApprovalCallback + ":approve_session"},
				{Label: "♾️ Auto (always)", CallbackData: evt.ApprovalCallback + ":approve_always"},
			},
		})
		return
	}

	compactText := fmt.Sprintf(
		"🔧 <b>%s</b> — approve?\n<blockquote expandable>%s</blockquote>",
		html.EscapeString(evt.Tool),
		html.EscapeString(evt.Text),
	)
	_ = a.Send(ctx, adapter.OutgoingMessage{
		Text:       compactText,
		ParseMode:  "HTML",
		ExternalID: msg.ExternalID,
		Adapter:    msg.Adapter,
		Buttons: []adapter.KeyboardButton{
			{Label: "✅ Approve", CallbackData: evt.ApprovalCallback + ":approve"},
			{Label: "❌ Deny", CallbackData: evt.ApprovalCallback + ":deny"},
			{Label: "🔄 15 min", CallbackData: evt.ApprovalCallback + ":approve_session"},
			{Label: "♾️ Always", CallbackData: evt.ApprovalCallback + ":approve_always"},
		},
		ButtonLayout: []int{2, 2},
	})
}

// sendErrorFeedback attempts to notify the user that their message could not be
// processed. This prevents the silent-failure scenario where the user sends a
// message and never receives any response.
func (d *Dispatcher) sendErrorFeedback(ctx context.Context, msg adapter.IncomingMessage) {
	a, ok := d.adapters[msg.Adapter]
	if !ok {
		return
	}
	_ = a.Send(ctx, adapter.OutgoingMessage{
		Adapter:    msg.Adapter,
		ExternalID: msg.ExternalID,
		Text:       "Sorry, I encountered an error processing your message. Please try again.",
	})
}

// activityLog accumulates tool events into a single Telegram message that is
// edited in-place as new events arrive. This replaces the pattern of sending
// separate messages for each tool_start, tool_end, and auto_approved event.
//
// The message uses HTML formatting and looks like:
//
//	🔧 search_web — auto-approved
//	🔧 fetch_url ✅ 340ms
//	🔧 read_file ⏳
type activityLog struct {
	editor     adapter.MessageEditor
	externalID string
	adapter    string
	logger     *slog.Logger
	messageID  string         // platform message ID, empty until first send
	lines      []activityLine // ordered entries
	toolIndex  map[string]int // tool name → index in lines (for in-flight updates)
}

type activityLine struct {
	tool   string
	status string // "⏳", "✅ 340ms", "❌ err", "auto-approved"
}

// render builds the HTML text for the current activity log.
func (l *activityLog) render() string {
	var b strings.Builder
	for i, line := range l.lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "🔧 <b>%s</b> — %s",
			html.EscapeString(line.tool), line.status)
	}
	return b.String()
}

// flush sends or edits the activity message with the current content.
func (l *activityLog) flush(ctx context.Context) {
	text := l.render()
	if l.messageID == "" {
		// First event — send a new message and capture its ID.
		id, err := l.editor.SendAndGetID(ctx, adapter.OutgoingMessage{
			Text:       text,
			ParseMode:  "HTML",
			ExternalID: l.externalID,
			Adapter:    l.adapter,
		})
		if err != nil {
			l.logger.Debug("activity log: failed to send initial message", "error", err)
			return
		}
		l.messageID = id
	} else {
		// Subsequent events — edit in-place.
		if err := l.editor.EditText(ctx, l.externalID, l.messageID, text, "HTML"); err != nil {
			l.logger.Debug("activity log: failed to edit message", "error", err)
		}
	}
}

func (l *activityLog) ensureIndex() {
	if l.toolIndex == nil {
		l.toolIndex = make(map[string]int)
	}
}

func (l *activityLog) autoApproved(ctx context.Context, tool string) {
	l.ensureIndex()
	l.lines = append(l.lines, activityLine{tool: tool, status: "auto-approved"})
	l.toolIndex[tool] = len(l.lines) - 1
	l.flush(ctx)
}

func (l *activityLog) toolStart(ctx context.Context, tool string) {
	l.ensureIndex()
	// Check if there's already a line for this tool (e.g. from auto-approved).
	if idx, ok := l.toolIndex[tool]; ok {
		l.lines[idx].status = "⏳"
		l.flush(ctx)
		return
	}
	l.lines = append(l.lines, activityLine{tool: tool, status: "⏳"})
	l.toolIndex[tool] = len(l.lines) - 1
	l.flush(ctx)
}

func (l *activityLog) toolEnd(ctx context.Context, tool string, durationMS int64, errMsg string) {
	l.ensureIndex()
	idx, ok := l.toolIndex[tool]
	if !ok {
		// tool_end without a matching tool_start — add a new line.
		l.lines = append(l.lines, activityLine{tool: tool})
		idx = len(l.lines) - 1
		l.toolIndex[tool] = idx
	}
	if errMsg != "" {
		l.lines[idx].status = "❌"
	} else {
		l.lines[idx].status = fmt.Sprintf("✅ %dms", durationMS)
	}
	// Remove from index so a second call to the same tool gets a new line.
	delete(l.toolIndex, tool)
	l.flush(ctx)
}
