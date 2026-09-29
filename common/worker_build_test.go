package common

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestWorkerBuildID(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("a", 40)
	for _, tt := range []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{"missing", nil, "unknown"},
		{"clean", []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: sha}, {Key: "vcs.modified", Value: "false"}}, sha},
		{"dirty", []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: sha}, {Key: "vcs.modified", Value: "true"}}, "unknown"},
		{"unspecified cleanliness", []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: sha}}, "unknown"},
		{"missing revision", []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.modified", Value: "false"}}, "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := workerBuildID(tt.settings); got != tt.want {
				t.Fatalf("workerBuildID() = %q, want %q", got, tt.want)
			}
		})
	}
}