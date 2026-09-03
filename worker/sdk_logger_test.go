package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedLog struct {
	level string
	msg   string
}

type recordingLogger struct {
	logs []recordedLog
}

func (r *recordingLogger) Debug(msg string, keyvals ...interface{}) {
	r.logs = append(r.logs, recordedLog{"debug", msg})
}

func (r *recordingLogger) Info(msg string, keyvals ...interface{}) {
	r.logs = append(r.logs, recordedLog{"info", msg})
}

func (r *recordingLogger) Warn(msg string, keyvals ...interface{}) {
	r.logs = append(r.logs, recordedLog{"warn", msg})
}

func (r *recordingLogger) Error(msg string, keyvals ...interface{}) {
	r.logs = append(r.logs, recordedLog{"error", msg})
}

func TestWarnDowngradingLogger(t *testing.T) {
	t.Parallel()

	// verbatim prefix of the SDK warning this logger exists to silence
	changeVersionMsg := "Serialized size of TemporalChangeVersion search attribute update would " +
		"exceed the maximum value size. Skipping this upsert."

	cases := []struct {
		name      string
		log       func(l warnDowngradingLogger)
		wantLevel string
	}{
		{
			name:      "change version size warning is downgraded to debug",
			log:       func(l warnDowngradingLogger) { l.Warn(changeVersionMsg, "WorkflowID", "wf-1") },
			wantLevel: "debug",
		},
		{
			name:      "other warnings pass through",
			log:       func(l warnDowngradingLogger) { l.Warn("something else went wrong") },
			wantLevel: "warn",
		},
		{
			name:      "debug passes through",
			log:       func(l warnDowngradingLogger) { l.Debug(changeVersionMsg) },
			wantLevel: "debug",
		},
		{
			name:      "info passes through",
			log:       func(l warnDowngradingLogger) { l.Info(changeVersionMsg) },
			wantLevel: "info",
		},
		{
			name:      "error passes through even when message matches",
			log:       func(l warnDowngradingLogger) { l.Error(changeVersionMsg) },
			wantLevel: "error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := &recordingLogger{}
			tc.log(warnDowngradingLogger{inner: inner})
			require.Len(t, inner.logs, 1)
			assert.Equal(t, tc.wantLevel, inner.logs[0].level)
		})
	}
}