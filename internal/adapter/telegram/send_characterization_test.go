package telegram

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/Temikus/denkeeper/internal/adapter"
)

// These tests assert what the adapter puts on the wire. Outgoing text is
// CommonMark, rendered locally to Telegram's HTML subset and sent with
// parse_mode=HTML; a caller-supplied parse mode is forwarded unrendered; and
// either a render failure or a Telegram rejection falls back to the original
// markdown with no parse mode.
//
// They began as characterization tests for the parse_mode=Markdown path they
// replaced. The assertion sites are unchanged and the expectations are inverted,
// so the diff against that commit is exactly the behaviour change.

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestSend_DefaultParseModeIsHTML(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "hello **world**",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1", got)
	}
	msg := bot.lastMessage(t)
	if msg.ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", msg.ParseMode)
	}
	if msg.Text != "hello <b>world</b>" {
		t.Errorf("Text = %q, want the rendered HTML %q", msg.Text, "hello <b>world</b>")
	}
	if msg.ChatID != 12345 {
		t.Errorf("ChatID = %d, want 12345", msg.ChatID)
	}
}

// TestSend_RejectedHTMLRetriesWithoutParseMode covers R30. The retry must carry
// the original markdown, never a half-rendered string.
func TestSend_RejectedHTMLRetriesWithoutParseMode(t *testing.T) {
	bot := newFakeBot().failOnSend(1)
	a := newWithSender(bot, nil, testLogger(), nil)

	const src = "a **bold** reply"
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 2 {
		t.Fatalf("send count = %d, want 2 (HTML attempt then plain retry)", len(msgs))
	}
	if msgs[0].ParseMode != "HTML" {
		t.Errorf("first attempt ParseMode = %q, want HTML", msgs[0].ParseMode)
	}
	if msgs[0].Text != "a <b>bold</b> reply" {
		t.Errorf("first attempt Text = %q, want the rendered HTML", msgs[0].Text)
	}
	if msgs[1].ParseMode != "" {
		t.Errorf("retry ParseMode = %q, want empty", msgs[1].ParseMode)
	}
	if msgs[1].Text != src {
		t.Errorf("retry Text = %q, want the original markdown %q", msgs[1].Text, src)
	}
}

// TestSend_RenderFailureSendsPlainTextWithNoParseMode covers R29. Raw HTML in
// the source is the one construct that fails closed in the renderer, so it is
// the path a render failure actually takes in practice.
func TestSend_RenderFailureSendsPlainTextWithNoParseMode(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	const src = "here is a <div>raw html block</div>"
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("send count = %d, want 1 — a render failure does not attempt HTML first", len(msgs))
	}
	if msgs[0].ParseMode != "" {
		t.Errorf("ParseMode = %q, want empty", msgs[0].ParseMode)
	}
	if msgs[0].Text != src {
		t.Errorf("Text = %q, want the original markdown %q", msgs[0].Text, src)
	}
}

func TestSend_BothAttemptsRejectedReturnsError(t *testing.T) {
	bot := newFakeBot().failOnSend(1, 2)
	a := newWithSender(bot, nil, testLogger(), nil)

	err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "hello",
	})
	if err == nil {
		t.Fatal("expected an error when both attempts fail")
	}
	if got := bot.sendCount(); got != 2 {
		t.Errorf("send count = %d, want 2 — the retry must not loop", got)
	}
}

func TestSendAndGetID_DefaultParseModeIsHTML(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	id, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "hello **world**",
	})
	if err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1", got)
	}
	msg := bot.lastMessage(t)
	if msg.ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", msg.ParseMode)
	}
	if msg.Text != "hello <b>world</b>" {
		t.Errorf("Text = %q, want the rendered HTML", msg.Text)
	}
	want := strconv.Itoa(firstFakeMessageID)
	if id != want {
		t.Errorf("returned message ID = %q, want %q", id, want)
	}
}

func TestSendAndGetID_RejectedHTMLRetriesWithoutParseMode(t *testing.T) {
	bot := newFakeBot().failOnSend(1)
	a := newWithSender(bot, nil, testLogger(), nil)

	const src = "a **bold** reply"
	id, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	})
	if err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 2 {
		t.Fatalf("send count = %d, want 2 (HTML attempt then plain retry)", len(msgs))
	}
	if msgs[0].ParseMode != "HTML" {
		t.Errorf("first attempt ParseMode = %q, want HTML", msgs[0].ParseMode)
	}
	if msgs[1].ParseMode != "" {
		t.Errorf("retry ParseMode = %q, want empty", msgs[1].ParseMode)
	}
	if msgs[1].Text != src {
		t.Errorf("retry Text = %q, want the original markdown %q", msgs[1].Text, src)
	}
	// The ID must come from the accepted send, not from a zero-value Message.
	want := strconv.Itoa(firstFakeMessageID)
	if id != want {
		t.Errorf("returned message ID = %q, want %q", id, want)
	}
}

func TestSendAndGetID_RenderFailureSendsPlainTextWithNoParseMode(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	const src = "here is a <div>raw html block</div>"
	if _, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("send count = %d, want 1", len(msgs))
	}
	if msgs[0].ParseMode != "" {
		t.Errorf("ParseMode = %q, want empty", msgs[0].ParseMode)
	}
	if msgs[0].Text != src {
		t.Errorf("Text = %q, want the original markdown", msgs[0].Text)
	}
}

// bugReportText is the message from the defect that motivated this work. Both
// URLs contain an underscore, which Telegram's legacy Markdown parser consumes
// as an emphasis delimiter because it has no intraword-emphasis rule.
const bugReportText = "See https://example.com/a_b and https://example.com/c_d"

// TestSend_BugReportInputGoesOutAsHTML is the originating defect's regression
// test at the adapter layer. It previously asserted the raw markdown went out
// with parse_mode=Markdown — the defect pinned as a fact — and now asserts the
// same input goes out under parse_mode=HTML with both underscores intact.
//
// The text is byte-identical to the input either way, which is the point: what
// changed is who parses it. Under Markdown, Telegram consumed the underscores as
// emphasis delimiters and broke both links. Under HTML there is no markup in this
// string for Telegram to misread, and it autolinks the visible URLs after
// parsing.
func TestSend_BugReportInputGoesOutAsHTML(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       bugReportText,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1 — no fallback should trigger", got)
	}
	msg := bot.lastMessage(t)
	if msg.ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", msg.ParseMode)
	}
	if msg.Text != bugReportText {
		t.Errorf("Text = %q, want byte-identical input %q", msg.Text, bugReportText)
	}
	for _, url := range []string{"https://example.com/a_b", "https://example.com/c_d"} {
		if !strings.Contains(msg.Text, url) {
			t.Errorf("outbound text %q lost %q", msg.Text, url)
		}
	}
	if strings.Contains(msg.Text, `\`) {
		t.Errorf("outbound text %q contains a backslash escape", msg.Text)
	}
}

func TestSendAndGetID_BugReportInputGoesOutAsHTML(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if _, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       bugReportText,
	}); err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}

	msg := bot.lastMessage(t)
	if msg.ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", msg.ParseMode)
	}
	if msg.Text != bugReportText {
		t.Errorf("Text = %q, want byte-identical input %q", msg.Text, bugReportText)
	}
}

// TestSend_SnakeCaseIdentifierInProseSurvives is the second half of the
// originating defect: an identifier in ordinary prose, not a URL. Every
// underscore must survive with no backslash in front of it.
func TestSend_SnakeCaseIdentifierInProseSurvives(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	const src = "the snake_case_identifier and another_one_here are both fine"
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg := bot.lastMessage(t)
	if msg.Text != src {
		t.Errorf("Text = %q, want byte-identical input %q", msg.Text, src)
	}
	if msg.ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", msg.ParseMode)
	}
}

// TestSend_MultiBlockReplyGoesOutAsOneMessage pins the interim behaviour before
// chunking lands: a reply that renders to several blocks is joined and sent as a
// single Telegram message. When the chunker arrives this becomes one message per
// chunk, and this test is the assertion site that changes.
func TestSend_MultiBlockReplyGoesOutAsOneMessage(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "# Heading\n\nFirst paragraph.\n\nSecond paragraph.",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1", got)
	}
	msg := bot.lastMessage(t)
	want := "<b>Heading</b>\n\nFirst paragraph.\n\nSecond paragraph."
	if msg.Text != want {
		t.Errorf("Text = %q, want %q", msg.Text, want)
	}
	if msg.ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", msg.ParseMode)
	}
}

// TestSend_DebugToggleTextIsUnaffected covers a non-LLM adapter message. The
// /debug confirmation goes out through the seam directly, not through Send, so it
// must not acquire a parse mode now that Send means "render as CommonMark".
func TestSend_DebugToggleTextIsUnaffected(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	a.handleDebugCommand(12345)

	msg := bot.lastMessage(t)
	if msg.ParseMode != "" {
		t.Errorf("ParseMode = %q, want empty — this message is plain text", msg.ParseMode)
	}
	if !strings.Contains(msg.Text, "Debug mode enabled") {
		t.Errorf("Text = %q, want the debug confirmation", msg.Text)
	}
}

// kitchenSinkMarkdown exercises every construct fixed alongside the renderer's
// vendored-in defects. The renderer's own golden test pins the exact bytes; this
// asserts the document survives the send path intact and triggers no fallback.
const kitchenSinkMarkdown = "Wrapped prose that runs across\ntwo source lines here.\n\n" +
	"A hard break line  \nfollows immediately.\n\n" +
	"---\n\n" +
	"> quoted line one\n> quoted line two\n\n" +
	"- parent bullet\n    - child bullet\n\n" +
	"1. first step\n2. second step\n"

func TestSend_KitchenSinkDocumentTriggersNoFallback(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       kitchenSinkMarkdown,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1 — no fallback may trigger", got)
	}
	msg := bot.lastMessage(t)
	if msg.ParseMode != "HTML" {
		t.Fatalf("ParseMode = %q, want HTML", msg.ParseMode)
	}

	for _, want := range []string{
		"across\ntwo source lines",                                  // soft line break
		"A hard break line\nfollows",                                // hard line break
		"<blockquote>quoted line one\nquoted line two</blockquote>", // quotation, both defects
		"- parent bullet\n  - child bullet",                         // nested list indentation
		"1. first step\n2. second step",                             // ordered list ordinals
	} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("outbound text is missing %q\ngot: %q", want, msg.Text)
		}
	}
	if strings.Contains(msg.Text, "&gt;") {
		t.Errorf("outbound text %q still contains a literal quote marker", msg.Text)
	}
	if strings.Contains(msg.Text, "<hr") {
		t.Errorf("outbound text %q contains the malformed thematic break", msg.Text)
	}
}

// typographyMarkdown is the inline-and-block typography document: a heading, a
// strikethrough span, an ordinary link, a rejected javascript: link, and a
// language-annotated fenced code block. The renderer's golden test pins the
// exact bytes; this asserts the document survives the send path and triggers no
// fallback.
const typographyMarkdown = "## Results\n\n" +
	"The ~~old~~ new approach works. See [the docs](https://example.com/a_b) " +
	"and do not follow [this one](javascript:alert(1)).\n\n" +
	"```go\nif a < b { return \"ok\" }\n```\n"

func TestSend_TypographyDocumentTriggersNoFallback(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       typographyMarkdown,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1 — no fallback may trigger", got)
	}
	msg := bot.lastMessage(t)
	if msg.ParseMode != "HTML" {
		t.Fatalf("ParseMode = %q, want HTML", msg.ParseMode)
	}

	for _, want := range []string{
		"<b>Results</b>\n", // heading, bold, own line
		"<s>old</s>",       // strikethrough
		`<a href="https://example.com/a_b">the docs</a>`, // allowlisted scheme, underscore intact
		"<pre><code class=\"language-go\">",              // fenced code with its language
		"if a &lt; b { return \"ok\" }",                  // fence content escaped, quote left literal
	} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("outbound text is missing %q\ngot: %q", want, msg.Text)
		}
	}
	// The rejected destination must not reach Telegram in any form: it autolinks
	// visible text after parsing, so a destination left in the text could come
	// back as a live link.
	if strings.Contains(msg.Text, "alert(1)") {
		t.Errorf("outbound text %q carries the rejected destination", msg.Text)
	}
	if !strings.Contains(msg.Text, "do not follow this one.") {
		t.Errorf("outbound text %q lost the rejected link's label", msg.Text)
	}
}

// tableAndImageMarkdown is the pair of constructs upstream failed closed on.
// This phase's whole value is that neither routes the message to the plain-text
// fallback, so the send-count assertion below is the point of the test.
const tableAndImageMarkdown = "Here are the results:\n\n" +
	"| Name | Qty | Price |\n" +
	"|------|-----|-------|\n" +
	"| apple | 3 | 1.50 |\n" +
	"| watermelon | 12 | 0.25 |\n\n" +
	"![the chart](https://example.com/chart_1.png)\n"

func TestSend_TableAndImageTriggerNoFallback(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       tableAndImageMarkdown,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1 — no plain-text retry may happen", got)
	}
	msg := bot.lastMessage(t)
	if msg.ParseMode != "HTML" {
		t.Fatalf("ParseMode = %q, want HTML — an empty parse mode means the fallback fired", msg.ParseMode)
	}

	for _, want := range []string{
		"<pre>Name       | Qty | Price\n",                         // aligned header
		"apple      | 3   | 1.50\n",                               // aligned body row
		`<a href="https://example.com/chart_1.png">the chart</a>`, // image degraded to an anchor
	} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("outbound text is missing %q\ngot: %q", want, msg.Text)
		}
	}
}

// recordingTTS captures the text handed to speech synthesis.
type recordingTTS struct {
	text string
}

func (r *recordingTTS) Synthesize(_ context.Context, text, _ string) ([]byte, error) {
	r.text = text
	return []byte("ogg-bytes"), nil
}

// TestSend_VoiceReplyIsNotRendered guards the TTS path. Send returns early for a
// voice reply, so rendering must not run before that branch — otherwise speech
// synthesis reads HTML tags aloud.
func TestSend_VoiceReplyIsNotRendered(t *testing.T) {
	tts := &recordingTTS{}
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), &VoiceOpts{
		TTS:            tts,
		TTSVoice:       "nova",
		AutoVoiceReply: true,
	})

	const src = "a **bold** claim with a [link](https://example.com/a_b)"
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
		IsVoice:    true,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if tts.text != src {
		t.Errorf("synthesized text = %q, want the original markdown %q", tts.text, src)
	}
	if strings.Contains(tts.text, "<") {
		t.Errorf("synthesized text = %q — must contain no HTML tags", tts.text)
	}
	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1 (the voice message)", got)
	}
	if _, isText := bot.sends[0].(tgbotapi.MessageConfig); isText {
		t.Error("voice reply went out as a text message")
	}
}

// activityLogHTML is the shape the dispatcher's activity log sends: hand-built,
// pre-escaped HTML with an explicit parse mode.
const activityLogHTML = "<b>Tool</b>\n<blockquote expandable>read_file &amp; write</blockquote>"

// TestSend_ExplicitParseModePassesThrough covers the highest-blast-radius path
// in this work. The activity log builds its own HTML and sets ParseMode, and
// the adapter must forward it untouched with no retry.
func TestSend_ExplicitParseModePassesThrough(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID:   "12345",
		Text:         activityLogHTML,
		ParseMode:    "HTML",
		Buttons:      []adapter.KeyboardButton{{Label: "Approve", CallbackData: "ok"}},
		ButtonLayout: []int{1},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1", got)
	}
	msg := bot.lastMessage(t)
	if msg.Text != activityLogHTML {
		t.Errorf("Text = %q, want byte-identical input %q", msg.Text, activityLogHTML)
	}
	if msg.ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", msg.ParseMode)
	}
	markup, ok := msg.ReplyMarkup.(tgbotapi.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("ReplyMarkup = %T, want tgbotapi.InlineKeyboardMarkup", msg.ReplyMarkup)
	}
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("keyboard shape = %v, want one row of one button", markup.InlineKeyboard)
	}
	if markup.InlineKeyboard[0][0].Text != "Approve" {
		t.Errorf("button label = %q, want Approve", markup.InlineKeyboard[0][0].Text)
	}
}

// TestSend_ExplicitParseModeIsNotRetried pins that an explicit parse mode opts
// out of the plain-text safety net entirely.
func TestSend_ExplicitParseModeIsNotRetried(t *testing.T) {
	bot := newFakeBot().failOnSend(1)
	a := newWithSender(bot, nil, testLogger(), nil)

	err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       activityLogHTML,
		ParseMode:  "HTML",
	})
	if err == nil {
		t.Fatal("expected the rejection to surface as an error")
	}
	if got := bot.sendCount(); got != 1 {
		t.Errorf("send count = %d, want 1 — an explicit parse mode must not be retried", got)
	}
}

func TestSendTyping_RoutesThroughTheSeam(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.SendTyping(context.Background(), "12345"); err != nil {
		t.Fatalf("SendTyping: %v", err)
	}

	actions := bot.chatActions(t)
	if len(actions) != 1 {
		t.Fatalf("chat action count = %d, want 1", len(actions))
	}
	if actions[0].ChatID != 12345 {
		t.Errorf("ChatID = %d, want 12345", actions[0].ChatID)
	}
	if actions[0].Action != tgbotapi.ChatTyping {
		t.Errorf("Action = %q, want %q", actions[0].Action, tgbotapi.ChatTyping)
	}
}

func TestEditText_ParseModeIsCallerControlled(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.EditText(context.Background(), "12345", "77", activityLogHTML, "HTML"); err != nil {
		t.Fatalf("EditText: %v", err)
	}

	edits := bot.edits(t)
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1", len(edits))
	}
	if edits[0].Text != activityLogHTML {
		t.Errorf("Text = %q, want byte-identical input", edits[0].Text)
	}
	if edits[0].ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", edits[0].ParseMode)
	}
	if edits[0].MessageID != 77 {
		t.Errorf("MessageID = %d, want 77", edits[0].MessageID)
	}
}

func TestEditText_EmptyParseModeStaysEmpty(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.EditText(context.Background(), "12345", "77", "plain text", ""); err != nil {
		t.Fatalf("EditText: %v", err)
	}

	edits := bot.edits(t)
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1", len(edits))
	}
	if edits[0].ParseMode != "" {
		t.Errorf("ParseMode = %q, want empty — EditText must not supply a default", edits[0].ParseMode)
	}
}

func TestEditMessage_ParseModeIsCallerControlled(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	err := a.EditMessage(context.Background(), "12345", "77", adapter.OutgoingMessage{
		Text:      activityLogHTML,
		ParseMode: "HTML",
		Buttons:   []adapter.KeyboardButton{{Label: "Deny", CallbackData: "no"}},
	})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}

	edits := bot.edits(t)
	if len(edits) != 1 {
		t.Fatalf("edit count = %d, want 1", len(edits))
	}
	if edits[0].Text != activityLogHTML {
		t.Errorf("Text = %q, want byte-identical input", edits[0].Text)
	}
	if edits[0].ParseMode != "HTML" {
		t.Errorf("ParseMode = %q, want HTML", edits[0].ParseMode)
	}
	if edits[0].ReplyMarkup == nil || len(edits[0].ReplyMarkup.InlineKeyboard) != 1 {
		t.Fatalf("ReplyMarkup = %v, want one keyboard row", edits[0].ReplyMarkup)
	}
}
