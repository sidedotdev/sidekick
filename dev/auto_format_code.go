package dev

import (
	"fmt"
	"sidekick/env"

	"go.temporal.io/sdk/workflow"
)

func AutoFormatCode(dCtx DevContext) error {
	// TODO /gen take in a list of files/directories to format

	// TODO /gen make this configurable via repo config list of autoformat_commands

	// TODO /gen if not configured, then detect the list of languages used in
	// the repo, and use the appropriate formatter (for languages that have a
	// standard formatter). Or if the repo has a .editorconfig, .prettierrc,
	// rustfmt.toml or .rustfmt.toml etc, use that to determine the formatter
	err := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "go",
		Args:               []string{"fmt", "./..."},
	}).Get(dCtx, nil)
	if err != nil {
		return fmt.Errorf("failed to format the code: %v", err)
	}
	return nil
}
