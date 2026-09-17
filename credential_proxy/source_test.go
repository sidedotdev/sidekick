package credential_proxy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sidekick/common"
)

func TestResolveCredentialEnvironment(t *testing.T) {
	t.Setenv("SIDE_TEST_PROXY_CREDENTIAL", "provider-secret")
	value, err := Resolve(context.Background(), common.CredentialSource{Env: "SIDE_TEST_PROXY_CREDENTIAL"})
	if err != nil || value != "provider-secret" {
		t.Fatal("expected configured environment credential")
	}

	t.Setenv("SIDE_TEST_PROXY_CREDENTIAL", "")
	if _, err := Resolve(context.Background(), common.CredentialSource{Env: "SIDE_TEST_PROXY_CREDENTIAL"}); err == nil {
		t.Fatal("empty credentials must fail closed")
	}
}

func TestResolveCredentialCommand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		source  common.CredentialSource
		want    string
		wantErr bool
	}{
		{
			name:   "stdout with trailing newline",
			source: common.CredentialSource{Command: "printf 'provider-secret\\n'"},
			want:   "provider-secret",
		},
		{
			name:    "missing source",
			wantErr: true,
		},
		{
			name:    "ambiguous sources",
			source:  common.CredentialSource{Env: "PATH", Command: "printf provider-secret"},
			wantErr: true,
		},
		{
			name:    "empty stdout",
			source:  common.CredentialSource{Command: "true"},
			wantErr: true,
		},
		{
			name:    "failed command",
			source:  common.CredentialSource{Command: "printf provider-secret; printf provider-secret >&2; exit 1"},
			wantErr: true,
		},
		{
			name:    "embedded newline",
			source:  common.CredentialSource{Command: "printf 'provider-secret\\ninjected-header: value'"},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value, err := Resolve(context.Background(), tc.source)
			if (err != nil) != tc.wantErr {
				t.Fatal("unexpected credential resolution outcome")
			}
			if err != nil {
				if value != "" {
					t.Fatal("failed resolution returned a credential")
				}
				if strings.Contains(err.Error(), "provider-secret") {
					t.Fatal("credential disclosed in error")
				}
				return
			}
			if value != tc.want {
				t.Fatal("unexpected resolved credential")
			}
		})
	}
}

func TestResolveCredentialCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err := Resolve(ctx, common.CredentialSource{Command: "printf provider-secret"})
	if err == nil || value != "" {
		t.Fatal("canceled resolution must not return credentials")
	}
}

func TestCredentialOutputBound(t *testing.T) {
	t.Parallel()

	output := &credentialOutput{}
	input := strings.Repeat("x", maxCredentialBytes*4)
	// Hide WriterTo to exercise the destination optimization used by io.Copy.
	reader := struct{ io.Reader }{strings.NewReader(input)}
	if _, err := io.Copy(output, reader); err != nil {
		t.Fatal(err)
	}
	if output.Len() > maxCredentialBytes || !output.exceeded {
		t.Fatal("credential output bypassed the size limit")
	}
}

func TestResolveCredentialOversizedCommand(t *testing.T) {
	t.Parallel()

	value, err := Resolve(context.Background(), common.CredentialSource{
		Command: "head -c 65536 /dev/zero | tr '\\000' x",
	})
	if err == nil || value != "" {
		t.Fatal("oversized command output must fail closed")
	}
}

func TestResolveCredentialCancellationDuringCommand(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	value, err := Resolve(ctx, common.CredentialSource{Command: "sleep 30; printf provider-secret"})
	if err == nil || value != "" {
		t.Fatal("canceled command returned a credential")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("credential command did not stop promptly")
	}
}

func TestResolveCredentialCancellationStopsDescendants(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	completed := filepath.Join(dir, "completed")
	script := filepath.Join(dir, "helper.sh")
	if err := os.WriteFile(script, []byte("touch \"$1\"\nsleep 1\ntouch \"$2\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := "sh '" + script + "' '" + ready + "' '" + completed + "' & wait"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Resolve(ctx, common.CredentialSource{Command: command})
		done <- err
	}()

	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
waiting:
	for {
		select {
		case err := <-done:
			t.Fatalf("helper exited before cancellation: %v", err)
		case <-deadline.C:
			t.Fatal("helper did not start")
		case <-ticker.C:
			if _, err := os.Stat(ready); err == nil {
				break waiting
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled helper succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not return promptly")
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(completed); !os.IsNotExist(err) {
		t.Fatal("child process survived cancellation")
	}

	if _, err := Resolve(context.Background(), common.CredentialSource{
		Command: "sh '" + script + "' '" + ready + "' '" + completed + "'; printf credential",
	}); err != nil {
		t.Fatal("positive control failed")
	}
	if _, err := os.Stat(completed); err != nil {
		t.Fatal("positive control did not create completion marker")
	}
}
