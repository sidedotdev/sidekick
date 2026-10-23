package git

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"sidekick/env"

	"github.com/rs/zerolog/log"
)

const workerRefPrefix = "refs/remotes/side-worker/"

func syncGitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func syncGitScript(ctx context.Context, sshArgs []string, script string) (string, error) {
	out, err := env.RunSSHScript(ctx, sshArgs, script)
	if err != nil {
		return "", err
	}
	if out.ExitStatus != 0 {
		return "", fmt.Errorf("git sync script failed (exit %d): %s", out.ExitStatus, out.Stderr)
	}
	return strings.TrimSpace(out.Stdout), nil
}

func parseSyncRefs(output string) (map[string]string, error) {
	refs := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "refs/") {
			return nil, fmt.Errorf("invalid git ref advertisement: %q", line)
		}
		if len(fields[0]) != 40 && len(fields[0]) != 64 {
			return nil, fmt.Errorf("invalid git object ID: %q", fields[0])
		}
		for _, c := range fields[0] {
			if !strings.ContainsRune("0123456789abcdef", c) {
				return nil, fmt.Errorf("invalid git object ID: %q", fields[0])
			}
		}
		refs[fields[1]] = fields[0]
	}
	return refs, nil
}

func gitBackupRef(branch, oid string) string {
	return fmt.Sprintf("refs/sidekick/backups/%x/%s", sha256.Sum256([]byte(branch)), oid)
}

// Backups are immutable so a restored sandbox or rewritten branch cannot
// erase an earlier checkpoint, even when the same-name branch cannot advance.
func syncGitRepositoriesWithLock(ctx context.Context, sshArgs []string, localRepoDir, workingDirectory, checkoutLock string) error {
	quotedDir := shellQuote(workingDirectory)
	advertisement, err := syncGitScript(ctx, sshArgs, "set -e; cd "+quotedDir+`; branch=$(git symbolic-ref -q HEAD || echo -); if [ "$branch" = - ]; then git rev-parse --verify HEAD; else git rev-parse --verify "$branch"; fi; printf '%s\n' "$branch"; git for-each-ref --format='%(objectname) %(refname)' refs/heads/ refs/remotes/side-worker/`)
	if err != nil {
		return err
	}
	lines := strings.SplitN(advertisement, "\n", 3)
	if len(lines) < 3 {
		return fmt.Errorf("incomplete git sync advertisement")
	}
	tip, activeRef := lines[0], lines[1]
	remoteRefs, err := parseSyncRefs(lines[2])
	if err != nil {
		return err
	}
	// Validate detached HEAD too; it may not appear among the advertised refs.
	if _, err := parseSyncRefs(tip + " refs/sidekick/head"); err != nil {
		return err
	}
	backupRef := gitBackupRef(activeRef, tip)
	localAdvertisement, err := syncGitCommand(ctx, localRepoDir, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads/", backupRef)
	if err != nil {
		return err
	}
	localRefs, err := parseSyncRefs(localAdvertisement)
	if err != nil {
		return err
	}

	if localRefs[backupRef] != tip {
		if _, err := syncGitCommand(ctx, localRepoDir, "cat-file", "-e", tip+"^{commit}"); err == nil {
			if _, err := syncGitCommand(ctx, localRepoDir, "update-ref", backupRef, tip, ""); err != nil {
				return err
			}
		} else if err := env.FetchRemoteRefToLocal(ctx, sshArgs, workingDirectory, localRepoDir, tip+":"+backupRef); err != nil {
			return err
		}
	}
	if strings.HasPrefix(activeRef, "refs/heads/") && localRefs[activeRef] != tip {
		// Fetch's normal fast-forward and checked-out-branch guards apply to
		// every worktree, without touching the worker's index or files.
		if _, err := syncGitCommand(ctx, localRepoDir, "fetch", "--no-tags", "--no-write-fetch-head", ".", backupRef+":"+activeRef); err != nil {
			log.Warn().Err(err).Str("branch", activeRef).Str("backupRef", backupRef).
				Msg("git checkpoint retained separately; worker branch could not advance")
		}
	}

	var branches []string
	for ref := range localRefs {
		if strings.HasPrefix(ref, "refs/heads/") && ref != activeRef {
			branches = append(branches, strings.TrimPrefix(ref, "refs/heads/"))
		}
	}
	sort.Strings(branches)
	var refspecs, leases, preserve, advance []string
	for _, branch := range branches {
		headRef := "refs/heads/" + branch
		trackingRef := workerRefPrefix + branch
		oid := localRefs[headRef]
		if old := remoteRefs[trackingRef]; old != oid {
			if old != "" {
				historyRef := fmt.Sprintf("refs/sidekick/worker-history/%x/%s", sha256.Sum256([]byte(headRef)), old)
				preserve = append(preserve, "git update-ref "+shellQuote(historyRef)+" "+shellQuote(old))
			}
			refspecs = append(refspecs, oid+":"+trackingRef)
			leases = append(leases, "--force-with-lease="+trackingRef+":"+old)
		}
		if remoteRefs[headRef] != oid {
			advance = append(advance, branch)
		}
	}
	if len(refspecs) > 0 {
		if len(preserve) > 0 {
			if _, err := syncGitScript(ctx, sshArgs, "set -e; cd "+quotedDir+"; "+strings.Join(preserve, "; ")); err != nil {
				return err
			}
		}
		dest, opts := env.SplitSSHDestination(sshArgs)
		if dest == "" {
			return fmt.Errorf("missing git sync SSH destination")
		}
		args := append([]string{"-C", localRepoDir, "push", "--quiet"}, leases...)
		args = append(args, dest+":"+workingDirectory)
		args = append(args, refspecs...)
		if out, err := env.RunGitOverSSH(ctx, opts, args...); err != nil {
			return fmt.Errorf("refresh side-worker refs: %w: %s", err, out)
		}
	}
	if len(advance) > 0 {
		var quotedBranches []string
		for _, branch := range advance {
			quotedBranches = append(quotedBranches, shellQuote(branch))
		}
		script := "set -e; cd " + quotedDir + "; checkout_lock=" + shellQuote(checkoutLock) + "; " +
			fmt.Sprintf(advanceWorkerBranchesScript, strings.Join(quotedBranches, " "))
		output, err := syncGitScript(ctx, sshArgs, script)
		if err != nil {
			return err
		}
		if output != "" {
			log.Debug().Str("details", output).Msg("side-worker branch refresh")
		}
	}
	return nil
}

// Linked worktrees belong to active flows. Background passes never acquire
// the command fence; only checkpoints may advance the main checkout in place.
const advanceWorkerBranchesScript = `
checkout_allowed=false
if [ -n "$checkout_lock" ] && command -v flock >/dev/null 2>&1; then
	exec 9>>"$checkout_lock"
	if flock -xn 9; then checkout_allowed=true; fi
fi
main=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
for branch in %s; do
	ref="refs/heads/$branch"
	source="refs/remotes/side-worker/$branch"
	if git show-ref --verify --quiet "$ref"; then
		if ! git merge-base --is-ancestor "$ref" "$source"; then
			echo "Skipped divergent $ref"
			continue
		fi
	fi
	wt=$(git -c core.quotePath=false worktree list --porcelain | awk -v b="$ref" '$1=="worktree"{p=substr($0,10)} $0==("branch " b){print p; exit}')
	if [ -n "$wt" ]; then
		if [ "$wt" != "$main" ] || [ "$checkout_allowed" != true ]; then
			echo "Skipped active worktree $ref"
			continue
		fi
		if [ -n "$(git -C "$wt" status --porcelain --untracked-files=normal)" ]; then
			echo "Skipped dirty $ref"
			continue
		fi
		busy=false
		for state in MERGE_HEAD CHERRY_PICK_HEAD REVERT_HEAD rebase-merge rebase-apply sequencer; do
			if [ -e "$(git -C "$wt" rev-parse --path-format=absolute --git-path "$state")" ]; then busy=true; fi
		done
		if [ "$busy" = true ]; then
			echo "Skipped in-progress operation on $ref"
			continue
		fi
		if ! git -C "$wt" -c core.hooksPath=/dev/null -c merge.autoStash=false merge --ff-only --no-edit --no-overwrite-ignore "$source"; then
			echo "Skipped unsafe update to $ref"
		fi
	else
		if ! git fetch --no-tags --no-write-fetch-head . "$source:$ref"; then
			echo "Skipped unsafe update to $ref"
		fi
	fi
done
`
