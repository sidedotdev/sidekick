package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"

	"go.temporal.io/sdk/workflow"
)

var errCommandNotApproved = errors.New("command not approved")

func commandPermissionPolicyKey(dCtx DevContext) string {
	// CommandPermissionConfig contains only slices of strings and booleans.
	data, err := json.Marshal(effectiveCommandPermissions(dCtx))
	if err != nil {
		panic(err)
	}
	return string(data)
}

func commandSandboxMode(dCtx DevContext) bool {
	if workflow.GetVersion(dCtx, "sandbox-command-permissions", workflow.DefaultVersion, 1) < 1 {
		return false
	}
	switch dCtx.EnvContainer.Env.GetType() {
	case env.EnvTypeDevPod, env.EnvTypeOpenShell, env.EnvTypeModal:
		return true
	default:
		return false
	}
}

func reusableCommandPermission(evaluation *common.ScriptPermissionEvaluation, previousPolicy, currentPolicy string, previousSandbox, currentSandbox, bypass bool) bool {
	if evaluation == nil {
		return false
	}
	for _, factor := range evaluation.Factors {
		if factor.Kind == common.PermissionFactorSandboxBypass {
			return bypass
		}
	}
	return previousPolicy == currentPolicy && previousSandbox == currentSandbox &&
		(evaluation.Outcome == common.PermissionAutoApprove || evaluation.Approved)
}

func runCommandWithExpectedEnv(dCtx DevContext, params RunCommandParams) (string, error) {
	var evaluation *common.ScriptPermissionEvaluation
	var previousPolicy, denyMessage, advisory string
	var previousSandbox bool
	var output env.EnvRunCommandActivityOutput

	dispatch := func() (workflow.Future, error) {
		for {
			if err := dCtx.Err(); err != nil {
				return nil, err
			}
			tracker := env.EnvTrackerFromContext(dCtx)
			if tracker != nil {
				if err := workflow.Await(dCtx, func() bool { return !tracker.SwitchInProgress() }); err != nil {
					return nil, err
				}
			}
			target := *dCtx.EnvContainer
			if tracker != nil {
				target = tracker.Resolve(target)
			}
			attempt := dCtx
			attempt.EnvContainer = &target
			guarded, err := env.WithExpectedEnv(dCtx.Context, target)
			if err != nil {
				return nil, err
			}
			policy := commandPermissionPolicyKey(attempt)
			sandbox := commandSandboxMode(attempt)
			bypass := sandbox && workflow.GetVersion(dCtx, "sandbox-command-permissions-bypass", workflow.DefaultVersion, 1) >= 1
			if !reusableCommandPermission(evaluation, previousPolicy, policy, previousSandbox, sandbox, bypass) {
				var proceed bool
				approved := evaluation != nil && evaluation.Approved && !previousSandbox && !sandbox
				proceed, denyMessage, advisory, evaluation, err = checkCommandPermissionWithEvaluation(attempt, params.Command, params.WorkingDir, approved)
				if err != nil {
					if contextErr := dCtx.Err(); contextErr != nil {
						return nil, contextErr
					}
					return nil, err
				}
				if !proceed {
					return nil, errCommandNotApproved
				}
				previousPolicy, previousSandbox = policy, sandbox
			}
			// Policy publication can occur while evaluation or approval yields,
			// even when the environment's identity is unchanged.
			if policy != commandPermissionPolicyKey(dCtx) {
				continue
			}
			if err := dCtx.Err(); err != nil {
				return nil, err
			}

			relWorkDir := "."
			if params.WorkingDir != "" {
				relWorkDir = filepath.Clean(params.WorkingDir)
			}
			var committerName, committerEmail string
			if dCtx.GlobalState != nil {
				committerName = dCtx.GlobalState.GetStringValue("committerName")
				committerEmail = dCtx.GlobalState.GetStringValue("committerEmail")
			}
			input := env.EnvRunCommandActivityInput{
				EnvContainer:       target,
				Command:            "sh",
				Args:               []string{"-c", params.Command},
				RelativeWorkingDir: relWorkDir,
				EnvVars:            git.BuildGitEnvVars(committerName, committerEmail),
			}
			future := workflow.ExecuteActivity(guarded, env.EnvRunCommandActivity, input)
			if future.IsReady() && errors.Is(future.Get(dCtx, nil), env.ErrEnvChanged) {
				continue
			}
			return future, nil
		}
	}

	var err error
	if workflow.GetVersion(dCtx, "docker-command-recovery", workflow.DefaultVersion, 1) >= 1 {
		err = flow_action.PerformWithUserRetryOptions(
			flow_action.ActionContext{ExecContext: dCtx.ExecContext, ActionType: "run_command"},
			flow_action.UserRetryOptions{
				Dispatch: dispatch, OnFailure: DockerFailureHandler{}, PropagateCancellation: true,
			},
			env.EnvRunCommandActivity, &output,
		)
	} else {
		var future workflow.Future
		future, err = dispatch()
		if err == nil {
			err = future.Get(dCtx, &output)
		}
	}
	if errors.Is(err, errCommandNotApproved) {
		return denyMessage, nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to execute command: %w", err)
	}
	response := fmt.Sprintf("Command executed with exit status %d\n\nStdout:\n%s\n\nStderr:\n%s",
		output.ExitStatus, output.Stdout, output.Stderr)
	if advisory != "" {
		response += "\n\n" + advisory
	}
	return response, nil
}
