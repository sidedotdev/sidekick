package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"encoding/json"
	"net/url"
	"os/signal"
	"strings"
	"syscall"

	"sidekick/client"
	"sidekick/common"
	"sidekick/domain"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/erikgeiser/promptkit/selection"
	"github.com/gorilla/websocket"
	"github.com/urfave/cli/v3"
)

// clientTaskRequestPayload represents the client-side task creation request,
// containing only fields that can be set by clients
type clientTaskRequestPayload struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	FlowType    string                 `json:"flowType"`
	FlowOptions map[string]interface{} `json:"flowOptions"`
}

// parseFlowOptions combines --flow-options JSON with individual --flow-option key=value pairs,
// with the latter taking precedence
func parseFlowOptions(cmd *cli.Command) (map[string]interface{}, error) {
	flowOpts := make(map[string]interface{})

	optionsJSON := cmd.String("flow-options")
	if err := json.Unmarshal([]byte(optionsJSON), &flowOpts); err != nil {
		return nil, fmt.Errorf("invalid --flow-options JSON (value: %s): %w", optionsJSON, err)
	}

	// --no-requirements flag overrides the "determineRequirements" key
	if cmd.Bool("no-requirements") {
		flowOpts["determineRequirements"] = false
	}

	// --flow-option key=value pairs override any existing keys
	for _, optStr := range cmd.StringSlice("flow-option") {
		parts := strings.SplitN(optStr, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --flow-option format: '%s'. Expected key=value", optStr)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("invalid --flow-option format: '%s'. Key cannot be empty", optStr)
		}
		valueStr := parts[1]

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
	return flowOpts, nil
}

func waitForFlow(ctx context.Context, c client.Client, workspaceID, taskID string) string {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(3 * time.Second)

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return ""
		case <-timeout:
			if lastErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: error fetching task details: %v\n", lastErr)
			}
			return ""
		case <-ticker.C:
			task, err := c.GetTask(workspaceID, taskID)
			if err != nil {
				lastErr = err
				continue
			}
			if len(task.Flows) > 0 {
				return task.Flows[0].Id
			}
		}
	}
}

// streamTaskProgress provides real-time updates of task execution by streaming flow action changes
// through a WebSocket connection. It handles connection lifecycle and updates a Bubble Tea UI model
// to display progress.
func streamTaskProgress(ctx context.Context, sigChan chan os.Signal, workspaceID, flowID, taskID string, wg *sync.WaitGroup) {
	defer wg.Done()
	serverPort := common.GetServerPort()
	u := url.URL{Scheme: "ws", Host: fmt.Sprintf("localhost:%d", serverPort), Path: fmt.Sprintf("/ws/v1/workspaces/%s/flows/%s/action_changes_ws", workspaceID, flowID)}

	conn, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to connect to WebSocket for task progress: %v", err)
		if resp != nil {
			bodyBytes, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				errMsg = fmt.Sprintf("Failed to connect to WebSocket for task progress (status %s): %v. Response body: %s", resp.Status, err, string(bodyBytes))
			}
		}
		fmt.Fprintln(os.Stderr, errMsg)
		return
	}
	defer conn.Close()

	p := tea.NewProgram(newProgressModel(taskID, flowID))
	done := make(chan struct{})

	// Goroutine to read from WebSocket and send messages to Bubble Tea program
	go func() {
		defer close(done)
		for {
			var action domain.FlowAction
			if err := conn.ReadJSON(&action); err != nil {
				if ctx.Err() != nil {
					// Context was cancelled, exit cleanly
					return
				}

				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					// Normal closure from server or our cancellation handler
					p.Send(taskCompleteMsg{})
				} else {
					// Unexpected error
					p.Send(taskErrorMsg{err: fmt.Errorf("websocket read error: %w", err)})
				}
				return
			}

			p.Send(taskProgressMsg{
				taskID:       taskID,
				actionType:   action.ActionType,
				actionStatus: action.ActionStatus,
			})
		}
	}()

	go func() {
		<-ctx.Done()
		conn.Close()
		p.Send(contextCancelledMsg{})
	}()

	model, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running progress view: %v\n", err)
	}
	if sig := model.(taskProgressModel).signal; sig != nil {
		sigChan <- *sig
	}
}

func executeTaskCommand(c client.Client, cmd *cli.Command) error {
	taskDescription := cmd.Args().First()

	if taskDescription == "" {
		if !cmd.IsSet("help") {
			_ = cli.ShowSubcommandHelp(cmd)
			return cli.Exit("Task description is required.", 1)
		}
		return nil
	}

	if taskDescription == "help" {
		if !cmd.IsSet("help") {
			_ = cli.ShowSubcommandHelp(cmd)
		}
		return nil
	}

	// Ensure the Sidekick server is running before proceeding.
	if !checkServerStatus() {
		fmt.Println("Starting sidekick server...")
		if err := startServerDetached(); err != nil {
			return cli.Exit(fmt.Sprintf("Failed to start Sidekick server: %v. Please try running `side start` manually.", err), 1)
		}

		if !waitForServer(10 * time.Second) {
			return cli.Exit("Failed to start Sidekick server. Please check logs or run 'side start server' manually.", 1)
		}
	}

	disableHumanInTheLoop := cmd.Bool("disable-human-in-the-loop")
	workspace, err := ensureWorkspace(context.Background(), c, disableHumanInTheLoop)
	if err != nil {
		return cli.Exit(fmt.Sprintf("Workspace setup failed: %v", err), 1)
	}

	flowType := cmd.String("flow")
	if cmd.Bool("P") {
		flowType = "planned_dev"
	}

	flowOpts, err := parseFlowOptions(cmd)
	if err != nil {
		return cli.Exit(fmt.Sprintf("Error processing flow options: %v", err), 1)
	}

	req := &client.CreateTaskRequest{
		Title:       taskDescription,
		Description: taskDescription,
		FlowType:    flowType,
		FlowOptions: flowOpts,
	}

	task, err := c.CreateTask(workspace.Id, req)
	if err != nil {
		return cli.Exit(fmt.Sprintf("Failed to create task via API: %v", err), 1)
	}

	if cmd.Bool("async") {
		fmt.Println("Task submitted")
		return nil
	}

	monitor := NewTaskMonitor(c, workspace.Id, task.Id)
	model := newLifecycleModel(task.Id, "")
	p := tea.NewProgram(model)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		monitor.Stop()
		if err := c.CancelTask(workspace.Id, task.Id); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to cancel task: %v\n", err)
		}
	}()

	if _, err := p.Run(); err != nil {
		return cli.Exit(fmt.Sprintf("Error running task UI: %v", err), 1)
	}

	return nil
}

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
			&cli.StringFlag{Name: "flow-options", Value: `{"determineRequirements": true}`, Usage: "JSON string for flow options"},
			&cli.StringSliceFlag{Name: "flow-option", Aliases: []string{"o"}, Usage: "Add flow option (key=value), can be specified multiple times"},
			&cli.BoolFlag{Name: "no-requirements", Aliases: []string{"nr"}, Usage: "Shorthand to set determineRequirements to false in flow options"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			client := client.NewClient(fmt.Sprintf("http://localhost:%d", common.GetServerPort()))
			return executeTaskCommand(client, cmd)
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
func ensureWorkspace(ctx context.Context, c client.Client, disableHumanInTheLoop bool) (*domain.Workspace, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}
	absPath, err := filepath.Abs(currentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for current directory: %w", err)
	}

	// Step 1: Find existing workspaces for the current directory
	workspacesResult, err := c.GetWorkspacesByPath(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve workspaces for path %s: %w", absPath, err)
	}

	// Convert to pointer slice for consistency with existing code
	workspaces := make([]*domain.Workspace, len(workspacesResult))
	for i := range workspacesResult {
		workspaces[i] = &workspacesResult[i]
	}

	if len(workspaces) == 0 {
		// Step 2: If none exists, create one automatically
		fmt.Println("Creating workspace")
		dirName := filepath.Base(absPath)
		defaultWorkspaceName := fmt.Sprintf("%s-workspace", dirName)

		req := &client.CreateWorkspaceRequest{
			Name:         defaultWorkspaceName,
			LocalRepoDir: absPath,
		}
		createdWorkspace, err := c.CreateWorkspace(req)
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
	fmt.Printf("Multiple workspaces found for directory %s:\n", absPath)
	// Sort by name for consistent display order before prompting
	sort.Slice(workspaces, func(i, j int) bool {
		if workspaces[i].Name != workspaces[j].Name {
			return workspaces[i].Name < workspaces[j].Name
		}
		return workspaces[i].Id < workspaces[j].Id // Secondary sort by ID if names are identical
	})

	if disableHumanInTheLoop {
		// Sort by Updated (descending) to get the most recent one
		sort.Slice(workspaces, func(i, j int) bool {
			return workspaces[i].Updated.After(workspaces[j].Updated)
		})
		fmt.Printf("Human-in-the-loop disabled. Using the most recently updated workspace: %s\n", workspaces[0].Name)
		return workspaces[0], nil
	}

	// Prompt user to select
	workspaceMap := make(map[string]*domain.Workspace)
	workspaceStrings := make([]string, len(workspaces))
	for i, ws := range workspaces {
		wsString := fmt.Sprintf("%s (ID: %s, Updated: %s)", ws.Name, ws.Id, ws.Updated.Format(time.RFC3339))
		workspaceStrings[i] = wsString
		workspaceMap[wsString] = ws
	}

	prompt := selection.New("Please select a workspace", workspaceStrings)

	selectedWorkspaceString, err := prompt.RunPrompt()
	if err != nil {
		return nil, fmt.Errorf("workspace selection failed: %w", err)
	}

	return workspaceMap[selectedWorkspaceString], nil
}

// --- Placeholder API client functions ---
