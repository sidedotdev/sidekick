package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"bytes"
	"encoding/json"
	"net"
	"net/http"
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
	"github.com/segmentio/ksuid"
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

// parseFlowOptions constructs the flow options map from various command-line flags.
func parseFlowOptions(cmd *cli.Command) (map[string]interface{}, error) {
	flowOpts := make(map[string]interface{})

	// Start with default or --flow-options.
	// cmd.String will return the default value if the flag is not set.
	optionsJSON := cmd.String("flow-options")
	if err := json.Unmarshal([]byte(optionsJSON), &flowOpts); err != nil {
		return nil, fmt.Errorf("invalid --flow-options JSON (value: %s): %w", optionsJSON, err)
	}

	// --no-requirements flag overrides the "requirements" key
	if cmd.Bool("no-requirements") {
		flowOpts["requirements"] = false
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
		valueStr := parts[1] // Raw value string

		// Strip surrounding quotes "" or `` if present
		if (strings.HasPrefix(valueStr, `"`) && strings.HasSuffix(valueStr, `"`)) ||
			(strings.HasPrefix(valueStr, "`") && strings.HasSuffix(valueStr, "`")) {
			if len(valueStr) >= 2 {
				valueStr = valueStr[1 : len(valueStr)-1]
			} else {
				valueStr = "" // Handles cases like input "" or ``
			}
		}
		// All values from --flow-option are stored as strings.
		// For specific types (bool, number), users should use --flow-options with full JSON.
		flowOpts[key] = valueStr
	}
	return flowOpts, nil
}

// createTaskAPI sends a request to the Sidekick server to create a new task.
func createTaskAPI(workspaceID string, payload []byte) (map[string]interface{}, error) {
	serverBaseURL := fmt.Sprintf("http://localhost:%d", common.GetServerPort())
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks", serverBaseURL, workspaceID)
	resp, err := http.Post(reqURL, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to send create task request to API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated { // Expect 201 Created
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("API request to create task failed with status %s and could not read response body: %w", resp.Status, readErr)
		}
		return nil, fmt.Errorf("API request to create task failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&responseData); err != nil {
		// Attempt to read the body for context if JSON decoding fails
		// Note: resp.Body might have been partially consumed by json.NewDecoder.
		// A more robust way would be to read it fully first, then try to decode.
		// For now, this provides some context.
		bodyBytes, _ := io.ReadAll(resp.Body) // Read remaining
		return nil, fmt.Errorf("failed to decode API response for create task (status %s): %w. Response body fragment: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData, nil
}

// getTaskAPI fetches the details of a specific task from the Sidekick server.
func getTaskAPI(workspaceID string, taskID string) (map[string]interface{}, error) {
	serverBaseURL := fmt.Sprintf("http://localhost:%d", common.GetServerPort())
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks/%s", serverBaseURL, workspaceID, taskID)

	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("failed to send get task request to API: %w", err)
	}
	defer resp.Body.Close()

	// Read the entire body first to ensure it's available for error reporting
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body from get task request (status %s): %w", resp.Status, readErr)
	}

	if resp.StatusCode != http.StatusOK { // Expect 200 OK
		return nil, fmt.Errorf("API request to get task failed with status %s: %s", resp.Status, string(bodyBytes))
	}

	var responseData map[string]interface{}
	// Now decode from the buffered bodyBytes
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode API response for get task (status %s): %w. Full response body: %s", resp.Status, err, string(bodyBytes))
	}
	return responseData, nil
}

// streamTaskProgress connects to the WebSocket endpoint to stream flow action changes for a task.
// It uses flowID to identify the specific flow to stream actions from.
func streamTaskProgress(ctx context.Context, workspaceID, flowID, taskID string) {
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
			select {
			case <-ctx.Done():
				return
			default:
				conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				var action domain.FlowAction
				if err := conn.ReadJSON(&action); err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue // Expected timeout, loop to check ctx.Done()
					}

					// For any other error, we want to exit the read loop.
					// We'll send a message to bubbletea to terminate it.
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
						p.Send(taskErrorMsg{err: fmt.Errorf("unexpected websocket close: %w", err)})
					} else {
						// Assume normal closure.
						p.Send(taskCompleteMsg{})
					}
					return
				}

				p.Send(taskProgressMsg{
					taskID:       taskID,
					actionType:   action.ActionType,
					actionStatus: action.ActionStatus,
				})
			}
		}
	}()

	// Goroutine to handle context cancellation and quit Bubble Tea program
	go func() {
		<-ctx.Done()
		p.Send(contextCancelledMsg{})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running progress view: %v\n", err)
	}

	// After bubbletea program exits, attempt to gracefully close the WebSocket connection.
	err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	if err != nil && !errors.Is(err, websocket.ErrCloseSent) && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		// Don't spam about errors on a connection that is already closing.
	}

	// Wait for the read loop to finish.
	<-done
}

// cancelTaskAPI sends a request to the Sidekick server to cancel a task.
func cancelTaskAPI(workspaceID string, taskID string) error {
	serverBaseURL := fmt.Sprintf("http://localhost:%d", common.GetServerPort())
	reqURL := fmt.Sprintf("%s/api/v1/workspaces/%s/tasks/%s/cancel", serverBaseURL, workspaceID, taskID)

	req, err := http.NewRequest("POST", reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create cancel task request: %w", err)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send cancel task request to API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK { // Expect 200 OK for cancellation
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("API request to cancel task failed with status %s and could not read response body: %w", resp.Status, readErr)
		}
		var errorResponse struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &errorResponse) == nil && errorResponse.Error != "" {
			return fmt.Errorf("API request to cancel task failed with status %s: %s", resp.Status, errorResponse.Error)
		}
		return fmt.Errorf("API request to cancel task failed with status %s: %s", resp.Status, string(bodyBytes))
	}
	return nil
}

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
			fmt.Printf("Using workspace: %s (ID: %s, Path: %s)\n", workspace.Name, workspace.Id, workspace.LocalRepoDir) // Corrected \\\\n to \n

			// 1. Construct TaskRequest payload
			// taskDescription is already validated and available.

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
			fmt.Printf("Attempting to create task '%s' in workspace '%s' (ID: %s)...\\n", taskDescription, workspace.Name, workspace.Id)
			// fmt.Printf("Payload: %s\\n", string(payloadBytes)) // For debugging, can be removed later

			taskResponseData, err := createTaskAPI(workspace.Id, payloadBytes)
			if err != nil {
				return cli.Exit(fmt.Sprintf("Failed to create task via API: %v", err), 1)
			}

			// 3. Handle the API response
			taskID, ok := taskResponseData["id"].(string)
			if !ok {
				responseBytes, _ := json.Marshal(taskResponseData) // Attempt to log the full response for debugging
				errorMsg := fmt.Sprintf("Task creation API call succeeded, but task ID was not found or not a string in the response. Full response: %s", string(responseBytes))
				fmt.Println(errorMsg) // Print to stdout for user visibility
				return cli.Exit(errorMsg, 1)
			}

			fmt.Printf("Successfully created task with ID: %s\n", taskID)
			// Store taskID if needed for sync mode (next steps)
			// e.g., cmd.Context().Set("createdTaskID", taskID) // urfave/cli/v3 context is context.Context

			// Further steps (sync wait, progress streaming, Ctrl+C) will use this taskID.
			// For now, we just print the ID.
			if cmd.Bool("async") {
				fmt.Println("Task submitted in async mode. CLI will now exit.")
				return nil
			} else {
				// Synchronous mode
				fmt.Printf("Task submitted in sync mode. Waiting for completion (Task ID: %s). Press Ctrl+C to cancel.\\n", taskID)

				syncCtx, cancelSync := context.WithCancel(ctx)
				defer cancelSync()

				// Attempt to get flowID for progress streaming
				var flowID string
				// Fetch full task details once to get the flow ID.
				taskDetailsForFlow, err := getTaskAPI(workspace.Id, taskID)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not fetch task details to get flow ID for progress streaming: %v\n", err)
				} else {
					if flowsVal, ok := taskDetailsForFlow["flows"]; ok {
						if flowsArray, ok := flowsVal.([]interface{}); ok && len(flowsArray) > 0 {
							if firstFlow, ok := flowsArray[0].(map[string]interface{}); ok {
								if idVal, ok := firstFlow["id"].(string); ok && idVal != "" {
									flowID = idVal
								} else {
									fmt.Fprintf(os.Stderr, "Warning: ID not found or empty for the first flow. Progress streaming will be unavailable.\n")
								}
							} else {
								fmt.Fprintf(os.Stderr, "Warning: First flow data is not in the expected format (map[string]interface{}). Progress streaming will be unavailable.\n")
							}
						} else {
							fmt.Fprintf(os.Stderr, "Warning: 'flows' field is not a non-empty array in task details. Progress streaming will be unavailable.\n")
						}
					} else {
						fmt.Fprintf(os.Stderr, "Warning: 'flows' field not found in task details. Progress streaming will be unavailable.\n")
					}
				}

				if flowID != "" {
					fmt.Printf("[INFO] Starting progress streaming for Flow ID: %s\n", flowID)
					go streamTaskProgress(syncCtx, workspace.Id, flowID, taskID)
				} else {
					fmt.Println("Progress streaming is unavailable as Flow ID could not be determined.")
				}

				sigChan := make(chan os.Signal, 1)
				signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

				doneChan := make(chan string, 1)   // Final status string
				errPollChan := make(chan error, 1) // Errors from polling

				go func() { // Polling goroutine
					defer close(doneChan)
					defer close(errPollChan)

					ticker := time.NewTicker(2 * time.Second) // Polling interval
					defer ticker.Stop()

					checkStatus := func() (status string, done bool, err error) {
						taskData, apiErr := getTaskAPI(workspace.Id, taskID)
						if apiErr != nil {
							fmt.Fprintf(os.Stderr, "Error polling task status: %v. Will retry.\n", apiErr)
							return "", false, nil // Not a fatal error for the poller, just a failed attempt
						}

						currentStatus, ok := taskData["status"].(string)
						if !ok {
							return "", true, fmt.Errorf("task status not found or not a string in API response: %v", taskData)
						}

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
					// Attempt to cancel the task on the server if it was due to Ctrl+C, though cancelSync might be from task completion too.
					// If sigChan was the source, it's handled below. If doneChan or errPollChan, task is already finished/failed.
					return nil
				case sig := <-sigChan:
					fmt.Printf("\nSignal %v received. Attempting to cancel task %s...\n", sig, taskID)
					cancelSync() // Signal goroutines (polling, streaming) to stop
					cancelErr := cancelTaskAPI(workspace.Id, taskID)
					if cancelErr != nil {
						fmt.Fprintf(os.Stderr, "Failed to send cancel request for task %s: %v\n", taskID, cancelErr)
						return cli.Exit(fmt.Sprintf("Task cancellation request failed for %s.", taskID), 1)
					}
					fmt.Printf("Task %s cancellation requested successfully. Waiting for confirmation or timeout...\n", taskID)
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
					fmt.Printf("Task %s finished with status: %s\n", taskID, finalStatus)
					if finalStatus == string(domain.TaskStatusFailed) {
						return cli.Exit(fmt.Sprintf("Task %s failed.", taskID), 1)
					}
					return nil
				case err := <-errPollChan:
					cancelSync() // Ensure other goroutines are stopped
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
