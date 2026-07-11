package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsActiveEnvNonLocal(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset or empty", value: "", want: false},
		{name: "local", value: "local", want: false},
		{name: "local_git_worktree", value: "local_git_worktree", want: false},
		{name: "devpod", value: "devpod", want: true},
		{name: "openshell", value: "openshell", want: true},
		{name: "future remote type", value: "some_future_remote", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(ActiveEnvTypeEnvVar, tt.value)
			assert.Equal(t, tt.want, IsActiveEnvNonLocal())
		})
	}
}
