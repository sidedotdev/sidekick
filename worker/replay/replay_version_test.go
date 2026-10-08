package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestIsDivergentBuild(t *testing.T) {
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
		{"ancestor", head, ancestor, false},
		{"equal", head, head, false},
		{"dirty ancestor", head, ancestor + "-dirty", false},
		{"divergent", head, divergent, true},
		{"dirty divergent", head, divergent + "-dirty", true},
		{"unavailable commit", head, strings.Repeat("1", 40), true},
		{"missing current", "", divergent, false},
		{"missing build", head, "", false},
		{"unknown build", head, "unknown", false},
		{"sdk binary checksum", head, "8ac10dfc0d9a346a97e3f39170dc3dc8", false},
		{"symbolic ref", head, "HEAD~1", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDivergentBuild(tt.current, tt.build); got != tt.want {
				t.Fatalf("isDivergentBuild(%q, %q) = %v, want %v",
					tt.current, tt.build, got, tt.want)
			}
		})
	}
}
