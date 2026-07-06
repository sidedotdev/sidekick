// Command hibernate_worktrees reclaims disk by hibernating the oldest worktrees
// in a workspace. Hibernation stores a compressed diff of the working tree plus
// metadata and removes the git worktree directory contents, keeping the branch.
//
// It intentionally does NOT signal the owning flows: waking is filesystem
// driven, so a hibernated worktree auto-wakes the next time its flow runs any
// Env command. This makes the script safe to run out-of-band (even when the
// worker is down) purely to reclaim space.
//
// Roll out gradually with -dry-run (the default) to preview, then narrow with
// -limit N, -percent P, or -older-than-months M (e.g. hibernate everything idle
// for more than 2 months). Selection is always oldest-activity first.
package main

import (
	"context"
	"flag"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sidekick"
	"sidekick/domain"
	"sidekick/env"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// worktreeGitdirExists reports whether a linked worktree's backing gitdir
// (recorded in its ".git" file as "gitdir: <path>") still exists on disk. It
// returns false for orphaned worktrees whose main repository has been moved or
// deleted, which cannot be hibernated or woken.
func worktreeGitdirExists(worktreeDir string) bool {
	data, err := os.ReadFile(filepath.Join(worktreeDir, ".git"))
	if err != nil {
		return false
	}
	line := strings.TrimSpace(string(data))
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if gitdir == "" || gitdir == line {
		return false
	}
	_, err = os.Stat(gitdir)
	return err == nil
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	var workspaceId string
	var limit int
	var percent float64
	var olderThanMonths int
	var dryRun bool
	flag.StringVar(&workspaceId, "workspace", "", "Workspace ID to hibernate worktrees for")
	flag.IntVar(&limit, "limit", 0, "Hibernate at most this many of the oldest worktrees (0 = no count limit)")
	flag.Float64Var(&percent, "percent", 0, "Hibernate this percentage of the oldest worktrees (ignored when -older-than-months or -limit is set)")
	flag.IntVar(&olderThanMonths, "older-than-months", 0, "Hibernate every worktree whose last flow activity is older than this many months (takes precedence over -limit/-percent)")
	flag.BoolVar(&dryRun, "dry-run", true, "If true, only report which worktrees would be hibernated")
	flag.Parse()

	if workspaceId == "" {
		log.Fatal().Msg("missing required flag: -workspace")
	}

	service, err := sidekick.GetService()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize service")
	}

	ctx := context.Background()
	worktrees, err := service.GetWorktrees(ctx, workspaceId)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to list worktrees")
	}

	// Only consider real, on-disk, linked git worktrees that aren't already
	// hibernated. A linked worktree has a ".git" FILE pointing at the main
	// repo, whereas the main repo (and unrelated directories such as the
	// worktrees parent) have a ".git" directory or none at all. Requiring the
	// ".git" file plus a non-empty FlowId guards against ever tarring-and-
	// deleting anything that isn't an actual worktree.
	var eligible []domain.Worktree
	for _, wt := range worktrees {
		if wt.WorkingDirectory == "" || wt.FlowId == "" {
			continue
		}
		info, statErr := os.Stat(wt.WorkingDirectory)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if env.IsHibernated(wt.WorkingDirectory) {
			continue
		}
		gitInfo, gitErr := os.Stat(filepath.Join(wt.WorkingDirectory, ".git"))
		if gitErr != nil || gitInfo.IsDir() {
			continue
		}
		// Skip orphaned worktrees whose backing gitdir (main repo) is gone:
		// they can neither be hibernated nor woken and should instead be
		// pruned by stale-worktree cleanup.
		if !worktreeGitdirExists(wt.WorkingDirectory) {
			continue
		}
		eligible = append(eligible, wt)
	}

	// Order by last flow activity (most-idle first). The latest flow-action
	// timestamp is a much finer "last touched" signal than the task's Updated
	// time, and avoids an expensive filesystem walk of each worktree. Falls
	// back to the flow's task Updated time when a flow has no recorded actions.
	type worktreePlan struct {
		wt           domain.Worktree
		lastActivity time.Time
	}
	plans := make([]worktreePlan, 0, len(eligible))
	for _, wt := range eligible {
		var latest time.Time
		if actions, err := service.GetFlowActions(ctx, workspaceId, wt.FlowId); err == nil {
			for _, a := range actions {
				if a.Updated.After(latest) {
					latest = a.Updated
				}
				if a.Created.After(latest) {
					latest = a.Created
				}
			}
		}
		if latest.IsZero() {
			if flow, err := service.GetFlow(ctx, workspaceId, wt.FlowId); err == nil {
				if task, err := service.GetTask(ctx, workspaceId, flow.ParentId); err == nil {
					latest = task.Updated
				}
			}
		}
		plans = append(plans, worktreePlan{wt: wt, lastActivity: latest})
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].lastActivity.Before(plans[j].lastActivity)
	})

	// plans is sorted oldest-activity first, so any cutoff-based subset is a
	// contiguous prefix.
	count := len(plans)
	if olderThanMonths > 0 {
		cutoff := time.Now().AddDate(0, -olderThanMonths, 0)
		count = 0
		for _, p := range plans {
			if p.lastActivity.Before(cutoff) {
				count++
			}
		}
	} else if limit > 0 {
		if limit < count {
			count = limit
		}
	} else if percent > 0 {
		count = int(math.Ceil(float64(len(plans)) * percent / 100.0))
		if count > len(plans) {
			count = len(plans)
		}
	}
	selected := plans[:count]

	log.Info().
		Int("totalWorktrees", len(worktrees)).
		Int("eligible", len(eligible)).
		Int("selected", len(selected)).
		Int("olderThanMonths", olderThanMonths).
		Bool("dryRun", dryRun).
		Msg("hibernation plan")

	var hibernated, failed int
	for _, p := range selected {
		wt := p.wt
		taskId := ""
		if flow, err := service.GetFlow(ctx, workspaceId, wt.FlowId); err == nil {
			taskId = flow.ParentId
		}
		l := log.With().
			Str("flowId", wt.FlowId).
			Str("taskId", taskId).
			Str("branch", wt.Name).
			Str("dir", wt.WorkingDirectory).
			Time("lastActivity", p.lastActivity).
			Logger()

		if dryRun {
			l.Info().Msg("would hibernate")
			continue
		}

		e := &env.LocalEnv{WorkingDirectory: wt.WorkingDirectory}
		if _, err := env.HibernateEnv(ctx, e, wt.Name); err != nil {
			l.Error().Err(err).Msg("failed to hibernate worktree")
			failed++
			continue
		}
		hibernated++
		l.Info().Msg("hibernated worktree")
	}

	log.Info().
		Int("hibernated", hibernated).
		Int("failed", failed).
		Bool("dryRun", dryRun).
		Msg("hibernation complete")
}