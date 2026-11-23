package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
)

const skillIndexReloadBase = `[telegram]
token = "123456:ABC-DEF"
allowed_users = [111222333]

[llm.openrouter]
api_key = "sk-or-test-key"
`

// skillIndexReloadFixture runs engines "a" and "b" and returns a reload func
// bound to a TOML file, plus a writer for that file. extra goes after the
// base; agentA is appended to agent a's entry.
func skillIndexReloadFixture(t *testing.T) (*agent.Dispatcher, func() error, func(extra, agentA string)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "denkeeper.toml")
	write := func(extra, agentA string) {
		t.Helper()
		body := skillIndexReloadBase + extra +
			"\n[[agents]]\nname = \"a\"\npersona_dir = \"/agents/a\"\nadapters = [\"telegram\"]\n" + agentA +
			"\n[[agents]]\nname = \"b\"\npersona_dir = \"/agents/b\"\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("", "")

	engines := map[string]*agent.Engine{"a": newReloadEngine("a"), "b": newReloadEngine("b")}
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

func TestReload_SkillIndexGlobalOffThenOn(t *testing.T) {
	disp, reload, write := skillIndexReloadFixture(t)

	write("\n[skills]\nindex = false\n", "")
	mustReload(t, reload)
	if disp.Agent("a").SkillIndexEnabled() || disp.Agent("b").SkillIndexEnabled() {
		t.Fatal("[skills] index = false left an agent's index on")
	}

	write("", "")
	mustReload(t, reload)
	if !disp.Agent("a").SkillIndexEnabled() || !disp.Agent("b").SkillIndexEnabled() {
		t.Fatal("removing [skills] index left an agent's index off")
	}
}

func TestReload_SkillIndexOneAgentOff(t *testing.T) {
	disp, reload, write := skillIndexReloadFixture(t)

	write("", "skill_index = false\n")
	mustReload(t, reload)
	if disp.Agent("a").SkillIndexEnabled() {
		t.Fatal("agent a's skill_index = false not applied")
	}
	if !disp.Agent("b").SkillIndexEnabled() {
		t.Fatal("agent b's index turned off by a's setting")
	}
}
