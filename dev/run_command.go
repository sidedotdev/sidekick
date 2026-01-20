package dev

import (
	"fmt"
	"path/filepath"
	"sidekick/common"
	"sidekick/env"
	"sidekick/llm"
	"sidekick/logger"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

type RunCommandParams struct {
	Command    string `json:"command" jsonschema:"description=The shell command or script to execute. Will automatically be run in the repository root working directory (cd not needed)\\, or the relative directory specified via workingDir."`
	WorkingDir string `json:"workingDir,omitempty" jsonschema:"description=Optional working directory relative to the repository root directory"`
}

var runCommandTool = llm.Tool{
	Name:        "run_command",
	Description: "Not for running tests or reading code. This tool is used to execute other shell commands, subject to user approval. The command will be run through the 'sh' shell if approved. This tool should NOT be used to run tests. When asked to provide edit blocks, all tests are run automatically after no further edit blocks are provided. Also don't use this tool when a more specific tool already exists, e.g. specific tools to get symbol definitions, read files, search repo, etc",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&RunCommandParams{}),
}

// checkCommandPermission evaluates command permissions and handles user approval if needed.
// Returns (proceed, message, error) where proceed indicates whether to execute the command,
// message contains any early return message (for denied or unapproved commands), and error
// for any failures during approval.
func checkCommandPermission(dCtx DevContext, command string, workingDir string) (proceed bool, message string, err error) {
	enableCommandPermissions := workflow.GetVersion(dCtx, "command-permissions", workflow.DefaultVersion, 1) >= 1
	stripEnvVarPrefix := workflow.GetVersion(dCtx, "command-permissions-strip-env-var", workflow.DefaultVersion, 1) >= 1
	if enableCommandPermissions {
		opts := common.EvaluatePermissionOptions{
			StripEnvVarPrefix: stripEnvVarPrefix,
		}
		permResult, permMessage := common.EvaluateScriptPermissionWithOptions(dCtx.RepoConfig.CommandPermissions, command, opts)
		workflow.GetLogger(dCtx).Debug("Command permission evaluation result", "command", command, "result", permResult, "message", permMessage)

		switch permResult {
		case common.PermissionDeny:
			return false, fmt.Sprintf("Command denied: %s", permMessage), nil
		case common.PermissionAutoApprove:
			return true, "", nil
		case common.PermissionRequireApproval:
			// Fall through to request approval
		}
	} else {
		workflow.GetLogger(dCtx).Warn("Command permissions are not enabled")
	}

	l := logger.Get()
	l.Debug().Str("cmd", command).Bool("auto-approved", proceed).Str("message", message).Msg("checkCommandPermission auto result")

	// Request user approval (legacy behavior or when permission requires approval)
	approvalPrompt := "Allow running the following command?"
	userResponse, err := GetUserApproval(dCtx, "run_command", approvalPrompt, map[string]any{
		"command":    command,
		"workingDir": workingDir,
	})
	if err != nil {
		return false, "", fmt.Errorf("failed to get user approval: %v", err)
	}
	if userResponse == nil || userResponse.Approved == nil || !*userResponse.Approved {
		return false, "Command execution was not approved by user. They said:\n\n" + userResponse.Content, nil
	}

	return true, "", nil
}

// RunCommand handles the execution of shell commands with user approval
func RunCommand(dCtx DevContext, params RunCommandParams) (string, error) {
	proceed, message, err := checkCommandPermission(dCtx, params.Command, params.WorkingDir)
	if err != nil {
		return "", err
	}
	if !proceed {
		return message, nil
	}

	// Prepare working directory
	relWorkDir := "."
	if params.WorkingDir != "" {
		relWorkDir = filepath.Clean(params.WorkingDir)
	}

	// Execute command through sh
	var output env.EnvRunCommandActivityOutput
	err = workflow.ExecuteActivity(dCtx.Context, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		Command:            "sh",
		Args:               []string{"-c", params.Command},
		RelativeWorkingDir: relWorkDir,
	}).Get(dCtx.Context, &output)
	if err != nil {
		return "", fmt.Errorf("failed to execute command: %v", err)
	}

	// Format response
	response := fmt.Sprintf("Command executed with exit status %d\n\nStdout:\n%s\n\nStderr:\n%s",
		output.ExitStatus,
		output.Stdout,
		output.Stderr)

	return response, nil
}
