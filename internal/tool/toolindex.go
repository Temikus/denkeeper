package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/llm"
)

// Tool-name collision handling for the MCP registry: two servers contributing
// the same tool name are both advertised under server-qualified names, and the
// bare name becomes unroutable rather than resolving to whichever server
// happened to register last.
//
// Origin: item 3, "Tool key collisions and namespacing", of the
// spatiotemporal-composability review (docs/composability-findings.md, local),
// which audited this codebase against the model in "A Programming Paradigm for
// Spatiotemporal Composability" (Shi/Zhang/Cui 2026). The qualification scheme
// below is not prescribed by that paper — it is the fix for a gap the review
// surfaced: two components contributing one name to a shared registry, with no
// rule for composing them and no detection when they clash.

// ErrAmbiguousTool is returned when a bare tool name is advertised by more than
// one connected MCP server. Colliding tools are advertised — and must be called
// — under their server-qualified name, so resolving the bare name would mean
// guessing which server the caller meant. It never guesses. Callers classify
// with errors.Is, mirroring the ErrToolNotFound convention.
var ErrAmbiguousTool = errors.New("ambiguous tool name")

// toolNameSeparator joins a server name and a tool name into the qualified name
// used when two servers advertise the same tool. A double underscore is safe
// for the OpenAI/Anthropic function-name charsets, which reject ":".
const toolNameSeparator = "__"

// maxFunctionNameLen is the function-name length several providers enforce.
// Exceeding it is not an error here — the tool is still routable — but it is
// worth warning about, since the provider may reject the whole request.
const maxFunctionNameLen = 64

// qualifiedToolName renders the server-qualified form of a colliding tool name.
func qualifiedToolName(server, tool string) string {
	return server + toolNameSeparator + tool
}

// toolCollision records a bare tool name and the servers advertising it, in
// registration order.
type toolCollision struct {
	tool    string
	servers []string
}

// qualified renders the advertised name for each colliding owner.
func (c toolCollision) qualified() []string {
	names := make([]string, 0, len(c.servers))
	for _, s := range c.servers {
		names = append(names, qualifiedToolName(s, c.tool))
	}
	return names
}

// rebuildToolIndex re-derives owners, toolMap, localOf and toolDefs from the
// per-server tool lists (serverConn.tools). It is the only writer of those
// fields: every registration change rebuilds the whole projection instead of
// patching it in place, which is what keeps collision state — and its removal —
// consistent. In-place filtering is what used to drop the owner's map entry
// when the *other* collider was unregistered.
//
// It returns the collisions that appeared in this rebuild and the ones that
// cleared (survivor listed as the single server), so the caller can log and
// audit them after releasing the lock.
//
// Caller must hold m.mu (write lock).
func (m *Manager) rebuildToolIndex() (appeared, cleared []toolCollision) {
	before := m.collidingNames()

	owners := make(map[string][]*serverConn, len(m.toolMap))
	for _, serverName := range m.discoveryOrder {
		sc := m.servers[serverName]
		if sc == nil {
			continue
		}
		for _, td := range sc.tools {
			owners[td.Function.Name] = append(owners[td.Function.Name], sc)
		}
	}

	toolMap := make(map[string]*serverConn, len(owners))
	localOf := make(map[string]string)
	defs := make([]llm.ToolDef, 0, len(owners))
	for _, serverName := range m.discoveryOrder {
		sc := m.servers[serverName]
		if sc == nil {
			continue
		}
		for _, td := range sc.tools {
			local := td.Function.Name
			advertised := local
			if len(owners[local]) > 1 {
				advertised = qualifiedToolName(sc.name, local)
			}
			if prev, dup := toolMap[advertised]; dup {
				// A tool named literally "<server>__<tool>" can claim the
				// qualified form of somebody else's collision. Keep the first
				// claim so the advertised definition and its routing never
				// disagree, and drop the later one instead of misrouting it.
				m.logger.Warn("advertised MCP tool name claimed twice — keeping the first, dropping the later",
					slog.String("tool", advertised),
					slog.String("server", prev.name),
					slog.String("dropped_server", sc.name))
				continue
			}
			if advertised != local {
				localOf[advertised] = local
			}
			td.Function.Name = advertised // td is a copy; the server's own list keeps the local name
			toolMap[advertised] = sc
			defs = append(defs, td)
		}
	}

	m.owners = owners
	m.toolMap = toolMap
	m.localOf = localOf
	// Keep nil rather than an empty slice so an empty registry produces the
	// same (absent) tools payload as before.
	if len(defs) == 0 {
		defs = nil
	}
	m.toolDefs = defs

	after := m.collidingNames()
	for name, servers := range after {
		if _, had := before[name]; !had {
			appeared = append(appeared, toolCollision{tool: name, servers: servers})
		}
	}
	for name := range before {
		if _, still := after[name]; still {
			continue
		}
		// Report who is left, not who used to collide: that is the server
		// reclaiming the bare name (or nobody, if every owner went away).
		cleared = append(cleared, toolCollision{tool: name, servers: serverNames(owners[name])})
	}
	slices.SortFunc(appeared, func(a, b toolCollision) int { return strings.Compare(a.tool, b.tool) })
	slices.SortFunc(cleared, func(a, b toolCollision) int { return strings.Compare(a.tool, b.tool) })
	return appeared, cleared
}

// collidingNames returns the current bare names with more than one owner,
// mapped to their owning server names in registration order.
// Caller must hold m.mu.
func (m *Manager) collidingNames() map[string][]string {
	out := make(map[string][]string)
	for name, owners := range m.owners {
		if len(owners) < 2 {
			continue
		}
		out[name] = serverNames(owners)
	}
	return out
}

// serverNames projects a list of connections onto their configured names.
func serverNames(owners []*serverConn) []string {
	names := make([]string, 0, len(owners))
	for _, sc := range owners {
		names = append(names, sc.name)
	}
	return names
}

// resolveTool resolves an advertised or bare tool name to its owning server and
// the server-local tool name the MCP session expects.
//
// Bare names resolve only while they are unique; once two servers advertise the
// same name neither is reachable that way and ErrAmbiguousTool is returned,
// naming the owners and the qualified alternatives. Qualified names are matched
// by exact server-name prefix — never by splitting on the separator, which tool
// names may legitimately contain — and stay routable even after a collision
// clears, so a rule or in-flight call written against the qualified form does
// not break when the other server goes away.
//
// Caller must hold m.mu (read or write).
func (m *Manager) resolveTool(name string) (*serverConn, string, error) {
	if sc, ok := m.toolMap[name]; ok {
		if local, qualified := m.localOf[name]; qualified {
			return sc, local, nil
		}
		return sc, name, nil
	}

	if owners := m.owners[name]; len(owners) > 1 {
		return nil, "", ambiguousToolError(name, owners)
	}

	// Qualified name for a tool that is not (or is no longer) colliding.
	// discoveryOrder covers every server that has tools, and keeps the scan
	// deterministic.
	for _, serverName := range m.discoveryOrder {
		sc := m.servers[serverName]
		if sc == nil {
			continue
		}
		prefix := sc.name + toolNameSeparator
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		local := strings.TrimPrefix(name, prefix)
		for _, td := range sc.tools {
			if td.Function.Name == local {
				return sc, local, nil
			}
		}
	}

	return nil, "", fmt.Errorf("tool %q: %w", name, ErrToolNotFound)
}

// ambiguousToolError builds the readable ErrAmbiguousTool message: which
// servers claim the name, and what to call instead.
func ambiguousToolError(name string, owners []*serverConn) error {
	servers := make([]string, 0, len(owners))
	qualified := make([]string, 0, len(owners))
	for _, sc := range owners {
		servers = append(servers, sc.name)
		qualified = append(qualified, qualifiedToolName(sc.name, name))
	}
	return fmt.Errorf("tool %q is advertised by %d MCP servers (%s) — call it as %s: %w",
		name, len(owners), strings.Join(servers, ", "), strings.Join(qualified, " or "), ErrAmbiguousTool)
}

// noteDiscoveryOrder records a server's position in the advertised-tool
// ordering. Servers keep their first position across re-discovery; a server
// removed by UnregisterServer and re-registered moves to the end, which is the
// ordering the append-only toolDefs slice produced before this change.
// Caller must hold m.mu (write lock).
func (m *Manager) noteDiscoveryOrder(name string) {
	if slices.Contains(m.discoveryOrder, name) {
		return
	}
	m.discoveryOrder = append(m.discoveryOrder, name)
}

// reportCollisions logs and audits collisions that appeared during discovery of
// the trigger server. Call it after releasing m.mu — the auditor may block.
func (m *Manager) reportCollisions(ctx context.Context, trigger string, collisions []toolCollision) {
	for _, c := range collisions {
		qualified := c.qualified()
		m.logger.Warn("MCP tool name collision — advertising server-qualified names; the bare name is no longer advertised or routable",
			slog.String("tool", c.tool),
			slog.String("servers", strings.Join(c.servers, ", ")),
			slog.String("new_server", trigger),
			slog.String("qualified", strings.Join(qualified, ", ")))

		for _, q := range qualified {
			if len(q) > maxFunctionNameLen {
				m.logger.Warn("qualified MCP tool name exceeds the function-name length limit some LLM providers enforce",
					slog.String("tool", q),
					slog.Int("length", len(q)),
					slog.Int("limit", maxFunctionNameLen))
			}
		}

		if m.Auditor == nil {
			continue
		}
		detail, err := json.Marshal(map[string]any{
			"tool":       c.tool,
			"servers":    c.servers,
			"new_server": trigger,
			"qualified":  qualified,
		})
		if err != nil {
			detail = []byte("{}")
		}
		m.Auditor.Emit(ctx, audit.Event{
			Category: audit.CategoryMCP,
			Action:   "tool_name_collision",
			Summary: fmt.Sprintf("MCP tool %q is advertised by %d servers (%s) — advertising %s instead",
				c.tool, len(c.servers), strings.Join(c.servers, ", "), strings.Join(qualified, " and ")),
			Detail: string(detail),
			Status: audit.StatusError,
			Source: "tool_manager",
		})
	}
}

// reportClearedCollisions logs collisions that resolved because an owner went
// away: the survivor is advertised under the bare name again.
func (m *Manager) reportClearedCollisions(collisions []toolCollision) {
	for _, c := range collisions {
		if len(c.servers) != 1 {
			// More than one owner left — still colliding, nothing reclaimed.
			continue
		}
		m.logger.Info("MCP tool name collision cleared — the surviving server reclaims the bare name",
			slog.String("tool", c.tool),
			slog.String("server", c.servers[0]))
	}
}
