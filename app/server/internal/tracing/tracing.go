// File: app/server/internal/tracing/tracing.go
package tracing

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.22.0" // Ensure this version matches your otel version
)

// InitTracer sets up the OTLP exporter for Logfire using HTTP/protobuf.
// It returns a function that should be deferred to shutdown the tracer provider.
func InitTracer(serviceName string) (func(context.Context) error, error) {
	ctx := context.Background()

	logfireToken := os.Getenv("LOGFIRE_TOKEN")
	if logfireToken == "" {
		return nil, errors.New("LOGFIRE_TOKEN environment variable not set")
	}
	log.Println("[TRACING] LOGFIRE_TOKEN found.")

	otelExporterOTLPEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otelExporterOTLPEndpoint == "" {
		// Default Logfire endpoint for OTLP/HTTP
		otelExporterOTLPEndpoint = "https://logfire-api.pydantic.dev" // Updated default
		log.Printf("[TRACING] OTEL_EXPORTER_OTLP_ENDPOINT not set, using default: %s", otelExporterOTLPEndpoint)
	}

	// Logfire expects OTLP/HTTP with protobuf. The otlptracehttp exporter handles this.
	// The protocol is usually inferred or part of the endpoint.
	// No explicit "OTEL_EXPORTER_OTLP_PROTOCOL" needed for otlptracehttp if endpoint is correct.

	// Configure resource attributes (service.name, etc.)
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
		semconv.ServiceVersionKey.String("0.1.0"), // Replace with your app's version
	)
	log.Printf("[TRACING] OpenTelemetry resource configured for service: %s", serviceName)

	// Configure OTLP HTTP exporter options
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(strings.TrimPrefix(otelExporterOTLPEndpoint, "https://")), // e.g., "logfire-api.pydantic.dev" or "logfire-api.pydantic.dev:443"
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", logfireToken), // Standard Bearer token
			"Content-Type":  "application/x-protobuf",               // Logfire expects protobuf
		}),
	}

	// If your endpoint doesn't explicitly use https and isn't port 443,
	// and you are NOT using a secure connection (e.g. local testing without TLS),
	// you might need WithInsecure. For Logfire's cloud endpoint, this is not needed.
	if !strings.HasPrefix(otelExporterOTLPEndpoint, "https://") && !strings.Contains(otelExporterOTLPEndpoint, ":443") {
		// Check if it's a known local/dev endpoint before automatically setting insecure
		if strings.HasPrefix(otelExporterOTLPEndpoint, "localhost") || strings.HasPrefix(otelExporterOTLPEndpoint, "127.0.0.1") {
			log.Println("[TRACING] Using insecure connection for OTLP HTTP exporter (local development).")
			opts = append(opts, otlptracehttp.WithInsecure())
		}
	}

	log.Printf("[TRACING] Configuring OTLP HTTP exporter for Logfire at: %s", otelExporterOTLPEndpoint)
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP HTTP exporter: %w", err)
	}
	log.Println("[TRACING] OTLP HTTP exporter created successfully.")

	// Configure TracerProvider with batching
	// A BatchSpanProcessor is generally recommended for production.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithSampler(sdktrace.AlwaysSample()), // Good for debugging, consider ParentBased(TraceIDRatio(0.1)) for production
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// Set up global propagator to W3C Trace Context (standard for inter-service propagation)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	log.Println("[TRACING] TracerProvider configured and set globally.")

	// Return the shutdown function for the TracerProvider
	return func(shutdownCtx context.Context) error {
		log.Println("[TRACING] Shutting down TracerProvider...")
		// Attempt to flush all batched spans.
		if err := tp.ForceFlush(shutdownCtx); err != nil {
			log.Printf("[TRACING] Error flushing TracerProvider: %v", err)
			// Still attempt shutdown
		}
		if err := tp.Shutdown(shutdownCtx); err != nil {
			log.Printf("[TRACING] Error shutting down TracerProvider: %v", err)
			return fmt.Errorf("failed to shutdown TracerProvider: %w", err)
		}
		log.Println("[TRACING] TracerProvider shut down successfully.")
		return nil
	}, nil
}
