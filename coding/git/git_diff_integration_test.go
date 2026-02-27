package git

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/coding/diffanalysis"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFilterDiffForReview_Integration exercises the full pipeline:
// real git repo → GitDiffActivity → FilterDiffForReview, confirming that
// merge-introduced changes are correctly filtered after merging the base
// branch into the feature branch.
func TestFilterDiffForReview_Integration(t *testing.T) {
	t.Parallel()

	t.Run("after_merge_base_into_feature", func(t *testing.T) {
		t.Parallel()

		// Setup:
		//   main: initial → baseChange (adds base_file.go) → new_main_file.go
		//   feature: initial → featureWork → merge(main) → [review] → merge(main again)
		//
		// new_main_file.go shows up in sinceReviewDiff (tree changed since review)
		// but NOT in branchDiff (three-dot), so it should be filtered out.

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc Shared() {}\n", "initial commit")

		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		createFileAndCommit(t, repoDir, "feature.go", "package feature\n\nfunc Feature() {}\n", "our feature work")

		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		createFileAndCommit(t, repoDir, "base_file.go", "package base\n\nfunc Base() {}\n", "base branch addition")

		runGitCommandInTestRepo(t, repoDir, "checkout", "feature")
		runGitCommandInTestRepo(t, repoDir, "merge", "main", "-m", "merge main into feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		createFileAndCommit(t, repoDir, "new_main_file.go", "package newmain\n\nfunc NewMain() {}\n", "new main file")

		runGitCommandInTestRepo(t, repoDir, "checkout", "feature")
		runGitCommandInTestRepo(t, repoDir, "merge", "main", "-m", "merge main again")

		sinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:  true,
			BaseRef: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("sinceReviewDiff:\n%s", sinceReviewDiff)

		assert.Contains(t, sinceReviewDiff, "new_main_file.go",
			"since-review diff should show merge-introduced file")

		zeroCtx := 0
		branchDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:       true,
			ThreeDotDiff: true,
			BaseRef:      "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("branchDiff (three-dot):\n%s", branchDiff)

		assert.Contains(t, branchDiff, "feature.go", "three-dot should include our work")
		assert.NotContains(t, branchDiff, "new_main_file.go",
			"three-dot should NOT include merge-introduced files")

		baseSinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			BaseRef:      lastReviewTreeHash,
			EndRef:       "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("baseSinceReviewDiff:\n%s", baseSinceReviewDiff)

		result, err := diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
		require.NoError(t, err)
		t.Logf("FilterDiffForReview result:\n%s", result)

		// feature.go was committed before the review snapshot, so it does NOT
		// appear in sinceReviewDiff. Only new_main_file.go (from the second
		// merge) appears, and it should be filtered out.
		assert.NotContains(t, result, "new_main_file.go",
			"merge-introduced file should be filtered out")
		assert.Empty(t, result,
			"no files should remain: feature.go was already reviewed, new_main_file.go is merge-introduced")
	})

	t.Run("merge_introduced_file_filtered", func(t *testing.T) {
		t.Parallel()

		// Even without a prior merge, files added on main and merged in should
		// be excluded because they don't appear in the three-dot diff.

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc Shared() {}\n", "initial commit")

		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		createFileAndCommit(t, repoDir, "feature.go", "package feature\n\nfunc Feature() {}\n", "our feature work")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		createFileAndCommit(t, repoDir, "base_file.go", "package base\n\nfunc Base() {}\n", "base branch work")

		runGitCommandInTestRepo(t, repoDir, "checkout", "feature")
		createFileAndCommit(t, repoDir, "feature2.go", "package feature\n\nfunc Feature2() {}\n", "more feature work")

		runGitCommandInTestRepo(t, repoDir, "merge", "main", "-m", "merge main")

		sinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:  true,
			BaseRef: lastReviewTreeHash,
		})
		require.NoError(t, err)

		zeroCtx := 0
		branchDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:       true,
			ThreeDotDiff: true,
			BaseRef:      "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)

		baseSinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			BaseRef:      lastReviewTreeHash,
			EndRef:       "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)

		result, err := diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
		require.NoError(t, err)
		t.Logf("FilterDiffForReview result:\n%s", result)

		assert.Contains(t, result, "feature2.go", "our new work should be included")
		assert.NotContains(t, result, "base_file.go",
			"merge-introduced file should be filtered")
	})

	t.Run("reverted_file_not_in_since_review", func(t *testing.T) {
		t.Parallel()

		// A file changed then fully reverted has no net change from the review
		// tree, so it shouldn't appear in sinceReviewDiff at all.

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "config.yml", "key: value1\n", "initial commit")

		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(repoDir, "config.yml"), []byte("key: value2\n"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "config.yml")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "change config")

		err = os.WriteFile(filepath.Join(repoDir, "config.yml"), []byte("key: value1\n"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "config.yml")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "revert config")

		createFileAndCommit(t, repoDir, "feature.go", "package feature\n", "add feature")

		sinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:  true,
			BaseRef: lastReviewTreeHash,
		})
		require.NoError(t, err)

		zeroCtx := 0
		branchDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:       true,
			ThreeDotDiff: true,
			BaseRef:      "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)

		baseSinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			BaseRef:      lastReviewTreeHash,
			EndRef:       "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)

		result, err := diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
		require.NoError(t, err)
		t.Logf("FilterDiffForReview result:\n%s", result)

		assert.Contains(t, result, "feature.go", "our feature work should be included")
		assert.NotContains(t, result, "config.yml",
			"fully reverted file should not appear in since-review diff")
	})

	t.Run("partial_revert_preserved", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "config.yml",
			"line1: a\nline2: b\nline3: c\n", "initial commit")

		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(repoDir, "config.yml"),
			[]byte("line1: changed\nline2: changed\nline3: c\n"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "config.yml")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "change config")

		err = os.WriteFile(filepath.Join(repoDir, "config.yml"),
			[]byte("line1: a\nline2: changed\nline3: c\n"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "config.yml")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "partial revert")

		sinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:  true,
			BaseRef: lastReviewTreeHash,
		})
		require.NoError(t, err)

		zeroCtx := 0
		branchDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:       true,
			ThreeDotDiff: true,
			BaseRef:      "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)

		baseSinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			BaseRef:      lastReviewTreeHash,
			EndRef:       "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)

		result, err := diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
		require.NoError(t, err)
		t.Logf("FilterDiffForReview result:\n%s", result)

		assert.Contains(t, result, "line2: changed",
			"partially reverted file should still show remaining changes")
	})

	t.Run("shared_file_different_hunks_all_kept", func(t *testing.T) {
		t.Parallel()

		// A file is modified by both our branch and main at different locations.
		// After merging main, sinceReviewDiff shows both changes. branchDiff
		// (three-dot) shows only our change. The file should be kept with ALL
		// sinceReview hunks since it's a file our branch touched.

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		// Create a file with enough lines to produce separate hunks
		original := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\n"
		createFileAndCommit(t, repoDir, "shared.go", original, "initial commit")

		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		// Our branch modifies the top of the file
		withOurChange := "line1\nOUR_FEATURE_CHANGE\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\n"
		err = os.WriteFile(filepath.Join(repoDir, "shared.go"), []byte(withOurChange), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "shared.go")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "our feature change at top")

		// Main modifies the bottom of the file
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		withMainChange := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nMAIN_CHANGE\n"
		err = os.WriteFile(filepath.Join(repoDir, "shared.go"), []byte(withMainChange), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "shared.go")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "main change at bottom")

		// Merge main into feature
		runGitCommandInTestRepo(t, repoDir, "checkout", "feature")
		runGitCommandInTestRepo(t, repoDir, "merge", "main", "-m", "merge main")

		sinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:  true,
			BaseRef: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("sinceReviewDiff:\n%s", sinceReviewDiff)

		zeroCtx := 0
		branchDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:       true,
			ThreeDotDiff: true,
			BaseRef:      "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("branchDiff (three-dot):\n%s", branchDiff)

		// sinceReviewDiff should contain both changes
		assert.Contains(t, sinceReviewDiff, "OUR_FEATURE_CHANGE")
		assert.Contains(t, sinceReviewDiff, "MAIN_CHANGE")

		// branchDiff (three-dot) should only contain our change
		assert.Contains(t, branchDiff, "OUR_FEATURE_CHANGE")
		assert.NotContains(t, branchDiff, "MAIN_CHANGE")

		baseSinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			BaseRef:      lastReviewTreeHash,
			EndRef:       "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("baseSinceReviewDiff:\n%s", baseSinceReviewDiff)

		result, err := diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
		require.NoError(t, err)
		t.Logf("FilterDiffForReview result:\n%s", result)

		// Hunk-level filtering: OUR_FEATURE_CHANGE hunk is not in
		// baseSinceReview → kept. MAIN_CHANGE hunk overlaps baseSinceReview
		// but not branchDiff → dropped.
		assert.Contains(t, result, "OUR_FEATURE_CHANGE",
			"our change should be in the result")
		assert.NotContains(t, result, "MAIN_CHANGE",
			"merge-introduced hunk should be filtered even in a file we also touched")
		assert.Contains(t, result, "shared.go")
	})

	t.Run("revert_to_match_main_after_review", func(t *testing.T) {
		t.Parallel()

		// Scenario the automated reviewer is worried about:
		// 1. Review snapshot captured with our modified config.yml
		// 2. We revert config.yml to match what main has
		// 3. sinceReviewDiff shows the revert (tree changed since review)
		// 4. branchDiff (three-dot) does NOT show config.yml (matches main)
		// 5. Filter drops it — is that correct or a bug?
		//
		// Dropping it is correct: the file now matches main, so there's
		// nothing for the reviewer to act on.

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "config.yml", "key: original\n", "initial commit")

		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

		// Modify config.yml on our branch
		err := os.WriteFile(filepath.Join(repoDir, "config.yml"), []byte("key: our-change\n"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "config.yml")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "modify config")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		// Review happens here — snapshot includes our modified config.yml
		lastReviewTreeHash, err := WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		// Main also changes config.yml
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		err = os.WriteFile(filepath.Join(repoDir, "config.yml"), []byte("key: main-change\n"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "config.yml")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "main changes config")

		// Back on feature, revert config.yml to match main's version
		runGitCommandInTestRepo(t, repoDir, "checkout", "feature")
		err = os.WriteFile(filepath.Join(repoDir, "config.yml"), []byte("key: main-change\n"), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "config.yml")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "revert config to match main")

		// Merge main
		runGitCommandInTestRepo(t, repoDir, "merge", "main", "-m", "merge main")

		sinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:  true,
			BaseRef: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("sinceReviewDiff:\n%s", sinceReviewDiff)

		zeroCtx := 0
		branchDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:       true,
			ThreeDotDiff: true,
			BaseRef:      "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("branchDiff (three-dot):\n%s", branchDiff)

		// sinceReviewDiff should show config.yml changed (from "our-change" to "main-change")
		assert.Contains(t, sinceReviewDiff, "config.yml")

		// branchDiff should NOT show config.yml (matches main now)
		assert.NotContains(t, branchDiff, "config.yml")

		baseSinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			BaseRef:      lastReviewTreeHash,
			EndRef:       "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("baseSinceReviewDiff:\n%s", baseSinceReviewDiff)

		result, err := diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
		require.NoError(t, err)
		t.Logf("FilterDiffForReview result:\n%s", result)

		// config.yml hunk overlaps baseSinceReview but not branchDiff → dropped
		assert.NotContains(t, result, "config.yml",
			"file reverted to match main should be dropped — nothing actionable for reviewer")
	})

	t.Run("production_scenario_worktree_setup_revert", func(t *testing.T) {
		t.Parallel()

		// Mirrors the production bug from the requirements:
		// side.yml had a line removed (worktree_setup change) that was committed
		// then main independently made the same change, and the merge converges.
		// The three-dot diff still shows our test_commands changes.

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		sideYmlOriginal := `mission: |
  Help developers be productive.

worktree_setup: |
  go install sidekick/cmd/gotestreport
  cd frontend && bun ci && touch dist/empty.txt

test_commands:
  - command: "go test -test.timeout 30s ./..."
`
		createFileAndCommit(t, repoDir, "side.yml", sideYmlOriginal, "initial commit")

		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		sideYmlModified := `mission: |
  Help developers be productive.

worktree_setup: |
  cd frontend && bun ci && touch dist/empty.txt

test_commands:
  - command: "gotestreport -test.timeout 30s ./..."
`
		err = os.WriteFile(filepath.Join(repoDir, "side.yml"), []byte(sideYmlModified), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "side.yml")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "update side.yml")

		lastReviewTreeHash, err := WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		sideYmlMain := `mission: |
  Help developers be productive.

worktree_setup: |
  cd frontend && bun ci && touch dist/empty.txt

test_commands:
  - command: "go test -test.timeout 30s ./..."
`
		err = os.WriteFile(filepath.Join(repoDir, "side.yml"), []byte(sideYmlMain), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "side.yml")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "also remove worktree_setup line on main")

		runGitCommandInTestRepo(t, repoDir, "checkout", "feature")
		runGitCommandInTestRepo(t, repoDir, "merge", "main", "-m", "merge main")

		sinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:  true,
			BaseRef: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("sinceReviewDiff:\n%s", sinceReviewDiff)

		zeroCtx := 0
		branchDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:       true,
			ThreeDotDiff: true,
			BaseRef:      "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("branchDiff:\n%s", branchDiff)

		baseSinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			BaseRef:      lastReviewTreeHash,
			EndRef:       "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("baseSinceReviewDiff:\n%s", baseSinceReviewDiff)

		result, err := diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
		require.NoError(t, err)
		t.Logf("FilterDiffForReview result:\n%s", result)

		// The review snapshot was taken after our feature changes. Main
		// independently made the same worktree_setup removal, so the merge
		// doesn't change the tree — sinceReviewDiff is empty.
		// The only remaining difference (test_commands) was already reviewed.
		assert.Empty(t, sinceReviewDiff,
			"since-review diff should be empty when merge converges to same state")
		assert.Empty(t, result,
			"no result when there are no changes since review")
	})

	t.Run("rebase_filters_merge_introduced_changes", func(t *testing.T) {
		t.Parallel()

		// After rebasing onto main, the merge-base IS main's tip, so the
		// three-dot diff only contains our branch's own changes. Meanwhile
		// baseSinceReviewDiff (reviewTree → main) captures what main added
		// since the review, letting us filter those hunks from sinceReviewDiff.

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc Shared() {}\n", "initial commit")

		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")
		createFileAndCommit(t, repoDir, "feature.go", "package feature\n\nfunc Feature() {}\n", "feature work")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		// Main adds a file after review
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		createFileAndCommit(t, repoDir, "main_new.go", "package main_new\n\nfunc MainNew() {}\n", "main adds file")

		// Rebase feature onto main
		runGitCommandInTestRepo(t, repoDir, "checkout", "feature")
		runGitCommandInTestRepo(t, repoDir, "rebase", "main")

		// Post-rebase: add more work
		createFileAndCommit(t, repoDir, "feature2.go", "package feature\n\nfunc Feature2() {}\n", "more feature work")

		sinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:  true,
			BaseRef: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("sinceReviewDiff:\n%s", sinceReviewDiff)

		assert.Contains(t, sinceReviewDiff, "main_new.go",
			"since-review should include rebased-in file")
		assert.Contains(t, sinceReviewDiff, "feature2.go",
			"since-review should include our new work")

		zeroCtx := 0
		branchDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:       true,
			ThreeDotDiff: true,
			BaseRef:      "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("branchDiff (three-dot):\n%s", branchDiff)

		// After rebase, three-dot only shows our branch's own commits
		assert.NotContains(t, branchDiff, "main_new.go",
			"three-dot after rebase should not include main's file")

		baseSinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			BaseRef:      lastReviewTreeHash,
			EndRef:       "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("baseSinceReviewDiff:\n%s", baseSinceReviewDiff)

		assert.Contains(t, baseSinceReviewDiff, "main_new.go",
			"baseSinceReview should show main's additions since the review")

		result, err := diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
		require.NoError(t, err)
		t.Logf("FilterDiffForReview result:\n%s", result)

		assert.Contains(t, result, "feature2.go",
			"our post-rebase work should be included")
		assert.NotContains(t, result, "main_new.go",
			"rebase-introduced file should be filtered out")
	})

	t.Run("rebase_shared_file_hunk_level_filtering", func(t *testing.T) {
		t.Parallel()

		// Both our branch and main edit the same file at different locations.
		// After rebasing and adding more work, sinceReviewDiff shows both
		// hunks. The hunk from main should be filtered; our hunk (made after
		// the review snapshot) should be kept.

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		// Create a file with enough lines to have separated hunks
		content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\n"
		createFileAndCommit(t, repoDir, "shared.go", content, "initial commit")

		runGitCommandInTestRepo(t, repoDir, "checkout", "-b", "feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		// Take review snapshot BEFORE our edit
		lastReviewTreeHash, err := WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		// Our branch edits line 2 (after review)
		newContent := "line1\nOUR_CHANGE\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\n"
		err = os.WriteFile(filepath.Join(repoDir, "shared.go"), []byte(newContent), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "shared.go")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "our change at line 2")

		// Main edits line 20
		runGitCommandInTestRepo(t, repoDir, "checkout", "main")
		mainContent := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nMAIN_CHANGE\n"
		err = os.WriteFile(filepath.Join(repoDir, "shared.go"), []byte(mainContent), 0644)
		require.NoError(t, err)
		runGitCommandInTestRepo(t, repoDir, "add", "shared.go")
		runGitCommandInTestRepo(t, repoDir, "commit", "-m", "main change at line 20")

		// Rebase feature onto main
		runGitCommandInTestRepo(t, repoDir, "checkout", "feature")
		runGitCommandInTestRepo(t, repoDir, "rebase", "main")

		sinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:  true,
			BaseRef: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("sinceReviewDiff:\n%s", sinceReviewDiff)

		assert.Contains(t, sinceReviewDiff, "OUR_CHANGE")
		assert.Contains(t, sinceReviewDiff, "MAIN_CHANGE")

		zeroCtx := 0
		branchDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			Staged:       true,
			ThreeDotDiff: true,
			BaseRef:      "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("branchDiff (three-dot):\n%s", branchDiff)

		assert.Contains(t, branchDiff, "OUR_CHANGE")
		assert.NotContains(t, branchDiff, "MAIN_CHANGE")

		baseSinceReviewDiff, err := GitDiffActivity(ctx, envContainer, GitDiffParams{
			BaseRef:      lastReviewTreeHash,
			EndRef:       "main",
			ContextLines: &zeroCtx,
		})
		require.NoError(t, err)
		t.Logf("baseSinceReviewDiff:\n%s", baseSinceReviewDiff)

		assert.Contains(t, baseSinceReviewDiff, "MAIN_CHANGE")

		result, err := diffanalysis.FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
		require.NoError(t, err)
		t.Logf("FilterDiffForReview result:\n%s", result)

		assert.Contains(t, result, "OUR_CHANGE",
			"our hunk should be kept after rebase")
		assert.NotContains(t, result, "MAIN_CHANGE",
			"main's hunk should be filtered even after rebase")
	})
}
