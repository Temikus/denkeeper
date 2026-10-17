package tasktools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Temikus/denkeeper/internal/task"
)

const (
	pamela = "agent:pamela"
	scout  = "agent:scout"
	claude = "ext:claude-code"
)

func newService(t *testing.T) *task.Service {
	t.Helper()
	store, err := task.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	dir := task.DirectoryFunc(func(context.Context) ([]task.Member, error) {
		return []task.Member{
			{ID: pamela, Name: "pamela", Kind: "agent"},
			{ID: scout, Name: "scout", Kind: "agent"},
			{ID: claude, Name: "claude-code", Kind: "external"},
		}, nil
	})
	return task.NewService(store, dir, nil)
}

// connect registers the tools on a fresh server whose authorizer always acts
// as actor, refusing writes when readOnly is set, and returns a client session.
func connect(t *testing.T, svc *task.Service, actor string, readOnly bool) *mcp.ClientSession {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "tasks-test", Version: "test"}, nil)
	Register(srv, svc, func(_ context.Context, write bool) (string, *mcp.CallToolResult) {
		if write && readOnly {
			return "", &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "insufficient scope"}}, IsError: true}
		}
		return actor, nil
	})
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call runs a tool and returns its text and error flag.
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s: content is %T, want text", name, res.Content[0])
	}
	return tc.Text, res.IsError
}

func str(v any) string { s, _ := v.(string); return s }

func asList(v any) []any { l, _ := v.([]any); return l }

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

// callOK runs a tool that must succeed and decodes its JSON result.
func callOK(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	text, isErr := call(t, s, name, args)
	if isErr {
		t.Fatalf("%s(%v) failed: %s", name, args, text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: decoding %q: %v", name, text, err)
	}
	return out
}

func taskField(out map[string]any, field string) string {
	return str(obj(out["task"])[field])
}

func TestRegister_SixTools(t *testing.T) {
	s := connect(t, newService(t), pamela, false)
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := "task_checkout,task_comment,task_create,task_get,task_list,task_update"
	if strings.Join(names, ",") != want {
		t.Errorf("tools = %v, want %s", names, want)
	}
}

func TestTools_DelegationRoundTrip(t *testing.T) {
	svc := newService(t)
	pam := connect(t, svc, pamela, false)
	cc := connect(t, svc, claude, false)

	parent := callOK(t, pam, "task_create", map[string]any{"title": "Add Fastmail to the weekly digest", "assignee": "me"})
	if taskField(parent, "key") != "DK-1" || !strings.Contains(str(parent["note"]), "Claim DK-1") {
		t.Fatalf("parent = %v", parent)
	}
	callOK(t, pam, "task_checkout", map[string]any{"id": "DK-1"})

	child := callOK(t, pam, "task_create", map[string]any{
		"title": "Check Fastmail rate limits", "assignee": "claude-code", "parent": "DK-1",
	})
	if taskField(child, "parent") != "DK-1" || taskField(child, "reviewer") != pamela {
		t.Errorf("child = %v", child)
	}
	if note := str(child["note"]); !strings.Contains(note, "Don't wait for it in this run") {
		t.Errorf("child note = %q", note)
	}

	// Claude Code finds its work with the defaults and does it.
	mine := callOK(t, cc, "task_list", nil)
	if list := asList(mine["tasks"]); len(list) != 1 || obj(list[0])["key"] != "DK-2" {
		t.Fatalf("claude's list = %v", mine)
	}
	callOK(t, cc, "task_checkout", map[string]any{"id": "dk-2"})
	callOK(t, cc, "task_comment", map[string]any{"id": "2", "body": "JMAP allows a weekly pull comfortably."})
	review := callOK(t, cc, "task_update", map[string]any{"id": "DK-2", "status": "review"})
	if !strings.Contains(str(review["note"]), "pamela reviews DK-2") {
		t.Errorf("review note = %v", review["note"])
	}

	// Claude can't close its own reviewed work; Pamela can.
	text, isErr := call(t, cc, "task_update", map[string]any{"id": "DK-2", "status": "done"})
	if !isErr || !strings.Contains(text, "needs review by pamela") {
		t.Errorf("claude close = %q, %v", text, isErr)
	}
	callOK(t, pam, "task_update", map[string]any{"id": "DK-2", "status": "done"})

	detail := callOK(t, pam, "task_get", map[string]any{"id": "DK-1"})
	if taskField(detail, "subtasks") != "1/1" {
		t.Errorf("parent subtasks = %v", detail["task"])
	}
	child2 := callOK(t, pam, "task_get", map[string]any{"id": "DK-2"})
	thread := asList(child2["thread"])
	if len(thread) < 4 || child2["parent"] == nil {
		t.Errorf("child detail = %v", child2)
	}
}

func TestTools_RefusedClaimSaysDontRetry(t *testing.T) {
	svc := newService(t)
	pam := connect(t, svc, pamela, false)
	sc := connect(t, svc, scout, false)
	callOK(t, pam, "task_create", map[string]any{"title": "Unassigned chore"})
	callOK(t, pam, "task_checkout", map[string]any{"id": "DK-1"})

	text, isErr := call(t, sc, "task_checkout", map[string]any{"id": "DK-1"})
	if !isErr || !strings.Contains(text, "held by pamela") || !strings.Contains(text, "Don't retry") {
		t.Errorf("refused claim = %q, %v", text, isErr)
	}
}

func TestTools_ListUnassignedAndMembers(t *testing.T) {
	svc := newService(t)
	pam := connect(t, svc, pamela, false)
	callOK(t, pam, "task_create", map[string]any{"title": "For anyone"})
	callOK(t, pam, "task_create", map[string]any{"title": "For scout", "assignee": "scout"})

	out := callOK(t, pam, "task_list", map[string]any{"assignee": "none", "members": true})
	list := asList(out["tasks"])
	if len(list) != 1 || obj(list[0])["title"] != "For anyone" {
		t.Errorf("unassigned = %v", list)
	}
	if ms := asList(out["members"]); len(ms) != 4 {
		t.Errorf("members = %v, want you plus three", ms)
	}
	anyone := callOK(t, pam, "task_list", map[string]any{"assignee": "any"})
	if len(asList(anyone["tasks"])) != 2 {
		t.Errorf("any = %v", anyone)
	}
}

func TestTools_ClosedTasksOnlyWhenAsked(t *testing.T) {
	svc := newService(t)
	pam := connect(t, svc, pamela, false)
	callOK(t, pam, "task_create", map[string]any{"title": "Quick one", "assignee": "me"})
	callOK(t, pam, "task_update", map[string]any{"id": "DK-1", "status": "done"})

	if open := callOK(t, pam, "task_list", nil); len(asList(open["tasks"])) != 0 {
		t.Errorf("default list includes closed: %v", open)
	}
	if all := callOK(t, pam, "task_list", map[string]any{"include_closed": true}); len(asList(all["tasks"])) != 1 {
		t.Errorf("include_closed = %v", all)
	}
}

func TestTools_ReadOnlyCallerCanReadNotWrite(t *testing.T) {
	svc := newService(t)
	if _, err := svc.Create(context.Background(), task.Human, task.CreateRequest{Title: "Seeded", Assignee: "scout"}); err != nil {
		t.Fatal(err)
	}
	ro := connect(t, svc, scout, true)

	if out := callOK(t, ro, "task_list", nil); len(asList(out["tasks"])) != 1 {
		t.Errorf("read-only list = %v", out)
	}
	callOK(t, ro, "task_get", map[string]any{"id": "DK-1"})
	writes := map[string]map[string]any{
		"task_create":   {"title": "x"},
		"task_checkout": {"id": "DK-1"},
		"task_comment":  {"id": "DK-1", "body": "x"},
		"task_update":   {"id": "DK-1", "status": "review"},
	}
	for name, args := range writes {
		if text, isErr := call(t, ro, name, args); !isErr || text != "insufficient scope" {
			t.Errorf("%s as read-only = %q, %v", name, text, isErr)
		}
	}
}

func TestTools_BadInput(t *testing.T) {
	pam := connect(t, newService(t), pamela, false)
	if text, isErr := call(t, pam, "task_get", map[string]any{"id": "abc"}); !isErr || !strings.Contains(text, "invalid task id") {
		t.Errorf("bad id = %q", text)
	}
	if text, isErr := call(t, pam, "task_get", map[string]any{"id": "DK-9"}); !isErr || !strings.Contains(text, "not found") {
		t.Errorf("missing = %q", text)
	}
	if text, isErr := call(t, pam, "task_list", map[string]any{"status": []string{"paused"}}); !isErr || !strings.Contains(text, "unknown status") {
		t.Errorf("bad status = %q", text)
	}
	if text, isErr := call(t, pam, "task_create", map[string]any{"title": "x", "assignee": "quill"}); !isErr || !strings.Contains(text, "no member named") {
		t.Errorf("bad assignee = %q", text)
	}
}

func TestAssignedNote_EachKind(t *testing.T) {
	cases := map[string]string{
		"":      "anyone may claim",
		pamela:  "Assigned to you",
		"human": "operator's list",
		claude:  "claude-code picks it up",
		scout:   "scout sees it the next time",
	}
	for assignee, want := range cases {
		got := assignedNote(&task.Task{ID: 1, Assignee: assignee}, pamela)
		if !strings.Contains(got, want) {
			t.Errorf("assignee %q: note %q, want %q", assignee, got, want)
		}
	}
}
