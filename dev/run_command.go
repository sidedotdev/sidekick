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
	Command    string `json:"command" jsonschema:"description=The shell command to execute,required"`
	WorkingDir string `json:"workingDir,omitempty" jsonschema:"description=Optional working directory relative to environment's working directory"`
}

var runCommandTool = llm.Tool{
	Name:        "run_command",
	Description: "Used to execute shell commands after getting user approval. The command will be run through 'sh' shell. If workingDir is provided, it will be interpreted relative to the environment's working directory.",
	Parameters:  (&jsonschema.Reflector{ExpandedStruct: true}).Reflect(&RunCommandParams{}),
}

// RunCommand handles the execution of shell commands with user approval
func RunCommand(dCtx DevContext, params RunCommandParams) (string, error) {
	// Format approval prompt
	workDirInfo := ""
	if params.WorkingDir != "" {
		workDirInfo = fmt.Sprintf(" in directory '%s'", params.WorkingDir)
	}
	approvalPrompt := fmt.Sprintf("Do you approve running the following command%s?\n\n%s", workDirInfo, params.Command)

	// Get user approval
	userResponse, err := GetUserApproval(dCtx.NewActionContext("RunCommand"), approvalPrompt, map[string]interface{}{
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
