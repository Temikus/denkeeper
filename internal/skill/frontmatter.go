package skill

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// Dialect is the frontmatter language a skill file was written in.
type Dialect string

const (
	DialectTOML Dialect = "toml" // +++ delimiters
	DialectYAML Dialect = "yaml" // --- delimiters
)

func (d Dialect) delimiter() string {
	if d == DialectYAML {
		return "---"
	}
	return "+++"
}

// knownKeys are the top-level frontmatter keys Denkeeper reads and writes, in
// write order. Inside requires only tools is known. Every other key is
// unknown: kept on write, never updated. A new frontmatter field must be
// added here and to Fields, or edits will never change it.
var knownKeys = []string{"name", "description", "version", "triggers", "max_tool_rounds", "max_output_tokens", "requires"}

const requiresToolsKey = "tools"

// Fields are the frontmatter keys Denkeeper reads and writes.
type Fields struct {
	Name, Description, Version string
	Triggers                   []string
	MaxToolRounds              int
	MaxOutputTokens            int
	RequiresTools              []string
}

// Fields returns the skill's known frontmatter values.
func (s Skill) Fields() Fields {
	return Fields{
		Name: s.Name, Description: s.Description, Version: s.Version,
		Triggers: s.Triggers, MaxToolRounds: s.MaxToolRounds, MaxOutputTokens: s.MaxOutputTokens,
		RequiresTools: s.Requires.Tools,
	}
}

// normalized trims strings and treats an empty slice as nil, so two Fields
// that would write the same frontmatter compare equal.
func (f Fields) normalized() Fields {
	f.Name, f.Description, f.Version = strings.TrimSpace(f.Name), strings.TrimSpace(f.Description), strings.TrimSpace(f.Version)
	if len(f.Triggers) == 0 {
		f.Triggers = nil
	}
	if len(f.RequiresTools) == 0 {
		f.RequiresTools = nil
	}
	return f
}

// value returns the field for a known top-level key, or nil when it is empty
// (and so absent from the frontmatter). requires is handled separately.
func (f Fields) value(key string) any {
	switch key {
	case "name", "description", "version":
		s := map[string]string{"name": f.Name, "description": f.Description, "version": f.Version}[key]
		if s == "" {
			return nil
		}
		return s
	case "triggers":
		if len(f.Triggers) == 0 {
			return nil
		}
		return f.Triggers
	case "max_tool_rounds", "max_output_tokens":
		n := f.MaxToolRounds
		if key == "max_output_tokens" {
			n = f.MaxOutputTokens
		}
		if n == 0 {
			return nil
		}
		return n
	}
	return nil
}

// --- Splitting ---

// splitResult is a skill file cut at its frontmatter delimiters. Block is the
// raw text between them (leading and trailing CR/LF removed, nothing else);
// blockStart is its byte offset in the text, for file line numbers.
type splitResult struct {
	dialect    Dialect
	block      string
	body       string
	blockStart int
}

// splitFrontmatter finds the dialect, the block and the trimmed body.
func splitFrontmatter(path, text string) (splitResult, error) {
	if strings.HasPrefix(text, "+++") {
		rest := text[3:]
		idx := strings.Index(rest, "+++")
		if idx == -1 {
			return splitResult{}, fmt.Errorf("skill %q: missing closing +++ delimiter", path)
		}
		return splitResult{dialect: DialectTOML, block: strings.Trim(rest[:idx], "\r\n"), body: strings.TrimSpace(rest[idx+3:])}, nil
	}
	firstLine, rest, _ := strings.Cut(text, "\n")
	if !isYAMLDelimiter(firstLine) {
		return splitResult{}, fmt.Errorf("skill %q: missing opening frontmatter delimiter (+++ or ---)", path)
	}
	block, body, lead, ok := splitYAML(rest)
	if !ok {
		return splitResult{}, fmt.Errorf("skill %q: missing closing --- delimiter", path)
	}
	return splitResult{dialect: DialectYAML, block: block, body: body, blockStart: len(firstLine) + 1 + lead}, nil
}

func isYAMLDelimiter(line string) bool {
	return strings.TrimRight(line, " \t\r") == "---"
}

// splitYAML ends the block at the first line that is exactly ---. Later ---
// lines belong to the body, so a markdown rule there is safe. lead is how
// many CR/LF bytes were trimmed from the front of the block.
func splitYAML(rest string) (block, body string, lead int, ok bool) {
	pos := 0
	for pos <= len(rest) {
		line, after, more := strings.Cut(rest[pos:], "\n")
		if isYAMLDelimiter(line) {
			raw := rest[:pos]
			lead = len(raw) - len(strings.TrimLeft(raw, "\r\n"))
			return strings.Trim(raw, "\r\n"), strings.TrimSpace(after), lead, true
		}
		if !more {
			return "", "", 0, false
		}
		pos += len(line) + 1
	}
	return "", "", 0, false
}

// --- Decoding ---

func decodeTOML(path, block string, fm *frontmatter) error {
	if err := toml.Unmarshal([]byte(strings.TrimSpace(block)), fm); err != nil {
		return fmt.Errorf("skill %q: parsing frontmatter: %w", path, err)
	}
	return nil
}

// parseYAMLMapping parses block into its top-level mapping node. An empty
// block is an empty mapping. A parser panic becomes an error.
func parseYAMLMapping(block string) (top *yaml.Node, doc *yaml.Node, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("could not parse the YAML frontmatter: %v", r)
		}
	}()
	doc = &yaml.Node{}
	if err := yaml.Unmarshal([]byte(block), doc); err != nil {
		return nil, nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		top = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		return top, nil, nil
	}
	top = doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, nil, errNotMapping
	}
	return top, doc, nil
}

var errNotMapping = fmt.Errorf("frontmatter must be YAML key: value pairs")

// decodeYAML decodes block. lineBase is the file line of the block's first
// line, so refusals name the line a person sees in the file.
func decodeYAML(path, block string, lineBase int, fm *frontmatter) error {
	top, _, err := parseYAMLMapping(block)
	if err == errNotMapping {
		return fmt.Errorf("skill %q: %w", path, err)
	}
	if err != nil {
		return fmt.Errorf("skill %q: parsing frontmatter: %w", path, err)
	}
	if err := checkYAMLTree(path, top, lineBase); err != nil {
		return err
	}
	if err := checkYAMLIntegers(path, top); err != nil {
		return err
	}
	if err := decodeYAMLNode(top, fm); err != nil {
		return fmt.Errorf("skill %q: parsing frontmatter: %w", path, err)
	}
	return nil
}

// decodeYAMLNode runs the struct decode under recover, like the parse.
func decodeYAMLNode(top *yaml.Node, fm *frontmatter) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("could not parse the YAML frontmatter: %v", r)
		}
	}()
	return top.Decode(fm)
}

// checkYAMLTree walks the whole tree before any value is decoded. Anchors and
// aliases are refused (no alias is ever expanded, so a bomb costs nothing),
// as are explicit tags (!!binary turns base64 into any string), merge keys
// and a repeated key in any mapping: yaml.v3 accepts the last two silently
// when decoding into a Node. Lines are file lines: node line + lineBase - 1.
func checkYAMLTree(path string, n *yaml.Node, lineBase int) error {
	line := n.Line + lineBase - 1
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return fmt.Errorf("skill %q: YAML anchors and aliases are not supported in frontmatter (line %d); write the value out in full", path, line)
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return fmt.Errorf("skill %q: YAML tags are not supported in frontmatter (line %d); write the value without a tag", path, line)
	}
	if n.Kind == yaml.MappingNode {
		if err := checkYAMLKeys(path, n, lineBase); err != nil {
			return err
		}
	}
	for _, c := range n.Content {
		if err := checkYAMLTree(path, c, lineBase); err != nil {
			return err
		}
	}
	return nil
}

func checkYAMLKeys(path string, m *yaml.Node, lineBase int) error {
	seen := make(map[string]bool, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		line := k.Line + lineBase - 1
		if k.ShortTag() == "!!merge" {
			return fmt.Errorf("skill %q: YAML merge keys are not supported in frontmatter (line %d); write the keys out in full", path, line)
		}
		if k.Kind == yaml.ScalarNode {
			if seen[k.Value] {
				return fmt.Errorf("skill %q: duplicate frontmatter key %q (line %d)", path, k.Value, line)
			}
			seen[k.Value] = true
		}
	}
	return nil
}

// checkYAMLIntegers refuses a round cap that is not a YAML integer: decoding
// 5.5 into an int would silently truncate it.
func checkYAMLIntegers(path string, top *yaml.Node) error {
	for i := 0; i+1 < len(top.Content); i += 2 {
		key, val := top.Content[i].Value, top.Content[i+1]
		if key != "max_tool_rounds" && key != "max_output_tokens" {
			continue
		}
		if val.Kind == yaml.ScalarNode && (val.ShortTag() == "!!null" || val.ShortTag() == "!!int") {
			continue
		}
		return fmt.Errorf("skill %q: frontmatter field %q must be an integer", path, key)
	}
	return nil
}

// --- Rendering ---

// tomlKnown is the TOML layout of the known keys. requires must stay last: a
// TOML table header captures every key after it.
type tomlKnown struct {
	Name            string        `toml:"name"`
	Description     string        `toml:"description,omitempty"`
	Version         string        `toml:"version,omitempty"`
	Triggers        []string      `toml:"triggers,omitempty"`
	MaxToolRounds   int           `toml:"max_tool_rounds,omitempty"`
	MaxOutputTokens int           `toml:"max_output_tokens,omitempty"`
	Requires        SkillRequires `toml:"requires,omitempty"`
}

// tomlKnownScalars is tomlKnown without requires, for blocks that also carry
// unknown keys.
type tomlKnownScalars struct {
	Name            string   `toml:"name"`
	Description     string   `toml:"description,omitempty"`
	Version         string   `toml:"version,omitempty"`
	Triggers        []string `toml:"triggers,omitempty"`
	MaxToolRounds   int      `toml:"max_tool_rounds,omitempty"`
	MaxOutputTokens int      `toml:"max_output_tokens,omitempty"`
}

func tomlBlock(f Fields) string {
	data, _ := toml.Marshal(tomlKnown{
		Name: f.Name, Description: f.Description, Version: f.Version, Triggers: f.Triggers,
		MaxToolRounds: f.MaxToolRounds, MaxOutputTokens: f.MaxOutputTokens,
		Requires: SkillRequires{Tools: f.RequiresTools},
	})
	return strings.TrimSpace(string(data))
}

// RenderNew writes a new skill file. New skills are always TOML.
func RenderNew(f Fields, body string) string {
	return "+++\n" + tomlBlock(f) + "\n+++\n\n" + strings.TrimSpace(body)
}

// Render writes a skill file for an edited skill in prior's dialect. When no
// field changed, prior's frontmatter block is reused byte for byte. Otherwise
// known keys are updated and unknown keys kept: YAML by editing nodes, TOML
// through a map. A rewrite that changes the set of unknown keys is an error,
// so nothing is written.
func Render(prior Skill, f Fields, body string) (string, error) {
	if strings.TrimSpace(f.Name) == "" {
		return "", fmt.Errorf("skill: name is required")
	}
	dialect := prior.Dialect
	if dialect == "" {
		dialect = DialectTOML
	}
	block, err := renderBlock(prior, dialect, f)
	if err != nil {
		return "", err
	}
	delim := dialect.delimiter()
	return delim + "\n" + block + "\n" + delim + "\n\n" + strings.TrimSpace(body), nil
}

func renderBlock(prior Skill, dialect Dialect, f Fields) (string, error) {
	if prior.Frontmatter == "" {
		if dialect == DialectYAML {
			return rewriteYAML("", Fields{}, f)
		}
		return tomlBlock(f), nil
	}
	old := prior.Fields()
	if reflect.DeepEqual(old.normalized(), f.normalized()) {
		return prior.Frontmatter, nil
	}
	var block string
	var err error
	if dialect == DialectYAML {
		block, err = rewriteYAML(prior.Frontmatter, old, f)
	} else {
		block, err = rewriteTOML(prior.Frontmatter, f)
	}
	if err != nil {
		return "", err
	}
	if err := sameUnknownKeys(dialect, prior.Frontmatter, block); err != nil {
		return "", err
	}
	return block, nil
}

// rewriteTOML drops the known keys from the prior block and writes them back
// from f, known scalars first so no table header can capture them.
func rewriteTOML(raw string, f Fields) (string, error) {
	unknown := map[string]any{}
	if err := toml.Unmarshal([]byte(raw), &unknown); err != nil {
		return "", fmt.Errorf("skill: rewriting frontmatter: %w", err)
	}
	stripKnownTOML(unknown)
	if len(unknown) == 0 {
		return tomlBlock(f), nil
	}
	if len(f.RequiresTools) > 0 {
		req, _ := unknown["requires"].(map[string]any)
		if req == nil {
			req = map[string]any{}
		}
		req[requiresToolsKey] = f.RequiresTools
		unknown["requires"] = req
	}
	known, err := toml.Marshal(tomlKnownScalars{
		Name: f.Name, Description: f.Description, Version: f.Version, Triggers: f.Triggers,
		MaxToolRounds: f.MaxToolRounds, MaxOutputTokens: f.MaxOutputTokens,
	})
	if err != nil {
		return "", fmt.Errorf("skill: rewriting frontmatter: %w", err)
	}
	rest, err := toml.Marshal(unknown)
	if err != nil {
		return "", fmt.Errorf("skill: rewriting frontmatter: %w", err)
	}
	return strings.TrimSpace(string(known)) + "\n\n" + strings.TrimSpace(string(rest)), nil
}

// stripKnownTOML removes the known keys, and tools inside requires. An
// emptied requires table is removed.
func stripKnownTOML(m map[string]any) {
	for _, k := range knownKeys {
		if k != "requires" {
			delete(m, k)
		}
	}
	req, ok := m["requires"].(map[string]any)
	if !ok {
		delete(m, "requires")
		return
	}
	delete(req, requiresToolsKey)
	if len(req) == 0 {
		delete(m, "requires")
	}
}

// rewriteYAML edits only the value nodes of known keys that changed between
// old and f; every other node is untouched. A new key goes at the end, and an
// emptied one is removed (name never is).
func rewriteYAML(raw string, old, f Fields) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("skill: rewriting frontmatter: could not parse the YAML frontmatter: %v", r)
		}
	}()
	top, doc, err := parseYAMLMapping(raw)
	if err != nil {
		return "", fmt.Errorf("skill: rewriting frontmatter: %w", err)
	}
	for _, key := range knownKeys {
		if key == "requires" {
			if !reflect.DeepEqual(old.normalized().RequiresTools, f.normalized().RequiresTools) {
				if err := setYAMLRequiresTools(top, f.normalized().RequiresTools); err != nil {
					return "", err
				}
			}
			continue
		}
		before, after := old.normalized().value(key), f.normalized().value(key)
		if reflect.DeepEqual(before, after) {
			continue
		}
		if err := setYAMLKey(top, key, after); err != nil {
			return "", err
		}
	}
	return encodeYAML(top, doc)
}

func setYAMLKey(m *yaml.Node, key string, v any) error {
	idx := yamlKeyIndex(m, key)
	if v == nil {
		if idx >= 0 && key != "name" {
			m.Content = append(m.Content[:idx], m.Content[idx+2:]...)
		}
		return nil
	}
	var val yaml.Node
	if err := val.Encode(v); err != nil {
		return fmt.Errorf("skill: encoding frontmatter field %q: %w", key, err)
	}
	if idx >= 0 {
		// Keep the line comment that sits on the key; the old value's goes.
		m.Content[idx+1] = &val
		return nil
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &val)
	return nil
}

// setYAMLRequiresTools sets or removes tools inside requires. An emptied
// requires mapping is removed; one with unknown keys stays.
func setYAMLRequiresTools(top *yaml.Node, tools []string) error {
	idx := yamlKeyIndex(top, "requires")
	if idx < 0 {
		if len(tools) == 0 {
			return nil
		}
		req := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if err := setYAMLKey(req, requiresToolsKey, tools); err != nil {
			return err
		}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "requires"}, req)
		return nil
	}
	req := top.Content[idx+1]
	if req.Kind != yaml.MappingNode {
		return fmt.Errorf("skill: frontmatter field \"requires\" is not a mapping")
	}
	var v any
	if len(tools) > 0 {
		v = tools
	}
	if err := setYAMLKey(req, requiresToolsKey, v); err != nil {
		return err
	}
	if len(req.Content) == 0 {
		top.Content = append(top.Content[:idx], top.Content[idx+2:]...)
	}
	return nil
}

func yamlKeyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func encodeYAML(top, doc *yaml.Node) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	root := top
	if doc != nil {
		root = doc
	}
	if err := enc.Encode(root); err != nil {
		return "", fmt.Errorf("skill: encoding frontmatter: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("skill: encoding frontmatter: %w", err)
	}
	return strings.Trim(buf.String(), "\r\n"), nil
}

// ErrUnknownKeysChanged is Render refusing a rewrite that would lose or add a
// key Denkeeper does not read. Nothing is written.
var ErrUnknownKeysChanged = errors.New("skill: rewriting frontmatter changed its unknown keys")

// sameUnknownKeys compares the unknown key names (top level and inside
// requires) of two blocks of one dialect. Any difference means the rewrite
// lost or invented a key.
func sameUnknownKeys(dialect Dialect, before, after string) error {
	b, err := unknownKeys(dialect, before)
	if err != nil {
		return fmt.Errorf("skill: rewriting frontmatter: %w", err)
	}
	a, err := unknownKeys(dialect, after)
	if err != nil {
		return fmt.Errorf("skill: rewriting frontmatter: %w", err)
	}
	if !reflect.DeepEqual(a, b) {
		return fmt.Errorf("%w (before %v, after %v)", ErrUnknownKeysChanged, b, a)
	}
	return nil
}

func unknownKeys(dialect Dialect, block string) ([]string, error) {
	var top, req []string
	if dialect == DialectYAML {
		m, _, err := parseYAMLMapping(block)
		if err != nil {
			return nil, err
		}
		top = yamlMappingKeys(m)
		if i := yamlKeyIndex(m, "requires"); i >= 0 {
			req = yamlMappingKeys(m.Content[i+1])
		}
	} else {
		var m map[string]any
		if err := toml.Unmarshal([]byte(block), &m); err != nil {
			return nil, err
		}
		for k := range m {
			top = append(top, k)
		}
		if r, ok := m["requires"].(map[string]any); ok {
			for k := range r {
				req = append(req, k)
			}
		}
	}
	return filterUnknown(top, req), nil
}

func yamlMappingKeys(m *yaml.Node) []string {
	var keys []string
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i].Value)
	}
	return keys
}

func filterUnknown(top, req []string) []string {
	known := make(map[string]bool, len(knownKeys))
	for _, k := range knownKeys {
		known[k] = true
	}
	out := []string{}
	for _, k := range top {
		if !known[k] {
			out = append(out, k)
		}
	}
	for _, k := range req {
		if k != requiresToolsKey {
			out = append(out, "requires."+k)
		}
	}
	sort.Strings(out)
	return out
}
