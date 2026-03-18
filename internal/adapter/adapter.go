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
