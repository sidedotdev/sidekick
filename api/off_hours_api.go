package api

import (
	"net/http"
	"time"

	"sidekick/common"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// OffHoursResponse is the JSON response for the off-hours endpoint.
type OffHoursResponse struct {
	Enabled   bool                    `json:"enabled"`
	Blocked   bool                    `json:"blocked"`
	UnblockAt *time.Time              `json:"unblockAt,omitempty"`
	Message   string                  `json:"message,omitempty"`
	Windows   []common.OffHoursWindow `json:"windows,omitempty"`
}

func (ctrl *Controller) GetOffHoursHandler(c *gin.Context) {
	config, err := common.LoadSidekickConfig(common.GetSidekickConfigPath())
	if err != nil {
		log.Warn().Err(err).Msg("Failed to load sidekick config for off-hours")
		c.JSON(http.StatusOK, OffHoursResponse{Enabled: false})
		return
	}

	offHours := config.OffHours
	if len(offHours.Windows) == 0 {
		c.JSON(http.StatusOK, OffHoursResponse{Enabled: false})
		return
	}

	status := common.IsOffHoursBlockedAt(time.Now(), offHours)

	c.JSON(http.StatusOK, OffHoursResponse{
		Enabled:   true,
		Blocked:   status.Blocked,
		UnblockAt: status.UnblockAt,
		Message:   status.Message,
		Windows:   offHours.Windows,
	})
}
