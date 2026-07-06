package utils

import (
	"os"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/worker"
)

// TestDeadlockDetectionTimeoutEnvVar overrides, when set to a Go duration string
// (e.g. "1s"), the deadlock-detection timeout applied to Temporal test
// environments. Lower it to surface genuine deadlock sources that the lenient
// default would otherwise hide.
const TestDeadlockDetectionTimeoutEnvVar = "SIDE_TEST_DEADLOCK_DETECTION_TIMEOUT"

// defaultTestDeadlockDetectionTimeout is intentionally lenient so that slow or
// loaded CI machines don't trip the detector with false positives.
const defaultTestDeadlockDetectionTimeout = 30 * time.Second

// TestDeadlockDetectionTimeout returns the deadlock-detection timeout to apply
// to Temporal test environments, honoring TestDeadlockDetectionTimeoutEnvVar.
func TestDeadlockDetectionTimeout() time.Duration {
	raw := os.Getenv(TestDeadlockDetectionTimeoutEnvVar)
	if raw == "" {
		return defaultTestDeadlockDetectionTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		log.Warn().Err(err).Str("value", raw).Msgf("invalid %s, using default", TestDeadlockDetectionTimeoutEnvVar)
		return defaultTestDeadlockDetectionTimeout
	}
	return d
}

// TestWorkerOptions returns the shared worker.Options for Temporal test
// environments, notably an adjustable deadlock-detection timeout. Apply it via
// (*testsuite.TestWorkflowEnvironment).SetWorkerOptions.
func TestWorkerOptions() worker.Options {
	return worker.Options{DeadlockDetectionTimeout: TestDeadlockDetectionTimeout()}
}

// TestReplayerOptions returns the shared worker.WorkflowReplayerOptions for
// Temporal replay tests. Unlike a worker, the replayer exposes no configurable
// deadlock-detection timeout, only an on/off switch, so detection is disabled by
// default to avoid false positives on slow or loaded machines and re-enabled when
// TestDeadlockDetectionTimeoutEnvVar is set so genuine deadlocks can be surfaced.
func TestReplayerOptions() worker.WorkflowReplayerOptions {
	return worker.WorkflowReplayerOptions{
		DisableDeadlockDetection: os.Getenv(TestDeadlockDetectionTimeoutEnvVar) == "",
	}
}
