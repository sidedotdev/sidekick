package env

import (
	"context"
	"fmt"
	"io"
	"time"

	modal "github.com/modal-labs/libmodal/modal-go"
)

// DebugModalSandboxStatus prints how each Modal control-plane surface sees
// the named sandbox: name lookup, poll result, exec admission and tunnel
// endpoint. These views can disagree while a sandbox shuts down, which is
// exactly what this helper exists to make observable.
func DebugModalSandboxStatus(ctx context.Context, w io.Writer, name string) {
	now := time.Now().Format(time.RFC3339)
	client, err := getModalClient()
	if err != nil {
		fmt.Fprintf(w, "%s client error: %v\n", now, err)
		return
	}
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
