package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain sets up an isolated git configuration for the whole package's tests.
// Git operations that create commits or annotated tags (both in test setup and
// inside the activities under test) require a committer identity, which is not
// guaranteed to exist in CI containers. Pointing GIT_CONFIG_GLOBAL at a
// throwaway config with a default identity keeps these tests self-contained and
// independent of any machine-level git configuration: when GIT_CONFIG_GLOBAL is
// set, git never falls back to ~/.gitconfig, so HOME needs no override.
func TestMain(m *testing.M) {
	tempHome, err := os.MkdirTemp("", "sidekick-git-test")
	if err != nil {
		panic(err)
	}

	globalConfigPath := filepath.Join(tempHome, ".gitconfig")
	globalConfig := strings.Join([]string{
		"[user]",
		"\tname = Sidekick Test",
		"\temail = test@example.com",
		"[init]",
		"\tdefaultBranch = main",
		"[commit]",
		"\tgpgsign = false",
		"",
	}, "\n")
	if err := os.WriteFile(globalConfigPath, []byte(globalConfig), 0o644); err != nil {
		panic(err)
	}

	os.Setenv("GIT_CONFIG_GLOBAL", globalConfigPath)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	code := m.Run()
	_ = os.RemoveAll(tempHome)
	os.Exit(code)
}