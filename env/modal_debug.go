package env

import (
	"context"
	"fmt"
	"io"
	"time"

	modal "github.com/modal-labs/libmodal/modal-go"
)

// DebugModalSandboxStatus prints how each Modal control-plane surface sees
// the named sandbox: name lookup, poll result, exec admission, tunnel
// endpoint and the guard's snapshot record. These views can disagree while a
// sandbox shuts down, which is exactly what this helper exists to make
// observable. The snapshot record decides whether a vanished sandbox can be
// restored at all, so it is reported even when the sandbox is gone.
func DebugModalSandboxStatus(ctx context.Context, w io.Writer, name string) {
	now := time.Now().Format(time.RFC3339)
	client, err := getModalClient()
	if err != nil {
		fmt.Fprintf(w, "%s client error: %v\n", now, err)
		return
	}
	debugModalSnapshotRecord(ctx, w, client, name)
	sb, err := client.Sandboxes.FromName(ctx, modalAppName, name, nil)
	if err != nil {
		fmt.Fprintf(w, "%s FromName error: %v\n", now, err)
		return
	}
	fmt.Fprintf(w, "%s FromName ok: sandboxId=%s\n", now, sb.SandboxID)

	exitCode, err := sb.Poll(ctx)
	switch {
	case err != nil:
		fmt.Fprintf(w, "%s Poll error: %v\n", now, err)
	case exitCode == nil:
		fmt.Fprintf(w, "%s Poll: running (nil exit code)\n", now)
	default:
		fmt.Fprintf(w, "%s Poll: exited with code %d\n", now, *exitCode)
	}

	proc, err := sb.Exec(ctx, []string{"true"}, &modal.SandboxExecParams{Stdout: modal.Ignore, Stderr: modal.Ignore})
	if err != nil {
		fmt.Fprintf(w, "%s Exec error: %v (terminatingOrTerminated=%v)\n", now, err, isModalSandboxTerminatingOrTerminated(err))
	} else if code, werr := proc.Wait(ctx); werr != nil {
		fmt.Fprintf(w, "%s Exec wait error: %v\n", now, werr)
	} else {
		fmt.Fprintf(w, "%s Exec: ok (exit %d)\n", now, code)
	}

	host, port, err := modalTunnelEndpoint(ctx, sb)
	if err != nil {
		fmt.Fprintf(w, "%s Tunnel error: %v\n", now, err)
	} else {
		fmt.Fprintf(w, "%s Tunnel: %s:%d\n", now, host, port)
	}
}

// DebugModalSandboxExec runs a shell script inside the named sandbox through
// the Modal API and writes its output to w, so investigations can read
// in-sandbox state (e.g. the watchdog log, which survives snapshots) without
// an SSH endpoint.
func DebugModalSandboxExec(ctx context.Context, w io.Writer, name, script string) error {
	client, err := getModalClient()
	if err != nil {
		return err
	}
	sb, err := findModalSandbox(ctx, client, name)
	if err != nil {
		return err
	}
	if sb == nil {
		return fmt.Errorf("%w: %s", errModalSandboxNotRunning, name)
	}
	stdout, stderr, exitCode, err := modalExecCapture(ctx, sb, script)
	if err != nil {
		return err
	}
	return writeModalExecResult(w, sb.SandboxID, stdout, stderr, exitCode)
}

// writeModalExecResult reports a remote script's output and then its failure,
// so a script that died remotely cannot look like a clean run to whatever is
// driving the diagnostics.
func writeModalExecResult(w io.Writer, sandboxID, stdout, stderr string, exitCode int) error {
	fmt.Fprintf(w, "exec sandboxId=%s exit=%d\n--- stdout ---\n%s", sandboxID, exitCode, stdout)
	if stderr != "" {
		fmt.Fprintf(w, "--- stderr ---\n%s", stderr)
	}
	if exitCode != 0 {
		return fmt.Errorf("remote script exited with code %d", exitCode)
	}
	return nil
}

// debugModalSnapshotRecord reports the guard's snapshot record, separating
// "the guard is not deployed" from "the guard has no record for this name":
// the recovery path treats both as unrestorable, but only the latter means
// the sandbox's filesystem was truly never captured.
func debugModalSnapshotRecord(ctx context.Context, w io.Writer, client *modal.Client, name string) {
	now := time.Now().Format(time.RFC3339)
	if _, err := client.Functions.FromName(ctx, modalGuardAppName(), "latest_snapshot", nil); err != nil {
		fmt.Fprintf(w, "%s Snapshot: guard %q unavailable: %v\n", now, modalGuardAppName(), err)
		return
	}
	if identity, err := modalGuardIdentityFor(ctx, client); err != nil {
		fmt.Fprintf(w, "%s Guard: app=%s identity unavailable: %v\n", now, modalGuardAppName(), err)
	} else {
		fmt.Fprintf(w, "%s Guard: app=%s volume=%s namespace=%q deployedSourceHash=%s wantSourceHash=%s\n",
			now, identity.AppName, identity.VolumeName, identity.Namespace, identity.SourceHash, modalGuardScriptHash())
	}
	record, err := modalReadLatestSnapshot(ctx, client, name)
	switch {
	case err != nil:
		fmt.Fprintf(w, "%s Snapshot error: %v\n", now, err)
	case record == nil:
		fmt.Fprintf(w, "%s Snapshot: none recorded (sandbox cannot be restored)\n", now)
	default:
		taken := "unknown"
		if t := record.snapshotTime(); !t.IsZero() {
			taken = t.Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%s Snapshot: imageId=%s imageVersion=%d takenAt=%s ofSandbox=%q history=%v\n",
			now, record.ImageId, record.ImageVersion, taken, record.SandboxId, record.History)
	}
}

// DebugModalSnapshotEvents prints the guard's durable lifecycle events for
// the named sandbox, which outlive both Modal's log retention and the
// sandbox incarnations they describe.
func DebugModalSnapshotEvents(ctx context.Context, w io.Writer, name string, limit int) error {
	client, err := getModalClient()
	if err != nil {
		return err
	}
	events, err := modalSnapshotEvents(ctx, client, name, limit)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		fmt.Fprintf(w, "no guard lifecycle events recorded for %s\n", name)
		return nil
	}
	for _, event := range events {
		fmt.Fprintln(w, event)
	}
	return nil
}
