package coding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// reviewDiffsScenario covers one of the two kinds of start point the review
// diffs support: a base branch, as the basic dev and review/resolve flows use,
// and a commit pinned at the start of a planned dev step.
type reviewDiffsScenario struct {
	name string

	// prepare creates whatever history precedes the start point on the feature
	// branch, returning the start point, the base branch to exclude merged-in
	// changes with, and a marker only found in changes made before the start
	// point.
	prepare func(t *testing.T, repoDir string) (startPoint, baseBranch, preStartPointMarker string)
}

func reviewDiffsScenarios() []reviewDiffsScenario {
	return []reviewDiffsScenario{
		{
			name: "base branch start point",
			prepare: func(t *testing.T, repoDir string) (string, string, string) {
				return "main", "", "PreExisting"
			},
		},
		{
			name: "pinned step start point",
			prepare: func(t *testing.T, repoDir string) (string, string, string) {
				createFileAndCommit(t, repoDir, "previous_step.go", "package feature\n\nfunc PreviousStep() {}\n", "previous step work")
				pinned := strings.TrimSpace(runGit(t, repoDir, "rev-parse", "HEAD"))
				return pinned, "main", "PreviousStep"
			},
		},
	}
}

// setupReviewDiffsRepo creates a repo with a main branch and a feature branch
// checked out, mirroring how flows work on their own branch.
func setupReviewDiffsRepo(t *testing.T, ctx context.Context) (string, env.EnvContainer) {
	t.Helper()
	repoDir := setupTestGitRepo(t)
	createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc PreExisting() {}\n", "initial commit")
	runGit(t, repoDir, "checkout", "-b", "feature")
	return repoDir, newReviewDiffsTestEnv(t, ctx, repoDir)
}

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

func TestGenerateReviewDiffsActivity_StagedChangesAlwaysIncluded(t *testing.T) {
	t.Parallel()

	for _, scenario := range reviewDiffsScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			repoDir, envContainer := setupReviewDiffsRepo(t, ctx)
			startPoint, baseBranch, preStartPointMarker := scenario.prepare(t, repoDir)
			ca := &CodingActivities{}

			writeAndStage(t, repoDir, "staged_before.go", "package feature\n\nfunc StagedBeforeReview() {}\n")
			reviewed, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
				EnvContainer: envContainer,
				StartPoint:   startPoint,
				BaseBranch:   baseBranch,
			})
			require.NoError(t, err)
			require.Contains(t, reviewed.FullDiff, "StagedBeforeReview")

			writeAndStage(t, repoDir, "staged_after.go", "package feature\n\nfunc StagedAfterReview() {}\n")
			result, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
				EnvContainer:    envContainer,
				StartPoint:      startPoint,
				BaseBranch:      baseBranch,
				PriorReviewDiff: reviewed.FullDiff,
			})
			require.NoError(t, err)

			assert.Contains(t, result.FullDiff, "StagedBeforeReview")
			assert.Contains(t, result.FullDiff, "StagedAfterReview")
			assert.Contains(t, result.SinceDiff, "StagedAfterReview")
			assert.NotContains(t, result.SinceDiff, "StagedBeforeReview", "already reviewed staged changes are not new")
			assert.NotContains(t, result.FullDiff, preStartPointMarker, "changes before the start point are not ours to review")
			assert.NotContains(t, result.SinceDiff, preStartPointMarker)
		})
	}
}

func TestGenerateReviewDiffsActivity_CommitsRelativeToStartPointAndReview(t *testing.T) {
	t.Parallel()

	for _, scenario := range reviewDiffsScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			repoDir, envContainer := setupReviewDiffsRepo(t, ctx)
			startPoint, baseBranch, preStartPointMarker := scenario.prepare(t, repoDir)
			ca := &CodingActivities{}

			createFileAndCommit(t, repoDir, "reviewed.go", "package feature\n\nfunc ReviewedWork() {}\n", "reviewed work")
			reviewed, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
				EnvContainer: envContainer,
				StartPoint:   startPoint,
				BaseBranch:   baseBranch,
			})
			require.NoError(t, err)
			require.Contains(t, reviewed.FullDiff, "ReviewedWork")

			createFileAndCommit(t, repoDir, "later.go", "package feature\n\nfunc LaterWork() {}\n", "later work")
			result, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
				EnvContainer:    envContainer,
				StartPoint:      startPoint,
				BaseBranch:      baseBranch,
				PriorReviewDiff: reviewed.FullDiff,
			})
			require.NoError(t, err)

			assert.Contains(t, result.FullDiff, "ReviewedWork")
			assert.Contains(t, result.FullDiff, "LaterWork")
			assert.Contains(t, result.SinceDiff, "LaterWork")
			assert.NotContains(t, result.SinceDiff, "ReviewedWork", "commits made before the last review are not new")
			assert.NotContains(t, result.FullDiff, preStartPointMarker, "commits made before the start point are in neither diff")
			assert.NotContains(t, result.SinceDiff, preStartPointMarker)
		})
	}
}

func TestGenerateReviewDiffsActivity_CleanBaseMergeDoesNotAffectDiffs(t *testing.T) {
	t.Parallel()

	for _, scenario := range reviewDiffsScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			repoDir, envContainer := setupReviewDiffsRepo(t, ctx)
			startPoint, baseBranch, preStartPointMarker := scenario.prepare(t, repoDir)
			ca := &CodingActivities{}

			createFileAndCommit(t, repoDir, "ours.go", "package feature\n\nfunc OurWork() {}\n", "our work")
			reviewed, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
				EnvContainer: envContainer,
				StartPoint:   startPoint,
				BaseBranch:   baseBranch,
			})
			require.NoError(t, err)
			require.Contains(t, reviewed.FullDiff, "OurWork")

			runGit(t, repoDir, "checkout", "main")
			createFileAndCommit(t, repoDir, "upstream.go", "package shared\n\nfunc UpstreamWork() {}\n", "upstream work")
			runGit(t, repoDir, "checkout", "feature")
			runGit(t, repoDir, "merge", "main", "-m", "merge main")

			result, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
				EnvContainer:    envContainer,
				StartPoint:      startPoint,
				BaseBranch:      baseBranch,
				PriorReviewDiff: reviewed.FullDiff,
			})
			require.NoError(t, err)

			assert.Equal(t, reviewed.FullDiff, result.FullDiff, "a clean base branch merge leaves the full diff untouched")
			assert.Contains(t, result.FullDiff, "OurWork")
			assert.NotContains(t, result.FullDiff, "UpstreamWork", "merging the base branch in does not make its changes ours")
			assert.NotContains(t, result.FullDiff, preStartPointMarker)
			assert.Empty(t, strings.TrimSpace(result.SinceDiff), "a clean base branch merge changes nothing since the last review")
		})
	}
}

func TestGenerateReviewDiffsActivity_ConflictResolvingBaseMerge(t *testing.T) {
	t.Parallel()

	for _, scenario := range reviewDiffsScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			repoDir, envContainer := setupReviewDiffsRepo(t, ctx)
			startPoint, baseBranch, preStartPointMarker := scenario.prepare(t, repoDir)
			ca := &CodingActivities{}

			createFileAndCommit(t, repoDir, "contended.go", "package shared\n\nfunc Contended() { ours() }\n", "our change to the contended file")
			reviewed, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
				EnvContainer: envContainer,
				StartPoint:   startPoint,
				BaseBranch:   baseBranch,
			})
			require.NoError(t, err)
			require.Contains(t, reviewed.FullDiff, "ours()")

			runGit(t, repoDir, "checkout", "main")
			createFileAndCommit(t, repoDir, "contended.go", "package shared\n\nfunc Contended() { theirs() }\n", "their change to the contended file")
			createFileAndCommit(t, repoDir, "upstream.go", "package shared\n\nfunc UpstreamWork() {}\n", "upstream work")
			runGit(t, repoDir, "checkout", "feature")

			mergeOutput := runGitAllowFailure(t, repoDir, "merge", "main")
			require.Contains(t, mergeOutput, "CONFLICT", "the merge is expected to conflict")

			writeAndStage(t, repoDir, "contended.go", "package shared\n\nfunc Contended() { ours(); theirs() }\n")
			runGit(t, repoDir, "commit", "--no-edit")

			result, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
				EnvContainer:    envContainer,
				StartPoint:      startPoint,
				BaseBranch:      baseBranch,
				PriorReviewDiff: reviewed.FullDiff,
			})
			require.NoError(t, err)

			assert.Contains(t, result.FullDiff, "ours(); theirs()", "our end of the conflict resolution is ours to review")
			assert.NotContains(t, result.FullDiff, "UpstreamWork", "non-conflicting base branch changes are not ours")
			assert.NotContains(t, result.FullDiff, preStartPointMarker)
			assert.Contains(t, result.SinceDiff, "ours(); theirs()", "the resolution happened since the last review")
			assert.NotContains(t, result.SinceDiff, "UpstreamWork")
		})
	}
}

// Each round's full diff becomes the next round's prior review diff, so it has
// to survive being fed back in, including when it was rendered rather than
// taken straight from git.
func TestGenerateReviewDiffsActivity_SuccessiveReviewRounds(t *testing.T) {
	t.Parallel()

	for _, scenario := range reviewDiffsScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			repoDir, envContainer := setupReviewDiffsRepo(t, ctx)
			startPoint, baseBranch, preStartPointMarker := scenario.prepare(t, repoDir)
			ca := &CodingActivities{}

			review := func(priorReviewDiff string) GenerateReviewDiffsResult {
				t.Helper()
				result, err := ca.GenerateReviewDiffsActivity(ctx, GenerateReviewDiffsParams{
					EnvContainer:    envContainer,
					StartPoint:      startPoint,
					BaseBranch:      baseBranch,
					PriorReviewDiff: priorReviewDiff,
				})
				require.NoError(t, err)
				return result
			}

			createFileAndCommit(t, repoDir, "first.go", "package feature\n\nfunc FirstRound() {}\n", "first round")
			first := review("")
			require.Contains(t, first.FullDiff, "FirstRound")

			createFileAndCommit(t, repoDir, "second.go", "package feature\n\nfunc SecondRound() {}\n", "second round")
			second := review(first.FullDiff)
			require.Contains(t, second.SinceDiff, "SecondRound")

			writeAndStage(t, repoDir, "third.go", "package feature\n\nfunc ThirdRound() {}\n")
			third := review(second.FullDiff)

			assert.Contains(t, third.FullDiff, "FirstRound")
			assert.Contains(t, third.FullDiff, "SecondRound")
			assert.Contains(t, third.FullDiff, "ThirdRound")
			assert.Contains(t, third.SinceDiff, "ThirdRound")
			assert.NotContains(t, third.SinceDiff, "FirstRound")
			assert.NotContains(t, third.SinceDiff, "SecondRound")
			assert.NotContains(t, third.FullDiff, preStartPointMarker)
		})
	}
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
