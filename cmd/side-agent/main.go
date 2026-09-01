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
	mode := "exec"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	var err error
	switch mode {
	case "exec":
		err = sideagent.Serve(os.Stdin, os.Stdout)
	case "sftp":
		err = sideagent.ServeSFTP(&stdioReadWriteCloser{os.Stdin, os.Stdout})
	case "gc":
		sideagent.CleanupStaleSiblings()
	default:
		err = fmt.Errorf("unknown mode %q (expected \"exec\", \"sftp\" or \"gc\")", mode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "side-agent:", err)
		os.Exit(1)
	}
}
