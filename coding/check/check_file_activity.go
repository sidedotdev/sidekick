package check

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/env"
	"strings"
)

// CheckFileActivityOutput is the output of the CheckFileActivity function.
type CheckFileActivityOutput struct {
	// CheckPassed is a map from check command to whether it passed.
	CheckPassed map[string]bool
	// AllPassed indicates whether all checks passed.
	AllPassed bool
	// Output contains the combined output of all check commands.
	Output string
}

// returns whether the file passed all the check commands and built-in
// language-specific checks. if any checks did not pass, the output contains the
// errors from the non-passing checks
type CheckFileActivityInput struct {
	EnvContainer  env.EnvContainer
	FilePath      string
	CheckCommands []common.CommandConfig
}

func CheckFileActivity(input CheckFileActivityInput) (CheckFileActivityOutput, error) {
	// Initialize a variable to store the combined output of all check commands
	var combinedOutput string
	allPassed := true
	checkPassed := make(map[string]bool)

	// Run all the check_commands via a shell using the envContainer.Env.RunCommand
	for _, command := range input.CheckCommands {
		shellCommand := strings.ReplaceAll(command.Command, "{file}", input.FilePath)
		output, err := input.EnvContainer.Env.RunCommand(context.Background(), env.EnvRunCommandInput{
			RelativeWorkingDir: command.WorkingDir,
			Command:            "/usr/bin/env",
			Args:               []string{"sh", "-c", shellCommand},
		})
		if err != nil {
			// Append the error message to combinedOutput and continue with the next check command
			combinedOutput += fmt.Sprintf("failed to run check command '%s': %v\n", command.Command, err)
			allPassed = false
			checkPassed[command.Command] = false
			continue
		}
		combinedOutput += fmt.Sprintf("check command: %s\n", command.Command)
		if output.ExitStatus != 0 {
			combinedOutput += fmt.Sprintf("check passed: false\n")
			combinedOutput += output.Stdout + "\n" + output.Stderr
			allPassed = false
			checkPassed[command.Command] = false
		} else {
			combinedOutput += fmt.Sprintf("check passed: true\n")
			checkPassed[command.Command] = true
			// leave out the rest of the output, if any, when it passed
		}
	}

	valid, checkOutput, err := CheckFileValidity(input.EnvContainer, input.FilePath)
	if err != nil {
		return CheckFileActivityOutput{}, fmt.Errorf("failed to check file validity: %w", err)
	}
	if !valid {
		combinedOutput += fmt.Sprintf("errors found when checking file validity: %s\n", checkOutput)
		allPassed = false
	}
	checkPassed["baseFileValidityChecks"] = valid

	return CheckFileActivityOutput{
		CheckPassed: checkPassed,
		AllPassed:   allPassed,
		Output:      combinedOutput,
	}, nil
}
