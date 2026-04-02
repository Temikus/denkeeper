package sandbox

import (
	"context"
	"fmt"
	"os/exec"
)

// dockerCommand is the Docker CLI binary name.
// Podman is CLI-compatible and can be used as a drop-in replacement via alias.
const dockerCommand = "docker"

// DockerRuntime implements Runtime using Docker (or Podman) containers.
// Each sandbox is a `docker run -i --rm` invocation whose stdin/stdout
// carry the MCP stdio transport.
type DockerRuntime struct{}

// NewDockerRuntime creates a DockerRuntime. Returns an error if the Docker
// CLI is not found on PATH.
func NewDockerRuntime() (*DockerRuntime, error) {
	if _, err := exec.LookPath(dockerCommand); err != nil {
		return nil, fmt.Errorf("docker CLI not found on PATH: %w — install Docker or Podman to use docker plugins", err)
	}
	return &DockerRuntime{}, nil
}

// Spawn builds a `docker run -i --rm` command with security hardening and
// returns connection info. The container is interactive (-i) so stdin/stdout
// are connected to the MCP stdio transport. --rm ensures cleanup on exit.
func (d *DockerRuntime) Spawn(_ context.Context, _ string, opts SpawnOpts) (*Process, error) {
	if opts.Image == "" {
		return nil, fmt.Errorf("image is required for docker sandbox")
	}

	args := []string{"run", "--rm", "-i"}

	// Network isolation: default to "none" (no network access).
	network := string(opts.Network)
	if network == "" {
		network = "none"
	}
	args = append(args, "--network", network)

	// Resource limits.
	if opts.MemoryLimit != "" {
		args = append(args, "--memory", opts.MemoryLimit)
	}
	if opts.CPULimit != "" {
		args = append(args, "--cpus", opts.CPULimit)
	}

	// Security hardening: drop all capabilities, read-only root filesystem.
	args = append(args, "--cap-drop", "ALL")
	args = append(args, "--read-only")
	args = append(args, "--security-opt", "no-new-privileges")

	// Tmpfs mounts (e.g. for /tmp in read-only containers).
	for _, t := range opts.Tmpfs {
		args = append(args, "--tmpfs", t)
	}

	// Shared memory size (e.g. for Chromium).
	if opts.ShmSize != "" {
		args = append(args, "--shm-size", opts.ShmSize)
	}

	// Bind mounts.
	for _, v := range opts.Volumes {
		args = append(args, "-v", v)
	}

	// Environment variables.
	for k, v := range opts.Env {
		args = append(args, "-e", k+"="+v)
	}

	// Image.
	args = append(args, opts.Image)

	// Entrypoint override: if Command is set, use it as the entrypoint.
	// Otherwise, the image's default ENTRYPOINT/CMD is used.
	if opts.Command != "" {
		args = append(args, opts.Command)
	}
	args = append(args, opts.Args...)

	return &Process{
		Command: dockerCommand,
		Args:    args,
	}, nil
}

// Stop is a no-op for Docker — the --rm flag ensures container cleanup
// when the process exits (triggered by closing the MCP session's stdio).
func (d *DockerRuntime) Stop(_ context.Context, _ string) error {
	return nil
}

// Close is a no-op for Docker — there are no persistent runtime resources.
func (d *DockerRuntime) Close() error {
	return nil
}
