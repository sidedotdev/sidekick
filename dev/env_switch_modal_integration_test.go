package dev

import (
	"context"
	"os"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/env"
)

func TestModalEnvSwitchRoundTrip(t *testing.T) {
	if testing.Short() || os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping Modal e2e test; requires SIDE_E2E_TEST=true without -short")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping Modal e2e test; credentials are unavailable in non-local sidekick environments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := env.CheckSandboxActivity(ctx, env.CheckSandboxInput{
		EnvType: env.EnvTypeModal, SandboxName: "side-e2e-credential-probe",
	}); err != nil {
		t.Skipf("modal credentials not configured or Modal unreachable: %v", err)
	}
	runEnvSwitchRoundTrip(t, env.EnvTypeModal, nil)
}
