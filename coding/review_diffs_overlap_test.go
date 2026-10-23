package coding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateReviewDiffsActivity_OverlappingBaseEdits(t *testing.T) {
	t.Parallel()

	base := `<template>
  <form>
    <div v-if="requestContent" class="markdown-container">
      <p>{{ requestContent }}</p>
    </div>
  </form>
</template>
`
	prior := strings.Replace(base, `    <div v-if="requestContent"`, `    <div v-if="isRateLimited" class="rate-limit-notice">
      <p>{{ rateLimitCopy }}</p>
    </div>
    <div v-else-if="requestContent"`, 1)
	addStyle := func(content string) string {
		return strings.Replace(content, `class="markdown-container">`,
			`class="markdown-container" :style="{ '--level': level }">`, 1)
	}
	upstream, combined := addStyle(base), addStyle(prior)

	for _, tc := range []struct {
		name    string
		current string
	}{
		{name: "unchanged feature", current: combined},
		{name: "revised feature", current: strings.Replace(combined, "rateLimitCopy", "updatedRateLimitCopy", 1)},
		{name: "reverted upstream attribute", current: prior},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			repoDir, envContainer := setupReviewDiffsRepo(t, ctx)
			runGit(t, repoDir, "checkout", "main")
			createFileAndCommit(t, repoDir, "component.vue", base, "base component")
			runGit(t, repoDir, "checkout", "feature")
			runGit(t, repoDir, "merge", "--ff-only", "main")
			createFileAndCommit(t, repoDir, "component.vue", prior, "reviewed feature")

			ca := &CodingActivities{}
			params := GenerateReviewDiffsParams{
				EnvContainer: envContainer,
				StartPoint:   "main",
				BaseBranch:   "main",
			}
			reviewed, err := ca.GenerateReviewDiffsActivity(ctx, params)
			require.NoError(t, err)
			require.Empty(t, reviewed.SinceDiffError)
			require.NotEmpty(t, reviewed.FullDiff)

			runGit(t, repoDir, "checkout", "main")
			createFileAndCommit(t, repoDir, "component.vue", upstream, "upstream attribute")
			runGit(t, repoDir, "checkout", "feature")
			runGit(t, repoDir, "merge", "--no-commit", "-s", "ours", "main")
			writeAndStage(t, repoDir, "component.vue", combined)
			runGit(t, repoDir, "commit", "-m", "resolve component overlap")
			rebasedPriorTree := strings.TrimSpace(runGit(t, repoDir, "write-tree"))
			if tc.current != combined {
				createFileAndCommit(t, repoDir, "component.vue", tc.current, "subsequent edit")
			}
			currentTree := strings.TrimSpace(runGit(t, repoDir, "write-tree"))

			params.PriorReviewDiff = reviewed.FullDiff
			params.PriorReviewSnapshot = reviewed.ReviewSnapshot
			result, err := ca.GenerateReviewDiffsActivity(ctx, params)
			require.NoError(t, err)
			require.Empty(t, result.SinceDiffError)
			if tc.current == combined {
				require.Empty(t, result.SinceDiff)
			} else {
				require.NotEmpty(t, result.SinceDiff)
				patch := filepath.Join(t.TempDir(), "since.diff")
				require.NoError(t, os.WriteFile(patch, []byte(result.SinceDiff), 0600))
				runGit(t, repoDir, "read-tree", rebasedPriorTree)
				runGit(t, repoDir, "apply", "--cached", "--unidiff-zero", "--whitespace=nowarn", patch)
			}
			require.Equal(t, currentTree, strings.TrimSpace(runGit(t, repoDir, "write-tree")))
		})
	}
}
