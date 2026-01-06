package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"syscall"
	"time"

	"sidekick/client"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/utils"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/erikgeiser/promptkit/selection"
)

func kanbanLink(workspaceId string) string {
	return fmt.Sprintf("http://localhost:%d/kanban?workspaceId=%s", common.GetServerPort(), workspaceId)
}

// checkServerStatus checks if the Sidekick server is responsive.
func checkServerStatus() bool {
	httpClient := &http.Client{Timeout: 1 * time.Second}
	resp, err := httpClient.Get(fmt.Sprintf("http://localhost:%d/", common.GetServerPort()))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	buf := make([]byte, 1024*1024)
	n, _ := resp.Body.Read(buf)
	return regexp.MustCompile("(?i)sidekick").Match(buf[:n])
}

// waitForServer waits for the server to become responsive.
func waitForServer(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if checkServerStatus() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func ensureSideServer(p *tea.Program) error {
	if !checkServerStatus() {
		p.Send(updateLifecycleMsg{key: "init", content: "Starting sidekick server...", spin: true})
		process, err := startServerDetached()
		defer process.Release()

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

func finishMessage(task client.Task, link string) string {
	var message string
	switch task.Status {
	case domain.TaskStatusComplete:
		message = "Task completed"
	case domain.TaskStatusCanceled:
		message = "Task canceled"
	case domain.TaskStatusFailed:
		message = fmt.Sprintf("Task failed. See details at %s", link)
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

	cmd := exec.Command(executable, "start", "--disable-auto-open")

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("Failed to start Sidekick server process (`%s start`): %w", executable, err)
	}

	return cmd.Process, nil
}

type teaSendable interface {
	Send(msg tea.Msg)
}

// ensureWorkspace handles finding, creating, or selecting a workspace.
func ensureWorkspace(ctx context.Context, dir string, p teaSendable, c client.Client, disableHumanInTheLoop bool) (*domain.Workspace, error) {
	p.Send(updateLifecycleMsg{key: "init", content: "Looking up workspace...", spin: true})

	repoPaths, err := utils.GetRepositoryPaths(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to get repository paths: %w", err)
	}

	allWorkspaces, err := c.GetAllWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve workspaces: %w", err)
	}

	var matchingWorkspaces []domain.Workspace
	for _, path := range repoPaths {
		for _, ws := range allWorkspaces {
			if filepath.Clean(ws.LocalRepoDir) == filepath.Clean(path) {
				matchingWorkspaces = append(matchingWorkspaces, ws)
			}
		}
		if len(matchingWorkspaces) > 0 {
			break
		}
	}

	workspaces := make([]*domain.Workspace, len(matchingWorkspaces))
	for i := range matchingWorkspaces {
		workspaces[i] = &matchingWorkspaces[i]
	}

	if len(workspaces) == 0 {
		p.Send(updateLifecycleMsg{key: "init", content: "Creating workspace...", spin: true})
		workspaceName := filepath.Base(dir)

		req := &client.CreateWorkspaceRequest{
			Name:         workspaceName,
			LocalRepoDir: dir,
		}
		createdWorkspace, err := c.CreateWorkspace(req)
		if err != nil {
			return nil, fmt.Errorf("failed to create workspace for path %s: %w", dir, err)
		}
		return createdWorkspace, nil
	}

	if len(workspaces) == 1 {
		return workspaces[0], nil
	}

	fmt.Printf("Multiple workspaces found for directory %s:\n", dir)
	sort.Slice(workspaces, func(i, j int) bool {
		if workspaces[i].Name != workspaces[j].Name {
			return workspaces[i].Name < workspaces[j].Name
		}
		return workspaces[i].Id < workspaces[j].Id
	})

	if disableHumanInTheLoop {
		sort.Slice(workspaces, func(i, j int) bool {
			return workspaces[i].Updated.After(workspaces[j].Updated)
		})
		fmt.Printf("Human-in-the-loop disabled. Using the most recently updated workspace: %s\n", workspaces[0].Name)
		return workspaces[0], nil
	}

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

// RunTaskUI runs the task UI with the given request, handling all lifecycle events.
func RunTaskUI(ctx context.Context, c client.Client, req *client.CreateTaskRequest, currentDir string, async bool) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	p := tea.NewProgram(newLifecycleModel(sigChan, c))

	var monitor *TaskMonitor
	var task client.Task

	// Extract disableHumanInTheLoop from flowOptions configOverrides
	disableHumanInTheLoop := false
	if req.FlowOptions != nil {
		if configOverrides, ok := req.FlowOptions["configOverrides"].(map[string]interface{}); ok {
			if disabled, ok := configOverrides["disableHumanInTheLoop"].(bool); ok {
				disableHumanInTheLoop = disabled
			}
		}
	}

	go func() {
		if err := ensureSideServer(p); err != nil {
			p.Send(updateLifecycleMsg{key: "error", content: err.Error()})
			p.Quit()
			return
		}

		workspace, err := ensureWorkspace(ctx, currentDir, p, c, disableHumanInTheLoop)
		if err != nil {
			p.Send(updateLifecycleMsg{key: "error", content: fmt.Sprintf("Workspace setup failed: %v", err)})
			p.Quit()
			return
		}

		p.Send(updateLifecycleMsg{key: "init", content: "Starting task...", spin: true})

		// Check off-hours blocking before creating task
		for {
			status := CheckOffHours()
			if !status.Blocked {
				break
			}
			p.Send(offHoursBlockedMsg{status: status})

			// Wait until unblock time or poll every 30 seconds
			var waitDuration time.Duration
			if status.UnblockAt != nil {
				waitDuration = time.Until(*status.UnblockAt) + time.Second
			} else {
				waitDuration = 30 * time.Second
			}
			time.Sleep(waitDuration)
		}
		// Clear blocked state
		p.Send(offHoursBlockedMsg{status: OffHoursStatus{Blocked: false}})

		task, err = c.CreateTask(workspace.Id, req)
		if err != nil {
			p.Send(updateLifecycleMsg{key: "error", content: fmt.Sprintf("Failed to create task: %v", err)})
			p.Quit()
			return
		}
		started := false
		p.Send(taskChangeMsg{task: task})

		if async {
			message := fmt.Sprintf("Task submitted. Follow progress at %s", kanbanLink(workspace.Id))
			p.Send(updateLifecycleMsg{key: "init", content: message})
			p.Quit()
			return
		}

		monitor = NewTaskMonitor(c, workspace.Id, task.Id)
		p.Send(setMonitorMsg{monitor: monitor})
		statusChan, progressChan, subflowChan, flowEventChan := monitor.Start(ctx)
		for {
			select {
			case action := <-progressChan:
				p.Send(flowActionChangeMsg{action: action})
			case subflow := <-subflowChan:
				p.Send(subflowFailedMsg{subflow: subflow})
			case flowEvent := <-flowEventChan:
				switch e := flowEvent.(type) {
				case domain.DevRunStartedEvent:
					p.Send(devRunStartedMsg{devRunId: e.DevRunId})
				case domain.DevRunEndedEvent:
					p.Send(devRunEndedMsg{devRunId: e.DevRunId})
				case domain.DevRunOutputEvent:
					p.Send(devRunOutputMsg{devRunId: e.DevRunId, stream: e.Stream, chunk: e.Chunk})
				}
			case taskStatus := <-statusChan:
				if !started && len(taskStatus.Task.Flows) > 0 {
					started = true
					p.Send(updateLifecycleMsg{key: "init", content: "Task started"})
				}
				p.Send(taskChangeMsg{task: taskStatus.Task})
				if taskStatus.Error != nil {
					p.Send(taskErrorMsg{err: taskStatus.Error})
				}
				if taskStatus.Finished {
					// Clear blocked state so the final message is visible
					p.Send(offHoursBlockedMsg{status: OffHoursStatus{Blocked: false}})
					p.Send(taskFinishedMsg{})
					finalMessage := finishMessage(taskStatus.Task, kanbanLink(workspace.Id))
					p.Send(updateLifecycleMsg{key: "finish", content: finalMessage})
					p.Quit()
					return
				}
			}
		}
	}()

	wg := sync.WaitGroup{}
	go func() {
		<-sigChan
		wg.Add(1)
		defer wg.Done()
		if task.Id != "" {
			if monitor != nil {
				monitor.Stop()
			}
			p.Send(updateLifecycleMsg{key: "finish", content: "Canceling task...", spin: true})
			if err := c.CancelTask(task.WorkspaceId, task.Id); err != nil {
				p.Send(updateLifecycleMsg{key: "error", content: fmt.Sprintf("Failed to cancel task: %v", err)})
			}
			p.Send(updateLifecycleMsg{key: "finish", content: "Task cancelled"})
		}
		p.Quit()
	}()

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("error running task UI: %v", err)
	}

	wg.Wait()

	return nil
}
