package chatapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const tgToken = "123456:ABC-secret-token-value-xyz"

// fakeTelegram serves getMe and getUpdates from a scripted update list and
// records the offsets clients send.
type fakeTelegram struct {
	mu      sync.Mutex
	updates []string // raw JSON update objects, in update_id order
	offsets []string
	code    int // when non-zero, every call fails with this error_code
}

func (f *fakeTelegram) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/bot"+tgToken+"/") {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
			return
		}
		if f.code != 0 {
			_, _ = w.Write([]byte(`{"ok":false,"error_code":` + itoa(f.code) + `,"description":"nope"}`))
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"Den","username":"my_den_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			f.offsets = append(f.offsets, r.URL.Query().Get("offset"))
			_, _ = w.Write([]byte(`{"ok":true,"result":[` + f.selectUpdates(r.URL.Query().Get("offset")) + `]}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// selectUpdates mimics getUpdates: offset -1 returns the last update,
// otherwise updates with update_id >= offset. Update i+1 is f.updates[i].
func (f *fakeTelegram) selectUpdates(offset string) string {
	if offset == "-1" {
		if len(f.updates) == 0 {
			return ""
		}
		return f.updates[len(f.updates)-1]
	}
	from, _ := strconv.Atoi(offset)
	var out []string
	for i, u := range f.updates {
		if i+1 >= from {
			out = append(out, u)
		}
	}
	return strings.Join(out, ",")
}

func itoa(n int) string { return strconv.Itoa(n) }

// update builds update number id (1-based, matching its position).
func update(id int, chatType string, isBot bool, text string) string {
	bot := "false"
	if isBot {
		bot = "true"
	}
	return `{"update_id":` + itoa(id) + `,"message":{"from":{"id":4821,"is_bot":` + bot +
		`,"first_name":"Sam","username":"samk"},"chat":{"id":4821,"type":"` + chatType + `"},"text":"` + text + `"}}`
}

func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestTelegramVerify_OK(t *testing.T) {
	f := &fakeTelegram{}
	tg := Telegram{BaseURL: f.server(t).URL}

	bot, err := tg.Verify(shortCtx(t), tgToken)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if bot.Username != "my_den_bot" || bot.ID != "42" || bot.DisplayName != "Den" {
		t.Errorf("bot = %+v", bot)
	}
}

func TestTelegramVerify_BadToken(t *testing.T) {
	f := &fakeTelegram{}
	tg := Telegram{BaseURL: f.server(t).URL}

	if _, err := tg.Verify(shortCtx(t), "999:wrong"); !errors.Is(err, ErrRejected) {
		t.Errorf("err = %v, want ErrRejected", err)
	}
}

func TestTelegramWaitForSender_BaselineSkipsBacklog(t *testing.T) {
	// Update 1 was sent before pairing started; it must not be captured even
	// though it is a valid private message.
	f := &fakeTelegram{updates: []string{update(1, "private", false, "old")}}
	tg := Telegram{BaseURL: f.server(t).URL}

	_, cursor, err := tg.WaitForSender(shortCtx(t), tgToken, "")
	if !errors.Is(err, ErrNoSender) {
		t.Fatalf("err = %v, want ErrNoSender", err)
	}
	if cursor != "2" {
		t.Errorf("cursor = %q, want 2 (past the backlog)", cursor)
	}

	f.mu.Lock()
	f.updates = append(f.updates, update(2, "private", false, "hi"))
	f.mu.Unlock()

	s, _, err := tg.WaitForSender(shortCtx(t), tgToken, cursor)
	if err != nil {
		t.Fatalf("second wait: %v", err)
	}
	if s.ID != "4821" || s.Username != "samk" || s.Text != "hi" || s.ChatID != "4821" {
		t.Errorf("sender = %+v", s)
	}
}

func TestTelegramWaitForSender_IgnoresGroupAndBots(t *testing.T) {
	f := &fakeTelegram{updates: []string{
		update(1, "group", false, "group msg"),
		update(2, "private", true, "bot msg"),
		update(3, "private", false, "me"),
	}}
	tg := Telegram{BaseURL: f.server(t).URL}

	s, cursor, err := tg.WaitForSender(shortCtx(t), tgToken, "1")
	if err != nil {
		t.Fatalf("WaitForSender: %v", err)
	}
	if s.Text != "me" || cursor != "4" {
		t.Errorf("sender = %+v cursor = %q, want the private human message and cursor 4", s, cursor)
	}
}

func TestTelegramWaitForSender_AcksCapturedUpdate(t *testing.T) {
	f := &fakeTelegram{updates: []string{update(1, "private", false, "hi")}}
	tg := Telegram{BaseURL: f.server(t).URL}

	if _, _, err := tg.WaitForSender(shortCtx(t), tgToken, "1"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if last := f.offsets[len(f.offsets)-1]; last != "2" {
		t.Errorf("last getUpdates offset = %q, want 2 so the adapter never replays the pairing message", last)
	}
}

func TestTelegramWaitForSender_Conflict409(t *testing.T) {
	f := &fakeTelegram{code: http.StatusConflict}
	tg := Telegram{BaseURL: f.server(t).URL}

	if _, _, err := tg.WaitForSender(shortCtx(t), tgToken, "1"); !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict", err)
	}
}

func TestTelegram_ErrorsNeverContainToken(t *testing.T) {
	srv := (&fakeTelegram{}).server(t)
	url := srv.URL
	srv.Close() // connection refused: Go's error would include the full URL

	_, err := Telegram{BaseURL: url}.Verify(shortCtx(t), tgToken)
	if err == nil {
		t.Fatal("expected an error from a closed server")
	}
	if strings.Contains(err.Error(), tgToken) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaks the token: %v", err)
	}
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}
