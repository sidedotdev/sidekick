package testfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DirectGitPath bypasses macOS's developer-tools launcher without overriding
// explicitly selected Git installations or changing the lookup of other tools.
// dir must be a private directory that can hold a git symlink.
func DirectGitPath(dir string) (string, error) {
	path := os.Getenv("PATH")
	if runtime.GOOS != "darwin" {
		return path, nil
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return path, nil
	}
	gitPath, err = filepath.EvalSymlinks(gitPath)
	if err != nil {
		return "", fmt.Errorf("resolve test Git: %w", err)
	}
	if gitPath != "/usr/bin/git" {
		return path, nil
	}

	output, err := exec.Command("/usr/bin/xcrun", "--find", "git").Output()
	if err != nil {
		return "", fmt.Errorf("locate developer Git: %w", err)
	}
	target := strings.TrimSpace(string(output))
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("developer Git path is not absolute: %q", target)
	}
	target, err = exec.LookPath(target)
	if err != nil {
		return "", fmt.Errorf("locate developer Git executable: %w", err)
	}
	if target == gitPath {
		return path, nil
	}
	if err := os.Symlink(target, filepath.Join(dir, "git")); err != nil {
		return "", fmt.Errorf("link developer Git: %w", err)
	}
	return dir + string(os.PathListSeparator) + path, nil
}
