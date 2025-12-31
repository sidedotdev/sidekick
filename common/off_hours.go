package common

import (
	"fmt"
	"strings"
	"time"
)

// OffHoursConfig configures optional time-based blocking of sidekick usage.
// A slightly humorous name for when you should be sleeping instead.
type OffHoursConfig struct {
	// Message to display when blocked. Defaults to "Time to rest!" if empty.
	Message string `koanf:"message,omitempty"`
	// Windows defines the time windows during which sidekick is blocked.
	Windows []OffHoursWindow `koanf:"windows,omitempty"`
}

// OffHoursWindow defines a recurring time window for blocking.
type OffHoursWindow struct {
	// Days specifies which days of the week this window applies to.
	// Use lowercase day names: "monday", "tuesday", etc.
	// If empty, applies to all days.
	Days []string `koanf:"days,omitempty" json:"days,omitempty"`
	// Start is the start time in "HH:MM" 24-hour format (local time).
	Start string `koanf:"start" json:"start"`
	// End is the end time in "HH:MM" 24-hour format (local time).
	// If End < Start, the window crosses midnight.
	End string `koanf:"end" json:"end"`
}

// OffHoursStatus represents the current blocking status.
type OffHoursStatus struct {
	Blocked   bool       `json:"blocked"`
	UnblockAt *time.Time `json:"unblock_at,omitempty"`
	Message   string     `json:"message,omitempty"`
}

var dayNameToWeekday = map[string]time.Weekday{
	"sunday":    time.Sunday,
	"monday":    time.Monday,
	"tuesday":   time.Tuesday,
	"wednesday": time.Wednesday,
	"thursday":  time.Thursday,
	"friday":    time.Friday,
	"saturday":  time.Saturday,
}

// IsOffHoursBlockedAt checks if the given time falls within any configured off-hours window.
func IsOffHoursBlockedAt(t time.Time, cfg OffHoursConfig) OffHoursStatus {
	if len(cfg.Windows) == 0 {
		return OffHoursStatus{Blocked: false}
	}

	message := cfg.Message
	if message == "" {
		message = "Time to rest!"
	}

	for _, window := range cfg.Windows {
		blocked, unblockAt := isTimeInWindow(t, window)
		if blocked {
			return OffHoursStatus{
				Blocked:   true,
				UnblockAt: unblockAt,
				Message:   message,
			}
		}
	}

	return OffHoursStatus{Blocked: false}
}

// parseTimeOfDay parses a "HH:MM" string into hour and minute.
func parseTimeOfDay(s string) (hour, minute int, err error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time format: %s", s)
	}
	_, err = fmt.Sscanf(s, "%d:%d", &hour, &minute)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid time format: %s", s)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid time values: %s", s)
	}
	return hour, minute, nil
}

// timeOfDayMinutes converts hour:minute to minutes since midnight.
func timeOfDayMinutes(hour, minute int) int {
	return hour*60 + minute
}

// isTimeInWindow checks if time t falls within the given window.
// Returns whether blocked and when the block ends.
func isTimeInWindow(t time.Time, window OffHoursWindow) (blocked bool, unblockAt *time.Time) {
	startH, startM, err1 := parseTimeOfDay(window.Start)
	endH, endM, err2 := parseTimeOfDay(window.End)
	if err1 != nil || err2 != nil {
		return false, nil
	}

	startMins := timeOfDayMinutes(startH, startM)
	endMins := timeOfDayMinutes(endH, endM)
	currentMins := timeOfDayMinutes(t.Hour(), t.Minute())
	isOvernightWindow := endMins < startMins

	// Check day restrictions
	if len(window.Days) > 0 {
		currentDay := strings.ToLower(t.Weekday().String())
		yesterdayName := strings.ToLower(t.AddDate(0, 0, -1).Weekday().String())

		currentDayMatches := containsDay(window.Days, currentDay)
		yesterdayMatches := containsDay(window.Days, yesterdayName)

		if isOvernightWindow {
			// For overnight windows with day restrictions:
			// - Evening portion (after start): check if current day matches
			// - Morning portion (before end): check if yesterday matches
			inMorningPortion := currentMins < endMins
			inEveningPortion := currentMins >= startMins

			if inMorningPortion && yesterdayMatches {
				unblock := time.Date(t.Year(), t.Month(), t.Day(), endH, endM, 0, 0, t.Location())
				return true, &unblock
			}
			if inEveningPortion && currentDayMatches {
				unblock := time.Date(t.Year(), t.Month(), t.Day()+1, endH, endM, 0, 0, t.Location())
				return true, &unblock
			}
			return false, nil
		}

		// Same-day window: just check current day
		if !currentDayMatches {
			return false, nil
		}
	}

	// No day restrictions or same-day window with matching day
	if !isOvernightWindow {
		// Same-day window (e.g., 09:00 to 17:00)
		if currentMins >= startMins && currentMins < endMins {
			unblock := time.Date(t.Year(), t.Month(), t.Day(), endH, endM, 0, 0, t.Location())
			return true, &unblock
		}
	} else {
		// Overnight window without day restrictions (e.g., 23:00 to 07:00)
		if currentMins >= startMins {
			unblock := time.Date(t.Year(), t.Month(), t.Day()+1, endH, endM, 0, 0, t.Location())
			return true, &unblock
		} else if currentMins < endMins {
			unblock := time.Date(t.Year(), t.Month(), t.Day(), endH, endM, 0, 0, t.Location())
			return true, &unblock
		}
	}

	return false, nil
}

// containsDay checks if the days slice contains the given day (case-insensitive).
func containsDay(days []string, day string) bool {
	for _, d := range days {
		if strings.ToLower(d) == day {
			return true
		}
	}
	return false
}
