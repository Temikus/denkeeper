package agent

import (
	"context"
	"fmt"
	"strings"
)

// Channel is a named routing endpoint that binds adapter chats to an agent
// with explicit session identity. Channels decouple conversations from the
// rigid 1:1 agent-adapter binding, enabling session switching (/session) and
// cross-adapter session sharing.
type Channel struct {
	// Name is the unique identifier for this channel (e.g. "work", "personal").
	Name string

	// AgentName is the agent that handles messages routed through this channel.
	AgentName string

	// Adapters lists the adapter bindings from the config, using the same
	// "adapter" (wildcard) or "adapter:externalID" (specific) format.
	Adapters []string

	// Delivery controls how scheduled messages are delivered through this
	// channel's adapter bindings. "broadcast" delivers through all specific
	// bindings; any other value (including empty) uses the first specific
	// binding only.
	Delivery string

	// Implicit is true when the channel was auto-synthesized from an agent's
	// adapter bindings (backward compatibility). Implicit channels are not
	// shown in /session listings unless the user explicitly opts in.
	Implicit bool
}

// AdapterBinding is a parsed adapter:externalID pair.
type AdapterBinding struct {
	Adapter    string
	ExternalID string
}

// ChannelConversationID returns the conversation ID used for this channel.
// Channel-based conversations use the format "chan:{name}".
func (ch *Channel) ConversationID() string {
	return "chan:" + ch.Name
}

// ResolveBinding returns the first specific adapter binding (adapter:externalID)
// from the channel's adapter list. If only wildcard bindings exist, returns
// the wildcard adapter name with an empty externalID and wildcard=true.
// Returns ok=false if the channel has no adapter bindings at all.
func (ch *Channel) ResolveBinding() (adapterName, externalID string, wildcard, ok bool) {
	for _, binding := range ch.Adapters {
		idx := strings.IndexByte(binding, ':')
		if idx > 0 && idx < len(binding)-1 {
			return binding[:idx], binding[idx+1:], false, true
		}
	}
	if len(ch.Adapters) > 0 {
		return ch.Adapters[0], "", true, true
	}
	return "", "", false, false
}

// IsBroadcast returns true when the channel is configured for broadcast delivery.
func (ch *Channel) IsBroadcast() bool {
	return ch.Delivery == "broadcast"
}

// ResolveAllBindings returns all specific adapter:externalID bindings from the
// channel's adapter list. Wildcard-only bindings are skipped. Used for
// broadcast delivery where a message should be sent through every specific
// binding.
func (ch *Channel) ResolveAllBindings() []AdapterBinding {
	var bindings []AdapterBinding
	for _, raw := range ch.Adapters {
		idx := strings.IndexByte(raw, ':')
		if idx > 0 && idx < len(raw)-1 {
			bindings = append(bindings, AdapterBinding{
				Adapter:    raw[:idx],
				ExternalID: raw[idx+1:],
			})
		}
	}
	return bindings
}

// ResolveChannelByName looks up a named channel in a channel registry and
// returns the conversation ID, adapter name, and external ID. When the channel
// has only wildcard adapter bindings (no specific externalID), wildcard is true.
// Returns an error if the channel is not found.
func ResolveChannelByName(channels map[string]*Channel, name string) (conversationID, adapterName, externalID string, wildcard bool, err error) {
	if channels == nil {
		return "", "", "", false, fmt.Errorf("channel @%s not found: channels not configured", name)
	}
	ch, found := channels[name]
	if !found {
		return "", "", "", false, fmt.Errorf("channel @%s not found", name)
	}
	convID := ch.ConversationID()
	adapter, eid, wc, ok := ch.ResolveBinding()
	if !ok {
		return convID, "", "", false, nil
	}
	return convID, adapter, eid, wc, nil
}

// ActiveChannelStore persists the user's active channel selection per adapter
// chat. This state survives restarts so users don't lose their /session choice.
type ActiveChannelStore interface {
	// GetActiveChannel returns the active channel name for the given adapter
	// key ("adapter:externalID"). Returns ("", nil) when no override is set.
	GetActiveChannel(ctx context.Context, adapterKey string) (string, error)

	// SetActiveChannel persists the active channel override for the given
	// adapter key.
	SetActiveChannel(ctx context.Context, adapterKey, channelName string) error

	// ClearActiveChannel removes the active channel override, reverting the
	// adapter key to config-based routing.
	ClearActiveChannel(ctx context.Context, adapterKey string) error

	// ListActiveChannels returns all active channel overrides. Used on startup
	// to populate the dispatcher's in-memory cache.
	ListActiveChannels(ctx context.Context) (map[string]string, error)
}
