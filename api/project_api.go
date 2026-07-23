package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"sidekick/common"
	"sidekick/domain"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
)

type ProjectRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	Rank        string `json:"rank"`
}

func validateProjectRequest(req *ProjectRequest) (domain.ProjectPriority, error) {
	if strings.TrimSpace(req.Title) == "" {
		return "", errors.New("title is required")
	}
	if req.Priority == "" {
		return domain.ProjectPriorityNone, nil
	}
	return domain.StringToProjectPriority(req.Priority)
}

func (ctrl *Controller) CreateProjectHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	if strings.TrimSpace(workspaceId) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID is required"})
		return
	}

	var req ProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	priority, err := validateProjectRequest(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	now := time.Now().UTC()
	project := domain.Project{
		WorkspaceId: workspaceId,
		Id:          "project_" + ksuid.New().String(),
		Title:       strings.TrimSpace(req.Title),
		Description: req.Description,
		Priority:    priority,
		Rank:        req.Rank,
		Created:     now,
		Updated:     now,
	}

	if err := ctrl.service.PersistProject(c.Request.Context(), project); err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to persist project")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create project"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"project": project})
}

func (ctrl *Controller) GetProjectsHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	if strings.TrimSpace(workspaceId) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID is required"})
		return
	}

	projects, err := ctrl.service.GetProjects(c.Request.Context(), workspaceId)
	if err != nil {
		log.Error().Err(err).Str("workspaceId", workspaceId).Msg("Failed to fetch projects")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch projects"})
		return
	}
	if projects == nil {
		projects = []domain.Project{}
	}

	c.JSON(http.StatusOK, gin.H{"projects": projects})
}

func (ctrl *Controller) UpdateProjectHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	if strings.TrimSpace(workspaceId) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID is required"})
		return
	}
	projectId := c.Param("id")

	var req ProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}
	priority, err := validateProjectRequest(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	project, err := ctrl.service.GetProject(c.Request.Context(), workspaceId, projectId)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
			return
		}
		log.Error().Err(err).Str("projectId", projectId).Msg("Failed to fetch project")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch project"})
		return
	}

	project.Title = strings.TrimSpace(req.Title)
	project.Description = req.Description
	project.Priority = priority
	project.Rank = req.Rank
	project.Updated = time.Now().UTC()

	if err := ctrl.service.PersistProject(c.Request.Context(), project); err != nil {
		log.Error().Err(err).Str("projectId", projectId).Msg("Failed to persist project")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"project": project})
}

func (ctrl *Controller) DeleteProjectHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	if strings.TrimSpace(workspaceId) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID is required"})
		return
	}
	projectId := c.Param("id")

	err := ctrl.service.DeleteProject(c.Request.Context(), workspaceId, projectId)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
			return
		}
		log.Error().Err(err).Str("projectId", projectId).Msg("Failed to delete project")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete project"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Project deleted successfully"})
}
