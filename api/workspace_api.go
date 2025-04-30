package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/utils"
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

// BranchInfo represents information about a single Git branch for the API response.
type BranchInfo struct {
	Name      string `json:"name"`
	IsCurrent bool   `json:"isCurrent"`
	IsDefault bool   `json:"isDefault"`
}

func DefineWorkspaceApiRoutes(r *gin.Engine, ctrl *Controller) *gin.RouterGroup {
	workspaceApiRoutes := r.Group("api/v1/workspaces")
	workspaceApiRoutes.POST("", ctrl.CreateWorkspaceHandler)
	workspaceApiRoutes.GET("", ctrl.GetWorkspacesHandler)
	workspaceApiRoutes.GET(":workspaceId", ctrl.GetWorkspaceByIdHandler)
	workspaceApiRoutes.PUT(":workspaceId", ctrl.UpdateWorkspaceHandler)
	workspaceApiRoutes.GET(":workspaceId/branches", ctrl.GetWorkspaceBranchesHandler) // Add route for listing branches

	// Create a group with workspaceId parameter for nested routes
	workspaceGroup := workspaceApiRoutes.Group(":workspaceId")
	workspaceGroup.GET("/subflows/:id", ctrl.GetSubflowHandler)

	return workspaceGroup
}

// GetWorkspaceBranchesHandler retrieves the list of available Git branches for a workspace,
// excluding branches associated with managed worktrees.
func (ctrl *Controller) GetWorkspaceBranchesHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	if workspaceId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspaceId is required"})
		return
	}

	ctx := c.Request.Context() // Use context from request

	// 1. Call ctrl.service.GetWorkspace
	workspace, err := ctrl.service.GetWorkspace(ctx, workspaceId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) { // Use the requested error check
			c.JSON(http.StatusNotFound, gin.H{"error": "Workspace not found"})
		} else {
			log.Printf("Error getting workspace %s: %v", workspaceId, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get workspace"})
		}
		return
	}

	if workspace.LocalRepoDir == "" {
		log.Printf("Workspace %s has no LocalRepoDir configured", workspaceId)
		c.JSON(http.StatusConflict, gin.H{"error": "Workspace repository directory not configured"})
		return
	}
	repoDir := workspace.LocalRepoDir

	// Ensure the repo directory exists before running git commands
	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		log.Printf("Error: workspace repository directory does not exist: %s", repoDir)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Workspace repository directory not found"})
		return
	}

	// 2. Call coding/git.ListWorktreesActivity
	// TODO move this into determineManagedWorktreeBranches
	gitWorktrees, err := git.ListWorktreesActivity(ctx, repoDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list worktrees"})
	}
	fmt.Printf("List of worktrees for workspace %s:\n%s\n", workspaceId, utils.PrettyJSON(gitWorktrees))

	// 3. Call determineManagedWorktreeBranches
	managedWorktreeBranches, err := determineManagedWorktreeBranches(&workspace, gitWorktrees)
	if err != nil {
		log.Printf("Error determining managed worktree branches for workspace %s: %v", workspaceId, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to determine managed worktrees"})
		return
	}
	log.Printf("Identified managed worktree branches for workspace %s: %v", workspaceId, managedWorktreeBranches)

	// 4. Call coding/git activities
	currentBranchName, isDetached, err := git.GetCurrentBranch(ctx, repoDir)
	if err != nil {
		// Log the error but proceed. If we can't get the current branch, none will be marked as current.
		log.Printf("Warning: failed to get current branch for workspace %s in dir %s: %v", workspaceId, repoDir, err)
		currentBranchName = "" // Ensure it's empty on error
		isDetached = false     // Assume not detached if error occurred
	}

	defaultBranchName, err := git.GetDefaultBranch(ctx, repoDir)
	if err != nil {
		// Log the error but proceed. If we can't get the default branch, none will be marked as default.
		log.Printf("Warning: failed to get default branch for workspace %s in dir %s: %v", workspaceId, repoDir, err)
		defaultBranchName = "" // Ensure it's empty on error
	}

	localBranchNames, err := git.ListLocalBranches(ctx, repoDir)
	if err != nil {
		log.Printf("Error listing local branches for workspace %s in dir %s: %v", workspaceId, repoDir, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list git branches"})
		return
	}

	// 5. Filter branches
	// 6. Format results
	var filteredBranches []BranchInfo
	for _, branchName := range localBranchNames {
		if branchName == "" {
			continue // Skip empty lines just in case
		}
		if _, isManaged := managedWorktreeBranches[branchName]; isManaged {
			log.Printf("Filtering out managed worktree branch: %s", branchName)
			continue // Skip branches associated with managed worktrees
		} else {
			log.Printf("NOT filtering out worktree branch: %s", branchName)

		}
		filteredBranches = append(filteredBranches, BranchInfo{
			Name:      branchName,
			IsCurrent: !isDetached && branchName == currentBranchName,      // Only mark current if not detached
			IsDefault: branchName != "" && branchName == defaultBranchName, // Avoid marking empty string as default
		})
	}

	// 7. Return JSON response (matching original structure)
	c.JSON(http.StatusOK, gin.H{"branches": filteredBranches})
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

	if err := ctrl.service.PersistWorkspace(c, workspace); err != nil {
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

	if err := ctrl.service.PersistWorkspaceConfig(c, workspace.Id, workspaceConfig); err != nil {
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

	workspace, err := ctrl.service.GetWorkspace(c, workspaceId)
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

	config, err := ctrl.service.GetWorkspaceConfig(c, workspaceId)
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

	workspace, err := ctrl.service.GetWorkspace(c, workspaceId)
	if err != nil {
		ctrl.ErrorHandler(c, http.StatusNotFound, err)
		return
	}

	workspaceConfig, err := ctrl.service.GetWorkspaceConfig(c, workspaceId)
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

	if err := ctrl.service.PersistWorkspace(c, workspace); err != nil {
		ctrl.ErrorHandler(c, http.StatusInternalServerError, err)
		return
	}

	if err := ctrl.service.PersistWorkspaceConfig(c, workspaceId, workspaceConfig); err != nil {
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
	workspaces, err := c.service.GetAllWorkspaces(ctx)
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

// determineManagedWorktreeBranches identifies branches associated with
// sidekick-managed worktrees.
func determineManagedWorktreeBranches(workspace *domain.Workspace, gitWorktrees []git.GitWorktree) (map[string]struct{}, error) {
	managedBranches := make(map[string]struct{})

	sidekickDataHome, err := common.GetSidekickDataHome()
	if err != nil {
		return nil, fmt.Errorf("failed to get sidekick data home: %w", err)
	}

	managedWorktreeBaseDir := filepath.Join(sidekickDataHome, "worktrees", workspace.Id)
	for _, gitWorktree := range gitWorktrees {
		// using Contains because of symlinks in osx (/private/var/folders/... -> /var/folders/...)
		if strings.Contains(gitWorktree.Path, managedWorktreeBaseDir) {
			managedBranches[gitWorktree.Branch] = struct{}{}
		}
	}

	return managedBranches, nil
}
