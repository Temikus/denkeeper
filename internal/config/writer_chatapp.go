package config

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/pelletier/go-toml/v2"
)

// ErrAgentNotInConfig is returned when a write targets an [[agents]] entry
// the TOML file does not contain.
var ErrAgentNotInConfig = errors.New("agent not found in config")

// ErrInvalidCandidate wraps the load error of the config a write would have
// produced. The file is left unchanged.
var ErrInvalidCandidate = errors.New("resulting config is invalid")

// ChatAdapterBinding is one chat app connection made in the setup wizard.
type ChatAdapterBinding struct {
	Section      string   // "telegram" or "discord"
	Token        string   // bot token
	AllowedUsers []string // user IDs; numeric for both, stored as int64 for telegram
	Agent        string   // agent to bind the adapter to
}

// SaveChatAdapter writes the adapter token, merges allowed_users, and binds
// the adapter to the agent in one locked write. The candidate file is loaded
// with Parse before anything is written, so a binding conflict or a token
// without users fails with ErrInvalidCandidate and leaves the file as it was.
// The agent must already have an [[agents]] entry: none is synthesized,
// because the legacy-agent path would also bind every adapter with a token.
func SaveChatAdapter(path string, b ChatAdapterBinding) error {
	if b.Section != "telegram" && b.Section != "discord" {
		return fmt.Errorf("unknown chat adapter %q", b.Section)
	}
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}
	if err := bindAdapterToAgent(raw, b.Agent, b.Section); err != nil {
		return err
	}
	section, _ := raw[b.Section].(map[string]any)
	if section == nil {
		section = make(map[string]any)
	}
	users, err := mergeAllowedUsers(section["allowed_users"], b.AllowedUsers, b.Section == "telegram")
	if err != nil {
		return err
	}
	section["token"] = b.Token
	section["allowed_users"] = users
	raw[b.Section] = section

	data, err := toml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if _, err := Parse(data); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidCandidate, err)
	}
	return WriteRawConfig(path, raw)
}

// bindAdapterToAgent adds adapter to the named agent's adapters list.
func bindAdapterToAgent(raw map[string]any, agent, adapter string) error {
	for _, a := range rawAgents(raw) {
		m, ok := a.(map[string]any)
		if !ok || m["name"] != agent {
			continue
		}
		adapters, _ := m["adapters"].([]any)
		if !slices.Contains(adapters, any(adapter)) {
			adapters = append(adapters, adapter)
		}
		m["adapters"] = adapters
		return nil
	}
	return fmt.Errorf("%w: %q", ErrAgentNotInConfig, agent)
}

// mergeAllowedUsers unions existing and added IDs, keeping existing order.
// Telegram IDs are written as integers; Discord snowflakes as strings.
func mergeAllowedUsers(existing any, added []string, numeric bool) ([]any, error) {
	var out []any
	seen := make(map[string]bool)
	add := func(id string) error {
		if id == "" || seen[id] {
			return nil
		}
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("allowed user %q is not a numeric ID", id)
		}
		seen[id] = true
		if numeric {
			out = append(out, n)
		} else {
			out = append(out, id)
		}
		return nil
	}
	list, _ := existing.([]any)
	for _, v := range list {
		if err := add(fmt.Sprint(v)); err != nil {
			return nil, err
		}
	}
	for _, id := range added {
		if err := add(id); err != nil {
			return nil, err
		}
	}
	return out, nil
}
