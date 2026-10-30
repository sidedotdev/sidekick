package jetstream

import "github.com/rs/zerolog"

type storageTestLogger struct {
	log zerolog.Logger
}

func (l storageTestLogger) Noticef(format string, args ...any) {
	l.log.Info().Msgf(format, args...)
}

func (l storageTestLogger) Warnf(format string, args ...any) {
	l.log.Warn().Msgf(format, args...)
}

func (l storageTestLogger) Fatalf(format string, args ...any) {
	l.log.Error().Msgf(format, args...)
}

func (l storageTestLogger) Errorf(format string, args ...any) {
	l.log.Error().Msgf(format, args...)
}

func (l storageTestLogger) Debugf(format string, args ...any) {
	l.log.Debug().Msgf(format, args...)
}

func (l storageTestLogger) Tracef(format string, args ...any) {
	l.log.Trace().Msgf(format, args...)
}
