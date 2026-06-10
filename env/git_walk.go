package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"sidekick/common"
	"sidekick/gitwalk"
)

// sliceOverlay is a trivial gitwalk.Overlay backed by a slice of Changes.
type sliceOverlay []gitwalk.Change

func (s sliceOverlay) Changes() []gitwalk.Change { return []gitwalk.Change(s) }

// walkCodeDirectory walks workingDir, preferring a gitwalk-backed pass
// over HEAD plus the working-copy overlay, and falling back to a
// filepath.WalkDir + IgnoreManager pass when the directory isn't inside
// a git repo or has no HEAD commit. Callbacks receive absolute paths.
func walkCodeDirectory(
	ctx context.Context,
	workingDir string,
	ignoreFileNames []string,
	handleEntry func(path string, isDir bool) error,
) error {
	info, err := os.Stat(workingDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("baseDirectory must be a directory")
	}

	handled, err := walkCodeDirectoryGit(ctx, workingDir, ignoreFileNames, handleEntry)
	if handled {
		return err
	}
	return walkCodeDirectoryFallback(workingDir, ignoreFileNames, handleEntry)
}

// walkCodeDirectoryGit walks via gitwalk over the local git checkout
// containing workingDir. Returns handled=false when workingDir is not
// inside a git repo or HEAD is missing, so the caller can use the
// non-git fallback.
func walkCodeDirectoryGit(
	ctx context.Context,
	workingDir string,
	ignoreFileNames []string,
	handleEntry func(path string, isDir bool) error,
) (bool, error) {
	repoRoot, ok := resolveRepoRoot(ctx, workingDir)
	if !ok {
		return false, nil
	}
	if !hasHEAD(ctx, repoRoot) {
		return false, nil
	}

	porcelain, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "status", "--porcelain=v1", "-uall", "-z").Output()
	if err != nil {
		return true, fmt.Errorf("git status: %w", err)
	}
	changes, err := gitwalk.ChangesFromPorcelainZ(porcelain, func(relPath string) (io.ReadCloser, error) {
		return os.Open(filepath.Join(repoRoot, filepath.FromSlash(relPath)))
	})
	if err != nil {
		return true, err
	}

	w, err := gitwalk.New(ctx, gitwalk.Options{
		RepoPath:        repoRoot,
		Ref:             "HEAD",
		Overlay:         sliceOverlay(changes),
		SideIgnoreFiles: ignoreFilesWithGitignore(ignoreFileNames),
	})
	if err != nil {
		return true, err
	}
	defer w.Close()

	// Locate workingDir relative to repoRoot. Resolve symlinks on both
	// sides so e.g. macOS /var vs /private/var doesn't yield a bogus
	// '..'-laden relative path that would exclude every entry.
	subPrefix, ok := relativeSubPath(repoRoot, workingDir)
	if !ok {
		return false, nil
	}

	return true, w.Walk(ctx, func(d gitwalk.DirEntry) error {
		p := d.Path()
		if p == "" {
			return nil
		}
		var relToWD string
		if subPrefix == "" {
			relToWD = p
		} else {
			switch {
			case p == subPrefix:
				return nil
			case strings.HasPrefix(p, subPrefix+"/"):
				relToWD = strings.TrimPrefix(p, subPrefix+"/")
			case d.IsDir() && strings.HasPrefix(subPrefix+"/", p+"/"):
				return nil
			default:
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		abs := filepath.Join(workingDir, filepath.FromSlash(relToWD))
		return handleEntry(abs, d.IsDir())
	})
}

// relativeSubPath returns the slash-separated path of workingDir
// underneath repoRoot, or "" when they refer to the same directory.
// The second return value is false when workingDir is outside repoRoot.
func relativeSubPath(repoRoot, workingDir string) (string, bool) {
	root, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		root = repoRoot
	}
	wd, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		wd = workingDir
	}
	rel, err := filepath.Rel(root, wd)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	if strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func walkCodeDirectoryFallback(
	workingDir string,
	ignoreFileNames []string,
	handleEntry func(path string, isDir bool) error,
) error {
	ignoreManager, err := common.NewIgnoreManager(workingDir, ignoreFileNames)
	if err != nil {
		return err
	}
	return filepath.WalkDir(workingDir, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == workingDir {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if ignoreManager.IsIgnored(p, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			for i, name := range ignoreFileNames {
				if err := ignoreManager.AddIgnoreFile(name, i, p); err != nil {
					return err
				}
			}
		}
		return handleEntry(p, entry.IsDir())
	})
}

func resolveRepoRoot(ctx context.Context, dir string) (string, bool) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	return root, true
}

func hasHEAD(ctx context.Context, repoRoot string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	return cmd.Run() == nil
}

// ignoreFilesWithGitignore ensures `.gitignore` is part of the
// side-ignore set so tracked-but-ignored files stay hidden, matching
// the legacy walker's behavior (git itself only applies .gitignore to
// untracked files).
func ignoreFilesWithGitignore(names []string) []string {
	for _, n := range names {
		if n == ".gitignore" {
			return names
		}
	}
	out := make([]string, 0, len(names)+1)
	out = append(out, ".gitignore")
	out = append(out, names...)
	return out
}
