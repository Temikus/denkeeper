package plugin

// PluginType is the execution strategy for a plugin.
type PluginType string

const (
	// TypeSubprocess runs the plugin as a trusted subprocess with direct MCP stdio communication.
	TypeSubprocess PluginType = "subprocess"
	// TypeDocker runs the plugin in a Docker/Podman container with resource limits and network isolation.
	TypeDocker PluginType = "docker"
)

// Capability declares a contract a plugin satisfies.
type Capability string

const (
	// CapabilityTools indicates the plugin exposes MCP tools to the LLM.
	CapabilityTools Capability = "tools"
)

// Plugin is the validated, normalised representation of a single plugin entry.
type Plugin struct {
	Name         string
	Type         PluginType
	Command      string
	Args         []string
	Env          map[string]string
	Capabilities []Capability

	// Docker-specific fields (only set when Type == TypeDocker).

	// Image is the Docker/OCI image to run.
	Image string
	// MemoryLimit is the container memory limit (e.g. "256m", "1g").
	MemoryLimit string
	// CPULimit is the container CPU limit (e.g. "0.5", "2").
	CPULimit string
	// Network is the Docker network mode. Defaults to "none".
	Network string
	// Volumes is a list of bind mounts ("host:container[:ro]").
	Volumes []string
}
