package worker

import (
	"strings"

	sdklog "go.temporal.io/sdk/log"
)

// changeVersionSizeWarning matches the warning the Go SDK emits when
// GetVersion skips upserting the TemporalChangeVersion search attribute
// because its serialized size exceeds the SDK's hardcoded 2 KiB guard (see
// go.temporal.io/sdk issue #1052). Sidekick never queries workflows by change
// version, so the lost visibility record is harmless and the warning is just
// noise in long-lived workflows with many GetVersion patches.
const changeVersionSizeWarning = "search attribute update would exceed the maximum value size"

// warnDowngradingLogger downgrades known-harmless Temporal SDK warnings to
// debug level and passes everything else through unchanged.
//
// It deliberately does not embed the inner logger: if the inner logger
// implements the SDK's log.WithLogger, method promotion would let log.With
// return a derived inner logger that bypasses this filter. By implementing
// only log.Logger, the SDK stacks its own with-logger on top, keeping this
// filter in the path for workflow-tagged loggers too.
type warnDowngradingLogger struct {
	inner sdklog.Logger
}

var _ sdklog.Logger = warnDowngradingLogger{}

func (l warnDowngradingLogger) Debug(msg string, keyvals ...interface{}) {
	l.inner.Debug(msg, keyvals...)
}

func (l warnDowngradingLogger) Info(msg string, keyvals ...interface{}) {
	l.inner.Info(msg, keyvals...)
}

func (l warnDowngradingLogger) Warn(msg string, keyvals ...interface{}) {
	if strings.Contains(msg, changeVersionSizeWarning) {
		l.inner.Debug(msg, keyvals...)
		return
	}
	l.inner.Warn(msg, keyvals...)
}

func (l warnDowngradingLogger) Error(msg string, keyvals ...interface{}) {
	l.inner.Error(msg, keyvals...)
}
