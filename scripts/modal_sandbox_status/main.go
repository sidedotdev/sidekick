// Command modal_sandbox_status reports the lifecycle state of a named Modal
// sandbox as seen through each control-plane surface (FromName, Poll, exec,
// tunnels), to debug shutdown/reuse races where these views disagree.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"sidekick/env"
)

func main() {
	script := flag.String("exec", "", "shell script to run inside the sandbox via the Modal API after reporting status")
	events := flag.Int("events", 0, "also print the last N guard lifecycle events (snapshots, terminations) recorded for the sandbox")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: modal_sandbox_status [-exec script] [-events N] <sandbox-name> [poll-iterations]")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	name := flag.Arg(0)
	iterations := 1
	if flag.NArg() > 1 {
		if _, err := fmt.Sscanf(flag.Arg(1), "%d", &iterations); err != nil || iterations < 1 {
			fmt.Fprintf(os.Stderr, "invalid poll-iterations %q\n", flag.Arg(1))
			os.Exit(2)
		}
	}

	ctx := context.Background()
	if *events > 0 {
		defer func() {
			if err := env.DebugModalSnapshotEvents(ctx, os.Stdout, name, *events); err != nil {
				fmt.Fprintf(os.Stderr, "events failed: %v\n", err)
				os.Exit(1)
			}
		}()
	}
	if *script != "" {
		defer func() {
			if err := env.DebugModalSandboxExec(ctx, os.Stdout, name, *script); err != nil {
				fmt.Fprintf(os.Stderr, "exec failed: %v\n", err)
				os.Exit(1)
			}
		}()
	}
	for i := 0; i < iterations; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		env.DebugModalSandboxStatus(ctx, os.Stdout, name)
	}
}
