package telegram

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/Temikus/denkeeper/internal/adapter"
)

// captureClient records every Bot API request body so a test can assert on the
// exact payload Telegram would receive. rejectFirst simulates Telegram
// refusing to parse the entities in the first attempt.
type captureClient struct {
	mu          sync.Mutex
	requests    []url.Values
	rejectFirst bool
}

func (c *captureClient) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.requests = append(c.requests, values)
	n := len(c.requests)
	c.mu.Unlock()

	payload := `{"ok":true,"result":{"message_id":42,"date":1,"chat":{"id":1,"type":"private"}}}`
	if c.rejectFirst && n == 1 {
		payload = `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities"}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(payload)),
	}, nil
}

func (c *captureClient) request(t *testing.T, i int) url.Values {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if i >= len(c.requests) {
		t.Fatalf("expected at least %d requests, got %d", i+1, len(c.requests))
	}
	return c.requests[i]
}

func (c *captureClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

// newCapturingAdapter wires an Adapter to a stub Bot API transport.
func newCapturingAdapter(client *captureClient) *Adapter {
	bot := &tgbotapi.BotAPI{Token: "test-token", Client: client, Buffer: 1}
	bot.SetAPIEndpoint("http://127.0.0.1/bot%s/%s")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return newWithBot(bot, nil, logger, nil)
}

func TestSend_DeliversRenderedHTMLWithoutCharacterLoss(t *testing.T) {
	client := &captureClient{}
	a := newCapturingAdapter(client)

	err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       twoURLMessage,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	req := client.request(t, 0)
	if got := req.Get("parse_mode"); got != tgbotapi.ModeHTML {
		t.Fatalf("parse_mode = %q, want HTML (legacy Markdown corrupts URLs)", got)
	}

	text := req.Get("text")
	for _, want := range []string{"NRR-JaUmg_hm7jYl-LiWx", "QQR-KbVnh_in8kZm-MjXy"} {
		if !strings.Contains(text, want) {
			t.Errorf("delivered payload lost %q\npayload: %s", want, text)
		}
	}
	if got := visibleText(text); got != twoURLMessageVisible {
		t.Errorf("delivered text does not round-trip\n want: %q\n  got: %q", twoURLMessageVisible, got)
	}
}

func TestSend_ExplicitParseModeIsNotRewritten(t *testing.T) {
	client := &captureClient{}
	a := newCapturingAdapter(client)

	// The dispatcher's activity log owns its own markup.
	owned := "🔧 <b>tool_name</b> — <blockquote expandable>a &lt; b</blockquote>"
	err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       owned,
		ParseMode:  "HTML",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	req := client.request(t, 0)
	if got := req.Get("text"); got != owned {
		t.Errorf("caller-owned markup was rewritten\n want: %q\n  got: %q", owned, got)
	}
	if got := req.Get("parse_mode"); got != "HTML" {
		t.Errorf("parse_mode = %q, want HTML", got)
	}
}

func TestSend_FallsBackToPlainSourceWhenTelegramRejectsMarkup(t *testing.T) {
	client := &captureClient{rejectFirst: true}
	a := newCapturingAdapter(client)

	src := "Report **ready**: https://example.com/a_b/c_d"
	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if client.count() != 2 {
		t.Fatalf("expected a retry, got %d requests", client.count())
	}
	retry := client.request(t, 1)
	if got := retry.Get("parse_mode"); got != "" {
		t.Errorf("retry parse_mode = %q, want unset", got)
	}
	if got := retry.Get("text"); got != src {
		t.Errorf("retry must send the unmodified source\n want: %q\n  got: %q", src, got)
	}
}

func TestSend_ExplicitParseModeIsNotRetried(t *testing.T) {
	client := &captureClient{rejectFirst: true}
	a := newCapturingAdapter(client)

	err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "<b>owned</b>",
		ParseMode:  "HTML",
	})
	if err == nil {
		t.Fatal("expected the rejection to surface")
	}
	if client.count() != 1 {
		t.Errorf("caller-owned markup must not be retried, got %d requests", client.count())
	}
}

func TestSendAndGetID_DeliversRenderedHTML(t *testing.T) {
	client := &captureClient{}
	a := newCapturingAdapter(client)

	id, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       twoURLMessage,
	})
	if err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}
	if id != "42" {
		t.Errorf("message ID = %q, want 42", id)
	}

	req := client.request(t, 0)
	if got := req.Get("parse_mode"); got != tgbotapi.ModeHTML {
		t.Fatalf("parse_mode = %q, want HTML", got)
	}
	if got := visibleText(req.Get("text")); got != twoURLMessageVisible {
		t.Errorf("delivered text does not round-trip\n want: %q\n  got: %q", twoURLMessageVisible, got)
	}
}

func TestSendAndGetID_FallsBackToPlainSource(t *testing.T) {
	client := &captureClient{rejectFirst: true}
	a := newCapturingAdapter(client)

	src := "Heads up: a_b **c**"
	if _, err := a.SendAndGetID(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       src,
	}); err != nil {
		t.Fatalf("SendAndGetID: %v", err)
	}
	if got := client.request(t, 1).Get("text"); got != src {
		t.Errorf("retry text = %q, want %q", got, src)
	}
}

func TestPrepareOutgoingText_ExplicitModePassesThrough(t *testing.T) {
	text, mode := prepareOutgoingText(adapter.OutgoingMessage{Text: "*raw*", ParseMode: "MarkdownV2"})
	if text != "*raw*" || mode != "MarkdownV2" {
		t.Errorf("got (%q, %q), want (\"*raw*\", \"MarkdownV2\")", text, mode)
	}
}

func TestPrepareOutgoingText_OversizedRenderFallsBackToPlain(t *testing.T) {
	// Escaping expands the text; a render that crosses Telegram's cap must not
	// turn a deliverable message into a failed one.
	src := strings.Repeat("<", maxMessageChars-1)

	text, mode := prepareOutgoingText(adapter.OutgoingMessage{Text: src})
	if mode != "" {
		t.Errorf("parse mode = %q, want unset for an oversized render", mode)
	}
	if text != src {
		t.Error("oversized render must fall back to the unmodified source")
	}
}

func TestPrepareOutgoingText_RendersMarkdown(t *testing.T) {
	text, mode := prepareOutgoingText(adapter.OutgoingMessage{Text: "a **b** c_d_e"})
	if mode != tgbotapi.ModeHTML {
		t.Fatalf("parse mode = %q, want HTML", mode)
	}
	if text != "a <b>b</b> c_d_e" {
		t.Errorf("text = %q, want %q", text, "a <b>b</b> c_d_e")
	}
}

func TestPrepareOutgoingText_WhitespaceOnlyFallsBackToPlain(t *testing.T) {
	text, mode := prepareOutgoingText(adapter.OutgoingMessage{Text: "   "})
	if mode != "" || text != "   " {
		t.Errorf("got (%q, %q), want (\"   \", \"\")", text, mode)
	}
}
