// side-agent is sidekick's remote helper binary, designed to be
// cross-compiled and uploaded to remote environments (devpod, openshell,
// modal) where the full sidekick binary is not available. It speaks over the
// stdin/stdout of an SSH session and supports two modes:
//
//	side-agent exec   serve the framed exec protocol: argv arrives verbatim
//	                  in structured messages and is exec'd directly, so no
//	                  shell quoting is ever involved
//	side-agent sftp   serve the SFTP protocol
//	side-agent gc     best-effort removal of stale sibling binaries, invoked
//	                  by the install chain so cleanup runs only on installs
package main

import (
	"fmt"
	"io"
	"os"

	"sidekick/sideagent"
)

type stdioReadWriteCloser struct {
	io.Reader
	io.WriteCloser
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "side-agent:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	mode := "exec"
	if len(args) > 0 {
		mode = args[0]
	}
	if mode == "exec" || mode == "sftp" || mode == "background" {
		release, err := sideagent.RegisterManagedSession()
		if err != nil {
			return fmt.Errorf("register managed session: %w", err)
		}
		defer release()
	}
	switch mode {
	case "exec":
		if err := sideagent.EnableOrphanReaping(); err != nil {
			fmt.Fprintln(os.Stderr, "side-agent: orphaned processes will not be reaped:", err)
		}
		return sideagent.Serve(os.Stdin, os.Stdout)
	case "sftp":
		return sideagent.ServeSFTP(&stdioReadWriteCloser{os.Stdin, os.Stdout})
	case "background":
		return sideagent.RunBackground(args[1:])
	case "gc":
		sideagent.CleanupStaleSiblings()
		return nil
	default:
		return fmt.Errorf("unknown mode %q (expected \"exec\", \"sftp\", \"background\" or \"gc\")", mode)
	}
}
