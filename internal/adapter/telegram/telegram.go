package telegram

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	debugChats       map[int64]bool           // per-chat debug toggle
	debugMu          sync.RWMutex
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

	// Register built-in slash commands so they appear in the Telegram command menu.
	cmds := builtinCommands()
	if _, err := bot.Request(tgbotapi.NewSetMyCommands(cmds...)); err != nil {
		logger.Warn("failed to register bot commands", "error", err)
	}

	a := &Adapter{
		bot:          bot,
		allowedUsers: allowed,
		logger:       logger,
		debugChats:   make(map[int64]bool),
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
		debugChats:   make(map[int64]bool),
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

// SkillCommand represents a skill's command trigger for registration.
type SkillCommand struct {
	Command     string
	Description string
}

// RegisterSkillCommands updates the Telegram bot's command menu to include
// both the built-in commands and the provided skill command triggers.
// Call this after adapter construction and skill loading, before Start.
func (a *Adapter) RegisterSkillCommands(skillCmds []SkillCommand) {
	var tgCmds []tgbotapi.BotCommand
	for _, sc := range skillCmds {
		tgCmds = append(tgCmds, tgbotapi.BotCommand{
			Command:     sc.Command,
			Description: sc.Description,
		})
	}
	cmds := buildBotCommands(tgCmds)
	if _, err := a.bot.Request(tgbotapi.NewSetMyCommands(cmds...)); err != nil {
		a.logger.Warn("failed to register bot commands with skills", "error", err)
	} else {
		a.logger.Info("registered bot commands", "builtin", len(builtinCommands()), "skill", len(skillCmds), "total", len(cmds))
	}
}

// builtinCommands returns the hardcoded set of built-in bot commands.
func builtinCommands() []tgbotapi.BotCommand {
	return []tgbotapi.BotCommand{
		{Command: "start", Description: "Start a conversation"},
		{Command: "help", Description: "Show help and available commands"},
		{Command: "debug", Description: "Toggle verbose approval messages"},
		{Command: "stop", Description: "Cancel the current request"},
		{Command: "panic", Description: "Emergency stop all processing"},
		{Command: "resume", Description: "Resume after emergency stop"},
		{Command: "clear", Description: "Clear session history"},
		{Command: "compact", Description: "Compact session into summary"},
	}
}

// buildBotCommands merges built-in commands with skill commands, skipping
// duplicates and truncating descriptions to Telegram's 256-char limit.
func buildBotCommands(skillCmds []tgbotapi.BotCommand) []tgbotapi.BotCommand {
	builtin := builtinCommands()
	seen := make(map[string]bool, len(builtin))
	for _, c := range builtin {
		seen[c.Command] = true
	}

	for _, sc := range skillCmds {
		if seen[sc.Command] {
			continue
		}
		seen[sc.Command] = true
		desc := sc.Description
		if len(desc) > 256 {
			desc = desc[:253] + "..."
		}
		builtin = append(builtin, tgbotapi.BotCommand{
			Command:     sc.Command,
			Description: desc,
		})
	}
	return builtin
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

			// Handle /debug command locally — toggle per-chat debug mode.
			if update.Message.IsCommand() && update.Message.Command() == "debug" {
				a.handleDebugCommand(update.Message.Chat.ID)
				continue
			}

			// Skip typing indicator for control commands handled by the dispatcher.
			if !isControlCommand(update.Message.Text) {
				_, _ = a.bot.Send(tgbotapi.NewChatAction(update.Message.Chat.ID, tgbotapi.ChatTyping))
			}

			msg, ok := a.buildIncomingMessage(ctx, update.Message)
			if !ok {
				continue
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

// buildIncomingMessage extracts text (or transcribes voice) from a Telegram
// message and returns an IncomingMessage. Returns false if the message should
// be skipped (empty text or transcription failure).
func (a *Adapter) buildIncomingMessage(ctx context.Context, tgMsg *tgbotapi.Message) (adapter.IncomingMessage, bool) {
	var text string
	var isVoice bool

	if tgMsg.Voice != nil && a.stt != nil {
		audioData, err := a.downloadVoiceFile(ctx, tgMsg.Voice.FileID)
		if err != nil {
			a.logger.Error("downloading voice file", "error", err)
			reply := tgbotapi.NewMessage(tgMsg.Chat.ID, "Sorry, I couldn't download your voice message. Please try again.")
			_, _ = a.bot.Send(reply)
			return adapter.IncomingMessage{}, false
		}
		transcribed, err := a.stt.Transcribe(ctx, audioData, "ogg")
		if err != nil {
			a.logger.Error("transcribing voice message", "error", err)
			reply := tgbotapi.NewMessage(tgMsg.Chat.ID, "Sorry, I couldn't transcribe your voice message. Please try sending it as text.")
			_, _ = a.bot.Send(reply)
			return adapter.IncomingMessage{}, false
		}
		text = transcribed
		isVoice = true
		a.logger.Info("voice message transcribed",
			"duration", tgMsg.Voice.Duration,
			"text_len", len(text),
		)
	} else {
		text = tgMsg.Text
	}

	if text == "" {
		return adapter.IncomingMessage{}, false
	}

	return adapter.IncomingMessage{
		Adapter:    "telegram",
		ExternalID: strconv.FormatInt(tgMsg.Chat.ID, 10),
		UserID:     strconv.FormatInt(tgMsg.From.ID, 10),
		UserName:   tgMsg.From.UserName,
		Text:       text,
		Timestamp:  time.Unix(int64(tgMsg.Date), 0),
		IsVoice:    isVoice,
	}, true
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
		rows := buildButtonRows(msg.Buttons, msg.ButtonLayout)
		tgMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	}

	// Use explicit parse mode if set, otherwise try Markdown with plain-text
	// fallback for LLM responses that may break Telegram's parser.
	if msg.ParseMode != "" {
		tgMsg.ParseMode = msg.ParseMode
		_, err = a.bot.Send(tgMsg)
	} else {
		tgMsg.ParseMode = "Markdown"
		_, err = a.bot.Send(tgMsg)
		if err != nil {
			a.logger.Debug("markdown send failed, retrying as plain text", "error", err)
			tgMsg.ParseMode = ""
			_, err = a.bot.Send(tgMsg)
		}
	}
	if err != nil {
		return fmt.Errorf("sending telegram message: %w", err)
	}

	return nil
}

// handleCallbackQuery processes an inline keyboard button click. It answers
// the Telegram callback query, then either edits the original message in-place
// (compact mode) or removes the keyboard and sends a separate confirmation
// message (debug mode).
func (a *Adapter) handleCallbackQuery(ctx context.Context, cq *tgbotapi.CallbackQuery) {
	if cq.Message == nil {
		return
	}
	chatID := cq.Message.Chat.ID

	// Ask the resolver what text to send back to the user.
	responseText, err := a.callbackResolver.Resolve(ctx, cq.Data)
	if err != nil {
		a.logger.Error("resolving callback query", "data", cq.Data, "error", err)
		// Still answer the callback to clear the loading spinner.
		answer := tgbotapi.NewCallback(cq.ID, "")
		_, _ = a.bot.Request(answer)
		return
	}
	if responseText == "" {
		answer := tgbotapi.NewCallback(cq.ID, "")
		_, _ = a.bot.Request(answer)
		return // unknown callback, silently ignore
	}

	debug := a.IsDebug(chatID)

	if debug {
		// Debug mode: answer callback, strip keyboard, send separate message
		// (original verbose behaviour).
		answer := tgbotapi.NewCallback(cq.ID, "")
		if _, err := a.bot.Request(answer); err != nil {
			a.logger.Warn("failed to answer callback query", "error", err)
		}

		edit := tgbotapi.NewEditMessageReplyMarkup(
			chatID,
			cq.Message.MessageID,
			tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}},
		)
		if _, editErr := a.bot.Request(edit); editErr != nil {
			a.logger.Debug("failed to remove inline keyboard from message", "error", editErr)
		}

		reply := tgbotapi.NewMessage(chatID, responseText)
		if _, err := a.bot.Send(reply); err != nil {
			a.logger.Warn("failed to send callback response", "chat_id", chatID, "error", err)
		}
		return
	}

	// Compact mode: the activity log message holds tool history plus the
	// pending approval section + buttons. Stripping the inline keyboard is
	// enough — subsequent tool_start / tool_end / denied events from the
	// engine will rewrite the message text via the activity log's edit path.
	answer := tgbotapi.NewCallback(cq.ID, "")
	if _, err := a.bot.Request(answer); err != nil {
		a.logger.Warn("failed to answer callback query", "error", err)
	}

	stripKeyboard := tgbotapi.NewEditMessageReplyMarkup(
		chatID,
		cq.Message.MessageID,
		tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}},
	)
	if _, editErr := a.bot.Request(stripKeyboard); editErr != nil {
		a.logger.Debug("failed to strip inline keyboard from approval message", "error", editErr)
	}
}

// buildButtonRows arranges buttons into rows according to the layout spec.
// Each element of layout specifies the number of buttons in that row.
// When layout is nil, each button gets its own row (legacy behaviour).
func buildButtonRows(buttons []adapter.KeyboardButton, layout []int) [][]tgbotapi.InlineKeyboardButton {
	if len(layout) == 0 {
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(buttons))
		for _, btn := range buttons {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(btn.Label, btn.CallbackData),
			))
		}
		return rows
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(layout))
	idx := 0
	for _, n := range layout {
		if idx >= len(buttons) {
			break
		}
		end := idx + n
		if end > len(buttons) {
			end = len(buttons)
		}
		row := make([]tgbotapi.InlineKeyboardButton, 0, n)
		for _, btn := range buttons[idx:end] {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(btn.Label, btn.CallbackData))
		}
		rows = append(rows, row)
		idx = end
	}
	return rows
}

// IsDebug reports whether the given chat has debug mode enabled.
func (a *Adapter) IsDebug(chatID int64) bool {
	a.debugMu.RLock()
	defer a.debugMu.RUnlock()
	return a.debugChats[chatID]
}

// IsDebugByExternalID is a convenience wrapper that parses an ExternalID string.
func (a *Adapter) IsDebugByExternalID(externalID string) bool {
	chatID, err := strconv.ParseInt(externalID, 10, 64)
	if err != nil {
		return false
	}
	return a.IsDebug(chatID)
}

// handleDebugCommand toggles debug mode for the originating chat and sends a
// confirmation message.
func (a *Adapter) handleDebugCommand(chatID int64) {
	a.debugMu.Lock()
	a.debugChats[chatID] = !a.debugChats[chatID]
	enabled := a.debugChats[chatID]
	a.debugMu.Unlock()

	var text string
	if enabled {
		text = "Debug mode enabled — approval messages will show full tool arguments and separate confirmation messages."
	} else {
		text = "Debug mode disabled — approval messages are now compact."
	}
	reply := tgbotapi.NewMessage(chatID, text)
	if _, err := a.bot.Send(reply); err != nil {
		a.logger.Warn("failed to send debug toggle response", "chat_id", chatID, "error", err)
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

// isControlCommand returns true for /stop, /panic, /resume — commands that
// are intercepted by the Dispatcher and don't need a typing indicator.
func isControlCommand(text string) bool {
	trimmed := strings.TrimSpace(text)
	return trimmed == "/stop" || trimmed == "/panic" || trimmed == "/resume"
}

// SendAndGetID sends a message and returns the Telegram message ID as a string.
// Implements adapter.MessageEditor.
func (a *Adapter) SendAndGetID(ctx context.Context, msg adapter.OutgoingMessage) (string, error) {
	chatID, err := strconv.ParseInt(msg.ExternalID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("parsing chat ID: %w", err)
	}

	tgMsg := tgbotapi.NewMessage(chatID, msg.Text)
	if msg.ParseMode != "" {
		tgMsg.ParseMode = msg.ParseMode
	} else {
		tgMsg.ParseMode = "Markdown"
	}

	if len(msg.Buttons) > 0 {
		rows := buildButtonRows(msg.Buttons, msg.ButtonLayout)
		tgMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	}

	sent, err := a.bot.Send(tgMsg)
	if err != nil && msg.ParseMode == "" {
		// Markdown failed, retry plain text.
		a.logger.Debug("markdown send failed, retrying as plain text", "error", err)
		tgMsg.ParseMode = ""
		sent, err = a.bot.Send(tgMsg)
	}
	if err != nil {
		return "", fmt.Errorf("sending telegram message: %w", err)
	}
	return strconv.Itoa(sent.MessageID), nil
}

// EditText edits an existing Telegram message. Implements adapter.MessageEditor.
func (a *Adapter) EditText(_ context.Context, externalID, messageID, text, parseMode string) error {
	chatID, err := strconv.ParseInt(externalID, 10, 64)
	if err != nil {
		return fmt.Errorf("parsing chat ID: %w", err)
	}
	msgID, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("parsing message ID: %w", err)
	}

	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	if parseMode != "" {
		edit.ParseMode = parseMode
	}
	if _, editErr := a.bot.Request(edit); editErr != nil {
		return fmt.Errorf("editing telegram message: %w", editErr)
	}
	return nil
}

// EditMessage edits both the text and inline keyboard of an existing Telegram
// message. Implements adapter.MessageEditor. When msg.Buttons is empty, the
// existing keyboard is removed.
func (a *Adapter) EditMessage(_ context.Context, externalID, messageID string, msg adapter.OutgoingMessage) error {
	chatID, err := strconv.ParseInt(externalID, 10, 64)
	if err != nil {
		return fmt.Errorf("parsing chat ID: %w", err)
	}
	msgID, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("parsing message ID: %w", err)
	}

	edit := tgbotapi.NewEditMessageText(chatID, msgID, msg.Text)
	if msg.ParseMode != "" {
		edit.ParseMode = msg.ParseMode
	}
	if len(msg.Buttons) > 0 {
		rows := buildButtonRows(msg.Buttons, msg.ButtonLayout)
		markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
		edit.ReplyMarkup = &markup
	} else {
		// Explicit empty keyboard removes any existing buttons.
		empty := tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}}
		edit.ReplyMarkup = &empty
	}
	if _, editErr := a.bot.Request(edit); editErr != nil {
		return fmt.Errorf("editing telegram message: %w", editErr)
	}
	return nil
}

// IsAllowed checks if a user ID is in the allowed list.
func (a *Adapter) IsAllowed(userID int64) bool {
	return a.allowedUsers[userID]
}
