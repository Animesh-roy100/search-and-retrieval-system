// Package tracing wires OpenTelemetry tracing for the Go services. Init returns a
// shutdown func and is a graceful no-op when OTEL_EXPORTER_OTLP_ENDPOINT is unset,
// so the stack runs fine without a trace backend. Uses the W3C tracecontext
// propagator so spans stitch across HTTP (query -> ML) and Kafka into one trace.
package tracing

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Init sets the global tracer provider + propagator for a service. The returned
// shutdown func flushes pending spans; call it on exit. When no OTLP endpoint is
// configured it installs the W3C propagator only, so tracing calls are cheap no-ops.
func Init(ctx context.Context, service string) (func(context.Context) error, error) {
	// Propagator is always installed so context flows even without an exporter.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracegrpc.New(ctx) // reads OTEL_EXPORTER_OTLP_* env (endpoint, insecure)
	if err != nil {
		return nil, err
	}
	res, _ := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(service)))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(2*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Tracer returns a named tracer from the global provider.
func Tracer(name string) trace.Tracer { return otel.Tracer(name) }
