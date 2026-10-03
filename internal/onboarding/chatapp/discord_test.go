package chatapp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discordServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/@me" || r.Header.Get("Authorization") != "Bot good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":"1234567890","username":"den_bot","global_name":"Den"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDiscordVerify_OK(t *testing.T) {
	bot, err := Discord{BaseURL: discordServer(t).URL}.Verify(shortCtx(t), "good")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if bot.ID != "1234567890" || bot.DisplayName != "Den" {
		t.Errorf("bot = %+v", bot)
	}
	if !strings.Contains(bot.InviteURL, "client_id=1234567890") || !strings.Contains(bot.InviteURL, "scope=bot") {
		t.Errorf("invite URL = %q", bot.InviteURL)
	}
}

func TestDiscordVerify_BadToken(t *testing.T) {
	if _, err := (Discord{BaseURL: discordServer(t).URL}).Verify(shortCtx(t), "bad"); !errors.Is(err, ErrRejected) {
		t.Errorf("err = %v, want ErrRejected", err)
	}
}

func TestDiscordWaitForSender_Unsupported(t *testing.T) {
	if _, _, err := (Discord{}).WaitForSender(shortCtx(t), "t", ""); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}
