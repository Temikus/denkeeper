package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/audit"
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

// inFlightRequest tracks a single in-flight message processing goroutine
// so it can be cancelled via /stop.
type inFlightRequest struct {
	cancel context.CancelFunc
	agent  string
	start  time.Time
}

// Dispatcher routes incoming messages to the correct agent Engine based on
// channel bindings (or legacy adapter bindings). It owns the adapter lifecycle
// and the shared incoming channel.
type Dispatcher struct {
	mu       sync.RWMutex
	agents   map[string]*Engine         // agent name → engine
	specific map[string]string          // "adapter:externalID" → agent name (legacy)
	wildcard map[string]string          // "adapter" → agent name (legacy)
	adapters map[string]adapter.Adapter // adapter name → adapter instance
	incoming chan adapter.IncomingMessage
	logger   *slog.Logger

	// Channel routing (replaces legacy specific/wildcard when channels are set).
	channels         map[string]*Channel // channel name → Channel
	channelSpecific  map[string]string   // "adapter:externalID" → channel name
	channelWildcard  map[string]string   // "adapter" → channel name
	activeChannelsMu sync.RWMutex
	activeChannels   map[string]string  // "adapter:externalID" → channel name (runtime /session overrides)
	activeStore      ActiveChannelStore // persistence for /session selections

	// In-flight request tracking for /stop cancellation.
	inFlightMu sync.Mutex
	inFlight   map[string]*inFlightRequest // "adapter:externalID" → request

	// Panic state — blocks all new messages until /resume.
	panicMu   sync.RWMutex
	panicked  bool
	panicTime time.Time

	// OnBroadcast, when set, is called after an adapter message (Telegram,
	// Discord, etc.) is successfully processed. The server uses this to
	// notify WebSocket clients of conversation activity from other adapters.
	OnBroadcast func(agentName, convID, adapterName, channelName, summary string)

	// OnPanic is called when a /panic command is processed. The server uses
	// this to pause the scheduler and broadcast a panic status frame.
	OnPanic func()

	// OnResume is called when a /resume command is processed.
	OnResume func()

	// Auditor emits audit events for routing/session decisions.
	Auditor audit.Emitter

	// OTel instrumentation.
	tracer    trace.Tracer
	mDispatch metric.Int64Counter
}

// DispatcherOption configures optional Dispatcher behavior.
type DispatcherOption func(*Dispatcher)

// WithChannels configures channel-based routing. When set, the dispatcher
// routes messages through the channel registry instead of the legacy
// specific/wildcard binding maps. The activeStore persists /session selections
// across restarts.
func WithChannels(channels []*Channel, activeStore ActiveChannelStore) DispatcherOption {
	return func(d *Dispatcher) {
		d.channels = make(map[string]*Channel, len(channels))
		d.channelSpecific = make(map[string]string)
		d.channelWildcard = make(map[string]string)
		d.activeChannels = make(map[string]string)
		d.activeStore = activeStore

		for _, ch := range channels {
			d.channels[ch.Name] = ch
			for _, binding := range ch.Adapters {
				if strings.Contains(binding, ":") {
					d.channelSpecific[binding] = ch.Name
				} else {
					d.channelWildcard[binding] = ch.Name
				}
			}
		}
	}
}

// NewDispatcher creates a Dispatcher from a set of named engines, bindings,
// and adapters. Bindings are processed in order; specific bindings
// ("telegram:12345") take priority over wildcard bindings ("telegram").
func NewDispatcher(
	agents map[string]*Engine,
	bindings []Binding,
	adapters []adapter.Adapter,
	logger *slog.Logger,
	opts ...DispatcherOption,
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

	d := &Dispatcher{
		agents:    agents,
		specific:  specific,
		wildcard:  wildcard,
		adapters:  adapterMap,
		incoming:  make(chan adapter.IncomingMessage, 64),
		inFlight:  make(map[string]*inFlightRequest),
		tracer:    tracer,
		mDispatch: dispatch,
		logger:    logger,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
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

// hasChannels returns true when channel-based routing is configured.
func (d *Dispatcher) hasChannels() bool {
	return d.channels != nil
}

// resolveChannel finds the Channel and Engine for the given message.
// Priority: active override (/session) > specific binding > wildcard > nil.
// Returns (nil, nil) when no channel matches; callers fall back to resolveAgent.
func (d *Dispatcher) resolveChannel(msg adapter.IncomingMessage) (*Channel, *Engine) {
	if !d.hasChannels() {
		return nil, nil
	}

	key := msg.Adapter + ":" + msg.ExternalID

	// 1. Runtime override from /session command.
	d.activeChannelsMu.RLock()
	if name, ok := d.activeChannels[key]; ok {
		d.activeChannelsMu.RUnlock()
		if ch, ok := d.channels[name]; ok {
			if e, ok := d.agents[ch.AgentName]; ok {
				return ch, e
			}
		}
		// Override references a stale channel — clear it and continue.
		d.activeChannelsMu.Lock()
		delete(d.activeChannels, key)
		d.activeChannelsMu.Unlock()
	} else {
		d.activeChannelsMu.RUnlock()
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	// 2. Config-specific binding.
	if name, ok := d.channelSpecific[key]; ok {
		if ch, ok := d.channels[name]; ok {
			if e, ok := d.agents[ch.AgentName]; ok {
				return ch, e
			}
		}
	}

	// 3. Config-wildcard binding.
	if name, ok := d.channelWildcard[msg.Adapter]; ok {
		if ch, ok := d.channels[name]; ok {
			if e, ok := d.agents[ch.AgentName]; ok {
				return ch, e
			}
		}
	}

	return nil, nil
}

// LoadActiveChannels reads persisted /session selections from the store and
// populates the in-memory cache. Call once at startup after WithChannels.
func (d *Dispatcher) LoadActiveChannels(ctx context.Context) error {
	if d.activeStore == nil {
		return nil
	}
	all, err := d.activeStore.ListActiveChannels(ctx)
	if err != nil {
		return fmt.Errorf("loading active channels: %w", err)
	}
	d.activeChannelsMu.Lock()
	defer d.activeChannelsMu.Unlock()
	for key, name := range all {
		// Only load if the channel still exists in config.
		if _, ok := d.channels[name]; ok {
			d.activeChannels[key] = name
		}
	}
	d.logger.Info("loaded active channel selections", "count", len(d.activeChannels))
	return nil
}

// Channels returns the channel registry. Returns nil when channels are not configured.
func (d *Dispatcher) Channels() map[string]*Channel {
	return d.channels
}

// ErrChannelsNotConfigured is returned when channel operations are attempted
// but no channels are configured.
var ErrChannelsNotConfigured = errors.New("channels not configured")

// ErrChannelNotFound is returned when the specified channel does not exist.
var ErrChannelNotFound = errors.New("channel not found")

// ErrAdapterKeyNotActive is returned when attempting to deactivate an adapter
// key that is not active on the specified channel.
var ErrAdapterKeyNotActive = errors.New("adapter key not active on this channel")

// ErrNotEnoughMessages is returned when a compact operation is attempted on a
// session with fewer than 2 messages.
var ErrNotEnoughMessages = errors.New("not enough messages to compact")

// SetActiveChannelByKey sets the active channel override for the given adapter
// key (e.g. "telegram:12345"). Returns an error if the channel name is not in
// the registry or channels are not configured.
func (d *Dispatcher) SetActiveChannelByKey(ctx context.Context, adapterKey, channelName string) error {
	if d.channels == nil {
		return ErrChannelsNotConfigured
	}
	if _, ok := d.channels[channelName]; !ok {
		return fmt.Errorf("%w: %q", ErrChannelNotFound, channelName)
	}

	d.activeChannelsMu.Lock()
	d.activeChannels[adapterKey] = channelName
	d.activeChannelsMu.Unlock()

	if d.activeStore != nil {
		if err := d.activeStore.SetActiveChannel(ctx, adapterKey, channelName); err != nil {
			return fmt.Errorf("persisting active channel: %w", err)
		}
	}
	return nil
}

// ClearActiveChannelByKey removes the active channel override for the given
// adapter key, but only if the key is currently active on the specified channel.
// Returns ErrAdapterKeyNotActive if the key is not active on channelName.
func (d *Dispatcher) ClearActiveChannelByKey(ctx context.Context, adapterKey, channelName string) error {
	d.activeChannelsMu.Lock()
	current, ok := d.activeChannels[adapterKey]
	if !ok || current != channelName {
		d.activeChannelsMu.Unlock()
		return ErrAdapterKeyNotActive
	}
	delete(d.activeChannels, adapterKey)
	d.activeChannelsMu.Unlock()

	if d.activeStore != nil {
		if err := d.activeStore.ClearActiveChannel(ctx, adapterKey); err != nil {
			return fmt.Errorf("clearing active channel: %w", err)
		}
	}
	return nil
}

// ActiveChannelsForChannel returns all adapter keys that currently have the
// given channel as their active override. Always returns a non-nil slice.
func (d *Dispatcher) ActiveChannelsForChannel(channelName string) []string {
	d.activeChannelsMu.RLock()
	defer d.activeChannelsMu.RUnlock()

	keys := make([]string, 0)
	for k, v := range d.activeChannels {
		if v == channelName {
			keys = append(keys, k)
		}
	}
	return keys
}

// AddChannel registers a new channel in the dispatcher's runtime registry and
// updates binding maps. Returns an error if channels are not configured, the
// name is already taken, or the referenced agent does not exist.
func (d *Dispatcher) AddChannel(ch *Channel) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.channels == nil {
		return ErrChannelsNotConfigured
	}
	if _, exists := d.channels[ch.Name]; exists {
		return fmt.Errorf("channel %q already exists", ch.Name)
	}
	if _, ok := d.agents[ch.AgentName]; !ok {
		return fmt.Errorf("agent %q not found", ch.AgentName)
	}

	d.channels[ch.Name] = ch
	for _, binding := range ch.Adapters {
		if strings.Contains(binding, ":") {
			d.channelSpecific[binding] = ch.Name
		} else {
			d.channelWildcard[binding] = ch.Name
		}
	}
	return nil
}

// UpdateChannel replaces an existing channel in the dispatcher's runtime
// registry, re-indexing its adapter bindings. Returns an error if the channel
// does not exist or is implicit.
func (d *Dispatcher) UpdateChannel(name string, ch *Channel) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	old, ok := d.channels[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrChannelNotFound, name)
	}
	if old.Implicit {
		return fmt.Errorf("cannot update implicit channel %q", name)
	}
	if ch.AgentName != "" {
		if _, ok := d.agents[ch.AgentName]; !ok {
			return fmt.Errorf("agent %q not found", ch.AgentName)
		}
	}

	// Remove old bindings.
	for _, binding := range old.Adapters {
		if strings.Contains(binding, ":") {
			delete(d.channelSpecific, binding)
		} else {
			delete(d.channelWildcard, binding)
		}
	}

	// Replace channel and re-index.
	d.channels[name] = ch
	for _, binding := range ch.Adapters {
		if strings.Contains(binding, ":") {
			d.channelSpecific[binding] = ch.Name
		} else {
			d.channelWildcard[binding] = ch.Name
		}
	}
	return nil
}

// RemoveChannel removes a channel from the dispatcher's runtime registry,
// clears its adapter bindings, and removes any active channel overrides that
// reference it. Returns an error if the channel does not exist or is implicit.
func (d *Dispatcher) RemoveChannel(ctx context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	ch, ok := d.channels[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrChannelNotFound, name)
	}
	if ch.Implicit {
		return fmt.Errorf("cannot remove implicit channel %q", name)
	}

	// Remove bindings.
	for _, binding := range ch.Adapters {
		if strings.Contains(binding, ":") {
			delete(d.channelSpecific, binding)
		} else {
			delete(d.channelWildcard, binding)
		}
	}
	delete(d.channels, name)

	// Clear active overrides referencing this channel.
	d.activeChannelsMu.Lock()
	var keysToRemove []string
	for k, v := range d.activeChannels {
		if v == name {
			keysToRemove = append(keysToRemove, k)
		}
	}
	for _, k := range keysToRemove {
		delete(d.activeChannels, k)
	}
	d.activeChannelsMu.Unlock()

	// Persist removal of active overrides.
	if d.activeStore != nil {
		for _, k := range keysToRemove {
			if err := d.activeStore.ClearActiveChannel(ctx, k); err != nil {
				d.logger.Warn("clearing active channel on delete",
					"channel", name, "adapter_key", k, "error", err)
			}
		}
	}
	return nil
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
	onEvent := d.buildEventHandler(ctx, msg)
	return e.HandleMessageWithEvents(ctx, msg, onEvent)
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

// AddAgent registers a new engine in the dispatcher's runtime map.
// Returns an error if the name is already taken.
func (d *Dispatcher) AddAgent(name string, engine *Engine) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.agents[name]; exists {
		return fmt.Errorf("agent %q already exists", name)
	}
	d.agents[name] = engine
	return nil
}

// RemoveAgent removes an engine from the dispatcher's runtime map.
// Returns an error if the agent does not exist or is "default".
// Callers must check for channel/schedule references before calling.
func (d *Dispatcher) RemoveAgent(name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if name == "default" {
		return fmt.Errorf("cannot remove the default agent")
	}
	if _, ok := d.agents[name]; !ok {
		return fmt.Errorf("agent %q not found", name)
	}

	delete(d.agents, name)

	// Clean up legacy binding maps that reference this agent.
	for k, v := range d.specific {
		if v == name {
			delete(d.specific, k)
		}
	}
	for k, v := range d.wildcard {
		if v == name {
			delete(d.wildcard, k)
		}
	}

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
			d.dispatchMessage(ctx, &wg, msg)
		}
	}
}

// dispatchMessage handles a single incoming message: intercepts control and
// session commands, resolves the target engine, and spawns a goroutine.
func (d *Dispatcher) dispatchMessage(ctx context.Context, wg *sync.WaitGroup, msg adapter.IncomingMessage) {
	// Intercept control commands (/stop, /panic, /resume) before routing.
	if cmd := controlCommand(msg.Text); cmd != "" {
		d.handleControlCommand(ctx, cmd, msg)
		return
	}

	// Block new messages while panicked.
	if d.IsPanicked() {
		d.sendPanicBlocked(ctx, msg)
		return
	}

	// Intercept /session commands before routing.
	if d.hasChannels() && isSessionCommand(msg.Text) {
		d.handleSessionCommand(ctx, msg)
		return
	}

	// Try channel-based routing first, fall back to legacy.
	var e *Engine
	var ch *Channel
	ch, e = d.resolveChannel(msg)
	if e == nil {
		e = d.resolveAgent(msg)
	}
	if e == nil {
		d.logger.Warn("no agent found for message, dropping", "adapter", msg.Adapter, "external_id", msg.ExternalID)
		return
	}

	// Set conversation ID from channel so the engine uses
	// the channel's session instead of the default key.
	if ch != nil && msg.ConversationID == "" {
		if ch.IsEphemeral() {
			msg.ConversationID = ch.EphemeralConversationID()
		} else {
			msg.ConversationID = ch.ConversationID()
		}
	}

	// Intercept /clear and /compact after routing so we have the engine
	// and conversation ID.
	if cmd := historyCommand(msg.Text); cmd != "" {
		d.handleHistoryCommand(ctx, wg, cmd, e, msg)
		return
	}

	d.mDispatch.Add(ctx, 1, metric.WithAttributes(
		attribute.String("adapter", msg.Adapter),
		attribute.String("agent", e.Name())))

	d.emitRouteAudit(ctx, msg, ch, e)

	wg.Add(1)
	go d.handleMessage(ctx, wg, e, msg)
}

// emitRouteAudit emits an audit event for message routing.
func (d *Dispatcher) emitRouteAudit(ctx context.Context, msg adapter.IncomingMessage, ch *Channel, e *Engine) {
	if d.Auditor == nil {
		return
	}
	chName := ""
	if ch != nil {
		chName = ch.Name
	}
	detail, _ := json.Marshal(map[string]string{"adapter": msg.Adapter, "channel": chName, "external_id": msg.ExternalID})
	d.Auditor.Emit(ctx, audit.Event{
		Category: audit.CategoryChannel,
		Action:   "route",
		Agent:    e.Name(),
		Summary:  fmt.Sprintf("Routed via %s to agent %s", msg.Adapter, e.Name()),
		Detail:   string(detail),
		Status:   audit.StatusOK,
		Source:   msg.Adapter,
	})
}

// handleMessage processes a single incoming message. It runs the engine
// pipeline and sends an error message back to the user if the pipeline fails.
// The context is wrapped with a cancel function so /stop can abort it.
func (d *Dispatcher) handleMessage(ctx context.Context, wg *sync.WaitGroup, e *Engine, msg adapter.IncomingMessage) {
	defer wg.Done()

	// Derive a cancellable context so /stop can abort this request.
	msgCtx, msgCancel := context.WithCancel(ctx)
	defer msgCancel()

	key := msg.Adapter + ":" + msg.ExternalID
	d.inFlightMu.Lock()
	d.inFlight[key] = &inFlightRequest{cancel: msgCancel, agent: e.Name(), start: time.Now()}
	d.inFlightMu.Unlock()
	defer func() {
		d.inFlightMu.Lock()
		delete(d.inFlight, key)
		d.inFlightMu.Unlock()
	}()

	msgCtx, span := d.tracer.Start(msgCtx, "dispatcher.route",
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
		// Don't log or send error feedback if the context was cancelled
		// by a /stop command — that's intentional, not an error.
		if msgCtx.Err() != nil {
			d.logger.Info("message cancelled", "agent", e.Name(), "adapter", msg.Adapter, "user", msg.UserName)
			return
		}
		d.logger.Error("handling message", "error", err, "agent", e.Name(), "adapter", msg.Adapter, "user", msg.UserName)
		span.RecordError(err)
		d.sendErrorFeedback(msgCtx, msg)
		return
	}

	// Notify WebSocket clients of adapter activity so the web UI can
	// refresh its session list or reload the active conversation.
	if d.OnBroadcast != nil && msg.Adapter != "ws" && msg.Adapter != "api" {
		convID := msg.ConversationID
		if convID == "" {
			convID = e.Name() + ":" + msg.Adapter + ":" + msg.ExternalID
		}
		channelName := ""
		if strings.HasPrefix(convID, "chan:") {
			channelName = strings.TrimPrefix(convID, "chan:")
		}
		d.OnBroadcast(e.Name(), convID, msg.Adapter, channelName, "New message processed")
	}
}

// ---------------------------------------------------------------------------
// /stop, /panic, /resume control commands
// ---------------------------------------------------------------------------

// controlCommand returns the control command name ("stop", "panic", "resume")
// if the message text is a recognised control command, or "" otherwise.
func controlCommand(text string) string {
	switch strings.TrimSpace(text) {
	case "/stop":
		return "stop"
	case "/panic":
		return "panic"
	case "/resume":
		return "resume"
	default:
		return ""
	}
}

// handleControlCommand dispatches a control command to the appropriate handler.
func (d *Dispatcher) handleControlCommand(ctx context.Context, cmd string, msg adapter.IncomingMessage) {
	switch cmd {
	case "stop":
		d.handleStopCommand(ctx, msg)
	case "panic":
		d.handlePanicCommand(ctx, msg)
	case "resume":
		d.handleResumeCommand(ctx, msg)
	}
}

// handleStopCommand cancels the in-flight request for the sender's chat.
func (d *Dispatcher) handleStopCommand(ctx context.Context, msg adapter.IncomingMessage) {
	key := msg.Adapter + ":" + msg.ExternalID
	d.inFlightMu.Lock()
	req, ok := d.inFlight[key]
	if ok {
		req.cancel()
		delete(d.inFlight, key)
	}
	d.inFlightMu.Unlock()

	text := "No request in progress."
	if ok {
		text = "Request cancelled."
		d.logger.Info("stop command: cancelled in-flight request", "adapter", msg.Adapter, "external_id", msg.ExternalID, "agent", req.agent)
	}

	d.emitSafetyAudit(ctx, "stop", msg, text)
	d.sendControlResponse(ctx, msg, text)
}

// handlePanicCommand triggers an emergency stop: cancels all in-flight
// requests and calls the OnPanic hook (which pauses the scheduler and
// broadcasts to WebSocket clients).
func (d *Dispatcher) handlePanicCommand(ctx context.Context, msg adapter.IncomingMessage) {
	d.executePanic()
	d.logger.Warn("panic command: all processing stopped", "source", msg.Adapter, "user", msg.UserName)
	d.emitSafetyAudit(ctx, "panic", msg, "Emergency stop triggered")
	d.sendControlResponse(ctx, msg, "All processing stopped. Use /resume to restart.")
}

// handleResumeCommand clears the panic state and calls the OnResume hook.
func (d *Dispatcher) handleResumeCommand(ctx context.Context, msg adapter.IncomingMessage) {
	d.executeResume()
	d.logger.Info("resume command: processing resumed", "source", msg.Adapter, "user", msg.UserName)
	d.emitSafetyAudit(ctx, "resume", msg, "Processing resumed")
	d.sendControlResponse(ctx, msg, "Processing resumed.")
}

// executePanic performs the panic: sets state, cancels all in-flight, calls hook.
func (d *Dispatcher) executePanic() {
	d.panicMu.Lock()
	d.panicked = true
	d.panicTime = time.Now()
	d.panicMu.Unlock()

	d.inFlightMu.Lock()
	for key, req := range d.inFlight {
		req.cancel()
		delete(d.inFlight, key)
	}
	d.inFlightMu.Unlock()

	if d.OnPanic != nil {
		d.OnPanic()
	}
}

// executeResume clears the panic state and calls the resume hook.
func (d *Dispatcher) executeResume() {
	d.panicMu.Lock()
	d.panicked = false
	d.panicMu.Unlock()

	if d.OnResume != nil {
		d.OnResume()
	}
}

// StopChat cancels the in-flight request for the given adapter and external ID.
// Returns an error if no request is in progress. Used by REST API and WebSocket.
func (d *Dispatcher) StopChat(adapterName, externalID string) error {
	key := adapterName + ":" + externalID
	d.inFlightMu.Lock()
	req, ok := d.inFlight[key]
	if ok {
		req.cancel()
		delete(d.inFlight, key)
	}
	d.inFlightMu.Unlock()

	if !ok {
		return fmt.Errorf("no in-flight request for %s", key)
	}
	d.logger.Info("stop: cancelled in-flight request via API", "key", key, "agent", req.agent)
	return nil
}

// Panic triggers an emergency stop via the API. Cancels all in-flight
// requests and calls the OnPanic hook.
func (d *Dispatcher) Panic() {
	d.executePanic()
	d.logger.Warn("panic triggered via API")
}

// Resume clears the panic state and calls the OnResume hook.
func (d *Dispatcher) Resume() {
	d.executeResume()
	d.logger.Info("resume triggered via API")
}

// IsPanicked returns true if the dispatcher is in panic mode.
func (d *Dispatcher) IsPanicked() bool {
	d.panicMu.RLock()
	defer d.panicMu.RUnlock()
	return d.panicked
}

// PanicTime returns the time when panic was triggered. Returns zero time
// if not panicked.
func (d *Dispatcher) PanicTime() time.Time {
	d.panicMu.RLock()
	defer d.panicMu.RUnlock()
	return d.panicTime
}

// sendPanicBlocked notifies the user that their message was blocked because
// the system is in panic mode.
func (d *Dispatcher) sendPanicBlocked(ctx context.Context, msg adapter.IncomingMessage) {
	d.sendControlResponse(ctx, msg, "System is paused. Use /resume to restart processing.")
}

// sendControlResponse sends a short text reply for control commands.
func (d *Dispatcher) sendControlResponse(ctx context.Context, msg adapter.IncomingMessage, text string) {
	a, ok := d.adapters[msg.Adapter]
	if !ok {
		return
	}
	_ = a.Send(ctx, adapter.OutgoingMessage{
		Adapter:    msg.Adapter,
		ExternalID: msg.ExternalID,
		Text:       text,
	})
}

// emitSafetyAudit emits an audit event for a safety control command.
func (d *Dispatcher) emitSafetyAudit(ctx context.Context, action string, msg adapter.IncomingMessage, summary string) {
	if d.Auditor == nil {
		return
	}
	detail, _ := json.Marshal(map[string]string{"adapter": msg.Adapter, "external_id": msg.ExternalID, "user": msg.UserName})
	d.Auditor.Emit(ctx, audit.Event{
		Category: audit.CategorySafety,
		Action:   action,
		Summary:  summary,
		Detail:   string(detail),
		Status:   audit.StatusOK,
		Source:   msg.Adapter,
	})
}

// ---------------------------------------------------------------------------
// /session command handling
// ---------------------------------------------------------------------------

// isSessionCommand returns true when the message text is a /session command.
func isSessionCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	return trimmed == "/session" || strings.HasPrefix(trimmed, "/session ")
}

// handleSessionCommand processes /session commands directly in the dispatcher,
// bypassing the engine/LLM entirely. Responses go straight to the adapter.
func (d *Dispatcher) handleSessionCommand(ctx context.Context, msg adapter.IncomingMessage) {
	a, ok := d.adapters[msg.Adapter]
	if !ok {
		return
	}

	parts := strings.Fields(strings.TrimSpace(msg.Text))
	// /session (no args) — list available channels.
	if len(parts) == 1 {
		d.sendSessionList(ctx, a, msg)
		return
	}

	arg := parts[1]

	// /session reset — clear override.
	if arg == "reset" {
		d.clearActiveChannel(ctx, msg)
		_ = a.Send(ctx, adapter.OutgoingMessage{
			Adapter:    msg.Adapter,
			ExternalID: msg.ExternalID,
			Text:       "Session reset to default routing.",
		})
		return
	}

	// /session <name> — switch to named channel.
	ch, ok := d.channels[arg]
	if !ok {
		_ = a.Send(ctx, adapter.OutgoingMessage{
			Adapter:    msg.Adapter,
			ExternalID: msg.ExternalID,
			Text:       fmt.Sprintf("Unknown channel %q. Use /session to list available channels.", arg),
		})
		return
	}

	// Verify the agent exists.
	if _, ok := d.agents[ch.AgentName]; !ok {
		_ = a.Send(ctx, adapter.OutgoingMessage{
			Adapter:    msg.Adapter,
			ExternalID: msg.ExternalID,
			Text:       fmt.Sprintf("Channel %q references unknown agent %q.", arg, ch.AgentName),
		})
		return
	}

	d.setActiveChannel(ctx, msg, ch.Name)

	// Audit: session switch.
	if d.Auditor != nil {
		switchDetail, _ := json.Marshal(map[string]string{"channel": ch.Name, "adapter": msg.Adapter, "agent": ch.AgentName})
		d.Auditor.Emit(ctx, audit.Event{
			Category: audit.CategorySession,
			Action:   "switch",
			Agent:    ch.AgentName,
			Summary:  fmt.Sprintf("Session switched to channel %q", ch.Name),
			Detail:   string(switchDetail),
			Status:   audit.StatusOK,
			Source:   msg.Adapter,
		})
	}

	_ = a.Send(ctx, adapter.OutgoingMessage{
		Adapter:    msg.Adapter,
		ExternalID: msg.ExternalID,
		Text:       fmt.Sprintf("Switched to channel %q (agent: %s).", ch.Name, ch.AgentName),
	})
}

// sendSessionList sends a list of available channels for the current adapter chat.
func (d *Dispatcher) sendSessionList(ctx context.Context, a adapter.Adapter, msg adapter.IncomingMessage) {
	key := msg.Adapter + ":" + msg.ExternalID

	d.activeChannelsMu.RLock()
	activeName := d.activeChannels[key]
	d.activeChannelsMu.RUnlock()

	var b strings.Builder
	b.WriteString("Available channels:\n")

	for _, ch := range d.channels {
		if ch.Implicit {
			continue // hide auto-synthesized channels
		}
		marker := "  "
		if ch.Name == activeName {
			marker = "> "
		}
		fmt.Fprintf(&b, "%s%s (agent: %s)\n", marker, ch.Name, ch.AgentName)
	}

	if activeName == "" {
		b.WriteString("\nNo active override — using default routing.")
	} else {
		fmt.Fprintf(&b, "\nActive: %s", activeName)
	}

	b.WriteString("\n\nUsage: /session <name> | /session reset")

	_ = a.Send(ctx, adapter.OutgoingMessage{
		Adapter:    msg.Adapter,
		ExternalID: msg.ExternalID,
		Text:       b.String(),
	})
}

// setActiveChannel persists a /session selection.
func (d *Dispatcher) setActiveChannel(ctx context.Context, msg adapter.IncomingMessage, channelName string) {
	key := msg.Adapter + ":" + msg.ExternalID
	d.activeChannelsMu.Lock()
	d.activeChannels[key] = channelName
	d.activeChannelsMu.Unlock()

	if d.activeStore != nil {
		if err := d.activeStore.SetActiveChannel(ctx, key, channelName); err != nil {
			d.logger.Error("persisting active channel", "error", err, "key", key, "channel", channelName)
		}
	}
}

// clearActiveChannel removes a /session override.
func (d *Dispatcher) clearActiveChannel(ctx context.Context, msg adapter.IncomingMessage) {
	key := msg.Adapter + ":" + msg.ExternalID
	d.activeChannelsMu.Lock()
	delete(d.activeChannels, key)
	d.activeChannelsMu.Unlock()

	if d.activeStore != nil {
		if err := d.activeStore.ClearActiveChannel(ctx, key); err != nil {
			d.logger.Error("clearing active channel", "error", err, "key", key)
		}
	}
}

// ---------------------------------------------------------------------------
// /clear and /compact history commands
// ---------------------------------------------------------------------------

// historyCommand returns "clear" or "compact" if the message text is a
// recognised history command, or "" otherwise.
func historyCommand(text string) string {
	switch strings.TrimSpace(text) {
	case "/clear":
		return "clear"
	case "/compact":
		return "compact"
	default:
		return ""
	}
}

// handleHistoryCommand dispatches /clear and /compact. Clear is synchronous;
// compact spawns a goroutine because it involves an LLM call.
func (d *Dispatcher) handleHistoryCommand(ctx context.Context, wg *sync.WaitGroup, cmd string, e *Engine, msg adapter.IncomingMessage) {
	convID := d.resolveConversationID(e, msg)

	switch cmd {
	case "clear":
		d.handleClearCommand(ctx, e, msg, convID)
	case "compact":
		wg.Add(1)
		go d.handleCompactCommand(ctx, wg, e, msg, convID)
	}
}

// resolveConversationID determines the conversation ID for a message without
// creating it. Used by history commands to identify the target session.
func (d *Dispatcher) resolveConversationID(e *Engine, msg adapter.IncomingMessage) string {
	if msg.ConversationID != "" {
		return msg.ConversationID
	}
	return e.Name() + ":" + msg.Adapter + ":" + msg.ExternalID
}

// handleClearCommand processes the /clear command synchronously.
func (d *Dispatcher) handleClearCommand(ctx context.Context, e *Engine, msg adapter.IncomingMessage, convID string) {
	if err := e.ClearSession(ctx, convID); err != nil {
		d.logger.Error("clear command failed", "error", err, "conversation", convID)
		d.sendControlResponse(ctx, msg, "Failed to clear session history.")
		return
	}
	d.sendControlResponse(ctx, msg, "Session history cleared.")
}

// handleCompactCommand processes the /compact command in a goroutine.
func (d *Dispatcher) handleCompactCommand(ctx context.Context, wg *sync.WaitGroup, e *Engine, msg adapter.IncomingMessage, convID string) {
	defer wg.Done()

	// Show typing indicator while the LLM works.
	stopTyping := d.startTypingTicker(ctx, msg)
	defer stopTyping()

	summary, err := e.CompactSession(ctx, convID)
	if err != nil {
		d.logger.Error("compact command failed", "error", err, "conversation", convID)
		d.sendControlResponse(ctx, msg, fmt.Sprintf("Failed to compact session: %v", err))
		return
	}

	// Send a rune-safe truncated preview of the summary.
	preview := summary
	const maxPreviewRunes = 500
	runes := []rune(preview)
	if len(runes) > maxPreviewRunes {
		preview = string(runes[:maxPreviewRunes]) + "..."
	}
	d.sendControlResponse(ctx, msg, fmt.Sprintf("Session compacted.\n\n%s", preview))
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
		return nil // no adapter — return nil so the engine can detect that approvals cannot be surfaced
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

	// Supervisor decisions are informational — no buttons needed.
	switch evt.ApprovalStatus {
	case "supervisor_approved":
		if debug {
			_ = a.Send(ctx, adapter.OutgoingMessage{
				Text:       fmt.Sprintf("Tool **%s** approved by supervisor", evt.Tool),
				ExternalID: msg.ExternalID,
				Adapter:    msg.Adapter,
			})
		} else if alog != nil {
			alog.autoApproved(ctx, evt.Tool+" (supervisor)")
		}
		return
	case "supervisor_denied":
		text := fmt.Sprintf("Tool **%s** denied by supervisor: %s", evt.Tool, evt.Text)
		_ = a.Send(ctx, adapter.OutgoingMessage{
			Text:       text,
			ExternalID: msg.ExternalID,
			Adapter:    msg.Adapter,
		})
		return
	case "supervisor_escalated":
		if debug {
			_ = a.Send(ctx, adapter.OutgoingMessage{
				Text:       fmt.Sprintf("Supervisor escalated tool **%s** — awaiting your review", evt.Tool),
				ExternalID: msg.ExternalID,
				Adapter:    msg.Adapter,
			})
		}
		// Don't return — the subsequent awaitToolApproval will emit another
		// tool_approval event with buttons for the human to act on.
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
