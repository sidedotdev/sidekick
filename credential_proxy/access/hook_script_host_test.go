package access

import (
	"context"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func TestHookScriptHostProvidesAcquireAccess(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	environment := suite.NewTestActivityEnvironment()
	run := func(ctx context.Context) ([]string, error) {
		host, err := HookScriptHost{}.HookScriptHost(ctx, env.CommandHookContext{
			WorkspaceId: "workspace",
			RepoConfig:  testInput().RepoConfig,
		})
		if err != nil {
			return nil, err
		}
		var names []string
		for namespace, operations := range host {
			for name, operation := range operations {
				if operation != nil {
					names = append(names, namespace+"."+name)
				}
			}
		}
		return names, nil
	}
	environment.RegisterActivity(run)
	value, err := environment.ExecuteActivity(run)
	require.NoError(t, err)
	var names []string
	require.NoError(t, value.Get(&names))
	require.Equal(t, []string{"credentialProxies.acquireAccess"}, names)
}

func TestHookScriptHostRequiresWorkspace(t *testing.T) {
	t.Parallel()
	_, err := HookScriptHost{}.HookScriptHost(context.Background(), env.CommandHookContext{
		RepoConfig: testInput().RepoConfig,
	})
	require.Error(t, err)
}
