package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"encoding/json"

	"os/signal"
	"strings"
	"syscall"

	"sidekick/client"
	"sidekick/common"
	"sidekick/domain"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/erikgeiser/promptkit/selection"
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
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			c := client.NewClient(fmt.Sprintf("http://localhost:%d", common.GetServerPort()))
			return executeTaskCommand(ctx, c, cmd)
		},
	}
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
	return flowOpts, nil
}

func executeTaskCommand(ctx context.Context, c client.Client, cmd *cli.Command) error {
	req, err := buildCreateTaskRequest(cmd)
	if err != nil {
		return err
	}

	// TODO merge into DevConfig, which goes into FlowOptions.DevConfigOverrides
	// in the task request
	disableHumanInTheLoop := cmd.Bool("disable-human-in-the-loop")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	p := tea.NewProgram(newLifecycleModel(sigChan))

	var monitor *TaskMonitor
	var task client.Task

	go func() {
		if err := ensureSideServer(p); err != nil {
			p.Send(updateLifecycleMsg{key: "error", message: lifecycleMessage{content: err.Error(), showSpinner: false}})
			p.Quit()
			return
		}

		workspace, err := ensureWorkspace(ctx, p, c, disableHumanInTheLoop)
		if err != nil {
			p.Send(updateLifecycleMsg{key: "error", message: lifecycleMessage{
				content:     fmt.Sprintf("Workspace setup failed: %v", err),
				showSpinner: false,
			}})
			p.Quit()
			return
		}

		p.Send(updateLifecycleMsg{key: "task", message: lifecycleMessage{
			content:     "Starting task...",
			showSpinner: true,
		}})

		task, err = c.CreateTask(workspace.Id, req)
		if err != nil {
			p.Send(updateLifecycleMsg{key: "error", message: lifecycleMessage{
				content:     fmt.Sprintf("Failed to create task: %v", err),
				showSpinner: false,
			}})
			p.Quit()
			return
		}
		p.Send(taskChangeMsg{task: task})

		if cmd.Bool("async") {
			message := fmt.Sprintf("Task submitted. Follow progress at %s", kanbanLink(workspace.Id))
			p.Send(updateLifecycleMsg{key: "completion", message: lifecycleMessage{
				content:     message,
				showSpinner: false,
			}})
			p.Quit()
			return
		}

		monitor = NewTaskMonitor(c, workspace.Id, task.Id)
		statusChan, progressChan := monitor.Start(ctx)
		for {
			select {
			case taskProgress := <-progressChan:
				p.Send(flowActionChangeMsg{actionType: taskProgress.ActionType, actionStatus: taskProgress.ActionStatus})
			case taskStatus := <-statusChan:
				p.Send(taskChangeMsg{task: taskStatus.Task})
				if taskStatus.Error != nil {
					p.Send(taskErrorMsg{err: taskStatus.Error})
				}
				if taskStatus.Finished {
					p.Send(finalUpdateMsg{message: finishMessage(taskStatus.Task, kanbanLink(workspace.Id))})
					p.Quit()
					return
				}
			}
		}
	}()

	wg := sync.WaitGroup{}
	go func() {
		// TODO make sure other go-routine returns early when it hasn't started
		// the task yet. Use context cancellation to do this, with contexts
		// passed in to ensureSideServer and CreateTask. ensureWorkspace takes
		// context, but doesn't actually use it. We need to adjust some of the
		// functions being called to take in context and be made cancellable
		//  monitor.Start does use the context correctly already, so passing in
		//  the same context we cancel is sufficient
		<-sigChan
		wg.Add(1)
		defer wg.Done()
		if task.Id != "" {
			if monitor != nil {
				monitor.Stop()
			}
			p.Send(updateLifecycleMsg{key: "task", message: lifecycleMessage{
				content:     "Canceling task...",
				showSpinner: true,
			}})
			if err := c.CancelTask(task.WorkspaceId, task.Id); err != nil {
				p.Send(updateLifecycleMsg{key: "error", message: lifecycleMessage{
					content:     fmt.Sprintf("Failed to cancel task: %v", err),
					showSpinner: false,
				}})
			}
			p.Send(updateLifecycleMsg{key: "completion", message: lifecycleMessage{
				content:     "Task cancelled",
				showSpinner: false,
			}})
		}
		p.Quit()
	}()

	if _, err := p.Run(); err != nil {
		return cli.Exit(fmt.Sprintf("Error running task UI: %v", err), 1)
	}

	// wait in case we are still canceling the task
	wg.Wait()

	return nil
}

func buildCreateTaskRequest(cmd *cli.Command) (*client.CreateTaskRequest, error) {
	taskDescription := cmd.Args().First()

	if taskDescription == "" {
		return nil, cli.Exit("ERROR:\n   A task description is required.\n\nUSAGE:\n  side task <task description>\n\nRun `side task help` to see all options.", 1)
	}

	flowType := cmd.String("flow")
	if cmd.Bool("P") {
		flowType = "planned_dev"
	}

	flowOpts, err := parseFlowOptions(cmd)
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

func kanbanLink(workspaceId string) string {
	return fmt.Sprintf("http://localhost:%d/kanban?workspaceId=%s", common.GetServerPort(), workspaceId)
}

func ensureSideServer(p *tea.Program) error {
	if !checkServerStatus() {
		p.Send(updateLifecycleMsg{key: "startup", message: lifecycleMessage{
			content:     "Starting sidekick server...",
			showSpinner: true,
		}})
		process, err := startServerDetached()
		defer process.Release() // don't wait, server runs in background

		if err != nil {
			return fmt.Errorf("Failed to start Sidekick server: %v\nTry running `side start` manually.", err)
		}

		if !waitForServer(10 * time.Second) {
			process.Kill()
			return errors.New("Timed out waiting for Sidekick server to be ready. Please check logs or run 'side start' manually.")
		}
	}
	return nil
}

func finishMessage(task client.Task, kanbanLink string) string {
	var message string
	switch task.Status {
	case domain.TaskStatusComplete:
		message = "Task completed"
	case domain.TaskStatusCanceled:
		message = "Task canceled"
	case domain.TaskStatusFailed:
		message = fmt.Sprintf("Task failed. See details at %s", kanbanLink)
	default:
		message = fmt.Sprintf("Task finished with status %s", task.Status)
	}
	return message
}

// startServerDetached attempts to start the Sidekick server in a detached background process
// by invoking the 'side start' command.
func startServerDetached() (*os.Process, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("Failed to determine executable path: %w", err)
	}

	cmd := exec.Command(executable, "start")

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("Failed to start Sidekick server process (`%s start`): %w", executable, err)
	}

	// TODO update/manage a new pidfile to track the process ID as well as
	// synchronize concurrent starts system-wide through file locking

	return cmd.Process, nil
	//fmt.Printf("Sidekick server process initiated with PID: %d. It will run in the background.\n", cmd.Process.Pid)
}

type teaSendable interface {
	Send(msg tea.Msg)
}

// ensureWorkspace handles finding, creating, or selecting a workspace.
func ensureWorkspace(ctx context.Context, p teaSendable, c client.Client, disableHumanInTheLoop bool) (*domain.Workspace, error) {
	p.Send(updateLifecycleMsg{key: "workspace", message: lifecycleMessage{
		content:     "Looking up workspace...",
		showSpinner: true,
	}})
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
		p.Send(updateLifecycleMsg{key: "workspace", message: lifecycleMessage{
			content:     "Creating workspace...",
			showSpinner: true,
		}})
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

	// TODO support --workspace-id,-W flag for selecting a specific workspace
	// instead, and fail here when human-in-the-loop is disabled without
	// specifying a workspace
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
