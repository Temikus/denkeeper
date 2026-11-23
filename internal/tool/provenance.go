package tool

import "errors"

// builtinUntrustedOutputTools names in-process tools whose results carry
// third-party text. Keyed by server-local tool name, in-process servers only.
// A new in-process tool that returns web or other third-party text must be
// added here, or its output reaches the model unframed.
var builtinUntrustedOutputTools = map[string]bool{"web_fetch": true, "web_search": true}

// Provenance says who wrote a tool's description and output.
type Provenance struct {
	Server          string // owning server; "" when the name does not resolve
	External        bool   // not an in-process session: its description and output are the server's
	UntrustedOutput bool   // External, or in builtinUntrustedOutputTools
}

// Provenance classifies toolName. An external server is always untrusted:
// idempotent, trust_annotations and guidance do not change that. An unknown
// or ambiguous name returns the zero value, since nothing runs for it. Only a
// genuine miss delegates to the parent.
func (m *Manager) Provenance(toolName string) Provenance {
	class, parent, err := m.classify(toolName)
	if err != nil {
		if parent != nil && errors.Is(err, ErrToolNotFound) {
			return parent.Provenance(toolName)
		}
		return Provenance{}
	}
	external := !class.inProcess
	return Provenance{
		Server:          class.server,
		External:        external,
		UntrustedOutput: external || builtinUntrustedOutputTools[class.local],
	}
}

// AdvertisesUntrustedOutput reports whether any enabled tool this manager
// (or its parent) advertises has untrusted output.
func (m *Manager) AdvertisesUntrustedOutput() bool {
	for _, name := range m.ToolNames() {
		if m.Provenance(name).UntrustedOutput {
			return true
		}
	}
	return false
}
