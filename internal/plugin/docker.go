package plugin

import (
	"fmt"
	"os/exec"
)

// dockerCommand is the name of the Docker CLI binary.
// Podman is CLI-compatible and can be used as a drop-in replacement via alias.
const dockerCommand = "docker"

// checkDockerAvailable verifies that the Docker CLI is on PATH.
func checkDockerAvailable() error {
	_, err := exec.LookPath(dockerCommand)
	if err != nil {
		return fmt.Errorf("docker CLI not found on PATH: %w — install Docker or Podman to use docker plugins", err)
	}
	return nil
}
