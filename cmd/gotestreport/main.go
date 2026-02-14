package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"

	"sidekick/gotestreport"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"./..."}
	}

	testArgs := append([]string{"test", "-json"}, args...)
	cmd := exec.Command("go", testArgs...)

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create stdout pipe: %v\n", err)
		os.Exit(1)
	}

	streamer := gotestreport.NewStreamer(os.Stdout)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start go test: %v\n", err)
		os.Exit(1)
	}

	readErr := streamer.ProcessReader(stdout)
	cmdErr := cmd.Wait()

	if readErr != nil {
		fmt.Fprintf(os.Stderr, "error reading test output: %v\n", readErr)
	}

	if stderrBuf.Len() > 0 && (cmdErr != nil || streamer.HasFailures() || readErr != nil) {
		fmt.Fprint(os.Stderr, stderrBuf.String())
	}

	fmt.Print(streamer.Summary())

	if cmdErr != nil || streamer.HasFailures() || readErr != nil {
		os.Exit(1)
	}
}
