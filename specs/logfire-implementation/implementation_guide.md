
Here's a detailed plan to instrument your Plandex Go server with OpenTelemetry for Logfire, broken down into phases:

**Overall Goal:** Instrument the Plandex server to send traces to Logfire, focusing on HTTP request ingress, key service logic, and outgoing LLM API calls.

**Prerequisites:**

*   Your Plandex Go server codebase.
*   Go programming environment set up.
*   Access to your Logfire account and a **Source Token** (Write Token).
*   Basic understanding of Go's `context` package.

---

**Phase 0: Preparation & Setup**

**Instructions:**

1.  **Create a New Git Branch:**
    *   Start from your main development branch (e.g., `main` or `develop`).
    *   Create a new branch for this work:
        ```bash
        git checkout -b feature/opentelemetry-instrumentation
        ```

2.  **Verify/Install Go Dependencies:**
    *   Ensure your main server project's `go.mod` file (likely at the root of your `plandex-server` module or your monorepo root if `app/server` is part of a larger module) includes the necessary OpenTelemetry packages. If not, you can add them by temporarily importing them in a `.go` file and running `go mod tidy`, or using `go get`.
    *   Required packages (versions should be compatible; check the latest stable versions if these are old):
        *   `go.opentelemetry.io/otel`
        *   `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`
        *   `go.opentelemetry.io/otel/sdk`
        *   `go.opentelemetry.io/otel/propagation`
        *   `go.opentelemetry.io/otel/trace`
        *   `go.opentelemetry.io/otel/semconv/v1.22.0` (or matching your `otel` version)
        *   `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` (for HTTP server instrumentation)
    *   Run `go mod tidy` after ensuring these are imported or `go.mod` is updated.

3.  **Set Environment Variables (for your local development):**
    *   Create a `.env` file in your project root (and add it to `.gitignore`) or set these in your shell environment:
        ```bash
        export LOGFIRE_TOKEN="YOUR_ACTUAL_LOGFIRE_WRITE_TOKEN"
        export OTEL_SERVICE_NAME="plandex-server"
        export OTEL_EXPORTER_OTLP_ENDPOINT="https://logfire-api.pydantic.dev" # Logfire's OTLP HTTP endpoint
        export OTEL_EXPORTER_OTLP_PROTOCOL="http/protobuf"
        # Optional: For more detailed OTel SDK logs (for debugging the SDK itself)
        # export OTEL_LOG_LEVEL="debug"
        ```
    *   **Important:** Replace `"YOUR_ACTUAL_LOGFIRE_WRITE_TOKEN"` with your actual token from Logfire.

4.  **Create a Tracing Initialization Package:**
    *   Create a new directory, for example, `app/server/internal/tracing`.
    *   Inside this directory, create a file named `tracing.go`.
    *   Copy the `InitTracer` function (provided in our previous discussion and refined below) into `app/server/internal/tracing/tracing.go`.

    ```go
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
    		otelExporterOTLPEndpoint = "https://in.logfire.dev" // Updated default
    		log.Printf("[TRACING] OTEL_EXPORTER_OTLP_ENDPOINT not set, using default: %s", otelExporterOTLPEndpoint)
    	}

    	// Logfire expects OTLP/HTTP with protobuf. The otlptracehttp exporter handles this.
    	// The protocol is usually inferred or part of the endpoint.
    	// No explicit "OTEL_EXPORTER_OTLP_PROTOCOL" needed for otlptracehttp if endpoint is correct.

    	// Configure resource attributes (service.name, etc.)
    	res, err := resource.Merge(
    		resource.Default(),
    		resource.NewWithAttributes(
    			semconv.SchemaURL,
    			semconv.ServiceNameKey.String(serviceName),
    			semconv.ServiceVersionKey.String("0.1.0"), // Replace with your app's version
    		),
    	)
    	if err != nil {
    		return nil, fmt.Errorf("failed to create resource: %w", err)
    	}
    	log.Printf("[TRACING] OpenTelemetry resource configured for service: %s", serviceName)

    	// Configure OTLP HTTP exporter options
    	opts := []otlptracehttp.Option{
    		otlptracehttp.WithEndpoint(otelExporterOTLPEndpoint), // e.g., "in.logfire.dev" or "in.logfire.dev:443"
    		otlptracehttp.WithHeaders(map[string]string{
    			"Authorization": fmt.Sprintf("Bearer %s", logfireToken), // Standard Bearer token
    			"Content-Type":  "application/x-protobuf",              // Logfire expects protobuf
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
    	bsp := sdktrace.NewBatchSpanProcessor(exporter)
    	tp := sdktrace.NewTracerProvider(
    		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)), // Or use bsp directly
    		// sdktrace.WithSpanProcessor(bsp), // Alternative way to add BatchSpanProcessor
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
    ```

**Testing Phase 0:**

*   **How it works:** This phase doesn't produce traces yet. It sets up the foundation.
*   **Output:**
    *   Your `go.mod` file should be updated with OpenTelemetry dependencies.
    *   The `app/server/internal/tracing/tracing.go` file should exist with the `InitTracer` function.
    *   The environment variables should be set in your shell or `.env` file.
*   **Verification:**
    *   Run `go mod tidy` from your server's module root. It should complete without errors.
    *   Try to build your server (`go build ./...` from the server module root). It should compile without errors related to the new tracing package.

---

**Phase 1: Initialize Tracer in `main()` and Instrument HTTP Ingress**

**Instructions:**

1.  **Modify Your Server's `main()` Function:**
    *   Locate your server's main entry point (e.g., `app/server/main.go` or wherever your `func main()` is).
    *   Import the `tracing` package you created and other necessary OTel packages.
    *   Call `tracing.InitTracer()` early in `main()`.
    *   Defer the shutdown function returned by `InitTracer()`.
    *   Wrap your main HTTP handler with `otelhttp.NewHandler`.

    ```go
    // Example: app/server/main.go (adapt to your actual main file)
    package main

    import (
    	"context"
    	"log"
    	"net/http"
    	"os"
    	"os/signal"
    	"syscall"
    	"time"

    	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
    	// Adjust this import path to where your tracing.go file is
    	"your_module_path/app/server/internal/tracing"
    	// ... your other server imports (e.g., for your router, handlers)
    )

    func main() {
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

    	// --- Setup Your HTTP Router and Handlers ---
    	// This is an example. Replace with your actual router setup.
    	mux := http.NewServeMux()
    	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
    		// The context r.Context() will already have trace info from otelhttp
    		log.Printf("[HANDLER /health] TraceID: %s", trace.SpanFromContext(r.Context()).SpanContext().TraceID().String())
    		w.WriteHeader(http.StatusOK)
    		w.Write([]byte("OK"))
    	})
    	// Add your other handlers to 'mux'
    	// For example, if you have a handler for "/api/v1/tell":
    	// mux.HandleFunc("/api/v1/tell", yourTellHandler)


    	// --- Wrap the main router with OTel HTTP instrumentation ---
    	// The second argument to NewHandler is the span name for incoming requests.
    	instrumentedHandler := otelhttp.NewHandler(mux, "http.server.request")

    	server := &http.Server{
    		Addr:    ":8080", // Or your configured port
    		Handler: instrumentedHandler,
    	}

    	// --- Graceful Shutdown Handling (Recommended) ---
    	go func() {
    		log.Printf("Server listening on %s", server.Addr)
    		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
    			log.Fatalf("Could not listen on %s: %v\n", server.Addr, err)
    		}
    	}()

    	stop := make(chan os.Signal, 1)
    	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
    	<-stop

    	log.Println("Shutting down server...")

    	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second) // Give 15s for server to shutdown
    	defer cancelShutdown()

    	if err := server.Shutdown(shutdownCtx); err != nil {
    		log.Fatalf("Server Shutdown Failed:%+v", err)
    	}
    	log.Println("Server gracefully stopped.")
    	// The deferred tracer shutdown will be called after this.
    }
    ```

**Testing Phase 1:**

*   **How it works:**
    *   When the server starts, `InitTracer` configures OpenTelemetry to send traces to Logfire.
    *   The `otelhttp.NewHandler` wraps your HTTP router. For every incoming HTTP request, it automatically:
        *   Checks for incoming trace context headers (e.g., from another instrumented service or a browser extension).
        *   Starts a new root span (or a child span if trace context was propagated).
        *   Adds standard HTTP attributes to the span (method, URL, status code on response, etc.).
        *   Injects the active span's context into the `r.Context()` of the `http.Request` object that your handlers receive.
*   **Output:**
    *   Your server should start, and you'll see `[TRACING]` log lines in your console (and log file if you also implemented Phase 0 of the file logging setup).
    *   When you make an HTTP request to your server (e.g., to `/health` or any other endpoint), a trace should be generated.
*   **Verification:**
    1.  Start your server: `go run app/server/main.go` (or your server's main).
    2.  Make an HTTP request to one of its endpoints (e.g., using `curl http://localhost:8080/health` or by using the Plandex CLI if it calls this server).
    3.  Go to your Logfire dashboard.
    4.  You should see a new trace for the service "plandex-server".
    5.  The trace should contain one span named "http.server.request" (or whatever you named it in `otelhttp.NewHandler`).
    6.  This span should have attributes like `http.method`, `http.target`, `http.status_code`.
    7.  The `[HANDLER /health]` log line (if you hit that endpoint) should show a valid TraceID.

**If Phase 1 is successful, commit your changes:**
```bash
git add .
git commit -m "Phase 1: Initialize OTel tracer and instrument HTTP ingress"
```

---

**Phase 2: Instrument Key Service Logic (e.g., `plan.Tell`)**

**Instructions:**

1.  **Identify Key Functions:** Choose a high-level function that is called by your HTTP handlers, like `plan.Tell`.
2.  **Modify Function Signature:** Add `context.Context` as the first parameter.
3.  **Start and End Spans:** Use `tracer.Start(ctx, "spanName")` and `defer span.End()`.
4.  **Add Attributes:** Set relevant attributes on the span.
5.  **Propagate Context:** Pass the new `spanCtx` to any functions it calls that will also be instrumented.

    ```go
    // Example: app/server/model/plan/tell_exec.go (adapt to your actual file and function)
    package plan

    import (
    	"context" // Add context
    	"log"
    	// ... other plan package imports ...
    	"your_module_path/app/server/internal/tracing" // If you need to get a tracer directly, though usually you get it once

    	"go.opentelemetry.io/otel"
    	"go.opentelemetry.io/otel/attribute"
    	"go.opentelemetry.io/otel/codes"
    	"go.opentelemetry.io/otel/trace" // For trace.SpanFromContext if needed
    	// ...
    	shared "plandex-shared" // Assuming this is how you import it
    )

    // Global or package-level tracer (initialized once)
    // It's often better to get this from otel.GetTracerProvider().Tracer()
    // but for simplicity in this example, we'll assume it's accessible.
    // A better pattern is to pass the tracer or have a global GetTracer function.
    var tracer = otel.Tracer("plandex-server/model/plan")


    // Modify Tell to accept and use context
    func Tell(
    	ctx context.Context, // <-- Add context parameter
    	clients map[string]model.ClientInfo,
    	plan *db.Plan,
    	branch string,
    	auth *types.ServerAuth,
    	req *shared.TellPlanRequest,
    ) error {
    	// Start a new span as a child of the incoming context's span (e.g., from HTTP handler)
    	spanCtx, span := tracer.Start(ctx, "plan.Tell")
    	defer span.End()

    	// Add attributes to the span
    	span.SetAttributes(
    		attribute.String("plan.id", plan.Id),
    		attribute.String("plan.branch", branch),
    		attribute.String("user.id", auth.User.Id),
    		attribute.Bool("request.is_chat_only", req.IsChatOnly),
    		attribute.String("request.session_id", req.SessionId),
    	)

    	log.Printf("[PLAN.TELL] TraceID: %s, SpanID: %s - Starting Tell operation",
    		span.SpanContext().TraceID().String(),
    		span.SpanContext().SpanID().String(),
    	)

    	// Your existing Tell logic...
    	// For example, when calling activatePlan:
    	activePlan, err := activatePlan(spanCtx, clients, plan, branch, auth, req.Prompt, false, req.AutoContext, req.SessionId) // Pass spanCtx
    	if err != nil {
    		span.RecordError(err)
    		span.SetStatus(codes.Error, "Failed to activate plan")
    		log.Printf("[PLAN.TELL] Error activating plan: %v", err)
    		return err // Or however you handle errors
    	}

    	// When calling execTellPlan, pass spanCtx
    	go execTellPlan(spanCtx, execTellPlanParams{ // Pass spanCtx
    		clients:            clients,
    		plan:               plan,
    		// ... other params ...
    	})

    	log.Printf("[PLAN.TELL] TraceID: %s, SpanID: %s - Tell operation initiated",
    		span.SpanContext().TraceID().String(),
    		span.SpanContext().SpanID().String(),
    	)
    	return nil
    }

    // You'll need to similarly modify activatePlan and execTellPlan
    // to accept context and start their own child spans.

    // Example for activatePlan (simplified)
    func activatePlan(
    	ctx context.Context, // <-- Add context
    	clients map[string]model.ClientInfo,
    	plan *db.Plan,
    	branch string,
    	auth *types.ServerAuth,
    	prompt string,
    	buildOnly bool,
    	autoContext bool,
    	sessionId string,
    ) (*types.ActivePlan, error) {
    	spanCtx, span := tracer.Start(ctx, "plan.activatePlan")
    	defer span.End()
    	span.SetAttributes(attribute.String("plan.id", plan.Id), attribute.String("plan.branch", branch))
    	// ... rest of activatePlan logic ...
    	// Ensure any calls made from here (e.g., to db functions) also receive spanCtx
    	return nil, nil // Placeholder
    }

    // Example for execTellPlan (simplified structure)
    func execTellPlan(ctx context.Context, params execTellPlanParams) { // <-- Add context
    	spanCtx, span := tracer.Start(ctx, "plan.execTellPlan")
    	defer span.End()
    	span.SetAttributes(attribute.String("plan.id", params.plan.Id), attribute.Int("iteration", params.iteration))
    	// ... rest of execTellPlan logic ...
    	// Calls to state.loadTellPlan(), state.doTellRequest() etc. should receive spanCtx
    }

    // In your state methods like state.loadTellPlan(), state.doTellRequest():
    // func (state *activeTellStreamState) loadTellPlan(ctx context.Context) error { // <-- Add context
    //     spanCtx, span := tracer.Start(ctx, "activeTellStreamState.loadTellPlan")
    //     defer span.End()
    //     // ...
    //     // When calling db.ExecRepoOperation, pass spanCtx to its Ctx field
    //     // db.ExecRepoOperation(db.ExecRepoOperationParams{ Ctx: spanCtx, ... })
    // }
    ```

**Testing Phase 2:**

*   **How it works:**
    *   The HTTP handler span (from Phase 1) is the parent.
    *   `plan.Tell` creates a child span.
    *   Functions called by `plan.Tell` (like `activatePlan`, `execTellPlan`) will create further nested child spans.
*   **Output:**
    *   When you make an HTTP request that triggers `plan.Tell`:
        *   Console/file logs should show `[PLAN.TELL]` messages with TraceIDs.
        *   Logfire should show a trace with a root "http.server.request" span.
        *   Nested under it, you should see a "plan.Tell" span.
        *   If you instrumented `activatePlan` and `execTellPlan`, you'll see those nested under "plan.Tell".
*   **Verification:**
    1.  Restart your server.
    2.  Trigger an action that calls the `plan.Tell` function (e.g., via the Plandex CLI or a `curl` command to the appropriate server endpoint).
    3.  Check Logfire. You should see the "http.server.request" span, and nested within it, the "plan.Tell" span with its attributes. If you added more, you'll see a hierarchy.

**If Phase 2 is successful, commit your changes:**
```bash
git add .
git commit -m "Phase 2: Instrument plan.Tell service logic"
```

---

**Phase 3: Instrument Outgoing LLM API Calls**

**Instructions:**

1.  **Modify `model.createChatCompletionStreamExtended` (in `app/server/model/client.go`):**
    *   Ensure it accepts `context.Context` as its first parameter.
    *   Start a span for the LLM HTTP call.
    *   Add LLM-specific attributes.
    *   Record errors and status codes.

    ```go
    // File: app/server/model/client.go
    package model

    import (
    	"context" // Add context
    	"encoding/json"
    	"log"
    	"net/http"
    	"os"
    	// ... other model package imports ...

    	"go.opentelemetry.io/otel"
    	"go.opentelemetry.io/otel/attribute"
    	"go.opentelemetry.io/otel/codes"
    	"go.opentelemetry.io/otel/trace"
    	// ...
    	shared "plandex-shared" // Assuming this is how you import it
    )

    var tracer = otel.Tracer("plandex-server/model/client")

    // Modify createChatCompletionStreamExtended
    func createChatCompletionStreamExtended(
    	ctx context.Context, // <-- Add context
    	clients map[string]ClientInfo,
    	modelConfig *shared.ModelRoleConfig,
    	reqBody interface{}, // This is your ExtendedChatCompletionRequest or Gemini specific request
    	// ... other parameters ...
    ) (*ExtendedChatCompletionStream, error) {
    	// Start a new span for the LLM HTTP call.
    	// The incoming 'ctx' should carry the parent span (e.g., from plan.Tell or plan.execTellPlan).
    	spanCtx, llmCallSpan := tracer.Start(ctx, "llm.request") // Using "llm.request" as per OTel conventions
    	defer llmCallSpan.End()

    	// --- Set LLM specific attributes ---
    	// (Semantic conventions for LLMs are evolving, these are common ones)
    	llmCallSpan.SetAttributes(
    		attribute.String("gen_ai.system", string(modelConfig.BaseModelConfig.Provider)), // e.g., "openai", "google_gemini"
    		attribute.String("gen_ai.request.model", string(modelConfig.BaseModelConfig.ModelName)),
    		// If you can determine the operation (e.g., "chat", "completion", "embedding")
    		attribute.String("gen_ai.operation.name", "chat"), // Assuming it's always chat for this function
    	)
    	// Add more attributes like temperature, top_p if they are part of modelConfig and relevant
    	if modelConfig.Temperature > 0 {
    		llmCallSpan.SetAttributes(attribute.Float64("gen_ai.request.temperature", float64(modelConfig.Temperature)))
    	}
    	if modelConfig.TopP > 0 {
    		llmCallSpan.SetAttributes(attribute.Float64("gen_ai.request.top_p", float64(modelConfig.TopP)))
    	}
    	// You might also add number of messages/prompts if easily available from reqBody
    	// e.g., if reqBody is ExtendedChatCompletionRequest:
    	// if extReq, ok := reqBody.(types.ExtendedChatCompletionRequest); ok {
    	//     llmCallSpan.SetAttributes(attribute.Int("gen_ai.request.prompt_count", len(extReq.Messages)))
    	// }


    	// --- Your existing logic to prepare and make the HTTP request ---
    	// Ensure you use `spanCtx` if you make helper calls that also take context.
    	// For the http.NewRequestWithContext, use spanCtx:
    	// httpRequest, err := http.NewRequestWithContext(spanCtx, http.MethodPost, url, bytes.NewBuffer(jsonBody))

    	// Example placeholder for your existing HTTP call logic:
    	url := modelConfig.BaseModelConfig.BaseUrl + "/chat/completions" // Adjust if path varies
    	llmCallSpan.SetAttributes(attribute.String("http.url", url), attribute.String("http.method", http.MethodPost))

    	// ... (jsonBody preparation as before) ...
        var jsonBody []byte
        var err error
        // Your existing marshalling logic
        if modelConfig.BaseModelConfig.Provider == shared.ModelProviderCustom || modelConfig.BaseModelConfig.Provider == shared.ModelProviderGoogle {
            log.Println("[DEBUG] USING CUSTOM MARSHALLING LOGIC FOR GEMINI/CUSTOM PROVIDER")
            // Assuming reqBody is already in the correct Gemini format or you have a conversion function
            // For example, if reqBody is your internal `ExtendedChatCompletionRequest`:
            // geminiReq := ToGeminiChatRequest(reqBody.(types.ExtendedChatCompletionRequest))
            // jsonBody, err = json.Marshal(geminiReq)
            // For now, let's assume reqBody is directly marshallable for custom/Gemini
            jsonBody, err = json.Marshal(reqBody)
        } else {
            log.Println("[DEBUG] USING ORIGINAL EXTENDEDREQUEST MARSHALLING")
            jsonBody, err = json.Marshal(reqBody)
        }
        if err != nil {
            llmCallSpan.RecordError(err)
            llmCallSpan.SetStatus(codes.Error, "Failed to marshal request body")
            return nil, fmt.Errorf("error marshalling request: %w", err)
        }
        log.Printf("[DEBUG LLM Request] Body: %s", string(jsonBody)) // This log is good to keep


    	// --- Make the HTTP request (ensure you use spanCtx if the client supports it) ---
    	// Example:
    	httpClient := &http.Client{Timeout: 60 * time.Second} // Or your existing client
    	httpReq, err := http.NewRequestWithContext(spanCtx, http.MethodPost, url, bytes.NewBuffer(jsonBody))
    	if err != nil {
    		llmCallSpan.RecordError(err)
    		llmCallSpan.SetStatus(codes.Error, "Failed to create HTTP request")
    		return nil, fmt.Errorf("error creating request: %w", err)
    	}
    	// ... (set headers as before, including addOpenRouterHeaders if applicable) ...
        if modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenRouter {
            addOpenRouterHeaders(httpReq) // Assuming this function exists
        } else if modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenAI {
             httpReq.Header.Set("Authorization", "Bearer "+os.Getenv(shared.OpenAIEnvVar))
        }
        // Add other necessary headers like Content-Type
        httpReq.Header.Set("Content-Type", "application/json")


    	resp, err := httpClient.Do(httpReq)
    	// --- End of HTTP request logic ---

    	if err != nil {
    		llmCallSpan.RecordError(err)
    		llmCallSpan.SetStatus(codes.Error, "LLM HTTP call failed")
    		log.Printf("[LLM CLIENT] HTTP request error: %v", err)
    		return nil, fmt.Errorf("error making request to LLM: %w", err)
    	}
    	// No defer resp.Body.Close() here because the stream needs it open

    	llmCallSpan.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

    	if resp.StatusCode != http.StatusOK {
    		// ... (your existing error handling for non-200 status codes) ...
    		// Make sure to record the error on the span
    		bodyBytes, _ := io.ReadAll(resp.Body)
            resp.Body.Close() // Close now since we've read it
    		errMsg := fmt.Sprintf("LLM API error: status %d, body: %s", resp.StatusCode, string(bodyBytes))
    		llmCallSpan.RecordError(fmt.Errorf(errMsg))
    		llmCallSpan.SetStatus(codes.Error, "LLM API returned non-200 status")
    		log.Printf("[LLM CLIENT] %s", errMsg)
    		return nil, fmt.Errorf(errMsg)
    	}

    	// Return your ExtendedChatCompletionStream
    	// The stream itself will handle reading the body.
    	// The span will end when createChatCompletionStreamExtended returns.
    	// If you need to capture total tokens from the stream, that's more complex
    	// and might involve wrapping the stream or getting data from the last chunk.
    	// For now, focus on the request itself.
    	return &ExtendedChatCompletionStream{
    		streamReader: newStreamReader(resp.Body, 65536), // Assuming newStreamReader and your buffer size
    		// ... other fields ...
    	}, nil
    }
    ```
2.  **Ensure Context Propagation:** Verify that the `context.Context` is passed from your service logic (e.g., `plan.Tell` -> `execTellPlan` -> `state.doTellRequest`) all the way to `createChatCompletionStreamExtended`.

**Testing Phase 3:**

*   **How it works:**
    *   The "plan.Tell" (or "plan.execTellPlan") span will be the parent.
    *   `createChatCompletionStreamExtended` will create a child span named "llm.request" for each actual HTTP call to an LLM.
*   **Output:**
    *   When your server makes an LLM call:
        *   Logfire should show the "llm.request" span nested under its parent (e.g., "plan.execTellPlan").
        *   This "llm.request" span should have attributes like `gen_ai.system`, `gen_ai.request.model`, `http.url`, and `http.status_code`.
*   **Verification:**
    1.  Restart your server.
    2.  Trigger an action that results in an LLM call (e.g., a `tell` command).
    3.  Check Logfire. You should see the "llm.request" span with its attributes, properly nested.
    4.  **Specifically for the Gemini 400 error:** If you can reproduce it, find the corresponding "llm.request" span. It should have `http.status_code = 400`. Check its attributes. If you added request body information as an attribute, it would be invaluable here. The logs captured by your file logger will still be the primary source for the *full* request body.

**If Phase 3 is successful, commit your changes:**
```bash
git add .
git commit -m "Phase 3: Instrument outgoing LLM API calls"
```

---

**Phase 4: Instrument Database Interactions (Optional but Recommended)**

**Instructions:**

1.  **Identify DB Call Sites:** Find where you make database queries (e.g., in your `app/server/db` package or functions that use it).
2.  **Wrap DB Calls in Spans:**
    *   Ensure these functions accept `context.Context`.
    *   Start a span before the DB call, end it after.
    *   Add attributes like `db.system`, `db.statement` (be careful with PII if logging full queries), `db.operation`.
    *   If using `database/sql`, explore `go.opentelemetry.io/contrib/instrumentation/database/sql/otelsql`.

    ```go
    // Example: app/server/db/plan.go (conceptual)
    package db

    import (
    	"context"
    	// ...
    	"go.opentelemetry.io/otel"
    	"go.opentelemetry.io/otel/attribute"
    	"go.opentelemetry.io/otel/codes"
    	// ...
    )

    var tracer = otel.Tracer("plandex-server/db")

    func GetPlan(ctx context.Context, db *sqlx.DB, planID string) (*shared.Plan, error) { // Accept ctx
    	spanCtx, span := tracer.Start(ctx, "db.GetPlan") // Start span
    	defer span.End()

    	span.SetAttributes(
    		attribute.String("db.system", "sqlite"), // Or your DB type
    		attribute.String("db.operation", "SELECT"),
    		attribute.String("db.statement", "SELECT * FROM plans WHERE id = ?"), // Sanitize if needed
    		attribute.String("plan.id", planID),
    	)

    	// Your actual DB query logic using db.GetContext(spanCtx, ...) or db.QueryRowContext(spanCtx, ...)
    	// Example:
    	// var plan shared.Plan
    	// err := db.GetContext(spanCtx, &plan, "SELECT * FROM plans WHERE id = $1", planID)
    	// if err != nil {
    	//     span.RecordError(err)
    	//     span.SetStatus(codes.Error, "Failed to get plan from DB")
    	//     return nil, err
    	// }
    	// return &plan, nil
    	return nil, nil // Placeholder
    }
    ```

**Testing Phase 4:**

*   **How it works:** Spans for DB operations will appear nested under their calling service logic spans (e.g., "db.GetPlan" under "plan.loadTellPlan").
*   **Output:** Logfire will show these DB spans with their attributes.
*   **Verification:**
    1.  Restart server.
    2.  Trigger actions that involve DB queries.
    3.  Check Logfire for `db.*` spans with relevant attributes.

**If Phase 4 is successful, commit your changes:**
```bash
git add .
git commit -m "Phase 4: Instrument database interactions"
```

---

**General Advice for All Phases:**

*   **Pass `context.Context`:** This is the most critical part. The `ctx` you pass to `tracer.Start()` determines the parent of the new span.
*   **Tracer Naming:** Use consistent and meaningful names for your tracers (e.g., `otel.Tracer("your_package_path")`).
*   **Span Naming:** Use clear, descriptive names for your spans (e.g., `http.server.request`, `plan.Tell`, `llm.request`, `db.query`). OpenTelemetry semantic conventions provide good guidance.
*   **Attributes:** Add attributes that will help you filter, group, and understand traces in Logfire.
*   **Error Recording:** Always use `span.RecordError(err)` and `span.SetStatus(codes.Error, "description")` when errors occur within a span's operation.
*   **Iterate:** Start with these phases. Once you see data in Logfire, you'll likely identify other areas you want to instrument.
*   **Performance:** OpenTelemetry is designed to be efficient, but excessive span creation or very large attributes can have an impact. Be mindful, especially in hot code paths. The batch exporter helps mitigate this.

This phased approach should allow you to incrementally add valuable tracing to your server and verify each step. Good luck!