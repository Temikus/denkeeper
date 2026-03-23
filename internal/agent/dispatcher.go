package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Temikus/denkeeper/internal/adapter"
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
	agents   map[string]*Engine         // agent name → engine
	specific map[string]string          // "adapter:externalID" → agent name
	wildcard map[string]string          // "adapter" → agent name
	adapters map[string]adapter.Adapter // adapter name → adapter instance
	incoming chan adapter.IncomingMessage
	logger   *slog.Logger
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

	return &Dispatcher{
		agents:   agents,
		specific: specific,
		wildcard: wildcard,
		adapters: adapterMap,
		incoming: make(chan adapter.IncomingMessage, 64),
		logger:   logger,
	}
}

// resolveAgent finds the Engine that should handle the given message.
// Priority: specific binding > wildcard binding > "default" agent.
func (d *Dispatcher) resolveAgent(msg adapter.IncomingMessage) *Engine {
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
	e, ok := d.agents[agentName]
	if !ok {
		return fmt.Errorf("agent %q not found", agentName)
	}
	return e.HandleMessage(ctx, msg)
}

// Run starts all adapters and processes incoming messages until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) error {
	for _, a := range d.adapters {
		a := a
		go func() {
			if err := a.Start(ctx, d.incoming); err != nil && ctx.Err() == nil {
				d.logger.Error("adapter stopped with error", "adapter", a.Name(), "error", err)
			}
		}()
	}

	d.logger.Info("dispatcher started", "agents", len(d.agents), "adapters", len(d.adapters))

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("dispatcher shutting down")
			return ctx.Err()
		case msg := <-d.incoming:
			e := d.resolveAgent(msg)
			if e == nil {
				d.logger.Warn("no agent found for message, dropping", "adapter", msg.Adapter, "external_id", msg.ExternalID)
				continue
			}
			if err := e.HandleMessage(ctx, msg); err != nil {
				d.logger.Error("handling message", "error", err, "agent", e.Name(), "adapter", msg.Adapter, "user", msg.UserName)
			}
		}
	}
}
