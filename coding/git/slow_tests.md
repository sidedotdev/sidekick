# Slowest Tests in coding/git

Run `go test -v ./coding/git/...` to reproduce.

## Top Slowest Tests (by recorded duration)

1. **TestGitMergeActivity/source_is_worktree,_with_target_NOT_checked_out_anywhere,_with_conflicts_-_reverse_merge_strategy** - 0.61s
2. **TestGitMergeActivity/no_worktree,_with_conflicts** - 0.53s
3. **TestGitDiffActivity/combined_flags_with_ignore_whitespace_and_file_paths** - 0.45s
4. **TestCleanupWorktreeActivity/Successful_Cleanup** - 0.44s
5. **TestGitDiffActivity/both_staged_and_three_dot_diff_with_valid_base_branch** - 0.43s
6. **TestGitDiffActivity/file_paths_filter** - 0.42s
7. **TestGitDiffActivity/only_three_dot_diff_true** - 0.40s
8. **TestGitMergeActivity/source_is_worktree,_with_target_checked_out_on_repoDir,_with_conflicts** - 0.39s
9. **TestListLocalBranches/Multiple_Branches_Sorted_by_Committer_Date** - 0.38s
10. **TestGitMergeActivity/source_is_worktree,_with_target_checked_out_on_repoDir,_no_conflicts** - 0.37s

## Summary by Test Function

- **TestGitMergeActivity**: 2.16s
- **TestCleanupWorktreeActivity**: 0.96s
- **TestGetDefaultBranch**: 0.90s
- **TestListWorktrees**: 0.74s
- **TestListLocalBranches**: 0.59s (significantly improved from 4.16s)
- **TestGitDiffActivity**: Parallel execution, individual tests are faster (~0.2-0.45s each).

## Performance Analysis

Profiling with `go test -cpuprofile` reveals the following bottlenecks:

1.  **Filesystem Cleanup (`os.RemoveAll` - ~28% of time)**:
    The heaviest operation is cleaning up temporary directories created for each test case. `testing.(*common).TempDir` cleanup (via `os.RemoveAll`) is very expensive, especially on filesystems where file deletion is slow or synchronized.

2.  **Git Process Overhead (`os/exec` - ~16% of time)**:
    Spawning external `git` processes for every single git operation (init, add, commit, branch, diff, etc.) adds significant overhead. `syscall.syscall` consumes ~40% of CPU time, largely driven by these process creations and filesystem interactions.
    *Update*: We reduced this overhead by removing `git config` commands from `setupTestGitRepo` and using environment variables instead.

3.  **Wait Times in Tests**:
    *Update*: `TestListLocalBranches` no longer sleeps. Wait times have been eliminated by using `GIT_COMMITTER_DATE` environment variables to control sort order deterministically.

### Conclusion
The primary slowness remains the overhead of creating and destroying temporary git repositories and executing git subprocesses, but we have successfully removed the artificial sleeps in `TestListLocalBranches` and reduced process overhead by eliminating `git config` calls. `TestListLocalBranches` is now ~7x faster.