package env

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"sidekick/coding/unix"
	"sidekick/sideagent"

	"github.com/rs/zerolog/log"
)

var devPodRecoveryLocks = struct {
	sync.Mutex
	entries map[string]*devPodRecoveryLock
}{
	entries: make(map[string]*devPodRecoveryLock),
}

type devPodRecoveryLock struct {
	token chan struct{}
	users int
}

func acquireDevPodRecoveryLock(ctx context.Context, name string) (func(), error) {
	devPodRecoveryLocks.Lock()
	entry := devPodRecoveryLocks.entries[name]
	if entry == nil {
		entry = &devPodRecoveryLock{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		devPodRecoveryLocks.entries[name] = entry
	}
	entry.users++
	devPodRecoveryLocks.Unlock()

	dropUser := func() {
		devPodRecoveryLocks.Lock()
		defer devPodRecoveryLocks.Unlock()
		entry.users--
		if entry.users == 0 {
			delete(devPodRecoveryLocks.entries, name)
		}
	}
	select {
	case <-ctx.Done():
		dropUser()
		return nil, ctx.Err()
	case <-entry.token:
	}
	release := func() {
		entry.token <- struct{}{}
		dropUser()
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (e *DevPodEnv) execWithRecovery(ctx context.Context, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
	resp, err := runRemoteCommand(ctx, e.sftpConnKey(), e.PortForwards, e, req)
	var dialErr *sshDialTransportError
	if err == nil || ctx.Err() != nil || !errors.As(err, &dialErr) || e.WorkspaceName == "" {
		return resp, err
	}

	// A dial failure proves that the request was never sent. Other transport
	// failures may follow execution and must not replay an arbitrary command.
	if recoveryErr := e.recoverConnection(ctx); recoveryErr != nil {
		return sideagent.ExecResponse{}, errors.Join(err, recoveryErr)
	}
	return runRemoteCommand(ctx, e.sftpConnKey(), e.PortForwards, e, req)
}

func (e *DevPodEnv) recoverConnection(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	release, err := acquireDevPodRecoveryLock(ctx, e.WorkspaceName)
	if err != nil {
		return err
	}
	defer release()

	// Another activity may have resumed the shared workspace while we waited.
	probe := agentExecRequest("/", e.GetType(), e.PortForwards, EnvRunCommandInput{
		Command: "true", SkipWaking: true,
	})
	_, err = runRemoteCommand(ctx, e.sftpConnKey(), e.PortForwards, e, probe)
	if err == nil {
		return nil
	}
	var dialErr *sshDialTransportError
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !errors.As(err, &dialErr) {
		return err
	}

	// A lost registration requires reconciliation with surviving containers.
	// Creating from LocalRepoDir here could start a duplicate workspace.
	if err := runDevPodRecoveryCommand(ctx, "status", e.WorkspaceName); err != nil {
		return fmt.Errorf("cannot recover DevPod workspace %q; check its registration before retrying: %w", e.WorkspaceName, err)
	}
	log.Warn().Str("workspace", e.WorkspaceName).Msg("resuming DevPod workspace after SSH dial failure")
	return runDevPodRecoveryCommand(ctx, "up", e.WorkspaceName, "--ide", "none",
		"--configure-ssh=true", "--recreate=false", "--reset=false")
}

func runDevPodRecoveryCommand(ctx context.Context, args ...string) error {
	out, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: ".", Command: "devpod", Args: args,
	})
	if err != nil {
		return fmt.Errorf("devpod %s: %w", args[0], err)
	}
	if out.ExitStatus != 0 {
		return fmt.Errorf("devpod %s exited with status %d: %s", args[0], out.ExitStatus,
			strings.TrimSpace(out.Stderr+"\n"+out.Stdout))
	}
	return nil
}
