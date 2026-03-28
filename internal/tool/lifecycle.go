package tool

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"

	"github.com/Temikus/denkeeper/internal/config"
)

// DefaultMaxTools is the combined limit for tools + plugins.
const DefaultMaxTools = 50

// validName matches alphanumeric names with hyphens and underscores.
var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// PluginStatus exposes metadata about a registered plugin.
type PluginStatus struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Command      string   `json:"command,omitempty"`
	Image        string   `json:"image,omitempty"`
	Args         []string `json:"args,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	ToolNames    []string `json:"tool_names"`
	Status       string   `json:"status"`
}

// LifecycleManager coordinates adding and removing MCP tools and plugins
// at runtime, persisting changes to the TOML config file.
type LifecycleManager struct {
	toolMgr    *Manager
	configPath string
	maxTools   int
	mu         sync.Mutex
	logger     *slog.Logger

	// plugins tracks plugin metadata for list/inspect (keyed by name).
	plugins map[string]pluginMeta
}

// pluginMeta stores the config used to register a plugin, for list/inspect.
type pluginMeta struct {
	cfg  config.PluginConfig
	name string
}

// NewLifecycleManager creates a lifecycle manager wrapping the given tool.Manager.
// configPath is the path to denkeeper.toml. maxTools is the combined limit
// (0 uses DefaultMaxTools).
func NewLifecycleManager(toolMgr *Manager, configPath string, maxTools int, logger *slog.Logger) *LifecycleManager {
	if maxTools <= 0 {
		maxTools = DefaultMaxTools
	}
	return &LifecycleManager{
		toolMgr:    toolMgr,
		configPath: configPath,
		maxTools:   maxTools,
		logger:     logger,
		plugins:    make(map[string]pluginMeta),
	}
}

// AddTool validates the config, spawns the MCP server, registers it,
// and persists the [tools.<name>] section to denkeeper.toml.
func (lm *LifecycleManager) AddTool(ctx context.Context, name string, cfg config.ToolConfig) error {
	if err := validateToolName(name); err != nil {
		return err
	}
	if cfg.Command == "" {
		return fmt.Errorf("command is required")
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()

	if err := lm.checkConflict(name); err != nil {
		return err
	}
	if err := lm.checkLimit(); err != nil {
		return err
	}

	if err := lm.toolMgr.RegisterServer(ctx, name, cfg.Command, cfg.Args, cfg.Env); err != nil {
		return fmt.Errorf("starting tool %q: %w", name, err)
	}

	if err := addToolToConfig(lm.configPath, name, cfg.Command, cfg.Args, cfg.Env); err != nil {
		// Best-effort rollback: unregister the server we just started.
		_ = lm.toolMgr.UnregisterServer(name)
		return fmt.Errorf("persisting tool %q to config: %w", name, err)
	}

	lm.logger.Info("tool added", "name", name, "command", cfg.Command)
	return nil
}

// RemoveTool unregisters the MCP server and removes [tools.<name>] from denkeeper.toml.
func (lm *LifecycleManager) RemoveTool(ctx context.Context, name string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	if err := lm.toolMgr.UnregisterServer(name); err != nil {
		return fmt.Errorf("unregistering tool %q: %w", name, err)
	}

	if err := removeToolFromConfig(lm.configPath, name); err != nil {
		return fmt.Errorf("removing tool %q from config: %w", name, err)
	}

	lm.logger.Info("tool removed", "name", name)
	return nil
}

// AddPlugin validates, optionally spawns, registers, and persists the
// [plugins.<name>] section.
func (lm *LifecycleManager) AddPlugin(ctx context.Context, name string, cfg config.PluginConfig) error {
	if err := validateToolName(name); err != nil {
		return err
	}
	if cfg.Type != "subprocess" && cfg.Type != "docker" {
		return fmt.Errorf("type must be \"subprocess\" or \"docker\"")
	}
	if cfg.Type == "subprocess" && cfg.Command == "" {
		return fmt.Errorf("command is required for subprocess plugins")
	}
	if cfg.Type == "docker" && cfg.Image == "" {
		return fmt.Errorf("image is required for docker plugins")
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()

	if err := lm.checkConflict(name); err != nil {
		return err
	}
	if err := lm.checkLimit(); err != nil {
		return err
	}

	// Register as MCP server if plugin has "tools" capability.
	hasTools := false
	for _, c := range cfg.Capabilities {
		if c == "tools" {
			hasTools = true
			break
		}
	}

	if hasTools {
		switch cfg.Type {
		case "subprocess":
			if err := lm.toolMgr.RegisterServer(ctx, name, cfg.Command, cfg.Args, cfg.Env); err != nil {
				return fmt.Errorf("starting plugin %q: %w", name, err)
			}
		case "docker":
			dockerArgs := buildPluginDockerArgs(cfg)
			if err := lm.toolMgr.RegisterServer(ctx, name, "docker", dockerArgs, nil); err != nil {
				return fmt.Errorf("starting docker plugin %q: %w", name, err)
			}
		}
	}

	pe := pluginEntry{
		Type:         cfg.Type,
		Command:      cfg.Command,
		Image:        cfg.Image,
		Args:         cfg.Args,
		Env:          cfg.Env,
		Capabilities: cfg.Capabilities,
		MemoryLimit:  cfg.MemoryLimit,
		CPULimit:     cfg.CPULimit,
		Network:      cfg.Network,
		Volumes:      cfg.Volumes,
	}
	if err := addPluginToConfig(lm.configPath, name, pe); err != nil {
		if hasTools {
			_ = lm.toolMgr.UnregisterServer(name)
		}
		return fmt.Errorf("persisting plugin %q to config: %w", name, err)
	}

	lm.plugins[name] = pluginMeta{cfg: cfg, name: name}
	lm.logger.Info("plugin added", "name", name, "type", cfg.Type)
	return nil
}

// RemovePlugin unregisters and removes [plugins.<name>] from denkeeper.toml.
func (lm *LifecycleManager) RemovePlugin(ctx context.Context, name string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// Try to unregister from tool manager (may not be there if no tools capability).
	_ = lm.toolMgr.UnregisterServer(name)

	if err := removePluginFromConfig(lm.configPath, name); err != nil {
		return fmt.Errorf("removing plugin %q from config: %w", name, err)
	}

	delete(lm.plugins, name)
	lm.logger.Info("plugin removed", "name", name)
	return nil
}

// ListTools returns metadata for all registered MCP tool servers.
func (lm *LifecycleManager) ListTools() []ServerStatus {
	names := lm.toolMgr.ServerNames()
	var result []ServerStatus
	for _, name := range names {
		info, ok := lm.toolMgr.ServerInfo(name)
		if !ok {
			continue
		}
		// Exclude configmcp sessions and plugin entries.
		if info.Command == "" {
			continue // in-process session
		}
		lm.mu.Lock()
		_, isPlugin := lm.plugins[name]
		lm.mu.Unlock()
		if isPlugin {
			continue
		}
		result = append(result, info)
	}
	return result
}

// ListPlugins returns metadata for all registered plugins.
func (lm *LifecycleManager) ListPlugins() []PluginStatus {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	result := make([]PluginStatus, 0, len(lm.plugins))
	for name, meta := range lm.plugins {
		ps := PluginStatus{
			Name:         name,
			Type:         meta.cfg.Type,
			Command:      meta.cfg.Command,
			Image:        meta.cfg.Image,
			Args:         meta.cfg.Args,
			Capabilities: meta.cfg.Capabilities,
			Status:       "connected",
		}

		info, ok := lm.toolMgr.ServerInfo(name)
		if ok {
			ps.ToolNames = info.ToolNames
		} else {
			ps.Status = "stopped"
		}

		result = append(result, ps)
	}
	return result
}

// ToolManager returns the underlying tool.Manager.
func (lm *LifecycleManager) ToolManager() *Manager {
	return lm.toolMgr
}

// TrackPlugin registers a plugin that was loaded at startup so ListPlugins
// can report it. This avoids re-registering already-running plugins.
func (lm *LifecycleManager) TrackPlugin(name string, cfg config.PluginConfig) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.plugins[name] = pluginMeta{cfg: cfg, name: name}
}

// checkConflict verifies no server or plugin with this name is already registered.
func (lm *LifecycleManager) checkConflict(name string) error {
	if _, ok := lm.toolMgr.ServerInfo(name); ok {
		return fmt.Errorf("name %q conflicts with an existing tool or plugin", name)
	}
	if _, ok := lm.plugins[name]; ok {
		return fmt.Errorf("name %q conflicts with an existing plugin", name)
	}
	return nil
}

// checkLimit verifies the combined tool+plugin count is within the configured max.
func (lm *LifecycleManager) checkLimit() error {
	count := len(lm.toolMgr.ServerNames()) + len(lm.plugins)
	if count >= lm.maxTools {
		return fmt.Errorf("maximum number of tools reached (%d)", lm.maxTools)
	}
	return nil
}

func validateToolName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("name %q must be alphanumeric with hyphens or underscores", name)
	}
	return nil
}

// buildPluginDockerArgs constructs docker run arguments for a plugin config.
// Mirrors the logic in internal/plugin/docker.go.
func buildPluginDockerArgs(cfg config.PluginConfig) []string {
	args := []string{"run", "--rm", "-i"}

	network := cfg.Network
	if network == "" {
		network = "none"
	}
	args = append(args, "--network", network)
	args = append(args, "--cap-drop", "ALL", "--read-only", "--security-opt", "no-new-privileges")

	if cfg.MemoryLimit != "" {
		args = append(args, "--memory", cfg.MemoryLimit)
	}
	if cfg.CPULimit != "" {
		args = append(args, "--cpus", cfg.CPULimit)
	}

	for _, v := range cfg.Volumes {
		args = append(args, "-v", v)
	}
	for k, v := range cfg.Env {
		args = append(args, "-e", k+"="+v)
	}

	args = append(args, cfg.Image)
	if cfg.Command != "" {
		args = append(args, cfg.Command)
	}
	args = append(args, cfg.Args...)

	return args
}
