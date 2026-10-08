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

func TestSupportsWorktreeHibernation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		envType EnvType
		want    bool
	}{
		{EnvTypeLocal, true},
		{EnvTypeLocalGitWorktree, true},
		{EnvTypeDevPod, true},
		{EnvTypeOpenShell, true},
		{EnvTypeModal, false},
		{"", true},
		{"unregistered", true},
	}
	for _, tt := range tests {
		t.Run(string(tt.envType), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, SupportsWorktreeHibernation(tt.envType))
		})
	}
}

func TestSandboxProviderRegistry_CoversSandboxEnvTypes(t *testing.T) {
	t.Parallel()
	for _, envType := range []EnvType{EnvTypeDevPod, EnvTypeOpenShell, EnvTypeModal} {
		provider, err := getSandboxProvider(envType)
		require.NoError(t, err, "env type %s", envType)
		assert.NotNil(t, provider)
	}
}
