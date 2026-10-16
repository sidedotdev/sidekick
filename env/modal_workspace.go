package env

import (
	"context"
	"encoding/json"
	"fmt"

	"sidekick/sideagent"
)

// ExecuteWorkspace uses the same pooled channel and admission gate as commands.
// Workspace operations are read-only or idempotent, so transport recovery may
// safely repeat them.
func (e *ModalEnv) ExecuteWorkspace(ctx context.Context, operation sideagent.WorkspaceRequest) (sideagent.WorkspaceResponse, error) {
	request := sideagent.ExecRequest{
		Dir:       e.WorkingDirectory,
		Env:       envVarsToInject(e.GetType(), e.PortForwards),
		LoginEnv:  true,
		TouchPath: remoteActivityMarker,
		Workspace: &operation,
	}
	var result sideagent.WorkspaceResponse
	err := e.runWithSSHTransportRecovery(ctx, func() error {
		return retryModalAdmission(ctx, modalAdmissionWaitBudget, func() error {
			response, err := runRemoteCommand(ctx, e.sftpConnKey(), e.PortForwards, nonRecoveringSSHEnv{e}, request)
			if err != nil {
				return err
			}
			if response.Sealed {
				return errModalCommandNotAdmitted
			}
			if response.Error != "" {
				return fmt.Errorf("workspace operation: %s", response.Error)
			}
			if response.ExitStatus != 0 {
				return fmt.Errorf("workspace operation exited %d: %s", response.ExitStatus, response.Stderr)
			}
			return json.Unmarshal(response.Stdout, &result)
		})
	})
	return result, err
}

// ReadFiles prefetches ordinary source files in one round trip. Callers fall
// back to ReadFile for any omitted files.
func (e *ModalEnv) ReadFiles(ctx context.Context, paths []string) (map[string][]byte, error) {
	if len(paths) == 0 {
		return map[string][]byte{}, nil
	}
	result, err := e.ExecuteWorkspace(ctx, sideagent.WorkspaceRequest{ReadFiles: paths})
	return result.Files, err
}
