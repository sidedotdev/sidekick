package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/urfave/cli/v3"
)

// NewTaskCommand creates and returns the definition for the "task" CLI subcommand.
func NewTaskCommand() *cli.Command {
	return &cli.Command{
		Name:      "task",
		Usage:     "Create and manage a task (e.g., side task \"fix the error in my tests\")",
		ArgsUsage: "<task description>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "disable-human-in-the-loop", Usage: "Disable human-in-the-loop prompts"},
			&cli.BoolFlag{Name: "async", Usage: "Run task asynchronously and exit immediately"},
			&cli.StringFlag{Name: "flow", Value: "basic_dev", Usage: "Specify flow type (e.g., basic_dev, planned_dev)"},
			&cli.BoolFlag{Name: "P", Usage: "Shorthand for --flow planned_dev"},
			&cli.StringFlag{Name: "flow-options", Value: `{"requirements": true}`, Usage: "JSON string for flow options"},
			&cli.StringSliceFlag{Name: "flow-option", Aliases: []string{"o"}, Usage: "Add flow option (key=value), can be specified multiple times"},
			&cli.BoolFlag{Name: "no-requirements", Aliases: []string{"nr"}, Usage: "Shorthand to set requirements to false in flow options"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			taskDescription := cmd.Args().First()

			// Handle cases: "side task", "side task --help", "side task help", "side task help --help"
			if taskDescription == "" {
				// Handles "side task" (no args) and "side task --help"
				if !cmd.IsSet("help") { // True for "side task"
					_ = cli.ShowSubcommandHelp(cmd) // Show help for "task" command
					return cli.Exit("Task description is required.", 1)
				}
				// For "side task --help", urfave/cli handles help output automatically.
				return nil
			}

			if taskDescription == "help" {
				// Handles "side task help" and "side task help --help"
				if !cmd.IsSet("help") { // True for "side task help" (if --help is not also specified)
					_ = cli.ShowSubcommandHelp(cmd) // Show help for "task" command
				}
				// For "side task help --help", urfave/cli handles help.
				// For "side task help", we've shown help.
				return nil
			}

			// Ensure the Sidekick server is running before proceeding.
			if !checkServerStatus() {
				fmt.Println("Sidekick server is not running. Attempting to start it in the background...")
				if err := startServerDetached(); err != nil {
					return cli.Exit(fmt.Sprintf("Failed to start Sidekick server: %v. Please try 'side start server' manually.", err), 1)
				}

				fmt.Println("Waiting up to 10 seconds for Sidekick server to become available...")
				if !waitForServer(10 * time.Second) {
					return cli.Exit("Sidekick server did not become available in time after attempting to start. Please check server logs or start it manually using 'side start server'.", 1)
				}
				fmt.Println("Sidekick server started and is now available.")
			} else {
				fmt.Println("Sidekick server is already running.")
			}

			// If we reach here, taskDescription is a real description.
			fmt.Printf("Task command invoked for: %q\n", taskDescription)
			fmt.Printf("  Disable Human in the Loop: %t\n", cmd.Bool("disable-human-in-the-loop"))
			fmt.Printf("  Async: %t\n", cmd.Bool("async"))

			flowType := cmd.String("flow")
			if cmd.Bool("P") {
				flowType = "planned_dev"
				fmt.Println("  (Flow type overridden to 'planned_dev' by -P flag)")
			}
			fmt.Printf("  Flow Type: %s\n", flowType)

			rawFlowOptions := cmd.String("flow-options")
			fmt.Printf("  Flow Options (raw JSON): %s\n", rawFlowOptions)

			if cmd.Bool("no-requirements") {
				fmt.Println("  (--no-requirements specified: will set 'requirements' to false in flow options)")
				// Actual modification of flowOptions JSON will be in a later step
			}

			flowOptionOverrides := cmd.StringSlice("flow-option")
			if len(flowOptionOverrides) > 0 {
				fmt.Printf("  Flow Option Overrides (key=value): %v\n", flowOptionOverrides)
				// Actual parsing and application of these overrides will be in a later step
			}

			fmt.Println("\n[INFO] This is a placeholder. Task creation logic will be implemented later.")
			return nil
		},
	}
}

// startServerDetached attempts to start the Sidekick server in a detached background process
// by invoking the 'side start server' command.
func startServerDetached() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}

	cmd := exec.Command(executable, "start", "server")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Sidekick server process ('%s start server'): %w", executable, err)
	}

	if cmd.Process != nil {
		fmt.Printf("Sidekick server process initiated with PID: %d. It will run in the background.\n", cmd.Process.Pid)
	} else {
		// This case should ideally not be reached if cmd.Start() succeeds without error.
		fmt.Println("Sidekick server process initiated, but PID was not immediately available.")
	}
	// Not calling cmd.Wait() allows the current command to proceed while the server runs independently.
	return nil
}
