package sandbox

import (
	"context"
	"testing"
)

func TestDockerRuntime_Spawn_BasicArgs(t *testing.T) {
	rt := &DockerRuntime{}

	proc, err := rt.Spawn(context.Background(), "test-plugin", SpawnOpts{
		Image: "my-image:v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if proc.Command != "docker" {
		t.Errorf("command = %q, want docker", proc.Command)
	}

	want := []string{"run", "--rm", "-i",
		"--network", "none",
		"--cap-drop", "ALL",
		"--read-only",
		"--security-opt", "no-new-privileges",
		"my-image:v1",
	}
	assertArgs(t, proc.Args, want)
}

func TestDockerRuntime_Spawn_AllOptions(t *testing.T) {
	rt := &DockerRuntime{}

	proc, err := rt.Spawn(context.Background(), "full-plugin", SpawnOpts{
		Image:       "registry/plugin:latest",
		Command:     "/usr/bin/server",
		Args:        []string{"--port", "8080"},
		MemoryLimit: "256m",
		CPULimit:    "0.5",
		Network:     NetworkEgress,
		Volumes:     []string{"/data:/mnt/data:ro"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, proc.Args, "--memory", "256m")
	assertContains(t, proc.Args, "--cpus", "0.5")
	assertContains(t, proc.Args, "--network", "egress")
	assertContains(t, proc.Args, "-v", "/data:/mnt/data:ro")

	// Image, command, and args should appear at the end.
	last3 := proc.Args[len(proc.Args)-3:]
	if last3[0] != "/usr/bin/server" || last3[1] != "--port" || last3[2] != "8080" {
		t.Errorf("command and args not at end: %v", proc.Args)
	}
}

func TestDockerRuntime_Spawn_EnvVars(t *testing.T) {
	rt := &DockerRuntime{}

	proc, err := rt.Spawn(context.Background(), "env-plugin", SpawnOpts{
		Image: "img:v1",
		Env:   map[string]string{"API_KEY": "secret"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, proc.Args, "-e", "API_KEY=secret")
}

func TestDockerRuntime_Spawn_MissingImage(t *testing.T) {
	rt := &DockerRuntime{}

	_, err := rt.Spawn(context.Background(), "bad", SpawnOpts{})
	if err == nil {
		t.Fatal("expected error for missing image")
	}
}

func TestDockerRuntime_Spawn_DefaultNetwork(t *testing.T) {
	rt := &DockerRuntime{}

	proc, err := rt.Spawn(context.Background(), "net-test", SpawnOpts{
		Image: "img:v1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, proc.Args, "--network", "none")
}

func TestDockerRuntime_StopAndClose(t *testing.T) {
	rt := &DockerRuntime{}

	if err := rt.Stop(context.Background(), "anything"); err != nil {
		t.Errorf("Stop returned error: %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

// assertArgs checks that got matches want exactly.
func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("args length %d != %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// assertContains checks that args contains flag followed by value.
func assertContains(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return
		}
	}
	t.Errorf("args missing %s %s: %v", flag, value, args)
}
