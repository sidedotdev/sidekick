package dev

import (
	"context"
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
	Description: "Not for reading code. This tool is used to execute other shell commands, subject to user approval. The command will be run through the 'sh' shell if approved. It can be used to run specific tests, suites, or even the full test suite when appropriate, especially when debugging failures or validating changes. The full test suite does not need to be run manually by default, because all tests are run automatically after no further edit blocks are provided. Also don't use this tool when a more specific tool already exists, e.g. specific tools to get symbol definitions, read files, search repo, etc",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&RunCommandParams{}),
}

type CheckCommandPermissionInput struct {
	CommandPermissions                common.CommandPermissionConfig
	Command                           string
	StripEnvVarPrefix                 bool
	DefaultAutoApprove                bool
	SkipAbsolutePathEscalation        bool
	HeredocFileWriteWarnInsteadOfDeny bool
}

type CheckCommandPermissionOutput struct {
	Result  common.PermissionResult
	Message string
}

func CheckCommandPermissionActivity(ctx context.Context, input CheckCommandPermissionInput) (CheckCommandPermissionOutput, error) {
	opts := common.EvaluatePermissionOptions{
		StripEnvVarPrefix:                 input.StripEnvVarPrefix,
		DefaultAutoApprove:                input.DefaultAutoApprove,
		SkipAbsolutePathEscalation:        input.SkipAbsolutePathEscalation,
		HeredocFileWriteWarnInsteadOfDeny: input.HeredocFileWriteWarnInsteadOfDeny,
	}
	result, message := common.EvaluateScriptPermissionWithOptions(input.CommandPermissions, input.Command, opts)
	return CheckCommandPermissionOutput{
		Result:  result,
		Message: message,
	}, nil
}

// checkCommandPermission evaluates command permissions and handles user approval if needed.
// Returns (proceed, denyMessage, advisory, error) where proceed indicates whether to execute
// the command, denyMessage contains any early-return message (for denied or unapproved
// commands), advisory contains informational guidance to append to the command output for
// auto-approved commands (e.g. heredoc warnings, cd guidance), and error for failures.
func checkCommandPermission(dCtx DevContext, command string, workingDir string) (proceed bool, denyMessage string, advisory string, err error) {
	enableCommandPermissions := workflow.GetVersion(dCtx, "command-permissions", workflow.DefaultVersion, 1) >= 1
	stripEnvVarPrefix := workflow.GetVersion(dCtx, "command-permissions-strip-env-var", workflow.DefaultVersion, 1) >= 1
	usePermissionActivity := workflow.GetVersion(dCtx, "command-permissions-activity", workflow.DefaultVersion, 1) >= 1

	sandboxMode := false
	if workflow.GetVersion(dCtx, "sandbox-command-permissions", workflow.DefaultVersion, 1) >= 1 {
		switch dCtx.EnvContainer.Env.GetType() {
		case env.EnvTypeDevPod, env.EnvTypeOpenShell:
			sandboxMode = true
		}
	}

	if enableCommandPermissions {
		var permResult common.PermissionResult
		var permMessage string

		if usePermissionActivity {
			var output CheckCommandPermissionOutput
			err := workflow.ExecuteActivity(dCtx.Context, CheckCommandPermissionActivity, CheckCommandPermissionInput{
				CommandPermissions:                dCtx.RepoConfig.CommandPermissions,
				Command:                           command,
				StripEnvVarPrefix:                 stripEnvVarPrefix,
				DefaultAutoApprove:                sandboxMode,
				SkipAbsolutePathEscalation:        sandboxMode,
				HeredocFileWriteWarnInsteadOfDeny: sandboxMode,
			}).Get(dCtx.Context, &output)
			if err != nil {
				return false, "", "", fmt.Errorf("failed to check command permission: %v", err)
			}
			permResult = output.Result
			permMessage = output.Message
		} else {
			opts := common.EvaluatePermissionOptions{
				StripEnvVarPrefix:                 stripEnvVarPrefix,
				UseLegacyCommandExtraction:        true,
				DefaultAutoApprove:                sandboxMode,
				SkipAbsolutePathEscalation:        sandboxMode,
				HeredocFileWriteWarnInsteadOfDeny: sandboxMode,
			}
			permResult, permMessage = common.EvaluateScriptPermissionWithOptions(dCtx.RepoConfig.CommandPermissions, command, opts)
		}

		workflow.GetLogger(dCtx).Debug("Command permission evaluation result", "command", command, "result", permResult, "message", permMessage)

		switch permResult {
		case common.PermissionDeny:
			return false, fmt.Sprintf("Command denied: %s", permMessage), "", nil
		case common.PermissionAutoApprove:
			return true, "", permMessage, nil
		case common.PermissionRequireApproval:
			// Fall through to request approval
		}
	} else {
		workflow.GetLogger(dCtx).Warn("Command permissions are not enabled")
	}

	l := logger.Get()
	l.Debug().Str("cmd", command).Bool("auto-approved", proceed).Msg("checkCommandPermission auto result")

	// Request user approval (legacy behavior or when permission requires approval)
	approvalPrompt := "Allow running the following command?"
	userResponse, err := GetUserApproval(dCtx, "run_command", approvalPrompt, map[string]any{
		"command":    command,
		"workingDir": workingDir,
	})
	if err != nil {
		return false, "", "", fmt.Errorf("failed to get user approval: %v", err)
	}
	if userResponse == nil || userResponse.Approved == nil || !*userResponse.Approved {
		return false, "Command execution was not approved by user. They said:\n\n" + userResponse.Content, "", nil
	}

	return true, "", "", nil
}

// RunCommand handles the execution of shell commands with user approval
func RunCommand(dCtx DevContext, params RunCommandParams) (string, error) {
	proceed, denyMessage, advisory, err := checkCommandPermission(dCtx, params.Command, params.WorkingDir)
	if err != nil {
		return "", err
	}
	if !proceed {
		return denyMessage, nil
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

	response := fmt.Sprintf("Command executed with exit status %d\n\nStdout:\n%s\n\nStderr:\n%s",
		output.ExitStatus,
		output.Stdout,
		output.Stderr)
	if advisory != "" {
		response = response + "\n\n" + advisory
	}

	return response, nil
}
