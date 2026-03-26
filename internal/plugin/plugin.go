package plugin

// PluginType is the execution strategy for a plugin.
type PluginType string

const (
	// TypeSubprocess runs the plugin as a trusted subprocess with direct MCP stdio communication.
	TypeSubprocess PluginType = "subprocess"
	// TypeDocker is reserved for future sandboxed execution and is not yet implemented.
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
}
