package env

import (
	"context"
	"errors"
	"fmt"
	"time"

	modal "github.com/modal-labs/libmodal/modal-go"
)

const modalFailureDiagnosticTimeout = 2 * time.Second

type modalSandboxStatus struct {
	id       string
	exitCode *int
}

var lookupModalSandboxStatus = func(ctx context.Context, name, id string) (modalSandboxStatus, error) {
	client, err := getModalClient()
	if err != nil {
		return modalSandboxStatus{}, err
	}
	var sb *modal.Sandbox
	if id != "" {
		sb, err = client.Sandboxes.FromID(ctx, id)
	} else {
		sb, err = client.Sandboxes.FromName(ctx, modalAppName, name, nil)
	}
	if err != nil {
		return modalSandboxStatus{}, err
	}
	defer sb.Detach()
	exitCode, err := sb.Poll(ctx)
	if err != nil {
		return modalSandboxStatus{}, err
	}
	return modalSandboxStatus{id: sb.SandboxID, exitCode: exitCode}, nil
}

// annotateAgentExecFailure only observes platform status. A lost response
// cannot establish whether the command ran, so diagnosis must not replay it.
func (e *ModalEnv) annotateAgentExecFailure(ctx context.Context, cause error) error {
	var dialErr *sshDialTransportError
	var transportErr *agentExecTransportError
	if errors.As(cause, &dialErr) || !errors.As(cause, &transportErr) || ctx.Err() != nil {
		return cause
	}
	ctx, cancel := context.WithTimeout(ctx, modalFailureDiagnosticTimeout)
	defer cancel()
	lookup := e.lookupModalStatus
	if lookup == nil {
		lookup = lookupModalSandboxStatus
	}
	status, err := lookup(ctx, e.SandboxName, "")
	if ctx.Err() != nil {
		return cause
	}
	var notFound modal.NotFoundError
	if errors.As(err, &notFound) {
		return fmt.Errorf("modal sandbox %s is no longer available; command outcome unknown: %w", e.SandboxName, cause)
	}
	if err != nil || status.exitCode == nil {
		return cause
	}
	return fmt.Errorf("modal sandbox %s (%s) has terminated (exit %d); command outcome unknown: %w",
		e.SandboxName, status.id, *status.exitCode, cause)
}
