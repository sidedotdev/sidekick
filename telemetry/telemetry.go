package telemetry

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

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
	} else {
		exporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
	}
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
