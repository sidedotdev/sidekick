package tui

import (
	"sidekick/common"
	"time"
)

// OffHoursStatus represents the current off-hours blocking status for the TUI.
type OffHoursStatus struct {
	Blocked   bool
	UnblockAt *time.Time
	Message   string
}

// CheckOffHours loads the local config and checks if we're currently in off-hours.
func CheckOffHours() OffHoursStatus {
	return CheckOffHoursAt(time.Now())
}

// CheckOffHoursAt checks if the given time falls within configured off-hours.
func CheckOffHoursAt(t time.Time) OffHoursStatus {
	configPath := common.GetSidekickConfigPath()
	config, err := common.LoadSidekickConfig(configPath)
	if err != nil {
		return OffHoursStatus{Blocked: false}
	}

	status := common.IsOffHoursBlockedAt(t, config.OffHours)
	return OffHoursStatus{
		Blocked:   status.Blocked,
		UnblockAt: status.UnblockAt,
		Message:   status.Message,
	}
}
