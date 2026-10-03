package chatapp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestDiscordVerify_DoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer elsewhere.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/users/@me", http.StatusFound)
	}))
	defer up.Close()

	if _, err := (Discord{BaseURL: up.URL}).Verify(shortCtx(t), "good"); err == nil {
		t.Error("Verify succeeded through a redirect")
	}
	if _, err := (Discord{BaseURL: up.URL, HTTP: &http.Client{}}).Verify(shortCtx(t), "good"); err == nil {
		t.Error("Verify with a supplied client succeeded through a redirect")
	}
	if hits.Load() != 0 {
		t.Error("Verify followed a redirect, which would carry the token header")
	}
}

func TestDiscordWaitForSender_Unsupported(t *testing.T) {
	if _, _, err := (Discord{}).WaitForSender(shortCtx(t), "t", ""); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}
