package config

import "testing"

func TestTasks_OffByDefault(t *testing.T) {
	cfg, err := Parse(deciderConfig(""))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Tasks.Enabled {
		t.Error("tasks enabled with no [tasks] section; want off")
	}
}

func TestTasks_EnabledFromTOML(t *testing.T) {
	cfg, err := Parse(deciderConfig("\n[tasks]\nenabled = true\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.Tasks.Enabled {
		t.Error("[tasks] enabled = true did not turn tasks on")
	}
}
