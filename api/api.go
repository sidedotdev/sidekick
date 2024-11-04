package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"sidekick/common"
	"sidekick/dev"
	"sidekick/domain"
	domain1 "sidekick/domain"
	"sidekick/frontend"
	"sidekick/srv"
	"sidekick/srv/redis"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/segmentio/ksuid"
	"go.temporal.io/sdk/client"
)

func RunServer() *http.Server {
	gin.SetMode(gin.ReleaseMode)
	ctrl := NewController()
	router := DefineRoutes(ctrl)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", common.GetServerPort()),
		Handler: router.Handler(),
	}

	// Start server in a goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start API server: %v\n", err)
		}
	}()

	return srv
}

type Controller struct {
	service           srv.Service
	temporalClient    client.Client
	temporalNamespace string
	temporalTaskQueue string
}

// ArchiveTaskHandler handles the request to archive a task
func (ctrl *Controller) ArchiveTaskHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	taskId := c.Param("id")

	task, err := ctrl.dbAccessor.GetTask(c.Request.Context(), workspaceId, taskId)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusNotFound, errors.New("task not found"))
		return
	}

	// Check if the task status is valid for archiving
	if task.Status != models.TaskStatusCanceled && task.Status != models.TaskStatusFailed && task.Status != models.TaskStatusComplete {
		ctrl.ErrorHandler(c, http.StatusBadRequest, errors.New("only tasks with status 'canceled', 'failed', or 'complete' can be archived"))
		return
	}

	// Set the Archived field to the current timestamp
	now := time.Now()
	task.Archived = &now

	// Persist the updated task
	err = ctrl.dbAccessor.PersistTask(c.Request.Context(), task)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, errors.New("failed to archive task"))
		return
	}

	c.Status(http.StatusNoContent)
}

func DefineRoutes(ctrl Controller) *gin.Engine {
	r := gin.Default()
	r.ForwardedByClientIP = true
	r.SetTrustedProxies(nil)

	workspaceApiRoutes := DefineWorkspaceApiRoutes(r, &ctrl)

	taskRoutes := workspaceApiRoutes.Group("/:workspaceId/tasks")
	taskRoutes.POST("/", ctrl.CreateTaskHandler)
	taskRoutes.GET("/", ctrl.GetTasksHandler)
	taskRoutes.GET("/:id", ctrl.GetTaskHandler)
	taskRoutes.PUT("/:id", ctrl.UpdateTaskHandler)
	taskRoutes.DELETE("/:id", ctrl.DeleteTaskHandler)
	taskRoutes.POST("/:id/archive", ctrl.ArchiveTaskHandler)
	taskRoutes.POST("/:id/cancel", ctrl.CancelTaskHandler)

	flowRoutes := workspaceApiRoutes.Group("/:workspaceId/flows")
	flowRoutes.GET("/:id/actions", ctrl.GetFlowActionsHandler)
	flowRoutes.POST("/:id/cancel", ctrl.CancelFlowHandler)
	flowRoutes.GET("/:id/action_changes", ctrl.GetFlowActionChangesHandler)

	workspaceApiRoutes.POST("/:workspaceId/flow_actions/:id/complete", ctrl.CompleteFlowActionHandler)

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
		log.Fatal("Failed to read embedded files", err)
	}
	dist := http.FS(frontend.DistFs)
	for _, file := range files {
		r.StaticFileFS("/"+file.Name(), "dist/"+file.Name(), dist)
	}

	// Wildcard route to serve index.html for other HTML-based frontend routes,
	// eg /kanban etc as they get defined by the frontend. This also serves the
	// root route rather than a custom route for the root.
	r.NoRoute(func(c *gin.Context) {
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
			fmt.Println("Failed to open index.html", err)
			c.Status(http.StatusInternalServerError)
			return
		} else {
			c.Status(http.StatusOK)
			_, err = io.Copy(c.Writer, file)
			if err != nil {
				log.Println("Failed to serve index.html", err)
			}
		}
	})

	return r
}

func NewController() Controller {

	clientOptions := client.Options{
		HostPort: common.GetTemporalServerHostPort(),
	}
	temporalClient, err := client.NewLazyClient(clientOptions)
	if err != nil {
		log.Fatal("Failed to create Temporal client", err)
	}

	redisStorage := redis.NewStorage()
	service := srv.NewDelegator(redisStorage, redis.NewStreamer())
	err = service.CheckConnection(context.Background())
	if err != nil {
		log.Fatal("Failed to connect to Redis", err)
	}

	return Controller{
		service:           service,
		temporalClient:    temporalClient,
		temporalNamespace: common.GetTemporalNamespace(),
		temporalTaskQueue: common.GetTemporalTaskQueue(),
	}
}

func (ctrl *Controller) ErrorHandler(c *gin.Context, status int, err error) {
	log.Println("Error:", err)
	c.JSON(status, gin.H{"error": err.Error()})
}

// CancelTaskHandler handles the cancellation of a task
func (ctrl *Controller) CancelTaskHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	taskId := c.Param("id")

	task, err := ctrl.dbAccessor.GetTask(c.Request.Context(), workspaceId, taskId)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Task not found"})
		return
	}

	// Check if the task status is eligible for cancellation
	if task.Status != models.TaskStatusToDo && task.Status != models.TaskStatusInProgress && task.Status != models.TaskStatusBlocked {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only tasks with status 'to_do', 'in_progress', or 'blocked' can be canceled"})
		return
	}

	// Get the child workflows of the task
	childFlows, err := ctrl.dbAccessor.GetFlowsForTask(c.Request.Context(), workspaceId, taskId)
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to terminate workflow"})
			return
		}
	}

	// Update the task status to 'canceled'
	task.Status = models.TaskStatusCanceled
	err = ctrl.dbAccessor.PersistTask(c.Request.Context(), task)
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
			log.Println("Error terminating workflow:", err)
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

func (ctrl *Controller) CancelFlowHandler(c *gin.Context) {
	workflowID := c.Param("id")
	// FIXME update the flow status in the database

	err := ctrl.temporalClient.CancelWorkflow(c.Request.Context(), workflowID, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": fmt.Sprintf("Failed to cancel workflow: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Workflow cancelled successfully",
	})
}

type TaskRequest struct {
	Id          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	FlowType    string `json:"flowType"`
	AgentType   string `json:"agentType"`
	Status      string `json:"status"`
	FlowOptions map[string]interface{}
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

	c.JSON(http.StatusOK, gin.H{"task": task})
}

func (ctrl *Controller) GetTasksHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	if workspaceId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID is required"})
		return
	}

	statusesStr := c.Query("statuses")
	if statusesStr == "" || statusesStr == "all" {
		statusesStr = "to_do,drafting,blocked,in_progress,complete,failed,canceled"
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

func (ctrl *Controller) GetFlowActionChangesHandler(c *gin.Context) {
	flowId := c.Param("id")
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	clientGone := c.Request.Context().Done()

	events := make(chan interface{}, 10)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		for event := range events {
			switch event := event.(type) {
			case domain.FlowAction:
				select {
				case <-clientGone:
					// if the client has disconnected, stop sending events
					log.Println("Flow action changes client disconnected")
					return
				default:
					c.SSEvent("flow/action", event)
					c.Writer.Flush()
				}
			case error:
				c.SSEvent("error", gin.H{"error": event.Error()})
				c.Writer.Flush()
			}
		}
		log.Println("Flow action changes events channel closed")
		wg.Done()
	}()

	ctx := c.Request.Context()
	workspaceId := c.Param("workspaceId")
	streamMessageStartId := "0"
	maxCount := int64(100)
	blockDuration := time.Second * 0

	for {
		select {
		case <-clientGone:
			// if the client has disconnected, stop fetching events
			log.Println("Flow action changes client disconnected")
			close(events)
			return
		default:
			flowActions, lastStreamId, err := ctrl.service.GetFlowActionChanges(ctx, workspaceId, flowId, streamMessageStartId, maxCount, blockDuration)
			if err != nil {
				log.Printf("Failed to get flow actions: %v\n", err)
				events <- err
				close(events)
				return
			}

			for _, flowAction := range flowActions {
				events <- flowAction
			}

			if lastStreamId == "end" {
				close(events)
				wg.Wait()
				return
			}
			streamMessageStartId = lastStreamId
		}
	}
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
			Content  string `json:"content"`
			Approved *bool  `json:"approved"`
			Choice   string `json:"choice"`
		} `json:"userResponse"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	if strings.TrimSpace(body.UserResponse.Content) == "" && body.UserResponse.Approved == nil && strings.TrimSpace(body.UserResponse.Choice) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User response cannot be empty"})
		return
	}

	devAgent := dev.DevAgent{
		TemporalClient:    ctrl.temporalClient,
		TemporalTaskQueue: ctrl.temporalTaskQueue,
		WorkspaceId:       workspaceId,
	}

	userResponse := dev.UserResponse{
		TargetWorkflowId: flowAction.FlowId,
		Content:          body.UserResponse.Content,
		Approved:         body.UserResponse.Approved,
		Choice:           body.UserResponse.Choice,
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
	task, err := ctrl.service.GetTask(ctx, workspaceId, flow.ParentId)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve task"})
		return
	}

	// Update the task status and agent type
	task.Status = domain.TaskStatusInProgress
	task.AgentType = domain.AgentTypeLLM
	if err := ctrl.service.PersistTask(ctx, task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update task"})
		return
	}

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
	ctx := c.Request.Context()
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
	maxCount := int64(100)
	blockDuration := time.Second * 0

	clientGone := make(chan struct{})

	// Handle disconnection detection in a separate goroutine
	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				log.Printf("Client disconnected or error: %v", err)
				close(clientGone)
				return
			}
		}
	}()

	// Main loop for streaming flow actions
	for {
		select {
		case <-clientGone:
			log.Println("Client disconnected, ending stream")
			return
		default:
			// Attempt to fetch the flow actions
			flowActions, lastStreamId, err := ctrl.service.GetFlowActionChanges(
				ctx, workspaceId, flowId, streamMessageStartId, maxCount, blockDuration,
			)
			if err != nil {
				log.Printf("Error fetching flow actions: %v", err)
				return
			}

			// Streaming each flow action
			for _, flowAction := range flowActions {
				if err := conn.WriteJSON(flowAction); err != nil {
					log.Printf("Error writing flow action to websocket: %v", err)
					return
				}
			}

			// Check if streaming should end based on data
			if lastStreamId == "end" || len(flowActions) == 0 {
				log.Println("Stream concluded: No new actions")
				return
			}
			streamMessageStartId = lastStreamId
		}
	}
}

func (ctrl *Controller) TaskChangesWebsocketHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	lastTaskStreamId := c.Query("lastTaskStreamId")
	if lastTaskStreamId == "" {
		lastTaskStreamId = "$" // Start from the latest message by default
	}

	clientGone := make(chan struct{})
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		http.Error(c.Writer, "Could not open websocket connection", http.StatusBadRequest)
		return
	}
	defer conn.Close()

	// Handle disconnection detection in a separate goroutine
	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				log.Printf("Client disconnected or error: %v", err)
				close(clientGone)
				return
			}
		}
	}()

	for {
		select {
		case <-clientGone:
			// if the client has disconnected, close the connection
			log.Println("Flow action changes client disconnected")
			close(clientGone)
			return
		default:
			tasks, lastId, err := ctrl.service.GetTaskChanges(context.Background(), workspaceId, lastTaskStreamId, 50, 0)
			if err != nil {
				log.Printf("Error getting task changes: %v", err)
				return
			}
			if len(tasks) > 0 {
				taskResponses := make([]TaskResponse, len(tasks))
				for i, task := range tasks {
					flows, err := ctrl.service.GetFlowsForTask(c, workspaceId, task.Id)

					if err != nil {
						log.Printf("Error getting flows for task: %v", err)
						return
					}
					taskResponses[i] = TaskResponse{
						Task:  task,
						Flows: flows,
					}
				}

				lastTaskStreamId = lastId
				taskData := map[string]interface{}{
					"tasks":            taskResponses,
					"lastTaskStreamId": lastTaskStreamId,
				}
				if err := conn.WriteJSON(taskData); err != nil {
					log.Printf("Error writing tasks to websocket: %v", err)
					return
				}
			}
		}
	}
}

type FlowEventSubscription struct {
	ParentId            string `json:"parentId"`
	LastStreamMessageId string `json:"lastStreamMessageId,omitempty"`
}

func (ctrl *Controller) FlowEventsWebsocketHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	ctx := c.Request.Context()
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

	streamKeys := sync.Map{}
	maxCount := int64(100)
	blockDuration := time.Millisecond * 250 // Note: we can't purely block with 0 duration as we want to handle new stream keys

	clientGone := make(chan struct{})

	// Goroutine to read subscription messages and handle disconnection detection
	go func() {
		for {
			_, r, err := conn.NextReader()
			if err != nil {
				log.Printf("Client disconnected or error: %v", err)
				close(clientGone)
				return
			}
			var sub FlowEventSubscription
			err = json.NewDecoder(r).Decode(&sub)
			if err == io.EOF {
				// One value is expected in the message.
				err = io.ErrUnexpectedEOF
			}
			if err != nil {
				log.Printf("Invalid message format: %v", err)
			}
			if sub.LastStreamMessageId == "" {
				sub.LastStreamMessageId = "0"
			}
			streamKey := fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, sub.ParentId)
			streamKeys.Store(streamKey, sub.LastStreamMessageId)
		}
	}()

	// Main loop for streaming flow events
	for {
		select {
		case <-clientGone:
			log.Println("Client disconnected, ending stream")
			return
		default:
			// Convert sync.Map to a regular map for `GetFlowEvents`
			keysMap := make(map[string]string)
			streamKeys.Range(func(key, value interface{}) bool {
				keysMap[key.(string)] = value.(string)
				return true
			})

			// wait until we have at least one stream key to fetch
			if len(keysMap) == 0 {
				time.Sleep(time.Millisecond * 20)
				continue
			}

			// Attempt to fetch the flow events
			flowEvents, lastStreamKeys, err := ctrl.service.GetFlowEvents(
				ctx, workspaceId, keysMap, maxCount, blockDuration,
			)
			if err != nil {
				log.Printf("Error fetching flow events: %v", err)
				return
			}

			// Update the stream keys for subsequent fetches
			for key, lastId := range lastStreamKeys {
				streamKeys.Store(key, lastId)
			}

			// Streaming each flow event
			for _, flowEvent := range flowEvents {
				if err := conn.WriteJSON(flowEvent); err != nil {
					log.Printf("Error writing flow event to websocket: %v", err)
					return
				}

				// remove stream keys that have been marked as ended
				if flowEvent.GetEventType() == domain1.EndStreamEventType {
					for key := range keysMap {
						if strings.HasSuffix(key, flowEvent.GetParentId()) {
							streamKeys.Delete(key)
						}
					}
				}
			}
		}
	}
}
