// Package toolrisk names the tool calls no standing approval may cover (#586):
// they install code, change which model answers, raise a schedule's tier or
// rewrite the agent's core instructions. It is a leaf package (standard library
// only) so approval, tool, agent and api share one table.
package toolrisk

import (
	"bytes"
	"encoding/json"
	"strings"
)

// LevelHigh is the only level Classify returns.
const LevelHigh = "high"

// Risk says why a call is high-risk and which of its arguments make it so.
type Risk struct {
	Level   string       `json:"level"`
	Warning string       `json:"warning"`
	Flagged []FlaggedArg `json:"flagged,omitempty"` // in table order
}

// FlaggedArg is one argument that makes the call risky, as compact JSON.
type FlaggedArg struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

const (
	warnToolAdd     = "Adds a tool server: starts a program on this host or connects to a remote server."
	warnPluginAdd   = "Adds a plugin: starts a process or container on this host."
	warnSetFallback = "Replaces the LLM fallback rules: which provider and model requests switch to."
	warnTier        = "Sets the permission tier this schedule runs at."
	warnSoul        = "Replaces the agent's soul (its core instructions)."
	warnIdentity    = "Replaces the agent's identity (name, emoji, theme)."
	warnPersona     = "Changes the agent's persona: its soul or identity."
)

// entry is one row of the table. check decodes the arguments the way the
// handler does and returns nil for a call that is not high-risk; an error
// means they do not parse, which Classify answers fail-closed.
type entry struct {
	always    bool
	humanOnly bool
	warning   string // also the fallback when the arguments do not parse
	check     func(args []byte) (*Risk, error)
}

var table = map[string]entry{
	"tool_add":        {always: true, humanOnly: true, warning: warnToolAdd, check: checkToolAdd},
	"plugin_add":      {always: true, humanOnly: true, warning: warnPluginAdd, check: checkPluginAdd},
	"set_fallback":    {always: true, warning: warnSetFallback, check: checkSetFallback},
	"schedule_add":    {warning: warnTier, check: checkScheduleAdd},
	"schedule_update": {warning: warnTier, check: checkScheduleUpdate},
	"persona_update":  {warning: warnPersona, check: checkPersonaUpdate},
}

// localName strips a collision qualifier ("<server>__tool_add"), the same
// rule tool.Manager uses for advertised names.
func localName(toolName string) string {
	if _, ok := table[toolName]; ok {
		return toolName
	}
	if i := strings.LastIndex(toolName, "__"); i >= 0 {
		return toolName[i+2:]
	}
	return toolName
}

// Classify returns nil for a call that is not high-risk. toolName is the
// advertised name, bare or collision-qualified. Classification is by name, so
// an external server's tool of the same name is treated the same way.
func Classify(toolName string, args []byte) *Risk {
	e, ok := table[localName(toolName)]
	if !ok {
		return nil
	}
	if len(bytes.TrimSpace(args)) == 0 {
		args = []byte("{}")
	}
	r, err := e.check(args)
	if err != nil {
		return &Risk{Level: LevelHigh, Warning: e.warning, Flagged: []FlaggedArg{{Name: "arguments", Value: string(args)}}}
	}
	return r
}

// AlwaysHigh reports whether every call to toolName is high-risk, whatever its
// arguments, so a rule naming it can never apply.
func AlwaysHigh(toolName string) bool {
	return table[localName(toolName)].always
}

// HumanOnly reports whether toolName needs a one-off human answer in every
// tier (DEN-82): tool_add and plugin_add start code on the host.
func HumanOnly(toolName string) bool {
	return table[localName(toolName)].humanOnly
}

// field is one candidate flagged argument; a nil raw value was absent.
type field struct {
	name string
	raw  json.RawMessage
}

func high(warning string, fields ...field) *Risk {
	r := &Risk{Level: LevelHigh, Warning: warning}
	for _, f := range fields {
		if f.raw == nil {
			continue
		}
		var buf bytes.Buffer
		value := string(f.raw)
		if json.Compact(&buf, f.raw) == nil {
			value = buf.String()
		}
		r.Flagged = append(r.Flagged, FlaggedArg{Name: f.name, Value: value})
	}
	return r
}

// The structs below carry the handlers' JSON tags (internal/configmcp), so a
// mixed-case or duplicate key resolves to the value the handler will act on.

func checkToolAdd(args []byte) (*Risk, error) {
	var a struct {
		Transport json.RawMessage `json:"transport"`
		Command   json.RawMessage `json:"command"`
		Args      json.RawMessage `json:"args"`
		URL       json.RawMessage `json:"url"`
		Env       json.RawMessage `json:"env"`
		Headers   json.RawMessage `json:"headers"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	return high(warnToolAdd, field{"transport", a.Transport}, field{"command", a.Command}, field{"args", a.Args},
		field{"url", a.URL}, field{"env", a.Env}, field{"headers", a.Headers}), nil
}

func checkPluginAdd(args []byte) (*Risk, error) {
	var a struct {
		Type    json.RawMessage `json:"type"`
		Command json.RawMessage `json:"command"`
		Image   json.RawMessage `json:"image"`
		Args    json.RawMessage `json:"args"`
		Env     json.RawMessage `json:"env"`
		Network json.RawMessage `json:"network"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	return high(warnPluginAdd, field{"type", a.Type}, field{"command", a.Command}, field{"image", a.Image},
		field{"args", a.Args}, field{"env", a.Env}, field{"network", a.Network}), nil
}

func checkSetFallback(args []byte) (*Risk, error) {
	var a struct {
		Rules json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	return high(warnSetFallback, field{"rules", a.Rules}), nil
}

type sessionTierArgs struct {
	SessionTier json.RawMessage `json:"session_tier"`
}

// schedule_add's handler reads a string, so an empty or null tier sets none.
// Any other value, including one that is not a string, counts.
func checkScheduleAdd(args []byte) (*Risk, error) {
	var a sessionTierArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	switch string(bytes.TrimSpace(a.SessionTier)) {
	case "", `""`, "null":
		return nil, nil
	}
	return high(warnTier, field{"session_tier", a.SessionTier}), nil
}

// schedule_update's handler reads a *string: any present value, even "",
// changes the tier, since clearing an override can raise a schedule that was
// pinned below its agent. null reads as absent there.
func checkScheduleUpdate(args []byte) (*Risk, error) {
	var a sessionTierArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	switch string(bytes.TrimSpace(a.SessionTier)) {
	case "", "null":
		return nil, nil
	}
	return high(warnTier, field{"session_tier", a.SessionTier}), nil
}

// persona_update's handler trims and lower-cases section. A section that is
// not a string fails there, so it is answered fail-closed here.
func checkPersonaUpdate(args []byte) (*Risk, error) {
	var a struct {
		Section json.RawMessage `json:"section"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, err
	}
	if a.Section == nil {
		return nil, nil
	}
	var section string
	if err := json.Unmarshal(a.Section, &section); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(strings.ToLower(section)) {
	case "soul":
		return high(warnSoul, field{"section", a.Section}), nil
	case "identity":
		return high(warnIdentity, field{"section", a.Section}), nil
	}
	return nil, nil
}
