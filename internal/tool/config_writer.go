package tool

import (
	"fmt"

	"github.com/Temikus/denkeeper/internal/config"
)

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
	config.ConfigMu.Lock()
	defer config.ConfigMu.Unlock()

	raw, err := config.ReadRawConfig(path)
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

	return config.WriteRawConfig(path, raw)
}

// toolConfigToMap converts a ToolConfig to a map suitable for TOML serialization,
// omitting zero-value fields.
func toolConfigToMap(cfg config.ToolConfig) map[string]any {
	entry := map[string]any{}
	if cfg.Enabled != nil && !*cfg.Enabled {
		entry["enabled"] = false
	}
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
	toolConfigOAuthToMap(cfg, entry)
	if cfg.AllowLoopback {
		entry["allow_loopback"] = true
	}
	if len(cfg.DisabledTools) > 0 {
		entry["disabled_tools"] = cfg.DisabledTools
	}
	return entry
}

func toolConfigOAuthToMap(cfg config.ToolConfig, entry map[string]any) {
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
}

// updateDisabledToolsInConfig persists only the disabled_tools field for a
// specific tool server without touching any other config fields.
func updateDisabledToolsInConfig(path, name string, disabledTools []string) error {
	config.ConfigMu.Lock()
	defer config.ConfigMu.Unlock()

	raw, err := config.ReadRawConfig(path)
	if err != nil {
		return err
	}

	tools, ok := raw["tools"].(map[string]any)
	if !ok {
		return fmt.Errorf("config: tools section not found")
	}
	entry, ok := tools[name].(map[string]any)
	if !ok {
		return fmt.Errorf("config: tool %q not found", name)
	}

	if len(disabledTools) > 0 {
		entry["disabled_tools"] = disabledTools
	} else {
		delete(entry, "disabled_tools")
	}
	tools[name] = entry
	raw["tools"] = tools

	return config.WriteRawConfig(path, raw)
}

// updateEnabledInConfig persists only the enabled field for a specific tool
// server without touching any other config fields.
func updateEnabledInConfig(path, name string, enabled bool) error {
	configMu.Lock()
	defer configMu.Unlock()

	raw, err := readRawConfig(path)
	if err != nil {
		return err
	}

	tools, ok := raw["tools"].(map[string]any)
	if !ok {
		return fmt.Errorf("config: tools section not found")
	}
	entry, ok := tools[name].(map[string]any)
	if !ok {
		return fmt.Errorf("config: tool %q not found", name)
	}

	if enabled {
		delete(entry, "enabled")
	} else {
		entry["enabled"] = false
	}
	tools[name] = entry
	raw["tools"] = tools

	return writeRawConfig(path, raw)
}

// removeToolFromConfig reads the TOML config at path, removes [tools.<name>],
// and writes it back atomically.
func removeToolFromConfig(path, name string) error {
	config.ConfigMu.Lock()
	defer config.ConfigMu.Unlock()

	raw, err := config.ReadRawConfig(path)
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

	return config.WriteRawConfig(path, raw)
}

// addPluginToConfig reads the TOML config at path, adds a [plugins.<name>] section,
// and writes it back atomically.
func addPluginToConfig(path, name string, pe pluginEntry) error {
	config.ConfigMu.Lock()
	defer config.ConfigMu.Unlock()

	raw, err := config.ReadRawConfig(path)
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

	return config.WriteRawConfig(path, raw)
}

// removePluginFromConfig reads the TOML config at path, removes [plugins.<name>],
// and writes it back atomically.
func removePluginFromConfig(path, name string) error {
	config.ConfigMu.Lock()
	defer config.ConfigMu.Unlock()

	raw, err := config.ReadRawConfig(path)
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

	return config.WriteRawConfig(path, raw)
}
