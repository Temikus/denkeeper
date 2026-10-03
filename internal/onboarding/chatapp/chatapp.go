// Package chatapp verifies chat bot tokens and pairs a bot with its owner for
// the web setup wizard, before any adapter is running. It talks to the chat
// platforms directly so errors can be scrubbed: a Telegram token is part of
// every request URL, and Go's *url.Error prints the full URL.
package chatapp

import (
	"context"
	"errors"
)

// BotInfo identifies a verified bot.
type BotInfo struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	// InviteURL adds the bot to a server. Discord only: a user can message a
	// bot only if they share a server with it.
	InviteURL string `json:"invite_url,omitempty"`
}

// Sender is the first person who messaged the bot during pairing.
type Sender struct {
	ID        string `json:"id"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	ChatID    string `json:"chat_id"`
	Text      string `json:"text,omitempty"` // trimmed, so the user can recognise their message
}

var (
	// ErrRejected means the platform refused the token.
	ErrRejected = errors.New("chatapp: token rejected")
	// ErrConflict means another client (a webhook or a second poller) is
	// consuming the bot's updates.
	ErrConflict = errors.New("chatapp: another client is reading this bot's messages")
	// ErrNoSender means no private message arrived before the wait ended.
	ErrNoSender = errors.New("chatapp: no message yet")
	// ErrUnsupported means the platform has no automatic pairing; the user
	// enters their ID by hand.
	ErrUnsupported = errors.New("chatapp: automatic pairing not supported")
	// ErrUnreachable means the platform did not answer.
	ErrUnreachable = errors.New("chatapp: platform unreachable")
)

// Prober is one chat platform's setup operations.
type Prober interface {
	// Verify checks token and returns the bot it belongs to.
	Verify(ctx context.Context, token string) (BotInfo, error)
	// WaitForSender waits until ctx ends for the first private message from
	// a person. cursor is "" on the first call; pass back the returned cursor
	// to continue, so messages already seen are not reported again. Returns
	// ErrNoSender with an updated cursor when the wait ends empty.
	WaitForSender(ctx context.Context, token, cursor string) (Sender, string, error)
	// Notify sends text to chatID. Best effort; used to confirm pairing.
	Notify(ctx context.Context, token, chatID, text string) error
}

// trimText shortens a captured message for display.
func trimText(s string) string {
	const max = 100
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
