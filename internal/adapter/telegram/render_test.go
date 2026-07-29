package telegram

import (
	"html"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// tagStripper removes the HTML tags renderTelegramHTML emits so a test can
// recover the visible text a Telegram client would show.
var tagStripper = regexp.MustCompile(`<[^>]*>`)

// visibleText returns the text a Telegram client renders for the given
// parse_mode=HTML payload: tags are markup, entities decode back to their
// source characters.
func visibleText(rendered string) string {
	return html.UnescapeString(tagStripper.ReplaceAllString(rendered, ""))
}

// The message from issue #243: legacy Markdown paired the underscore in one
// URL path with the underscore in the next, deleting both and italicising
// everything in between.
const twoURLMessage = "Report is at " +
	"https://example.com/flash/7/NRR-JaUmg_hm7jYl-LiWx/gmail-mcp-servers-brief-v2.md " +
	"and the **HTML** copy is at " +
	"https://example.com/flash/7/QQR-KbVnh_in8kZm-MjXy/gmail-mcp-servers-brief-v2.html " +
	"— check both."

// twoURLMessageVisible is what a Telegram client must display for
// twoURLMessage: identical except that the ** markers become bold styling.
const twoURLMessageVisible = "Report is at " +
	"https://example.com/flash/7/NRR-JaUmg_hm7jYl-LiWx/gmail-mcp-servers-brief-v2.md " +
	"and the HTML copy is at " +
	"https://example.com/flash/7/QQR-KbVnh_in8kZm-MjXy/gmail-mcp-servers-brief-v2.html " +
	"— check both."

func TestRenderTelegramHTML_TwoURLsWithUnderscoresPreserved(t *testing.T) {
	got := renderTelegramHTML(twoURLMessage)

	for _, want := range []string{
		"NRR-JaUmg_hm7jYl-LiWx",
		"QQR-KbVnh_in8kZm-MjXy",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output lost URL path segment %q\ngot: %s", want, got)
		}
	}
	if strings.Contains(got, "<i>") || strings.Contains(got, "<em>") {
		t.Errorf("underscores inside URLs must not produce emphasis\ngot: %s", got)
	}
	// The **HTML** between the two URLs is intentional bold, not literal
	// asterisks swallowed by an unterminated italic span.
	if !strings.Contains(got, "<b>HTML</b>") {
		t.Errorf("expected bold HTML between the URLs\ngot: %s", got)
	}
}

func TestRenderTelegramHTML_URLsRemainClickableWithFullPath(t *testing.T) {
	got := renderTelegramHTML(twoURLMessage)

	// Telegram autolinks post-parse text, so a lost underscore also breaks the
	// anchor target. Assert the href carries the exact path.
	for _, want := range []string{
		`href="https://example.com/flash/7/NRR-JaUmg_hm7jYl-LiWx/gmail-mcp-servers-brief-v2.md"`,
		`href="https://example.com/flash/7/QQR-KbVnh_in8kZm-MjXy/gmail-mcp-servers-brief-v2.html"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected anchor %s\ngot: %s", want, got)
		}
	}
}

func TestRenderTelegramHTML_NoCharacterLossInPlainProse(t *testing.T) {
	// A message with no intentional markup at all must survive verbatim:
	// snake_case identifiers, asterisks, brackets, angle brackets, ampersands.
	src := "Set max_tool_rounds and skill_name_v2 in the config. " +
		"Math: 3 * 4 * 5 and a[0] < b[1] && c > d. " +
		"See https://example.com/a_b/c_d_e?q=x_y#frag_1 for the schema_v2 notes."

	got := visibleText(renderTelegramHTML(src))
	if got != src {
		t.Errorf("plain prose was not preserved verbatim\n want: %q\n  got: %q", src, got)
	}
}

func TestRenderTelegramHTML_MarkupCharactersSurviveAsText(t *testing.T) {
	src := "Fields: `code_with_underscores`, a literal [bracket, an * asterisk, " +
		"a ` backtick pair `x_y` and a trailing _underscore."

	rendered := renderTelegramHTML(src)
	for _, want := range []string{
		"code_with_underscores",
		"[bracket",
		"x_y",
		"_underscore",
	} {
		if !strings.Contains(visibleText(rendered), want) {
			t.Errorf("expected %q in visible text\nrendered: %s", want, rendered)
		}
	}
}

func TestRenderTelegramHTML_EscapesHTMLSpecialCharacters(t *testing.T) {
	got := renderTelegramHTML("Use <b>tags</b> & <script>alert(1)</script> carefully")

	if strings.Contains(got, "<script>") {
		t.Fatalf("raw HTML from the model must be escaped, got: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("expected escaped script tag, got: %s", got)
	}
	if !strings.Contains(got, "&amp;") {
		t.Errorf("expected escaped ampersand, got: %s", got)
	}
	if visible := visibleText(got); !strings.Contains(visible, "<script>alert(1)</script>") {
		t.Errorf("escaped HTML must render back as the literal source, got: %q", visible)
	}
}

func TestRenderTelegramHTML_PreservesBoldAndItalic(t *testing.T) {
	got := renderTelegramHTML("This is **bold** and this is *italic*.")

	if !strings.Contains(got, "<b>bold</b>") {
		t.Errorf("expected bold tag, got: %s", got)
	}
	if !strings.Contains(got, "<i>italic</i>") {
		t.Errorf("expected italic tag, got: %s", got)
	}
}

func TestRenderTelegramHTML_PreservesFencedCodeBlock(t *testing.T) {
	src := "Run this:\n\n```go\nfunc main() { fmt.Println(\"a_b < c\") }\n```\n"
	got := renderTelegramHTML(src)

	if !strings.Contains(got, `<pre><code class="language-go">`) {
		t.Errorf("expected language-tagged pre block, got: %s", got)
	}
	if !strings.Contains(got, "a_b &lt; c") {
		t.Errorf("code block content must be escaped verbatim, got: %s", got)
	}
}

func TestRenderTelegramHTML_UntaggedCodeBlockOmitsLanguageClass(t *testing.T) {
	got := renderTelegramHTML("```\nplain_code_here\n```")

	if !strings.Contains(got, "<pre>plain_code_here</pre>") {
		t.Errorf("expected bare pre block, got: %s", got)
	}
}

func TestRenderTelegramHTML_RejectsUnusableCodeLanguage(t *testing.T) {
	got := renderTelegramHTML("```go\"><b>\nx\n```")

	if strings.Contains(got, `class="language-`) {
		t.Errorf("an unusable info string must be dropped, got: %s", got)
	}
	if !strings.Contains(got, "<pre>x</pre>") {
		t.Errorf("expected bare pre block, got: %s", got)
	}
}

func TestRenderTelegramHTML_InlineCodeIsEscaped(t *testing.T) {
	got := renderTelegramHTML("Call `foo_bar(a < b)` first.")

	if !strings.Contains(got, "<code>foo_bar(a &lt; b)</code>") {
		t.Errorf("expected escaped code span, got: %s", got)
	}
}

func TestRenderTelegramHTML_MarkdownLinkKeepsLabelAndTarget(t *testing.T) {
	got := renderTelegramHTML("See [the brief_v2 doc](https://example.com/a_b_c/d.md).")

	if !strings.Contains(got, `<a href="https://example.com/a_b_c/d.md">the brief_v2 doc</a>`) {
		t.Errorf("expected intact anchor, got: %s", got)
	}
}

func TestRenderTelegramHTML_UnsupportedSchemeRendersLabelOnly(t *testing.T) {
	got := renderTelegramHTML("[click me](javascript:alert(1))")

	if strings.Contains(got, "javascript:") {
		t.Fatalf("javascript: URLs must not become anchors, got: %s", got)
	}
	if !strings.Contains(got, "click me") {
		t.Errorf("link label must still be shown, got: %s", got)
	}
}

func TestRenderTelegramHTML_HeadingsBecomeBold(t *testing.T) {
	got := renderTelegramHTML("# Daily brief\n\nBody text.")

	if !strings.Contains(got, "<b>Daily brief</b>") {
		t.Errorf("expected heading rendered as bold, got: %s", got)
	}
	if strings.Contains(got, "<h1>") {
		t.Errorf("Telegram does not support <h1>, got: %s", got)
	}
}

func TestRenderTelegramHTML_ListsBecomePlainLines(t *testing.T) {
	got := renderTelegramHTML("- first_item\n- second_item\n\n1. one_a\n2. two_b")

	for _, want := range []string{"• first_item", "• second_item", "1. one_a", "2. two_b"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got: %s", want, got)
		}
	}
	if strings.Contains(got, "<ul>") || strings.Contains(got, "<li>") {
		t.Errorf("Telegram does not support list tags, got: %s", got)
	}
}

func TestRenderTelegramHTML_NestedListIsIndented(t *testing.T) {
	got := renderTelegramHTML("- outer_one\n    - inner_one\n- outer_two")

	if !strings.Contains(got, "  • inner_one") {
		t.Errorf("expected indented nested bullet, got: %q", got)
	}
}

func TestRenderTelegramHTML_BlockquoteUsesTelegramTag(t *testing.T) {
	got := renderTelegramHTML("> quoted_text here")

	if !strings.Contains(got, "<blockquote>quoted_text here</blockquote>") {
		t.Errorf("expected blockquote, got: %s", got)
	}
}

func TestRenderTelegramHTML_StrikethroughSupported(t *testing.T) {
	got := renderTelegramHTML("~~gone_now~~ stays")

	if !strings.Contains(got, "<s>gone_now</s>") {
		t.Errorf("expected strikethrough, got: %s", got)
	}
}

func TestRenderTelegramHTML_SoftLineBreaksPreserved(t *testing.T) {
	got := renderTelegramHTML("line_one\nline_two")

	if got != "line_one\nline_two" {
		t.Errorf("expected both lines preserved, got: %q", got)
	}
}

func TestRenderTelegramHTML_EmptyInput(t *testing.T) {
	if got := renderTelegramHTML(""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	if got := renderTelegramHTML("   \n\t "); got != "" {
		t.Errorf("expected empty string for whitespace-only input, got %q", got)
	}
}

func TestRenderTelegramHTML_ImageBecomesLink(t *testing.T) {
	got := renderTelegramHTML("![alt_text](https://example.com/a_b.png)")

	if !strings.Contains(got, `<a href="https://example.com/a_b.png">alt_text</a>`) {
		t.Errorf("expected image rendered as link, got: %s", got)
	}
}

func TestRenderTelegramHTML_NoUnsupportedTagsEmitted(t *testing.T) {
	// Telegram rejects a message containing any tag outside its allowlist, so
	// the renderer must never emit one.
	allowed := map[string]bool{
		"b": true, "i": true, "u": true, "s": true,
		"a": true, "code": true, "pre": true, "blockquote": true,
	}
	src := "# Head\n\n- item\n\n> quote\n\n```py\nx=1\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n" +
		"---\n\nText with **bold**, *it*, ~~s~~, `c`, [l](https://e.com), <div>raw</div>."

	got := renderTelegramHTML(src)
	for _, m := range tagStripper.FindAllString(got, -1) {
		name := strings.TrimSuffix(strings.TrimPrefix(strings.Trim(m, "<>"), "/"), " ")
		if i := strings.IndexAny(name, " \t"); i >= 0 {
			name = name[:i]
		}
		if !allowed[name] {
			t.Errorf("emitted unsupported tag %q in: %s", m, got)
		}
	}
}

func TestRenderTelegramHTML_ConcurrentRendersAreSafe(t *testing.T) {
	// The goldmark parser is shared across every agent reply, so the dispatcher
	// can call the renderer from several chats at once.
	want := renderTelegramHTML(twoURLMessage)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := renderTelegramHTML(twoURLMessage); got != want {
				t.Errorf("concurrent render diverged:\n want: %s\n  got: %s", want, got)
			}
		}()
	}
	wg.Wait()
}

func TestRenderTelegramHTML_TableTextIsNotLost(t *testing.T) {
	src := "| col_a | col_b |\n|---|---|\n| v_1 | v_2 |"
	got := visibleText(renderTelegramHTML(src))

	for _, want := range []string{"col_a", "col_b", "v_1", "v_2"} {
		if !strings.Contains(got, want) {
			t.Errorf("table cell %q was lost, got: %q", want, got)
		}
	}
}
