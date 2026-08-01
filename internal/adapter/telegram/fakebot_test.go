package telegram

import (
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// fakeBot is a test double for the adapter's outbound send/request seam. It
// records every outbound call in order so tests can assert on the exact bytes
// the adapter would put on the wire, and it can be told to fail on a chosen
// call index so the adapter's retry and abort paths are reachable in-process.
type fakeBot struct {
	mu sync.Mutex

	// sends and requests record calls in the order they were made.
	sends    []tgbotapi.Chattable
	requests []tgbotapi.Chattable

	// failSendOn holds 1-based Send call indices that must return failErr.
	failSendOn map[int]bool
	// failErr is what an injected failure returns. It defaults to a rejection
	// because that is what most injection sites mean: the adapter distinguishes
	// Telegram refusing a request as malformed, which R30 answers by retrying
	// without a parse mode, from Telegram failing to carry a request out, which
	// it does not. A test about the second sets its own.
	failErr error

	// nextMessageID is the ID handed to the next successful Send. It starts
	// well above zero so a real reply is distinguishable from a zero-value
	// tgbotapi.Message that a test accidentally asserts against.
	nextMessageID int
}

// firstFakeMessageID is the MessageID of the first message a fakeBot accepts.
// Deliberately non-zero and implausible as an accident.
const firstFakeMessageID = 9001

func newFakeBot() *fakeBot {
	return &fakeBot{
		failSendOn:    make(map[int]bool),
		failErr:       errRejected,
		nextMessageID: firstFakeMessageID,
	}
}

// failOnSend marks the given 1-based Send call indices as failures.
func (f *fakeBot) failOnSend(indices ...int) *fakeBot {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range indices {
		f.failSendOn[i] = true
	}
	return f
}

func (f *fakeBot) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, c)
	if f.failSendOn[len(f.sends)] {
		return tgbotapi.Message{}, f.failErr
	}
	if msg, ok := c.(tgbotapi.MessageConfig); ok {
		// Telegram rejects a message over its cap. Without this the fake accepts
		// anything, so a send path that skipped chunking looked healthy in every
		// test while losing the reply in production — which is exactly how the
		// unchunked plain-text fallback survived a full phase.
		if n := utf8.RuneCountInString(msg.Text); n > telegramMessageCap {
			return tgbotapi.Message{}, errOverCap
		}
	}
	id := f.nextMessageID
	f.nextMessageID++
	return tgbotapi.Message{MessageID: id}, nil
}

// errRejected is shaped like the API error Telegram returns for an HTML body it
// cannot parse — the fault R30's retry exists to answer.
var errRejected = &tgbotapi.Error{
	Code:    400,
	Message: "Bad Request: can't parse entities: Unsupported start tag",
}

// errOverCap is shaped like the API error Telegram returns for a message longer
// than it accepts.
var errOverCap = &tgbotapi.Error{Code: 400, Message: "Bad Request: message is too long"}

func (f *fakeBot) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, c)
	return &tgbotapi.APIResponse{Ok: true}, nil
}

// sendCount reports how many Send calls the adapter made.
func (f *fakeBot) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

// messages returns the captured Send calls that were text messages, in call
// order. tgbotapi.Chattable is an interface, so reading Text/ParseMode/
// ReplyMarkup requires the concrete MessageConfig.
func (f *fakeBot) messages(t *testing.T) []tgbotapi.MessageConfig {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tgbotapi.MessageConfig, 0, len(f.sends))
	for i, c := range f.sends {
		msg, ok := c.(tgbotapi.MessageConfig)
		if !ok {
			t.Fatalf("send %d: captured %T, want tgbotapi.MessageConfig", i+1, c)
		}
		out = append(out, msg)
	}
	return out
}

// TestFakeBot_FailureInjectionHitsOnlyTheChosenIndex guards the fake itself.
// Phases that assert "exactly one retry" or "abort on chunk N" are only
// meaningful if the injection fires where it is aimed and nowhere else.
func TestFakeBot_FailureInjectionHitsOnlyTheChosenIndex(t *testing.T) {
	bot := newFakeBot().failOnSend(2)

	var errs []error
	for i := 0; i < 3; i++ {
		_, err := bot.Send(tgbotapi.NewMessage(1, "x"))
		errs = append(errs, err)
	}

	if errs[0] != nil {
		t.Errorf("send 1 failed unexpectedly: %v", errs[0])
	}
	if errs[1] == nil {
		t.Error("send 2 should have failed — injection did not fire on its index")
	}
	if errs[2] != nil {
		t.Errorf("send 3 failed unexpectedly: %v", errs[2])
	}
	if got := bot.sendCount(); got != 3 {
		t.Errorf("send count = %d, want 3 — a failed send is still recorded", got)
	}
}

// TestFakeBot_AcceptedMessageIDsAreDistinguishableFromZero ensures a test can
// never pass by asserting against a zero-value tgbotapi.Message.
func TestFakeBot_AcceptedMessageIDsAreDistinguishableFromZero(t *testing.T) {
	bot := newFakeBot()

	first, err := bot.Send(tgbotapi.NewMessage(1, "x"))
	if err != nil {
		t.Fatalf("send 1: %v", err)
	}
	if first.MessageID != firstFakeMessageID {
		t.Errorf("first MessageID = %d, want %d", first.MessageID, firstFakeMessageID)
	}

	second, err := bot.Send(tgbotapi.NewMessage(1, "y"))
	if err != nil {
		t.Fatalf("send 2: %v", err)
	}
	if second.MessageID == first.MessageID {
		t.Errorf("MessageID %d reused — IDs must be distinct per accepted send", second.MessageID)
	}

	rejected, err := newFakeBot().failOnSend(1).Send(tgbotapi.NewMessage(1, "z"))
	if err == nil {
		t.Fatal("expected the injected failure")
	}
	if rejected.MessageID != 0 {
		t.Errorf("rejected send returned MessageID %d, want the zero value", rejected.MessageID)
	}
}

// lastMessage returns the final captured text message.
func (f *fakeBot) lastMessage(t *testing.T) tgbotapi.MessageConfig {
	t.Helper()
	msgs := f.messages(t)
	if len(msgs) == 0 {
		t.Fatal("no messages captured")
	}
	return msgs[len(msgs)-1]
}

// chatActions returns the captured Send calls that were chat actions (typing
// indicators), in call order.
func (f *fakeBot) chatActions(t *testing.T) []tgbotapi.ChatActionConfig {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tgbotapi.ChatActionConfig, 0, len(f.sends))
	for _, c := range f.sends {
		if action, ok := c.(tgbotapi.ChatActionConfig); ok {
			out = append(out, action)
		}
	}
	return out
}

// edits returns the captured Request calls that were message-text edits, in
// call order.
func (f *fakeBot) edits(t *testing.T) []tgbotapi.EditMessageTextConfig {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tgbotapi.EditMessageTextConfig, 0, len(f.requests))
	for _, c := range f.requests {
		if edit, ok := c.(tgbotapi.EditMessageTextConfig); ok {
			out = append(out, edit)
		}
	}
	return out
}

// TestFakeBot_RejectsAMessageOverTelegramsCap guards the guard. A fake that
// accepts anything makes every "the reply was delivered" assertion in this
// package meaningless for long replies.
func TestFakeBot_RejectsAMessageOverTelegramsCap(t *testing.T) {
	bot := newFakeBot()

	atCap, err := bot.Send(tgbotapi.NewMessage(1, strings.Repeat("é", telegramMessageCap)))
	if err != nil {
		t.Errorf("a message of exactly %d characters was rejected: %v", telegramMessageCap, err)
	}
	if atCap.MessageID == 0 {
		t.Error("an accepted send returned the zero-value message")
	}

	// One character over, and multi-byte throughout — a byte count would have
	// rejected the first message too.
	if _, err := bot.Send(tgbotapi.NewMessage(1, strings.Repeat("é", telegramMessageCap+1))); err == nil {
		t.Errorf("a message of %d characters was accepted", telegramMessageCap+1)
	}
}
