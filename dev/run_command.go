package dev

import (
	"fmt"
	"path/filepath"
	"sidekick/env"
	"sidekick/llm"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

type RunCommandParams struct {
	Command    string `json:"command" jsonschema:"description=The shell command or script to execute"`
	WorkingDir string `json:"workingDir,omitempty" jsonschema:"description=Optional working directory relative to the repository root directory"`
}

var runCommandTool = llm.Tool{
	Name:        "run_command",
	Description: "Used to execute shell commands after getting user approval. The command will be run through the 'sh' shell if approved.",
	Parameters:  (&jsonschema.Reflector{ExpandedStruct: true}).Reflect(&RunCommandParams{}),
}

// RunCommand handles the execution of shell commands with user approval
func RunCommand(dCtx DevContext, params RunCommandParams) (string, error) {
	// Format approval prompt
	approvalPrompt := fmt.Sprintf("Allow running the following command?\n\n%s", params.Command)

	// Get user approval
	actionCtx := dCtx.NewActionContext("Approve Command")
	userResponse, err := GetUserApproval(actionCtx, approvalPrompt, map[string]any{
		"command":    params.Command,
		"workingDir": params.WorkingDir,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get user approval: %v", err)
	}

	if userResponse == nil || !*userResponse.Approved {
		return "Command execution was not approved by user", nil
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
