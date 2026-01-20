package common

import (
	"os"
	"time"
)

const defaultTaskStartTimeout = 5 * time.Second

// GetTaskStartTimeout returns the configured timeout for task startup operations.
// Can be overridden by setting the SIDE_TASK_START_TIMEOUT environment variable
// to a duration string (e.g., "5s", "30s").
func GetTaskStartTimeout() time.Duration {
	timeoutStr := os.Getenv("SIDE_TASK_START_TIMEOUT")
	if timeoutStr == "" {
		return defaultTaskStartTimeout
	}
	duration, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return defaultTaskStartTimeout
	}
	return duration
}
