package dev

import (
	"sidekick/coding/git"

	"go.temporal.io/sdk/workflow"
)

const globalStateKeyHibernated = "hibernated"

type HibernateSignal struct{}

// SetupHibernateHandler listens for the hibernate signal and triggers worktree
// hibernation. When received, it saves the worktree state as a compressed patch
// and removes the git worktree. The workflow resumes automatically when any
// Env.RunCommand call detects the hibernation sentinel and auto-wakes.
//
// Re-signaling an already-hibernated workflow is safe: HibernateEnv is
// idempotent at the filesystem level. We intentionally do not short-circuit on
// GlobalState because Env.RunCommand may have auto-woken the worktree without
// clearing the workflow-level flag.
func SetupHibernateHandler(dCtx DevContext) {
	signalChan := workflow.GetSignalChannel(dCtx, SignalNameHibernate)
	hibernateHandlerVersion := workflow.GetVersion(dCtx, "hibernate", workflow.DefaultVersion, 1)
	workflow.Go(dCtx.Context, func(ctx workflow.Context) {
		for {
			if hibernateHandlerVersion == workflow.DefaultVersion {
				selector := workflow.NewSelector(ctx)
				selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
					c.Receive(ctx, &HibernateSignal{})
					if dCtx.Worktree == nil || dCtx.EnvContainer == nil {
						return
					}
					hibernateWorktree(dCtx.WithContext(ctx))
				})
				selector.Select(ctx)
			} else {
				// Receive directly in this coroutine rather than via a Selector
				// callback: blocking on the hibernation activity inside a
				// callback panics with "trying to block on coroutine which is
				// already blocked". Direct receive also serializes handling of
				// re-sent hibernate signals.
				signalChan.Receive(ctx, &HibernateSignal{})
				if ctx.Err() != nil {
					return
				}
				if dCtx.Worktree == nil || dCtx.EnvContainer == nil {
					continue
				}
				hibernateWorktree(dCtx.WithContext(ctx))
			}
		}
	})
}

func hibernateWorktree(dCtx DevContext) {
	// Don't short-circuit on GlobalState alone: if Env.RunCommand auto-woke
	// the worktree, GlobalState may still say "hibernated" while the
	// filesystem sentinels are gone. HibernateEnv is idempotent at the
	// filesystem level and handles both cases correctly.
	err := workflow.ExecuteActivity(dCtx, git.HibernateWorktreeActivity, git.HibernateWorktreeInput{
		EnvContainer: *dCtx.EnvContainer,
		WorktreePath: dCtx.EnvContainer.Env.GetWorkingDirectory(),
		BranchName:   dCtx.Worktree.Name,
	}).Get(dCtx, nil)
	if err != nil {
		workflow.GetLogger(dCtx).Error("Failed to hibernate worktree", "error", err)
		return
	}
	dCtx.ExecContext.GlobalState.SetValue(globalStateKeyHibernated, true)
	dCtx.ExecContext.GlobalState.Paused = true
}

// clearHibernationGlobalState clears the workflow-level hibernation state
// without performing any filesystem operations. Safe to call unconditionally;
// no-op when not hibernated.
func clearHibernationGlobalState(dCtx DevContext) {
	val := dCtx.ExecContext.GlobalState.GetValue(globalStateKeyHibernated)
	if hibernated, _ := val.(bool); hibernated {
		dCtx.ExecContext.GlobalState.SetValue(globalStateKeyHibernated, nil)
		dCtx.ExecContext.GlobalState.Paused = false
	}
}

// WakeIfHibernated checks if the worktree is in a hibernated state (via global
// state) and restores it if so. Should be called before any workflow step that
// needs the worktree. Returns true if the worktree was hibernated and has been
// woken up.
//
// Note: the low-level Env.RunCommand implementations also check for hibernation
// via the on-disk sentinel file and wake automatically, so this function is
// primarily for clearing the workflow-level global state and pause flag.
func WakeIfHibernated(dCtx DevContext) (bool, error) {
	val := dCtx.ExecContext.GlobalState.GetValue(globalStateKeyHibernated)
	hibernated, _ := val.(bool)
	if !hibernated {
		return false, nil
	}

	var output git.WakeWorktreeOutput
	err := workflow.ExecuteActivity(dCtx, git.WakeWorktreeActivity, git.WakeWorktreeInput{
		EnvContainer: *dCtx.EnvContainer,
	}).Get(dCtx, &output)
	if err != nil {
		return true, err
	}

	dCtx.ExecContext.GlobalState.SetValue(globalStateKeyHibernated, nil)
	dCtx.ExecContext.GlobalState.Paused = false
	return true, nil
}
