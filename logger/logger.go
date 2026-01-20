package logger

import (
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sidekick/common"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/pkgerrors"
)

var once sync.Once

var log zerolog.Logger

func GetLogLevel() zerolog.Level {
	logLevel, err := strconv.Atoi(os.Getenv("SIDE_LOG_LEVEL"))
	if err != nil {
		logLevel = int(zerolog.InfoLevel) // default to INFO
	}

	return zerolog.Level(logLevel)
}

func Get() zerolog.Logger {
	once.Do(func() {
		zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack
		zerolog.TimeFieldFormat = time.RFC3339Nano

		consoleWriter := zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		}

		var output io.Writer = consoleWriter

		stateHome, err := common.GetSidekickStateHome()
		if err == nil {
			fileWriter, err := newDailyRotatingLogWriter(stateHome)
			if err == nil {
				output = zerolog.MultiLevelWriter(consoleWriter, fileWriter)
			}
		}

		var gitRevision string
		buildInfo, ok := debug.ReadBuildInfo()
		if ok {
			for _, v := range buildInfo.Settings {
				if v.Key == "vcs.revision" {
					gitRevision = v.Value
					break
				}
			}
		}

		log = zerolog.New(output).
			Level(zerolog.Level(GetLogLevel())).
			With().
			Timestamp().
			Str("git_revision", gitRevision).
			Str("go_version", buildInfo.GoVersion).
			Logger()
	})

	return log
}

const (
	logFilePrefix   = "sidekick-"
	logFileSuffix   = ".log"
	maxLogFileCount = 7
)

type dailyRotatingLogWriter struct {
	mu          sync.Mutex
	stateHome   string
	currentDate string
	file        *os.File
}

func newDailyRotatingLogWriter(stateHome string) (*dailyRotatingLogWriter, error) {
	w := &dailyRotatingLogWriter{
		stateHome: stateHome,
	}
	if err := w.rotateIfNeeded(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dailyRotatingLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.rotateIfNeeded(); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

func (w *dailyRotatingLogWriter) rotateIfNeeded() error {
	today := time.Now().Format("2006-01-02")
	if w.currentDate == today && w.file != nil {
		return nil
	}

	if w.file != nil {
		w.file.Close()
	}

	logFileName := logFilePrefix + today + logFileSuffix
	file, err := os.OpenFile(
		filepath.Join(w.stateHome, logFileName),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)
	if err != nil {
		return err
	}

	w.file = file
	w.currentDate = today

	cleanupOldLogFiles(w.stateHome)

	return nil
}

func (w *dailyRotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

var _ io.WriteCloser = (*dailyRotatingLogWriter)(nil)

func cleanupOldLogFiles(stateHome string) {
	entries, err := os.ReadDir(stateHome)
	if err != nil {
		return
	}

	var logFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, logFilePrefix) && strings.HasSuffix(name, logFileSuffix) {
			logFiles = append(logFiles, name)
		}
	}

	if len(logFiles) <= maxLogFileCount {
		return
	}

	sort.Strings(logFiles)

	for i := 0; i < len(logFiles)-maxLogFileCount; i++ {
		os.Remove(filepath.Join(stateHome, logFiles[i]))
	}
}
