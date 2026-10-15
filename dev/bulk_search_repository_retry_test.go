package dev

import (
	"context"
	"fmt"
	"testing"

	"sidekick/env"
	"sidekick/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestBulkSearchRepositoryActivityRetry(t *testing.T) {
	for _, tt := range []struct {
		name         string
		version      workflow.Version
		recover      bool
		nonRetryable bool
		wantAttempts int
		wantSuccess  bool
	}{
		{name: "transient disconnect", version: 1, recover: true, wantAttempts: 2, wantSuccess: true},
		{name: "persistent disconnect", version: 1, wantAttempts: 2},
		{name: "non-retryable failure", version: 1, nonRetryable: true, wantAttempts: 1},
		{name: "historical no retry", version: workflow.DefaultVersion, recover: true, wantAttempts: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.OnGetVersion("bulk-search-activity-retry", workflow.DefaultVersion, 1).Return(tt.version)
			attempts := 0
			testEnv.OnActivity(BulkSearchRepositoryActivity, mock.Anything, mock.Anything).
				Return(func(context.Context, BulkSearchRepositoryActivityInput) (BulkSearchRepositoryActivityOutput, error) {
					attempts++
					if tt.recover && attempts > 1 {
						return BulkSearchRepositoryActivityOutput{Result: "file.go:3:match"}, nil
					}
					if tt.nonRetryable {
						return BulkSearchRepositoryActivityOutput{}, temporal.NewNonRetryableApplicationError("invalid search", "invalidSearch", nil)
					}
					return BulkSearchRepositoryActivityOutput{}, fmt.Errorf("failed to create core ignore dir: mkdirall .side/tmp: connection lost")
				})

			testEnv.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
				return BulkSearchRepository(utils.NoRetryCtx(ctx), env.EnvContainer{}, BulkSearchRepositoryParams{
					Searches: []SingleSearchParams{{PathGlob: "**/*.go", SearchTerm: "match"}},
				})
			})
			require.True(t, testEnv.IsWorkflowCompleted())
			assert.Equal(t, tt.wantAttempts, attempts)
			if tt.wantSuccess {
				require.NoError(t, testEnv.GetWorkflowError())
				var result string
				require.NoError(t, testEnv.GetWorkflowResult(&result))
				assert.Equal(t, "file.go:3:match", result)
			} else {
				require.Error(t, testEnv.GetWorkflowError())
			}
		})
	}
}
