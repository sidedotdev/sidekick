package dev

import (
	"fmt"
	"sidekick/env"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

func AutoFormatCode(dCtx DevContext) error {
	// TODO /gen take in a list of files/directories to format

	// TODO /gen make this configurable via repo config list of autoformat_commands

	// TODO /gen if not configured, then detect the list of languages used in
	// the repo, and use the appropriate formatter (for languages that have a
	// standard formatter). Or if the repo has a .editorconfig, .prettierrc,
	// rustfmt.toml or .rustfmt.toml etc, use that to determine the formatter
	input := env.EnvRunCommandActivityInput{
		EnvContainer:       *dCtx.EnvContainer,
		RelativeWorkingDir: "./",
		Command:            "go",
		Args:               []string{"fmt", "./..."},
	}
	var err error
	if workflow.GetVersion(dCtx, "auto-format-user-retry", workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		err = workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, input).Get(dCtx, nil)
	} else {
		err = flow_action.PerformActivityWithUserRetry(dCtx.ExecContext, "Format code", env.EnvRunCommandActivity, nil, input)
	}
	if err != nil {
		return fmt.Errorf("failed to format the code: %w", err)
	}
	return nil
}
