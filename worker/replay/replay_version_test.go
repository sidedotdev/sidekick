package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestIsReplayableVersion(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	git("commit", "--allow-empty", "-m", "ancestor")
	ancestor := git("rev-parse", "HEAD")
	git("commit", "--allow-empty", "-m", "head")
	head := git("rev-parse", "HEAD")
	git("checkout", "--detach", ancestor)
	git("commit", "--allow-empty", "-m", "divergent")
	divergent := git("rev-parse", "HEAD")

	t.Setenv("GIT_DIR", dir+"/.git")
	t.Setenv("GIT_WORK_TREE", dir)
	for _, tt := range []struct {
		name    string
		current string
		build   string
		want    bool
	}{
		{"ancestor", head, ancestor, true},
		{"equal", head, head, true},
		{"divergent", head, divergent, false},
		{"missing current", "", ancestor, false},
		{"missing build", head, "", false},
		{"unknown build", head, "unknown", false},
		{"unavailable commit", head, strings.Repeat("1", 40), false},
		{"symbolic ref", head, "HEAD~1", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isReplayableVersion(tt.current, tt.build); got != tt.want {
				t.Fatalf("isReplayableVersion(%q, %q) = %v, want %v",
					tt.current, tt.build, got, tt.want)
			}
		})
	}
}
