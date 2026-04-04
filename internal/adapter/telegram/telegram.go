package telegram

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/voice"
)

// VoiceOpts configures optional voice (STT/TTS) support for the adapter.
type VoiceOpts struct {
	STT            voice.STTProvider
	TTS            voice.TTSProvider
	TTSVoice       string
	AutoVoiceReply bool
}

type Adapter struct {
	bot              *tgbotapi.BotAPI
	allowedUsers     map[int64]bool
	logger           *slog.Logger
	stt              voice.STTProvider
	tts              voice.TTSProvider
	ttsVoice         string
	autoVoiceReply   bool
	callbackResolver adapter.CallbackResolver // nil = ignore callback queries
}

func New(token string, allowedUsers []int64, logger *slog.Logger, voiceOpts *VoiceOpts) (*Adapter, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("creating telegram bot: %w", err)
	}

	allowed := make(map[int64]bool, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = true
	}

	logger.Info("telegram bot authorized", "username", bot.Self.UserName)

	// Register slash commands so they appear in the Telegram command menu.
	cmds := []tgbotapi.BotCommand{
		{Command: "start", Description: "Start a conversation"},
		{Command: "help", Description: "Show help and available commands"},
	}
	if _, err := bot.Request(tgbotapi.NewSetMyCommands(cmds...)); err != nil {
		logger.Warn("failed to register bot commands", "error", err)
	}

	a := &Adapter{
		bot:          bot,
		allowedUsers: allowed,
		logger:       logger,
	}
	if voiceOpts != nil {
		a.stt = voiceOpts.STT
		a.tts = voiceOpts.TTS
		a.ttsVoice = voiceOpts.TTSVoice
		a.autoVoiceReply = voiceOpts.AutoVoiceReply
	}

	return a, nil
}

// newWithBot creates an adapter with a pre-configured bot (for testing).
func newWithBot(bot *tgbotapi.BotAPI, allowedUsers []int64, logger *slog.Logger, voiceOpts *VoiceOpts) *Adapter {
	allowed := make(map[int64]bool, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = true
	}
	a := &Adapter{
		bot:          bot,
		allowedUsers: allowed,
		logger:       logger,
	}
	if voiceOpts != nil {
		a.stt = voiceOpts.STT
		a.tts = voiceOpts.TTS
		a.ttsVoice = voiceOpts.TTSVoice
		a.autoVoiceReply = voiceOpts.AutoVoiceReply
	}
	return a
}

func (a *Adapter) Name() string { return "telegram" }

// SetCallbackResolver sets the resolver used to handle inline keyboard button
// clicks (callback queries). Call this after adapter construction, before Start.
func (a *Adapter) SetCallbackResolver(r adapter.CallbackResolver) {
	a.callbackResolver = r
}

// clearStalePollSession forces Telegram to drop any lingering getUpdates
// long-poll from a previous instance by issuing a short-timeout getUpdates
// call. The 409 Conflict (if any) lands here instead of in the main loop,
// and Telegram's server-side session is reset so the next call succeeds.
func (a *Adapter) clearStalePollSession() {
	probe := tgbotapi.NewUpdate(0)
	probe.Timeout = 1 // 1-second timeout — just enough to evict the stale session
	if _, err := a.bot.GetUpdates(probe); err != nil {
		slog.Info("cleared stale telegram poll session", "error", err)
	}
}

func (a *Adapter) Start(ctx context.Context, incoming chan<- adapter.IncomingMessage) error {
	a.clearStalePollSession()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := a.bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case update := <-updates:
			// Handle inline keyboard button clicks.
			if update.CallbackQuery != nil && a.callbackResolver != nil {
				a.handleCallbackQuery(ctx, update.CallbackQuery)
				continue
			}

			if update.Message == nil {
				continue
			}

			userID := update.Message.From.ID
			if !a.allowedUsers[userID] {
				a.logger.Warn("unauthorized user", "user_id", userID, "username", update.Message.From.UserName)
				continue
			}

			// Show typing indicator while processing.
			_, _ = a.bot.Send(tgbotapi.NewChatAction(update.Message.Chat.ID, tgbotapi.ChatTyping))

			var text string
			var isVoice bool

			if update.Message.Voice != nil && a.stt != nil {
				audioData, err := a.downloadVoiceFile(ctx, update.Message.Voice.FileID)
				if err != nil {
					a.logger.Error("downloading voice file", "error", err)
					continue
				}
				transcribed, err := a.stt.Transcribe(ctx, audioData, "ogg")
				if err != nil {
					a.logger.Error("transcribing voice message", "error", err)
					continue
				}
				text = transcribed
				isVoice = true
				a.logger.Info("voice message transcribed",
					"duration", update.Message.Voice.Duration,
					"text_len", len(text),
				)
			} else {
				text = update.Message.Text
			}

			if text == "" {
				continue
			}

			msg := adapter.IncomingMessage{
				Adapter:    "telegram",
				ExternalID: strconv.FormatInt(update.Message.Chat.ID, 10),
				UserID:     strconv.FormatInt(userID, 10),
				UserName:   update.Message.From.UserName,
				Text:       text,
				Timestamp:  time.Unix(int64(update.Message.Date), 0),
				IsVoice:    isVoice,
			}

			select {
			case incoming <- msg:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (a *Adapter) SendTyping(_ context.Context, externalID string) error {
	chatID, err := strconv.ParseInt(externalID, 10, 64)
	if err != nil {
		return fmt.Errorf("parsing chat ID: %w", err)
	}
	_, err = a.bot.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
	return err
}

func (a *Adapter) Send(ctx context.Context, msg adapter.OutgoingMessage) error {
	chatID, err := strconv.ParseInt(msg.ExternalID, 10, 64)
	if err != nil {
		return fmt.Errorf("parsing chat ID: %w", err)
	}

	// Voice reply: synthesize audio and send as a Telegram voice message.
	if msg.IsVoice && a.autoVoiceReply && a.tts != nil {
		audioData, ttsErr := a.tts.Synthesize(ctx, msg.Text, a.ttsVoice)
		if ttsErr != nil {
			a.logger.Warn("TTS synthesis failed, falling back to text", "error", ttsErr)
		} else {
			voiceMsg := tgbotapi.NewVoice(chatID, tgbotapi.FileBytes{
				Name:  "response.ogg",
				Bytes: audioData,
			})
			if _, sendErr := a.bot.Send(voiceMsg); sendErr != nil {
				return fmt.Errorf("sending voice message: %w", sendErr)
			}
			return nil
		}
	}

	// Text reply (default path).
	tgMsg := tgbotapi.NewMessage(chatID, msg.Text)

	// Attach inline keyboard buttons if provided.
	if len(msg.Buttons) > 0 {
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(msg.Buttons))
		for _, btn := range msg.Buttons {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(btn.Label, btn.CallbackData),
			))
		}
		tgMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	}

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

// handleCallbackQuery processes an inline keyboard button click. It answers
// the Telegram callback query (required to clear the loading state), removes
// the inline keyboard from the original message so buttons cannot be clicked
// again, and sends a confirmation message to the chat.
func (a *Adapter) handleCallbackQuery(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	// Answer the callback query first — Telegram requires this within a few
	// seconds or the button shows a loading spinner indefinitely.
	answer := tgbotapi.NewCallback(cq.ID, "")
	if _, err := a.bot.Request(answer); err != nil {
		a.logger.Warn("failed to answer callback query", "error", err)
	}

	// Ask the resolver what text to send back to the user.
	responseText, err := a.callbackResolver.Resolve(ctx, cq.Data)
	if err != nil {
		a.logger.Error("resolving callback query", "data", cq.Data, "error", err)
		return
	}
	if responseText == "" {
		return // unknown callback, silently ignore
	}

	// Remove the inline keyboard from the original message. This prevents the
	// user from clicking the buttons a second time after the approval has
	// already been resolved or expired.
	if cq.Message != nil {
		edit := tgbotapi.NewEditMessageReplyMarkup(
			cq.Message.Chat.ID,
			cq.Message.MessageID,
			tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}},
		)
		if _, editErr := a.bot.Request(edit); editErr != nil {
			// Non-fatal: the message may have been deleted or the bot lacks edit permission.
			a.logger.Debug("failed to remove inline keyboard from message", "error", editErr)
		}
	}

	// Send the confirmation message to the originating chat.
	reply := tgbotapi.NewMessage(cq.Message.Chat.ID, responseText)
	if _, err := a.bot.Send(reply); err != nil {
		a.logger.Warn("failed to send callback response", "chat_id", cq.Message.Chat.ID, "error", err)
	}
}

// downloadVoiceFile retrieves a voice file from Telegram's servers by file ID.
func (a *Adapter) downloadVoiceFile(ctx context.Context, fileID string) ([]byte, error) {
	url, err := a.bot.GetFileDirectURL(fileID)
	if err != nil {
		return nil, fmt.Errorf("getting voice file URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating voice download request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading voice file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("voice file download returned status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading voice file: %w", err)
	}
	return data, nil
}

func (a *Adapter) Stop() error {
	a.bot.StopReceivingUpdates()
	return nil
}

// IsAllowed checks if a user ID is in the allowed list.
func (a *Adapter) IsAllowed(userID int64) bool {
	return a.allowedUsers[userID]
}
