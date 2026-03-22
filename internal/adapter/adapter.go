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
}

// OutgoingMessage represents a message to send to an external platform.
type OutgoingMessage struct {
	ExternalID string
	Text       string
}

// Adapter defines the interface for communication platform integrations.
type Adapter interface {
	Name() string
	Start(ctx context.Context, incoming chan<- IncomingMessage) error
	Send(ctx context.Context, msg OutgoingMessage) error
	Stop() error
}
