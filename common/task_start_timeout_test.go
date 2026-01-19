package common

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetTaskStartTimeout(t *testing.T) {
	t.Run("default timeout", func(t *testing.T) {
		os.Unsetenv("SIDE_TASK_START_TIMEOUT")
		timeout := GetTaskStartTimeout()
		assert.Equal(t, 5*time.Second, timeout)
	})

	t.Run("custom timeout", func(t *testing.T) {
		os.Setenv("SIDE_TASK_START_TIMEOUT", "30s")
		defer os.Unsetenv("SIDE_TASK_START_TIMEOUT")
		timeout := GetTaskStartTimeout()
		assert.Equal(t, 30*time.Second, timeout)
	})

	t.Run("short timeout", func(t *testing.T) {
		os.Setenv("SIDE_TASK_START_TIMEOUT", "100ms")
		defer os.Unsetenv("SIDE_TASK_START_TIMEOUT")
		timeout := GetTaskStartTimeout()
		assert.Equal(t, 100*time.Millisecond, timeout)
	})

	t.Run("invalid duration falls back to default", func(t *testing.T) {
		os.Setenv("SIDE_TASK_START_TIMEOUT", "invalid")
		defer os.Unsetenv("SIDE_TASK_START_TIMEOUT")
		timeout := GetTaskStartTimeout()
		assert.Equal(t, 5*time.Second, timeout)
	})
}
