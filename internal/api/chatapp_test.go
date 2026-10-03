package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/onboarding/chatapp"
)

const goodTGToken = "123456789:AAFabcdefghijklmnopqrstuvwxyz0123456"

// fakeProber scripts a chat platform for the endpoints.
type fakeProber struct {
	verifyErr error
	sender    chatapp.Sender
	waitErr   error
	block     chan struct{} // when set, WaitForSender blocks until closed or ctx ends
	mu        sync.Mutex
	cursors   []string
	notified  []string
}

func (f *fakeProber) Verify(context.Context, string) (chatapp.BotInfo, error) {
	return chatapp.BotInfo{ID: "42", Username: "my_den_bot"}, f.verifyErr
}

func (f *fakeProber) WaitForSender(ctx context.Context, _ string, cursor string) (chatapp.Sender, string, error) {
	f.mu.Lock()
	f.cursors = append(f.cursors, cursor)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return chatapp.Sender{}, "7", chatapp.ErrNoSender
		}
	}
	return f.sender, "8", f.waitErr
}

func (f *fakeProber) Notify(_ context.Context, _, chatID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notified = append(f.notified, chatID)
	return nil
}

func chatAppServer(t *testing.T, p chatapp.Prober, cfg *config.Config, toml string) *Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "denkeeper.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := testDepsWithPersona(t) // dispatcher with no adapters running
	deps.Config = config.NewHolder(cfg)
	deps.ConfigPath = path
	deps.ChatApps = map[string]chatapp.Prober{"telegram": p}
	deps.RestartFunc = func() error { return nil }
	return &Server{deps: deps, logger: testLogger()}
}

func callChatApp(t *testing.T, s *Server, h func(http.ResponseWriter, *http.Request), body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestChatAppVerify_OK(t *testing.T) {
	s := chatAppServer(t, &fakeProber{}, &config.Config{}, "")
	code, out := callChatApp(t, s, s.handleChatAppVerify, `{"type":"telegram","token":"`+goodTGToken+`"}`)
	msg, _ := out["message"].(string)
	if code != http.StatusOK || out["status"] != "ok" || !strings.Contains(msg, "@my_den_bot") {
		t.Errorf("code=%d out=%v", code, out)
	}
}

func TestChatAppVerify_Rejected(t *testing.T) {
	s := chatAppServer(t, &fakeProber{verifyErr: chatapp.ErrRejected}, &config.Config{}, "")
	_, out := callChatApp(t, s, s.handleChatAppVerify, `{"type":"telegram","token":"`+goodTGToken+`"}`)
	if out["status"] != "rejected" {
		t.Errorf("out = %v, want rejected", out)
	}
}

func TestChatAppVerify_MalformedTelegramToken400(t *testing.T) {
	s := chatAppServer(t, &fakeProber{}, &config.Config{}, "")
	if code, _ := callChatApp(t, s, s.handleChatAppVerify, `{"type":"telegram","token":"nope"}`); code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", code)
	}
}

func TestChatAppPair_Found(t *testing.T) {
	p := &fakeProber{sender: chatapp.Sender{ID: "4821", Username: "samk", ChatID: "4821"}}
	s := chatAppServer(t, p, &config.Config{}, "")
	_, out := callChatApp(t, s, s.handleChatAppPair, `{"type":"telegram","token":"`+goodTGToken+`","cursor":"5"}`)

	if out["status"] != "found" || out["cursor"] != "8" {
		t.Fatalf("out = %v, want found with cursor 8", out)
	}
	if p.cursors[0] != "5" {
		t.Errorf("cursor passed to prober = %q, want 5", p.cursors[0])
	}
}

func TestChatAppPair_TimeoutReturnsCursor(t *testing.T) {
	p := &fakeProber{block: make(chan struct{})}
	s := chatAppServer(t, p, &config.Config{}, "")
	_, out := callChatApp(t, s, s.handleChatAppPair, `{"type":"telegram","token":"`+goodTGToken+`","timeout_seconds":1}`)
	if out["status"] != "timeout" || out["cursor"] != "7" {
		t.Errorf("out = %v, want timeout with cursor 7", out)
	}
}

func TestChatAppPair_ConcurrentPair409(t *testing.T) {
	p := &fakeProber{block: make(chan struct{})}
	s := chatAppServer(t, p, &config.Config{}, "")

	first := make(chan int)
	go func() {
		code, _ := callChatApp(t, s, s.handleChatAppPair, `{"type":"telegram","token":"`+goodTGToken+`","timeout_seconds":5}`)
		first <- code
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		started := len(p.cursors) > 0
		p.mu.Unlock()
		if started || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	code, _ := callChatApp(t, s, s.handleChatAppPair, `{"type":"telegram","token":"`+goodTGToken+`"}`)
	close(p.block)
	if code != http.StatusConflict {
		t.Errorf("second pairing code = %d, want 409", code)
	}
	if c := <-first; c != http.StatusOK {
		t.Errorf("first pairing code = %d, want 200", c)
	}
}

const chatAppTOML = `
[[llm.providers]]
name = "anthropic"
type = "anthropic"
api_key = "k"

[[agents]]
name = "assistant"
llm_provider = "anthropic"
`

func chatAppConfig() *config.Config {
	return &config.Config{
		LLM:    config.LLMConfig{Providers: []config.ProviderInstanceConfig{{Name: "anthropic", Type: "anthropic", APIKey: "k"}}},
		Agents: []config.AgentInstanceConfig{{Name: "assistant", LLMProvider: "anthropic"}},
	}
}

func TestChatAppSave_PersistsAndReportsRestart(t *testing.T) {
	p := &fakeProber{}
	s := chatAppServer(t, p, chatAppConfig(), chatAppTOML)
	code, out := callChatApp(t, s, s.handleChatAppSave,
		`{"type":"telegram","token":"`+goodTGToken+`","allowed_users":["4821"],"notify_chat_id":"4821"}`)

	if code != http.StatusOK || out["status"] != "saved" || out["agent"] != "assistant" || out["restart_required"] != true {
		t.Fatalf("code=%d out=%v", code, out)
	}
	cfg := s.appConfig()
	if cfg.Telegram.Token != goodTGToken || len(cfg.Telegram.AllowedUsers) != 1 || cfg.Agents[0].Adapters[0] != "telegram" {
		t.Errorf("in-memory config not updated: telegram=%+v adapters=%v", cfg.Telegram, cfg.Agents[0].Adapters)
	}
	if len(p.notified) != 1 || p.notified[0] != "4821" {
		t.Errorf("notified = %v, want the paired chat", p.notified)
	}
}

func TestChatAppSave_EnvTokenShadow409(t *testing.T) {
	t.Setenv("DENKEEPER_TELEGRAM_TOKEN", "1:from-env")
	s := chatAppServer(t, &fakeProber{}, chatAppConfig(), chatAppTOML)
	code, _ := callChatApp(t, s, s.handleChatAppSave, `{"type":"telegram","token":"`+goodTGToken+`","allowed_users":["1"]}`)
	if code != http.StatusConflict {
		t.Errorf("code = %d, want 409", code)
	}
}

func TestChatAppSave_NoUsers400(t *testing.T) {
	s := chatAppServer(t, &fakeProber{}, chatAppConfig(), chatAppTOML)
	code, _ := callChatApp(t, s, s.handleChatAppSave, `{"type":"telegram","token":"`+goodTGToken+`","allowed_users":[]}`)
	if code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", code)
	}
}

func TestChatAppSave_BindingConflict409(t *testing.T) {
	toml := chatAppTOML + "adapters = [\"telegram\"]\n\n[[agents]]\nname = \"other\"\nllm_provider = \"anthropic\"\n\n[telegram]\ntoken = \"1:old\"\nallowed_users = [1]\n"
	cfg := chatAppConfig()
	cfg.Agents = append(cfg.Agents, config.AgentInstanceConfig{Name: "other"})
	s := chatAppServer(t, &fakeProber{}, cfg, toml)

	code, _ := callChatApp(t, s, s.handleChatAppSave, `{"type":"telegram","token":"`+goodTGToken+`","allowed_users":["1"],"agent":"other"}`)
	if code != http.StatusConflict {
		t.Errorf("code = %d, want 409 for a second wildcard telegram binding", code)
	}
}
