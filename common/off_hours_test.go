package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsOffHoursBlockedAt(t *testing.T) {
	t.Parallel()

	// Use a fixed location for consistent testing
	loc := time.FixedZone("Test", 0)

	tests := []struct {
		name          string
		cfg           OffHoursConfig
		checkTime     time.Time
		wantBlocked   bool
		wantMessage   string
		wantUnblockAt *time.Time
	}{
		{
			name:        "empty config - not blocked",
			cfg:         OffHoursConfig{},
			checkTime:   time.Date(2024, 1, 15, 10, 0, 0, 0, loc), // Monday 10:00
			wantBlocked: false,
		},
		{
			name: "same-day window - inside window",
			cfg: OffHoursConfig{
				Message: "Go to sleep!",
				Windows: []OffHoursWindow{
					{Start: "09:00", End: "17:00"},
				},
			},
			checkTime:     time.Date(2024, 1, 15, 12, 0, 0, 0, loc), // Monday 12:00
			wantBlocked:   true,
			wantMessage:   "Go to sleep!",
			wantUnblockAt: ptr(time.Date(2024, 1, 15, 17, 0, 0, 0, loc)),
		},
		{
			name: "same-day window - before window",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "09:00", End: "17:00"},
				},
			},
			checkTime:   time.Date(2024, 1, 15, 8, 0, 0, 0, loc), // Monday 08:00
			wantBlocked: false,
		},
		{
			name: "same-day window - after window",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "09:00", End: "17:00"},
				},
			},
			checkTime:   time.Date(2024, 1, 15, 18, 0, 0, 0, loc), // Monday 18:00
			wantBlocked: false,
		},
		{
			name: "same-day window - at start boundary",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "09:00", End: "17:00"},
				},
			},
			checkTime:     time.Date(2024, 1, 15, 9, 0, 0, 0, loc), // Monday 09:00
			wantBlocked:   true,
			wantMessage:   "Time to rest!",
			wantUnblockAt: ptr(time.Date(2024, 1, 15, 17, 0, 0, 0, loc)),
		},
		{
			name: "same-day window - at end boundary",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "09:00", End: "17:00"},
				},
			},
			checkTime:   time.Date(2024, 1, 15, 17, 0, 0, 0, loc), // Monday 17:00
			wantBlocked: false,
		},
		{
			name: "overnight window - after start (same day)",
			cfg: OffHoursConfig{
				Message: "Sleep time!",
				Windows: []OffHoursWindow{
					{Start: "23:00", End: "07:00"},
				},
			},
			checkTime:     time.Date(2024, 1, 15, 23, 30, 0, 0, loc), // Monday 23:30
			wantBlocked:   true,
			wantMessage:   "Sleep time!",
			wantUnblockAt: ptr(time.Date(2024, 1, 16, 7, 0, 0, 0, loc)),
		},
		{
			name: "overnight window - before end (next day)",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "23:00", End: "07:00"},
				},
			},
			checkTime:     time.Date(2024, 1, 16, 5, 0, 0, 0, loc), // Tuesday 05:00
			wantBlocked:   true,
			wantMessage:   "Time to rest!",
			wantUnblockAt: ptr(time.Date(2024, 1, 16, 7, 0, 0, 0, loc)),
		},
		{
			name: "overnight window - outside window (afternoon)",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "23:00", End: "07:00"},
				},
			},
			checkTime:   time.Date(2024, 1, 15, 14, 0, 0, 0, loc), // Monday 14:00
			wantBlocked: false,
		},
		{
			name: "overnight window - at end boundary",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "23:00", End: "07:00"},
				},
			},
			checkTime:   time.Date(2024, 1, 16, 7, 0, 0, 0, loc), // Tuesday 07:00
			wantBlocked: false,
		},
		{
			name: "day-specific window - matching day",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Days: []string{"monday", "wednesday"}, Start: "03:00", End: "07:00"},
				},
			},
			checkTime:     time.Date(2024, 1, 15, 4, 0, 0, 0, loc), // Monday 04:00
			wantBlocked:   true,
			wantMessage:   "Time to rest!",
			wantUnblockAt: ptr(time.Date(2024, 1, 15, 7, 0, 0, 0, loc)),
		},
		{
			name: "day-specific window - non-matching day",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Days: []string{"monday", "wednesday"}, Start: "03:00", End: "07:00"},
				},
			},
			checkTime:   time.Date(2024, 1, 16, 4, 0, 0, 0, loc), // Tuesday 04:00
			wantBlocked: false,
		},
		{
			name: "day-specific overnight window - morning belongs to previous day",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Days: []string{"tuesday"}, Start: "23:00", End: "07:00"},
				},
			},
			// Tuesday 05:00 should NOT be blocked because the morning portion
			// belongs to Monday's window (which isn't in Days)
			checkTime:   time.Date(2024, 1, 16, 5, 0, 0, 0, loc), // Tuesday 05:00
			wantBlocked: false,
		},
		{
			name: "day-specific overnight window - evening portion blocked",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Days: []string{"tuesday"}, Start: "23:00", End: "07:00"},
				},
			},
			// Tuesday 23:30 should be blocked (evening portion of Tuesday's window)
			checkTime:     time.Date(2024, 1, 16, 23, 30, 0, 0, loc), // Tuesday 23:30
			wantBlocked:   true,
			wantMessage:   "Time to rest!",
			wantUnblockAt: ptr(time.Date(2024, 1, 17, 7, 0, 0, 0, loc)), // Wednesday 07:00
		},
		{
			name: "day-specific overnight window - next morning blocked",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Days: []string{"tuesday"}, Start: "23:00", End: "07:00"},
				},
			},
			// Wednesday 05:00 should be blocked (morning portion of Tuesday's window)
			checkTime:     time.Date(2024, 1, 17, 5, 0, 0, 0, loc), // Wednesday 05:00
			wantBlocked:   true,
			wantMessage:   "Time to rest!",
			wantUnblockAt: ptr(time.Date(2024, 1, 17, 7, 0, 0, 0, loc)), // Wednesday 07:00
		},
		{
			name: "day-specific window - case insensitive",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Days: []string{"Monday", "WEDNESDAY"}, Start: "03:00", End: "07:00"},
				},
			},
			checkTime:     time.Date(2024, 1, 15, 4, 0, 0, 0, loc), // Monday 04:00
			wantBlocked:   true,
			wantMessage:   "Time to rest!",
			wantUnblockAt: ptr(time.Date(2024, 1, 15, 7, 0, 0, 0, loc)),
		},
		{
			name: "multiple windows - first matches",
			cfg: OffHoursConfig{
				Message: "Take a break",
				Windows: []OffHoursWindow{
					{Start: "12:00", End: "13:00"},
					{Start: "03:00", End: "07:00"},
				},
			},
			checkTime:     time.Date(2024, 1, 15, 12, 30, 0, 0, loc), // Monday 12:30
			wantBlocked:   true,
			wantMessage:   "Take a break",
			wantUnblockAt: ptr(time.Date(2024, 1, 15, 13, 0, 0, 0, loc)),
		},
		{
			name: "multiple windows - second matches",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "12:00", End: "13:00"},
					{Start: "03:00", End: "07:00"},
				},
			},
			checkTime:     time.Date(2024, 1, 15, 5, 0, 0, 0, loc), // Monday 05:00
			wantBlocked:   true,
			wantMessage:   "Time to rest!",
			wantUnblockAt: ptr(time.Date(2024, 1, 15, 7, 0, 0, 0, loc)),
		},
		{
			name: "multiple windows - none match",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "12:00", End: "13:00"},
					{Start: "03:00", End: "07:00"},
				},
			},
			checkTime:   time.Date(2024, 1, 15, 10, 0, 0, 0, loc), // Monday 10:00
			wantBlocked: false,
		},
		{
			name: "invalid time format - not blocked",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "invalid", End: "07:00"},
				},
			},
			checkTime:   time.Date(2024, 1, 15, 5, 0, 0, 0, loc),
			wantBlocked: false,
		},
		{
			name: "default message when not specified",
			cfg: OffHoursConfig{
				Windows: []OffHoursWindow{
					{Start: "03:00", End: "07:00"},
				},
			},
			checkTime:     time.Date(2024, 1, 15, 4, 0, 0, 0, loc),
			wantBlocked:   true,
			wantMessage:   "Time to rest!",
			wantUnblockAt: ptr(time.Date(2024, 1, 15, 7, 0, 0, 0, loc)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status := IsOffHoursBlockedAt(tt.checkTime, tt.cfg)

			assert.Equal(t, tt.wantBlocked, status.Blocked, "blocked mismatch")

			if tt.wantBlocked {
				assert.Equal(t, tt.wantMessage, status.Message, "message mismatch")
				require.NotNil(t, status.UnblockAt, "expected unblock time")
				assert.Equal(t, *tt.wantUnblockAt, *status.UnblockAt, "unblock time mismatch")
			} else {
				assert.Nil(t, status.UnblockAt, "expected no unblock time")
			}
		})
	}
}

func TestParseTimeOfDay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input      string
		wantHour   int
		wantMinute int
		wantErr    bool
	}{
		{"00:00", 0, 0, false},
		{"09:30", 9, 30, false},
		{"23:59", 23, 59, false},
		{"12:00", 12, 0, false},
		{"invalid", 0, 0, true},
		{"25:00", 0, 0, true},
		{"12:60", 0, 0, true},
		{"", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			hour, minute, err := parseTimeOfDay(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantHour, hour)
				assert.Equal(t, tt.wantMinute, minute)
			}
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}
