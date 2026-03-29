// Package sandbox defines the pluggable sandbox runtime interface for
// executing MCP server plugins in isolated environments. Backends include
// Docker/Podman (standalone) and Kubernetes (cluster deployments).
package sandbox

import "context"

// NetworkPolicy controls the network access level for a sandbox.
type NetworkPolicy string

const (
	NetworkNone   NetworkPolicy = "none"   // no network access
	NetworkEgress NetworkPolicy = "egress" // outbound only
	NetworkFull   NetworkPolicy = "full"   // unrestricted
)

// SpawnOpts describes the sandboxed environment to create for a plugin.
type SpawnOpts struct {
	Image       string            // OCI image to run
	Command     string            // entrypoint override (empty = image default)
	Args        []string          // arguments passed to the entrypoint
	Env         map[string]string // environment variables
	MemoryLimit string            // memory limit (e.g. "256m", "1g")
	CPULimit    string            // CPU limit (e.g. "0.5", "2")
	Network     NetworkPolicy     // network access level
	Volumes     []string          // bind mounts ("host:container[:ro]")
}

// Process describes how to connect to a spawned sandbox's stdin/stdout
// for MCP stdio transport. The caller execs Command with Args and
// communicates over the process's stdio pipes.
type Process struct {
	Command string            // executable (e.g. "docker", "kubectl")
	Args    []string          // arguments
	Env     map[string]string // additional env vars (nil if baked into args)
}

// Runtime is the interface for sandbox backends that can spawn isolated
// environments for MCP server plugins. Each backend implements the same
// lifecycle: spawn a sandbox, return connection info, and tear down on stop.
type Runtime interface {
	// Spawn creates a sandboxed environment and returns connection info.
	// The returned Process describes the command to exec for MCP stdio.
	// name is a unique identifier for the sandbox (typically the plugin name).
	Spawn(ctx context.Context, name string, opts SpawnOpts) (*Process, error)

	// Stop tears down the sandbox identified by name. For Docker this is a
	// no-op (--rm handles cleanup). For Kubernetes this deletes the Pod.
	Stop(ctx context.Context, name string) error

	// Close releases all runtime resources and stops any active sandboxes.
	Close() error
}
