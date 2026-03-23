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
	bot            *tgbotapi.BotAPI
	allowedUsers   map[int64]bool
	logger         *slog.Logger
	stt            voice.STTProvider
	tts            voice.TTSProvider
	ttsVoice       string
	autoVoiceReply bool
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
