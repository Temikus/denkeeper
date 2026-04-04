package discord

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Temikus/denkeeper/internal/adapter"
)

// Adapter implements adapter.Adapter for Discord using bwmarrin/discordgo.
// It supports DMs and guild text channels. Only users in the allowlist can
// interact with the bot.
type Adapter struct {
	session      *discordgo.Session
	allowedUsers map[string]bool // Discord user snowflake IDs
	logger       *slog.Logger

	incoming chan<- adapter.IncomingMessage
	mu       sync.Mutex // guards incoming assignment
}

// New creates an Adapter and opens the Discord WebSocket session.
// allowedUsers should contain Discord user snowflake IDs as strings.
func New(token string, allowedUsers []string, logger *slog.Logger) (*Adapter, error) {
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}

	dg.Identify.Intents = discordgo.IntentsDirectMessages |
		discordgo.IntentsGuildMessages |
		discordgo.IntentMessageContent

	allowed := make(map[string]bool, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = true
	}

	a := &Adapter{
		session:      dg,
		allowedUsers: allowed,
		logger:       logger,
	}

	dg.AddHandler(a.onMessageCreate)

	return a, nil
}

// newWithSession creates an Adapter with a pre-built session (for testing).
func newWithSession(dg *discordgo.Session, allowedUsers []string, logger *slog.Logger) *Adapter {
	allowed := make(map[string]bool, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = true
	}
	a := &Adapter{
		session:      dg,
		allowedUsers: allowed,
		logger:       logger,
	}
	dg.AddHandler(a.onMessageCreate)
	return a
}

func (a *Adapter) Name() string { return "discord" }

// Start opens the Discord gateway and blocks until ctx is cancelled.
func (a *Adapter) Start(ctx context.Context, incoming chan<- adapter.IncomingMessage) error {
	a.mu.Lock()
	a.incoming = incoming
	a.mu.Unlock()

	if err := a.session.Open(); err != nil {
		return fmt.Errorf("opening discord session: %w", err)
	}
	a.logger.Info("discord adapter connected", "username", a.session.State.User.Username)

	<-ctx.Done()
	return ctx.Err()
}

func (a *Adapter) SendTyping(_ context.Context, externalID string) error {
	return a.session.ChannelTyping(externalID)
}

// Send sends a text message (and optional buttons) to a Discord channel.
// msg.ExternalID must be the Discord channel ID.
func (a *Adapter) Send(ctx context.Context, msg adapter.OutgoingMessage) error {
	if len(msg.Buttons) > 0 {
		return a.sendWithButtons(msg)
	}
	_, err := a.session.ChannelMessageSend(msg.ExternalID, msg.Text)
	if err != nil {
		return fmt.Errorf("sending discord message: %w", err)
	}
	return nil
}

// sendWithButtons sends a message with Discord action-row components.
func (a *Adapter) sendWithButtons(msg adapter.OutgoingMessage) error {
	buttons := make([]discordgo.MessageComponent, 0, len(msg.Buttons))
	for _, btn := range msg.Buttons {
		buttons = append(buttons, discordgo.Button{
			Label:    btn.Label,
			Style:    discordgo.PrimaryButton,
			CustomID: btn.CallbackData,
		})
	}

	_, err := a.session.ChannelMessageSendComplex(msg.ExternalID, &discordgo.MessageSend{
		Content: msg.Text,
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: buttons},
		},
	})
	if err != nil {
		return fmt.Errorf("sending discord message with buttons: %w", err)
	}
	return nil
}

// Stop closes the Discord WebSocket connection.
func (a *Adapter) Stop() error {
	return a.session.Close()
}

// onMessageCreate handles incoming Discord messages.
func (a *Adapter) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages from the bot itself.
	if m.Author == nil || m.Author.ID == s.State.User.ID {
		return
	}

	if !a.allowedUsers[m.Author.ID] {
		a.logger.Warn("unauthorized discord user", "user_id", m.Author.ID, "username", m.Author.Username)
		return
	}

	if m.Content == "" {
		return
	}

	// Typing indicator — analogous to Telegram's sendChatAction.
	_ = s.ChannelTyping(m.ChannelID)

	a.mu.Lock()
	ch := a.incoming
	a.mu.Unlock()

	if ch == nil {
		return
	}

	ch <- adapter.IncomingMessage{
		Adapter:    "discord",
		ExternalID: m.ChannelID,
		UserID:     m.Author.ID,
		UserName:   m.Author.Username,
		Text:       m.Content,
		Timestamp:  time.Now(),
	}
}
