package config

import (
	"errors"
	"testing"
)

const chatAppBase = `
[[llm.providers]]
name = "anthropic"
type = "anthropic"
api_key = "k"

[[agents]]
name = "assistant"
llm_provider = "anthropic"
`

func TestSaveChatAdapter_Telegram(t *testing.T) {
	path := writeTestConfig(t, chatAppBase)

	err := SaveChatAdapter(path, ChatAdapterBinding{Section: "telegram", Token: "1:abc", AllowedUsers: []string{"48210937"}, Agent: "assistant"})
	if err != nil {
		t.Fatalf("SaveChatAdapter: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("saved config does not load: %v", err)
	}
	if cfg.Telegram.Token != "1:abc" || len(cfg.Telegram.AllowedUsers) != 1 || cfg.Telegram.AllowedUsers[0] != 48210937 {
		t.Errorf("telegram = %+v", cfg.Telegram)
	}
	if got := cfg.Agents[0].Adapters; len(got) != 1 || got[0] != "telegram" {
		t.Errorf("agent adapters = %v, want [telegram]", got)
	}
}

func TestSaveChatAdapter_Discord(t *testing.T) {
	path := writeTestConfig(t, chatAppBase)

	err := SaveChatAdapter(path, ChatAdapterBinding{Section: "discord", Token: "tok", AllowedUsers: []string{"123456789012345678"}, Agent: "assistant"})
	if err != nil {
		t.Fatalf("SaveChatAdapter: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("saved config does not load: %v", err)
	}
	if len(cfg.Discord.AllowedUsers) != 1 || cfg.Discord.AllowedUsers[0] != "123456789012345678" {
		t.Errorf("discord users = %v", cfg.Discord.AllowedUsers)
	}
}

func TestSaveChatAdapter_MergesAllowedUsers(t *testing.T) {
	path := writeTestConfig(t, chatAppBase+"\n[telegram]\ntoken = \"1:old\"\nallowed_users = [111]\n")

	err := SaveChatAdapter(path, ChatAdapterBinding{Section: "telegram", Token: "1:new", AllowedUsers: []string{"222", "111"}, Agent: "assistant"})
	if err != nil {
		t.Fatalf("SaveChatAdapter: %v", err)
	}
	cfg, _ := Load(path)
	if len(cfg.Telegram.AllowedUsers) != 2 || cfg.Telegram.AllowedUsers[0] != 111 || cfg.Telegram.AllowedUsers[1] != 222 {
		t.Errorf("allowed_users = %v, want [111 222]", cfg.Telegram.AllowedUsers)
	}
}

func TestSaveChatAdapter_AgentMissing(t *testing.T) {
	path := writeTestConfig(t, chatAppBase)

	err := SaveChatAdapter(path, ChatAdapterBinding{Section: "telegram", Token: "1:abc", AllowedUsers: []string{"1"}, Agent: "nobody"})
	if !errors.Is(err, ErrAgentNotInConfig) {
		t.Errorf("err = %v, want ErrAgentNotInConfig", err)
	}
}

func TestSaveChatAdapter_ConflictLeavesFileUnchanged(t *testing.T) {
	// Two agents may not both take the telegram wildcard binding.
	base := chatAppBase + "adapters = [\"telegram\"]\n\n[[agents]]\nname = \"other\"\nllm_provider = \"anthropic\"\n" +
		"\n[telegram]\ntoken = \"1:old\"\nallowed_users = [111]\n"
	path := writeTestConfig(t, base)
	before := readTestConfig(t, path)

	err := SaveChatAdapter(path, ChatAdapterBinding{Section: "telegram", Token: "1:abc", AllowedUsers: []string{"1"}, Agent: "other"})
	if !errors.Is(err, ErrInvalidCandidate) {
		t.Fatalf("err = %v, want ErrInvalidCandidate", err)
	}
	if after := readTestConfig(t, path); after != before {
		t.Errorf("file changed on a rejected write:\n%s", after)
	}
}

func TestSaveChatAdapter_NonNumericUserRejected(t *testing.T) {
	path := writeTestConfig(t, chatAppBase)

	if err := SaveChatAdapter(path, ChatAdapterBinding{Section: "telegram", Token: "1:abc", AllowedUsers: []string{"@samk"}, Agent: "assistant"}); err == nil {
		t.Error("expected error for a username instead of an ID")
	}
}
