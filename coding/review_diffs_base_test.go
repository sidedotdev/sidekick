package coding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateReviewDiffsActivity_MovingBase(t *testing.T) {
	t.Parallel()

	for _, scenario := range reviewDiffsScenarios() {
		for _, operation := range []string{"merge", "rebase"} {
			for _, change := range []string{"insert", "delete"} {
				for _, edited := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/%s/edited=%t", scenario.name, operation, change, edited)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						ctx := context.Background()
						repoDir, envContainer := setupReviewDiffsRepo(t, ctx)
						var lines []string
						for i := 0; i < 80; i++ {
							lines = append(lines, fmt.Sprintf("base line %d\n", i))
						}
						base := strings.Join(lines, "")
						upstream := "upstream insertion\n" + base
						if change == "delete" {
							upstream = strings.Join(lines[12:], "")
						}
						runGit(t, repoDir, "checkout", "main")
						createFileAndCommit(t, repoDir, "moving.txt", base, "base content")
						runGit(t, repoDir, "checkout", "feature")
						runGit(t, repoDir, "merge", "--ff-only", "main")
						startPoint, baseBranch, _ := scenario.prepare(t, repoDir)
						feature := strings.Replace(base, "base line 60\n", "reviewed feature\n", 1)
						createFileAndCommit(t, repoDir, "moving.txt", feature, "feature edit")
						ca := &CodingActivities{}
						params := GenerateReviewDiffsParams{
							EnvContainer: envContainer,
							StartPoint:   startPoint,
							BaseBranch:   baseBranch,
						}
						reviewed, err := ca.GenerateReviewDiffsActivity(ctx, params)
						require.NoError(t, err)
						require.Contains(t, reviewed.FullDiff, "+reviewed feature")

						runGit(t, repoDir, "checkout", "main")
						createFileAndCommit(t, repoDir, "moving.txt", upstream, "upstream edit")
						runGit(t, repoDir, "checkout", "feature")
						runGit(t, repoDir, operation, "main")
						rebasedPriorTree := strings.TrimSpace(runGit(t, repoDir, "write-tree"))
						if edited {
							current := strings.Replace(upstream, "base line 60\n", "updated feature\n", 1)
							createFileAndCommit(t, repoDir, "moving.txt", current, "subsequent feature edit")
						}
						currentTree := strings.TrimSpace(runGit(t, repoDir, "write-tree"))
						params.PriorReviewDiff = reviewed.FullDiff
						params.PriorReviewSnapshot = reviewed.ReviewSnapshot
						result, err := ca.GenerateReviewDiffsActivity(ctx, params)
						require.NoError(t, err)
						require.Empty(t, result.SinceDiffError)
						if !edited {
							require.Empty(t, result.SinceDiff)
							return
						}

						require.Contains(t, result.SinceDiff, "-reviewed feature\n")
						require.Contains(t, result.SinceDiff, "+updated feature\n")
						patch := filepath.Join(t.TempDir(), "since.diff")
						require.NoError(t, os.WriteFile(patch, []byte(result.SinceDiff), 0600))
						runGit(t, repoDir, "read-tree", rebasedPriorTree)
						runGit(t, repoDir, "apply", "--cached", "--unidiff-zero", "--whitespace=nowarn", patch)
						require.Equal(t, currentTree, strings.TrimSpace(runGit(t, repoDir, "write-tree")))
					})
				}
			}
		}
	}
}

func TestGenerateReviewDiffsActivity_ReviewedEmptyDiff(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir, container := setupReviewDiffsRepo(t, ctx)
	ca := &CodingActivities{}
	params := GenerateReviewDiffsParams{EnvContainer: container, StartPoint: "main", BaseBranch: "main"}
	first, err := ca.GenerateReviewDiffsActivity(ctx, params)
	require.NoError(t, err)
	require.Empty(t, first.FullDiff)
	require.NotNil(t, first.ReviewSnapshot)

	createFileAndCommit(t, repoDir, "after-review.txt", "new work\n", "work after empty review")
	params.PriorReviewDiff = first.FullDiff
	params.PriorReviewSnapshot = first.ReviewSnapshot
	second, err := ca.GenerateReviewDiffsActivity(ctx, params)
	require.NoError(t, err)
	require.Empty(t, second.SinceDiffError)
	require.NotEmpty(t, second.FullDiff)
	require.Equal(t, second.FullDiff, second.SinceDiff)
}

func TestGenerateReviewDiffsActivity_IdenticalVisiblePinnedReviews(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir, container := setupReviewDiffsRepo(t, ctx)
	createFileAndCommit(t, repoDir, "earlier.txt", "earlier work\n", "earlier step")
	start := strings.TrimSpace(runGit(t, repoDir, "rev-parse", "HEAD"))
	createFileAndCommit(t, repoDir, "step.txt", "reviewed step\nunchanged\n", "current step")
	ca := &CodingActivities{}
	params := GenerateReviewDiffsParams{EnvContainer: container, StartPoint: start, BaseBranch: "main"}
	first, err := ca.GenerateReviewDiffsActivity(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, first.ReviewSnapshot)
	require.NotEmpty(t, first.ReviewSnapshot.ExcludedDiff)
	require.NotContains(t, first.FullDiff, "earlier.txt")
	firstSnapshot := *first.ReviewSnapshot

	runGit(t, repoDir, "checkout", "main")
	createFileAndCommit(t, repoDir, "earlier.txt", "earlier work\n", "upstream adopts earlier work")
	runGit(t, repoDir, "checkout", "feature")
	runGit(t, repoDir, "merge", "main")
	params.PriorReviewDiff, params.PriorReviewSnapshot = first.FullDiff, first.ReviewSnapshot
	second, err := ca.GenerateReviewDiffsActivity(ctx, params)
	require.NoError(t, err)
	require.Empty(t, second.SinceDiffError)
	require.Empty(t, second.SinceDiff)
	require.Equal(t, first.FullDiff, second.FullDiff)
	require.NotEqual(t, first.ReviewSnapshot.BaseCommit, second.ReviewSnapshot.BaseCommit)
	require.Empty(t, second.ReviewSnapshot.ExcludedDiff)
	require.Equal(t, firstSnapshot, *first.ReviewSnapshot)

	priorTree := strings.TrimSpace(runGit(t, repoDir, "write-tree"))
	createFileAndCommit(t, repoDir, "step.txt", "updated step\nunchanged\n", "update current step")
	wantTree := strings.TrimSpace(runGit(t, repoDir, "write-tree"))
	third, err := ca.GenerateReviewDiffsActivity(ctx, params)
	require.NoError(t, err)
	require.Empty(t, third.SinceDiffError)
	require.NotContains(t, third.FullDiff, "earlier.txt")
	require.NotContains(t, third.SinceDiff, "earlier.txt")
	require.Contains(t, third.SinceDiff, "-reviewed step\n")
	require.Contains(t, third.SinceDiff, "+updated step\n")
	patch := filepath.Join(t.TempDir(), "since.diff")
	require.NoError(t, os.WriteFile(patch, []byte(third.SinceDiff), 0600))
	runGit(t, repoDir, "read-tree", priorTree)
	runGit(t, repoDir, "apply", "--cached", "--whitespace=nowarn", patch)
	require.Equal(t, wantTree, strings.TrimSpace(runGit(t, repoDir, "write-tree")))
}
