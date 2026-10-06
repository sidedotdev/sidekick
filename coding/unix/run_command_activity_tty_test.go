package unix

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

const jobControlHelperEnvVar = "RUN_COMMAND_JOB_CONTROL_HELPER"

// A worker started by a supervisor that gives it its own process group (see
// scripts/supervisor.go) runs in a background process group of the
// supervisor's terminal. If a command it runs touches that terminal, as sudo
// does to prompt for a password, the kernel sends SIGTTOU/SIGTTIN to the whole
// process group, stopping the worker itself unless the command is detached
// from the terminal.
func Test_RunCommandActivity_TerminalPromptDoesNotStopCaller(t *testing.T) {
	t.Parallel()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("failed to open pty: %v", err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		_ = tty.Close()
	})

	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	t.Cleanup(func() { _ = outR.Close() })

	supervisor := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_JobControl$")
	supervisor.Env = append(os.Environ(), jobControlHelperEnvVar+"=supervisor")
	supervisor.Stdin = tty
	supervisor.Stdout = outW
	supervisor.Stderr = outW
	supervisor.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := supervisor.Start(); err != nil {
		t.Fatalf("failed to start supervisor helper: %v", err)
	}
	_ = outW.Close()
	t.Cleanup(func() {
		_ = syscall.Kill(-supervisor.Process.Pid, syscall.SIGKILL)
		_ = supervisor.Wait()
	})

	lines := make(chan string, 100)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(outR)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	var output []string
	workerPid := 0
	timeout := time.After(5 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("helpers exited without the worker finishing:\n%s", strings.Join(output, "\n"))
			}
			output = append(output, line)
			if pidStr, found := strings.CutPrefix(line, "worker "); found {
				if pid, err := strconv.Atoi(pidStr); err == nil {
					workerPid = pid
					t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
				}
			}
			if result, found := strings.CutPrefix(line, "done "); found {
				if !strings.Contains(result, "err=<nil>") || strings.Contains(result, "exitStatus=0 ") {
					t.Fatalf("expected the terminal prompt to fail fast with a non-zero exit, got: %s", result)
				}
				return
			}
		case <-timeout:
			t.Fatalf("worker calling RunCommandActivity froze while its command used the terminal\nprocesses:\n%s\noutput:\n%s",
				describeProcessTree(workerPid), strings.Join(output, "\n"))
		}
	}
}

// describeProcessTree reports the state of rootPid and its descendants, where
// a "T" state shows a process stopped by job control.
func describeProcessTree(rootPid int) string {
	if rootPid == 0 {
		return "worker pid unknown"
	}
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,pgid=,stat=,command=").Output()
	if err != nil {
		return fmt.Sprintf("ps failed: %v", err)
	}
	inTree := map[int]bool{rootPid: true}
	var rows [][]string
	for _, line := range strings.Split(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) >= 5 {
			rows = append(rows, fields)
		}
	}
	var described []string
	for changed := true; changed; {
		changed = false
		for _, fields := range rows {
			pid, _ := strconv.Atoi(fields[0])
			ppid, _ := strconv.Atoi(fields[1])
			if !inTree[pid] && inTree[ppid] {
				inTree[pid] = true
				changed = true
			}
		}
	}
	for _, fields := range rows {
		if pid, _ := strconv.Atoi(fields[0]); inTree[pid] {
			described = append(described, strings.Join(fields, " "))
		}
	}
	return strings.Join(described, "\n")
}

// TestHelperProcess_JobControl plays two roles in subprocesses: a supervisor
// that owns a pty as its controlling terminal, and the worker it starts in a
// separate background process group of that terminal. The worker's parent
// being in the same session keeps its process group non-orphaned, so terminal
// access from it stops the group rather than failing with EIO.
func TestHelperProcess_JobControl(t *testing.T) {
	switch os.Getenv(jobControlHelperEnvVar) {
	case "supervisor":
		worker := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_JobControl$")
		worker.Env = append(os.Environ(), jobControlHelperEnvVar+"=worker")
		worker.Stdin = os.Stdin
		worker.Stdout = os.Stdout
		worker.Stderr = os.Stderr
		worker.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := worker.Start(); err != nil {
			fmt.Printf("failed to start worker: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("worker %d\n", worker.Process.Pid)
		_ = worker.Wait()
	case "worker":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// Mimics sudo's password prompt: disable echo, then read from the terminal.
		out, err := RunCommandActivity(ctx, RunCommandActivityInput{
			WorkingDir: t.TempDir(),
			Command:    "sh",
			Args:       []string{"-c", "stty -echo < /dev/tty; read -r pw < /dev/tty"},
		})
		fmt.Printf("done exitStatus=%d err=%v stderr=%q \n", out.ExitStatus, err, out.Stderr)
	}
}
