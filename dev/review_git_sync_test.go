package dev

import (
	"context"
	"errors"
	"testing"
	"time"

	"sidekick/coding/git"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/srv/sqlite"
	"sidekick/temporalmeta"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

func TestReviewAndResolveGitCheckpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		version    workflow.Version
		mergeFails bool
		canceled   bool
		syncFails  bool
		wantEvents []string
		wantErrors []string
	}{
		{
			name:       "successful merge deletes sandbox without checkpoint",
			version:    1,
			syncFails:  true,
			wantEvents: []string{"merge", "cleanup", "delete"},
		},
		{
			name:       "legacy successful merge still checkpoints",
			version:    workflow.DefaultVersion,
			syncFails:  true,
			wantEvents: []string{"merge", "cleanup", "delete", "checkpoint"},
			wantErrors: []string{"checkpoint git", "sandbox no longer exists"},
		},
		{
			name:       "failed merge checkpoints",
			version:    1,
			mergeFails: true,
			wantEvents: []string{"merge", "checkpoint"},
			wantErrors: []string{"merge unavailable"},
		},
		{
			name:       "failed merge preserves both errors",
			version:    1,
			mergeFails: true,
			syncFails:  true,
			wantEvents: []string{"merge", "checkpoint"},
			wantErrors: []string{"merge unavailable", "checkpoint git", "sandbox no longer exists"},
		},
		{
			name:       "canceled review checkpoints",
			version:    1,
			canceled:   true,
			wantEvents: []string{"checkpoint"},
			wantErrors: []string{"canceled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &AutoMergeApprovalTestSuite{}
			s.SetT(t)
			s.SetupTest()
			t.Cleanup(func() { s.env.AssertExpectations(t) })
			storage := sqlite.NewTestSqliteStorage(t, "review_git_sync")
			s.env.RegisterActivityWithOptions(storage.PersistSubflow, activity.RegisterOptions{Name: "PersistSubflow"})
			if tt.canceled {
				s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).
					Return(nil).After(time.Second).Once()
			}
			s.setupMergeMocks()

			var meta *temporalmeta.TemporalMetaActivities
			s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
				Return([]domain.TemporalActivityRef{}, nil).Maybe()
			var manager *DevAgentManagerActivities
			s.env.OnActivity(manager.GetFlowTaskTitle, mock.Anything, mock.Anything).
				Return(GetFlowTaskTitleOutput{Title: "Implement the thing"}, nil).Maybe()
			s.env.OnGetVersion("skip-successful-review-git-checkpoint", workflow.DefaultVersion, 1).
				Return(tt.version).Maybe()

			var events []string
			s.env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
				switch info.ActivityType.Name {
				case "GitAddActivity":
					if tt.canceled {
						s.env.RegisterDelayedCallback(s.env.CancelWorkflow, 0)
					}
				case "GitMergeActivity":
					events = append(events, "merge")
				case "CleanupWorktreeActivity":
					events = append(events, "cleanup")
				case "DeleteSandboxActivity":
					events = append(events, "delete")
				case "SyncGitActivity":
					events = append(events, "checkpoint")
				}
			})
			s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
				Return(func(context.Context, env.EnvContainer, git.GitMergeParams) (git.MergeActivityResult, error) {
					if tt.mergeFails {
						return git.MergeActivityResult{}, errors.New("merge unavailable")
					}
					return git.MergeActivityResult{}, nil
				}).Maybe()
			s.env.OnActivity(env.DeleteSandboxActivity, mock.Anything, mock.Anything).
				Return(env.DeleteSandboxOutput{}, nil).Maybe()
			s.env.OnActivity(git.SyncGitActivity, mock.Anything, mock.Anything).
				Return(func(context.Context, git.SyncGitInput) (git.SyncGitOutput, error) {
					if tt.syncFails {
						return git.SyncGitOutput{}, errors.New("sandbox no longer exists")
					}
					return git.SyncGitOutput{}, nil
				}).Maybe()

			s.env.ExecuteWorkflow(func(ctx workflow.Context) error {
				dCtx, err := s.mergeDevContext(ctx)
				if err != nil {
					return err
				}
				dCtx.DisableHumanInTheLoop = true
				dCtx.EnvContainer = &env.EnvContainer{Env: &env.ModalEnv{
					WorkingDirectory: "/repo/worktree",
					LocalRepoDir:     "/worker/repo",
					SandboxName:      "sandbox",
				}}
				target := "main"
				return reviewAndResolve(dCtx, MergeWithReviewParams{
					Requirements: "Implement the thing",
					StartBranch:  &target,
					AutoMerge:    true,
				})
			})

			require.True(t, s.env.IsWorkflowCompleted())
			if len(tt.wantErrors) == 0 {
				require.NoError(t, s.env.GetWorkflowError())
			} else {
				for _, message := range tt.wantErrors {
					require.ErrorContains(t, s.env.GetWorkflowError(), message)
				}
			}
			require.Equal(t, tt.wantEvents, events)
		})
	}
}
