package redis

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"sidekick/srv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func newTestRedisService(t *testing.T) *srv.Delegator {
	t.Helper()
	storage := newTestRedisStorage(t)
	streamer := newTestRedisStreamer(t)
	return srv.NewDelegator(storage, streamer)
}

func newTestRedisStorage(t *testing.T) *Storage {
	t.Helper()
	return &Storage{Client: newTestRedisClient(t)}
}

func newTestRedisStreamer(t *testing.T) *Streamer {
	t.Helper()
	return &Streamer{Client: newTestRedisClient(t)}
}

// A private server prevents concurrent test binaries from clearing each other's data.
func newTestRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skipf("Skipping test; redis-server not available: %v", err)
	}

	dir := t.TempDir()
	socket := filepath.Join(dir, "redis.sock")
	cmd := exec.Command(executable,
		"--port", "0",
		"--unixsocket", socket,
		"--unixsocketperm", "700",
		"--save", "",
		"--appendonly", "no",
		"--dir", dir,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("Starting Redis test server: %v", err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()

	client := redis.NewClient(&redis.Options{
		Network:    "unix",
		Addr:       socket,
		DB:         1,
		MaxRetries: -1,
	})
	t.Cleanup(func() {
		_ = client.Close()
		_ = cmd.Process.Kill()
		<-done
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		pingErr := client.Ping(ctx).Err()
		if pingErr == nil {
			return client
		}
		select {
		case <-done:
			t.Fatalf("Redis test server exited: %v\n%s", waitErr, output.String())
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-done
			t.Fatalf("Redis test server did not become ready: %v\n%s", pingErr, output.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
