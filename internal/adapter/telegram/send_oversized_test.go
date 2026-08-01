package telegram

import (
	"context"
	_ "embed"
	"strings"
	"testing"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/jamestelfer/telegold/tgtest"
)

// goSourceFixture is a real Go source file, not a synthetic string. Real code
// carries what a generated fixture forgets: angle brackets and ampersands that
// have to survive as entities, long unbroken identifier runs with nowhere
// natural to break, and blank lines whose loss a word-level comparison would
// not notice.
//
// It is a frozen snapshot, deliberately not kept in sync with the file it was
// taken from — see the header inside it. Embedding a live source file would
// make this test's chunk count a function of that file's length, and a file
// still under change could drift past maxOutboundChunks and quietly turn a
// no-loss test into a truncation test.
//
//go:embed testdata/sample_source.go.txt
var goSourceFixture string

// entityDecoder is the exact inverse of what the renderer escapes. Deliberately
// narrow rather than html.UnescapeString: an entity outside this set reaching
// the output would be a renderer defect, and this test should surface it as a
// mismatch instead of silently decoding it. Simultaneous replacement, so an
// escaped ampersand cannot be decoded twice.
var entityDecoder = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")

// TestSend_OversizedCodeBlockReassemblesToItsSource is Phase 6's adapter-level
// proof of R22. A code block several times the chunk limit must arrive as
// several messages, each a well-formed code block in its own right, together
// carrying the original source with nothing lost, duplicated or reordered.
//
// The comparison is byte equality against the fixture, which is the no-loss
// proof the plan asks for: a dropped newline, a mangled entity or a boundary
// landing mid-rune all fail it, and none of them are visible to a human reading
// the messages.
func TestSend_OversizedCodeBlockReassemblesToItsSource(t *testing.T) {
	bot := newFakeBot()
	a := newWithSender(bot, nil, testLogger(), nil)

	if err := a.Send(context.Background(), adapter.OutgoingMessage{
		ExternalID: "12345",
		Text:       "```go\n" + goSourceFixture + "```\n",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := bot.messages(t)
	if len(msgs) < 2 {
		t.Fatalf("send count = %d, want several — a block over the limit must split", len(msgs))
	}
	assertSentChunks(t, msgs)

	const openTags = `<pre><code class="language-go">`
	const closeTags = `</code></pre>`

	var reassembled strings.Builder
	for i, m := range msgs {
		if !strings.HasPrefix(m.Text, openTags) {
			t.Errorf("chunk %d does not reopen the code block, it starts %.40q", i+1, m.Text)
			continue
		}
		if !strings.HasSuffix(m.Text, closeTags) {
			t.Errorf("chunk %d does not close the code block, it ends %q",
				i+1, m.Text[max(len(m.Text)-len(closeTags), 0):])
			continue
		}
		body := m.Text[len(openTags) : len(m.Text)-len(closeTags)]
		reassembled.WriteString(entityDecoder.Replace(body))
	}

	tgtest.AssertSameText(t, goSourceFixture, reassembled.String())
}
