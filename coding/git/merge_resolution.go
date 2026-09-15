package git

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"sidekick/env"
)

func (r mergeRepository) script(ctx context.Context, dir, script string) (string, error) {
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: r.envContainer,
		Command:      "sh",
		Args:         []string{"-c", "cd " + shellQuote(dir) + " && (" + script + ")"},
	})
	if err != nil {
		return "", err
	}
	if out.ExitStatus != 0 {
		return "", fmt.Errorf("merge repository command failed: %s", strings.TrimSpace(out.Stderr+out.Stdout))
	}
	return strings.TrimSpace(out.Stdout), nil
}

func (r mergeRepository) containsWorktree(ctx context.Context, dir string) (bool, error) {
	worktrees, err := ListWorktrees(ctx, r.envContainer)
	if err != nil {
		return false, err
	}
	for _, wt := range worktrees {
		if wt.Path == dir {
			return true, nil
		}
	}
	// A missing path is distinct from an environment execution failure.
	path, err := r.script(ctx, r.envContainer.Env.GetWorkingDirectory(),
		"if [ -d "+shellQuote(dir)+" ]; then cd "+shellQuote(dir)+" && pwd -P; fi")
	if err != nil {
		return false, err
	}
	for _, wt := range worktrees {
		if wt.Path == path {
			return true, nil
		}
	}
	return false, nil
}

func (c *mergeCoordinator) resolutionTarget(ctx context.Context, transport mergeTransport, dir, baseStash, receipt string) (mergeRepository, bool, error) {
	local, err := c.repository.containsWorktree(ctx, dir)
	if err != nil {
		return mergeRepository{}, false, err
	}
	if _, same := transport.(sameRepositoryMergeTransport); same {
		if !local {
			return mergeRepository{}, false, fmt.Errorf("target is not a repository worktree: %s", dir)
		}
		return c.repository, false, nil
	}
	host := transport.targetRepository()
	onHost, err := host.containsWorktree(ctx, dir)
	if err != nil {
		return mergeRepository{}, false, err
	}
	if local && onHost {
		sourceReceipt, err := c.repository.script(ctx, dir, "git rev-parse --verify --quiet "+shellQuote(receipt)+" || test $? = 1")
		if err != nil {
			return mergeRepository{}, false, err
		}
		hostReceipt, err := host.script(ctx, dir, "git rev-parse --verify --quiet "+shellQuote(receipt)+" || test $? = 1")
		if err != nil {
			return mergeRepository{}, false, err
		}
		if (sourceReceipt != "") != (hostReceipt != "") {
			if sourceReceipt != "" {
				return c.repository, false, nil
			}
			return host, true, nil
		}
		if baseStash != "" {
			sourceOwns, err := c.repository.ownsStash(ctx, baseStash)
			if err != nil {
				return mergeRepository{}, false, err
			}
			hostOwns, err := host.ownsStash(ctx, baseStash)
			if err != nil {
				return mergeRepository{}, false, err
			}
			if sourceOwns != hostOwns {
				if sourceOwns {
					return c.repository, false, nil
				}
				return host, true, nil
			}
		}
		return mergeRepository{}, false, fmt.Errorf("ambiguous target worktree: %s", dir)
	}
	if onHost {
		return host, true, nil
	}
	if local {
		return c.repository, false, nil
	}
	return mergeRepository{}, false, fmt.Errorf("target is not a source or host repository worktree: %s", dir)
}

func (c *mergeCoordinator) returnResolution(ctx context.Context, transport mergeTransport, params GitTransferWorktreeChangesParams) error {
	if params.SourceWorktreePath == "" || params.TargetWorktreePath == "" {
		return fmt.Errorf("source and target worktree paths are required to transfer changes")
	}
	source := c.repository
	member, err := source.containsWorktree(ctx, params.SourceWorktreePath)
	if err != nil {
		return err
	}
	if !member {
		return fmt.Errorf("source is not a repository worktree: %s", params.SourceWorktreePath)
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(params.SourceWorktreePath+"\x00"+params.TargetWorktreePath+"\x00"+params.BaseStashSha)))
	ref := "refs/sidekick-merge/resolution/" + key
	doneRef := ref + "-delivered"
	target, toHost, err := c.resolutionTarget(ctx, transport, params.TargetWorktreePath, params.BaseStashSha, doneRef)
	if err != nil {
		return err
	}
	sourceDir, targetDir := params.SourceWorktreePath, params.TargetWorktreePath

	delivered, err := target.script(ctx, targetDir, "git rev-parse --verify --quiet "+shellQuote(doneRef)+" || test $? = 1")
	if err != nil {
		return err
	}
	if delivered != "" && params.BaseStashSha == "" {
		dirty, err := worktreeHasUncommittedChanges(ctx, source.envContainer, sourceDir)
		if err != nil {
			return err
		}
		if dirty {
			if _, err := target.script(ctx, targetDir, "git update-ref -d "+shellQuote(doneRef)+" && git update-ref -d "+shellQuote(ref+"-expected")); err != nil {
				return err
			}
			delivered = ""
		}
	}
	if delivered == "" {
		legacy, err := source.script(ctx, sourceDir, "git stash list --format=%gs")
		if err != nil {
			return err
		}
		for _, line := range strings.Split(legacy, "\n") {
			if strings.HasSuffix(line, ": sidekick-resolution-transfer") {
				return fmt.Errorf("legacy resolution transfer has ambiguous ownership; preserved stashes require recovery")
			}
		}
		// The stash message closes the interruption window between stash push
		// and pinning its commit under the operation's private ref.
		message := "sidekick-resolution-" + key
		script := fmt.Sprintf(`
sha=$(git rev-parse --verify --quiet %s) || {
	sha=$(git stash list --format='%%H %%gs' | awk -v suffix=%s 'substr($0,length($0)-length(suffix)+1)==suffix {print $1; exit}')
}
if [ -z "$sha" ]; then
	status=$(git status --porcelain) || exit
	if [ -n "$status" ]; then
		git stash push --include-untracked -m %s >&2 || exit
		sha=$(git rev-parse refs/stash) || exit
	fi
fi
if [ -n "$sha" ]; then
	git update-ref %s "$sha" || exit
	printf '%%s' "$sha"
fi`, shellQuote(ref), shellQuote(": "+message), shellQuote(message), shellQuote(ref))
		stash, err := source.script(ctx, sourceDir, script)
		if err != nil {
			return err
		}
		if stash != "" {
			if toHost {
				if err := transport.copyRef(ctx, ref, true); err != nil {
					return fmt.Errorf("resolution remains preserved at %s: %w", ref, err)
				}
			}
			if err := target.deliverResolution(ctx, targetDir, ref, stash); err != nil {
				return err
			}
			delivered = stash
		} else {
			delivered, err = target.script(ctx, targetDir, "git rev-parse HEAD")
			if err != nil {
				return err
			}
			if _, err := target.script(ctx, targetDir, "git update-ref "+shellQuote(doneRef)+" "+shellQuote(delivered)); err != nil {
				return err
			}
		}
	}
	if err := dropStashBySha(ctx, source.envContainer, sourceDir, delivered, nil); err != nil {
		return err
	}
	if params.BaseStashSha != "" {
		if err := dropStashBySha(ctx, target.envContainer, targetDir, params.BaseStashSha, nil); err != nil {
			return err
		}
		if err := target.clearMergeState(ctx, targetDir, params.BaseStashSha); err != nil {
			return err
		}
		if toHost {
			if err := source.clearMergeState(ctx, sourceDir, params.BaseStashSha); err != nil {
				return err
			}
		}
	}
	if _, err := source.script(ctx, sourceDir, "git update-ref -d "+shellQuote(ref)); err != nil {
		return err
	}
	if toHost {
		_, err = target.script(ctx, targetDir, "git update-ref -d "+shellQuote(ref))
	}
	return err
}

func (r mergeRepository) deliverResolution(ctx context.Context, dir, ref, stash string) error {
	expectedRef := ref + "-expected"
	expected, err := r.script(ctx, dir, "git rev-parse --verify --quiet "+shellQuote(expectedRef)+" || test $? = 1")
	if err != nil {
		return err
	}
	if expected == "" {
		dirty, err := worktreeHasUncommittedChanges(ctx, r.envContainer, dir)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("target has uncommitted changes; resolution preserved at %s", ref)
		}
		expected, err = r.prepareStashTree(ctx, dir, stash, expectedRef, false)
		if err != nil {
			return fmt.Errorf("cannot prepare resolution delivery; stash preserved: %w", err)
		}
	}
	current, err := r.script(ctx, dir, `
index=$(mktemp) || exit
trap 'rm -f "$index"' EXIT
rm -f "$index"
export GIT_INDEX_FILE="$index"
git read-tree HEAD && git add -A && git write-tree`)
	if err != nil {
		return err
	}
	if current != expected {
		dirty, err := worktreeHasUncommittedChanges(ctx, r.envContainer, dir)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("incomplete delivery or intervening target edits; resolution preserved at %s", ref)
		}
		if _, err := r.script(ctx, dir, "git stash apply "+shellQuote(stash)); err != nil {
			return fmt.Errorf("resolution application incomplete; stash preserved at %s: %w", ref, err)
		}
	}
	_, err = r.script(ctx, dir, "git update-ref "+shellQuote(ref+"-delivered")+" "+shellQuote(stash))
	return err
}

func (c *mergeCoordinator) relocateStashConflict(ctx context.Context, transport mergeTransport, target mergeRepository, targetDir, sourceDir, stash string, fromHost bool) error {
	ref := "refs/sidekick-merge/base/" + stash
	cleaned, err := target.checkRelocationCleanup(ctx, targetDir, ref, stash)
	if err != nil {
		return err
	}
	if cleaned {
		return nil
	}
	if _, err := target.script(ctx, targetDir, "git update-ref "+shellQuote(ref)+" "+shellQuote(stash)); err != nil {
		return err
	}
	if fromHost {
		if err := transport.copyRef(ctx, ref, false); err != nil {
			return fmt.Errorf("base stash preserved at %s: %w", ref, err)
		}
	}
	source := c.repository
	receipt := ref + "-relocated"
	relocated, err := source.script(ctx, sourceDir, "git rev-parse --verify --quiet "+shellQuote(receipt)+" || test $? = 1")
	if err != nil {
		return err
	}
	if relocated == "" {
		dirty, err := worktreeHasUncommittedChanges(ctx, source.envContainer, sourceDir)
		if err != nil {
			return err
		}
		expectedRef := ref + "-source-tree"
		expected, err := source.script(ctx, sourceDir, "git rev-parse --verify --quiet "+shellQuote(expectedRef)+" || test $? = 1")
		if err != nil {
			return err
		}
		if expected == "" {
			if dirty {
				return fmt.Errorf("source has edits; base stash preserved at %s", ref)
			}
			expected, err = source.prepareStashTree(ctx, sourceDir, stash, expectedRef, true)
			if err != nil {
				return err
			}
		}
		if dirty {
			current, err := source.snapshotTree(ctx, sourceDir)
			if err != nil {
				return err
			}
			if current != expected {
				return fmt.Errorf("source differs from expected relocation; stash preserved at %s", ref)
			}
		}
		if !dirty {
			out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
				EnvContainer: source.envContainer,
				Command:      "git",
				Args:         []string{"-C", sourceDir, "stash", "apply", stash},
			})
			if err != nil {
				return err
			}
			if out.ExitStatus != 0 {
				unmerged, err := source.script(ctx, sourceDir, "git diff --name-only --diff-filter=U")
				if err != nil {
					return err
				}
				if unmerged == "" {
					return fmt.Errorf("failed to relocate base stash: %s", out.Stderr+out.Stdout)
				}
			}
		}
		if _, err := source.script(ctx, sourceDir, "git update-ref "+shellQuote(receipt)+" "+shellQuote(stash)); err != nil {
			return err
		}
	}
	cleaned, err = target.checkRelocationCleanup(ctx, targetDir, ref, stash)
	if err != nil || cleaned {
		return err
	}
	// Remove only restored untracked files that still match the owned stash.
	_, err = target.script(ctx, targetDir, fmt.Sprintf(`
if git rev-parse --verify --quiet %s^3 >/dev/null; then
	git ls-tree -rz --name-only %s^3 | xargs -0 sh -c '
		stash=$1
		shift
		for file do
			if git ls-files --error-unmatch -- "$file" >/dev/null 2>&1; then continue; fi
			if [ -f "$file" ] && [ ! -L "$file" ]; then
				actual=$(git hash-object -- "$file") || exit
				expected=$(git rev-parse "$stash^3:$file") || exit
				if [ "$actual" = "$expected" ]; then rm -- "$file" || exit; fi
			fi
		done
	' sh %s || exit
fi
git reset --hard HEAD`, shellQuote(stash), shellQuote(stash), shellQuote(stash)))
	return err
}

func (r mergeRepository) snapshotTree(ctx context.Context, dir string) (string, error) {
	return r.script(ctx, dir, `
index=$(mktemp) || exit
trap 'rm -f "$index"' EXIT
rm -f "$index"
export GIT_INDEX_FILE="$index"
git read-tree HEAD && git add -A && git write-tree`)
}

func (r mergeRepository) prepareStashTree(ctx context.Context, dir, stash, ref string, allowConflicts bool) (string, error) {
	conflicts := "exit 1"
	if allowConflicts {
		conflicts = `test -n "$(git -C "$tmp" diff --name-only --diff-filter=U)" || exit 1`
	}
	return r.script(ctx, dir, fmt.Sprintf(`
tmp=$(mktemp -d) || exit
trap 'git worktree remove --force "$tmp" >/dev/null 2>&1; rm -rf "$tmp"' EXIT
git worktree add --detach "$tmp" HEAD >&2 || exit
git -C "$tmp" stash apply %s >&2 || { %s; }
git -C "$tmp" add -A || exit
tree=$(git -C "$tmp" write-tree) || exit
git update-ref %s "$tree" || exit
printf '%%s' "$tree"`, shellQuote(stash), conflicts, shellQuote(ref)))
}

func (r mergeRepository) captureMergeStash(ctx context.Context, dir, source, target string) (string, string, error) {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(dir+"\x00"+source+"\x00"+target)))
	ref := "refs/sidekick-merge/pending/" + key
	message := "sidekick-merge-" + key
	sha, err := r.script(ctx, dir, fmt.Sprintf(`
sha=$(git rev-parse --verify --quiet %s) || {
	sha=$(git stash list --format='%%H %%gs' | awk -v suffix=%s 'substr($0,length($0)-length(suffix)+1)==suffix {print $1; exit}')
}
if [ -z "$sha" ]; then
	status=$(git status --porcelain) || exit
	if [ -n "$status" ]; then
		git stash push --include-untracked -m %s >&2 || exit
		sha=$(git stash list --format='%%H %%gs' | awk -v suffix=%s 'substr($0,length($0)-length(suffix)+1)==suffix {print $1; exit}')
		test -n "$sha" || exit 1
	fi
fi
if [ -n "$sha" ]; then
	git update-ref %s "$sha" || exit
	printf '%%s' "$sha"
fi`, shellQuote(ref), shellQuote(": "+message), shellQuote(message), shellQuote(": "+message), shellQuote(ref)))
	return sha, ref, err
}

func restoreOwnedMergeStash(ctx context.Context, container env.EnvContainer, dir, sha, ref string, vars []string) (bool, error) {
	repository := mergeRepository{envContainer: container}
	restored, err := repository.restoredMergeStash(ctx, dir, sha, ref)
	if err != nil {
		return false, err
	}
	if restored {
		return false, repository.finishMergeRestore(ctx, dir, sha, ref, vars)
	}
	out, err := env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
		EnvContainer: container,
		Command:      "git",
		Args:         []string{"-C", dir, "stash", "apply", "--index", sha},
		EnvVars:      vars,
	})
	if err != nil {
		return false, err
	}
	if out.ExitStatus != 0 {
		repository := mergeRepository{envContainer: container}
		unmerged, inspectErr := repository.script(ctx, dir, "git diff --name-only --diff-filter=U")
		if inspectErr != nil {
			return false, inspectErr
		}
		if unmerged != "" {
			return true, nil
		}
		if strings.Contains(out.Stderr+out.Stdout, "Try without --index") {
			if _, err := repository.script(ctx, dir, "git diff --cached --quiet HEAD"); err != nil {
				return false, fmt.Errorf("stash index restoration incomplete; stash %s preserved: %w", sha, err)
			}
			out, err = env.EnvRunCommandActivity(ctx, env.EnvRunCommandActivityInput{
				EnvContainer: container,
				Command:      "git",
				Args:         []string{"-C", dir, "stash", "apply", sha},
				EnvVars:      vars,
			})
			if err != nil {
				return false, err
			}
			if out.ExitStatus != 0 {
				unmerged, err = repository.script(ctx, dir, "git diff --name-only --diff-filter=U")
				if err != nil {
					return false, err
				}
				if unmerged != "" {
					return true, nil
				}
			}
		}
		if out.ExitStatus != 0 {
			return false, fmt.Errorf("owned stash %s remains preserved: %s", sha, out.Stderr+out.Stdout)
		}
	}
	return false, repository.finishMergeRestore(ctx, dir, sha, ref, vars)
}

func (r mergeRepository) ownsStash(ctx context.Context, sha string) (bool, error) {
	stashes, err := r.script(ctx, r.envContainer.Env.GetWorkingDirectory(), "git stash list --format=%H")
	if err != nil {
		return false, err
	}
	for _, entry := range strings.Split(stashes, "\n") {
		if entry == sha {
			return true, nil
		}
	}
	return false, nil
}

func (r mergeRepository) clearMergeState(ctx context.Context, dir, stash string) error {
	baseRef := "refs/sidekick-merge/base/" + stash
	_, err := r.script(ctx, dir, fmt.Sprintf(`
git for-each-ref --format='%%(refname) %%(objectname)' refs/sidekick-merge/pending |
while read -r ref sha; do
	if [ "$sha" = %s ]; then
		printf 'delete %%s %%s\ndelete %%s\ndelete %%s\n' "$ref" "$sha" "$ref-restore-tree" "$ref-restore-index" | git update-ref --stdin || exit
	fi
done || exit
git update-ref -d %s &&
git update-ref -d %s &&
git update-ref -d %s &&
git update-ref -d %s &&
git update-ref -d %s &&
git update-ref -d %s`, shellQuote(stash), shellQuote(baseRef), shellQuote(baseRef+"-relocated"), shellQuote(baseRef+"-source-tree"), shellQuote(baseRef+"-target-tree"), shellQuote(baseRef+"-target-head"), shellQuote(baseRef+"-target-index")))
	return err
}

func (r mergeRepository) restoredMergeStash(ctx context.Context, dir, sha, ref string) (bool, error) {
	expectedRef := ref + "-restore-tree"
	indexRef := ref + "-restore-index"
	expected, err := r.script(ctx, dir, "git rev-parse --verify --quiet "+shellQuote(expectedRef)+" || test $? = 1")
	if err != nil {
		return false, err
	}
	if expected == "" {
		expected, err = r.script(ctx, dir, fmt.Sprintf(`
tmp=$(mktemp -d) || exit
trap 'git worktree remove --force "$tmp" >/dev/null 2>&1; rm -rf "$tmp"' EXIT
git worktree add --detach "$tmp" HEAD >&2 || exit
if ! git -C "$tmp" stash apply --index %s >&2; then exit 0; fi
index=$(git -C "$tmp" write-tree) || exit
git -C "$tmp" add -A || exit
tree=$(git -C "$tmp" write-tree) || exit
git update-ref %s "$index" && git update-ref %s "$tree" || exit
printf '%%s' "$tree"`, shellQuote(sha), shellQuote(indexRef), shellQuote(expectedRef)))
		if err != nil || expected == "" {
			return false, err
		}
	}
	current, err := r.snapshotTree(ctx, dir)
	if err != nil || current != expected {
		return false, err
	}
	index, err := r.script(ctx, dir, "git write-tree")
	if err != nil {
		return false, err
	}
	expectedIndex, err := r.script(ctx, dir, "git rev-parse "+shellQuote(indexRef))
	return index == expectedIndex, err
}

func (r mergeRepository) finishMergeRestore(ctx context.Context, dir, sha, ref string, vars []string) error {
	if err := dropStashBySha(ctx, r.envContainer, dir, sha, vars); err != nil {
		return err
	}
	_, err := r.script(ctx, dir, fmt.Sprintf("printf 'delete %%s %%s\\ndelete %%s\\ndelete %%s\\n' %s %s %s %s | git update-ref --stdin",
		shellQuote(ref), shellQuote(sha), shellQuote(ref+"-restore-tree"), shellQuote(ref+"-restore-index")))
	return err
}

func (r mergeRepository) checkRelocationCleanup(ctx context.Context, dir, ref, stash string) (bool, error) {
	tree, err := r.snapshotTree(ctx, dir)
	if err != nil {
		return false, err
	}
	state, err := r.script(ctx, dir, fmt.Sprintf(`
head=$(git rev-parse HEAD) || exit
index=$(git ls-files --stage -z | git hash-object -w --stdin) || exit
saved=$(git rev-parse --verify --quiet %s) || {
	printf 'update %%s %%s\nupdate %%s %%s\nupdate %%s %%s\n' %s %s %s "$index" %s "$head" | git update-ref --stdin >&2 || exit
	saved=%s
}
savedHead=$(git rev-parse %s) || exit
savedIndex=$(git rev-parse %s) || exit
test "$head" = "$savedHead" || exit 1
if [ %s = "$(git rev-parse HEAD^{tree})" ] && git diff --cached --quiet HEAD; then
	printf cleaned
	exit
fi
test "$index" = "$savedIndex" || exit 1
tmp=$(mktemp) || exit
trap 'rm -f "$tmp"' EXIT
rm -f "$tmp"
export GIT_INDEX_FILE="$tmp"
git read-tree %s || exit
if git rev-parse --verify --quiet %s^3 >/dev/null; then
	git ls-tree -rz --name-only %s^3 | xargs -0 sh -c '
		stash=$1
		saved=$2
		shift 2
		for file do
			if [ ! -e "$file" ] && [ ! -L "$file" ]; then
				owned=$(git rev-parse "$stash^3:$file") || exit
				before=$(git rev-parse "$saved:$file") || exit
				test "$owned" = "$before" || exit 1
				git ls-tree -z "$stash^3" -- "$file" | git update-index -z --index-info || exit
			fi
		done
	' sh %s "$saved" || exit
fi
test "$(git write-tree)" = "$saved" || exit 1
printf pending`, shellQuote(ref+"-target-tree"),
		shellQuote(ref+"-target-tree"), shellQuote(tree), shellQuote(ref+"-target-index"), shellQuote(ref+"-target-head"), shellQuote(tree),
		shellQuote(ref+"-target-head"), shellQuote(ref+"-target-index"), shellQuote(tree), shellQuote(tree),
		shellQuote(stash), shellQuote(stash), shellQuote(stash)))
	if err != nil {
		return false, fmt.Errorf("target changed since relocation began; refusing destructive cleanup: %w", err)
	}
	return state == "cleaned", nil
}
