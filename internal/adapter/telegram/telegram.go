package telegram

import (
	"context"
	"errors"
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
	"github.com/jamestelfer/telegold"
)

// VoiceOpts configures optional voice (STT/TTS) support for the adapter.
type VoiceOpts struct {
	STT            voice.STTProvider
	TTS            voice.TTSProvider
	TTSVoice       string
	AutoVoiceReply bool
}

// botSender is the adapter's outbound seam over *tgbotapi.BotAPI. It covers
// send and request only: update polling, file URLs and shutdown still go
// through Adapter.bot directly, because widening this interface over all of
// BotAPI would buy nothing and cost a large fake.
type botSender interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}

// unavailableSender stands in for a missing bot. Assigning a nil
// *tgbotapi.BotAPI to a botSender would produce a non-nil interface holding a
// nil pointer, which panics on the first outbound call; failing with an error
// instead keeps that hazard out of the send paths.
type unavailableSender struct{}

func (unavailableSender) Send(tgbotapi.Chattable) (tgbotapi.Message, error) {
	return tgbotapi.Message{}, errNoBot
}

func (unavailableSender) Request(tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return nil, errNoBot
}

var errNoBot = errors.New("telegram bot is not configured")

// senderFor returns the outbound seam for a bot, tolerating a nil bot.
func senderFor(bot *tgbotapi.BotAPI) botSender {
	if bot == nil {
		return unavailableSender{}
	}
	return bot
}

type Adapter struct {
	bot              *tgbotapi.BotAPI
	sender           botSender
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

	logger.Info("telegram bot authorized", "username", bot.Self.UserName)

	// Register built-in slash commands so they appear in the Telegram command menu.
	cmds := builtinCommands()
	if _, err := bot.Request(tgbotapi.NewSetMyCommands(cmds...)); err != nil {
		logger.Warn("failed to register bot commands", "error", err)
	}

	a := newWithSender(bot, allowedUsers, logger, voiceOpts)
	a.bot = bot

	return a, nil
}

// newWithBot creates an adapter with a pre-configured bot (for testing).
func newWithBot(bot *tgbotapi.BotAPI, allowedUsers []int64, logger *slog.Logger, voiceOpts *VoiceOpts) *Adapter {
	a := newWithSender(senderFor(bot), allowedUsers, logger, voiceOpts)
	a.bot = bot
	return a
}

// newWithSender creates an adapter whose outbound calls go through the given
// seam. Adapter.bot is left nil, so only the send/request paths are usable.
func newWithSender(sender botSender, allowedUsers []int64, logger *slog.Logger, voiceOpts *VoiceOpts) *Adapter {
	allowed := make(map[int64]bool, len(allowedUsers))
	for _, uid := range allowedUsers {
		allowed[uid] = true
	}
	a := &Adapter{
		sender:       sender,
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
	if _, err := a.sender.Request(tgbotapi.NewSetMyCommands(cmds...)); err != nil {
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
				_, _ = a.sender.Send(tgbotapi.NewChatAction(update.Message.Chat.ID, tgbotapi.ChatTyping))
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
	_, err = a.sender.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
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
			_, _ = a.sender.Send(reply)
			return adapter.IncomingMessage{}, false
		}
		transcribed, err := a.stt.Transcribe(ctx, audioData, "ogg")
		if err != nil {
			a.logger.Error("transcribing voice message", "error", err)
			reply := tgbotapi.NewMessage(tgMsg.Chat.ID, "Sorry, I couldn't transcribe your voice message. Please try sending it as text.")
			_, _ = a.sender.Send(reply)
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
			if _, sendErr := a.sender.Send(voiceMsg); sendErr != nil {
				return fmt.Errorf("sending voice message: %w", sendErr)
			}
			return nil
		}
	}

	// Text reply (default path).
	_, err = a.sendText(chatID, msg)
	return err
}

// parseModeHTML is Telegram's HTML parse mode identifier.
const parseModeHTML = "HTML"

// MessageChunkLimit is the largest chunk the adapter will send, in bytes of
// rendered HTML.
//
// Telegram's own cap is 4096 characters of the entity-stripped text, counted in
// UTF-16 code units. Counting raw HTML bytes over-counts against that, which is
// conservative and costs only the occasional extra message.
//
// Exported because the dispatcher's activity log builds Telegram HTML and chunks
// it itself, against this same number. Sharing the constant keeps the two from
// drifting apart, which is all it does — they previously carried the same literal
// with nothing tying them together.
//
// It does not make the two budgets equivalent, and nothing here should be read as
// saying the activity log is bounded because it shares this constant. That path
// measures its accumulated lines, then appends an approval section budgeted in
// unescaped runes which HTML escaping can expand several times over, and it
// forwards the result with an explicit parse mode — which this adapter sends
// unchunked by contract. A single message over Telegram's cap is the outcome, and
// the fix belongs where the length is decided, not here.
const MessageChunkLimit = 3500

// telegramMessageCap is Telegram's own hard limit on a single message, in
// characters of the entity-stripped text.
//
// Distinct from MessageChunkLimit, which is the deliberately conservative bound
// the chunker splits on. This one is the API's documented ceiling, and it is
// used to answer a different question: whether a given string has any chance of
// going out as one message at all.
const telegramMessageCap = 4096

// plainChunkLimit is the largest plain-text message the adapter will send, in
// characters — the unit telegramMessageCap is expressed in.
//
// The same headroom as MessageChunkLimit, applied to a different measure: that
// one bounds raw HTML in bytes, where over-counting against Telegram's
// entity-stripped cap is conservative. Plain text goes out as written, so
// characters are what it has to be counted in.
const plainChunkLimit = MessageChunkLimit

// maxOutboundChunks bounds how many messages one reply may become.
//
// sendChunks issues one API call per chunk, and Telegram rate-limits per chat
// at roughly twenty messages a minute. Ten keeps any single reply comfortably
// inside that, and ten chunks is already 35 KB of rendered HTML — far more than
// a reply that is merely long. Exceeding it means the chunker is producing
// chunks that carry very little text each, which heavily-tagged content can do
// because chunk length is counted in raw HTML bytes.
const maxOutboundChunks = 10

// maxRenderInput bounds how much text the adapter will parse as CommonMark, in
// bytes.
//
// goldmark's parse cost is quadratic in nesting depth: 32 KB of nested
// blockquote markers takes about half a second, 64 KB one and three quarter
// seconds, 200 KB seventeen. The renderer's own walk is linear — the cost is all
// in the parse — and the text is model output, so nothing upstream guarantees
// its shape. Without a bound, one reply can occupy a core for that long.
//
// The bound is what a reply can actually be delivered as: maxOutboundChunks
// messages of MessageChunkLimit each. Beyond it capChunks discards the surplus
// anyway, so the extra parse buys output that is thrown away. Markup can shrink
// under rendering — a page of link reference definitions renders to almost
// nothing — so this is not a proof that nothing is lost, and such a reply
// degrades to plain text. Bounded CPU on the send path is worth more than
// formatting for a reply already ten times longer than one that can be sent.
const maxRenderInput = maxOutboundChunks * MessageChunkLimit

// truncationNotice is appended to the last message of a reply that was cut
// short. Plain text: it rides on a chunk sent with HTML parse mode, so it must
// carry nothing that needs escaping.
const truncationNotice = "[reply truncated: too long to send]"

// sendText renders msg's text and delivers it, returning the message ID of the
// last message sent. It is the shared body of Send and SendAndGetID, so the two
// cannot drift in how they render, chunk, fall back or abort.
//
// An ID of zero with no error means the text rendered to nothing and no message
// was sent.
func (a *Adapter) sendText(chatID int64, msg adapter.OutgoingMessage) (int, error) {
	base := tgbotapi.NewMessage(chatID, msg.Text)
	base.DisableNotification = msg.Silent

	var markup interface{}
	if len(msg.Buttons) > 0 {
		rows := buildButtonRows(msg.Buttons, msg.ButtonLayout)
		markup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	}

	// An explicit parse mode is the caller's own markup — the activity log
	// builds pre-escaped HTML and chunks it itself — so it is forwarded
	// unrendered, unchunked and unretried.
	if msg.ParseMode != "" {
		base.ParseMode = msg.ParseMode
		base.ReplyMarkup = markup
		return a.sendOne(base)
	}

	// Past maxRenderInput the text is not parsed at all. This is the same exit
	// R29 takes, for the same reason: the rendered form is not available, so the
	// reply goes out as plain text rather than not at all.
	if len(msg.Text) > maxRenderInput {
		a.logger.Warn("reply too long to render, sending as plain text",
			"bytes", len(msg.Text), "limit", maxRenderInput)
		return a.sendPlain(base, markup)
	}

	// Default path: the text is CommonMark. Render it to Telegram's HTML subset
	// locally rather than asking Telegram's legacy Markdown parser to do it.
	chunks, renderErr := renderChunks(msg.Text)
	if renderErr != nil {
		a.logger.Debug("render failed, sending as plain text", "error", renderErr)
		return a.sendPlain(base, markup)
	}
	// A render that produces nothing from a reply that has something in it is a
	// message that would otherwise disappear: no send, no error, and callers that
	// discard the error have no way to notice. It is the same predicament R29
	// answers — the rendered form cannot carry the reply — so it takes the same
	// exit rather than a silent one. A reply that is genuinely blank renders to
	// nothing for the ordinary reason and is not a message at all.
	if len(chunks) == 0 {
		if strings.TrimSpace(msg.Text) == "" {
			// A blank reply is not a message and nothing is lost by not sending
			// it — unless it was carrying a keyboard. Buttons ride on a chunk, so
			// with no chunk there is nowhere to put them, and Telegram rejects a
			// message with empty text, so no send would deliver them either.
			// Inventing text to carry them is not this layer's call to make, so
			// the error is the remedy: an approval whose buttons never arrive
			// waits for a click that cannot happen, and silence is what makes
			// that undiagnosable.
			if len(msg.Buttons) > 0 {
				return 0, fmt.Errorf("telegram: refusing to drop %d button(s) on a blank message", len(msg.Buttons))
			}
			return 0, nil
		}
		a.logger.Debug("render produced no output, sending as plain text")
		return a.sendPlain(base, markup)
	}

	chunks = a.capChunks(chunks)

	html := base
	html.ParseMode = parseModeHTML
	id, delivered, err := a.sendChunks(html, markup, chunks)
	if err == nil {
		return id, nil
	}

	// Where R30's retry ends and R31 begins is decided by what already reached
	// the chat, not by how many chunks the reply happened to split into. The
	// retry re-sends the whole reply as one message, so it is only safe while
	// nothing has landed: once a chunk has arrived, repeating it would leave the
	// reader with a duplicated fragment, which reads worse than the visible
	// failure R31 asks for.
	//
	// Length is no longer part of this decision. The retry splits the original
	// markdown the same way the HTML attempt was split, so a source over
	// Telegram's cap is no longer a certain rejection — it is simply a retry of
	// several messages instead of one.
	if delivered > 0 {
		return 0, err
	}

	// Only a rejection is answered by dropping the parse mode. A timeout, a
	// cancellation or a 429 says nothing about the markup, and re-sending the
	// whole reply unformatted would spend its formatting permanently on a fault
	// that was never about parsing — while issuing the calls a second time into a
	// chat that, when the fault is a 429, is already over its limit.
	if !isRejection(err) {
		return 0, err
	}

	// Telegram rejected the HTML and nothing is on screen yet. Retry the original
	// markdown once with no parse mode — never a half-rendered string.
	a.logger.Debug("html send failed, retrying as plain text", "error", err)
	return a.sendPlain(base, markup)
}

// isRejection reports whether err is Telegram refusing the request as malformed,
// as opposed to failing to carry it out.
//
// A 400 is the API's answer to a bad request, which is what an HTML body it
// cannot parse is. It is not exclusively a parse failure — "message is too long"
// is also a 400 — but plain text is the right answer to both, and the chunker
// makes the length case one the send path should no longer reach. Every other
// class, including 429 and 5xx, is Telegram declining to act on a request it
// understood.
func isRejection(err error) bool {
	var apiErr *tgbotapi.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Code == http.StatusBadRequest
}

// capChunks bounds a reply to maxOutboundChunks messages, marking the last one
// so the truncation is visible to the reader rather than silent.
//
// The notice is appended rather than sent as an extra message: an extra message
// is one more call against the rate limit this cap exists to protect, and a
// chunk has headroom for it — the chunker splits at MessageChunkLimit, well
// under Telegram's own cap.
func (a *Adapter) capChunks(chunks []string) []string {
	if len(chunks) <= maxOutboundChunks {
		return chunks
	}
	a.logger.Warn("reply truncated: too many chunks to send",
		"chunks", len(chunks), "limit", maxOutboundChunks)

	capped := chunks[:maxOutboundChunks]
	last := len(capped) - 1
	capped[last] += "\n\n" + truncationNotice
	return capped
}

// sendChunks sends one Telegram message per chunk, in source order, and returns
// the final chunk's message ID. The inline keyboard rides on the final chunk
// only: buttons under a mid-message fragment read as a broken message.
//
// The parse mode comes from base and is not set here, so the same loop carries
// the rendered HTML and the plain-text fallback. Each caller decides on its own
// copy of base, which keeps the fallback structurally incapable of going out as
// HTML.
//
// The second return is how many chunks actually reached the chat. On failure it
// is what tells the caller whether the reply is still recoverable — a failure on
// the first chunk left the conversation untouched, a later one did not — so the
// error alone is not enough to decide.
func (a *Adapter) sendChunks(base tgbotapi.MessageConfig, markup interface{}, chunks []string) (id, delivered int, err error) {
	for i, chunk := range chunks {
		out := base
		out.Text = chunk
		out.ReplyMarkup = nil
		if i == len(chunks)-1 {
			out.ReplyMarkup = markup
		}
		// Telegram notifies per message, but the reply is one event however many
		// messages carry it. Only the first chunk announces it — a reply that
		// split ten ways would otherwise ping ten times. Silencing the rest can
		// only add silence, so a caller that asked for none still gets none.
		if i > 0 {
			out.DisableNotification = true
		}
		sent, sendErr := a.sender.Send(out)
		if sendErr != nil {
			return 0, i, fmt.Errorf("sending telegram message chunk %d of %d: %w", i+1, len(chunks), sendErr)
		}
		id = sent.MessageID
	}
	return id, len(chunks), nil
}

// sendPlain sends base with no parse mode — the fallback both R29 and R30 land
// on — splitting it across messages when it is too long for one.
//
// The split is what makes this a fallback rather than a second failure. Without
// it a reply over Telegram's cap was handed over whole and rejected on sight,
// so the message the renderer could not format was not delivered at all. Prose
// containing something CommonMark reads as raw HTML — `Vec<T>`, `<Enter>` —
// takes this exit routinely, so the case is ordinary rather than exotic.
//
// base.Text is still the original markdown: sendText builds it from msg.Text
// and sendChunks mutates only its own copy, so the fallback cannot carry a
// half-rendered string.
func (a *Adapter) sendPlain(base tgbotapi.MessageConfig, markup interface{}) (int, error) {
	base.ParseMode = ""

	chunks, err := telegold.ChunkPlain(base.Text, plainChunkLimit)
	if err != nil {
		return 0, fmt.Errorf("chunking telegram plain text: %w", err)
	}
	if len(chunks) <= 1 {
		base.ReplyMarkup = markup
		return a.sendOne(base)
	}

	id, _, err := a.sendChunks(base, markup, a.capChunks(chunks))
	return id, err
}

func (a *Adapter) sendOne(out tgbotapi.MessageConfig) (int, error) {
	sent, err := a.sender.Send(out)
	if err != nil {
		return 0, fmt.Errorf("sending telegram message: %w", err)
	}
	return sent.MessageID, nil
}

// renderChunks renders outgoing markdown to Telegram-HTML chunks, each within
// the message limit and each independently tag-balanced.
func renderChunks(text string) ([]string, error) {
	blocks, err := telegold.Render([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("rendering markdown to telegram html: %w", err)
	}
	chunks, err := telegold.Chunk(blocks, MessageChunkLimit)
	if err != nil {
		return nil, fmt.Errorf("chunking telegram html: %w", err)
	}
	return chunks, nil
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
		_, _ = a.sender.Request(answer)
		return
	}
	if responseText == "" {
		answer := tgbotapi.NewCallback(cq.ID, "")
		_, _ = a.sender.Request(answer)
		return // unknown callback, silently ignore
	}

	debug := a.IsDebug(chatID)

	if debug {
		// Debug mode: answer callback, strip keyboard, send separate message
		// (original verbose behaviour).
		answer := tgbotapi.NewCallback(cq.ID, "")
		if _, err := a.sender.Request(answer); err != nil {
			a.logger.Warn("failed to answer callback query", "error", err)
		}

		edit := tgbotapi.NewEditMessageReplyMarkup(
			chatID,
			cq.Message.MessageID,
			tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}},
		)
		if _, editErr := a.sender.Request(edit); editErr != nil {
			a.logger.Debug("failed to remove inline keyboard from message", "error", editErr)
		}

		reply := tgbotapi.NewMessage(chatID, responseText)
		if _, err := a.sender.Send(reply); err != nil {
			a.logger.Warn("failed to send callback response", "chat_id", chatID, "error", err)
		}
		return
	}

	// Compact mode: the activity log message holds tool history plus the
	// pending approval section + buttons. Stripping the inline keyboard is
	// enough — subsequent tool_start / tool_end / denied events from the
	// engine will rewrite the message text via the activity log's edit path.
	answer := tgbotapi.NewCallback(cq.ID, "")
	if _, err := a.sender.Request(answer); err != nil {
		a.logger.Warn("failed to answer callback query", "error", err)
	}

	stripKeyboard := tgbotapi.NewEditMessageReplyMarkup(
		chatID,
		cq.Message.MessageID,
		tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}},
	)
	if _, editErr := a.sender.Request(stripKeyboard); editErr != nil {
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
	if _, err := a.sender.Send(reply); err != nil {
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

	// Shared with Send: same rendering, chunking, fallback and abort behaviour.
	// A multi-chunk reply returns the final chunk's ID, because callers edit the
	// message they were handed and an edit must land on the tail of the reply.
	id, err := a.sendText(chatID, msg)
	if err != nil {
		return "", err
	}
	if id == 0 {
		return "", fmt.Errorf("telegram message %q rendered to no content", msg.ExternalID)
	}
	return strconv.Itoa(id), nil
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
	if _, editErr := a.sender.Request(edit); editErr != nil {
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
	if _, editErr := a.sender.Request(edit); editErr != nil {
		return fmt.Errorf("editing telegram message: %w", editErr)
	}
	return nil
}

// IsAllowed checks if a user ID is in the allowed list.
func (a *Adapter) IsAllowed(userID int64) bool {
	return a.allowedUsers[userID]
}
