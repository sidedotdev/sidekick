package env

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func TestCommandProvisioningRedactsBeforeTruncation(t *testing.T) {
	t.Parallel()

	token := strings.Repeat("0123456789abcdef", 4)
	total := maxActivityOutputBytes + 1024*1024
	truncated := truncateMiddle(strings.Repeat("X", total), maxActivityOutputBytes)
	boundary := strings.Index(truncated, "\n\n[... truncated")
	require.Positive(t, boundary)
	content := []byte(strings.Repeat("X", total))
	copy(content[boundary-len(token)+1:], token)
	copy(content[total-boundary-1:], token)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "output"), content, 0600))

	activities := CommandActivities{Provisioners: map[string]CommandProvisioner{
		"test": func(context.Context, CommandProvisioning) (CommandEnvironment, error) {
			return CommandEnvironment{Sensitive: []string{token}}, nil
		},
	}}
	testSuite := &testsuite.WorkflowTestSuite{}
	activityEnv := testSuite.NewTestActivityEnvironment()
	activityEnv.RegisterActivity(activities.EnvRunCommandActivity)
	result, err := activityEnv.ExecuteActivity(activities.EnvRunCommandActivity, EnvRunCommandActivityInput{
		EnvContainer: EnvContainer{Env: &LocalEnv{WorkingDirectory: dir}},
		Command:      "sh", Args: []string{"-c", "cat output; cat output >&2"},
		Provisioning: &CommandProvisioning{Kind: "test", Scope: "flow"},
	})
	require.NoError(t, err)
	var output EnvRunCommandActivityOutput
	require.NoError(t, result.Get(&output))
	for _, text := range []string{output.Stdout, output.Stderr} {
		require.LessOrEqual(t, len(text), maxActivityOutputBytes)
		require.Contains(t, text, "[... truncated")
		require.NotContains(t, text, token[:len(token)-1])
		require.NotContains(t, text, token[1:])
	}
}
