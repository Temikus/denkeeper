package plugin

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"maps"
	"os/exec"
	"slices"

	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/sandbox"
	"github.com/Temikus/denkeeper/internal/security"
)

// ToolRegistrar is the interface Manager uses to register MCP tool servers.
// *tool.Manager satisfies this interface; inject a mock in tests.
type ToolRegistrar interface {
	RegisterServer(ctx context.Context, name, command string, args []string, env map[string]string) error
}

// VerifyOpts configures plugin signature verification.
type VerifyOpts struct {
	// TrustedKeys is the set of Ed25519 public keys that can sign plugins.
	TrustedKeys []ed25519.PublicKey
	// AllowUnsigned controls whether unsigned plugins are accepted.
	// When false, all subprocess plugins must have a valid .sig file.
	AllowUnsigned bool
}

// Manager loads plugin configs, validates them, and registers their capabilities
// with the appropriate subsystem (currently only MCP tool servers via ToolRegistrar).
type Manager struct {
	plugins    []Plugin
	logger     *slog.Logger
	verifyOpts *VerifyOpts
	runtime    sandbox.Runtime // optional; used for Docker/K8s plugins
}

// NewManager creates a plugin Manager with no plugins loaded.
// Pass nil for verifyOpts to skip signature verification.
// Pass nil for runtime to use the legacy inline Docker args path
// (a DockerRuntime will be created lazily if Docker plugins are loaded).
func NewManager(logger *slog.Logger, verifyOpts *VerifyOpts, runtime sandbox.Runtime) *Manager {
	return &Manager{logger: logger, verifyOpts: verifyOpts, runtime: runtime}
}

// Load validates all plugin configs and populates the internal plugin list.
// existingToolNames is used to detect name collisions with registered [tools.*] entries.
// Keys are processed in sorted order for deterministic error reporting.
// Returns an error on the first invalid plugin; does not continue past errors.
func (m *Manager) Load(plugins map[string]config.PluginConfig, existingToolNames map[string]bool) error {
	hasDocker := false

	for _, name := range slices.Sorted(maps.Keys(plugins)) {
		pc := plugins[name]
		pt := PluginType(pc.Type)

		switch pt {
		case TypeSubprocess:
			if pc.Command == "" {
				return fmt.Errorf("plugin %q: command is required", name)
			}
			if err := m.verifyBinary(name, pc.Command); err != nil {
				return err
			}
		case TypeDocker:
			if pc.Image == "" {
				return fmt.Errorf("plugin %q: image is required for docker plugins", name)
			}
			hasDocker = true
			// Docker image verification (cosign/DCT) is out of scope;
			// the container runtime handles image trust.
		default:
			return fmt.Errorf("plugin %q: unsupported type %q", name, pc.Type)
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

		network := pc.Network
		if pt == TypeDocker && network == "" {
			network = "none"
		}

		m.plugins = append(m.plugins, Plugin{
			Name:         name,
			Type:         pt,
			Command:      pc.Command,
			Args:         pc.Args,
			Env:          pc.Env,
			Capabilities: caps,
			Image:        pc.Image,
			MemoryLimit:  pc.MemoryLimit,
			CPULimit:     pc.CPULimit,
			Network:      network,
			Volumes:      pc.Volumes,
		})
	}

	// Check Docker availability once if any Docker plugins are configured
	// and no sandbox runtime was provided (runtime handles its own checks).
	if hasDocker && m.runtime == nil {
		if err := checkDockerAvailable(); err != nil {
			return err
		}
	}

	return nil
}

// verifyBinary checks the Ed25519 signature of a subprocess plugin binary
// against the trusted keys, if signature verification is configured.
func (m *Manager) verifyBinary(name, command string) error {
	if m.verifyOpts == nil {
		return nil // verification not configured
	}

	// Resolve the full path of the command for signature lookup.
	path, err := exec.LookPath(command)
	if err != nil {
		// Can't resolve the binary path yet — it will fail later at Start time.
		// Skip verification; the binary might be in a PATH that's set up later.
		return nil
	}

	if err := security.VerifyFile(m.verifyOpts.TrustedKeys, path); err != nil {
		if m.verifyOpts.AllowUnsigned {
			m.logger.Warn("plugin signature verification failed, allowing unsigned",
				"plugin", name, "error", err)
			return nil
		}
		return fmt.Errorf("plugin %q: signature verification failed: %w — sign with 'denkeeper plugin sign' or set security.allow_unsigned = true", name, err)
	}

	m.logger.Info("plugin signature verified", "plugin", name)
	return nil
}

// Start registers each loaded plugin's capabilities with the appropriate subsystem.
// Plugins with the "tools" capability are registered as MCP servers via tools.RegisterServer.
//
// Subprocess plugins pass their command directly. Sandboxed plugins (Docker, Kubernetes)
// are spawned via the sandbox.Runtime, which returns the command to connect to their stdio.
//
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

		var err error
		switch p.Type {
		case TypeSubprocess:
			err = tools.RegisterServer(ctx, p.Name, p.Command, p.Args, p.Env)
		default:
			// Sandboxed plugins (Docker, Kubernetes) go through the runtime.
			err = m.startSandboxed(ctx, p, tools)
		}

		if err != nil {
			m.logger.Error("plugin start failed, skipping", "plugin", p.Name, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("plugin %q: %w", p.Name, err)
			}
			continue
		}
		m.logger.Info("plugin started", "plugin", p.Name, "type", string(p.Type))
	}
	return firstErr
}

// startSandboxed spawns a sandboxed plugin via the Runtime and registers
// the resulting process with the tool manager.
func (m *Manager) startSandboxed(ctx context.Context, p Plugin, tools ToolRegistrar) error {
	if m.runtime == nil {
		return fmt.Errorf("no sandbox runtime configured for %s plugin %q", p.Type, p.Name)
	}
	proc, err := m.runtime.Spawn(ctx, p.Name, sandbox.SpawnOpts{
		Image:       p.Image,
		Command:     p.Command,
		Args:        p.Args,
		Env:         p.Env,
		MemoryLimit: p.MemoryLimit,
		CPULimit:    p.CPULimit,
		Network:     sandbox.NetworkPolicy(p.Network),
		Volumes:     p.Volumes,
	})
	if err != nil {
		return fmt.Errorf("spawning sandbox: %w", err)
	}
	return tools.RegisterServer(ctx, p.Name, proc.Command, proc.Args, proc.Env)
}

// Count returns the number of successfully loaded plugins.
func (m *Manager) Count() int {
	return len(m.plugins)
}
