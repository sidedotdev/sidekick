package git

import (
	"context"
	"fmt"
	"sidekick/env"
	"strings"
)

// ConflictMarkerSnapshot identifies the state captured immediately after
// merge conflicts were written into the working tree but before any
// resolution edits. It is later used to produce the conflict-resolution
// diff that highlights only the edits made to resolve conflicts.
type ConflictMarkerSnapshot struct {
	// TreeHash points at a git tree containing the conflict-marker
	// versions of the conflicted files. Diffing this tree against the
	// current working tree (restricted to ConflictedPaths) reveals what
	// the resolver actually did.
	TreeHash string `json:"treeHash"`
	// ConflictedPaths lists repository-relative paths that were unmerged
	// at the time the snapshot was taken.
	ConflictedPaths []string `json:"conflictedPaths"`
}

// GitMergeAbortParams targets a specific worktree/directory to abort an
// in-progress merge in.
type GitMergeAbortParams struct {
	WorktreePath string
}

// mergeAbortCommand builds a shell command that aborts any in-progress merge
// in the given directory, covering both regular merges and squash merges. A
// squash merge never sets MERGE_HEAD (even when it conflicts), so `git merge
// --abort` refuses to run; in that case SQUASH_MSG is present and `git reset
// --merge` restores the pre-merge state, including removing the leftover
// SQUASH_MSG/MERGE_MSG state files. When neither is present, the command is a
// no-op so callers can use it as a pre-cleanup step without introspecting
// state first.
func mergeAbortCommand(dir string) string {
	return fmt.Sprintf(
		"cd %s && if git rev-parse --verify -q MERGE_HEAD >/dev/null; then git merge --abort; "+
			"elif [ -f \"$(git rev-parse --git-path SQUASH_MSG)\" ]; then git reset --merge; "+
			"else true; fi",
		shellQuote(dir),
	)
}

// GitMergeAbortActivity aborts an in-progress merge (regular or squash) in
// the given directory. It is a no-op (and not an error) when no merge is in
// progress, so it can be used as a pre-cleanup step without callers having
// to introspect state first.
func GitMergeAbortActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeAbortParams) error {
	dir := params.WorktreePath
	if dir == "" {
		dir = envContainer.Env.GetWorkingDirectory()
	}
	if dir == "" {
		return fmt.Errorf("worktree path is required to abort a merge")
	}
	cmd := mergeAbortCommand(dir)
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", cmd},
	})
	if err != nil {
		return fmt.Errorf("failed to run merge abort command: %w", err)
	}
	if out.ExitStatus != 0 {
		return fmt.Errorf("git merge --abort failed: %s", strings.TrimSpace(out.Stderr+out.Stdout))
	}
	return nil
}

// GitMergeInProgressParams targets the worktree to inspect.
type GitMergeInProgressParams struct {
	WorktreePath string
}

// GitMergeInProgressActivity reports whether a merge is currently in
// progress in the given worktree, i.e. whether `MERGE_HEAD` exists.
// Callers use this to confirm there is something to finalize before
// invoking GitCommitMergeActivity, which would otherwise produce a
// regular (single-parent) commit on top of HEAD and mis-represent the
// history.
func GitMergeInProgressActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeInProgressParams) (bool, error) {
	dir := params.WorktreePath
	if dir == "" {
		dir = envContainer.Env.GetWorkingDirectory()
	}
	if dir == "" {
		return false, fmt.Errorf("worktree path is required to check merge state")
	}
	cmd := fmt.Sprintf(
		"cd %s && if git rev-parse --verify -q MERGE_HEAD >/dev/null; then echo yes; else echo no; fi",
		shellQuote(dir),
	)
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", cmd},
	})
	if err != nil {
		return false, fmt.Errorf("failed to check merge state: %w", err)
	}
	if out.ExitStatus != 0 {
		return false, fmt.Errorf("git rev-parse for MERGE_HEAD failed: %s", strings.TrimSpace(out.Stderr+out.Stdout))
	}
	return strings.TrimSpace(out.Stdout) == "yes", nil
}

// GitListUnmergedParams targets the worktree to inspect.
type GitListUnmergedParams struct {
	WorktreePath string
}

// GitListUnmergedActivity returns the repository-relative paths of all
// files that are still unresolved in the given worktree. This combines
// two checks: index-level unmerged entries (`git ls-files -u`, the
// authoritative signal for a real conflict state) and any tracked files
// that still contain literal conflict markers (in case a resolver wrote
// markers into a file but then `git add`ed it, which clears the
// unmerged stage but leaves a textual conflict). Both signals matter:
// the merge cannot be finalized cleanly until both are empty.
func GitListUnmergedActivity(ctx context.Context, envContainer env.EnvContainer, params GitListUnmergedParams) ([]string, error) {
	dir := params.WorktreePath
	if dir == "" {
		dir = envContainer.Env.GetWorkingDirectory()
	}
	if dir == "" {
		return nil, fmt.Errorf("worktree path is required to list unmerged files")
	}

	// git ls-files -u: stage>0 entries (real unmerged state).
	// git grep over the tracked set: surfaces lingering conflict markers
	// after a faux-resolve that `git add`ed a still-conflicted file.
	cmd := fmt.Sprintf(
		"cd %s && git ls-files -u && printf '\\n---MARKERS---\\n' && (git grep -lE '^(<{7}|={7}|>{7})( |$)' -- . || true)",
		shellQuote(dir),
	)
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", cmd},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list unmerged files: %w", err)
	}
	if out.ExitStatus != 0 {
		return nil, fmt.Errorf("git ls-files -u failed: %s", out.Stderr)
	}

	parts := strings.SplitN(out.Stdout, "\n---MARKERS---\n", 2)
	unmergedSection := parts[0]
	var markerSection string
	if len(parts) == 2 {
		markerSection = parts[1]
	}

	seen := map[string]struct{}{}
	var paths []string
	addPath := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}

	for _, line := range strings.Split(strings.TrimSpace(unmergedSection), "\n") {
		if line == "" {
			continue
		}
		// Format: <mode> <sha> <stage>\t<path>
		tab := strings.Index(line, "\t")
		if tab < 0 {
			continue
		}
		addPath(line[tab+1:])
	}
	for _, line := range strings.Split(strings.TrimSpace(markerSection), "\n") {
		addPath(strings.TrimSpace(line))
	}
	return paths, nil
}

// GitSnapshotConflictMarkersParams targets the worktree to snapshot.
type GitSnapshotConflictMarkersParams struct {
	WorktreePath string
}

// GitSnapshotConflictMarkersActivity captures the conflict-marker content
// of currently-unmerged files into a git tree object, returning a snapshot
// that can later be diffed against the resolved state. It uses a private
// temporary index so it does not disturb the live index or working tree.
func GitSnapshotConflictMarkersActivity(ctx context.Context, envContainer env.EnvContainer, params GitSnapshotConflictMarkersParams) (ConflictMarkerSnapshot, error) {
	var snapshot ConflictMarkerSnapshot
	dir := params.WorktreePath
	if dir == "" {
		dir = envContainer.Env.GetWorkingDirectory()
	}
	if dir == "" {
		return snapshot, fmt.Errorf("worktree path is required to snapshot conflict markers")
	}

	unmerged, err := GitListUnmergedActivity(ctx, envContainer, GitListUnmergedParams{WorktreePath: dir})
	if err != nil {
		return snapshot, err
	}
	if len(unmerged) == 0 {
		return snapshot, nil
	}
	snapshot.ConflictedPaths = unmerged

	// `git rev-parse --git-path` resolves the correct location even in
	// linked worktrees, where `.git` is a file pointing at the real
	// gitdir under the main repo. Using a literal `.git/foo` would break
	// in that case.
	//
	// `git read-tree HEAD` seeds a fresh index with HEAD's tree; the
	// follow-up `git add` then overlays the working-tree (conflict-
	// marker) versions of just the listed paths, isolating the snapshot
	// to those files. We don't try to handle add/add deletions here:
	// `git add -- <path>` on a missing file fails, which we surface so
	// the caller can fall back to a non-snapshot resolution flow.
	addArgs := []string{"add", "--"}
	for _, p := range unmerged {
		addArgs = append(addArgs, shellQuote(p))
	}
	pipeline := []string{
		fmt.Sprintf("cd %s", shellQuote(dir)),
		`tmpidx="$(git rev-parse --git-path sidekick-conflict-snapshot.index)"`,
		`rm -f "$tmpidx"`,
		`GIT_INDEX_FILE="$tmpidx" git read-tree HEAD`,
		fmt.Sprintf(`GIT_INDEX_FILE="$tmpidx" git %s`, strings.Join(addArgs, " ")),
		`tree="$(GIT_INDEX_FILE="$tmpidx" git write-tree)"`,
		`rm -f "$tmpidx"`,
		`printf '%s\n' "$tree"`,
	}
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", strings.Join(pipeline, " && ")},
	})
	if err != nil {
		return snapshot, fmt.Errorf("failed to write conflict snapshot tree: %w", err)
	}
	if out.ExitStatus != 0 {
		return snapshot, fmt.Errorf("git write-tree for conflict snapshot failed: %s", strings.TrimSpace(out.Stderr+out.Stdout))
	}
	snapshot.TreeHash = strings.TrimSpace(out.Stdout)
	return snapshot, nil
}

// GitConflictResolutionDiffParams diffs a previously captured conflict
// snapshot against the current working tree, restricted to the paths
// that were originally conflicted.
type GitConflictResolutionDiffParams struct {
	WorktreePath string
	Snapshot     ConflictMarkerSnapshot
}

// GitConflictResolutionDiffActivity returns a unified diff that shows
// how the conflict markers were resolved. Regular `git diff` is empty
// after a merge commit, and diffing against either parent only shows
// the changes from that side; this combined "snapshot vs resolved"
// diff is what makes the resolution itself reviewable.
func GitConflictResolutionDiffActivity(ctx context.Context, envContainer env.EnvContainer, params GitConflictResolutionDiffParams) (string, error) {
	dir := params.WorktreePath
	if dir == "" {
		dir = envContainer.Env.GetWorkingDirectory()
	}
	if dir == "" {
		return "", fmt.Errorf("worktree path is required for conflict resolution diff")
	}
	if params.Snapshot.TreeHash == "" || len(params.Snapshot.ConflictedPaths) == 0 {
		return "", nil
	}
	args := []string{"diff", "--no-color", params.Snapshot.TreeHash, "--"}
	for _, p := range params.Snapshot.ConflictedPaths {
		args = append(args, shellQuote(p))
	}
	cmd := fmt.Sprintf("cd %s && git %s", shellQuote(dir), strings.Join(args, " "))
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", cmd},
	})
	if err != nil {
		return "", fmt.Errorf("failed to compute conflict resolution diff: %w", err)
	}
	if out.ExitStatus != 0 {
		return "", fmt.Errorf("git diff for conflict resolution failed: %s", out.Stderr)
	}
	return out.Stdout, nil
}

// GitCommitMergeParams finalizes an in-progress merge by committing it.
type GitCommitMergeParams struct {
	WorktreePath   string
	CommitMessage  string
	CommitterName  string
	CommitterEmail string
}

// GitCommitMergeActivity finalizes the in-progress merge in the given
// directory by creating the merge commit. The agent is expected to have
// resolved (and `git add`ed) all conflicts; this activity does not write
// any working-tree files.
func GitCommitMergeActivity(ctx context.Context, envContainer env.EnvContainer, params GitCommitMergeParams) error {
	dir := params.WorktreePath
	if dir == "" {
		dir = envContainer.Env.GetWorkingDirectory()
	}
	if dir == "" {
		return fmt.Errorf("worktree path is required to finalize merge commit")
	}

	committerName, committerEmail := params.CommitterName, params.CommitterEmail
	if committerName == "" || committerEmail == "" {
		envType := envContainer.Env.GetType()
		if envType == env.EnvTypeLocal || envType == env.EnvTypeLocalGitWorktree {
			name, email, err := getGitUserConfig(ctx, envContainer)
			if err == nil {
				if committerName == "" {
					committerName = name
				}
				if committerEmail == "" {
					committerEmail = email
				}
			}
		}
	}
	envVars := buildGitEnvVars(committerName, committerEmail)

	msg := strings.TrimSpace(params.CommitMessage)
	if msg == "" {
		msg = "Merge with conflict resolution"
	}

	// Stage everything the resolver touched, including newly created or
	// renamed files (e.g. add/add or rename-style resolutions), so the
	// merge commit captures the full resolution. `git add -A` covers
	// new, modified and deleted tracked content; unmerged entries are
	// still surfaced as failures by the subsequent `git commit` if any
	// conflict remains, which is what we want.
	cmd := fmt.Sprintf(
		"cd %s && git add -A && git commit --no-verify -m %s",
		shellQuote(dir), shellQuote(msg),
	)
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", cmd},
		EnvVars:      envVars,
	})
	if err != nil {
		return fmt.Errorf("failed to finalize merge commit: %w", err)
	}
	if out.ExitStatus != 0 {
		return fmt.Errorf("git commit for merge finalization failed: %s", strings.TrimSpace(out.Stderr+out.Stdout))
	}
	return nil
}

// GitMergeIntoWorktreeParams merges a branch into the working tree at
// WorktreePath, without auto-committing. It is used to (re)create a
// conflict in the source worktree so the agent can resolve it.
type GitMergeIntoWorktreeParams struct {
	WorktreePath   string
	SourceBranch   string
	CommitterName  string
	CommitterEmail string
}

// GitMergeIntoWorktreeResult reports whether conflicts occurred. When
// true, conflict markers are present in the working tree at WorktreePath
// and `MERGE_HEAD` is set, ready for the resolution flow.
type GitMergeIntoWorktreeResult struct {
	HasConflicts bool   `json:"hasConflicts"`
	WorktreePath string `json:"worktreePath"`
}

// GitMergeIntoWorktreeActivity runs `git merge --no-commit --no-ff
// <sourceBranch>` in the given worktree directory. The merge is left
// un-finalized either way so the caller can run further steps before
// committing.
func GitMergeIntoWorktreeActivity(ctx context.Context, envContainer env.EnvContainer, params GitMergeIntoWorktreeParams) (GitMergeIntoWorktreeResult, error) {
	var result GitMergeIntoWorktreeResult
	if params.SourceBranch == "" {
		return result, fmt.Errorf("source branch is required")
	}
	dir := params.WorktreePath
	if dir == "" {
		dir = envContainer.Env.GetWorkingDirectory()
	}
	if dir == "" {
		return result, fmt.Errorf("worktree path is required")
	}
	result.WorktreePath = dir

	committerName, committerEmail := params.CommitterName, params.CommitterEmail
	if committerName == "" || committerEmail == "" {
		envType := envContainer.Env.GetType()
		if envType == env.EnvTypeLocal || envType == env.EnvTypeLocalGitWorktree {
			name, email, err := getGitUserConfig(ctx, envContainer)
			if err == nil {
				if committerName == "" {
					committerName = name
				}
				if committerEmail == "" {
					committerEmail = email
				}
			}
		}
	}
	envVars := buildGitEnvVars(committerName, committerEmail)

	cmd := fmt.Sprintf(
		"cd %s && git merge --no-commit --no-ff %s",
		shellQuote(dir), shellQuote(params.SourceBranch),
	)
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: envContainer,
		Command:      "sh",
		Args:         []string{"-c", cmd},
		EnvVars:      envVars,
	})
	if err != nil {
		return result, fmt.Errorf("failed to execute merge into worktree: %w", err)
	}
	if out.ExitStatus != 0 {
		if strings.Contains(out.Stdout, "CONFLICT") || strings.Contains(out.Stderr, "conflict") {
			result.HasConflicts = true
			return result, nil
		}
		return result, fmt.Errorf("merge into worktree failed: %s", strings.TrimSpace(out.Stderr+out.Stdout))
	}
	return result, nil
}
