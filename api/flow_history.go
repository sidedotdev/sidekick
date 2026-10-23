package api

import (
	"errors"
	"net/http"
	"strconv"

	"sidekick/domain"
	"sidekick/srv"

	"github.com/gin-gonic/gin"
)

func historyPageRequest(c *gin.Context) (domain.HistoryPageRequest, bool, error) {
	limit, hasLimit := c.GetQuery("limit")
	before, hasBefore := c.GetQuery("before")
	if !hasLimit && !hasBefore {
		return domain.HistoryPageRequest{}, false, nil
	}
	size, err := strconv.Atoi(limit)
	if err != nil || size < 1 || size > domain.MaxHistoryPageSize {
		return domain.HistoryPageRequest{}, true, domain.ErrInvalidHistoryPage
	}
	return domain.HistoryPageRequest{Limit: size, Before: before}, true, nil
}

func (ctrl *Controller) getFlowActionsPage(c *gin.Context, workspaceID, flowID string, page domain.HistoryPageRequest) {
	result, err := ctrl.service.GetFlowActionsPage(c.Request.Context(), workspaceID, flowID, page)
	if errors.Is(err, domain.ErrInvalidHistoryPage) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get flow actions"})
		return
	}
	if len(result.FlowActions) == 0 {
		if _, err := ctrl.service.GetFlow(c.Request.Context(), workspaceID, flowID); err != nil {
			if errors.Is(err, srv.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "Flow not found"})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get flow"})
			}
			return
		}
	}
	c.JSON(http.StatusOK, result)
}

func (ctrl *Controller) getSubflowsPage(c *gin.Context, workspaceID, flowID string, page domain.HistoryPageRequest) {
	result, err := ctrl.service.GetSubflowsPage(c.Request.Context(), workspaceID, flowID, page)
	if errors.Is(err, domain.ErrInvalidHistoryPage) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get subflows"})
		return
	}
	c.JSON(http.StatusOK, result)
}
