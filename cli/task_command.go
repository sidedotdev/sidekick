package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"sidekick/client"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/tui"

	"github.com/urfave/cli/v3"
)

func NewTaskCommand() *cli.Command {
	return &cli.Command{
		Name:      "task",
		Usage:     "Start a new task (e.g., side task \"fix the error in my tests\")",
		ArgsUsage: "<task description>",
		Flags: []cli.Flag{
			// TODO support this flag, after introducing a way to provide a customized DevConfig per invoked flow
			//&cli.BoolFlag{Name: "disable-human-in-the-loop", Usage: "Disable human-in-the-loop prompts"},
			&cli.BoolFlag{Name: "async", Usage: "Run task asynchronously and exit immediately"},
			&cli.StringFlag{Name: "flow", Value: "basic_dev", Usage: "Specify flow type (e.g., basic_dev, planned_dev)"},
			&cli.BoolFlag{Name: "plan", Aliases: []string{"p"}, Usage: "Shorthand for --flow planned_dev"},
			&cli.StringFlag{Name: "flow-options", Value: `{"determineRequirements": true}`, Usage: "JSON string for flow options"},
			&cli.StringSliceFlag{Name: "flow-option", Aliases: []string{"O"}, Usage: "Add flow option (key=value), can be specified multiple times"},
			&cli.BoolFlag{Name: "no-requirements", Aliases: []string{"n"}, Usage: "Shorthand to set determineRequirements to false in flow options"},
			&cli.BoolFlag{Name: "worktree", Aliases: []string{"w"}, Usage: "Use a git worktree. Sets --start-branch to the current branch if not specified."},
			&cli.StringFlag{Name: "start-branch", Aliases: []string{"B"}, Usage: "The worktree start branch. Implies --worktree"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			c := client.NewClient(fmt.Sprintf("http://localhost:%d", common.GetServerPort()))
			return executeTaskCommand(ctx, c, cmd)
		},
	}
}

// parseFlowOptions combines --flow-options JSON with individual --flow-option key=value pairs,
// with the latter taking precedence
func parseFlowOptions(ctx context.Context, cmd *cli.Command, currentDir string) (map[string]interface{}, error) {
	flowOpts := make(map[string]interface{})

	optionsJSON := cmd.String("flow-options")
	if err := json.Unmarshal([]byte(optionsJSON), &flowOpts); err != nil {
		return nil, fmt.Errorf("invalid --flow-options JSON (value: %s): %w", optionsJSON, err)
	}

	// --no-requirements flag overrides the "determineRequirements" key
	if cmd.Bool("no-requirements") {
		flowOpts["determineRequirements"] = false
	}

	// --disable-human-in-the-loop flag sets configOverrides
	if cmd.Bool("disable-human-in-the-loop") {
		configOverrides := map[string]interface{}{"disableHumanInTheLoop": true}
		flowOpts["configOverrides"] = configOverrides
	}

	// --flow-option key=value pairs override any existing keys
	for _, optStr := range cmd.StringSlice("flow-option") {
		key, valueStr, didCut := strings.Cut(optStr, "=")
		if !didCut {
			return nil, fmt.Errorf("invalid --flow-option format: '%s'. Expected key=value", optStr)
		}
		if key == "" {
			return nil, fmt.Errorf("invalid --flow-option format: '%s'. Key cannot be empty", optStr)
		}

		// Remove enclosing quotes to support both quoted and unquoted values
		if (strings.HasPrefix(valueStr, `"`) && strings.HasSuffix(valueStr, `"`)) ||
			(strings.HasPrefix(valueStr, "`") && strings.HasSuffix(valueStr, "`")) {
			if len(valueStr) >= 2 {
				valueStr = valueStr[1 : len(valueStr)-1]
			} else {
				valueStr = ""
			}
		}
		flowOpts[key] = valueStr
	}

	// --worktree flag or --start-branch flag implies worktree environment
	if cmd.Bool("worktree") || cmd.String("start-branch") != "" {
		flowOpts["envType"] = "local_git_worktree"
	}

	// --start-branch flag overrides startBranch
	if startBranch := cmd.String("start-branch"); startBranch != "" {
		flowOpts["startBranch"] = startBranch
	}

	// If envType is local_git_worktree but startBranch is not set, detect current branch
	if envType, ok := flowOpts["envType"]; ok && envType == "local_git_worktree" {
		if _, hasStartBranch := flowOpts["startBranch"]; !hasStartBranch {
			branchState, err := git.GetCurrentBranch(ctx, currentDir)
			if err != nil {
				return nil, fmt.Errorf("failed to detect current git branch for worktree: %w", err)
			}
			if branchState.IsDetached {
				return nil, fmt.Errorf("cannot use worktree with detached HEAD state, please specify --start-branch or checkout a branch")
			}
			flowOpts["startBranch"] = branchState.Name
		}
	}

	return flowOpts, nil
}

func executeTaskCommand(ctx context.Context, c client.Client, cmd *cli.Command) error {
	currentDir, err := os.Getwd()
	if err != nil {
		return cli.Exit(fmt.Errorf("Error getting current working directory: %w", err), 1)
	}

	req, err := buildCreateTaskRequest(ctx, cmd, currentDir)
	if err != nil {
		return err
	}

	if cmd.Bool("async") {
		req.Async = true
	}

	if err := tui.RunTaskUI(ctx, c, req, currentDir); err != nil {
		return cli.Exit(err, 1)
	}

	return nil
}

func buildCreateTaskRequest(ctx context.Context, cmd *cli.Command, currentDir string) (*client.CreateTaskRequest, error) {
	taskDescription := cmd.Args().First()

	if taskDescription == "" {
		return nil, cli.Exit("ERROR:\n   A task description is required.\n\nUSAGE:\n  side task <task description>\n\nRun `side task help` to see all options.", 1)
	}

	flowType := cmd.String("flow")
	if cmd.Bool("plan") {
		flowType = "planned_dev"
	}

	flowOpts, err := parseFlowOptions(ctx, cmd, currentDir)
	if err != nil {
		return nil, cli.Exit(fmt.Errorf("Error parsing flow options: %v", err), 1)
	}

	req := &client.CreateTaskRequest{
		Description: taskDescription,
		FlowType:    flowType,
		FlowOptions: flowOpts,
	}
	return req, nil
}
