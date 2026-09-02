// Command modal_sandbox_status reports the lifecycle state of a named Modal
// sandbox as seen through each control-plane surface (FromName, Poll, exec,
// tunnels), to debug shutdown/reuse races where these views disagree.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"sidekick/env"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: modal_sandbox_status <sandbox-name> [poll-iterations]")
		os.Exit(2)
	}
	name := os.Args[1]
	iterations := 1
	if len(os.Args) > 2 {
		if _, err := fmt.Sscanf(os.Args[2], "%d", &iterations); err != nil || iterations < 1 {
			fmt.Fprintf(os.Stderr, "invalid poll-iterations %q\n", os.Args[2])
			os.Exit(2)
		}
	}

	ctx := context.Background()
	for i := 0; i < iterations; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		env.DebugModalSandboxStatus(ctx, os.Stdout, name)
	}
}
