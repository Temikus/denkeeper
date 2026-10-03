package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
)

const supervisorReloadBase = `[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm.openrouter]
api_key = "sk-or-test-key"

[[agents]]
name = "guard"
persona_dir = "/agents/guard"
session_tier = "autonomous"

[[agents]]
name = "guard2"
persona_dir = "/agents/guard2"
session_tier = "autonomous"
`

func newReloadEngine(name string) *agent.Engine {
	return agent.NewEngine(name, nil, nil, nil, nil, nil, "", nil, nil, nil, slog.Default())
}

// supervisorReloadFixture runs worker, guard and guard2 engines and returns a
// reload func bound to a TOML file, plus a writer that rewrites the worker's
// [[agents]] entry (appended after the shared base).
func supervisorReloadFixture(t *testing.T) (*agent.Dispatcher, func() error, func(worker string)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "denkeeper.toml")
	write := func(worker string) {
		t.Helper()
		body := supervisorReloadBase + "\n[[agents]]\nname = \"worker\"\npersona_dir = \"/agents/worker\"\nadapters = [\"telegram\"]\nsession_tier = \"supervised\"\n" + worker
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("")

	engines := map[string]*agent.Engine{}
	for _, n := range []string{"worker", "guard", "guard2"} {
		engines[n] = newReloadEngine(n)
	}
	disp := agent.NewDispatcher(engines, nil, nil, slog.Default())

	store, err := approval.NewInMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("setup config: %v", err)
	}
	reload := buildReloadFunc(path, config.NewHolder(cfg), disp, approval.NewManager(store, slog.Default()), nil, reloadRuntime{}, slog.Default())
	return disp, reload, write
}

func mustReload(t *testing.T, reload func() error) {
	t.Helper()
	if err := reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
}

func TestReload_SupervisorAdded(t *testing.T) {
	disp, reload, write := supervisorReloadFixture(t)

	write(`supervisor = "guard"` + "\n")
	mustReload(t, reload)

	if got := disp.Agent("worker").Supervisor(); got != disp.Agent("guard") {
		t.Errorf("worker supervisor = %v, want guard", got)
	}
}

func TestReload_SupervisorChanged(t *testing.T) {
	disp, reload, write := supervisorReloadFixture(t)
	write(`supervisor = "guard"` + "\n")
	mustReload(t, reload)

	write(`supervisor = "guard2"` + "\n")
	mustReload(t, reload)

	if got := disp.Agent("worker").Supervisor(); got != disp.Agent("guard2") {
		t.Errorf("worker supervisor = %v, want guard2", got)
	}
}

func TestReload_SupervisorRemoved(t *testing.T) {
	disp, reload, write := supervisorReloadFixture(t)
	write(`supervisor = "guard"` + "\n")
	mustReload(t, reload)

	write("")
	mustReload(t, reload)

	if got := disp.Agent("worker").Supervisor(); got != nil {
		t.Errorf("worker supervisor = %v after removal, want nil", got)
	}
}

// A supervisor in the config but not running (it failed to build) must not
// leave the previous one reviewing calls.
func TestReconcileSupervisor_NotRunningClears(t *testing.T) {
	disp := agent.NewDispatcher(map[string]*agent.Engine{
		"worker": newReloadEngine("worker"),
		"guard":  newReloadEngine("guard"),
	}, nil, nil, slog.Default())
	worker := disp.Agent("worker")
	worker.SetSupervisor(disp.Agent("guard"))

	reconcileSupervisor(worker, config.AgentInstanceConfig{Name: "worker", Supervisor: "absent"}, disp, slog.Default())

	if got := worker.Supervisor(); got != nil {
		t.Errorf("worker supervisor = %v, want nil when the named one is not running", got)
	}
}

func TestReload_SupervisorKnobsClearedRestoreDefaults(t *testing.T) {
	disp, reload, write := supervisorReloadFixture(t)
	worker := disp.Agent("worker")
	defaults := [4]int64{int64(worker.SupervisorTimeout()), int64(worker.SupervisorContextMessages()), int64(worker.SupervisorBodyExcerptLen()), int64(worker.SupervisorToolDescLen())}

	write("supervisor = \"guard\"\nsupervisor_timeout = \"7s\"\nsupervisor_context_messages = 9\nsupervisor_body_excerpt_len = 900\nsupervisor_tool_desc_len = 90\n")
	mustReload(t, reload)
	if got := worker.SupervisorTimeout(); got != 7*time.Second {
		t.Fatalf("after override: timeout = %v, want 7s", got)
	}
	if got := worker.SupervisorToolDescLen(); got != 90 {
		t.Fatalf("after override: tool_desc_len = %d, want 90", got)
	}

	write(`supervisor = "guard"` + "\n")
	mustReload(t, reload)

	got := [4]int64{int64(worker.SupervisorTimeout()), int64(worker.SupervisorContextMessages()), int64(worker.SupervisorBodyExcerptLen()), int64(worker.SupervisorToolDescLen())}
	if got != defaults {
		t.Errorf("after clearing: knobs = %v, want defaults %v", got, defaults)
	}
}
