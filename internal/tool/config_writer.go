package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pelletier/go-toml/v2"

	"github.com/Temikus/denkeeper/internal/config"
)

// configMu serializes all config read-modify-write operations to prevent
// concurrent writes from losing updates. Each public config write function
// acquires this lock before reading and releases it after writing.
var configMu sync.Mutex

type pluginEntry struct {
	Type         string            `toml:"type"`
	Command      string            `toml:"command,omitempty"`
	Image        string            `toml:"image,omitempty"`
	Args         []string          `toml:"args,omitempty"`
	Env          map[string]string `toml:"env,omitempty"`
	Capabilities []string          `toml:"capabilities,omitempty"`
	MemoryLimit  string            `toml:"memory_limit,omitempty"`
	CPULimit     string            `toml:"cpu_limit,omitempty"`
	Network      string            `toml:"network,omitempty"`
	Volumes      []string          `toml:"volumes,omitempty"`
}

// addToolToConfig reads the TOML config at path, adds a [tools.<name>] section,
// and writes it back atomically.
func addToolToConfig(path, name string, cfg config.ToolConfig) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
	if err != nil {
		return err
	}

	if raw["tools"] == nil {
		raw["tools"] = map[string]any{}
	}
	tools, ok := raw["tools"].(map[string]any)
	if !ok {
		return fmt.Errorf("config: tools section has unexpected type")
	}

	tools[name] = toolConfigToMap(cfg)
	raw["tools"] = tools

	return writeRawConfig(path, raw)
}

// toolConfigToMap converts a ToolConfig to a map suitable for TOML serialization,
// omitting zero-value fields.
func toolConfigToMap(cfg config.ToolConfig) map[string]any {
	entry := map[string]any{}
	if cfg.Transport != "" && cfg.Transport != "stdio" {
		entry["transport"] = cfg.Transport
	}
	if cfg.Command != "" {
		entry["command"] = cfg.Command
	}
	if cfg.URL != "" {
		entry["url"] = cfg.URL
	}
	if len(cfg.Args) > 0 {
		entry["args"] = cfg.Args
	}
	if len(cfg.Env) > 0 {
		entry["env"] = cfg.Env
	}
	if len(cfg.Headers) > 0 {
		entry["headers"] = cfg.Headers
	}
	if cfg.RequestTimeoutSecs > 0 {
		entry["request_timeout_secs"] = cfg.RequestTimeoutSecs
	}
	if cfg.SSEKeepAliveSecs > 0 {
		entry["sse_keep_alive_secs"] = cfg.SSEKeepAliveSecs
	}
	if cfg.Auth != "" {
		entry["auth"] = cfg.Auth
	}
	if cfg.ClientID != "" {
		entry["client_id"] = cfg.ClientID
	}
	if cfg.ClientSecret != "" {
		entry["client_secret"] = cfg.ClientSecret
	}
	if len(cfg.Scopes) > 0 {
		entry["scopes"] = cfg.Scopes
	}
	if cfg.AllowLoopback {
		entry["allow_loopback"] = true
	}
	return entry
}

// removeToolFromConfig reads the TOML config at path, removes [tools.<name>],
// and writes it back atomically.
func removeToolFromConfig(path, name string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
	if err != nil {
		return err
	}

	tools, ok := raw["tools"].(map[string]any)
	if !ok {
		return nil // no tools section, nothing to remove
	}
	delete(tools, name)
	if len(tools) == 0 {
		delete(raw, "tools")
	} else {
		raw["tools"] = tools
	}

	return writeRawConfig(path, raw)
}

// addPluginToConfig reads the TOML config at path, adds a [plugins.<name>] section,
// and writes it back atomically.
func addPluginToConfig(path, name string, pe pluginEntry) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
	if err != nil {
		return err
	}

	if raw["plugins"] == nil {
		raw["plugins"] = map[string]any{}
	}
	plugins, ok := raw["plugins"].(map[string]any)
	if !ok {
		return fmt.Errorf("config: plugins section has unexpected type")
	}

	entry := map[string]any{"type": pe.Type}
	if pe.Command != "" {
		entry["command"] = pe.Command
	}
	if pe.Image != "" {
		entry["image"] = pe.Image
	}
	if len(pe.Args) > 0 {
		entry["args"] = pe.Args
	}
	if len(pe.Env) > 0 {
		entry["env"] = pe.Env
	}
	if len(pe.Capabilities) > 0 {
		entry["capabilities"] = pe.Capabilities
	}
	if pe.MemoryLimit != "" {
		entry["memory_limit"] = pe.MemoryLimit
	}
	if pe.CPULimit != "" {
		entry["cpu_limit"] = pe.CPULimit
	}
	if pe.Network != "" {
		entry["network"] = pe.Network
	}
	if len(pe.Volumes) > 0 {
		entry["volumes"] = pe.Volumes
	}
	plugins[name] = entry
	raw["plugins"] = plugins

	return writeRawConfig(path, raw)
}

// removePluginFromConfig reads the TOML config at path, removes [plugins.<name>],
// and writes it back atomically.
func removePluginFromConfig(path, name string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
	if err != nil {
		return err
	}

	plugins, ok := raw["plugins"].(map[string]any)
	if !ok {
		return nil // no plugins section
	}
	delete(plugins, name)
	if len(plugins) == 0 {
		delete(raw, "plugins")
	} else {
		raw["plugins"] = plugins
	}

	return writeRawConfig(path, raw)
}

// ---------------------------------------------------------------------------
// Schedule config persistence
// ---------------------------------------------------------------------------

// scheduleToMap converts schedule fields to a generic map for TOML serialization.
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
		// Convert to []any for TOML array compatibility.
		t := make([]any, len(tags))
		for i, v := range tags {
			t[i] = v
		}
		m["tags"] = t
	}
	return m
}

// AddScheduleToConfig appends a [[schedules]] entry to the TOML config.
func AddScheduleToConfig(path, name, schedExpr, skillName, channel, sessionMode, sessionTier, agent string, tags []string, enabled bool) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
	if err != nil {
		return err
	}

	entry := scheduleToMap(name, schedExpr, skillName, channel, sessionMode, sessionTier, agent, tags, enabled)

	schedules := rawSchedules(raw)
	schedules = append(schedules, entry)
	raw["schedules"] = schedules
	return writeRawConfig(path, raw)
}

// UpdateScheduleInConfig replaces a [[schedules]] entry matched by name.
func UpdateScheduleInConfig(path, name, schedExpr, skillName, channel, sessionMode, sessionTier, agent string, tags []string, enabled bool) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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
	return writeRawConfig(path, raw)
}

// RemoveScheduleFromConfig removes a [[schedules]] entry matched by name.
func RemoveScheduleFromConfig(path, name string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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
	return writeRawConfig(path, raw)
}

// ---------------------------------------------------------------------------
// Channel config persistence
// ---------------------------------------------------------------------------

// rawChannels extracts the channels array from the raw config map.
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

// channelToMap converts channel fields to a generic map for TOML serialization.
// Zero-value fields are omitted.
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
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
	if err != nil {
		return err
	}

	entry := channelToMap(name, agentName, delivery, sessionMode, adapters)

	channels := rawChannels(raw)
	channels = append(channels, entry)
	raw["channels"] = channels
	return writeRawConfig(path, raw)
}

// UpdateChannelInConfig replaces a [[channels]] entry matched by name.
func UpdateChannelInConfig(path, name, agentName, delivery, sessionMode string, adapters []string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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
	return writeRawConfig(path, raw)
}

// RemoveChannelFromConfig removes a [[channels]] entry matched by name.
func RemoveChannelFromConfig(path, name string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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
	return writeRawConfig(path, raw)
}

// ---------------------------------------------------------------------------
// Agent config persistence
// ---------------------------------------------------------------------------

// UpdateAgentInConfig updates fields of an [[agents]] entry matched by name.
// Only keys present in changes are applied (partial update).
// If no [[agents]] section exists (synthesized default agent), a new entry is
// created with the given name and changes applied.
func UpdateAgentInConfig(path, name string, changes map[string]any) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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
			// [[agents]] exists but this name isn't in it — real error.
			return fmt.Errorf("agent %q not found in config", name)
		}
		// No [[agents]] section — the agent was synthesized. Create the
		// entry with legacy fields so a reload doesn't lose adapters/tier.
		entry := synthesizeLegacyAgentEntry(raw, name)
		for k, v := range changes {
			entry[k] = v
		}
		agents = []any{entry}
	}
	raw["agents"] = agents
	return writeRawConfig(path, raw)
}

// RenameAgentInConfig changes an agent's name in the TOML config and updates
// any [[schedules]] entries that reference the old name.
func RenameAgentInConfig(path, oldName, newName string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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

	return writeRawConfig(path, raw)
}

// agentToMap converts agent creation fields to a generic map for TOML serialization.
// Zero-value fields are omitted.
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

// AddAgentToConfig appends an [[agents]] entry to the TOML config.
func AddAgentToConfig(path, name, provider, model, tier, description, personaDir string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
	if err != nil {
		return err
	}

	entry := agentToMap(name, provider, model, tier, description, personaDir)

	agents := rawAgents(raw)
	agents = append(agents, entry)
	raw["agents"] = agents
	return writeRawConfig(path, raw)
}

// RemoveAgentFromConfig removes an [[agents]] entry matched by name.
func RemoveAgentFromConfig(path, name string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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
	return writeRawConfig(path, raw)
}

// rawAgents extracts the agents array from the raw config map.
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

// synthesizeLegacyAgentEntry builds an [[agents]] entry from legacy
// [agent]/[session]/[telegram]/[discord] sections, mirroring
// config.synthesizeDefaultAgent so that a reload produces the same result.
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

// legacyAdapters infers the adapters list from legacy top-level adapter sections.
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

// legacyStringField returns a string value from a nested section of the raw config.
func legacyStringField(raw map[string]any, section, key string) string {
	if sec, ok := raw[section].(map[string]any); ok {
		if v, ok := sec[key].(string); ok {
			return v
		}
	}
	return ""
}

// rawSchedules extracts the schedules array from the raw config map.
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

// ---------------------------------------------------------------------------
// LLM config persistence
// ---------------------------------------------------------------------------

// UpdateLLMConfig persists changes to top-level [llm] keys (default_provider,
// default_model, cost_limit_soft, cost_limit_hard). Only keys present in
// changes are applied (partial update).
func UpdateLLMConfig(path string, changes map[string]any) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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

	return writeRawConfig(path, raw)
}

// UpdateLLMProviderConfig persists changes to [llm.<provider>] in the TOML
// config. Only keys present in changes are applied (partial update).
func UpdateLLMProviderConfig(path, provider string, changes map[string]any) error {
	configMu.Lock()
	defer configMu.Unlock()

	return updateLLMProviderConfigLocked(path, provider, changes)
}

// updateLLMProviderConfigLocked is the inner implementation called with configMu held.
func updateLLMProviderConfigLocked(path, provider string, changes map[string]any) error {
	raw, err := readRawConfig(path)
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
	for k, v := range changes {
		provSection[k] = v
	}
	llmSection[provider] = provSection
	raw["llm"] = llmSection

	return writeRawConfig(path, raw)
}

// UpdateLLMProviderInstanceConfig persists changes to a named entry in
// [[llm.providers]]. If the provider uses legacy config (i.e. it appears in
// [llm.<type>] rather than [[llm.providers]]), it falls back to the old-style
// persistence path.
func UpdateLLMProviderInstanceConfig(path, name string, changes map[string]any) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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
				m[k] = v
			}
			providers[i] = m
			found = true
			break
		}
	}

	if found {
		llmSection["providers"] = providers
		raw["llm"] = llmSection
		return writeRawConfig(path, raw)
	}

	// Fall back to legacy [llm.<name>] section (for configs not yet migrated).
	return updateLLMProviderConfigLocked(path, name, changes)
}

// ---------------------------------------------------------------------------
// API config persistence
// ---------------------------------------------------------------------------

// UpdateAPIConfig persists changes to the [api] section of the TOML config.
// Only keys present in changes are applied (partial update).
func UpdateAPIConfig(path string, changes map[string]any) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
	if err != nil {
		return err
	}

	apiSection, ok := raw["api"].(map[string]any)
	if !ok {
		apiSection = map[string]any{}
	}
	for k, v := range changes {
		apiSection[k] = v
	}
	raw["api"] = apiSection

	return writeRawConfig(path, raw)
}

// ---------------------------------------------------------------------------
// Auth config persistence
// ---------------------------------------------------------------------------

// SetSessionSecret persists only the session_secret into [api.auth] without
// touching other auth fields. Used at startup to auto-generate a stable secret.
func SetSessionSecret(path, sessionSecret string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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

	return writeRawConfig(path, raw)
}

// SetAuthConfig persists password_hash and session_secret to [api.auth] in the
// TOML config file. Used by the PIN-protected account setup flow.
func SetAuthConfig(path, passwordHash, sessionSecret string) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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

	return writeRawConfig(path, raw)
}

// UpdateAuthConfig applies partial updates to the [api.auth] section of the
// TOML config. Only keys present in changes are written; other fields are
// preserved. Used for password changes and preference updates.
func UpdateAuthConfig(path string, changes map[string]any) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
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

	return writeRawConfig(path, raw)
}

// readRawConfig reads a TOML file into a generic map.
func readRawConfig(path string) (map[string]any, error) {
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

// writeRawConfig marshals raw to TOML and writes it atomically via temp+rename.
func writeRawConfig(path string, raw map[string]any) error {
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
