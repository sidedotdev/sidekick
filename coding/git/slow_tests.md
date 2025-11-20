# Slowest Tests in coding/git

Run `go test -v ./coding/git/...` to reproduce.

## Top Slowest Tests (by recorded duration)

1. **TestListLocalBranches/Multiple_Branches_Sorted_by_Committer_Date** - 3.88s
2. **TestGitDiffActivity/combined_flags_with_ignore_whitespace_and_file_paths** - 0.90s
3. **TestGitDiffActivity/both_staged_and_three_dot_diff_with_valid_base_branch** - 0.86s
4. **TestGitDiffActivity/only_three_dot_diff_true** - 0.83s
5. **TestGitDiffActivity/file_paths_filter** - 0.82s
6. **TestGitDiffActivity/neither_flag_true_untracked_files** - 0.74s
7. **TestGitDiffActivity/neither_flag_true_working_tree_changes** - 0.68s
8. **TestGitMergeActivity/no_worktree,_with_conflicts** - 0.67s
9. **TestGitDiffActivity/ignore_whitespace_flag** - 0.65s
10. **TestGitDiffActivity/only_staged_true** - 0.65s
11. **TestGitDiffActivity/no_changes_empty_output** - 0.64s
12. **TestGitMergeActivity/source_is_worktree,_with_target_NOT_checked_out_anywhere,_with_conflicts_-_reverse_merge_strategy** - 0.61s
13. **TestCleanupWorktreeActivity/Successful_Cleanup_with_Empty_Archive_Message** - 0.58s
14. **TestGitDiffActivity/only_three_dot_diff_true_empty_base_branch** - 0.57s

## Summary by Test Function

- **TestListLocalBranches**: 4.16s (dominated by one subtest)
- **TestGitMergeActivity**: 2.44s (spread across subtests)
- **TestCleanupWorktreeActivity**: 1.48s
- **TestGetDefaultBranch**: 1.07s
- **TestListWorktrees**: 1.06s
- **TestGitDiffActivity**: Parallel execution, individual tests are slow (~0.6-0.9s each).