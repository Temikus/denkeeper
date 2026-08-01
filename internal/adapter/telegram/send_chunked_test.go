package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/jamestelfer/telegold/tgtest"
)

// longReply builds a reply of n numbered lines, each its own markdown paragraph
// so it renders to its own block, with enough mixed formatting that the chunks
// carry real markup rather than plain text.
func longReply(n int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "Line %d: **bold** and `code_span` and a [link](https://example.com/a_%d).\n\n", i, i)
	}
	return sb.String()
}

// assertSentChunks applies the invariants every chunked send shares: HTML parse
// mode, nothing over the limit, valid UTF-8, and tag balance — asserted on the
// exact bytes handed to Telegram rather than on the chunker's return value, so
// anything the adapter does to a chunk after chunking it is covered too.
func assertSentChunks(t *testing.T, msgs []tgbotapi.MessageConfig) {
	t.Helper()
	for i, m := range msgs {
		if m.ParseMode != parseModeHTML {
			t.Errorf("chunk %d ParseMode = %q, want %s", i+1, m.ParseMode, parseModeHTML)
		}
		if len(m.Text) > MessageChunkLimit {
			t.Errorf("chunk %d is %d bytes, over the %d-byte limit", i+1, len(m.Text), MessageChunkLimit)
		}
		if !utf8.ValidString(m.Text) {
			t.Errorf("chunk %d is not valid UTF-8: %q", i+1, m.Text)
		}
		tgtest.AssertTagBalanced(t, m.Text)
	}
}

// TestSend_LongReplyIsSentAsSeveralMessagesInOrder covers R25's multi-chunk half
// and R17 at the adapter layer.
func TestSend_LongReplyIsSentAsSeveralMessagesInOrder(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       longReply(60),
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) < 2 {
		t.Fatalf("send count = %d, want several — a reply over the cap must chunk", len(msgs))
	}
	assertSentChunks(t, msgs)
	for i, m := range msgs {
		if m.ChatID != 12345 {
			t.Errorf("chunk %d ChatID = %d, want 12345", i+1, m.ChatID)
		}
	}
}

// TestSend_ChunkedReplyNotifiesOnce covers the notification budget chunking
// spends. A reply is one event to the reader however many messages it takes to
// deliver, and Telegram raises a notification per message, so copying the
// message's own notification setting onto every chunk turns one reply into a
// burst of pings. The first chunk carries it, because that is when the reply
// arrives.
func TestSend_ChunkedReplyNotifiesOnce(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       longReply(60),
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) < 2 {
		t.Fatalf("send count = %d, want several", len(msgs))
	}
	for i, m := range msgs {
		wantSilent := i > 0
		if m.DisableNotification != wantSilent {
			t.Errorf("chunk %d DisableNotification = %v, want %v — only the first chunk announces the reply",
				i+1, m.DisableNotification, wantSilent)
		}
	}
}

// TestSend_SilentChunkedReplyNotifiesNotAtAll keeps the caller's own choice
// authoritative: silencing the trailing chunks must not un-silence the first.
func TestSend_SilentChunkedReplyNotifiesNotAtAll(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       longReply(60),
		Silent:     true,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) < 2 {
		t.Fatalf("send count = %d, want several", len(msgs))
	}
	for i, m := range msgs {
		if !m.DisableNotification {
			t.Errorf("chunk %d notified, want every chunk of a silent reply silent", i+1)
		}
	}
}

// TestSend_ReplyOverTheRenderLimitSkipsRendering bounds the parse cost of the
// send path. goldmark's parse is quadratic in nesting depth — 64 KB of nested
// blockquote markers takes over a second, 200 KB takes seventeen — and the text
// is model output, so its shape is not the adapter's to trust.
//
// The limit is asserted through the path taken rather than the time spent: past
// it the text is never parsed, so the send goes out with no parse mode. A timing
// bound would measure the machine as much as the code.
func TestSend_ReplyOverTheRenderLimitSkipsRendering(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	// Nested blockquote markers: the shape whose parse cost is quadratic, so a
	// regression here is expensive rather than merely wrong.
	text := strings.Repeat("> ", maxRenderInput) + "x"
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       text,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) == 0 {
		t.Fatal("nothing was sent — an oversized reply must still be delivered")
	}
	for i, m := range msgs {
		if m.ParseMode != "" {
			t.Errorf("chunk %d ParseMode = %q, want none — an over-limit reply is not rendered",
				i+1, m.ParseMode)
		}
	}
}

// TestSend_ReplyAtTheRenderLimitIsStillRendered pins the other side of the
// boundary, so the limit cannot quietly grow into a rule that stops rendering
// ordinary long replies.
func TestSend_ReplyAtTheRenderLimitIsStillRendered(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	text := "**b** " + strings.Repeat("a", maxRenderInput-len("**b** "))
	if len(text) != maxRenderInput {
		t.Fatalf("fixture is %d bytes, want exactly the limit %d", len(text), maxRenderInput)
	}
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       text,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) == 0 {
		t.Fatal("nothing was sent")
	}
	if msgs[0].ParseMode != parseModeHTML {
		t.Errorf("ParseMode = %q, want %s — text at the limit is still rendered",
			msgs[0].ParseMode, parseModeHTML)
	}
}

// TestSend_TransportFailureDoesNotDowngradeToPlainText draws the line R30 draws:
// its retry answers Telegram *rejecting* the HTML, which is a 400. A timeout, a
// cancelled context or a 429 says nothing about the markup, and answering one by
// re-sending the whole reply unformatted spends the formatting permanently on a
// fault that had nothing to do with parsing — and issues the sends again into a
// chat that, in the 429 case, is already over its rate limit.
func TestSend_TransportFailureDoesNotDowngradeToPlainText(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"network", errors.New("dial tcp: i/o timeout")},
		{"rate limited", &tgbotapi.Error{Code: 429, Message: "Too Many Requests: retry after 30"}},
		{"server error", &tgbotapi.Error{Code: 500, Message: "Internal Server Error"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bot := newFakeBot().failOnSend(1)
			bot.failErr = tc.err
			a := newWithSender(bot, nil, testLogger(), nil)

			err := a.Send(context.Background(), adapter.OutgoingMessage{
				ExternalID: "12345",
				Text:       "a **bold** reply",
			})
			if err == nil {
				t.Fatal("Send returned nil — a failure that is not a rejection must surface")
			}
			if got := bot.sendCount(); got != 1 {
				t.Errorf("send count = %d, want 1 — no plain-text retry for %v", got, tc.err)
			}
		})
	}
}

// TestSend_BlankTextWithButtonsIsAnError guards the one case where sending
// nothing is not a harmless outcome. A blank reply renders to no chunks and is
// correctly not sent — it is not a message. But an inline keyboard rides on a
// chunk, so with no chunk there is nowhere to put it, and an approval prompt
// whose buttons never arrive leaves the approval waiting for a click that cannot
// happen.
//
// Telegram rejects a message with empty text, so there is no send that would
// deliver the keyboard and nothing here may invent text to carry it. The error
// is the whole remedy: it reaches a caller that can report it.
func TestSend_BlankTextWithButtonsIsAnError(t *testing.T) {
	for _, text := range []string{"", "   ", "\n\n", "\t \n"} {
		t.Run(fmt.Sprintf("%q", text), func(t *testing.T) {
			bot := newFakeBot()
			a := newWithSender(bot, nil, testLogger(), nil)

			err := a.Send(context.Background(), adapter.OutgoingMessage{
				ExternalID: "12345",
				Text:       text,
				Buttons:    []adapter.KeyboardButton{{Label: "Approve", CallbackData: "approve"}},
			})
			if err == nil {
				t.Error("Send returned nil — a keyboard with nowhere to go must surface")
			}
			if got := bot.sendCount(); got != 0 {
				t.Errorf("send count = %d, want 0 — Telegram rejects empty text", got)
			}
		})
	}
}

// TestSend_BlankTextWithoutButtonsIsNotAMessage keeps the ordinary blank reply
// silent. Nothing is lost by not sending it, and it is the counterpart that stops
// the keyboard guard from turning every empty turn into an error.
func TestSend_BlankTextWithoutButtonsIsNotAMessage(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "   ",
	}); err != nil {
		t.Errorf("Send: %v — a blank reply with no keyboard is not an error", err)
	}
	if got := bot.sendCount(); got != 0 {
		t.Errorf("send count = %d, want 0", got)
	}
}

// TestSend_LongReplyReassemblesExactly is the test that matters for chunking. It
// concatenates the captured sends in call order and asserts every ordinal is
// present exactly once and in ascending order — the two failure modes a reader
// scrolling a long thread is worst at spotting.
func TestSend_LongReplyReassemblesExactly(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	const lines = 60
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       longReply(lines),
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var all strings.Builder
	for _, m := range bot.messages(t) {
		all.WriteString(m.Text)
	}
	reassembled := all.String()

	prev := -1
	for i := 1; i <= lines; i++ {
		marker := fmt.Sprintf("Line %d:", i)
		at := strings.Index(reassembled, marker)
		if at < 0 {
			t.Fatalf("%q is missing from the reassembled reply", marker)
		}
		if n := strings.Count(reassembled, marker); n != 1 {
			t.Errorf("%q appears %d times, want exactly once", marker, n)
		}
		if at < prev {
			t.Errorf("%q appears at %d, before the previous line at %d — order was not preserved", marker, at, prev)
		}
		prev = at
	}
	// The formatting must survive chunking too, not just the text.
	if !strings.Contains(reassembled, "<b>bold</b>") {
		t.Error("reassembled reply lost its bold markup")
	}
	if !strings.Contains(reassembled, "<code>code_span</code>") {
		t.Error("reassembled reply lost its code spans")
	}
}

// TestSend_ButtonsRideOnTheFinalChunkOnly covers R26. Buttons under a
// mid-message fragment read as a broken message.
func TestSend_ButtonsRideOnTheFinalChunkOnly(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID:   "12345",
		Text:         longReply(60),
		Buttons:      []adapter.KeyboardButton{{Label: "Approve", CallbackData: "ok"}},
		ButtonLayout: []int{1},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) < 2 {
		t.Fatalf("send count = %d, want several", len(msgs))
	}
	for i, m := range msgs[:len(msgs)-1] {
		if m.ReplyMarkup != nil {
			t.Errorf("chunk %d carries a keyboard; only the final chunk may", i)
		}
	}
	markup, ok := msgs[len(msgs)-1].ReplyMarkup.(tgbotapi.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("final chunk ReplyMarkup = %T, want tgbotapi.InlineKeyboardMarkup", msgs[len(msgs)-1].ReplyMarkup)
	}
	if len(markup.InlineKeyboard) != 1 || markup.InlineKeyboard[0][0].Text != "Approve" {
		t.Errorf("final chunk keyboard = %v, want one Approve button", markup.InlineKeyboard)
	}
}

// TestSendAndGetID_MultiChunkReturnsTheFinalChunkID covers R27. The activity
// log edits the message it was handed, and an edit must land on the tail of the
// reply, not on a fragment in the middle of it.
func TestSendAndGetID_MultiChunkReturnsTheFinalChunkID(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	id, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       longReply(60),
	})
	if err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}

	sent := bot.sendCount()
	if sent < 2 {
		t.Fatalf("send count = %d, want several", sent)
	}
	// The fake advances its ID on every accepted send, so the final chunk's ID
	// is the first ID plus one per preceding chunk.
	want := strconv.Itoa(firstFakeMessageID + sent - 1)
	if id != want {
		t.Errorf("returned message ID = %q, want the final chunk's %q", id, want)
	}
}

// fourChunkReply is long enough to render to four chunks, so a failure can be
// injected mid-sequence with chunks still queued behind it.
func fourChunkReply(t *testing.T) string {
	t.Helper()
	src := longReply(120)
	chunks, err := renderChunks(src)
	if err != nil {
		t.Fatalf("renderChunks: %v", err)
	}
	if len(chunks) != 4 {
		t.Fatalf("fixture renders to %d chunks, want 4", len(chunks))
	}
	return src
}

// TestSend_FailedChunkAbortsTheRemainder covers R31. A gap in the middle of a
// reply is worse than a truncated one, so there is no partial recovery and no
// best-effort continuation.
func TestSend_FailedChunkAbortsTheRemainder(t *testing.T) {
	bot := newFakeBot().failOnSend(2)
	a := newWithSender(bot, nil, testLogger(), nil)

	err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       fourChunkReply(t),
	})
	if err == nil {
		t.Fatal("expected the chunk failure to surface as an error")
	}
	if got := bot.sendCount(); got != 2 {
		t.Errorf("send count = %d, want exactly 2 of 4 — the remaining chunks must be abandoned", got)
	}
}

// TestSendAndGetID_FailedChunkAbortsTheRemainder is R31 on the other send path.
func TestSendAndGetID_FailedChunkAbortsTheRemainder(t *testing.T) {
	bot := newFakeBot().failOnSend(2)
	a := newWithSender(bot, nil, testLogger(), nil)

	id, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       fourChunkReply(t),
	})
	if err == nil {
		t.Fatal("expected the chunk failure to surface as an error")
	}
	if id != "" {
		t.Errorf("returned message ID = %q, want empty on failure", id)
	}
	if got := bot.sendCount(); got != 2 {
		t.Errorf("send count = %d, want exactly 2", got)
	}
}

// TestSend_RateLimitedChunkAbortsWithoutBackoff records the rate-limit decision.
//
// Telegram documents roughly one message per second per chat with bursts
// tolerated, and a chunked reply is a handful of messages, so tripping the limit
// is unlikely. When it happens R31 applies unchanged: abort. A bounded backoff
// would leave a reply half-delivered and then resume after an arbitrary pause,
// which reads worse than a visible failure the operator can see and retry.
func TestSend_RateLimitedChunkAbortsWithoutBackoff(t *testing.T) {
	bot := newFakeBot().failOnSend(3)
	bot.failErr = &tgbotapi.Error{
		Code:    429,
		Message: "Too Many Requests: retry after 5",
	}
	a := newWithSender(bot, nil, testLogger(), nil)

	err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       fourChunkReply(t),
	})
	if err == nil {
		t.Fatal("expected the 429 to surface as an error")
	}
	if got := bot.sendCount(); got != 3 {
		t.Errorf("send count = %d, want exactly 3 — a 429 is not retried or backed off", got)
	}
}

// TestSend_SingleChunkReplyStaysOneMessage is the common case, and the one a
// chunker most easily regresses: no extra empty message, no trailing separator.
func TestSend_SingleChunkReplyStaysOneMessage(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "A short reply.\n\nWith two paragraphs.",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("send count = %d, want 1: %v", len(msgs), msgs)
	}
	want := "A short reply.\n\nWith two paragraphs."
	if msgs[0].Text != want {
		t.Errorf("Text = %q, want %q", msgs[0].Text, want)
	}
}

// TestSend_EmptyRenderSendsNothing keeps a reply that renders to no blocks from
// becoming an empty Telegram message, which the API rejects.
func TestSend_EmptyRenderSendsNothing(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "   \n\n  \n",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := bot.sendCount(); got != 0 {
		t.Errorf("send count = %d, want 0 — there is nothing to send", got)
	}
}

// TestSendAndGetID_EmptyRenderIsAnError is the other half of sending nothing.
// Send has nothing to do, but a caller asking for an identifier needs one, and
// an empty string would be used to edit a message that does not exist.
func TestSendAndGetID_EmptyRenderIsAnError(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	id, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "   \n\n  \n",
	})
	if err == nil {
		t.Fatal("expected an error when there is no content to send")
	}
	if id != "" {
		t.Errorf("returned message ID = %q, want empty", id)
	}
	if got := bot.sendCount(); got != 0 {
		t.Errorf("send count = %d, want 0", got)
	}
}

// TestSend_OversizedReplyRetriesAsPlainChunks covers the retry on a reply too
// long for one message.
//
// Nothing was delivered — the very first chunk failed — so R31 has no partial
// delivery to protect and R30's retry applies. It used to be skipped here on
// the grounds that a source several times Telegram's cap could only be rejected
// again; that was true only while the retry sent one message. It splits now, so
// the length of the source decides how many messages the fallback is, not
// whether there is one.
func TestSend_OversizedReplyRetriesAsPlainChunks(t *testing.T) {
	bot := newFakeBot().failOnSend(1)
	a := newWithSender(bot, nil, testLogger(), nil)

	src := longReply(60)
	if utf8.RuneCountInString(src) <= telegramMessageCap {
		t.Fatalf("fixture source is %d chars, want over %d", utf8.RuneCountInString(src), telegramMessageCap)
	}
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) < 3 {
		t.Fatalf("send count = %d, want the failed HTML attempt plus several plain messages", len(msgs))
	}
	if msgs[0].ParseMode != parseModeHTML {
		t.Errorf("first attempt ParseMode = %q, want %s", msgs[0].ParseMode, parseModeHTML)
	}

	var reassembled strings.Builder
	for i, m := range msgs[1:] {
		if m.ParseMode != "" {
			t.Errorf("retry message %d ParseMode = %q, want empty", i+1, m.ParseMode)
		}
		if n := utf8.RuneCountInString(m.Text); n > telegramMessageCap {
			t.Errorf("retry message %d is %d chars, over Telegram's %d-char cap", i+1, n, telegramMessageCap)
		}
		reassembled.WriteString(m.Text)
	}
	tgtest.AssertSameText(t, src, reassembled.String())
}

// twoChunkReplyUnderCap builds the reply the retry rule turns on: markup
// inflates the rendered HTML past the chunk limit, so it splits, yet the source
// markdown still fits inside one Telegram message.
//
// Both halves are asserted here rather than in the test. A fixture that drifted
// to three chunks, or past the cap, would leave the test green while it stopped
// exercising the case it was written for.
func twoChunkReplyUnderCap(t *testing.T) string {
	t.Helper()
	src := longReply(45)
	if len(src) > telegramMessageCap {
		t.Fatalf("fixture source is %d bytes, want at most %d so the plain retry can fit in one message", len(src), telegramMessageCap)
	}
	chunks, err := renderChunks(src)
	if err != nil {
		t.Fatalf("renderChunks: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("fixture renders to %d chunks, want 2", len(chunks))
	}
	return src
}

// TestSend_FirstChunkFailureRetriesPlainTextWhenTheSourceFits covers the case
// the chunk count alone gets wrong.
//
// The reply chunks, but the failure landed on chunk one, so the chat is
// untouched and R31 has no partial delivery to protect. The source is under
// Telegram's cap, so the single-message retry can actually land. Both conditions
// hold, so R30's fallback applies exactly as it would to a short reply.
func TestSend_FirstChunkFailureRetriesPlainTextWhenTheSourceFits(t *testing.T) {
	bot := newFakeBot().failOnSend(1)
	a := newWithSender(bot, nil, testLogger(), nil)

	src := twoChunkReplyUnderCap(t)
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 2 {
		t.Fatalf("send count = %d, want exactly 2 — the HTML attempt and one plain-text retry", len(msgs))
	}
	if msgs[0].ParseMode != "HTML" {
		t.Errorf("first attempt ParseMode = %q, want HTML", msgs[0].ParseMode)
	}
	if msgs[1].ParseMode != "" {
		t.Errorf("retry ParseMode = %q, want empty — the retry carries no markup", msgs[1].ParseMode)
	}
	if msgs[1].Text != src {
		t.Error("retry text is not the original markdown")
	}
}

// TestSend_VoiceReplyIsNotChunked confirms the TTS path returns before the text
// path, so chunking is unreachable from a voice reply however long it is.
func TestSend_VoiceReplyIsNotChunked(t *testing.T) {
	tts := &recordingTTS{}
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), &VoiceOpts{
		TTS:            tts,
		TTSVoice:       "nova",
		AutoVoiceReply: true,
	})

	src := longReply(60)
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
		IsVoice:    true,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Errorf("send count = %d, want 1 (the voice message)", got)
	}
	if tts.text != src {
		t.Error("synthesized text is not the original markdown")
	}
}

// TestSend_ExplicitParseModeIsNotChunked keeps the activity log on its own
// chunker. It passes pre-built HTML and its own parse mode, and rerouting it
// through this one would re-split markup it already split.
func TestSend_ExplicitParseModeIsNotChunked(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	long := "<b>" + strings.Repeat("x", MessageChunkLimit+500) + "</b>"
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       long,
		ParseMode:  "HTML",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("send count = %d, want 1 — an explicit parse mode bypasses chunking", len(msgs))
	}
	if msgs[0].Text != long {
		t.Error("Text is not byte-identical to the input")
	}
}

// TestMessageChunkLimit_LeavesHeadroomBelowTelegramsCap pins the headroom
// decision. The chunker counts raw HTML bytes while Telegram counts the
// entity-stripped text in UTF-16 code units, so the count is conservative
// already, but it must still leave room under the cap.
func TestMessageChunkLimit_LeavesHeadroomBelowTelegramsCap(t *testing.T) {
	const telegramCap = 4096
	if MessageChunkLimit >= telegramCap {
		t.Errorf("MessageChunkLimit = %d, want headroom below Telegram's %d", MessageChunkLimit, telegramCap)
	}
}

// multiByteReplyUnderCap builds a reply that fits in one Telegram message and
// does not fit in 4096 bytes. Telegram counts characters, so the two are only
// the same string for ASCII.
//
// Both halves are asserted here so the fixture cannot drift into testing
// nothing: an ASCII reply would satisfy the test for the wrong reason.
func multiByteReplyUnderCap(t *testing.T) string {
	t.Helper()
	// Japanese: three bytes per character, so 1500 characters is 4500 bytes.
	src := strings.TrimSpace(strings.Repeat("こんにちは世界。これはテストです。\n", 100))
	if n := utf8.RuneCountInString(src); n > telegramMessageCap {
		t.Fatalf("fixture is %d characters, want at most %d so one message can hold it", n, telegramMessageCap)
	}
	if len(src) <= telegramMessageCap {
		t.Fatalf("fixture is %d bytes, want over %d so a byte-counted cap would reject it", len(src), telegramMessageCap)
	}
	return src
}

// TestSend_MultiByteReplyUnderTheCapRetriesPlainText pins what the futility
// check in R30 is actually asking.
//
// The question is whether the original markdown could go out as one message,
// and Telegram answers it in characters of the entity-stripped text — the unit
// telegramMessageCap is documented in. Measuring the source in bytes instead
// answers a different question, and answers it wrongly for every reply that is
// not ASCII: a Japanese reply well inside the cap looks oversized, so the retry
// that would have succeeded is never attempted and the reply is lost.
func TestSend_MultiByteReplyUnderTheCapRetriesPlainText(t *testing.T) {
	bot := newFakeBot().failOnSend(1)
	a := newWithSender(bot, nil, testLogger(), nil)

	src := multiByteReplyUnderCap(t)
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v — the plain-text retry should have carried the reply", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 2 {
		t.Fatalf("send count = %d, want 2 — the failed HTML attempt and one plain-text retry", len(msgs))
	}
	if msgs[0].ParseMode != parseModeHTML {
		t.Errorf("first ParseMode = %q, want %q", msgs[0].ParseMode, parseModeHTML)
	}
	if msgs[1].ParseMode != "" {
		t.Errorf("retry ParseMode = %q, want empty — the retry carries no markup", msgs[1].ParseMode)
	}
	if msgs[1].Text != src {
		t.Errorf("retry text = %q, want the original markdown", msgs[1].Text)
	}
}

// TestSend_ReplyThatRendersToNothingFallsBackToPlainText covers the gap between
// R29 and R28: a render that fails is sent as plain text, and a render that
// succeeds is sent as HTML, but a render that succeeds and produces nothing was
// neither — it returned no error and made no call.
//
// An image is the reachable case. Telegram's HTML subset cannot embed one, so
// it degrades to an anchor labelled with its alternative text; with no
// alternative text and a destination the scheme allowlist rejects, there is
// nothing left to emit. The reply is not empty — the user wrote something — so
// silence is a lost message, and the plain-text fallback already exists for
// exactly the case where the rendered form cannot carry it.
func TestSend_ReplyThatRendersToNothingFallsBackToPlainText(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	src := "![](./local.png)"
	chunks, err := renderChunks(src)
	if err != nil {
		t.Fatalf("renderChunks: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("fixture renders to %d chunks, want 0 — the test needs the empty render", len(chunks))
	}

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("send count = %d, want 1 — the reply must reach the chat", len(msgs))
	}
	if msgs[0].ParseMode != "" {
		t.Errorf("ParseMode = %q, want empty — there is no rendered form to send", msgs[0].ParseMode)
	}
	if msgs[0].Text != src {
		t.Errorf("text = %q, want the original markdown %q", msgs[0].Text, src)
	}
}

// TestSend_BlankReplySendsNothing is the other side of that boundary. A reply
// with no characters in it renders to nothing for the ordinary reason, and
// falling back would send Telegram a message it rejects as empty.
func TestSend_BlankReplySendsNothing(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "   \n\t\n",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 0 {
		t.Errorf("send count = %d, want 0 — a blank reply is not a message", got)
	}
}

// TestSend_TooManyChunksIsTruncatedToABoundedNumberOfMessages closes the
// unbounded-send path. sendChunks issues one API call per chunk with nothing
// limiting how many there are, so a reply that splits absurdly becomes a burst
// of sequential sendMessage calls. Telegram rate-limits per chat, so the burst
// is answered with a 429 partway through — and by then chunks have been
// delivered, which disables the plain-text retry. The reader is left with a
// truncated reply either way; the difference is whether the bot spent its rate
// limit getting there.
//
// Falling back to plain text is not the remedy: a reply that splits this far is
// far past the 4096-character cap, so the single plain message would be rejected
// and the whole reply lost. Truncating delivers the bulk of it, says so, and
// bounds the cost.
func TestSend_TooManyChunksIsTruncatedToABoundedNumberOfMessages(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	src := longReply(400)
	chunks, err := renderChunks(src)
	if err != nil {
		t.Fatalf("renderChunks: %v", err)
	}
	if len(chunks) <= maxOutboundChunks {
		t.Fatalf("fixture renders to %d chunks, want more than %d", len(chunks), maxOutboundChunks)
	}

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != maxOutboundChunks {
		t.Fatalf("send count = %d, want %d — the number of messages is bounded", len(msgs), maxOutboundChunks)
	}
	last := msgs[len(msgs)-1].Text
	if !strings.Contains(last, truncationNotice) {
		t.Errorf("last message does not say the reply was cut short: %q", last)
	}
	for i, m := range msgs {
		if n := utf8.RuneCountInString(m.Text); n > telegramMessageCap {
			t.Errorf("message %d is %d characters, over Telegram's cap of %d", i, n, telegramMessageCap)
		}
	}
}

// TestSend_LongUnrenderableReplyIsDeliveredAsPlainChunks covers the hole in
// R29's fallback.
//
// Raw HTML fails closed (R15), and CommonMark calls `Vec<T>` raw inline HTML —
// so ordinary technical prose takes the render-error exit. That exit sent the
// whole reply as one message with no length check, so any such reply over
// Telegram's cap was rejected outright and lost: no formatting, no text, and an
// error most callers discard.
func TestSend_LongUnrenderableReplyIsDeliveredAsPlainChunks(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	src := "Generic containers such as Vec<T> are everywhere.\n\n" + longReply(60)
	if _, err := renderChunks(src); err == nil {
		t.Fatal("fixture renders cleanly — it no longer exercises the render-error path")
	}
	if utf8.RuneCountInString(src) <= telegramMessageCap {
		t.Fatalf("fixture is %d chars, want over %d", utf8.RuneCountInString(src), telegramMessageCap)
	}

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) < 2 {
		t.Fatalf("send count = %d, want several — a reply over the cap must be split", len(msgs))
	}

	var reassembled strings.Builder
	for i, m := range msgs {
		if m.ParseMode != "" {
			t.Errorf("message %d ParseMode = %q, want empty — the fallback carries no markup", i+1, m.ParseMode)
		}
		if n := utf8.RuneCountInString(m.Text); n > telegramMessageCap {
			t.Errorf("message %d is %d chars, over Telegram's %d-char cap", i+1, n, telegramMessageCap)
		}
		reassembled.WriteString(m.Text)
	}
	tgtest.AssertSameText(t, src, reassembled.String())
}
