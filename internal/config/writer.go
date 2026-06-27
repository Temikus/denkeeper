package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// ConfigMu serializes all config read-modify-write operations to prevent
// concurrent writes from losing updates. Each public config write function
// acquires this lock before reading and releases it after writing.
// Exported so that internal/tool can share the same lock for tool/plugin writes.
var ConfigMu sync.Mutex

// ---------------------------------------------------------------------------
// Core I/O
// ---------------------------------------------------------------------------

// ReadRawConfig reads a TOML file into a generic map.
func ReadRawConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- TOML config path from startup
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if raw == nil {
		raw = make(map[string]any)
	}
	return raw, nil
}

// WriteRawConfig marshals raw to TOML and writes it atomically via temp+rename.
func WriteRawConfig(path string, raw map[string]any) error {
	data, err := toml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Best-effort .bak of current config before overwriting.
	if _, statErr := os.Stat(path); statErr == nil {
		if existing, readErr := os.ReadFile(path); readErr == nil { // #nosec G304
			_ = os.WriteFile(path+".bak", existing, 0o600) //nolint:gosec // path is the startup config path, not user input
		}
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".denkeeper-config-*.toml")
	if err != nil {
		return fmt.Errorf("creating temp config file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing temp config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing temp config file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("committing config file: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Schedule config persistence
// ---------------------------------------------------------------------------

func scheduleToMap(name, schedExpr, skillName, channel, sessionMode, sessionTier, agent string, tags []string, enabled bool) map[string]any {
	m := map[string]any{
		"name":     name,
		"type":     "agent",
		"schedule": schedExpr,
		"channel":  channel,
		"enabled":  enabled,
	}
	if skillName != "" {
		m["skill"] = skillName
	}
	if sessionMode != "" {
		m["session_mode"] = sessionMode
	}
	if sessionTier != "" {
		m["session_tier"] = sessionTier
	}
	if agent != "" {
		m["agent"] = agent
	}
	if len(tags) > 0 {
		t := make([]any, len(tags))
		for i, v := range tags {
			t[i] = v
		}
		m["tags"] = t
	}
	return m
}

func rawSchedules(raw map[string]any) []any {
	switch v := raw["schedules"].(type) {
	case []any:
		return v
	case nil:
		return nil
	default:
		return nil
	}
}

// AddScheduleToConfig appends a [[schedules]] entry to the TOML config.
func AddScheduleToConfig(path, name, schedExpr, skillName, channel, sessionMode, sessionTier, agent string, tags []string, enabled bool) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	entry := scheduleToMap(name, schedExpr, skillName, channel, sessionMode, sessionTier, agent, tags, enabled)

	schedules := rawSchedules(raw)
	schedules = append(schedules, entry)
	raw["schedules"] = schedules
	return WriteRawConfig(path, raw)
}

// UpdateScheduleInConfig replaces a [[schedules]] entry matched by name.
func UpdateScheduleInConfig(path, name, schedExpr, skillName, channel, sessionMode, sessionTier, agent string, tags []string, enabled bool) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	entry := scheduleToMap(name, schedExpr, skillName, channel, sessionMode, sessionTier, agent, tags, enabled)

	schedules := rawSchedules(raw)
	found := false
	for i, s := range schedules {
		if m, ok := s.(map[string]any); ok && m["name"] == name {
			schedules[i] = entry
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("schedule %q not found in config", name)
	}
	raw["schedules"] = schedules
	return WriteRawConfig(path, raw)
}

// RemoveScheduleFromConfig removes a [[schedules]] entry matched by name.
func RemoveScheduleFromConfig(path, name string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	schedules := rawSchedules(raw)
	filtered := make([]any, 0, len(schedules))
	for _, s := range schedules {
		if m, ok := s.(map[string]any); ok && m["name"] == name {
			continue
		}
		filtered = append(filtered, s)
	}
	if len(filtered) == 0 {
		delete(raw, "schedules")
	} else {
		raw["schedules"] = filtered
	}
	return WriteRawConfig(path, raw)
}

// ---------------------------------------------------------------------------
// Channel config persistence
// ---------------------------------------------------------------------------

func rawChannels(raw map[string]any) []any {
	switch v := raw["channels"].(type) {
	case []any:
		return v
	case nil:
		return nil
	default:
		return nil
	}
}

func channelToMap(name, agentName, delivery, sessionMode string, adapters []string) map[string]any {
	m := map[string]any{
		"name":  name,
		"agent": agentName,
	}
	if delivery != "" {
		m["delivery"] = delivery
	}
	if sessionMode != "" {
		m["session_mode"] = sessionMode
	}
	if len(adapters) > 0 {
		a := make([]any, len(adapters))
		for i, v := range adapters {
			a[i] = v
		}
		m["adapters"] = a
	}
	return m
}

// AddChannelToConfig appends a [[channels]] entry to the TOML config.
func AddChannelToConfig(path, name, agentName, delivery, sessionMode string, adapters []string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	entry := channelToMap(name, agentName, delivery, sessionMode, adapters)

	channels := rawChannels(raw)
	channels = append(channels, entry)
	raw["channels"] = channels
	return WriteRawConfig(path, raw)
}

// UpdateChannelInConfig replaces a [[channels]] entry matched by name.
func UpdateChannelInConfig(path, name, agentName, delivery, sessionMode string, adapters []string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	entry := channelToMap(name, agentName, delivery, sessionMode, adapters)

	channels := rawChannels(raw)
	found := false
	for i, c := range channels {
		if m, ok := c.(map[string]any); ok && m["name"] == name {
			channels[i] = entry
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("channel %q not found in config", name)
	}
	raw["channels"] = channels
	return WriteRawConfig(path, raw)
}

// RemoveChannelFromConfig removes a [[channels]] entry matched by name.
func RemoveChannelFromConfig(path, name string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	channels := rawChannels(raw)
	filtered := make([]any, 0, len(channels))
	for _, c := range channels {
		if m, ok := c.(map[string]any); ok && m["name"] == name {
			continue
		}
		filtered = append(filtered, c)
	}
	if len(filtered) == 0 {
		delete(raw, "channels")
	} else {
		raw["channels"] = filtered
	}
	return WriteRawConfig(path, raw)
}

// ---------------------------------------------------------------------------
// Agent config persistence
// ---------------------------------------------------------------------------

func rawAgents(raw map[string]any) []any {
	switch v := raw["agents"].(type) {
	case []any:
		return v
	case nil:
		return nil
	default:
		return nil
	}
}

func synthesizeLegacyAgentEntry(raw map[string]any, name string) map[string]any {
	entry := map[string]any{"name": name}

	if adapters := legacyAdapters(raw); len(adapters) > 0 {
		entry["adapters"] = adapters
	}
	if tier := legacyStringField(raw, "session", "tier"); tier != "" {
		entry["session_tier"] = tier
	}
	if pd := legacyStringField(raw, "agent", "persona_dir"); pd != "" {
		entry["persona_dir"] = pd
	}
	if sd := legacyStringField(raw, "agent", "skills_dir"); sd != "" {
		entry["skills_dir"] = sd
	}

	return entry
}

func legacyAdapters(raw map[string]any) []any {
	var adapters []any
	if tg, ok := raw["telegram"].(map[string]any); ok && tg["token"] != nil && tg["token"] != "" {
		adapters = append(adapters, "telegram")
	}
	if dc, ok := raw["discord"].(map[string]any); ok && dc["token"] != nil && dc["token"] != "" {
		adapters = append(adapters, "discord")
	}
	return adapters
}

func legacyStringField(raw map[string]any, section, key string) string {
	if sec, ok := raw[section].(map[string]any); ok {
		if v, ok := sec[key].(string); ok {
			return v
		}
	}
	return ""
}

func agentToMap(name, provider, model, tier, description, personaDir string) map[string]any {
	m := map[string]any{"name": name}
	if provider != "" {
		m["llm_provider"] = provider
	}
	if model != "" {
		m["llm_model"] = model
	}
	if tier != "" {
		m["session_tier"] = tier
	}
	if description != "" {
		m["description"] = description
	}
	if personaDir != "" {
		m["persona_dir"] = personaDir
	}
	return m
}

// UpdateAgentInConfig updates fields of an [[agents]] entry matched by name.
// Only keys present in changes are applied (partial update).
// If no [[agents]] section exists (synthesized default agent), a new entry is
// created with the given name and changes applied.
func UpdateAgentInConfig(path, name string, changes map[string]any) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	agents := rawAgents(raw)
	found := false
	for i, a := range agents {
		m, ok := a.(map[string]any)
		if !ok || m["name"] != name {
			continue
		}
		for k, v := range changes {
			m[k] = v
		}
		agents[i] = m
		found = true
		break
	}
	if !found {
		if agents != nil {
			return fmt.Errorf("agent %q not found in config", name)
		}
		entry := synthesizeLegacyAgentEntry(raw, name)
		for k, v := range changes {
			entry[k] = v
		}
		agents = []any{entry}
	}
	raw["agents"] = agents
	return WriteRawConfig(path, raw)
}

// RenameAgentInConfig changes an agent's name in the TOML config and updates
// any [[schedules]] entries that reference the old name.
func RenameAgentInConfig(path, oldName, newName string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	agents := rawAgents(raw)
	found := false
	for i, a := range agents {
		m, ok := a.(map[string]any)
		if !ok || m["name"] != oldName {
			continue
		}
		m["name"] = newName
		agents[i] = m
		found = true
		break
	}
	if !found {
		return fmt.Errorf("agent %q not found in config", oldName)
	}
	raw["agents"] = agents

	for _, s := range rawSchedules(raw) {
		if m, ok := s.(map[string]any); ok && m["agent"] == oldName {
			m["agent"] = newName
		}
	}

	return WriteRawConfig(path, raw)
}

// AddAgentToConfig appends an [[agents]] entry to the TOML config.
func AddAgentToConfig(path, name, provider, model, tier, description, personaDir string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	entry := agentToMap(name, provider, model, tier, description, personaDir)

	agents := rawAgents(raw)
	agents = append(agents, entry)
	raw["agents"] = agents
	return WriteRawConfig(path, raw)
}

// RemoveAgentFromConfig removes an [[agents]] entry matched by name.
func RemoveAgentFromConfig(path, name string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	agents := rawAgents(raw)
	filtered := make([]any, 0, len(agents))
	for _, a := range agents {
		if m, ok := a.(map[string]any); ok && m["name"] == name {
			continue
		}
		filtered = append(filtered, a)
	}
	if len(filtered) == 0 {
		delete(raw, "agents")
	} else {
		raw["agents"] = filtered
	}
	return WriteRawConfig(path, raw)
}

// ---------------------------------------------------------------------------
// LLM config persistence
// ---------------------------------------------------------------------------

func rawProviders(raw map[string]any) []any {
	llm, ok := raw["llm"].(map[string]any)
	if !ok {
		return nil
	}
	switch v := llm["providers"].(type) {
	case []any:
		return v
	default:
		return nil
	}
}

func providerToMap(name, typ, apiKey, baseURL, organization string) map[string]any {
	m := map[string]any{
		"name": name,
		"type": typ,
	}
	if apiKey != "" {
		m["api_key"] = apiKey
	}
	if baseURL != "" {
		m["base_url"] = baseURL
	}
	if organization != "" {
		m["organization"] = organization
	}
	return m
}

// updateLLMProviderConfigLocked is the inner implementation called with ConfigMu held.
func updateLLMProviderConfigLocked(path, provider string, changes map[string]any) error {
	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	llmSection, ok := raw["llm"].(map[string]any)
	if !ok {
		llmSection = map[string]any{}
	}
	provSection, ok := llmSection[provider].(map[string]any)
	if !ok {
		provSection = map[string]any{}
	}
	// A nil value deletes the key, so a cleared field doesn't linger in TOML
	// and resurrect on restart (mirrors UpdateLLMProviderInstanceConfig).
	for k, v := range changes {
		if v == nil {
			delete(provSection, k)
		} else {
			provSection[k] = v
		}
	}
	llmSection[provider] = provSection
	raw["llm"] = llmSection

	return WriteRawConfig(path, raw)
}

// UpdateLLMConfig persists changes to top-level [llm] keys (default_provider,
// default_model, cost_limit_soft, cost_limit_hard). Only keys present in
// changes are applied (partial update).
func UpdateLLMConfig(path string, changes map[string]any) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	llmSection, ok := raw["llm"].(map[string]any)
	if !ok {
		llmSection = map[string]any{}
	}
	for k, v := range changes {
		llmSection[k] = v
	}
	raw["llm"] = llmSection

	return WriteRawConfig(path, raw)
}

// UpdateLLMProviderConfig persists changes to [llm.<provider>] in the TOML
// config. Only keys present in changes are applied (partial update); a key
// mapped to a nil value is deleted, letting callers clear a field rather than
// leave a stale value behind.
func UpdateLLMProviderConfig(path, provider string, changes map[string]any) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	return updateLLMProviderConfigLocked(path, provider, changes)
}

// UpdateLLMProviderInstanceConfig persists changes to a named entry in
// [[llm.providers]]. If the provider uses legacy config (i.e. it appears in
// [llm.<type>] rather than [[llm.providers]]), it falls back to the old-style
// persistence path.
func UpdateLLMProviderInstanceConfig(path, name string, changes map[string]any) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	llmSection, ok := raw["llm"].(map[string]any)
	if !ok {
		llmSection = map[string]any{}
	}

	providers, _ := llmSection["providers"].([]any)
	found := false
	for i, entry := range providers {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if m["name"] == name {
			for k, v := range changes {
				if v == nil {
					delete(m, k)
				} else {
					m[k] = v
				}
			}
			providers[i] = m
			found = true
			break
		}
	}

	if found {
		llmSection["providers"] = providers
		raw["llm"] = llmSection
		return WriteRawConfig(path, raw)
	}

	return updateLLMProviderConfigLocked(path, name, changes)
}

// AddLLMProviderToConfig appends a [[llm.providers]] entry to the TOML config.
func AddLLMProviderToConfig(path, name, typ, apiKey, baseURL, organization string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	entry := providerToMap(name, typ, apiKey, baseURL, organization)

	llmSection, ok := raw["llm"].(map[string]any)
	if !ok {
		llmSection = map[string]any{}
	}
	providers := rawProviders(raw)
	providers = append(providers, entry)
	llmSection["providers"] = providers
	raw["llm"] = llmSection
	return WriteRawConfig(path, raw)
}

// RemoveLLMProviderFromConfig removes a [[llm.providers]] entry matched by name.
func RemoveLLMProviderFromConfig(path, name string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	llmSection, ok := raw["llm"].(map[string]any)
	if !ok {
		return nil
	}

	providers := rawProviders(raw)
	filtered := make([]any, 0, len(providers))
	for _, p := range providers {
		if m, ok := p.(map[string]any); ok && m["name"] == name {
			continue
		}
		filtered = append(filtered, p)
	}
	if len(filtered) == 0 {
		delete(llmSection, "providers")
	} else {
		llmSection["providers"] = filtered
	}
	raw["llm"] = llmSection
	return WriteRawConfig(path, raw)
}

// ---------------------------------------------------------------------------
// API config persistence
// ---------------------------------------------------------------------------

// UpdateAPIConfig persists changes to the [api] section of the TOML config.
// Only keys present in changes are applied (partial update).
func UpdateAPIConfig(path string, changes map[string]any) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	apiSection, ok := raw["api"].(map[string]any)
	if !ok {
		apiSection = map[string]any{}
	}
	// Nested map values (e.g. mcp_server) are merged key-by-key rather than
	// replaced wholesale, so callers can update individual sub-keys without
	// clobbering siblings. Scalar values are replaced as before.
	for k, v := range changes {
		if sub, isSub := v.(map[string]any); isSub {
			existing, _ := apiSection[k].(map[string]any)
			if existing == nil {
				existing = map[string]any{}
			}
			for sk, sv := range sub {
				existing[sk] = sv
			}
			apiSection[k] = existing
		} else {
			apiSection[k] = v
		}
	}
	raw["api"] = apiSection

	return WriteRawConfig(path, raw)
}

// ---------------------------------------------------------------------------
// Auth config persistence
// ---------------------------------------------------------------------------

// SetSessionSecret persists only the session_secret into [api.auth] without
// touching other auth fields. Used at startup to auto-generate a stable secret.
func SetSessionSecret(path, sessionSecret string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	apiSection, ok := raw["api"].(map[string]any)
	if !ok {
		apiSection = map[string]any{}
	}
	authSection, ok := apiSection["auth"].(map[string]any)
	if !ok {
		authSection = map[string]any{}
	}

	authSection["session_secret"] = sessionSecret
	apiSection["auth"] = authSection
	raw["api"] = apiSection

	return WriteRawConfig(path, raw)
}

// SetAuthConfig persists password_hash and session_secret to [api.auth] in the
// TOML config file. Used by the PIN-protected account setup flow.
func SetAuthConfig(path, passwordHash, sessionSecret string) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	apiSection, ok := raw["api"].(map[string]any)
	if !ok {
		apiSection = map[string]any{}
	}
	authSection, ok := apiSection["auth"].(map[string]any)
	if !ok {
		authSection = map[string]any{}
	}

	authSection["password_hash"] = passwordHash
	authSection["session_secret"] = sessionSecret
	apiSection["auth"] = authSection
	raw["api"] = apiSection

	return WriteRawConfig(path, raw)
}

// UpdateAuthConfig applies partial updates to the [api.auth] section of the
// TOML config. Only keys present in changes are written; other fields are
// preserved. Used for password changes and preference updates.
func UpdateAuthConfig(path string, changes map[string]any) error {
	ConfigMu.Lock()
	defer ConfigMu.Unlock()

	raw, err := ReadRawConfig(path)
	if err != nil {
		return err
	}

	apiSection, ok := raw["api"].(map[string]any)
	if !ok {
		apiSection = map[string]any{}
	}
	authSection, ok := apiSection["auth"].(map[string]any)
	if !ok {
		authSection = map[string]any{}
	}
	for k, v := range changes {
		authSection[k] = v
	}
	apiSection["auth"] = authSection
	raw["api"] = apiSection

	return WriteRawConfig(path, raw)
}
