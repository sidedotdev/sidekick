package common

import (
	"github.com/rs/zerolog"
	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
)

type zerologLogger struct {
	log zerolog.Logger
}

var _ log.Logger = (*zerologLogger)(nil)

func NewZerologLogger(log zerolog.Logger) log.Logger {
	return &zerologLogger{log: log}
}

func (z *zerologLogger) Debug(msg string, tags ...tag.Tag) {
	z.log.Debug().Fields(logTagsToFields(tags)).Msg(msg)
}

func (z *zerologLogger) Info(msg string, tags ...tag.Tag) {
	z.log.Info().Fields(logTagsToFields(tags)).Msg(msg)
}

func (z *zerologLogger) Warn(msg string, tags ...tag.Tag) {
	z.log.Warn().Fields(logTagsToFields(tags)).Msg(msg)
}

func (z *zerologLogger) Error(msg string, tags ...tag.Tag) {
	z.log.Error().Fields(logTagsToFields(tags)).Msg(msg)
}

func (z *zerologLogger) Fatal(msg string, tags ...tag.Tag) {
	z.log.Fatal().Fields(logTagsToFields(tags)).Msg(msg)
}

// Implement other methods (DPanic, Panic) as Error for now
func (z *zerologLogger) DPanic(msg string, tags ...tag.Tag) {
	z.log.Error().Fields(logTagsToFields(tags)).Msg(msg)
}

func (z *zerologLogger) Panic(msg string, tags ...tag.Tag) {
	z.log.Error().Fields(logTagsToFields(tags)).Msg(msg)
}

func logTagsToFields(tags []tag.Tag) map[string]interface{} {
	fields := make(map[string]interface{}, len(tags))
	for _, t := range tags {
		fields[t.Key()] = t.Value()
	}
	return fields
}
