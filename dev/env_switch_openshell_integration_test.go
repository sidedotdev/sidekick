package dev

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sidekick/common"
	"sidekick/env"

	"github.com/stretchr/testify/require"
)

func TestOpenShellEnvSwitchRoundTrip(t *testing.T) {
	if testing.Short() || os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping OpenShell e2e test; requires SIDE_E2E_TEST=true without -short")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping OpenShell e2e test; container-in-container is not supported in non-local sidekick environments")
	}
	if _, err := exec.LookPath("openshell"); err != nil {
		t.Skip("openshell command not found in PATH")
	}
	dockerfile, err := filepath.Abs(filepath.Join("..", "env", "testdata", "Dockerfile.openshell-ripgrep"))
	require.NoError(t, err)
	runEnvSwitchRoundTrip(t, env.EnvTypeOpenShell, func(config *common.RepoConfig) {
		config.OpenShellConfig.From = dockerfile
	})
}
