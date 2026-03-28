package plugin

import (
	"fmt"
	"os/exec"
)

// dockerCommand is the name of the Docker CLI binary.
// Podman is CLI-compatible and can be used as a drop-in replacement via alias.
const dockerCommand = "docker"

// buildDockerArgs constructs the arguments for a `docker run -i --rm` command
// that spawns an MCP server inside a container with stdio transport.
//
// The container runs interactively (-i) so stdin/stdout are connected to the
// MCP stdio transport. --rm ensures cleanup on exit.
func buildDockerArgs(p Plugin) []string {
	args := []string{"run", "--rm", "-i"}

	// Network isolation: default to "none" (no network access).
	network := p.Network
	if network == "" {
		network = "none"
	}
	args = append(args, "--network", network)

	// Resource limits.
	if p.MemoryLimit != "" {
		args = append(args, "--memory", p.MemoryLimit)
	}
	if p.CPULimit != "" {
		args = append(args, "--cpus", p.CPULimit)
	}

	// Security hardening: drop all capabilities, read-only root filesystem.
	args = append(args, "--cap-drop", "ALL")
	args = append(args, "--read-only")
	args = append(args, "--security-opt", "no-new-privileges")

	// Bind mounts.
	for _, v := range p.Volumes {
		args = append(args, "-v", v)
	}

	// Environment variables.
	for k, v := range p.Env {
		args = append(args, "-e", k+"="+v)
	}

	// Image.
	args = append(args, p.Image)

	// Entrypoint override: if Command is set, use it as the entrypoint.
	// Otherwise, the image's default ENTRYPOINT/CMD is used.
	if p.Command != "" {
		args = append(args, p.Command)
	}
	args = append(args, p.Args...)

	return args
}

// checkDockerAvailable verifies that the Docker CLI is on PATH.
func checkDockerAvailable() error {
	_, err := exec.LookPath(dockerCommand)
	if err != nil {
		return fmt.Errorf("docker CLI not found on PATH: %w — install Docker or Podman to use docker plugins", err)
	}
	return nil
}
