package skill

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// kelosFrontmatter is the frontmatter of skills/kelos/SKILL.md in
// github.com/kelos-dev/kelos at commit b48320b, used under the Apache-2.0
// licence.
const kelosFrontmatter = `name: kelos
description: >-
  Install and configure Kelos; author, document, inspect, troubleshoot, and
  operate its Kubernetes custom resources and CRD schemas in the kelos.dev API
  group using manifests, kubectl, or the kelos CLI. Use when a request names
  Kelos or a distinctive Kelos kind such as AgentConfig or TaskSpawner and
  concerns an installation, CLI command, API or CRD schema, manifest or example,
  resource-field documentation, or live custom resource. Do not use for ordinary
  code, test, build, review, CI, or Git work, even in the Kelos repository or on
  a service hosted under kelos.dev.`

func mustParse(t *testing.T, content string) *Skill {
	t.Helper()
	s, err := ParseFile("test.md", []byte(content))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	return s
}

func parseErr(t *testing.T, content, want string) {
	t.Helper()
	_, err := ParseFile("test.md", []byte(content))
	if err == nil {
		t.Fatalf("ParseFile accepted:\n%s", content)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// --- Parsing ---

func TestParseFile_MinimalYAML(t *testing.T) {
	s := mustParse(t, "---\nname: demo\n---\n\nDo the thing.")
	if s.Name != "demo" || s.Body != "Do the thing." {
		t.Errorf("skill = %+v", s)
	}
	if s.Dialect != DialectYAML || s.Frontmatter != "name: demo" {
		t.Errorf("dialect %q frontmatter %q, want yaml and the block", s.Dialect, s.Frontmatter)
	}
}

func TestParseFile_KelosFrontmatter(t *testing.T) {
	s := mustParse(t, "---\n"+kelosFrontmatter+"\n---\n\n# Kelos Skill\n")
	if s.Name != "kelos" {
		t.Fatalf("Name = %q, want kelos", s.Name)
	}
	if strings.Contains(s.Description, "\n") || !strings.HasPrefix(s.Description, "Install and configure Kelos;") ||
		!strings.HasSuffix(s.Description, "a service hosted under kelos.dev.") {
		t.Errorf("Description = %q, want the folded text on one line", s.Description)
	}
}

func TestParseFile_YAMLMatchesTOMLTwin(t *testing.T) {
	y := mustParse(t, `---
name: inbox
triggers: ["command:inbox", "schedule:morning"]
max_tool_rounds: 5
max_output_tokens: 800
requires:
  tools: [gmail_list]
---
body`)
	tm := mustParse(t, `+++
name = "inbox"
triggers = ["command:inbox", "schedule:morning"]
max_tool_rounds = 5
max_output_tokens = 800
[requires]
tools = ["gmail_list"]
+++
body`)
	if !reflect.DeepEqual(y.Fields(), tm.Fields()) || !reflect.DeepEqual(y.ParsedTriggers, tm.ParsedTriggers) {
		t.Errorf("yaml %+v\ntoml %+v", y.Fields(), tm.Fields())
	}
	if tm.Dialect != DialectTOML || !strings.HasPrefix(tm.Frontmatter, `name = "inbox"`) ||
		!strings.HasSuffix(tm.Frontmatter, `tools = ["gmail_list"]`) {
		t.Errorf("toml dialect %q frontmatter %q", tm.Dialect, tm.Frontmatter)
	}
}

func TestParseFile_YAMLAmbiguousScalarsAreText(t *testing.T) {
	s := mustParse(t, "---\nname: yes\nversion: 1.10\ndescription: 2026-01-01\n---\nb")
	if s.Name != "yes" || s.Version != "1.10" || s.Description != "2026-01-01" {
		t.Errorf("name %q version %q description %q", s.Name, s.Version, s.Description)
	}
	s = mustParse(t, "---\nname: n\nversion: 2026-01-01\n---\nb")
	if s.Version != "2026-01-01" {
		t.Errorf("version = %q", s.Version)
	}
}

func TestParseFile_YAMLRuleInBodyStaysInBody(t *testing.T) {
	s := mustParse(t, "---\nname: n\n---\nabove\n---\nbelow")
	if s.Body != "above\n---\nbelow" {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestParseFile_YAMLDelimiterErrors(t *testing.T) {
	parseErr(t, "---\nname: n\nbody", "missing closing --- delimiter")
	parseErr(t, "name: n\nbody", "missing opening frontmatter delimiter (+++ or ---)")
	parseErr(t, "---\n- a\n- b\n---\nbody", "frontmatter must be a YAML mapping")
}

func TestParseFile_YAMLDuplicateKeys(t *testing.T) {
	parseErr(t, "---\nname: a\nname: b\n---\nbody", `duplicate frontmatter key "name"`)
	parseErr(t, "---\nname: a\nmetadata:\n  x: 1\n  x: 2\n---\nbody", `duplicate frontmatter key "x"`)
}

func TestParseFile_YAMLAnchorsAndAliasesRejected(t *testing.T) {
	const msg = "YAML anchors and aliases are not supported in frontmatter"
	parseErr(t, "---\nname: n\nbase: &b {x: 1}\n---\nbody", msg)
	parseErr(t, "---\nname: n\nother: *b\n---\nbody", "")
	parseErr(t, "---\nname: &n demo\nalias: *n\n---\nbody", msg)

	bomb := "---\nname: n\na: &a [\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\"]\n" +
		"b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]\nc: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]\n" +
		"d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]\ne: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]\n" +
		"f: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e]\ng: &g [*f,*f,*f,*f,*f,*f,*f,*f,*f]\n---\nbody"
	start := time.Now()
	parseErr(t, bomb, msg)
	if d := time.Since(start); d > time.Second {
		t.Errorf("alias bomb took %v to reject", d)
	}
}

func TestParseFile_YAMLMergeKeyRejected(t *testing.T) {
	parseErr(t, "---\nname: good\n<<: {name: evil}\n---\nbody", "merge keys are not supported")
}

func TestParseFile_YAMLRoundCapsMustBeIntegers(t *testing.T) {
	parseErr(t, "---\nname: n\nmax_tool_rounds: 5.5\n---\nb", `frontmatter field "max_tool_rounds" must be an integer`)
	parseErr(t, "---\nname: n\nmax_tool_rounds: \"5\"\n---\nb", `frontmatter field "max_tool_rounds" must be an integer`)
	parseErr(t, "---\nname: n\nmax_output_tokens: 1.5\n---\nb", `frontmatter field "max_output_tokens" must be an integer`)
	parseErr(t, "---\nname: n\nmax_tool_rounds: -1\n---\nb", "max_tool_rounds must be >= 0")
	if s := mustParse(t, "---\nname: n\nmax_tool_rounds: ~\n---\nb"); s.MaxToolRounds != 0 {
		t.Errorf("null max_tool_rounds = %d, want 0", s.MaxToolRounds)
	}
}

func TestParseFile_YAMLInvalidTrigger(t *testing.T) {
	parseErr(t, "---\nname: n\ntriggers: [\"bogus:thing\"]\n---\nb", "trigger")
}

func TestParseFile_YAMLMissingName(t *testing.T) {
	parseErr(t, "---\ndescription: d\n---\nb", "missing required field: name")
	parseErr(t, "---\n---\nb", "missing required field: name")
}

func TestLoadDir_YAMLSubdirSkill(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "kelos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kelos", "SKILL.md"), []byte("---\n"+kelosFrontmatter+"\n---\n\n# Kelos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := LoadDir(dir, discardLogger())
	if err != nil || len(skills) != 1 {
		t.Fatalf("LoadDir = %d skills, %v", len(skills), err)
	}
	if skills[0].Name != "kelos" || skills[0].Dir == "" || skills[0].Dialect != DialectYAML {
		t.Errorf("skill = %+v", skills[0])
	}
}

// --- Rendering ---

func TestRenderNew_ExactTOML(t *testing.T) {
	got := RenderNew(Fields{
		Name: "inbox", Description: "Triage mail", Version: "1.2.0",
		Triggers: []string{"command:inbox"}, MaxToolRounds: 5, MaxOutputTokens: 800,
		RequiresTools: []string{"gmail_list"},
	}, "  Body text.\n")
	want := "+++\n" +
		"name = 'inbox'\n" +
		"description = 'Triage mail'\n" +
		"version = '1.2.0'\n" +
		"triggers = ['command:inbox']\n" +
		"max_tool_rounds = 5\n" +
		"max_output_tokens = 800\n" +
		"\n" +
		"[requires]\n" +
		"tools = ['gmail_list']\n" +
		"+++\n\nBody text."
	if got != want {
		t.Errorf("RenderNew =\n%s\nwant\n%s", got, want)
	}
}

func renderOK(t *testing.T, prior *Skill, f Fields, body string) string {
	t.Helper()
	out, err := Render(*prior, f, body)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

func TestRender_TOMLBodyOnlyKeepsBlockBytes(t *testing.T) {
	block := "# hand-written\nname = \"demo\"   # trailing\nlicense = \"MIT\""
	prior := mustParse(t, "+++\n"+block+"\n+++\n\nold body")
	out := renderOK(t, prior, prior.Fields(), "new body")
	if out != "+++\n"+block+"\n+++\n\nnew body" {
		t.Errorf("Render =\n%s", out)
	}
}

func TestRender_TOMLFieldChangeKeepsUnknownValues(t *testing.T) {
	prior := mustParse(t, `+++
name = "demo"
description = "old"
license = "MIT"
released = 2026-01-01
[metadata]
author = "kim"
[requires]
tools = ["gmail_list"]
env = ["API_KEY"]
+++
body`)
	f := prior.Fields()
	f.Description = "new"
	out := renderOK(t, prior, f, "body")
	re := mustParse(t, out)
	if re.Description != "new" || !reflect.DeepEqual(re.Requires.Tools, []string{"gmail_list"}) {
		t.Errorf("reparsed = %+v", re.Fields())
	}

	var before, after map[string]any
	if err := toml.Unmarshal([]byte(prior.Frontmatter), &before); err != nil {
		t.Fatal(err)
	}
	if err := toml.Unmarshal([]byte(re.Frontmatter), &after); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"license", "released", "metadata"} {
		if !reflect.DeepEqual(before[k], after[k]) {
			t.Errorf("%s = %#v, want %#v", k, after[k], before[k])
		}
	}
	beforeReq, okBefore := before["requires"].(map[string]any)
	afterReq, okAfter := after["requires"].(map[string]any)
	if !okBefore || !okAfter || !reflect.DeepEqual(afterReq["env"], beforeReq["env"]) {
		t.Errorf("requires = %#v, want env kept from %#v", after["requires"], before["requires"])
	}
	// Known scalars come before any table header.
	firstTable := strings.Index(re.Frontmatter, "\n[")
	if firstTable < 0 || strings.Index(re.Frontmatter, "description") > firstTable {
		t.Errorf("known keys are not before the tables:\n%s", re.Frontmatter)
	}
}

func TestRender_TOMLNoUnknownKeysEqualsRenderNew(t *testing.T) {
	prior := mustParse(t, "+++\nname = \"demo\"\nversion = \"1\"\n+++\nbody")
	f := prior.Fields()
	f.Version = "2"
	if got, want := renderOK(t, prior, f, "body"), RenderNew(f, "body"); got != want {
		t.Errorf("Render =\n%s\nwant\n%s", got, want)
	}
}

const yamlWithUnknowns = `# head comment
name: demo
description: >-
  A folded description that runs
  over two lines.
version: "1.0"
license: Apache-2.0
metadata:
  author: kim
  tags: [a, b]`

func TestRender_YAMLBodyOnlyKeepsBlockBytes(t *testing.T) {
	prior := mustParse(t, "---\n"+yamlWithUnknowns+"\n---\n\nold body")
	out := renderOK(t, prior, prior.Fields(), "new body")
	if out != "---\n"+yamlWithUnknowns+"\n---\n\nnew body" {
		t.Errorf("Render =\n%s", out)
	}
}

func yamlTop(t *testing.T, block string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(block), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func yamlKeys(n *yaml.Node) []string {
	var keys []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		keys = append(keys, n.Content[i].Value)
	}
	return keys
}

func yamlValue(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func TestRender_YAMLVersionChangeKeepsTheRest(t *testing.T) {
	prior := mustParse(t, "---\n"+yamlWithUnknowns+"\n---\nbody")
	f := prior.Fields()
	f.Version = "1.1"
	out := renderOK(t, prior, f, "body")
	if !strings.HasPrefix(out, "---\n# head comment\n") {
		t.Errorf("delimiter or head comment lost:\n%s", out)
	}
	re := mustParse(t, out)
	if re.Version != "1.1" || re.Description != prior.Description {
		t.Errorf("reparsed = %+v", re.Fields())
	}
	top := yamlTop(t, re.Frontmatter)
	if !reflect.DeepEqual(yamlKeys(top), []string{"name", "description", "version", "license", "metadata"}) {
		t.Errorf("key order = %v", yamlKeys(top))
	}
	if d := yamlValue(top, "description"); d.Style != yaml.FoldedStyle {
		t.Errorf("description style = %v, want folded", d.Style)
	}
	if yamlValue(top, "license").Value != "Apache-2.0" || yamlValue(yamlValue(top, "metadata"), "author").Value != "kim" {
		t.Errorf("unknown values changed:\n%s", re.Frontmatter)
	}
}

func TestRender_YAMLTriggersAddedToKelosGoLast(t *testing.T) {
	prior := mustParse(t, "---\n"+kelosFrontmatter+"\n---\nbody")
	f := prior.Fields()
	f.Triggers = []string{"command:kelos"}
	re := mustParse(t, renderOK(t, prior, f, "body"))
	keys := yamlKeys(yamlTop(t, re.Frontmatter))
	if keys[len(keys)-1] != "triggers" || !reflect.DeepEqual(re.Triggers, []string{"command:kelos"}) {
		t.Errorf("keys %v triggers %v", keys, re.Triggers)
	}
}

func TestRender_YAMLClearingRemovesKeys(t *testing.T) {
	prior := mustParse(t, `---
name: demo
triggers: ["command:demo"]
requires:
  tools: [gmail_list]
  env: [API_KEY]
---
body`)
	f := prior.Fields()
	f.Triggers, f.RequiresTools = nil, nil
	re := mustParse(t, renderOK(t, prior, f, "body"))
	top := yamlTop(t, re.Frontmatter)
	if yamlValue(top, "triggers") != nil {
		t.Errorf("triggers still present:\n%s", re.Frontmatter)
	}
	req := yamlValue(top, "requires")
	if req == nil || yamlValue(req, "tools") != nil || yamlValue(req, "env") == nil {
		t.Errorf("requires = %v, want only env left:\n%s", req, re.Frontmatter)
	}

	only := mustParse(t, "---\nname: demo\nrequires:\n  tools: [a]\n---\nbody")
	g := only.Fields()
	g.RequiresTools = nil
	if re := mustParse(t, renderOK(t, only, g, "body")); yamlValue(yamlTop(t, re.Frontmatter), "requires") != nil {
		t.Errorf("empty requires kept:\n%s", re.Frontmatter)
	}
}

func TestRender_YAMLAmbiguousValuesAreQuoted(t *testing.T) {
	prior := mustParse(t, "---\nname: demo\n---\nbody")
	f := prior.Fields()
	f.Description, f.Version = "yes", "1.10"
	re := mustParse(t, renderOK(t, prior, f, "body"))
	if re.Description != "yes" || re.Version != "1.10" {
		t.Errorf("reparsed description %q version %q", re.Description, re.Version)
	}
	top := yamlTop(t, re.Frontmatter)
	for _, k := range []string{"description", "version"} {
		if v := yamlValue(top, k); v.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) == 0 {
			t.Errorf("%s is not quoted:\n%s", k, re.Frontmatter)
		}
	}
}

func TestRender_YAMLMultilineDescriptionRoundTrips(t *testing.T) {
	prior := mustParse(t, "---\nname: demo\n---\nbody")
	f := prior.Fields()
	f.Description = "line one\nline two: with colon\n- and a dash"
	if re := mustParse(t, renderOK(t, prior, f, "body")); re.Description != f.Description {
		t.Errorf("description = %q, want %q", re.Description, f.Description)
	}
}

func TestRender_YAMLRenameChangesOnlyName(t *testing.T) {
	prior := mustParse(t, "---\n"+yamlWithUnknowns+"\n---\nbody")
	f := prior.Fields()
	f.Name = "renamed"
	re := mustParse(t, renderOK(t, prior, f, "body"))
	if re.Name != "renamed" {
		t.Fatalf("name = %q", re.Name)
	}
	// Untouched keys re-encode, so compare their decoded form, not bytes.
	top, old := yamlTop(t, re.Frontmatter), yamlTop(t, prior.Frontmatter)
	if !reflect.DeepEqual(yamlKeys(top), yamlKeys(old)) {
		t.Errorf("key order changed: %v, want %v", yamlKeys(top), yamlKeys(old))
	}
	want := prior.Fields()
	want.Name = "renamed"
	if !reflect.DeepEqual(re.Fields(), want) || yamlValue(top, "license").Value != "Apache-2.0" ||
		yamlValue(top, "description").Style != yaml.FoldedStyle {
		t.Errorf("rename touched other keys:\n%s", re.Frontmatter)
	}
}

func TestRender_YAMLPriorWithoutFrontmatterWritesFreshBlock(t *testing.T) {
	prior := Skill{Name: "demo", Dialect: DialectYAML}
	out := renderOK(t, &prior, Fields{Name: "demo", Description: "d"}, "body")
	if !strings.HasPrefix(out, "---\nname: demo\ndescription: d\n---\n\nbody") {
		t.Errorf("Render =\n%s", out)
	}
	if re := mustParse(t, out); re.Dialect != DialectYAML || re.Description != "d" {
		t.Errorf("reparsed = %+v", re)
	}
}

func TestRender_EmptyNameIsAnError(t *testing.T) {
	prior := mustParse(t, "---\nname: demo\n---\nbody")
	if _, err := Render(*prior, Fields{}, "body"); err == nil {
		t.Error("Render accepted an empty name")
	}
}
