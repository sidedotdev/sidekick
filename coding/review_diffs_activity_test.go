package coding

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func newReviewDiffsTestEnv(t *testing.T, ctx context.Context, repoDir string) env.EnvContainer {
	t.Helper()
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	require.NoError(t, err)
	return env.EnvContainer{Env: devEnv}
}

func writeAndStage(t *testing.T, repoDir, filename, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, filename), []byte(content), 0644))
	runGit(t, repoDir, "add", filename)
}

func TestGenerateReviewDiffsActivity_FirstRound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc Shared() {}\n", "initial commit")
	runGit(t, repoDir, "checkout", "-b", "feature")
	writeAndStage(t, repoDir, "feature.go", "package feature\n\nfunc Feature() {}\n")

	ca := &CodingActivities{}
	result, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
		EnvContainer: newReviewDiffsTestEnv(t, ctx, repoDir),
		StartPoint:   "main",
	})
	require.NoError(t, err)

	assert.Contains(t, result.FullDiff, "feature.go", "staged changes belong in the full diff")
	assert.Empty(t, result.SinceDiff, "no prior review means no diff since last review")
}

func TestGenerateReviewDiffsActivity_SinceLastReview(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc Shared() {}\n", "initial commit")
	runGit(t, repoDir, "checkout", "-b", "feature")
	createFileAndCommit(t, repoDir, "feature.go", "package feature\n\nfunc Feature() {}\n", "feature work")

	ca := &CodingActivities{}
	envContainer := newReviewDiffsTestEnv(t, ctx, repoDir)
	reviewed, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
		EnvContainer: envContainer,
		StartPoint:   "main",
	})
	require.NoError(t, err)
	require.Contains(t, reviewed.FullDiff, "feature.go")

	writeAndStage(t, repoDir, "feature2.go", "package feature\n\nfunc Feature2() {}\n")

	result, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
		EnvContainer:    envContainer,
		StartPoint:      "main",
		PriorReviewDiff: reviewed.FullDiff,
	})
	require.NoError(t, err)

	assert.Contains(t, result.FullDiff, "feature.go", "already-reviewed work stays in the full diff")
	assert.Contains(t, result.FullDiff, "feature2.go")
	assert.Contains(t, result.SinceDiff, "feature2.go", "new work belongs in the since diff")
	assert.NotContains(t, result.SinceDiff, "feature.go", "already-reviewed work is not new")
}

// Registration happens via the *CodingActivities struct in worker.StartWorker
// and scripts/run_activity, so the activity must be resolvable by name.
func TestGenerateReviewDiffsActivity_RegisteredByName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "shared.go", "package shared\n", "initial commit")
	runGit(t, repoDir, "checkout", "-b", "feature")
	writeAndStage(t, repoDir, "feature.go", "package feature\n")

	var testSuite testsuite.WorkflowTestSuite
	activityEnv := testSuite.NewTestActivityEnvironment()
	activityEnv.RegisterActivity(&CodingActivities{})

	encoded, err := activityEnv.ExecuteActivity("GenerateReviewDiffsActivity", GenerateReviewDiffsParams{
		EnvContainer: newReviewDiffsTestEnv(t, ctx, repoDir),
		StartPoint:   "main",
	})
	require.NoError(t, err)

	var result GenerateReviewDiffsResult
	require.NoError(t, encoded.Get(&result))
	assert.Contains(t, result.FullDiff, "feature.go")
}

func TestGenerateReviewDiffsActivity_RequiresStartPoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "shared.go", "package shared\n", "initial commit")

	ca := &CodingActivities{}
	_, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
		EnvContainer: newReviewDiffsTestEnv(t, ctx, repoDir),
	})
	assert.Error(t, err)
}
