package main

import (
	"context"
	"errors" // New import
	"fmt"
	"io" // New import for io.EOF
	"os"
	"os/exec"
	"path/filepath" // New import
	"sort"          // New import
	"time"

	"sidekick/domain" // New import

	"github.com/segmentio/ksuid" // New import
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

			// Workspace handling
			disableHumanInTheLoop := cmd.Bool("disable-human-in-the-loop")
			workspace, err := ensureWorkspace(ctx, disableHumanInTheLoop)
			if err != nil {
				return cli.Exit(fmt.Sprintf("Workspace setup failed: %v", err), 1)
			}
			fmt.Printf("Using workspace: %s (ID: %s, Path: %s)\\n", workspace.Name, workspace.Id, workspace.LocalRepoDir)

			// If we reach here, taskDescription is a real description.
			fmt.Printf("Task command invoked for: %q\\n", taskDescription)
			fmt.Printf("  Disable Human in the Loop: %t\\n", disableHumanInTheLoop)
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

// ensureWorkspace handles finding, creating, or selecting a workspace.
func ensureWorkspace(ctx context.Context, disableHumanInTheLoop bool) (*domain.Workspace, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}
	absPath, err := filepath.Abs(currentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for current directory: %w", err)
	}

	fmt.Printf("Looking for workspace in directory: %s\\n", absPath)

	// Step 1: Find existing workspaces for the current directory (via API call)
	// Placeholder for API call: apiClient.GetWorkspacesByPath(ctx, absPath)
	workspaces, err := getWorkspacesByPathAPI(ctx, absPath) // Assumed API client method
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve workspaces for path %s: %w", absPath, err)
	}

	if len(workspaces) == 0 {
		// Step 2: If none exists, create one automatically
		fmt.Printf("No existing workspace found for %s. Creating a new one.\\n", absPath)
		dirName := filepath.Base(absPath)
		defaultWorkspaceName := fmt.Sprintf("%s-workspace", dirName) // Default name

		// Placeholder for API call: apiClient.CreateWorkspace(ctx, defaultWorkspaceName, absPath)
		createdWorkspace, err := createWorkspaceAPI(ctx, defaultWorkspaceName, absPath) // Assumed API client method
		if err != nil {
			return nil, fmt.Errorf("failed to create workspace for path %s: %w", absPath, err)
		}
		fmt.Printf("Successfully created workspace: %s (ID: %s)\\n", createdWorkspace.Name, createdWorkspace.Id)
		return createdWorkspace, nil
	}

	if len(workspaces) == 1 {
		// Only one workspace found, use it.
		fmt.Printf("Found existing workspace: %s\\n", workspaces[0].Name)
		return workspaces[0], nil
	}

	// Step 3: Multiple workspaces match
	fmt.Printf("Multiple workspaces found for directory %s:\\n", absPath)
	// Sort by name for consistent display order before prompting
	sort.Slice(workspaces, func(i, j int) bool {
		if workspaces[i].Name != workspaces[j].Name {
			return workspaces[i].Name < workspaces[j].Name
		}
		return workspaces[i].Id < workspaces[j].Id // Secondary sort by ID if names are identical
	})

	for i, ws := range workspaces {
		fmt.Printf("  %d: %s (ID: %s, Updated: %s)\\n", i+1, ws.Name, ws.Id, ws.Updated.Format(time.RFC3339))
	}

	if disableHumanInTheLoop {
		// Sort by Updated (descending) to get the most recent one
		sort.Slice(workspaces, func(i, j int) bool {
			return workspaces[i].Updated.After(workspaces[j].Updated)
		})
		fmt.Printf("Human-in-the-loop disabled. Using the most recently updated workspace: %s\\n", workspaces[0].Name)
		return workspaces[0], nil
	}

	// Prompt user to select
	var choice int
	for {
		fmt.Print("Please select a workspace by number: ")
		// Basic prompt, consider using a library for better UX
		var input string
		if _, err := fmt.Scanln(&input); err != nil {
			// Handle EOF or other scan errors
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("input aborted by user")
			}
			fmt.Println("Error reading input. Please try again.")
			continue
		}

		numScanned, scanErr := fmt.Sscan(input, &choice)
		if scanErr == nil && numScanned == 1 && choice > 0 && choice <= len(workspaces) {
			break
		}
		fmt.Println("Invalid selection. Please enter a number from the list.")
	}
	return workspaces[choice-1], nil // User choice is 1-based
}

// --- Placeholder API client functions ---
// These functions simulate what an API client would do.
// They need to be replaced with actual HTTP calls to the Sidekick server.

// getWorkspacesByPathAPI is a placeholder for an API call to the server.
func getWorkspacesByPathAPI(ctx context.Context, path string) ([]*domain.Workspace, error) {
	// TODO: Implement actual API call: GET /api/v1/workspaces?path={path}
	// This function should ideally live in an API client package.
	fmt.Printf("[INFO] STUBBED API CALL: getWorkspacesByPathAPI for path %s. Simulating NO workspace found.\\n", path)

	// Simulate different scenarios for testing by uncommenting:
	// return []*domain.Workspace{}, nil // No workspace

	// return []*domain.Workspace{ // One workspace
	// 	{Id: "ws_single_abcdef12345", Name: filepath.Base(path) + "-ws", LocalRepoDir: path, Created: time.Now(), Updated: time.Now()},
	// }, nil

	// return []*domain.Workspace{ // Multiple workspaces
	//  {Id: "ws_multi_alpha67890", Name: filepath.Base(path) + "-alpha-ws", LocalRepoDir: path, Created: time.Now().Add(-2 * time.Hour), Updated: time.Now().Add(-time.Hour)},
	//  {Id: "ws_multi_beta12345", Name: filepath.Base(path) + "-beta-ws", LocalRepoDir: path, Created: time.Now().Add(-time.Hour), Updated: time.Now()},
	// }, nil

	return []*domain.Workspace{}, nil // Default: Simulate no workspace found
}

// createWorkspaceAPI is a placeholder for an API call to the server.
func createWorkspaceAPI(ctx context.Context, name string, path string) (*domain.Workspace, error) {
	// TODO: Implement actual API call: POST /api/v1/workspaces with {name, localRepoDir}
	// This function should ideally live in an API client package.
	fmt.Printf("[INFO] STUBBED API CALL: createWorkspaceAPI with name '%s', path '%s'. Simulating creation.\\n", name, path)

	newWorkspace := &domain.Workspace{
		Id:           "ws_" + ksuid.New().String(), // ksuid needs "github.com/segmentio/ksuid"
		Name:         name,
		LocalRepoDir: path,
		Created:      time.Now(),
		Updated:      time.Now(),
	}
	return newWorkspace, nil
}

// --- End Placeholder API client functions ---
