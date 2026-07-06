package git

import (
	"context"
	"path/filepath"

	"sidekick/env"
)

// HibernateWorktreeInput contains parameters for hibernating a worktree.
type HibernateWorktreeInput struct {
	EnvContainer env.EnvContainer `json:"envContainer"`
	WorktreePath string           `json:"worktreePath"`
	BranchName   string           `json:"branchName"`
}

// HibernateWorktreeOutput contains the result of a hibernation operation.
type HibernateWorktreeOutput struct {
	MetadataPath string `json:"metadataPath"`
	PatchPath    string `json:"patchPath"`
}

// HibernationMetadata stores information needed to restore a hibernated worktree.
// HibernationMetadata is an alias for the env-level type.
type HibernationMetadata = env.HibernationMetadata

const (
	HibernationMetadataFile = env.HibernationMetadataFile
	HibernationPatchFile    = env.HibernationPatchFile
)

// HibernateWorktreeActivity saves the working tree diff as a compressed patch,
// removes the git worktree link, and leaves only hibernation artifacts on disk.
//
// All file operations are performed through the environment abstraction so that
// hibernation works for local, DevPod, and OpenShell environments alike. The
// combined patch and staged patch are generated and compressed entirely inside
// the environment to avoid transferring large data through the Temporal worker.
//
// This is intended for worktrees that are inactive but not stale — the flow is
// still running but hasn't progressed in a while. Hibernation frees disk and git
// resources while preserving enough state to restore later.
//
// Delegates to env.HibernateEnv which handles locking, atomicity, and
// idempotency (re-hibernating an already-hibernated worktree is a no-op).
func HibernateWorktreeActivity(ctx context.Context, input HibernateWorktreeInput) (HibernateWorktreeOutput, error) {
	e := input.EnvContainer.Env
	_, err := env.HibernateEnv(ctx, e, input.BranchName)
	if err != nil {
		return HibernateWorktreeOutput{}, err
	}
	workDir := e.GetWorkingDirectory()
	return HibernateWorktreeOutput{
		MetadataPath: filepath.Join(workDir, HibernationMetadataFile),
		PatchPath:    filepath.Join(workDir, HibernationPatchFile),
	}, nil
}

// WakeWorktreeInput contains parameters for waking a hibernated worktree.
type WakeWorktreeInput struct {
	EnvContainer env.EnvContainer `json:"envContainer"`
}

// WakeWorktreeOutput contains the result of waking a hibernated worktree.
type WakeWorktreeOutput struct {
	BranchName string `json:"branchName"`
}

// WakeWorktreeActivity restores a hibernated worktree to the exact filesystem
// state it had before hibernation, to the best of our ability.
//
// Delegates to env.WakeHibernatedEnv which handles locking, atomicity, and
// idempotency (waking an already-woken worktree is a no-op).
//
// See env.WakeHibernatedEnv for full caveats.
func WakeWorktreeActivity(ctx context.Context, input WakeWorktreeInput) (WakeWorktreeOutput, error) {
	e := input.EnvContainer.Env
	metadata, err := env.WakeHibernatedEnv(ctx, e)
	if err != nil {
		return WakeWorktreeOutput{}, err
	}
	return WakeWorktreeOutput{BranchName: metadata.BranchName}, nil
}

// IsWorktreeHibernated checks if a worktree directory contains hibernation files.
func IsWorktreeHibernated(worktreePath string) bool {
	return env.IsHibernated(worktreePath)
}
