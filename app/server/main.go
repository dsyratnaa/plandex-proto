package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"plandex-server/internal/tracing"
	"plandex-server/routes"
	"plandex-server/setup"
	"time"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	log.Println("--- RUNNING LATEST BUILD ---")
	// Configure the default logger to include milliseconds in timestamps
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	// --- Initialize OpenTelemetry Tracer ---
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "plandex-server" // Default service name for the server
	}

	shutdownTracer, err := tracing.InitTracer(serviceName)
	if err != nil {
		log.Fatalf("❌ Failed to initialize OpenTelemetry tracer: %v", err)
	}
	// Defer shutdown with a timeout context
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // Give 10s for shutdown
		defer cancel()
		log.Println("[MAIN] Attempting to shut down tracer provider...")
		if err := shutdownTracer(ctx); err != nil {
			log.Printf("⚠️ Error shutting down tracer provider: %v", err)
		} else {
			log.Println("[MAIN] Tracer provider shut down successfully.")
		}
	}()

	log.Println("🚀 Plandex Server starting with tracing enabled...")

	routes.RegisterHandlePlandex(func(router *mux.Router, path string, isStreaming bool, handler routes.PlandexHandler) *mux.Route {
		return router.HandleFunc(path, handler)
	})

	r := mux.NewRouter()

	// Add a simple health endpoint with tracing demonstration
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// The context r.Context() will already have trace info from otelhttp
		span := trace.SpanFromContext(r.Context())
		log.Printf("[HANDLER /health] TraceID: %s", span.SpanContext().TraceID().String())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	routes.AddHealthRoutes(r)
	routes.AddApiRoutes(r)
	routes.AddProxyableApiRoutes(r)
	setup.MustLoadIp()
	setup.MustInitDb()

	// --- Wrap the main router with OTel HTTP instrumentation ---
	// The second argument to NewHandler is the span name for incoming requests.
	instrumentedHandler := otelhttp.NewHandler(r, "http.server.request")

	setup.StartServer(instrumentedHandler, nil, nil)
	os.Exit(0)
}
