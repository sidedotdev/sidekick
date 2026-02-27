package coding

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"sidekick/coding/git"
	"sidekick/env"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOwnChangesSinceReviewActivity(t *testing.T) {
	t.Parallel()
	ca := &CodingActivities{}

	t.Run("after_merge_base_into_feature", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc Shared() {}\n", "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")
		createFileAndCommit(t, repoDir, "feature.go", "package feature\n\nfunc Feature() {}\n", "our feature work")

		runGit(t, repoDir, "checkout", "main")
		createFileAndCommit(t, repoDir, "base_file.go", "package base\n\nfunc Base() {}\n", "base branch addition")

		runGit(t, repoDir, "checkout", "feature")
		runGit(t, repoDir, "merge", "main", "-m", "merge main into feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		runGit(t, repoDir, "checkout", "main")
		createFileAndCommit(t, repoDir, "new_main_file.go", "package newmain\n\nfunc NewMain() {}\n", "new main file")

		runGit(t, repoDir, "checkout", "feature")
		runGit(t, repoDir, "merge", "main", "-m", "merge main again")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)

		assert.NotContains(t, result, "new_main_file.go",
			"merge-introduced file should be filtered out")
		assert.Empty(t, result,
			"no files should remain: feature.go was already reviewed, new_main_file.go is merge-introduced")
	})

	t.Run("merge_introduced_file_filtered", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc Shared() {}\n", "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")
		createFileAndCommit(t, repoDir, "feature.go", "package feature\n\nfunc Feature() {}\n", "our feature work")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		runGit(t, repoDir, "checkout", "main")
		createFileAndCommit(t, repoDir, "base_file.go", "package base\n\nfunc Base() {}\n", "base branch work")

		runGit(t, repoDir, "checkout", "feature")
		createFileAndCommit(t, repoDir, "feature2.go", "package feature\n\nfunc Feature2() {}\n", "more feature work")

		runGit(t, repoDir, "merge", "main", "-m", "merge main")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("result:\n%s", result)

		assert.Contains(t, result, "feature2.go", "our new work should be included")
		assert.NotContains(t, result, "base_file.go",
			"merge-introduced file should be filtered")
	})

	t.Run("fully_reverted_change_not_shown", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "config.yml", "key: value1\n", "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		err = os.WriteFile(filepath.Join(repoDir, "config.yml"), []byte("key: changed\n"), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "config.yml")
		runGit(t, repoDir, "commit", "-m", "modify config")

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(repoDir, "config.yml"), []byte("key: value1\n"), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "config.yml")
		runGit(t, repoDir, "commit", "-m", "revert config")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)

		assert.Empty(t, result,
			"fully reverted change should produce empty diff")
	})

	t.Run("partial_revert_preserved", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "config.yml",
			"line1: a\nline2: b\nline3: c\n", "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		err = os.WriteFile(filepath.Join(repoDir, "config.yml"),
			[]byte("line1: a\nline2: modified\nline3: also_modified\n"), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "config.yml")
		runGit(t, repoDir, "commit", "-m", "modify two lines")

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(repoDir, "config.yml"),
			[]byte("line1: a\nline2: changed\nline3: c\n"), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "config.yml")
		runGit(t, repoDir, "commit", "-m", "partial revert")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("result:\n%s", result)

		assert.Contains(t, result, "line2: changed",
			"partially reverted file should still show remaining changes")
	})

	t.Run("shared_file_different_hunks_all_kept", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		original := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\n"
		createFileAndCommit(t, repoDir, "shared.go", original, "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		withOurChange := "line1\nOUR_FEATURE_CHANGE\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\n"
		err = os.WriteFile(filepath.Join(repoDir, "shared.go"), []byte(withOurChange), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "shared.go")
		runGit(t, repoDir, "commit", "-m", "our feature change at top")

		runGit(t, repoDir, "checkout", "main")
		withMainChange := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nMAIN_CHANGE\n"
		err = os.WriteFile(filepath.Join(repoDir, "shared.go"), []byte(withMainChange), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "shared.go")
		runGit(t, repoDir, "commit", "-m", "main change at bottom")

		runGit(t, repoDir, "checkout", "feature")
		runGit(t, repoDir, "merge", "main", "-m", "merge main")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("result:\n%s", result)

		assert.Contains(t, result, "OUR_FEATURE_CHANGE",
			"our change should be in the result")
		assert.NotContains(t, result, "MAIN_CHANGE",
			"merge-introduced hunk should be filtered even in a file we also touched")
		assert.Contains(t, result, "shared.go")
	})

	t.Run("revert_to_match_main_after_review", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "config.yml", "key: original\n", "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")
		err := os.WriteFile(filepath.Join(repoDir, "config.yml"), []byte("key: modified\n"), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "config.yml")
		runGit(t, repoDir, "commit", "-m", "modify config")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(repoDir, "config.yml"), []byte("key: original\n"), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "config.yml")
		runGit(t, repoDir, "commit", "-m", "revert to match main")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("result:\n%s", result)

		assert.NotContains(t, result, "config.yml",
			"file reverted to match main should be filtered out")
	})

	t.Run("convergent_change_kept", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "test_commands",
			"run_tests: old_command\nother: stuff\n", "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")
		err := os.WriteFile(filepath.Join(repoDir, "test_commands"),
			[]byte("run_tests: new_command\nother: stuff\n"), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "test_commands")
		runGit(t, repoDir, "commit", "-m", "update test command")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		runGit(t, repoDir, "checkout", "main")
		err = os.WriteFile(filepath.Join(repoDir, "test_commands"),
			[]byte("run_tests: new_command\nother: stuff\n"), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "test_commands")
		runGit(t, repoDir, "commit", "-m", "same change on main")

		runGit(t, repoDir, "checkout", "feature")
		runGit(t, repoDir, "merge", "main", "-m", "merge main")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("result:\n%s", result)

		assert.Empty(t, result,
			"file now matches main so sinceReviewDiff is empty, nothing to show")
	})

	t.Run("undone_changes_excluded", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc Shared() {}\n", "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")
		createFileAndCommit(t, repoDir, "feature.go", "package feature\n\nfunc Feature() {}\n", "add feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		runGit(t, repoDir, "rm", "feature.go")
		runGit(t, repoDir, "commit", "-m", "remove feature.go")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("result:\n%s", result)

		assert.NotContains(t, result, "feature.go",
			"undone change (file added then removed) should be excluded from since-review diff")
	})

	t.Run("rebase_new_file_filtered", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		createFileAndCommit(t, repoDir, "shared.go", "package shared\n\nfunc Shared() {}\n", "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")
		createFileAndCommit(t, repoDir, "feature.go", "package feature\n\nfunc Feature() {}\n", "add feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		runGit(t, repoDir, "checkout", "main")
		createFileAndCommit(t, repoDir, "main_new.go", "package main_new\n\nfunc MainNew() {}\n", "main adds file")

		runGit(t, repoDir, "checkout", "feature")
		runGit(t, repoDir, "rebase", "main")

		createFileAndCommit(t, repoDir, "feature2.go", "package feature\n\nfunc Feature2() {}\n", "more feature work")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("result:\n%s", result)

		assert.Contains(t, result, "feature2.go",
			"our post-rebase work should be included")
		assert.NotContains(t, result, "main_new.go",
			"rebase-introduced file should be filtered out")
	})

	t.Run("rebase_shared_file_hunk_level_filtering", func(t *testing.T) {
		t.Parallel()

		repoDir := setupTestGitRepo(t)
		ctx := context.Background()

		content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\n"
		createFileAndCommit(t, repoDir, "shared.go", content, "initial commit")

		runGit(t, repoDir, "checkout", "-b", "feature")

		devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
		require.NoError(t, err)
		envContainer := env.EnvContainer{Env: devEnv}

		lastReviewTreeHash, err := git.WriteTreeActivity(ctx, envContainer)
		require.NoError(t, err)

		newContent := "line1\nOUR_CHANGE\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\n"
		err = os.WriteFile(filepath.Join(repoDir, "shared.go"), []byte(newContent), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "shared.go")
		runGit(t, repoDir, "commit", "-m", "our change at line 2")

		runGit(t, repoDir, "checkout", "main")
		mainContent := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n" +
			"line11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\nMAIN_CHANGE\n"
		err = os.WriteFile(filepath.Join(repoDir, "shared.go"), []byte(mainContent), 0644)
		require.NoError(t, err)
		runGit(t, repoDir, "add", "shared.go")
		runGit(t, repoDir, "commit", "-m", "main change at line 20")

		runGit(t, repoDir, "checkout", "feature")
		runGit(t, repoDir, "rebase", "main")

		result, err := ca.GetOwnChangesSinceReviewActivity(ctx, GetOwnChangesSinceReviewParams{
			EnvContainer:   envContainer,
			BaseBranch:     "main",
			LastReviewTree: lastReviewTreeHash,
		})
		require.NoError(t, err)
		t.Logf("result:\n%s", result)

		assert.Contains(t, result, "OUR_CHANGE",
			"our hunk should be kept after rebase")
		assert.NotContains(t, result, "MAIN_CHANGE",
			"main's hunk should be filtered even after rebase")
	})
}
