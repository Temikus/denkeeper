package tool

import (
	"io"
	"log/slog"
	"testing"

	"github.com/Temikus/denkeeper/internal/llm"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestToolNames_Empty(t *testing.T) {
	m := NewManager(testLogger())
	names := m.ToolNames()
	if len(names) != 0 {
		t.Errorf("ToolNames() on empty manager = %v, want []", names)
	}
}

func TestToolNames_WithTools(t *testing.T) {
	m := NewManager(testLogger())
	m.toolDefs = []llm.ToolDef{
		{Type: "function", Function: llm.FunctionDef{Name: "search_web"}},
		{Type: "function", Function: llm.FunctionDef{Name: "read_url"}},
		{Type: "function", Function: llm.FunctionDef{Name: "shell_exec"}},
	}

	names := m.ToolNames()
	if len(names) != 3 {
		t.Fatalf("ToolNames() count = %d, want 3", len(names))
	}
	want := []string{"search_web", "read_url", "shell_exec"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("ToolNames()[%d] = %q, want %q", i, names[i], w)
		}
	}
}
