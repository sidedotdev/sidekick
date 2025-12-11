package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sidekick"
	"slices"
	"strconv"
	"strings"
	"time"

	"sidekick/common"
	"sidekick/dev"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/frontend"
	"sidekick/llm"
	"sidekick/secret_manager"
	"sidekick/srv"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

func RunServer() *http.Server {
	gin.SetMode(gin.ReleaseMode)
	ctrl, err := NewController()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize controller")
	}
	router := DefineRoutes(ctrl)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", common.GetServerPort()),
		Handler: router.Handler(),
	}

	// Start server in a goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("Failed to start API server")
		}
	}()

	return srv
}

type Controller struct {
	service           srv.Service
	temporalClient    client.Client
	temporalNamespace string
	temporalTaskQueue string
	secretManager     secret_manager.SecretManager
}

// UserActionRequest defines the expected request body for user actions.
type UserActionRequest struct {
	ActionType string `json:"actionType"`
}

// ArchiveTaskHandler handles the request to archive a task
func (ctrl *Controller) ArchiveTaskHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	taskId := c.Param("id")

	task, err := ctrl.service.GetTask(c.Request.Context(), workspaceId, taskId)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusNotFound, errors.New("task not found"))
		return
	}

	// Check if the task status is valid for archiving
	if task.Status != domain.TaskStatusCanceled && task.Status != domain.TaskStatusFailed && task.Status != domain.TaskStatusComplete {
		ctrl.ErrorHandler(c, http.StatusBadRequest, errors.New("only tasks with status 'canceled', 'failed', or 'complete' can be archived"))
		return
	}

	// Set the Archived field to the current timestamp
	now := time.Now()
	task.Archived = &now

	// Persist the updated task
	err = ctrl.service.PersistTask(c.Request.Context(), task)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, errors.New("failed to archive task"))
		return
	}

	c.Status(http.StatusNoContent)
}

func (ctrl *Controller) GetModelsHandler(c *gin.Context) {
	data, err := common.LoadModelsDev()
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, data)
}

// ArchiveFinishedTasksHandler handles the request to archive all finished tasks
func (ctrl *Controller) ArchiveFinishedTasksHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")

	// Get all tasks with status 'complete', 'canceled', or 'failed'
	tasks, err := ctrl.service.GetTasks(c.Request.Context(), workspaceId, []domain.TaskStatus{
		domain.TaskStatusComplete,
		domain.TaskStatusCanceled,
		domain.TaskStatusFailed,
	})
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, errors.New("failed to fetch tasks"))
		return
	}

	archivedCount := 0
	now := time.Now()

	for _, task := range tasks {
		task.Archived = &now
		err := ctrl.service.PersistTask(c.Request.Context(), task)
		if err != nil {
			// Log the error but continue with other tasks
			log.Error().Err(err).Str("taskId", task.Id).Msg("Failed to archive task")
		} else {
			archivedCount++
		}
	}

	c.JSON(http.StatusOK, gin.H{"archivedCount": archivedCount})
}

func DefineRoutes(ctrl Controller) *gin.Engine {
	r := gin.Default()
	r.ForwardedByClientIP = true
	r.SetTrustedProxies(nil)

	r.GET("/api/v1/providers", ctrl.GetProvidersHandler)
	r.GET("/api/v1/models", ctrl.GetModelsHandler)

	workspaceApiRoutes := DefineWorkspaceApiRoutes(r, &ctrl)
	workspaceApiRoutes.GET("/archived_tasks", ctrl.GetArchivedTasksHandler)

	taskRoutes := workspaceApiRoutes.Group("/tasks")
	taskRoutes.POST("/", ctrl.CreateTaskHandler)
	taskRoutes.GET("/", ctrl.GetTasksHandler)
	taskRoutes.GET("/:id", ctrl.GetTaskHandler)
	taskRoutes.PUT("/:id", ctrl.UpdateTaskHandler)
	taskRoutes.DELETE("/:id", ctrl.DeleteTaskHandler)
	taskRoutes.POST("/:id/archive", ctrl.ArchiveTaskHandler)
	taskRoutes.POST("/:id/cancel", ctrl.CancelTaskHandler)
	taskRoutes.POST("/archive_finished", ctrl.ArchiveFinishedTasksHandler)

	flowRoutes := workspaceApiRoutes.Group("/flows")
	flowRoutes.GET("/:id", ctrl.GetFlowHandler)
	flowRoutes.GET("/:id/actions", ctrl.GetFlowActionsHandler)
	flowRoutes.POST("/:id/pause", ctrl.PauseFlowHandler)
	flowRoutes.POST("/:id/cancel", ctrl.CancelFlowHandler)
	flowRoutes.POST("/:id/user_action", ctrl.UserActionHandler)

	workspaceApiRoutes.POST("/flow_actions/:id/complete", ctrl.CompleteFlowActionHandler)
	workspaceApiRoutes.PUT("/flow_actions/:id", ctrl.UpdateFlowActionHandler)

	workspaceWsRoutes := r.Group("/ws/v1/workspaces")
	workspaceWsRoutes.GET("/:workspaceId/task_changes", ctrl.TaskChangesWebsocketHandler)
	workspaceWsRoutes.GET("/:workspaceId/flows/:id/action_changes_ws", ctrl.FlowActionChangesWebsocketHandler)
	workspaceWsRoutes.GET("/:workspaceId/flows/:id/events", ctrl.FlowEventsWebsocketHandler)

	assets := http.FS(frontend.AssetsSubdirFs)
	r.StaticFS("/assets", assets)

	// loop through the static files directly under the dist directory and serve
	// them at the top-level (not possible with StaticFS due to route pattern conflict)
	files, err := frontend.DistFs.ReadDir("dist")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to read embedded files")
	}
	dist := http.FS(frontend.DistFs)
	for _, file := range files {
		r.StaticFileFS("/"+file.Name(), "dist/"+file.Name(), dist)
	}

	// Wildcard route to serve index.html for other HTML-based frontend routes,
	// eg /kanban etc as they get defined by the frontend. This also serves the
	// root route rather than a custom route for the root.
	r.NoRoute(ctrl.WildcardHandler)

	return r
}

func NewController() (Controller, error) {
	clientOptions := client.Options{
		HostPort: common.GetTemporalServerHostPort(),
	}
	temporalClient, err := client.NewLazyClient(clientOptions)
	if err != nil {
		return Controller{}, fmt.Errorf("failed to create Temporal client: %w", err)
	}

	service, err := sidekick.GetService()
	if err != nil {
		return Controller{}, fmt.Errorf("failed to initialize storage: %w", err)
	}
	err = service.CheckConnection(context.Background())
	if err != nil {
		return Controller{}, fmt.Errorf("failed to connect to storage: %w", err)
	}

	secretManager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		secret_manager.EnvSecretManager{},
		secret_manager.KeyringSecretManager{},
		secret_manager.LocalConfigSecretManager{},
	})

	return Controller{
		service:           service,
		temporalClient:    temporalClient,
		temporalNamespace: common.GetTemporalNamespace(),
		temporalTaskQueue: common.GetTemporalTaskQueue(),
		secretManager:     secretManager,
	}, nil
}

func (ctrl *Controller) GetProvidersHandler(c *gin.Context) {
	providers := []string{}
	seen := make(map[string]bool)

	config, err := common.LoadSidekickConfig(common.GetSidekickConfigPath())
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load sidekick config")
	} else {
		for _, p := range config.Providers {
			if p.Name != "" && !seen[p.Name] {
				providers = append(providers, p.Name)
				seen[p.Name] = true
			}
		}
	}

	for _, builtinProvider := range common.BuiltinProviders {
		if seen[builtinProvider] {
			continue
		}

		var secretNames []string
		switch builtinProvider {
		case "openai":
			secretNames = []string{llm.OpenaiApiKeySecretName}
		case "anthropic":
			secretNames = []string{llm.AnthropicApiKeySecretName, "ANTHROPIC_OAUTH"}
		case "google":
			secretNames = []string{llm.GoogleApiKeySecretName}
		}

		for _, secretName := range secretNames {
			if _, err := ctrl.secretManager.GetSecret(secretName); err == nil {
				if !slices.Contains(providers, builtinProvider) {
					providers = append(providers, builtinProvider)
				}
				break
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

func (ctrl *Controller) ErrorHandler(c *gin.Context, status int, err error) {
	log.Error().Err(err).Str("path", c.FullPath()).Int("status", status).Msg("Error handling request")
	c.JSON(status, gin.H{"error": err.Error()})
}

// CancelTaskHandler handles the cancellation of a task
func (ctrl *Controller) CancelTaskHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	taskId := c.Param("id")

	task, err := ctrl.service.GetTask(c.Request.Context(), workspaceId, taskId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Check if the task status is eligible for cancellation
	if task.Status != domain.TaskStatusToDo && task.Status != domain.TaskStatusInProgress && task.Status != domain.TaskStatusBlocked && task.Status != domain.TaskStatusInReview {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only tasks with status 'to_do', 'in_progress', 'blocked', or 'in_review' can be canceled"})
		return
	}

	// Get the child workflows of the task
	childFlows, err := ctrl.service.GetFlowsForTask(c.Request.Context(), workspaceId, taskId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get child workflows"})
		return
	}

	// Check if any of the child workflows are in progress and cancel them
	for _, flow := range childFlows {
		// Update and persist the flow status
		flow.Status = "canceled"
		if err := ctrl.service.PersistFlow(c.Request.Context(), flow); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"message": fmt.Sprintf("Failed to update flow status: %v", err),
			})
			return
		}

		err = ctrl.temporalClient.CancelWorkflow(c.Request.Context(), flow.Id, "")
		if err != nil {
			// Check if the error is due to workflow not found or already completed
			var notFoundErr *serviceerror.NotFound
			if !errors.As(err, &notFoundErr) && !strings.Contains(err.Error(), "workflow execution already completed") {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cancel workflow"})
				return
			}
		}
	}

	// Update the task status to 'canceled' and agent type to 'none'
	task.Status = domain.TaskStatusCanceled
	task.AgentType = domain.AgentTypeNone
	task.Updated = time.Now()
	err = ctrl.service.PersistTask(c.Request.Context(), task)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task canceled successfully"})
}

func (ctrl *Controller) DeleteTaskHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	taskId := c.Param("id")

	task, err := ctrl.service.GetTask(c.Request.Context(), workspaceId, taskId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Get the child workflows of the task
	childFlows, err := ctrl.service.GetFlowsForTask(c.Request.Context(), workspaceId, taskId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get child workflows"})
		return
	}

	// Check if any of the child workflows are in progress and cancel them
	devAgent := dev.DevAgent{
		TemporalClient:    ctrl.temporalClient,
		TemporalTaskQueue: ctrl.temporalTaskQueue,
		WorkspaceId:       task.WorkspaceId,
	}
	for _, flow := range childFlows {
		err = devAgent.TerminateWorkflowIfExists(c.Request.Context(), flow.Id)
		if err != nil {
			log.Error().Err(err).Str("flowId", flow.Id).Msg("Error terminating workflow")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to terminate workflow"})
			return
		}

		// TODO delete the flow from the database
	}

	err = ctrl.service.DeleteTask(c.Request.Context(), workspaceId, taskId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete task"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Task deleted successfully"})
}

func (ctrl *Controller) PauseFlowHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	// Get the flow first
	flow, err := ctrl.service.GetFlow(c.Request.Context(), workspaceId, flowId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": fmt.Sprintf("Failed to get flow: %v", err),
		})
		return
	}

	// Update flow status to paused
	flow.Status = "paused"
	err = ctrl.service.PersistFlow(c.Request.Context(), flow)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": fmt.Sprintf("Failed to persist flow status: %v", err),
		})
		return
	}

	// Send pause signal to temporal workflow
	err = ctrl.temporalClient.SignalWorkflow(c.Request.Context(), flowId, "", dev.SignalNamePause, &dev.Pause{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": fmt.Sprintf("Failed to send pause signal: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Flow paused successfully",
	})
}

func (ctrl *Controller) CancelFlowHandler(c *gin.Context) {
	workspaceID := c.Param("workspaceId")
	flowID := c.Param("id")

	// Get the flow first to ensure it exists
	flow, err := ctrl.service.GetFlow(c.Request.Context(), workspaceID, flowID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": fmt.Sprintf("Failed to get flow: %v", err),
		})
		return
	}

	// Update and persist the flow status
	flow.Status = "canceled"
	if err := ctrl.service.PersistFlow(c.Request.Context(), flow); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": fmt.Sprintf("Failed to update flow status: %v", err),
		})
		return
	}

	// Cancel the temporal workflow
	if err := ctrl.temporalClient.CancelWorkflow(c.Request.Context(), flowID, ""); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": fmt.Sprintf("Failed to cancel workflow: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Workflow cancelled successfully",
	})
}

// UserActionHandler handles requests to perform user-initiated actions on a flow.
func (ctrl *Controller) UserActionHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	_, err := ctrl.service.GetFlow(c, workspaceId, flowId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	var req UserActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Invalid request payload: " + err.Error()})
		return
	}
	if req.ActionType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "Invalid request payload: missing or blank actionType"})
		return
	}

	if req.ActionType != string(flow_action.UserActionGoNext) {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("Invalid actionType '%s'. Only '%s' is supported.", req.ActionType, flow_action.UserActionGoNext)})
		return
	}

	// Note: the only way to interact with the flow's GlobalState is by
	// signalling it. The signal handler will then process the action within the
	// context of the temporal workflow.
	err = ctrl.temporalClient.SignalWorkflow(c.Request.Context(), flowId, "", dev.SignalNameUserAction, flow_action.UserActionGoNext)
	if err != nil {
		var serviceErrNotFound *serviceerror.NotFound
		if errors.As(err, &serviceErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"message": fmt.Sprintf("Flow with ID %s not found", flowId)})
			return
		}
		log.Error().Err(err).Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Failed to signal workflow for user action")
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Failed to signal workflow: " + err.Error()})
		return
	}

	log.Info().Str("workspaceId", workspaceId).Str("flowId", flowId).Str("action", req.ActionType).Msg("User action signaled to workflow")
	c.JSON(http.StatusOK, gin.H{"message": "User action '" + req.ActionType + "' signaled successfully"})
}

type TaskRequest struct {
	Id          string                 `json:"id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	FlowType    string                 `json:"flowType"`
	AgentType   string                 `json:"agentType"`
	Status      string                 `json:"status"`
	FlowOptions map[string]interface{} `json:"flowOptions"`
}

func (ctrl *Controller) CreateTaskHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	var taskReq TaskRequest
	if err := c.ShouldBindJSON(&taskReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// default values for create only
	if taskReq.Status == "" {
		taskReq.Status = string(domain.TaskStatusToDo)
	}

	// create-specific validation (TODO let's separate out the types for the create and update task request bodies)
	if taskReq.Status != string(domain.TaskStatusDrafting) && taskReq.Status != string(domain.TaskStatusToDo) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Creating a task with status set to anything other than 'drafting' or 'to_do' is not allowed"})
		return
	}

	if taskReq.AgentType == "" {
		if taskReq.Status == string(domain.TaskStatusDrafting) || taskReq.Status == "" {
			taskReq.AgentType = string(domain.AgentTypeHuman)
		} else {
			taskReq.AgentType = string(domain.AgentTypeLLM)
		}
	}

	agentType, status, err := validateTaskRequest(&taskReq)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	flowType, err := domain.StringToFlowType(taskReq.FlowType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	task := domain.Task{
		WorkspaceId: workspaceId,
		Id:          "task_" + ksuid.New().String(),
		Created:     time.Now(),
		Updated:     time.Now(),
		// TODO add title afterwards automagically via LLM
		// Title:       "",
		Description: taskReq.Description,
		Status:      status, // Set the task status to the requested status
		AgentType:   agentType,
		FlowType:    flowType,
		FlowOptions: taskReq.FlowOptions,
	}

	if err := ctrl.service.PersistTask(c, task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create task"})
		return
	}

	if agentType == domain.AgentTypeLLM {
		if err := ctrl.AgentHandleNewTask(c, &task); err != nil {
			ctrl.ErrorHandler(c, http.StatusInternalServerError, fmt.Errorf("Failed to handle new task: %w", err))
			task.Status = domain.TaskStatusFailed
			task.AgentType = domain.AgentTypeNone
			ctrl.service.PersistTask(c, task)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"task": task})
}

// API response object for a task
type TaskResponse struct {
	domain.Task
	Flows []domain.Flow `json:"flows"`
}

func (ctrl *Controller) GetTaskHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	taskId := c.Param("id")

	if workspaceId == "" || taskId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID and Task ID are required"})
		return
	}

	task, err := ctrl.service.GetTask(c, workspaceId, taskId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	flows, err := ctrl.service.GetFlowsForTask(c, workspaceId, taskId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	response := TaskResponse{
		Task:  task,
		Flows: flows,
	}

	c.JSON(http.StatusOK, gin.H{"task": response})
}

// FlowWithWorktrees represents a Flow with its associated Worktrees
type FlowWithWorktrees struct {
	domain.Flow
	Worktrees []domain.Worktree `json:"worktrees"`
}

func (ctrl *Controller) GetFlowHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	if workspaceId == "" || flowId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID and Flow ID are required"})
		return
	}

	flow, err := ctrl.service.GetFlow(c, workspaceId, flowId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	worktrees, err := ctrl.service.GetWorktreesForFlow(c, workspaceId, flowId)
	if err != nil {
		fmt.Printf("Error fetching worktrees for flow %s: %v\n", flowId, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve worktrees"})
		return
	}

	flowWithWorktrees := FlowWithWorktrees{
		Flow:      flow,
		Worktrees: worktrees,
	}

	c.JSON(http.StatusOK, gin.H{"flow": flowWithWorktrees})
}

func (ctrl *Controller) GetTasksHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	if workspaceId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID is required"})
		return
	}

	statusesStr := c.Query("statuses")
	if statusesStr == "" || statusesStr == "all" {
		statusesStr = "to_do,drafting,blocked,in_review,in_progress,complete,failed,canceled"
	}
	statuses := strings.Split(statusesStr, ",")
	taskStatuses := []domain.TaskStatus{}
	for _, status := range statuses {
		taskStatus := domain.TaskStatus(status)
		taskStatuses = append(taskStatuses, taskStatus)
	}

	var tasks []domain.Task
	var err error

	if len(taskStatuses) > 0 {
		tasks, err = ctrl.service.GetTasks(c, workspaceId, taskStatuses)
		if err != nil {
			log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Error fetching tasks")
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	if tasks == nil {
		tasks = []domain.Task{}
	}

	taskResponses := make([]TaskResponse, len(tasks))
	for i, task := range tasks {
		flows, err := ctrl.service.GetFlowsForTask(c, workspaceId, task.Id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		taskResponses[i] = TaskResponse{
			Task:  task,
			Flows: flows,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"tasks": taskResponses,
	})
}

func (ctrl *Controller) GetArchivedTasksHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	if workspaceId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID is required"})
		return
	}

	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page number"})
		return
	}

	pageSize, err := strconv.Atoi(c.DefaultQuery("pageSize", "100"))
	if err != nil || pageSize < 1 || pageSize > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page size"})
		return
	}

	archivedTasks, totalCount, err := ctrl.service.GetArchivedTasks(c, workspaceId, int64(page), int64(pageSize))
	if err != nil {
		fmt.Println("Error fetching archived tasks:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	taskResponses := make([]TaskResponse, len(archivedTasks))
	for i, task := range archivedTasks {
		flows, err := ctrl.service.GetFlowsForTask(c, workspaceId, task.Id)
		if err != nil {
			fmt.Println("Error fetching flows for archived task:", task.Id, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		taskResponses[i] = TaskResponse{
			Task:  task,
			Flows: flows,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"tasks":      taskResponses,
		"totalCount": totalCount,
		"page":       page,
		"pageSize":   pageSize,
	})
}

func (ctrl *Controller) AgentHandleNewTask(ctx context.Context, task *domain.Task) error {
	devAgent := dev.DevAgent{
		TemporalClient:    ctrl.temporalClient,
		TemporalTaskQueue: ctrl.temporalTaskQueue,
		WorkspaceId:       task.WorkspaceId,
	}
	err := devAgent.HandleNewTask(ctx, task)
	if err != nil {
		return err
	}

	// Update the task status to in progress
	task.Status = domain.TaskStatusInProgress
	task.Updated = time.Now()
	err = ctrl.service.PersistTask(ctx, *task)
	if err != nil {
		return err
	}

	return nil
}

func (ctrl *Controller) GetFlowActionsHandler(c *gin.Context) {
	flowId := c.Param("id")
	workspaceId := c.Param("workspaceId")
	if ctrl.service == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database accessor not initialized"})
		return
	}
	flowActions, err := ctrl.service.GetFlowActions(c, workspaceId, flowId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get flow actions"})
		return
	}
	if flowActions == nil {
		flowActions = []domain.FlowAction{}
		_, err := ctrl.service.GetFlow(c, workspaceId, flowId)
		if err != nil {
			if errors.Is(err, srv.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get flow"})
				return
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"flowActions": flowActions})
}

func (ctrl *Controller) CompleteFlowActionHandler(c *gin.Context) {
	flowActionId := c.Param("id")

	ctx := c.Request.Context()
	workspaceId := c.Param("workspaceId")

	// Retrieve the flow action from the database
	flowAction, err := ctrl.service.GetFlowAction(ctx, workspaceId, flowActionId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve flow action"})
		return
	}

	// minimal validation
	if !flowAction.IsCallbackAction {
		c.JSON(http.StatusBadRequest, gin.H{"error": "This flow action doesn't support callback-based completion"})
		return
	} else if !flowAction.IsHumanAction {
		c.JSON(http.StatusBadRequest, gin.H{"error": "For now, only human actions can be completed via this endpoint"})
		return
	} else if flowAction.ActionStatus != domain.ActionStatusPending {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Flow action status is not pending"})
		return
	}

	var body struct {
		UserResponse struct {
			Content  string                 `json:"content"`
			Approved *bool                  `json:"approved"`
			Choice   string                 `json:"choice"`
			Params   map[string]interface{} `json:"params"`
		} `json:"userResponse"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	requestKindString, ok := flowAction.ActionParams["requestKind"].(string)
	if ok {
		switch flow_action.RequestKind(requestKindString) {
		case flow_action.RequestKindFreeForm:
			if strings.TrimSpace(body.UserResponse.Content) == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "User response cannot be empty"})
				return
			}
		case flow_action.RequestKindApproval:
			if body.UserResponse.Approved == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Approved cannot be empty"})
				return
			}
		case flow_action.RequestKindMergeApproval:
			if body.UserResponse.Approved == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Approved cannot be empty"})
				return
			}
			if body.UserResponse.Params["targetBranch"] == nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Target branch cannot be empty"})
			}
		case flow_action.RequestKindMultipleChoice:
			if strings.TrimSpace(body.UserResponse.Choice) == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "User choice cannot be empty"})
				return
			}
		}
	}

	devAgent := dev.DevAgent{
		TemporalClient:    ctrl.temporalClient,
		TemporalTaskQueue: ctrl.temporalTaskQueue,
		WorkspaceId:       workspaceId,
	}

	userResponse := flow_action.UserResponse{
		TargetWorkflowId: flowAction.FlowId,
		Content:          body.UserResponse.Content,
		Approved:         body.UserResponse.Approved,
		Choice:           body.UserResponse.Choice,
		Params:           body.UserResponse.Params,
	}
	if err := devAgent.RelayResponse(ctx, userResponse); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to relay user response"})
		return
	}

	// NOTE persisting explicitly shouldn't be required normally, i.e. when
	// using Track within flows, but we may have edge cases where we need to
	// persist explicitly and it doesn't hurt to do so here.
	userResponseJson, err := json.Marshal(userResponse)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to serialize user response"})
		return
	}
	flowAction.ActionResult = string(userResponseJson)
	flowAction.ActionStatus = domain.ActionStatusComplete

	if err := ctrl.service.PersistFlowAction(ctx, flowAction); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update flow action"})
		return
	}

	// Retrieve the flow and then task associated with the flow action
	flow, err := ctrl.service.GetFlow(ctx, workspaceId, flowAction.FlowId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve flow"})
		return
	}

	// If flow was paused, set it back to in_progress when completing an action
	if flow.Status == "paused" {
		flow.Status = "in_progress"
		if err := ctrl.service.PersistFlow(ctx, flow); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update flow status"})
			return
		}
	}

	task, err := ctrl.service.GetTask(ctx, workspaceId, flow.ParentId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve task"})
		return
	}

	// Update the task status and agent type
	task.Status = domain.TaskStatusInProgress
	task.AgentType = domain.AgentTypeLLM
	task.Updated = time.Now()
	if err := ctrl.service.PersistTask(ctx, task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task"})
		return
	}

	c.JSON(http.StatusOK, flowAction)
}

func (ctrl *Controller) UpdateFlowActionHandler(c *gin.Context) {
	flowActionId := c.Param("id")

	ctx := c.Request.Context()
	workspaceId := c.Param("workspaceId")

	// Retrieve the flow action from the database
	flowAction, err := ctrl.service.GetFlowAction(ctx, workspaceId, flowActionId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve flow action"})
		return
	}

	// minimal validation
	if !flowAction.IsCallbackAction {
		c.JSON(http.StatusBadRequest, gin.H{"error": "This flow action doesn't support callback-based completion"})
		return
	} else if !flowAction.IsHumanAction {
		c.JSON(http.StatusBadRequest, gin.H{"error": "For now, only human actions can be updated via this endpoint"})
		return
	} else if flowAction.ActionStatus != domain.ActionStatusPending {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Flow action status is not pending"})
		return
	}

	var body struct {
		UserResponse struct {
			Content  string                 `json:"content"`
			Approved *bool                  `json:"approved"`
			Choice   string                 `json:"choice"`
			Params   map[string]interface{} `json:"params"`
		} `json:"userResponse"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Reject requests where approved is not nil (indicates completion)
	if body.UserResponse.Approved != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Updates cannot include approval decision - use POST to complete the action"})
		return
	}

	devAgent := dev.DevAgent{
		TemporalClient:    ctrl.temporalClient,
		TemporalTaskQueue: ctrl.temporalTaskQueue,
		WorkspaceId:       workspaceId,
	}

	userResponse := flow_action.UserResponse{
		TargetWorkflowId: flowAction.FlowId,
		Content:          body.UserResponse.Content,
		Approved:         nil,
		Choice:           body.UserResponse.Choice,
		Params:           body.UserResponse.Params,
	}
	if err := devAgent.RelayResponse(ctx, userResponse); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to relay user response"})
		return
	}

	// Note: Unlike CompleteFlowActionHandler, we don't update ActionStatus or ActionResult
	// since this is an update, not a completion

	c.JSON(http.StatusOK, flowAction)
}

func (ctrl *Controller) UpdateTaskHandler(c *gin.Context) {
	requestCtx := c.Request.Context()
	workspaceId := c.Param("workspaceId")
	var taskReq TaskRequest
	if err := c.ShouldBindJSON(&taskReq); err != nil {
		ctrl.ErrorHandler(c, http.StatusBadRequest, err)
		return
	}
	taskReq.Id = c.Param("id")

	task, err := ctrl.service.GetTask(requestCtx, workspaceId, taskReq.Id)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			ctrl.ErrorHandler(c, http.StatusNotFound, err)
		} else {
			ctrl.ErrorHandler(c, http.StatusInternalServerError, err)
		}
		return
	}

	agentType, status, err := validateTaskRequest(&taskReq)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusBadRequest, err)
		return
	}

	// Update the 'updated' field to the current time before persisting
	task.Updated = time.Now()

	task.Description = taskReq.Description
	task.AgentType = agentType
	task.Status = status
	task.FlowOptions = taskReq.FlowOptions

	// If the task status is 'to_do' and there is no flow record, start the flow
	flows, err := ctrl.service.GetFlowsForTask(c, workspaceId, task.Id)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, err)
		return
	}

	if task.Status == domain.TaskStatusToDo && len(flows) == 0 {
		if err := ctrl.AgentHandleNewTask(requestCtx, &task); err != nil {
			ctrl.ErrorHandler(c, http.StatusInternalServerError, fmt.Errorf("Failed to handle new task: %w", err))
			task.Status = domain.TaskStatusFailed
			task.AgentType = domain.AgentTypeNone
			ctrl.service.PersistTask(c, task)
			return
		}
	}

	if err := ctrl.service.PersistTask(requestCtx, task); err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, fmt.Errorf("Failed to handle new task: %w", err))
		return
	}

	c.JSON(http.StatusOK, gin.H{"task": task})
}

func validateTaskRequest(taskReq *TaskRequest) (domain.AgentType, domain.TaskStatus, error) {
	var agentType domain.AgentType
	agentType, err := domain.StringToAgentType(taskReq.AgentType)
	if err != nil {
		return "", "", err
	}

	// Check if the 'Status' field is set in the request
	status, err := domain.StringToTaskStatus(taskReq.Status)
	if err != nil {
		return "", "", err
	}

	// if agentType wasn't provided, override default when it's dependent on status
	if taskReq.AgentType == "" && status == domain.TaskStatusDrafting {
		agentType = domain.AgentTypeHuman
	}

	if status == domain.TaskStatusDrafting {
		if agentType == domain.AgentTypeNone {
			agentType = domain.AgentTypeHuman
		} else if agentType != domain.AgentTypeHuman {
			return "", "", errors.New("When task status is 'drafting', the agent type must be 'human'")
		}
	} else if agentType == domain.AgentTypeNone && taskReq.Id == "" {
		return "", "", errors.New("Creating a task with agent type set to \"none\" is not allowed")
	}

	// Validate EnvType
	if envType, ok := taskReq.FlowOptions["envType"].(string); ok {
		if !env.EnvType(envType).IsValid() {
			return "", "", fmt.Errorf("invalid env type: %s", envType)
		}
	}

	return agentType, status, nil
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all connections by default
		// TODO /gen Add a check for the origin of the request based on an env variable for the origin
		return true
	},
}

func (ctrl *Controller) FlowActionChangesWebsocketHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	ctx, cancel := context.WithCancel(c.Request.Context())
	flowId := c.Param("id")

	if workspaceId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workspaceId"})
		return
	}
	if flowId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid flowId"})
		return
	}

	// Validate workspaceId
	if _, err := ctrl.service.GetWorkspace(ctx, workspaceId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Workspace not found"})
		} else {
			log.Printf("Error fetching workspace: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error fetching workspace"})
		}
		return
	}

	// Validate flowId under the given workspaceId
	if _, err := ctrl.service.GetFlow(ctx, workspaceId, flowId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		} else {
			log.Printf("Error fetching flow: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error fetching flow"})
		}
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	streamMessageStartId := "0"

	flowActionChan, errChan := ctrl.service.StreamFlowActionChanges(ctx, workspaceId, flowId, streamMessageStartId)

	// Handle disconnection detection in a separate goroutine
	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				log.Printf("Client disconnected or error: %v", err)
				cancel()
				return
			}
		}
	}()

	// Main loop for streaming flow actions
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("FlowActionChangesWebsocketHandler context cancelled, ending stream")
			return
		case err := <-errChan:
			log.Error().Err(err).Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Error streaming flow actions")
			return
		case flowAction, ok := <-flowActionChan:
			if !ok {
				log.Info().Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Flow action channel closed, ending stream")
				return
			}
			if err := conn.WriteJSON(flowAction); err != nil {
				log.Error().Err(err).Str("workspaceId", workspaceId).Str("flowId", flowId).Msg("Error writing flow action to websocket")
				return
			}
		}
	}
}

func (ctrl *Controller) TaskChangesWebsocketHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	lastTaskStreamId := c.Query("lastTaskStreamId")
	if lastTaskStreamId == "" {
		lastTaskStreamId = "$" // Start from the latest message by default
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		http.Error(c.Writer, "Could not open websocket connection", http.StatusBadRequest)
		return
	}
	defer conn.Close()

	// Create a new context that's canceled when the WebSocket connection is closed
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// Handle disconnection detection in a separate goroutine
	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				log.Printf("Client disconnected or error: %v", err)
				cancel()
				return
			}
		}
	}()

	taskChan, errChan := ctrl.service.StreamTaskChanges(ctx, workspaceId, lastTaskStreamId)

	for {
		select {
		case <-ctx.Done():
			log.Info().Str("workspaceId", workspaceId).Msg("Task changes client disconnected")
			return
		case err := <-errChan:
			if err != nil {
				log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Error streaming task changes")
				return
			}
		case task, ok := <-taskChan:
			if !ok {
				log.Info().Str("workspaceId", workspaceId).Msg("Task channel closed")
				return
			}
			flows, err := ctrl.service.GetFlowsForTask(ctx, workspaceId, task.Id)
			if err != nil {
				log.Error().Err(err).Str("workspaceId", workspaceId).Str("taskId", task.Id).Msg("Error getting flows for task")
				return
			}
			taskResponse := TaskResponse{
				Task:  task,
				Flows: flows,
			}
			taskData := map[string]interface{}{
				"tasks":            []TaskResponse{taskResponse},
				"lastTaskStreamId": task.StreamId,
			}
			if err := conn.WriteJSON(taskData); err != nil {
				log.Error().Err(err).Str("workspaceId", workspaceId).Str("taskId", task.Id).Msg("Error writing task to websocket")
				return
			}
		}
	}
}

func (ctrl *Controller) FlowEventsWebsocketHandler(c *gin.Context) {
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	if workspaceId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid workspaceId"})
		return
	}
	if flowId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid flowId"})
		return
	}

	// Validate workspaceId
	if _, err := ctrl.service.GetWorkspace(ctx, workspaceId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Workspace not found"})
		} else {
			log.Printf("Error fetching workspace: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error fetching workspace"})
		}
		return
	}

	// Validate flowId under the given workspaceId
	if _, err := ctrl.service.GetFlow(ctx, workspaceId, flowId); err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
		} else {
			log.Printf("Error fetching flow: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error fetching flow"})
		}
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	subscriptionCh := make(chan domain.FlowEventSubscription, 100)
	defer close(subscriptionCh)

	// Goroutine to read subscription messages and handle disconnection detection
	go func() {
		for {
			_, r, err := conn.NextReader()
			if err != nil {
				// Log client disconnection as info, not error, unless it's an unexpected error
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Error().Err(err).Msg("FlowEventsWebsocketHandler client read error")
				} else {
					log.Info().Msg("FlowEventsWebsocketHandler client disconnected")
				}
				cancel()
				return
			}
			var sub domain.FlowEventSubscription
			err = json.NewDecoder(r).Decode(&sub)
			if err == io.EOF {
				// One value is expected in the message.
				err = io.ErrUnexpectedEOF
			}
			if err != nil {
				log.Error().Err(err).Msg("Invalid message format received in FlowEventsWebsocketHandler")
				continue
			}
			log.Debug().Str("parentId", sub.ParentId).Msg("received subscription message")
			subscriptionCh <- sub
		}
	}()

	flowEventCh, errCh := ctrl.service.StreamFlowEvents(ctx, workspaceId, flowId, subscriptionCh)

	// Main loop for streaming flow events
	for {
		select {
		case <-ctx.Done():
			log.Printf("Client disconnected, ending stream\n")
			return
		case err := <-errCh:
			log.Printf("Error streaming flow events: %v", err)
			return
		case flowEvent := <-flowEventCh:
			if err := conn.WriteJSON(flowEvent); err != nil {
				log.Printf("Error writing flow event to websocket: %v", err)
				return
			}
		}
	}
}

// Wildcard route to serve index.html for other HTML-based frontend routes,
// eg /kanban etc as they get defined by the frontend. This also serves the
// root route rather than a custom route for the root.
func (ctrl *Controller) WildcardHandler(c *gin.Context) {
	// only do this for web page load requests
	acceptHeader := c.Request.Header.Get("Accept")
	isWebPage := strings.Contains(acceptHeader, "text/html") || strings.Contains(acceptHeader, "*/*") || acceptHeader == ""
	isWebPage = isWebPage && !strings.Contains(c.Request.URL.Path, "/api/")
	isWebPage = isWebPage && !strings.Contains(c.Request.URL.Path, "/ws/")
	if !isWebPage {
		c.Status(http.StatusNotFound)
		return
	}

	// render index.html
	file, err := frontend.DistFs.Open("dist/index.html")
	if err != nil {
		log.Error().Err(err).Msg("Failed to open index.html")
		c.Status(http.StatusInternalServerError)
		return
	} else {
		c.Status(http.StatusOK)
		_, err = io.Copy(c.Writer, file)
		if err != nil {
			log.Error().Err(err).Msg("Failed to serve index.html")
		}
	}
}
