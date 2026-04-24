package adapter

import (
	"context"
	"time"
)

// IncomingMessage represents a message received from an external platform.
type IncomingMessage struct {
	Adapter    string
	ExternalID string // chat/conversation ID
	UserID     string
	UserName   string
	Text       string
	Timestamp  time.Time
	// ConversationID, when non-empty, overrides the default adapter:externalID
	// conversation key. Used by the scheduler to create isolated sessions.
	ConversationID string
	// SessionTier, when non-empty, overrides the engine's global permission
	// tier for this message. Used by the scheduler to enforce per-schedule tiers.
	SessionTier string
	// SkillName, when non-empty, indicates this message targets a specific skill.
	// Used by the scheduler to activate schedule-triggered skills.
	SkillName string
	// ScheduleName is the name of the schedule that triggered this message.
	ScheduleName string
	// ScheduleCron is the cron expression of the triggering schedule (for audit display).
	ScheduleCron string
	// IsScheduled is true when this message was dispatched by the scheduler.
	// Used to correctly attribute audit events and distinguish scheduled
	// invocations from user-initiated ones.
	IsScheduled bool
	// IsVoice is true when the original message was a voice note.
	// The adapter sets this after transcribing the audio via STT.
	IsVoice bool
}

// KeyboardButton is an adapter-agnostic inline action button.
// Each adapter renders it as appropriate (Telegram: inline keyboard button;
// others may append as text or ignore if unsupported).
type KeyboardButton struct {
	Label        string
	CallbackData string
}

// OutgoingMessage represents a message to send to an external platform.
type OutgoingMessage struct {
	// Adapter is the name of the adapter to route the response through (e.g.
	// "telegram", "discord"). Set by Engine.HandleMessage from the incoming
	// message's Adapter field. The SendFunc implementation (typically a
	// Dispatcher wrapper) uses this to select the correct outbound adapter.
	Adapter    string
	ExternalID string
	Text       string
	// ParseMode overrides the default parse mode for this message (e.g. "HTML").
	// When empty, the adapter uses its default (Markdown for Telegram).
	ParseMode string
	// IsVoice signals that the adapter should attempt to send a voice reply
	// (via TTS) instead of plain text, if configured to do so.
	IsVoice bool
	// Buttons, when non-empty, requests the adapter render an inline keyboard.
	// Ignored during voice replies.
	Buttons []KeyboardButton
	// ButtonLayout controls how buttons are arranged into rows. Each element
	// specifies the number of buttons in that row. For example, [2, 2] creates
	// two rows of two buttons. When nil, each button gets its own row.
	ButtonLayout []int
}

// CallbackResolver handles adapter callback queries (e.g. Telegram inline
// button clicks). The adapter calls Resolve when it receives a callback; the
// resolver maps the callback data to a human-readable confirmation string.
// Returns ("", nil) if the callback is unknown or not handled.
// Defined here (not in approval/) to avoid circular imports.
type CallbackResolver interface {
	Resolve(ctx context.Context, callbackData string) (responseText string, err error)
}

// Adapter defines the interface for communication platform integrations.
type Adapter interface {
	Name() string
	Start(ctx context.Context, incoming chan<- IncomingMessage) error
	Send(ctx context.Context, msg OutgoingMessage) error
	// SendTyping sends a typing/activity indicator to the given chat.
	// Used by the Dispatcher to keep the indicator alive during long processing.
	SendTyping(ctx context.Context, externalID string) error
	Stop() error
}

// DebugChecker is an optional interface adapters can implement to expose
// per-chat debug mode. The dispatcher uses this to choose between compact
// and verbose approval message formatting.
type DebugChecker interface {
	IsDebugByExternalID(externalID string) bool
}

// MessageEditor is an optional interface adapters can implement to support
// sending a message and receiving its platform-specific ID, then editing that
// message in-place. Used by the dispatcher's activity log to accumulate tool
// events into a single updatable message.
type MessageEditor interface {
	// SendAndGetID sends a message and returns the platform message ID.
	SendAndGetID(ctx context.Context, msg OutgoingMessage) (messageID string, err error)
	// EditText replaces the text of an existing message identified by
	// externalID (chat) and messageID (message within that chat).
	EditText(ctx context.Context, externalID, messageID, text, parseMode string) error
}
