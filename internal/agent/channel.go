package agent

import "context"

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

	// Implicit is true when the channel was auto-synthesized from an agent's
	// adapter bindings (backward compatibility). Implicit channels are not
	// shown in /session listings unless the user explicitly opts in.
	Implicit bool
}

// ChannelConversationID returns the conversation ID used for this channel.
// Channel-based conversations use the format "chan:{name}".
func (ch *Channel) ConversationID() string {
	return "chan:" + ch.Name
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
