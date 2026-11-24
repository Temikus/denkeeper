package agent

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/toolrisk"
)

// promptPaths drives one pending event through handleToolApproval on all three
// render paths and returns what each sent, keyed by path.
func promptPaths(t *testing.T, evt ChatEvent) map[string]adapter.OutgoingMessage {
	t.Helper()
	msg := adapter.IncomingMessage{Adapter: "slack", ExternalID: "chat-1"}
	a := &mockAdapter{name: "slack"}
	d := &Dispatcher{adapters: map[string]adapter.Adapter{"slack": a}, logger: testLogger()}
	d.handleToolApproval(context.Background(), a, msg, evt, false, nil)
	d.handleToolApproval(context.Background(), a, msg, evt, true, nil)
	me := &mockMessageEditor{msgID: "m1"}
	d.handleToolApproval(context.Background(), a, msg, evt, false, newTestActivityLog(me))
	if len(a.sent) != 2 || len(me.sends) != 1 {
		t.Fatalf("sends = %d/%d, want 2/1", len(a.sent), len(me.sends))
	}
	return map[string]adapter.OutgoingMessage{"standalone": a.sent[0], "debug": a.sent[1], "activity log": me.sends[0]}
}

func fallbackRisk(rules string) *toolrisk.Risk {
	return &toolrisk.Risk{
		Level:   "high",
		Warning: "Replaces the LLM fallback rules: which provider and model requests switch to.",
		Flagged: []toolrisk.FlaggedArg{{Name: "rules", Value: rules}},
	}
}

func callbacks(out adapter.OutgoingMessage) []string {
	var got []string
	for _, b := range out.Buttons {
		got = append(got, b.CallbackData)
	}
	return got
}

// A harmless prompt must render byte for byte as before #586.
func TestApprovalPrompt_HarmlessCallRendersAsBefore(t *testing.T) {
	evt := ChatEvent{Type: "tool_approval", Tool: "fetch_url", Text: `{"url":"a&b"}`, ApprovalCallback: "appr:1"}
	out := promptPaths(t, evt)

	html := "🔧 <b>fetch_url</b> — approve?\n<blockquote expandable>{&#34;url&#34;:&#34;a&amp;b&#34;}</blockquote>"
	want := map[string]string{
		"standalone":   html,
		"activity log": html,
		"debug":        "Agent wants to execute tool **fetch_url**\n\n```\n{\"url\":\"a&b\"}\n```\n\nApprove?",
	}
	for path, text := range want {
		if out[path].Text != text {
			t.Errorf("%s text = %q\nwant %q", path, out[path].Text, text)
		}
		if n := len(out[path].Buttons); n != 4 {
			t.Errorf("%s: %d buttons, want 4", path, n)
		}
	}
}

func TestApprovalPrompt_HighRiskShowsWarningFlaggedThenArgsWithTwoButtons(t *testing.T) {
	evt := ChatEvent{Type: "tool_approval", Tool: "set_fallback", Text: `{"rules":[{"to":"cheap"}],"note":"x"}`,
		ApprovalCallback: "appr:1", ApprovalRisk: fallbackRisk(`[{"to":"cheap"}]`)}

	for path, out := range promptPaths(t, evt) {
		warning := strings.Index(out.Text, "Replaces the LLM fallback rules")
		flagged := strings.Index(out.Text, "rules:")
		args := strings.Index(out.Text, "note")
		if warning < 0 || flagged < 0 || args < 0 {
			t.Fatalf("%s: missing warning, flagged line or arguments: %q", path, out.Text)
		}
		if warning >= flagged || flagged >= args {
			t.Errorf("%s: order is warning %d, flagged %d, args %d; want that order: %q", path, warning, flagged, args, out.Text)
		}
		if !strings.Contains(out.Text, "High risk.") || !strings.Contains(out.Text, "Approve once or deny.") {
			t.Errorf("%s: no high-risk lead or answer line: %q", path, out.Text)
		}
		if got := callbacks(out); len(got) != 2 || got[0] != "appr:1:approve" || got[1] != "appr:1:deny" {
			t.Errorf("%s: buttons = %v, want approve and deny only", path, got)
		}
	}
	if html := promptPaths(t, evt)["standalone"].Text; !strings.Contains(html, "<b>rules:</b> <code>[{&#34;to&#34;:&#34;cheap&#34;}]</code>") {
		t.Errorf("standalone flagged line is not bold name plus code value: %q", html)
	}
}

// tool_add and plugin_add are human-only and always high-risk: the risk block
// says why there are only two buttons, so the human-only note is dropped
// rather than said twice. Without a risk the note stays (HumanOnlyApproval_SaysWhy).
func TestApprovalPrompt_HighRiskHumanOnlyShowsTheRiskNotTheNote(t *testing.T) {
	risk := &toolrisk.Risk{Level: "high", Warning: "Adds a tool server: starts a program on this host or connects to a remote server.",
		Flagged: []toolrisk.FlaggedArg{{Name: "command", Value: `"npx"`}}}
	evt := ChatEvent{Type: "tool_approval", Tool: "tool_add", Text: `{"command":"npx"}`,
		ApprovalCallback: "appr:1", ApprovalHumanOnly: true, ApprovalRisk: risk}

	for path, out := range promptPaths(t, evt) {
		if strings.Contains(out.Text, humanOnlyApprovalNote) {
			t.Errorf("%s: human-only note repeated next to the risk block: %q", path, out.Text)
		}
		if !strings.Contains(out.Text, "Adds a tool server: starts a program") || !strings.Contains(out.Text, "Approve once or deny.") {
			t.Errorf("%s: risk block missing: %q", path, out.Text)
		}
		if got := callbacks(out); len(got) != 2 {
			t.Errorf("%s: buttons = %v, want approve and deny", path, got)
		}
	}
}

// The model cannot push a flagged value past the arguments cut by putting
// padding in front of it: the flagged line is rendered from the risk, in full.
func TestApprovalPrompt_FlaggedValuePastTheArgsCutIsShownInFull(t *testing.T) {
	value := `"autonomous-tier-marker"`
	args := `{"padding":"` + strings.Repeat("p", 2500) + `","session_tier":` + value + `}`
	risk := &toolrisk.Risk{Level: "high", Warning: "Sets the permission tier this schedule runs at.",
		Flagged: []toolrisk.FlaggedArg{{Name: "session_tier", Value: value}}}
	evt := ChatEvent{Type: "tool_approval", Tool: "schedule_add", Text: args, ApprovalCallback: "appr:1", ApprovalRisk: risk}

	for path, out := range promptPaths(t, evt) {
		if !strings.Contains(out.Text, "autonomous-tier-marker") {
			t.Errorf("%s: flagged value missing: %q", path, out.Text)
		}
		if len(out.Text) > telegramMessageMaxBytes {
			t.Errorf("%s: %d bytes, over Telegram's cap", path, len(out.Text))
		}
	}
}

func TestApprovalPrompt_FlaggedLinesOverBudgetOfferDenyOnly(t *testing.T) {
	long := `"` + strings.Repeat("v", approvalRiskMaxBytes) + `-tail"`
	evt := ChatEvent{Type: "tool_approval", Tool: "set_fallback", Text: `{"rules":` + long + `}`,
		ApprovalCallback: "appr:1", ApprovalRisk: fallbackRisk(long)}

	for path, out := range promptPaths(t, evt) {
		if !strings.Contains(out.Text, riskTooLongLine) {
			t.Errorf("%s: no too-long line: %q", path, out.Text)
		}
		// The flagged line's own markup, not "rules:": the warning says "rules:" too.
		if strings.Contains(out.Text, "<b>rules:</b>") || strings.Contains(out.Text, "```\nrules: ") {
			t.Errorf("%s: shows a flagged line it could not fit; a cut value must never appear", path)
		}
		if strings.Contains(out.Text, "Approve once") {
			t.Errorf("%s: offers approval it has no button for: %q", path, out.Text)
		}
		if got := callbacks(out); len(got) != 1 || got[0] != "appr:1:deny" {
			t.Errorf("%s: buttons = %v, want deny only", path, got)
		}
		if len(out.Text) > telegramMessageMaxBytes {
			t.Errorf("%s: %d bytes, over Telegram's cap", path, len(out.Text))
		}
	}
}

// Over a Deny-only keyboard the HTML header must not ask "approve?". The
// debug header is neutral, so it stays.
func TestApprovalPrompt_TooLongHeaderPointsAtTheDashboard(t *testing.T) {
	long := `"` + strings.Repeat("v", approvalRiskMaxBytes) + `"`
	evt := ChatEvent{Type: "tool_approval", Tool: "set_fallback", Text: `{"rules":` + long + `}`,
		ApprovalCallback: "appr:1", ApprovalRisk: fallbackRisk(long)}

	out := promptPaths(t, evt)
	for _, path := range []string{"standalone", "activity log"} {
		if !strings.HasPrefix(out[path].Text, "🔧 <b>set_fallback</b> — review in the dashboard\n") {
			t.Errorf("%s header = %q, want the dashboard pointer", path, strings.SplitN(out[path].Text, "\n", 2)[0])
		}
		if strings.Contains(out[path].Text, "approve?") {
			t.Errorf("%s still asks approve? over a Deny-only keyboard", path)
		}
	}
	if !strings.HasPrefix(out["debug"].Text, "Agent wants to execute tool **set_fallback**") {
		t.Errorf("debug header changed: %q", out["debug"].Text)
	}
}

// A ``` in a value would close the debug path's code fence, and Discord would
// render what follows as markdown: a fake "**Looks safe, approve**" or a
// masked link. Every backtick run inside both fenced blocks is broken.
func TestApprovalPrompt_DebugFencesSurviveBacktickRuns(t *testing.T) {
	evil := "```\n**Looks safe, approve** [docs](https://evil.example)\n```"
	value := `"` + evil + `"`
	evt := ChatEvent{Type: "tool_approval", Tool: "set_fallback", Text: `{"rules":` + value + `}`,
		ApprovalCallback: "appr:1", ApprovalRisk: fallbackRisk(value)}

	text := promptPaths(t, evt)["debug"].Text
	// Two blocks, the flagged lines and the arguments: four fence lines, no more.
	if n := strings.Count(text, "```"); n != 4 {
		t.Fatalf("debug text has %d ``` runs, want the 4 fence markers only:\n%s", n, text)
	}
	if strings.Contains(text, "``") && strings.Count(text, "``") != 4 {
		t.Errorf("a double backtick survived inside a fence:\n%s", text)
	}
	if !strings.Contains(text, "Looks safe, approve") {
		t.Errorf("the value's text is gone, not just made inert:\n%s", text)
	}
}

// Backticks each grow by a zero-width space in the debug text, so the risk
// block's size check must count that form too, or a value of backticks would
// overflow the message.
func TestApprovalPrompt_BacktickHeavyValueStaysUnderTheCap(t *testing.T) {
	value := `"` + strings.Repeat("`", approvalRiskMaxBytes-200) + `"`
	evt := ChatEvent{Type: "tool_approval", Tool: "set_fallback", Text: `{"rules":` + value + strings.Repeat("`", 3000) + `}`,
		ApprovalCallback: "appr:1", ApprovalRisk: fallbackRisk(value)}

	for path, out := range promptPaths(t, evt) {
		if len(out.Text) > telegramMessageMaxBytes {
			t.Errorf("%s: %d bytes, over Telegram's cap", path, len(out.Text))
		}
	}
}

// A flagged value just inside the budget, made of characters HTML escaping
// expands, still keeps the section within approvalSectionMaxBytes.
func TestApprovalPrompt_HighRiskSectionAtTheLimitStaysWithinItsCap(t *testing.T) {
	risk := fallbackRisk("")
	for n := 1; ; n++ {
		candidate := fallbackRisk(strings.Repeat(`<&">`, n))
		if newRiskBlock(candidate).tooLong {
			break
		}
		risk = candidate
	}
	if newRiskBlock(risk).tooLong || len(risk.Flagged[0].Value) == 0 {
		t.Fatalf("could not build a flagged value just inside the budget")
	}
	evt := ChatEvent{Type: "tool_approval", Tool: strings.Repeat("&", 200), Text: strings.Repeat(`<&">`, 2000),
		ApprovalCallback: "appr:1", ApprovalRisk: risk, ApprovalHumanOnly: true}

	for path, out := range promptPaths(t, evt) {
		if path == "debug" {
			continue // markdown, not the HTML section; its bound is the message cap below
		}
		if len(out.Text) > approvalSectionMaxBytes {
			t.Errorf("%s: section = %d bytes, want <= %d", path, len(out.Text), approvalSectionMaxBytes)
		}
		if !strings.Contains(out.Text, "<b>rules:</b>") {
			t.Errorf("%s: flagged line missing at the limit", path)
		}
	}
	if debug := promptPaths(t, evt)["debug"].Text; len(debug) > telegramMessageMaxBytes {
		t.Errorf("debug: %d bytes, over Telegram's cap", len(debug))
	}
}

// An unsent chunk whose lines leave no room for an approval section: the
// high-risk prompt moves to a new chunk and keeps its keyboard.
func TestActivityLog_HighRiskApprovalOnAFullChunkStartsANewOneWithItsKeyboard(t *testing.T) {
	me := &mockMessageEditor{msgID: ""} // never sent, so only the size check can roll the chunk
	l := newTestActivityLog(me)
	ctx := context.Background()
	l.ensureChunk()
	for i := 0; len(l.renderLines(l.chunks[len(l.chunks)-1])) <= activityChunkWithApprovalMaxBytes; i++ {
		l.autoApproved(ctx, "tool_with_a_fairly_long_name_"+strconv.Itoa(i))
		if i > 10000 {
			t.Fatal("chunk never filled; budget wiring changed?")
		}
	}
	full := len(l.chunks)

	l.setPendingApproval(ctx, "set_fallback", `{"rules":[]}`, "appr:9", false, fallbackRisk("[]"))

	if len(l.chunks) != full+1 {
		t.Fatalf("chunks = %d, want a new one after %d", len(l.chunks), full)
	}
	last := me.sends[len(me.sends)-1]
	if got := callbacks(last); len(got) != 2 || got[1] != "appr:9:deny" {
		t.Errorf("new chunk buttons = %v, want approve and deny", got)
	}
	if len(last.Text) > telegramMessageMaxBytes || !strings.Contains(last.Text, "High risk.") {
		t.Errorf("new chunk = %d bytes, high-risk text present %v", len(last.Text), strings.Contains(last.Text, "High risk."))
	}
}
