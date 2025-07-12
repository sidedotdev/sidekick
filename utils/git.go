package utils

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// GetRepositoryPaths returns a list of repository directory candidates in order:
// 1. Current working directory
// 2. Git repository root directory
// 3. Git common directory (for worktrees)
// All paths returned are cleaned and absolute. Returns error if any git command fails.
func GetRepositoryPaths(ctx context.Context, currentDir string) ([]string, error) {
	// Get absolute path for current directory and evaluate symlinks
	absCurrentDir, err := filepath.Abs(currentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for current directory: %w", err)
	}
	absCurrentDir, err = filepath.EvalSymlinks(absCurrentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate symlinks for current directory: %w", err)
	}

	// Start with current directory
	paths := []string{absCurrentDir}

	// Get repository root using git rev-parse --show-toplevel
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = currentDir
	repoRootBytes, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get git repository root: %w", err)
	}
	repoRoot := strings.TrimSpace(string(repoRootBytes))
	absRepoRoot := repoRoot // Will be overwritten if repoRoot is not empty
	if repoRoot != "" {
		var err error
		absRepoRoot, err = filepath.Abs(repoRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for repository root: %w", err)
		}
		absRepoRoot, err = filepath.EvalSymlinks(absRepoRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate symlinks for repository root: %w", err)
		}
		if absRepoRoot != absCurrentDir {
			paths = append(paths, absRepoRoot)
		}
	}

	// Get git common directory using git rev-parse --git-common-dir
	cmd = exec.CommandContext(ctx, "git", "rev-parse", "--git-common-dir")
	cmd.Dir = currentDir
	commonDirBytes, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get git common directory: %w", err)
	}
	commonDir := strings.TrimSpace(string(commonDirBytes))
	if commonDir != "" {
		// If path is relative, resolve it relative to the repository root
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(repoRoot, commonDir)
		}
		// The common directory points to .git, we want its parent
		commonRepoDir := filepath.Dir(commonDir)
		absCommonRepoDir, err := filepath.Abs(commonRepoDir)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for common repository directory: %w", err)
		}
		absCommonRepoDir, err = filepath.EvalSymlinks(absCommonRepoDir)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate symlinks for common repository directory: %w", err)
		}
		// Only add common repo dir if it's not already covered by current dir or repo root
		// Check both exact matches and parent directory relationships
		if absCommonRepoDir != absCurrentDir && absCommonRepoDir != absRepoRoot &&
			!strings.HasPrefix(absCurrentDir, absCommonRepoDir+string(filepath.Separator)) &&
			!strings.HasPrefix(absRepoRoot, absCommonRepoDir+string(filepath.Separator)) {
			paths = append(paths, absCommonRepoDir)
		}
	}

	return paths, nil
}
