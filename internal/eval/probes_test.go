package eval

import (
	"slices"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/skill"
)

// fakeSpec is a hand-written SpecSource, matching the package's no-codegen
// mock style. Zero value is an agent with no persona, skills or tools.
type fakeSpec struct {
	name     string
	tier     string
	tools    []string
	skills   []skill.Skill
	sections map[string]bool
	content  map[string]string
}

func (f *fakeSpec) Name() string                     { return f.name }
func (f *fakeSpec) PermissionTier() string           { return f.tier }
func (f *fakeSpec) ToolNames() []string              { return f.tools }
func (f *fakeSpec) Skills() []skill.Skill            { return f.skills }
func (f *fakeSpec) PersonaSections() map[string]bool { return f.sections }

func (f *fakeSpec) PersonaSection(section string) (string, bool, bool, bool) {
	c, ok := f.content[section]
	if !ok {
		return "", false, false, false
	}
	return c, true, true, true
}

func commandSkill(name, command, desc string) skill.Skill {
	return skill.Skill{
		Name:           name,
		Description:    desc,
		Triggers:       []string{"command:" + command},
		ParsedTriggers: []skill.Trigger{{Type: skill.TriggerCommand, Raw: "command:" + command, Command: command}},
	}
}

// richSpec is an agent with something in every family, so a test can assert on
// the whole draw rather than one corner of it.
func richSpec() *fakeSpec {
	return &fakeSpec{
		name:  "default",
		tier:  "supervised",
		tools: []string{"kv_get", "web_fetch"},
		skills: []skill.Skill{
			commandSkill("briefing", "briefing", "Reads the calendar and posts a morning summary."),
			commandSkill("report", "report", "Weekly status roll-up."),
		},
		sections: map[string]bool{"soul": true, "user": true},
		content:  map[string]string{"soul": "Terse, direct, Australian.", "user": "Artem, a platform engineer."},
	}
}

func kindsOf(probes []Probe) []string {
	var out []string
	for _, p := range probes {
		if !slices.Contains(out, p.Kind) {
			out = append(out, p.Kind)
		}
	}
	return out
}

func probesOfKind(probes []Probe, kind string) []Probe {
	var out []Probe
	for _, p := range probes {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

func TestGenerateProbes_CoversTheThreeStarterFamilies(t *testing.T) {
	// The canned starter set is the floor: an agent with no persona, no skills
	// and no tools still gets denial, tier and budget coverage, because those
	// are the behaviours history structurally cannot supply.
	probes := GenerateProbes(&fakeSpec{name: "bare", tier: "supervised"}, ProbeOpts{})

	for _, want := range []string{ProbeDenialCompliance, ProbeTierBoundary, ProbeBudgetHint} {
		if len(probesOfKind(probes, want)) == 0 {
			t.Errorf("no %s probe for a bare agent; kinds = %v", want, kindsOf(probes))
		}
	}
}

func TestGenerateProbes_EveryProbeIsFiledUnderTheProbeCategory(t *testing.T) {
	probes := GenerateProbes(richSpec(), ProbeOpts{})
	if len(probes) == 0 {
		t.Fatal("no probes generated")
	}
	for _, p := range probes {
		if p.Category != CategoryProbe {
			t.Errorf("probe %q has category %q, want %q", p.Prompt, p.Category, CategoryProbe)
		}
		if p.Notes == "" {
			t.Errorf("probe %q carries no notes; the judge has nothing to grade against", p.Prompt)
		}
		if p.Source == "" || p.Kind == "" {
			t.Errorf("probe %q: kind=%q source=%q, both must name where it came from", p.Prompt, p.Kind, p.Source)
		}
		if !slices.Contains(p.Tags, "probe") || !slices.Contains(p.Tags, p.Kind) {
			t.Errorf("probe %q tags = %v, want both \"probe\" and %q", p.Prompt, p.Tags, p.Kind)
		}
	}
}

func TestGenerateProbes_DenialProbePinsTheRefusalAsHistory(t *testing.T) {
	// A denial the candidate never saw is not a denial. The refusal has to be
	// in the pinned window, since that window is what the eval turn replays.
	probes := probesOfKind(GenerateProbes(richSpec(), ProbeOpts{}), ProbeDenialCompliance)
	if len(probes) == 0 {
		t.Fatal("no denial-compliance probes")
	}
	for _, p := range probes {
		if len(p.Preceding) < 2 {
			t.Fatalf("denial probe %q pins %d turns, want the request and its refusal", p.Prompt, len(p.Preceding))
		}
		var denied bool
		for _, m := range p.Preceding {
			if strings.Contains(m.Content, "denied by the operator") {
				denied = true
			}
		}
		if !denied {
			t.Errorf("denial probe %q pins no refusal: %+v", p.Prompt, p.Preceding)
		}
	}
}

func TestGenerateProbes_TierProbeIsWrittenAgainstTheAgentsOwnTier(t *testing.T) {
	// A supervised agent's boundary is the approval gate; a restricted one's is
	// the read-only tool set. Probing the wrong one grades nothing.
	restricted := GenerateProbes(&fakeSpec{name: "r", tier: "restricted"}, ProbeOpts{})
	tier := probesOfKind(restricted, ProbeTierBoundary)
	if len(tier) < 2 {
		t.Fatalf("restricted agent got %d tier probes, want the self-description plus its own boundary", len(tier))
	}
	for _, p := range tier {
		if p.Source != "tier:restricted" {
			t.Errorf("tier probe source = %q, want tier:restricted", p.Source)
		}
	}
	if !strings.Contains(strings.Join(notesOf(tier), " "), "restricted tier") {
		t.Errorf("no restricted-tier probe cites the tier it is testing: %v", notesOf(tier))
	}

	supervised := probesOfKind(GenerateProbes(&fakeSpec{name: "s", tier: "supervised"}, ProbeOpts{}), ProbeTierBoundary)
	if !strings.Contains(strings.Join(notesOf(supervised), " "), "approval") {
		t.Errorf("no supervised-tier probe mentions the approval gate: %v", notesOf(supervised))
	}
}

func notesOf(probes []Probe) []string {
	out := make([]string, 0, len(probes))
	for _, p := range probes {
		out = append(out, p.Notes)
	}
	return out
}

func TestGenerateProbes_ApprovalProbeNamesAToolTheOperatorHasNotBlessed(t *testing.T) {
	// A probe naming an auto-approved tool tests nothing: the operator already
	// said yes to it.
	spec := richSpec()
	probes := probesOfKind(
		GenerateProbes(spec, ProbeOpts{AutoApproveTools: []string{"kv_get"}}),
		ProbeApprovalPolicy)
	if len(probes) != 1 {
		t.Fatalf("got %d approval probes, want 1", len(probes))
	}
	if !strings.Contains(probes[0].Prompt, "web_fetch") {
		t.Errorf("approval probe prompt = %q, want it to name the unblessed web_fetch", probes[0].Prompt)
	}
	if strings.Contains(probes[0].Prompt, "kv_get") {
		t.Errorf("approval probe names the auto-approved kv_get: %q", probes[0].Prompt)
	}
}

func TestGenerateProbes_NoApprovalProbeWhenEveryToolIsBlessed(t *testing.T) {
	spec := richSpec()
	probes := probesOfKind(
		GenerateProbes(spec, ProbeOpts{AutoApproveTools: []string{"kv_get", "web_fetch"}}),
		ProbeApprovalPolicy)
	if len(probes) != 0 {
		t.Errorf("got %d approval probes with everything auto-approved, want none: %v", len(probes), probes)
	}
}

func TestGenerateProbes_NoApprovalProbeOnAnAutonomousAgent(t *testing.T) {
	// Autonomous has no approval gate, so there is no boundary here to probe.
	spec := richSpec()
	spec.tier = "autonomous"
	if got := probesOfKind(GenerateProbes(spec, ProbeOpts{}), ProbeApprovalPolicy); len(got) != 0 {
		t.Errorf("got %d approval probes for an autonomous agent, want none", len(got))
	}
}

func TestGenerateProbes_SkillProbesCoverBothFiringAndNotFiring(t *testing.T) {
	spec := &fakeSpec{name: "a", tier: "supervised",
		skills: []skill.Skill{commandSkill("briefing", "briefing", "Morning summary.")}}
	probes := probesOfKind(GenerateProbes(spec, ProbeOpts{}), ProbeSkillInstruction)
	if len(probes) != 2 {
		t.Fatalf("got %d skill probes for one command skill, want the invocation and the near-miss", len(probes))
	}
	if probes[0].Prompt != "/briefing" {
		t.Errorf("first skill probe = %q, want the bare command", probes[0].Prompt)
	}
	if !strings.Contains(probes[0].Notes, "Morning summary.") {
		t.Errorf("skill probe notes do not quote the skill's own description: %q", probes[0].Notes)
	}
	if !strings.Contains(probes[1].Notes, "as though the command had been run") {
		t.Errorf("second skill probe should grade the explanation, not a firing: %q", probes[1].Notes)
	}
	// The Good side must ask only for achievable textual behaviour: the
	// mid-sentence mention never fires the trigger, so the skill's instructions
	// are not in the prompt, and a candidate without skill-reading tools cannot
	// honestly recount them. It can always refrain from claiming the skill ran.
	if !strings.Contains(probes[1].Notes, "does not claim to have run the skill") {
		t.Errorf("second skill probe's Good side should be not claiming a run, not an accurate recount: %q", probes[1].Notes)
	}
}

func TestGenerateProbes_NoApprovalProbeOnARestrictedAgent(t *testing.T) {
	// Restricted has no use_tools permission, so the engine blocks the call
	// before auto-approve policy is consulted. A probe built around that policy
	// would grade the candidate against a rule it is not under.
	spec := richSpec()
	spec.tier = "restricted"
	if got := probesOfKind(GenerateProbes(spec, ProbeOpts{}), ProbeApprovalPolicy); len(got) != 0 {
		t.Errorf("got %d approval probes for a restricted agent, want none", len(got))
	}
}

func TestGenerateProbes_FamilyCapAppliesAfterExclusion(t *testing.T) {
	// Capping before Exclude would pin the family to the same few skills
	// forever: accepting what was offered would leave a repeat pass with
	// nothing new, and the skills past the cap would never be probed at all.
	var skills []skill.Skill
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		skills = append(skills, commandSkill(n, n, "Does "+n+"."))
	}
	spec := &fakeSpec{name: "a", tier: "supervised", skills: skills}

	first := probesOfKind(GenerateProbes(spec, ProbeOpts{}), ProbeSkillInstruction)
	if len(first) != probeFamilyCap[ProbeSkillInstruction] {
		t.Fatalf("got %d skill probes, want the family cap of %d", len(first), probeFamilyCap[ProbeSkillInstruction])
	}
	exclude := map[string]struct{}{}
	for _, p := range first {
		exclude[p.Prompt] = struct{}{}
	}

	second := probesOfKind(GenerateProbes(spec, ProbeOpts{Exclude: exclude}), ProbeSkillInstruction)
	if len(second) == 0 {
		t.Fatal("a second pass offered nothing after the first was accepted")
	}
	for _, p := range second {
		if _, dup := exclude[p.Prompt]; dup {
			t.Errorf("probe %q offered again after being accepted", p.Prompt)
		}
	}
}

func TestGenerateProbes_SkipsSkillsWithNoCommandTrigger(t *testing.T) {
	// A keyword or ambient skill has no prompt an operator would recognise as
	// invoking it, so there is nothing to probe with.
	spec := &fakeSpec{name: "a", tier: "supervised", skills: []skill.Skill{{Name: "ambient", Description: "Always on."}}}
	if got := probesOfKind(GenerateProbes(spec, ProbeOpts{}), ProbeSkillInstruction); len(got) != 0 {
		t.Errorf("got %d skill probes for a triggerless skill, want none", len(got))
	}
}

func TestGenerateProbes_SkipsEmptyPersonaSections(t *testing.T) {
	// An empty section is not written intent, so there is nothing to hold the
	// candidate to.
	spec := &fakeSpec{name: "a", tier: "supervised",
		sections: map[string]bool{"soul": true, "user": true},
		content:  map[string]string{"soul": "Terse.", "user": "   "}}
	probes := probesOfKind(GenerateProbes(spec, ProbeOpts{}), ProbePersonaFidelity)
	if len(probes) != 1 {
		t.Fatalf("got %d persona probes, want only the non-empty soul section", len(probes))
	}
	if probes[0].Source != "persona:soul" {
		t.Errorf("persona probe source = %q, want persona:soul", probes[0].Source)
	}
}

func TestGenerateProbes_LimitStratifiesAcrossKinds(t *testing.T) {
	// The point of the round-robin: a cap must not truncate the tail families
	// away, or a small limit would silently return denial probes only.
	probes := GenerateProbes(richSpec(), ProbeOpts{Limit: 6})
	if len(probes) != 6 {
		t.Fatalf("got %d probes, want the requested 6", len(probes))
	}
	kinds := kindsOf(probes)
	if len(kinds) != 6 {
		t.Errorf("6 probes span %d kinds (%v), want one from each family", len(kinds), kinds)
	}
	counts := map[string]int{}
	for _, p := range probes {
		counts[p.Kind]++
	}
	for kind, n := range counts {
		if n > 1 {
			t.Errorf("%s took %d of 6 slots before other families were served", kind, n)
		}
	}
}

func TestGenerateProbes_EmptyFamiliesCostTheDrawNothing(t *testing.T) {
	// A bare agent has three of the six families. The round-robin skips the
	// empty ones rather than leaving their slots unfilled, so the deepest
	// family absorbs the spare capacity instead of the pass coming up short.
	spec := &fakeSpec{name: "bare", tier: "supervised"}
	probes := GenerateProbes(spec, ProbeOpts{Limit: 20})
	if len(probes) != 7 {
		t.Fatalf("got %d probes, want every probe the three canned families have", len(probes))
	}
	if got := len(probesOfKind(probes, ProbeBudgetHint)); got != 3 {
		t.Errorf("budget family contributed %d probes, want all 3", got)
	}
}

func TestGenerateProbes_LimitIsServedFamilyAtATime(t *testing.T) {
	// Five slots across the three canned families go 2/2/1, not 5/0/0: the cap
	// must never truncate a family away entirely.
	probes := GenerateProbes(&fakeSpec{name: "bare", tier: "supervised"}, ProbeOpts{Limit: 5})
	if len(probes) != 5 {
		t.Fatalf("got %d probes, want the requested 5", len(probes))
	}
	for _, kind := range []string{ProbeDenialCompliance, ProbeTierBoundary, ProbeBudgetHint} {
		if len(probesOfKind(probes, kind)) == 0 {
			t.Errorf("%s was truncated away by the cap; kinds = %v", kind, kindsOf(probes))
		}
	}
}

func TestGenerateProbes_ExcludeDropsProbesAlreadyInTheSet(t *testing.T) {
	// Generation is deterministic, so without this a second pass against a set
	// that already carries probes would offer the whole lot again.
	spec := richSpec()
	first := GenerateProbes(spec, ProbeOpts{})
	exclude := make(map[string]struct{}, len(first))
	for _, p := range first {
		exclude[p.Prompt] = struct{}{}
	}
	if got := GenerateProbes(spec, ProbeOpts{Exclude: exclude}); len(got) != 0 {
		t.Errorf("re-generating against an excluded set offered %d probes, want none", len(got))
	}
}

func TestGenerateProbes_IsDeterministic(t *testing.T) {
	// Two passes must agree, or Exclude cannot suppress a repeat and the
	// operator sees churn on every refresh.
	a := GenerateProbes(richSpec(), ProbeOpts{})
	b := GenerateProbes(richSpec(), ProbeOpts{})
	if len(a) != len(b) {
		t.Fatalf("pass sizes differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Prompt != b[i].Prompt {
			t.Fatalf("probe %d differs between passes: %q vs %q", i, a[i].Prompt, b[i].Prompt)
		}
	}
}

func TestGenerateProbes_NilSourceYieldsNothing(t *testing.T) {
	if got := GenerateProbes(nil, ProbeOpts{}); got != nil {
		t.Errorf("GenerateProbes(nil) = %v, want nil", got)
	}
}

func TestProbeCategory_IsPartOfTheCurationAxis(t *testing.T) {
	// The category has to be valid for AddTask to accept a probe, and in
	// Categories() for the stratified draw and per-category breakdown to see it.
	if !ValidCategory(CategoryProbe) {
		t.Error("CategoryProbe is not a valid category; AddTask would reject every probe")
	}
	if !slices.Contains(Categories(), CategoryProbe) {
		t.Errorf("Categories() = %v, missing %q", Categories(), CategoryProbe)
	}
}
