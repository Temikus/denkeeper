package telegram

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/Temikus/denkeeper/internal/adapter"
)

// These tests characterize the adapter's send behaviour as it is today:
// outgoing text is handed to Telegram verbatim with parse_mode=Markdown, and a
// rejected send is retried once with no parse mode. They are expected to be
// rewritten when the HTML render path lands — that inversion is the point.

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestSend_DefaultParseModeIsMarkdown(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "hello world",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1", got)
	}
	msg := bot.lastMessage(t)
	if msg.ParseMode != "Markdown" {
		t.Errorf("ParseMode = %q, want Markdown", msg.ParseMode)
	}
	if msg.Text != "hello world" {
		t.Errorf("Text = %q, want %q", msg.Text, "hello world")
	}
	if msg.ChatID != 12345 {
		t.Errorf("ChatID = %d, want 12345", msg.ChatID)
	}
}

func TestSend_RejectedMarkdownRetriesWithoutParseMode(t *testing.T) {
	bot := newFakeBot().failOnSend(1)
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "unbalanced *markdown",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 2 {
		t.Fatalf("send count = %d, want 2 (Markdown attempt then plain retry)", len(msgs))
	}
	if msgs[0].ParseMode != "Markdown" {
		t.Errorf("first attempt ParseMode = %q, want Markdown", msgs[0].ParseMode)
	}
	if msgs[1].ParseMode != "" {
		t.Errorf("retry ParseMode = %q, want empty", msgs[1].ParseMode)
	}
	if msgs[1].Text != "unbalanced *markdown" {
		t.Errorf("retry Text = %q, want the original text", msgs[1].Text)
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

func TestSendAndGetID_DefaultParseModeIsMarkdown(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	id, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "hello world",
	})
	if err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1", got)
	}
	if msg := bot.lastMessage(t); msg.ParseMode != "Markdown" {
		t.Errorf("ParseMode = %q, want Markdown", msg.ParseMode)
	}
	want := strconv.Itoa(firstFakeMessageID)
	if id != want {
		t.Errorf("returned message ID = %q, want %q", id, want)
	}
}

func TestSendAndGetID_RejectedMarkdownRetriesWithoutParseMode(t *testing.T) {
	bot := newFakeBot().failOnSend(1)
	a := newWithSender(bot, nil, testLogger(), nil)

	id, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "unbalanced *markdown",
	})
	if err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) != 2 {
		t.Fatalf("send count = %d, want 2 (Markdown attempt then plain retry)", len(msgs))
	}
	if msgs[0].ParseMode != "Markdown" {
		t.Errorf("first attempt ParseMode = %q, want Markdown", msgs[0].ParseMode)
	}
	if msgs[1].ParseMode != "" {
		t.Errorf("retry ParseMode = %q, want empty", msgs[1].ParseMode)
	}
	// The ID must come from the accepted send, not from a zero-value Message.
	want := strconv.Itoa(firstFakeMessageID)
	if id != want {
		t.Errorf("returned message ID = %q, want %q", id, want)
	}
}

// bugReportText is the message from the defect that motivated this work. Both
// URLs contain an underscore, which Telegram's legacy Markdown parser consumes
// as an emphasis delimiter because it has no intraword-emphasis rule.
const bugReportText = "See https://example.com/a_b and https://example.com/c_d"

// TestSend_ForwardsBrokenMarkdownVerbatim pins the defect as a fact: the
// adapter hands the raw markdown to Telegram and lets Telegram mangle it. The
// underscores are on the wire — the corruption happens in Telegram's parser,
// which is why nothing in this process can currently detect it.
//
// This is the assertion the HTML render path inverts: same input, same
// assertion site, expectation changed to rendered HTML with parse_mode=HTML.
func TestSend_ForwardsBrokenMarkdownVerbatim(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       bugReportText,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got := bot.sendCount(); got != 1 {
		t.Fatalf("send count = %d, want 1 — Telegram accepts this message, so no retry", got)
	}
	msg := bot.lastMessage(t)
	if msg.Text != bugReportText {
		t.Errorf("Text = %q, want byte-identical input %q", msg.Text, bugReportText)
	}
	if msg.ParseMode != "Markdown" {
		t.Errorf("ParseMode = %q, want Markdown", msg.ParseMode)
	}
}

func TestSendAndGetID_ForwardsBrokenMarkdownVerbatim(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if _, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       bugReportText,
	}); err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}

	msg := bot.lastMessage(t)
	if msg.Text != bugReportText {
		t.Errorf("Text = %q, want byte-identical input %q", msg.Text, bugReportText)
	}
	if msg.ParseMode != "Markdown" {
		t.Errorf("ParseMode = %q, want Markdown", msg.ParseMode)
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
