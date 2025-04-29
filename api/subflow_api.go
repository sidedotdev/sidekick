package api

import (
	"errors"
	"net/http"
	"sidekick/srv"

	"github.com/gin-gonic/gin"
)

// GetSubflowHandler handles GET requests to retrieve a single subflow by ID
func (ctrl *Controller) GetSubflowHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	subflowId := c.Param("id")

	// Validate required parameters
	if workspaceId == "" || subflowId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID and subflow ID are required"})
		return
	}

	// Get the subflow from storage
	subflow, err := ctrl.service.GetSubflow(c, workspaceId, subflowId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Subflow not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get subflow"})
		}
		return
	}

	// Return the subflow data
	c.JSON(http.StatusOK, gin.H{"subflow": subflow})
}
