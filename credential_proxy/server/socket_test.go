package server

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"sidekick/common"
)

// socketTempDir is a private temporary directory short enough for unix socket
// paths, which t.TempDir exceeds on macOS for long test names.
func socketTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestServiceSocketRecovery(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"stale", "file", "symlink", "live"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := socketTempDir(t)
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(dir, "control.sock")
			switch kind {
			case "stale", "live":
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				listener.SetUnlinkOnClose(false)
				if kind == "stale" {
					if err := listener.Close(); err != nil {
						t.Fatal(err)
					}
				} else {
					defer listener.Close()
				}
			case "file":
				if err := os.WriteFile(socket, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(dir, "missing"), socket); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(socket)
			if err != nil {
				t.Fatal(err)
			}
			service, err := StartService(0, socket, func() (map[string]common.CredentialProxyConfig, error) {
				return nil, nil
			}, func() bool { return false })
			if kind == "stale" {
				if err != nil {
					t.Fatalf("stale socket prevented restart: %v", err)
				}
				if err := service.Close(); err != nil {
					t.Fatal(err)
				}
				lockInfo, err := os.Stat(socket + ".lock")
				if err != nil {
					t.Fatalf("stable lock missing after shutdown: %v", err)
				}
				restarted, err := StartService(0, socket, func() (map[string]common.CredentialProxyConfig, error) {
					return nil, nil
				}, func() bool { return false })
				if err != nil {
					t.Fatalf("restart after Close failed: %v", err)
				}
				defer restarted.Close()
				currentLock, err := os.Stat(socket + ".lock")
				if err != nil || !os.SameFile(lockInfo, currentLock) {
					t.Fatal("restart replaced stable lock")
				}
				return
			}
			if err == nil {
				service.Close()
				t.Fatal("occupied path was replaced")
			}
			after, err := os.Lstat(socket)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("occupied path was altered")
			}
		})
	}
}

func TestServiceConcurrentStart(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(socketTempDir(t), "private", "control.sock")
	var wg sync.WaitGroup
	services := make(chan *Service, 8)
	start := make(chan struct{})
	for i := 0; i < cap(services); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			service, err := StartService(0, socket, func() (map[string]common.CredentialProxyConfig, error) {
				return nil, nil
			}, func() bool { return false })
			if err == nil {
				services <- service
			}
		}()
	}
	close(start)
	wg.Wait()
	close(services)
	count := 0
	for service := range services {
		count++
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	}
	if count != 1 {
		t.Fatalf("successful concurrent starts = %d, want 1", count)
	}
}

func TestServiceRejectsUnsafeLock(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := socketTempDir(t)
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(dir, "control.sock")
			lock := socket + ".lock"
			if kind == "symlink" {
				if err := os.Symlink(filepath.Join(dir, "missing"), lock); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(lock, 0700); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(lock)
			if err != nil {
				t.Fatal(err)
			}
			service, err := StartService(0, socket, func() (map[string]common.CredentialProxyConfig, error) {
				return nil, nil
			}, func() bool { return false })
			if err == nil {
				service.Close()
				t.Fatal("unsafe lock accepted")
			}
			after, err := os.Lstat(lock)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("unsafe lock path altered")
			}
		})
	}
}

func TestServiceClosePreservesReplacementPath(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(socketTempDir(t), "private", "control.sock")
	service, err := StartService(0, socket, func() (map[string]common.CredentialProxyConfig, error) {
		return nil, nil
	}, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socket, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(socket)
	if err != nil || string(data) != "replacement" {
		t.Fatal("shutdown removed replacement path")
	}
}
