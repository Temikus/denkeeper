package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/Temikus/denkeeper/internal/config"
)

// ToolRegistrar is the interface Manager uses to register MCP tool servers.
// *tool.Manager satisfies this interface; inject a mock in tests.
type ToolRegistrar interface {
	RegisterServer(ctx context.Context, name, command string, args []string, env map[string]string) error
}

// Manager loads plugin configs, validates them, and registers their capabilities
// with the appropriate subsystem (currently only MCP tool servers via ToolRegistrar).
type Manager struct {
	plugins []Plugin
	logger  *slog.Logger
}

// NewManager creates a plugin Manager with no plugins loaded.
func NewManager(logger *slog.Logger) *Manager {
	return &Manager{logger: logger}
}

// Load validates all plugin configs and populates the internal plugin list.
// existingToolNames is used to detect name collisions with registered [tools.*] entries.
// Keys are processed in sorted order for deterministic error reporting.
// Returns an error on the first invalid plugin; does not continue past errors.
func (m *Manager) Load(plugins map[string]config.PluginConfig, existingToolNames map[string]bool) error {
	for _, name := range slices.Sorted(maps.Keys(plugins)) {
		pc := plugins[name]

		if PluginType(pc.Type) == TypeDocker {
			return fmt.Errorf("plugin %q: docker sandbox not yet implemented", name)
		}
		if pc.Command == "" {
			return fmt.Errorf("plugin %q: command is required", name)
		}
		if existingToolNames[name] {
			return fmt.Errorf("plugin %q: name conflicts with existing tool", name)
		}

		caps := make([]Capability, 0, len(pc.Capabilities))
		for _, c := range pc.Capabilities {
			cap := Capability(c)
			if cap != CapabilityTools {
				m.logger.Warn("unsupported capability, skipping", "plugin", name, "capability", c)
				continue
			}
			caps = append(caps, cap)
		}

		m.plugins = append(m.plugins, Plugin{
			Name:         name,
			Type:         PluginType(pc.Type),
			Command:      pc.Command,
			Args:         pc.Args,
			Env:          pc.Env,
			Capabilities: caps,
		})
	}
	return nil
}

// Start registers each loaded plugin's capabilities with the appropriate subsystem.
// Plugins with the "tools" capability are registered as MCP servers via tools.RegisterServer.
// A failure to register a plugin is logged as an error but does not halt other plugins.
// Returns the first error encountered, or nil if all plugins started successfully.
func (m *Manager) Start(ctx context.Context, tools ToolRegistrar) error {
	var firstErr error
	for _, p := range m.plugins {
		hasTools := false
		for _, c := range p.Capabilities {
			if c == CapabilityTools {
				hasTools = true
				break
			}
		}
		if !hasTools {
			continue
		}

		if err := tools.RegisterServer(ctx, p.Name, p.Command, p.Args, p.Env); err != nil {
			m.logger.Error("plugin start failed, skipping", "plugin", p.Name, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("plugin %q: %w", p.Name, err)
			}
			continue
		}
		m.logger.Info("plugin started", "plugin", p.Name)
	}
	return firstErr
}

// Count returns the number of successfully loaded plugins.
func (m *Manager) Count() int {
	return len(m.plugins)
}
