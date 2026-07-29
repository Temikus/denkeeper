package telegram

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Agent replies are CommonMark authored by an LLM. They used to be handed to
// Telegram with parse_mode=Markdown (the legacy parser) verbatim, which has no
// intraword-emphasis rule: an underscore inside a URL path pairs with the next
// underscore anywhere in the message, silently deleting both characters and
// italicising everything between them. Telegram accepts such a message, so the
// corruption is invisible to the sender.
//
// Instead the markdown is parsed with a real CommonMark parser (the same
// dialect the web dashboard renders with) and re-emitted as the restricted HTML
// subset Telegram's parse_mode=HTML accepts. Every text node is escaped, so no
// character in the source can be reinterpreted as markup on Telegram's side.
// Bare URLs are consumed by the linkify extension before emphasis delimiters
// are collected, which keeps their underscores literal by construction.

// telegramMarkdown is the parser used for agent replies. Linkify keeps bare
// URLs intact (and clickable); Strikethrough matches the GFM dialect the web
// dashboard uses. Tables are deliberately not enabled — Telegram has no table
// markup, and leaving them as literal pipe-delimited text is lossless.
var telegramMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.Linkify, extension.Strikethrough),
)

// escapeText escapes the three characters Telegram's HTML parser treats as
// markup. Unlike html.EscapeString it leaves quotes and apostrophes alone:
// numeric character references are needless noise in message bodies, and
// Telegram counts entities against the 4096-character message cap.
var escapeText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

// escapeAttr escapes a value destined for a double-quoted HTML attribute.
var escapeAttr = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace

// renderTelegramHTML converts CommonMark markdown into the HTML subset
// Telegram accepts with parse_mode=HTML. Unsupported constructs degrade to
// escaped plain text rather than being dropped.
func renderTelegramHTML(src string) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	source := []byte(src)
	doc := telegramMarkdown.Parser().Parse(text.NewReader(source))
	return strings.TrimSpace(renderBlocks(source, doc))
}

// renderBlocks renders every block child of n, separated by a blank line.
func renderBlocks(src []byte, n ast.Node) string {
	parts := make([]string, 0, n.ChildCount())
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if s := renderBlock(src, c); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// renderBlock renders a single block-level node. Containers that Telegram has
// no markup for (documents, list items, unknown extensions) recurse into their
// children so their content is never lost.
func renderBlock(src []byte, n ast.Node) string {
	switch v := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return renderInlines(src, v)
	case *ast.Heading:
		return wrapNonEmpty("<b>", renderInlines(src, v), "</b>")
	case *ast.FencedCodeBlock:
		return renderCode(src, v, string(v.Language(src)))
	case *ast.CodeBlock:
		return renderCode(src, v, "")
	case *ast.Blockquote:
		return wrapNonEmpty("<blockquote>", renderBlocks(src, v), "</blockquote>")
	case *ast.List:
		return renderList(src, v, 0)
	case *ast.ThematicBreak:
		return "———"
	case *ast.HTMLBlock:
		// Raw HTML from the model is content, not markup: escape it so the
		// user sees exactly what the agent wrote.
		return escapeText(strings.TrimRight(linesText(src, v), "\n"))
	default:
		return renderBlocks(src, n)
	}
}

// renderList flattens a markdown list into indented bullet or numbered lines.
// Telegram has no list markup, so the structure is expressed as plain text.
func renderList(src []byte, l *ast.List, depth int) string {
	indent := strings.Repeat("  ", depth)
	num := l.Start
	if num == 0 {
		num = 1
	}

	lines := make([]string, 0, l.ChildCount())
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "• "
		if l.IsOrdered() {
			marker = fmt.Sprintf("%d. ", num)
			num++
		}
		body := renderListItem(src, item, depth)
		if body == "" {
			continue
		}
		lines = append(lines, indent+marker+body)
	}
	return strings.Join(lines, "\n")
}

// renderListItem renders one list item; nested lists continue on their own
// lines below the item text.
func renderListItem(src []byte, item ast.Node, depth int) string {
	parts := make([]string, 0, item.ChildCount())
	for c := item.FirstChild(); c != nil; c = c.NextSibling() {
		var s string
		if sub, ok := c.(*ast.List); ok {
			s = renderList(src, sub, depth+1)
		} else {
			s = renderBlock(src, c)
		}
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

// renderCode renders a code block. Telegram only understands a
// "language-<name>" class, so an unusable info string is dropped rather than
// risking a rejected message.
func renderCode(src []byte, n ast.Node, lang string) string {
	body := escapeText(strings.TrimRight(linesText(src, n), "\n"))
	if lang := sanitizeLanguage(lang); lang != "" {
		return `<pre><code class="language-` + lang + `">` + body + "</code></pre>"
	}
	return "<pre>" + body + "</pre>"
}

// sanitizeLanguage returns the info string when it is a plain language token,
// otherwise the empty string.
func sanitizeLanguage(lang string) string {
	if lang == "" || len(lang) > 32 {
		return ""
	}
	for _, r := range lang {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '+', r == '#', r == '-', r == '_', r == '.':
		default:
			return ""
		}
	}
	return lang
}

// linesText concatenates the raw source lines backing a block node.
func linesText(src []byte, n ast.Node) string {
	var b strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		b.Write(seg.Value(src))
	}
	return b.String()
}

// renderInlines renders every inline child of n.
func renderInlines(src []byte, n ast.Node) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		b.WriteString(renderInline(src, c))
	}
	return b.String()
}

// renderInline renders a single inline node. Text is always escaped, so a
// source character can never be reinterpreted as Telegram markup.
func renderInline(src []byte, n ast.Node) string {
	switch v := n.(type) {
	case *ast.Text:
		return renderText(src, v)
	case *ast.String:
		return escapeText(string(v.Value))
	case *ast.CodeSpan:
		return wrapNonEmpty("<code>", escapeText(rawInlineText(src, v)), "</code>")
	case *ast.Emphasis:
		return renderEmphasis(src, v)
	case *extast.Strikethrough:
		return wrapNonEmpty("<s>", renderInlines(src, v), "</s>")
	case *ast.Link:
		return renderLink(string(v.Destination), renderInlines(src, v))
	case *ast.AutoLink:
		return renderLink(string(v.URL(src)), escapeText(string(v.Label(src))))
	case *ast.Image:
		// Telegram cannot inline an image in a text message; surface it as a
		// link carrying the alt text.
		return renderLink(string(v.Destination), renderInlines(src, v))
	case *ast.RawHTML:
		return escapeText(string(v.Segments.Value(src)))
	default:
		return renderInlines(src, n)
	}
}

// renderText renders a text node, preserving line breaks.
func renderText(src []byte, t *ast.Text) string {
	s := escapeText(string(t.Segment.Value(src)))
	if t.HardLineBreak() || t.SoftLineBreak() {
		s += "\n"
	}
	return s
}

// renderEmphasis maps CommonMark emphasis levels onto Telegram's tags.
func renderEmphasis(src []byte, e *ast.Emphasis) string {
	inner := renderInlines(src, e)
	if e.Level >= 2 {
		return wrapNonEmpty("<b>", inner, "</b>")
	}
	return wrapNonEmpty("<i>", inner, "</i>")
}

// rawInlineText concatenates the literal source text of an inline node's
// children, used for code spans where nothing is markup.
func rawInlineText(src []byte, n ast.Node) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			b.Write(v.Segment.Value(src))
		case *ast.String:
			b.Write(v.Value)
		default:
			b.WriteString(rawInlineText(src, c))
		}
	}
	return b.String()
}

// linkSchemes are the URL schemes Telegram will accept in an href. Anything
// else (relative paths, javascript:, data:) renders as its label only.
var linkSchemes = []string{"http://", "https://", "ftp://", "mailto:", "tg://"}

// renderLink emits an anchor when the destination is a scheme Telegram
// supports, otherwise the label alone so no text is lost.
func renderLink(dest, label string) string {
	if label == "" {
		label = escapeText(dest)
	}
	lower := strings.ToLower(dest)
	for _, scheme := range linkSchemes {
		if strings.HasPrefix(lower, scheme) {
			return `<a href="` + escapeAttr(dest) + `">` + label + "</a>"
		}
	}
	return label
}

// wrapNonEmpty wraps inner in the given tags, returning "" when there is
// nothing to wrap — Telegram rejects empty entities.
func wrapNonEmpty(openTag, inner, closeTag string) string {
	if inner == "" {
		return ""
	}
	return openTag + inner + closeTag
}
