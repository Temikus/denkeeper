package configmcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/configmcp"
)

// channelTestEnv bundles test server state for channel tests.
type channelTestEnv struct {
	channels   map[string]*agent.Channel
	activeKeys map[string]string // adapterKey → channelName
	session    *mcp.ClientSession
}

func (e *channelTestEnv) call(t *testing.T, name string, args any) (string, bool) {
	t.Helper()
	return callTool(t, e.session, name, args)
}

func newTestServerWithChannels(t *testing.T) *channelTestEnv {
	t.Helper()

	env := &channelTestEnv{
		channels: map[string]*agent.Channel{
			"work": {
				Name:      "work",
				AgentName: "agent-a",
				Adapters:  []string{"telegram:123"},
				Delivery:  "single",
			},
			"personal": {
				Name:      "personal",
				AgentName: "agent-b",
				Adapters:  []string{"discord:456", "telegram"},
				Delivery:  "broadcast",
			},
		},
		activeKeys: make(map[string]string),
	}

	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetChannels = func() map[string]*agent.Channel {
			return env.channels
		}
		d.SetActiveChannel = func(_ context.Context, key, ch string) error {
			if _, ok := env.channels[ch]; !ok {
				return fmt.Errorf("channel not found: %q", ch)
			}
			env.activeKeys[key] = ch
			return nil
		}
		d.ActiveChannelsForChannel = func(name string) []string {
			var keys []string
			for k, v := range env.activeKeys {
				if v == name {
					keys = append(keys, k)
				}
			}
			return keys
		}
	})

	env.session = session
	return env
}

// --------------------------------------------------------------------------
// Tests: channel_list
// --------------------------------------------------------------------------

func TestChannelList_WithChannels(t *testing.T) {
	env := newTestServerWithChannels(t)

	text, isErr := env.call(t, "channel_list", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var result []map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(result))
	}

	// Sorted by name: personal, work.
	if result[0]["name"] != "personal" {
		t.Errorf("expected first channel 'personal', got %q", result[0]["name"])
	}
	if result[1]["name"] != "work" {
		t.Errorf("expected second channel 'work', got %q", result[1]["name"])
	}
	if result[1]["agent"] != "agent-a" {
		t.Errorf("expected agent 'agent-a', got %q", result[1]["agent"])
	}
}

func TestChannelList_Empty(t *testing.T) {
	session, _ := newTestServer(t, func(d *configmcp.Deps) {
		d.GetChannels = func() map[string]*agent.Channel {
			return nil
		}
	})

	text, isErr := callTool(t, session, "channel_list", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}
	if text != "[]" {
		t.Errorf("expected empty array, got %q", text)
	}
}

func TestChannelList_WithActiveKeys(t *testing.T) {
	env := newTestServerWithChannels(t)
	env.activeKeys["telegram:999"] = "work"

	text, isErr := env.call(t, "channel_list", map[string]any{})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var result []map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// work channel should have active_adapter_keys.
	for _, ch := range result {
		if ch["name"] == "work" {
			keys, ok := ch["active_adapter_keys"].([]any)
			if !ok || len(keys) != 1 {
				t.Errorf("expected 1 active key for work, got %v", ch["active_adapter_keys"])
			}
			return
		}
	}
	t.Error("work channel not found in result")
}

// --------------------------------------------------------------------------
// Tests: channel_switch
// --------------------------------------------------------------------------

func TestChannelSwitch_Success(t *testing.T) {
	env := newTestServerWithChannels(t)

	text, isErr := env.call(t, "channel_switch", map[string]any{
		"adapter_key":  "telegram:999",
		"channel_name": "work",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	if env.activeKeys["telegram:999"] != "work" {
		t.Errorf("expected active channel 'work', got %q", env.activeKeys["telegram:999"])
	}
}

func TestChannelSwitch_MissingAdapterKey(t *testing.T) {
	env := newTestServerWithChannels(t)

	text, isErr := env.call(t, "channel_switch", map[string]any{
		"channel_name": "work",
	})
	if !isErr {
		t.Fatalf("expected error, got: %s", text)
	}
}

func TestChannelSwitch_MissingChannelName(t *testing.T) {
	env := newTestServerWithChannels(t)

	text, isErr := env.call(t, "channel_switch", map[string]any{
		"adapter_key": "telegram:999",
	})
	if !isErr {
		t.Fatalf("expected error, got: %s", text)
	}
}

func TestChannelSwitch_NotFound(t *testing.T) {
	env := newTestServerWithChannels(t)

	text, isErr := env.call(t, "channel_switch", map[string]any{
		"adapter_key":  "telegram:999",
		"channel_name": "nonexistent",
	})
	if !isErr {
		t.Fatalf("expected error, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: channel_info
// --------------------------------------------------------------------------

func TestChannelInfo_Success(t *testing.T) {
	env := newTestServerWithChannels(t)

	text, isErr := env.call(t, "channel_info", map[string]any{
		"channel_name": "work",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", text)
	}

	var detail map[string]any
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if detail["name"] != "work" {
		t.Errorf("expected name 'work', got %q", detail["name"])
	}
	if detail["agent"] != "agent-a" {
		t.Errorf("expected agent 'agent-a', got %q", detail["agent"])
	}
	if detail["conversation_id"] != "chan:work" {
		t.Errorf("expected conversation_id 'chan:work', got %q", detail["conversation_id"])
	}
}

func TestChannelInfo_NotFound(t *testing.T) {
	env := newTestServerWithChannels(t)

	text, isErr := env.call(t, "channel_info", map[string]any{
		"channel_name": "nonexistent",
	})
	if !isErr {
		t.Fatalf("expected error, got: %s", text)
	}
}

func TestChannelInfo_MissingName(t *testing.T) {
	env := newTestServerWithChannels(t)

	text, isErr := env.call(t, "channel_info", map[string]any{})
	if !isErr {
		t.Fatalf("expected error, got: %s", text)
	}
}

// --------------------------------------------------------------------------
// Tests: tool discovery
// --------------------------------------------------------------------------

func TestServer_ListTools_IncludesChannelTools(t *testing.T) {
	env := newTestServerWithChannels(t)

	result, err := env.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	want := map[string]bool{
		"channel_list":   false,
		"channel_switch": false,
		"channel_info":   false,
	}
	for _, tool := range result.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q not listed", name)
		}
	}
}

func TestServer_ListTools_NoChannelToolsWithoutDeps(t *testing.T) {
	session, _ := newTestServer(t, nil)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	for _, tool := range result.Tools {
		if tool.Name == "channel_list" || tool.Name == "channel_switch" || tool.Name == "channel_info" {
			t.Errorf("channel tool %q should not be listed without deps", tool.Name)
		}
	}
}
