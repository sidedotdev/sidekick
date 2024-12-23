package api

import (
	"errors"
	"net/http"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/segmentio/ksuid"
)

// WorkspaceRequest defines the structure for workspace creation and update requests.
type WorkspaceRequest struct {
	Name            string                 `json:"name"`
	LocalRepoDir    string                 `json:"localRepoDir"`
	LLMConfig       common.LLMConfig       `json:"llmConfig,omitempty"`
	EmbeddingConfig common.EmbeddingConfig `json:"embeddingConfig,omitempty"`
}

type WorkspaceResponse struct {
	Id              string                 `json:"id"`
	Created         time.Time              `json:"created"`
	Updated         time.Time              `json:"updated"`
	Name            string                 `json:"name"`
	LocalRepoDir    string                 `json:"localRepoDir"`
	LLMConfig       common.LLMConfig       `json:"llmConfig,omitempty"`
	EmbeddingConfig common.EmbeddingConfig `json:"embeddingConfig,omitempty"`
}

func DefineWorkspaceApiRoutes(r *gin.Engine, ctrl *Controller) *gin.RouterGroup {
	workspaceApiRoutes := r.Group("/api/v1/workspaces")
	workspaceApiRoutes.POST("/", ctrl.CreateWorkspaceHandler)
	workspaceApiRoutes.GET("/", ctrl.GetWorkspacesHandler)
	workspaceApiRoutes.GET("/:workspaceId", ctrl.GetWorkspaceByIdHandler)
	workspaceApiRoutes.PUT("/:workspaceId", ctrl.UpdateWorkspaceHandler)
	return workspaceApiRoutes
}

func (ctrl *Controller) CreateWorkspaceHandler(c *gin.Context) {
	var workspaceReq WorkspaceRequest
	if err := c.ShouldBindJSON(&workspaceReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if workspaceReq.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name is required"})
		return
	}

	if workspaceReq.LocalRepoDir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "LocalRepoDir is required"})
		return
	}

	workspace := domain.Workspace{
		Id:           "ws_" + ksuid.New().String(),
		Name:         workspaceReq.Name,
		LocalRepoDir: workspaceReq.LocalRepoDir,
		Created:      time.Now(),
		Updated:      time.Now(),
	}

	if err := ctrl.dbAccessor.PersistWorkspace(c, workspace); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create workspace"})
		return
	}

	workspaceConfig := domain.WorkspaceConfig{
		LLM: common.LLMConfig{
			Defaults: workspaceReq.LLMConfig.Defaults,
		},
		Embedding: common.EmbeddingConfig{
			Defaults: workspaceReq.EmbeddingConfig.Defaults,
		},
	}

	// TODO /gen call SchedulePollFailuresWorkflow here, after fixing the TODO there

	if err := ctrl.dbAccessor.PersistWorkspaceConfig(c, workspace.Id, workspaceConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create workspace configuration"})
		return
	}

	response := WorkspaceResponse{
		Id:              workspace.Id,
		Created:         workspace.Created,
		Updated:         workspace.Updated,
		Name:            workspace.Name,
		LocalRepoDir:    workspace.LocalRepoDir,
		LLMConfig:       workspaceConfig.LLM,
		EmbeddingConfig: workspaceConfig.Embedding,
	}

	c.JSON(http.StatusOK, gin.H{"workspace": response})
}

// TODO /gen finalize this implementation with task queue set up properly with worker
func (ctrl *Controller) SchedulePollFailuresWorkflow() error {
	// Schedule the PollFailuresWorkflow for the newly created workspace
	// TODO /gen change this from a scheduled action to instead be a single
	// workflow that just loops with a timer and uses ContinueAsNew when needed
	// within the workflow and just kick it off once here. Also we only need
	// just one such workflow for the entire namespace instead of one per
	// workspace: the workspace should be an attribute on the temporal workflow
	// itself.
	//scheduleId := workspace.Id + "_poll_failures_schedule"
	//workflowId := workspace.Id + "_poll_failures_workflow"
	//_, err := ctrl.temporalClient.ScheduleClient().Create(c, client.ScheduleOptions{
	//	ID: scheduleId,
	//	Spec: client.ScheduleSpec{
	//		Intervals: []client.ScheduleIntervalSpec{
	//			{Every: 5 * time.Minute},
	//		},
	//	},
	//	Action: &client.ScheduleWorkflowAction{
	//		ID:       workflowId,
	//		Workflow: "PollFailuresWorkflow",
	//		Args: []any{
	//			poll_failures.PollFailuresWorkflowInput{
	//				WorkspaceId: workspace.Id,
	//			},
	//		},
	//		TaskQueue: "sidekick_maintenance",
	//		TypedSearchAttributes: temporal.NewSearchAttributes(
	//			temporal.NewSearchAttributeKeyString("WorkspaceId").ValueSet(workspace.Id),
	//		),
	//	},
	//})
	//if err != nil {
	//	return err
	//}
	return nil
}

func (ctrl *Controller) GetWorkspaceByIdHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")

	workspace, err := ctrl.dbAccessor.GetWorkspace(c, workspaceId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Workspace not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get workspace"})
		}
		return
	}

	response := WorkspaceResponse{
		Id:           workspace.Id,
		Created:      workspace.Created,
		Updated:      workspace.Updated,
		Name:         workspace.Name,
		LocalRepoDir: workspace.LocalRepoDir,
	}

	config, err := ctrl.dbAccessor.GetWorkspaceConfig(c, workspaceId)
	if err != nil {
		if !errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get workspace configuration"})
			return
		}
		// If the configuration is not found, we'll return the workspace without the config
		response.LLMConfig.Defaults = []common.ModelConfig{}
		response.LLMConfig.UseCaseConfigs = map[string][]common.ModelConfig{}
		response.EmbeddingConfig.Defaults = []common.ModelConfig{}
		response.EmbeddingConfig.UseCaseConfigs = map[string][]common.ModelConfig{}
	} else {
		response.LLMConfig = config.LLM
		response.EmbeddingConfig = config.Embedding
	}

	c.JSON(http.StatusOK, gin.H{"workspace": response})
}

func (ctrl *Controller) UpdateWorkspaceHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	var workspaceReq WorkspaceRequest
	if err := c.ShouldBindJSON(&workspaceReq); err != nil {
		ctrl.ErrorHandler(c, http.StatusBadRequest, err)
		return
	}

	if workspaceReq.Name == "" && workspaceReq.LocalRepoDir == "" && workspaceReq.LLMConfig.Defaults == nil && workspaceReq.EmbeddingConfig.Defaults == nil {
		ctrl.ErrorHandler(c, http.StatusBadRequest, errors.New("At least one of Name, LocalRepoDir, LLMConfig, or EmbeddingConfig is required"))
		return
	}

	workspace, err := ctrl.dbAccessor.GetWorkspace(c, workspaceId)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusNotFound, err)
		return
	}

	workspaceConfig, err := ctrl.dbAccessor.GetWorkspaceConfig(c, workspaceId)
	if err != nil {
		if !errors.Is(err, srv.ErrNotFound) {
			ctrl.ErrorHandler(c, http.StatusInternalServerError, err)
			return
		}
		// If the config is not found, create a new one
		workspaceConfig = domain.WorkspaceConfig{
			LLM:       common.LLMConfig{},
			Embedding: common.EmbeddingConfig{},
		}
	}

	if workspaceReq.Name != "" {
		workspace.Name = workspaceReq.Name
	}
	if workspaceReq.LocalRepoDir != "" {
		workspace.LocalRepoDir = workspaceReq.LocalRepoDir
	}
	if workspaceReq.LLMConfig.Defaults != nil {
		workspaceConfig.LLM.Defaults = workspaceReq.LLMConfig.Defaults
	}
	if workspaceReq.EmbeddingConfig.Defaults != nil {
		workspaceConfig.Embedding.Defaults = workspaceReq.EmbeddingConfig.Defaults
	}
	workspace.Updated = time.Now()

	if err := ctrl.dbAccessor.PersistWorkspace(c, workspace); err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, err)
		return
	}

	if err := ctrl.dbAccessor.PersistWorkspaceConfig(c, workspaceId, workspaceConfig); err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, err)
		return
	}

	response := WorkspaceResponse{
		Id:              workspace.Id,
		Created:         workspace.Created,
		Updated:         workspace.Updated,
		Name:            workspace.Name,
		LocalRepoDir:    workspace.LocalRepoDir,
		LLMConfig:       workspaceConfig.LLM,
		EmbeddingConfig: workspaceConfig.Embedding,
	}

	c.JSON(http.StatusOK, gin.H{"workspace": response})
}

// GetWorkspacesHandler handles the request for listing all workspaces
func (c *Controller) GetWorkspacesHandler(ctx *gin.Context) {
	workspaces, err := c.dbAccessor.GetAllWorkspaces(ctx)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve workspaces"})
		return
	}
	if workspaces == nil {
		workspaces = []domain.Workspace{}
	}
	// Format the workspace data into JSON and return it in the response
	ctx.JSON(http.StatusOK, gin.H{"workspaces": workspaces})
}
