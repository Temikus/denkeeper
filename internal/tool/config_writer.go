package tool

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
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
func addToolToConfig(path, name, command string, args []string, env map[string]string) error {
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

	entry := map[string]any{"command": command}
	if len(args) > 0 {
		entry["args"] = args
	}
	if len(env) > 0 {
		entry["env"] = env
	}
	tools[name] = entry
	raw["tools"] = tools

	return writeRawConfig(path, raw)
}

// removeToolFromConfig reads the TOML config at path, removes [tools.<name>],
// and writes it back atomically.
func removeToolFromConfig(path, name string) error {
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
