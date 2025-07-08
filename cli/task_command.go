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

var apiClient *client.Client

// initClient initializes the API client if not already initialized
func initClient() (*client.Client, error) {
	if apiClient != nil {
		return apiClient, nil
	}

	apiClient = client.NewClient()
	return apiClient, nil
}

// clientTaskRequestPayload defines the structure for the task creation API request.
// It omits fields like ID, AgentType, and Status, which are expected to be set by the server.
type clientTaskRequestPayload struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	FlowType    string                 `json:"flowType"`
	FlowOptions map[string]interface{} `json:"flowOptions"`
}

// parseFlowOptions constructs the flow options map by combining JSON from --flow-options with
// individual key-value pairs from --flow-option flags. Values from --flow-option override
// any existing keys from --flow-options.
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
		// Values from --flow-option are always strings. Use --flow-options JSON for typed values.
		flowOpts[key] = valueStr
	}
	return flowOpts, nil
}

func createTaskFromPayload(workspaceID string, payload []byte) (*domain.Task, error) {
	var req client.CreateTaskRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task request payload: %w", err)
	}
	return apiClient.CreateTask(workspaceID, &req)
}

func getTaskDetails(workspaceID string, taskID string) (*client.GetTaskResponse, error) {
	return apiClient.GetTask(workspaceID, taskID)
}

// waitForFlow attempts to get the flow ID for a task, retrying for up to 3 seconds.
// Returns the flow ID if found, or an empty string if no flow is available after timeout.
func waitForFlow(ctx context.Context, workspaceID, taskID string) string {
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
			details, err := getTaskDetails(workspaceID, taskID)
			if err != nil {
				lastErr = err
				continue
			}
			if len(details.Task.Flows) > 0 {
				return details.Task.Flows[0].Id
			}
		}
	}
}

// streamTaskProgress connects to the WebSocket endpoint to stream flow action changes for a task.
// It uses flowID to identify the specific flow to stream actions from.
func streamTaskProgress(ctx context.Context, workspaceID, flowID, taskID string, wg *sync.WaitGroup) {
	defer wg.Done()
	serverPort := common.GetServerPort()
	// Path: /ws/v1/workspaces/:workspaceId/flows/:flowId/action_changes_ws
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

	// Goroutine to handle context cancellation
	go func() {
		<-ctx.Done()
		conn.Close()
		p.Send(contextCancelledMsg{})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running progress view: %v\n", err)
	}

	<-done
}

func cancelTask(workspaceID string, taskID string) error {
	return apiClient.CancelTask(workspaceID, taskID)
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

			// Initialize API client
			client, err := initClient()
			if err != nil {
				return cli.Exit(fmt.Sprintf("Failed to initialize API client: %v", err), 1)
			}
			_ = client // TODO: Use client in subsequent API calls

			// Workspace handling
			disableHumanInTheLoop := cmd.Bool("disable-human-in-the-loop")
			workspace, err := ensureWorkspace(ctx, disableHumanInTheLoop)
			if err != nil {
				return cli.Exit(fmt.Sprintf("Workspace setup failed: %v", err), 1)
			}
			fmt.Printf("Using workspace: %s (ID: %s, Path: %s)\n", workspace.Name, workspace.Id, workspace.LocalRepoDir)

			flowType := cmd.String("flow")
			if cmd.Bool("P") {
				flowType = "planned_dev"
			}

			flowOpts, err := parseFlowOptions(cmd)
			if err != nil {
				return cli.Exit(fmt.Sprintf("Error processing flow options: %v", err), 1)
			}

			// Use taskDescription for both Title and Description for now.
			// Title could be a summarized version later if needed.
			requestPayload := clientTaskRequestPayload{
				Title:       taskDescription,
				Description: taskDescription,
				FlowType:    flowType,
				FlowOptions: flowOpts,
			}

			payloadBytes, err := json.Marshal(requestPayload)
			if err != nil {
				return cli.Exit(fmt.Sprintf("Failed to marshal task creation payload: %v", err), 1)
			}

			// 2. Make the HTTP POST request
			fmt.Printf("Attempting to create task '%s' in workspace '%s' (ID: %s)...\n", taskDescription, workspace.Name, workspace.Id)
			// fmt.Printf("Payload: %s\\n", string(payloadBytes)) // For debugging, can be removed later

			task, err := createTaskFromPayload(workspace.Id, payloadBytes)
			if err != nil {
				return cli.Exit(fmt.Sprintf("Failed to create task via API: %v", err), 1)
			}

			taskID := task.Id

			fmt.Printf("Successfully created task with ID: %s\n", taskID)
			// Store taskID if needed for sync mode (next steps)
			// e.g., cmd.Context().Set("createdTaskID", taskID) // urfave/cli/v3 context is context.Context

			// Further steps (sync wait, progress streaming, Ctrl+C) will use this taskID.
			// For now, we just print the ID.
			// Initialize WaitGroup for sync mode
			var wg sync.WaitGroup

			if cmd.Bool("async") {
				fmt.Println("Task submitted in async mode. CLI will now exit.")
				return nil
			} else {
				// Synchronous mode
				fmt.Printf("Task submitted in sync mode. Waiting for completion (Task ID: %s). Press Ctrl+C to cancel.\n", taskID)

				syncCtx, cancelSync := context.WithCancel(ctx)
				defer cancelSync()

				// Wait for flow to be created and get its ID
				flowID := waitForFlow(syncCtx, workspace.Id, taskID)

				if flowID == "" {
					fmt.Fprintf(os.Stderr, "Warning: No flows found for task. Progress streaming will be unavailable.\n")
				} else {
					fmt.Printf("[INFO] Starting progress streaming for Flow ID: %s\n", flowID)
					wg.Add(1)
					go streamTaskProgress(syncCtx, workspace.Id, flowID, taskID, &wg)
				}

				sigChan := make(chan os.Signal, 1)
				signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

				doneChan := make(chan string, 1)
				errPollChan := make(chan error, 1)

				go func() {
					defer close(doneChan)
					defer close(errPollChan)

					ticker := time.NewTicker(2 * time.Second)
					defer ticker.Stop()

					checkStatus := func() (status string, done bool, err error) {
						taskDetails, apiErr := getTaskDetails(workspace.Id, taskID)
						if apiErr != nil {
							fmt.Fprintf(os.Stderr, "Error polling task status: %v. Will retry.\n", apiErr)
							return "", false, nil // Not a fatal error for the poller, just a failed attempt
						}

						currentStatus := string(taskDetails.Task.Status)

						// Optional: print status updates, can be refined later with a spinner
						// fmt.Printf("Current task status: %s\n", currentStatus)

						switch currentStatus {
						case string(domain.TaskStatusComplete), string(domain.TaskStatusFailed), string(domain.TaskStatusCanceled):
							return currentStatus, true, nil
						case string(domain.TaskStatusToDo), string(domain.TaskStatusInProgress), string(domain.TaskStatusBlocked):
							return currentStatus, false, nil // Task is still ongoing
						default:
							return "", true, fmt.Errorf("unknown task status received: %s", currentStatus)
						}
					}

					// Initial check
					s, d, e := checkStatus()
					if e != nil {
						errPollChan <- e
						return
					}
					if d {
						doneChan <- s
						return
					}

					for {
						select {
						case <-syncCtx.Done():
							return // Exit if context is cancelled
						case <-ticker.C:
							s, d, e := checkStatus()
							if e != nil {
								errPollChan <- e
								return
							}
							if d {
								doneChan <- s
								return
							}
						}
					}
				}()

				select {
				case <-syncCtx.Done(): // Handles cancellation from defer cancelSync() or other external cancellations if cmd.Context() supports it
					fmt.Println("\nTask operation cancelled.")
					// Wait for progress view to clean up
					wg.Wait()
					// Attempt to cancel the task on the server if it was due to Ctrl+C, though cancelSync might be from task completion too.
					// If sigChan was the source, it's handled below. If doneChan or errPollChan, task is already finished/failed.
					return nil
				case sig := <-sigChan:
					fmt.Printf("\nSignal %v received. Attempting to cancel task %s...\n", sig, taskID)
					cancelSync() // Signal goroutines (polling, streaming) to stop
					cancelErr := cancelTask(workspace.Id, taskID)
					if cancelErr != nil {
						fmt.Fprintf(os.Stderr, "Failed to send cancel request for task %s: %v\n", taskID, cancelErr)
						return cli.Exit(fmt.Sprintf("Task cancellation request failed for %s.", taskID), 1)
					}
					fmt.Printf("Task %s cancellation requested successfully. Waiting for confirmation or timeout...\n", taskID)
					// Wait for progress view to clean up
					wg.Wait()
					// Wait for a short period to see if doneChan confirms cancellation, or timeout.
					select {
					case finalStatus := <-doneChan:
						fmt.Printf("Task %s confirmed status after cancellation request: %s\n", taskID, finalStatus)
					case <-time.After(5 * time.Second): // Give it a few seconds for server to process cancellation and poller to pick it up
						fmt.Println("Timed out waiting for cancellation confirmation. Exiting.")
					}
					return nil
				case finalStatus := <-doneChan:
					cancelSync() // Ensure other goroutines are stopped
					wg.Wait()    // Wait for progress view to clean up
					fmt.Printf("Task %s finished with status: %s\n", taskID, finalStatus)
					if finalStatus == string(domain.TaskStatusFailed) {
						return cli.Exit(fmt.Sprintf("Task %s failed.", taskID), 1)
					}
					return nil
				case err := <-errPollChan:
					cancelSync() // Ensure other goroutines are stopped
					wg.Wait()    // Wait for progress view to clean up
					return cli.Exit(fmt.Sprintf("Error during task monitoring: %v", err), 1)
				}
			}
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

	if apiClient == nil {
		return nil, fmt.Errorf("API client not initialized")
	}

	// Step 1: Find existing workspaces for the current directory
	workspacesResult, err := apiClient.GetWorkspacesByPath(absPath)
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
		fmt.Printf("No existing workspace found for %s. Creating a new one.\n", absPath)
		dirName := filepath.Base(absPath)
		defaultWorkspaceName := fmt.Sprintf("%s-workspace", dirName)

		req := &client.CreateWorkspaceRequest{
			Name:         defaultWorkspaceName,
			LocalRepoDir: absPath,
		}
		createdWorkspace, err := apiClient.CreateWorkspace(req)
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
