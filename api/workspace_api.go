package api

import (
	"errors"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/srv"
	"strings"
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

	workspace, err := ctrl.service.GetWorkspace(c.Request.Context(), workspaceId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Workspace not found"})
		} else {
			// Log the unexpected error for debugging
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

	// --- Determine managed worktree branches ---
	managedWorktreeBranches := make(map[string]struct{})
	sidekickDataHome, err := common.GetSidekickDataHome()
	if err != nil {
		log.Printf("Error getting sidekick data home: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to determine sidekick data directory"})
		return
	}
	managedWorktreeBasePath := filepath.Join(sidekickDataHome, "worktrees", workspaceId)

	cmdWorktreeList := exec.Command("git", "worktree", "list", "--porcelain")
	cmdWorktreeList.Dir = repoDir
	worktreeOutput, err := cmdWorktreeList.Output()
	if err != nil {
		// Log the error but continue; failure here just means we might not filter perfectly.
		log.Printf("Warning: 'git worktree list --porcelain' failed in %s: %v. Proceeding without filtering worktree branches.", repoDir, err)
	} else {
		lines := strings.Split(string(worktreeOutput), "\n")
		var currentWorktreePath string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "worktree ") {
				currentWorktreePath = strings.TrimPrefix(line, "worktree ")
			} else if strings.HasPrefix(line, "branch ") && currentWorktreePath != "" {
				// Check if the worktree path is within our managed directory
				isManaged, _ := filepath.Match(managedWorktreeBasePath+string(filepath.Separator)+"*", currentWorktreePath)
				// Also handle exact match in case the base path itself is listed (less likely)
				if currentWorktreePath == managedWorktreeBasePath || isManaged {
					branchRef := strings.TrimPrefix(line, "branch ")
					// Extract branch name from ref (e.g., refs/heads/my-branch -> my-branch)
					// Base might not be robust enough if refs aren't standard, but common case.
					branchName := filepath.Base(branchRef)
					if branchName != "." && branchName != "/" { // Basic sanity check
						managedWorktreeBranches[branchName] = struct{}{}
					}
				}
				currentWorktreePath = "" // Reset for the next block
			} else if line == "" || strings.HasPrefix(line, "detached") || strings.HasPrefix(line, "HEAD") {
				// Reset path if block ends or worktree is detached/HEAD info encountered before branch
				if line == "" {
					currentWorktreePath = ""
				}
			}
		}
	}
	log.Printf("Identified managed worktree branches for workspace %s: %v", workspaceId, managedWorktreeBranches)

	// --- Get current branch ---
	cmdCurrentBranch := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmdCurrentBranch.Dir = repoDir
	currentBranchOutput, err := cmdCurrentBranch.Output()
	currentBranchName := ""
	isDetached := false
	if err != nil {
		// Command fails in detached HEAD state, which is fine.
		log.Printf("Info: Could not get symbolic-ref HEAD in %s (likely detached HEAD): %v", repoDir, err)
		isDetached = true
	} else {
		currentBranchName = strings.TrimSpace(string(currentBranchOutput))
	}

	// --- Get default branch (main or master) ---
	defaultBranchName := ""
	for _, potentialDefault := range []string{"main", "master"} {
		cmdVerify := exec.Command("git", "rev-parse", "--verify", potentialDefault)
		cmdVerify.Dir = repoDir
		if err := cmdVerify.Run(); err == nil {
			defaultBranchName = potentialDefault
			break // Found the default
		}
	}
	if defaultBranchName == "" {
		log.Printf("Warning: Could not determine default branch (main/master) in %s", repoDir)
	}

	// --- Get all local branches, sorted ---
	// Format %(refname:short) gives just the branch name.
	cmdBranchList := exec.Command("git", "branch", "--list", "--sort=-committerdate", "--format=%(refname:short)")
	cmdBranchList.Dir = repoDir
	branchListOutput, err := cmdBranchList.Output()
	if err != nil {
		log.Printf("Error running 'git branch --list' in %s: %v", repoDir, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list branches"})
		return
	}

	allBranches := strings.Split(strings.TrimSpace(string(branchListOutput)), "\n")

	// --- Filter and format branches ---
	var filteredBranches []BranchInfo
	for _, branchName := range allBranches {
		if branchName == "" {
			continue // Skip empty lines if any
		}
		// Skip branches associated with managed worktrees
		if _, isManaged := managedWorktreeBranches[branchName]; isManaged {
			log.Printf("Filtering out managed worktree branch: %s", branchName)
			continue
		}

		isCurrent := !isDetached && (branchName == currentBranchName)
		isDefault := (defaultBranchName != "") && (branchName == defaultBranchName)

		filteredBranches = append(filteredBranches, BranchInfo{
			Name:      branchName,
			IsCurrent: isCurrent,
			IsDefault: isDefault,
		})
	}

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
