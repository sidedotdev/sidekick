package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestStopProcessAfterGroupLeaderExits(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", `
sh -c '
	trap "printf stopped > stopped; exit 0" TERM
	printf ready > ready
	while :; do sleep 1; done
' &
while [ ! -f ready ]; do sleep 0.01; done
`)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	// A child holding the output pipes can keep the supervised process marked
	// running after its group leader has exited.
	p := &Process{Cmd: cmd, running: true, cancel: cancel}
	sup := NewSupervisor(nil, false, dir, dir)
	sup.StopProcess(p)

	waitForCondition(t, 2*time.Second, func() bool {
		content, err := os.ReadFile(filepath.Join(dir, "stopped"))
		return err == nil && string(content) == "stopped"
	}, "stopping the process should terminate children after the group leader exits")
}

func TestStopProcessAllowsGracefulExit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup := NewSupervisor([]ProcessConfig{{
		Name:    "graceful",
		Command: "sh",
		Args: []string{"-c", `
trap 'sleep 0.2; printf stopped > stopped; exit 0' TERM
printf ready > ready
while :; do sleep 1; done
`},
	}}, false, dir, dir)
	p := sup.processes[0]
	if err := sup.StartProcess(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-p.Cmd.Process.Pid, syscall.SIGKILL)
	})
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(dir, "ready"))
		return err == nil
	}, "process did not install its termination handler")

	sup.StopProcess(p)

	waitForCondition(t, 2*time.Second, func() bool {
		content, err := os.ReadFile(filepath.Join(dir, "stopped"))
		return err == nil && string(content) == "stopped" && !p.isRunning()
	}, "the termination handler should complete before the process is killed")
}

func TestCancelProcessTerminatesChildren(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup := NewSupervisor([]ProcessConfig{{
		Name:    "cancelled",
		Command: "sh",
		Args: []string{"-c", `
sh -c 'printf ready > ready; exec sleep 30' &
wait
`},
	}}, false, dir, dir)
	p := sup.processes[0]
	if err := sup.StartProcess(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-p.Cmd.Process.Pid, syscall.SIGKILL)
	})
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(dir, "ready"))
		return err == nil
	}, "child process did not start")

	cancel()

	waitForCondition(t, 2*time.Second, func() bool {
		return !p.isRunning()
	}, "context cancellation should terminate children holding the output pipes")
}
