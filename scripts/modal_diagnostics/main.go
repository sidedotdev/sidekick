package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type diagnosticCommand struct {
	name         string
	args         []string
	outputFilter string
}

func runDiagnostic(ctx context.Context, command diagnosticCommand) {
	fmt.Printf("\n===== %s %s =====\n", command.name, strings.Join(command.args, " "))

	cmd := exec.CommandContext(ctx, command.name, command.args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()

	fmt.Print(filterDiagnosticOutput(output.String(), command.outputFilter))
	if err != nil {
		fmt.Printf("\ncommand error: %v\n", err)
	}
}

func filterDiagnosticOutput(output, filter string) string {
	if filter == "" {
		return output
	}

	var filtered strings.Builder
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, filter) {
			filtered.WriteString(line)
			filtered.WriteByte('\n')
		}
	}
	return filtered.String()
}

func main() {
	sandbox := flag.String("sandbox", "", "Modal sandbox name used to filter platform logs")
	sshHost := flag.String("ssh-host", "", "stored Modal SSH tunnel hostname")
	sshPort := flag.Int("ssh-port", 0, "stored Modal SSH tunnel port")
	duration := flag.Duration("duration", 20*time.Second, "maximum duration for each diagnostic command")
	localLog := flag.String("local-log", "", "optional local Sidekick log file to search")
	flag.Parse()

	if *sandbox == "" {
		fmt.Fprintln(os.Stderr, "Usage: modal_diagnostics -sandbox <name> [-ssh-host host -ssh-port port] [-local-log path]")
		os.Exit(2)
	}

	if *localLog != "" {
		ctx, cancel := context.WithTimeout(context.Background(), *duration)
		runDiagnostic(ctx, diagnosticCommand{
			name: "grep",
			args: []string{"-n", "-i", "-E", *sandbox + "|ssh|sftp|BulkGetSymbolDefinitions", *localLog},
		})
		cancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	runDiagnostic(ctx, diagnosticCommand{
		name:         "modal",
		args:         []string{"app", "logs", "sidekick"},
		outputFilter: *sandbox,
	})
	cancel()

	if *sshHost == "" || *sshPort == 0 {
		return
	}

	ctx, cancel = context.WithTimeout(context.Background(), *duration)
	runDiagnostic(ctx, diagnosticCommand{
		name: "ssh",
		args: []string{
			"-vvv",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=10",
			"-o", "ConnectionAttempts=1",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-p", strconv.Itoa(*sshPort),
			"root@" + *sshHost,
			"true",
		},
	})
	cancel()
}
