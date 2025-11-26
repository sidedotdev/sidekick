package dev

import (
	"fmt"
	"path/filepath"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"

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

// RunCommand handles the execution of shell commands with user approval
func RunCommand(dCtx DevContext, params RunCommandParams) (string, error) {
	// Format approval prompt
	approvalPrompt := "Allow running the following command?"

	// Get user approval
	userResponse, err := flow_action.GetUserApproval(dCtx.ExecContext, "run_command", approvalPrompt, map[string]any{
		"command":    params.Command,
		"workingDir": params.WorkingDir,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get user approval: %v", err)
	}

	if userResponse == nil || userResponse.Approved == nil || !*userResponse.Approved {
		return "Command execution was not approved by user. They said:\n\n" + userResponse.Content, nil
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
