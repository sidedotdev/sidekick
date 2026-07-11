package utils

import (
	"testing"
	"time"
)

func TestTestDeadlockDetectionTimeout(t *testing.T) {
	t.Run("defaults when env var unset", func(t *testing.T) {
		t.Setenv(TestDeadlockDetectionTimeoutEnvVar, "")
		if got := TestDeadlockDetectionTimeout(); got != defaultTestDeadlockDetectionTimeout {
			t.Fatalf("expected default %s, got %s", defaultTestDeadlockDetectionTimeout, got)
		}
	})

	t.Run("honors a valid override", func(t *testing.T) {
		t.Setenv(TestDeadlockDetectionTimeoutEnvVar, "250ms")
		if got := TestDeadlockDetectionTimeout(); got != 250*time.Millisecond {
			t.Fatalf("expected 250ms, got %s", got)
		}
		if opts := TestWorkerOptions(); opts.DeadlockDetectionTimeout != 250*time.Millisecond {
			t.Fatalf("expected worker option 250ms, got %s", opts.DeadlockDetectionTimeout)
		}
	})

	t.Run("falls back to default on invalid override", func(t *testing.T) {
		t.Setenv(TestDeadlockDetectionTimeoutEnvVar, "not-a-duration")
		if got := TestDeadlockDetectionTimeout(); got != defaultTestDeadlockDetectionTimeout {
			t.Fatalf("expected default %s, got %s", defaultTestDeadlockDetectionTimeout, got)
		}
	})
}

func TestTestReplayerOptions(t *testing.T) {
	t.Run("disables deadlock detection when env var unset", func(t *testing.T) {
		t.Setenv(TestDeadlockDetectionTimeoutEnvVar, "")
		if !TestReplayerOptions().DisableDeadlockDetection {
			t.Fatal("expected deadlock detection to be disabled by default")
		}
	})

	t.Run("enables deadlock detection when env var set", func(t *testing.T) {
		t.Setenv(TestDeadlockDetectionTimeoutEnvVar, "1s")
		if TestReplayerOptions().DisableDeadlockDetection {
			t.Fatal("expected deadlock detection to be enabled when the env var is set")
		}
	})
}
