package env

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIndependentSSHArgs(t *testing.T) {
	t.Parallel()

	in := []string{
		"-o", "ControlMaster=auto",
		"-S", "/tmp/devpod-ssh-myapp",
		"-o", "ControlPersist=3600",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=10",
		"-R", "127.0.0.1:18855:127.0.0.1:18855",
		"some-host.devpod",
		"--",
	}

	out := independentSSHArgs(in)
	joined := strings.Join(out, " ")

	assert.NotContains(t, joined, "ControlMaster")
	assert.NotContains(t, joined, "ControlPersist")
	assert.NotContains(t, joined, "ControlPath")
	assert.NotContains(t, joined, "-S")
	assert.NotContains(t, joined, "/tmp/devpod-ssh-myapp")
	assert.NotContains(t, joined, "-R")
	assert.NotContains(t, joined, "18855")

	// Non-multiplexing options and the destination must be preserved in order.
	assert.Equal(t, []string{
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=10",
		"some-host.devpod",
		"--",
	}, out)

	// Input must not be mutated.
	assert.Contains(t, in, "ControlMaster=auto")
}
