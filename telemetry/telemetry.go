package telemetry

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sidekick/common"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const (
	traceFilePrefix   = "traces-"
	traceFileSuffix   = ".json"
	maxTraceFileCount = 7
)

// dailyRotatingWriter is an io.Writer that rotates to a new file each day
type dailyRotatingWriter struct {
	mu          sync.Mutex
	stateHome   string
	currentDate string
	file        *os.File
}

func newDailyRotatingWriter(stateHome string) (*dailyRotatingWriter, error) {
	w := &dailyRotatingWriter{
		stateHome: stateHome,
	}
	if err := w.rotateIfNeeded(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dailyRotatingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.rotateIfNeeded(); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

func (w *dailyRotatingWriter) rotateIfNeeded() error {
	today := time.Now().Format("2006-01-02")
	if w.currentDate == today && w.file != nil {
		return nil
	}

	if w.file != nil {
		w.file.Close()
	}

	traceFileName := traceFilePrefix + today + traceFileSuffix
	file, err := os.OpenFile(
		filepath.Join(w.stateHome, traceFileName),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0644,
	)
	if err != nil {
		return err
	}

	w.file = file
	w.currentDate = today

	cleanupOldTraceFiles(w.stateHome)

	return nil
}

func (w *dailyRotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

var _ io.WriteCloser = (*dailyRotatingWriter)(nil)

func GetOtelEnabled() bool {
	val := os.Getenv("SIDE_OTEL_ENABLED")
	if val == "" {
		return true
	}
	lower := strings.ToLower(val)
	return lower != "false" && lower != "0"
}

func GetOtelEndpoint() string {
	return os.Getenv("SIDE_OTEL_ENDPOINT")
}

func IsEnabled() bool {
	return GetOtelEnabled()
}

func InitTracer(serviceName string) (func(context.Context) error, error) {
	if !IsEnabled() {
		return func(ctx context.Context) error { return nil }, nil
	}

	ctx := context.Background()

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	var exporter sdktrace.SpanExporter
	endpoint := GetOtelEndpoint()
	if endpoint != "" {
		exporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, err
		}
	} else {
		stateHome, err := common.GetSidekickStateHome()
		if err != nil {
			return nil, err
		}

		rotatingWriter, err := newDailyRotatingWriter(stateHome)
		if err != nil {
			return nil, err
		}

		exporter, err = stdouttrace.New(
			stdouttrace.WithPrettyPrint(),
			stdouttrace.WithWriter(rotatingWriter),
		)
		if err != nil {
			rotatingWriter.Close()
			return nil, err
		}
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// cleanupOldTraceFiles removes old trace files, keeping only the most recent maxTraceFileCount files
func cleanupOldTraceFiles(stateHome string) {
	entries, err := os.ReadDir(stateHome)
	if err != nil {
		return
	}

	var traceFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, traceFilePrefix) && strings.HasSuffix(name, traceFileSuffix) {
			traceFiles = append(traceFiles, name)
		}
	}

	if len(traceFiles) <= maxTraceFileCount {
		return
	}

	// Sort by filename (which includes date, so alphabetical order = chronological order)
	sort.Strings(traceFiles)

	// Remove oldest files, keeping only the most recent maxTraceFileCount
	for i := 0; i < len(traceFiles)-maxTraceFileCount; i++ {
		os.Remove(filepath.Join(stateHome, traceFiles[i]))
	}
}
