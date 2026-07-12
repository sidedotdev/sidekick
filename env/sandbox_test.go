package env

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxActivities_UnregisteredEnvType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	_, err := CreateSandboxActivity(ctx, CreateSandboxInput{EnvType: "bogus"})
	require.ErrorContains(t, err, "no sandbox provider registered")

	_, err = CheckSandboxActivity(ctx, CheckSandboxInput{EnvType: "bogus"})
	require.ErrorContains(t, err, "no sandbox provider registered")

	_, err = StopSandboxActivity(ctx, StopSandboxInput{EnvType: "bogus"})
	require.ErrorContains(t, err, "no sandbox provider registered")

	_, err = DeleteSandboxActivity(ctx, DeleteSandboxInput{EnvType: "bogus"})
	require.ErrorContains(t, err, "no sandbox provider registered")
}

func TestSandboxProviderRegistry_CoversSandboxEnvTypes(t *testing.T) {
	t.Parallel()
	for _, envType := range []EnvType{EnvTypeDevPod, EnvTypeOpenShell, EnvTypeModal} {
		provider, err := getSandboxProvider(envType)
		require.NoError(t, err, "env type %s", envType)
		assert.NotNil(t, provider)
	}
}