package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/Temikus/foxbox/internal/adapter"
)

type Adapter struct {
	bot          *tgbotapi.BotAPI
	allowedUsers map[int64]bool
	logger       *slog.Logger
}

func New(token string, allowedUsers []int64, logger *slog.Logger) (*Adapter, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("creating telegram bot: %w", err)
	}

	allowed := make(map[int64]bool, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = true
	}

	logger.Info("telegram bot authorized", "username", bot.Self.UserName)

	return &Adapter{
		bot:          bot,
		allowedUsers: allowed,
		logger:       logger,
	}, nil
}

// newWithBot creates an adapter with a pre-configured bot (for testing).
func newWithBot(bot *tgbotapi.BotAPI, allowedUsers []int64, logger *slog.Logger) *Adapter {
	allowed := make(map[int64]bool, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = true
	}
	return &Adapter{
		bot:          bot,
		allowedUsers: allowed,
		logger:       logger,
	}
}

func (a *Adapter) Name() string { return "telegram" }

func (a *Adapter) Start(ctx context.Context, incoming chan<- adapter.IncomingMessage) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := a.bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case update := <-updates:
			if update.Message == nil {
				continue
			}

			userID := update.Message.From.ID
			if !a.allowedUsers[userID] {
				a.logger.Warn("unauthorized user", "user_id", userID, "username", update.Message.From.UserName)
				continue
			}

			msg := adapter.IncomingMessage{
				Adapter:    "telegram",
				ExternalID: strconv.FormatInt(update.Message.Chat.ID, 10),
				UserID:     strconv.FormatInt(userID, 10),
				UserName:   update.Message.From.UserName,
				Text:       update.Message.Text,
				Timestamp:  time.Unix(int64(update.Message.Date), 0),
			}

			select {
			case incoming <- msg:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (a *Adapter) Send(_ context.Context, msg adapter.OutgoingMessage) error {
	chatID, err := strconv.ParseInt(msg.ExternalID, 10, 64)
	if err != nil {
		return fmt.Errorf("parsing chat ID: %w", err)
	}

	tgMsg := tgbotapi.NewMessage(chatID, msg.Text)

	// Try Markdown first, fall back to plain text if the LLM response
	// contains characters that break Telegram's Markdown parser.
	tgMsg.ParseMode = "Markdown"
	_, err = a.bot.Send(tgMsg)
	if err != nil {
		a.logger.Debug("markdown send failed, retrying as plain text", "error", err)
		tgMsg.ParseMode = ""
		_, err = a.bot.Send(tgMsg)
	}
	if err != nil {
		return fmt.Errorf("sending telegram message: %w", err)
	}

	return nil
}

func (a *Adapter) Stop() error {
	a.bot.StopReceivingUpdates()
	return nil
}

// IsAllowed checks if a user ID is in the allowed list.
func (a *Adapter) IsAllowed(userID int64) bool {
	return a.allowedUsers[userID]
}
