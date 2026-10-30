package dev

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"sidekick/env"
)

// In a Modal development sandbox with vm: true, install Docker with
// curl -fsSL https://get.docker.com | sh and start dockerd. Install DevPod from
// https://github.com/loft-sh/devpod/releases/latest/download/devpod-linux-amd64
// on PATH. No SIDE_E2E_TEST override is needed for this local Docker test.
func TestDevPodEnvSwitchRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real Docker/Temporal round trip in short mode")
	}
	for _, command := range []string{"docker", "devpod"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is not on PATH", command)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "docker", "info").CombinedOutput(); err != nil {
		t.Skipf("Docker is unavailable: %v\n%s", err, output)
	}

	runEnvSwitchRoundTrip(t, env.EnvTypeDevPod, nil)
}
