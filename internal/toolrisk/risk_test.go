package toolrisk

import (
	"strings"
	"testing"
)

func flagged(r *Risk, name string) (string, bool) {
	for _, f := range r.Flagged {
		if f.Name == name {
			return f.Value, true
		}
	}
	return "", false
}

func TestClassify_AlwaysHighToolsAreHighWithEmptyArguments(t *testing.T) {
	for _, name := range []string{"tool_add", "plugin_add", "set_fallback"} {
		for _, args := range []string{"", "{}"} {
			r := Classify(name, []byte(args))
			if r == nil {
				t.Errorf("%s(%q): not high-risk", name, args)
				continue
			}
			if r.Level != LevelHigh || r.Warning == "" {
				t.Errorf("%s(%q): level %q, warning %q", name, args, r.Level, r.Warning)
			}
			if !AlwaysHigh(name) {
				t.Errorf("AlwaysHigh(%s) = false", name)
			}
		}
	}
}

func TestClassify_ToolAddFlagsWhatItRuns(t *testing.T) {
	r := Classify("tool_add", []byte(`{"name":"gh","command":"npx","args":["-y","pkg@1.0.0"],"env":{"A":"1"}}`))
	if r == nil {
		t.Fatal("tool_add not high-risk")
	}
	var names []string
	for _, f := range r.Flagged {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, ","); got != "command,args,env" {
		t.Errorf("flagged = %s, want command,args,env in table order", got)
	}
	if v, _ := flagged(r, "args"); v != `["-y","pkg@1.0.0"]` {
		t.Errorf("args value = %s, want compact JSON", v)
	}
}

func TestClassify_ScheduleAddOnlyWithSessionTier(t *testing.T) {
	if r := Classify("schedule_add", []byte(`{"name":"x","schedule":"@daily"}`)); r != nil {
		t.Errorf("schedule_add without session_tier: %+v, want nil", r)
	}
	if r := Classify("schedule_add", []byte(`{"name":"x","session_tier":""}`)); r != nil {
		t.Errorf("schedule_add with an empty session_tier: %+v, want nil", r)
	}
	r := Classify("schedule_add", []byte(`{"name":"x","session_tier":"autonomous"}`))
	if r == nil {
		t.Fatal("schedule_add with session_tier: not high-risk")
	}
	if v, ok := flagged(r, "session_tier"); !ok || v != `"autonomous"` {
		t.Errorf("session_tier flagged = %q, %v", v, ok)
	}
	if AlwaysHigh("schedule_add") {
		t.Error("AlwaysHigh(schedule_add) = true")
	}
}

// Clearing an override can raise a schedule that was pinned below its agent.
func TestClassify_ScheduleUpdateEmptySessionTierIsHigh(t *testing.T) {
	r := Classify("schedule_update", []byte(`{"name":"x","session_tier":""}`))
	if r == nil {
		t.Fatal(`schedule_update with "session_tier": "" not high-risk`)
	}
	if v, _ := flagged(r, "session_tier"); v != `""` {
		t.Errorf("session_tier flagged = %q", v)
	}
	if r := Classify("schedule_update", []byte(`{"name":"x","schedule":"@daily"}`)); r != nil {
		t.Errorf("schedule_update without session_tier: %+v, want nil", r)
	}
	// The handler's *string reads null as absent.
	if r := Classify("schedule_update", []byte(`{"name":"x","session_tier":null}`)); r != nil {
		t.Errorf("schedule_update with session_tier null: %+v, want nil", r)
	}
}

func TestClassify_PersonaUpdateSoulAndIdentityOnly(t *testing.T) {
	for _, section := range []string{"soul", "identity", " Soul ", "IDENTITY"} {
		r := Classify("persona_update", []byte(`{"section":"`+section+`","content":"x"}`))
		if r == nil {
			t.Errorf("persona_update %q: not high-risk", section)
			continue
		}
		if v, _ := flagged(r, "section"); v != `"`+section+`"` {
			t.Errorf("persona_update %q: section flagged = %s", section, v)
		}
	}
	soul := Classify("persona_update", []byte(`{"section":"soul"}`))
	identity := Classify("persona_update", []byte(`{"section":"identity"}`))
	if soul == nil || identity == nil || soul.Warning == identity.Warning {
		t.Errorf("soul and identity warnings should differ: %+v / %+v", soul, identity)
	}
	for _, section := range []string{"user", "memory"} {
		if r := Classify("persona_update", []byte(`{"section":"`+section+`","content":"x"}`)); r != nil {
			t.Errorf("persona_update %q: %+v, want nil", section, r)
		}
	}
}

// encoding/json matches keys case-insensitively, as the handler does.
func TestClassify_MixedCaseKeyIsCaught(t *testing.T) {
	if r := Classify("schedule_add", []byte(`{"name":"x","Session_Tier":"autonomous"}`)); r == nil {
		t.Error("Session_Tier not caught")
	}
}

// The handler keeps the last duplicate, so the flagged value must too.
func TestClassify_DuplicateKeyFlagsTheLastValue(t *testing.T) {
	r := Classify("schedule_add", []byte(`{"session_tier":"restricted","name":"x","session_tier":"autonomous"}`))
	if r == nil {
		t.Fatal("not high-risk")
	}
	if v, _ := flagged(r, "session_tier"); v != `"autonomous"` {
		t.Errorf("flagged %s, want the last value", v)
	}
}

func TestClassify_FlaggedArgumentAfterLongArgumentsIsWhole(t *testing.T) {
	long := strings.Repeat("x", 3000)
	r := Classify("tool_add", []byte(`{"name":"`+long+`","command":"npx","env":{"NODE_OPTIONS":"--require=/tmp/evil.js"}}`))
	if r == nil {
		t.Fatal("not high-risk")
	}
	if v, _ := flagged(r, "env"); v != `{"NODE_OPTIONS":"--require=/tmp/evil.js"}` {
		t.Errorf("env flagged = %s", v)
	}
}

func TestClassify_CollisionQualifiedNameMatchesBareName(t *testing.T) {
	if r := Classify("config-a__set_fallback", []byte(`{"rules":[]}`)); r == nil {
		t.Error("config-a__set_fallback not high-risk")
	}
	if !AlwaysHigh("config-a__tool_add") || !HumanOnly("config-a__tool_add") {
		t.Error("qualified tool_add not always-high and human-only")
	}
}

func TestClassify_UnparseableArgumentsFailClosed(t *testing.T) {
	for _, name := range []string{"set_fallback", "schedule_add", "persona_update"} {
		r := Classify(name, []byte(`{"section": "soul",`))
		if r == nil {
			t.Errorf("%s: unparseable arguments not high-risk", name)
			continue
		}
		if v, ok := flagged(r, "arguments"); !ok || v != `{"section": "soul",` || r.Warning == "" {
			t.Errorf("%s: flagged %q (%v), warning %q; want the raw text", name, v, ok, r.Warning)
		}
	}
}

func TestClassify_ToolOutsideTheTableIsNil(t *testing.T) {
	for _, name := range []string{"kv_get", "web_fetch", "my_tool_add", ""} {
		if r := Classify(name, []byte(`{"session_tier":"autonomous"}`)); r != nil {
			t.Errorf("%q: %+v, want nil", name, r)
		}
	}
	if HumanOnly("set_fallback") {
		t.Error("set_fallback is not human-only")
	}
}
