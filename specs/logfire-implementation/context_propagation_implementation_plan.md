# Context Propagation Implementation Plan for Plandex-Logfire Integration

## Overview

This document outlines a comprehensive plan for implementing complete context propagation throughout the Plandex codebase to enhance the existing Logfire OpenTelemetry integration. The goal is to ensure unbroken trace chains from HTTP requests through all operations, providing complete observability for debugging, performance analysis, and system monitoring.

## Current State Assessment

The current implementation has established basic tracing with partial context propagation:

- HTTP handlers are wrapped with `otelhttp.NewHandler` for automatic request tracing
- Core functions like `plan.Tell` and `execTellPlanWithContext` accept and use context
- Some operations create spans but with disconnected trace chains
- Many internal functions still lack context parameters or don't propagate existing contexts

Key gaps include:
- Context helper functions creating new background contexts instead of using passed contexts
- Async operations (goroutines) losing trace context
- Database operations lacking proper context propagation
- CLI operations not integrated into the tracing system
- Inconsistent error handling in spans

## Implementation Strategy

The implementation will follow a methodical, layered approach to minimize disruption while ensuring complete coverage:

1. **Core Service Layer**: Update central service functions first
2. **Database Layer**: Enhance DB operations with context propagation
3. **API/Handler Layer**: Ensure proper context flow from external interfaces
4. **Async Operations**: Address goroutines and background tasks
5. **CLI Integration**: Extend context propagation to CLI operations

## Detailed Implementation Plan

### Phase 1: Core Service Layer (Days 1-3)

#### 1.1 Plan Operations

**Files to modify:**
- `app/server/model/plan/activate.go`
- `app/server/model/plan/tell_exec.go`
- `app/server/model/plan/build_exec.go`
- `app/server/model/plan/build_load.go`

**Implementation steps:**

1. Update `activatePlan` in `activate.go`:
   ```go
   // Before
   func activatePlan(clients map[string]model.ClientInfo, plan *db.Plan, branch string, auth *types.ServerAuth, prompt string, buildOnly bool, autoContext bool, sessionId string) (*activePlan, error) {
   
   // After
   func activatePlan(ctx context.Context, clients map[string]model.ClientInfo, plan *db.Plan, branch string, auth *types.ServerAuth, prompt string, buildOnly bool, autoContext bool, sessionId string) (*activePlan, error) {
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "plan.activatePlan",
           trace.WithAttributes(
               attribute.String("plan.id", plan.Id),
               attribute.String("plan.branch", branch),
               attribute.String("user.id", auth.User.Id),
               attribute.String("session.id", sessionId),
               attribute.Bool("build_only", buildOnly),
               attribute.Bool("auto_context", autoContext),
           ),
       )
       defer span.End()
       
       // Store the context in activePlan
       active := &activePlan{
           Ctx:         ctx, // Use the span context
           Plan:        plan,
           // ... other fields
       }
       
       // Update error handling
       if err != nil {
           span.RecordError(err)
           span.SetStatus(codes.Error, "Failed to activate plan")
           return nil, err
       }
       
       return active, nil
   }
   ```

2. Ensure `execTellPlanWithContext` in `tell_exec.go` properly uses the context:
   ```go
   func execTellPlanWithContext(ctx context.Context, params execTellPlanParams) {
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "plan.execTellPlan",
           trace.WithAttributes(
               // ... existing attributes
           ),
       )
       defer span.End()
       
       // Pass context to all operations that need it
       activePlan, err := activatePlan(ctx, params.clients, params.plan, params.branch, params.auth, params.req.Prompt, false, params.req.AutoContext, params.req.SessionId)
       
       // Update error handling
       if err != nil {
           span.RecordError(err)
           span.SetStatus(codes.Error, err.Error())
           return
       }
       
       // Pass context to LLM operations
       // ...
   }
   ```

3. Update `Build` in `build_exec.go`:
   ```go
   func Build(ctx context.Context, clients map[string]model.ClientInfo, plan *db.Plan, branch string, auth *types.ServerAuth, req *shared.BuildPlanRequest) error {
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "plan.Build",
           trace.WithAttributes(
               attribute.String("plan.id", plan.Id),
               attribute.String("plan.branch", branch),
               attribute.String("user.id", auth.User.Id),
               attribute.String("session.id", req.SessionId),
           ),
       )
       defer span.End()
       
       // Pass context to loadPendingBuilds
       pendingBuilds, err := loadPendingBuilds(ctx, plan, branch, auth)
       
       // Update error handling
       if err != nil {
           span.RecordError(err)
           span.SetStatus(codes.Error, "Failed to load pending builds")
           return err
       }
       
       // ...
   }
   ```

4. Update `loadPendingBuilds` in `build_load.go`:
   ```go
   func loadPendingBuilds(ctx context.Context, plan *db.Plan, branch string, auth *types.ServerAuth) ([]*pendingBuild, error) {
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "plan.loadPendingBuilds",
           trace.WithAttributes(
               attribute.String("plan.id", plan.Id),
               attribute.String("plan.branch", branch),
           ),
       )
       defer span.End()
       
       // Use ctx for database operations
       // ...
       
       return pendingBuilds, nil
   }
   ```

#### 1.2 Context Helper Operations

**Files to modify:**
- `app/server/handlers/context_helper.go`
- `app/server/model/plan/context_load.go`

**Implementation steps:**

1. Update `loadContexts` in `context_helper.go`:
   ```go
   func loadContexts(ctx context.Context, params loadContextsParams) ([]*shared.Context, []*db.Context, error) {
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "handlers.loadContexts",
           trace.WithAttributes(
               attribute.String("plan.id", params.plan.Id),
               attribute.String("branch", params.branchName),
               attribute.Int("contexts.count", len(*params.loadReq)),
           ),
       )
       defer span.End()
       
       // Use the passed context instead of creating a new one
       ctxWithCancel, cancel := context.WithCancel(ctx)
       defer cancel()
       
       // Pass context to database operations
       // ...
       
       return contexts, dbContexts, nil
   }
   ```

2. Update context loading functions in `context_load.go`:
   ```go
   func loadContextFromUrl(ctx context.Context, url string) (string, error) {
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "context.loadFromUrl",
           trace.WithAttributes(
               attribute.String("url", url),
           ),
       )
       defer span.End()
       
       // Use ctx for HTTP client operations
       req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
       if err != nil {
           span.RecordError(err)
           span.SetStatus(codes.Error, "Failed to create request")
           return "", err
       }
       
       // ...
   }
   ```

### Phase 2: Database Layer (Days 4-5)

#### 2.1 Database Operations

**Files to modify:**
- `app/server/db/plan_helpers.go`
- `app/server/db/context_helpers_load.go`
- `app/server/db/context_helpers_save.go`

**Implementation steps:**

1. Update `LoadPlan` in `plan_helpers.go`:
   ```go
   func LoadPlan(ctx context.Context, planId string) (*db.Plan, error) {
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "db.LoadPlan",
           trace.WithAttributes(
               attribute.String("plan.id", planId),
           ),
       )
       defer span.End()
       
       // Use ctx for database query
       var plan db.Plan
       err := database.Get(&plan, "SELECT * FROM plans WHERE id = $1", planId)
       if err != nil {
           span.RecordError(err)
           span.SetStatus(codes.Error, "Failed to load plan from database")
           return nil, err
       }
       
       return &plan, nil
   }
   ```

2. Update `LoadContexts` in `context_helpers_load.go`:
   ```go
   func LoadContexts(ctx context.Context, planId, branch string) ([]*db.Context, error) {
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "db.LoadContexts",
           trace.WithAttributes(
               attribute.String("plan.id", planId),
               attribute.String("branch", branch),
           ),
       )
       defer span.End()
       
       // Use ctx for database query
       var contexts []*db.Context
       err := database.Select(&contexts, "SELECT * FROM contexts WHERE plan_id = $1 AND branch = $2", planId, branch)
       if err != nil {
           span.RecordError(err)
           span.SetStatus(codes.Error, "Failed to load contexts from database")
           return nil, err
       }
       
       return contexts, nil
   }
   ```

3. Update `SaveContext` in `context_helpers_save.go`:
   ```go
   func SaveContext(ctx context.Context, context *db.Context) error {
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "db.SaveContext",
           trace.WithAttributes(
               attribute.String("plan.id", context.PlanId),
               attribute.String("branch", context.Branch),
               attribute.String("context.id", context.Id),
               attribute.String("context.type", context.Type),
           ),
       )
       defer span.End()
       
       // Use ctx for database transaction
       tx, err := database.Beginx()
       if err != nil {
           span.RecordError(err)
           span.SetStatus(codes.Error, "Failed to begin transaction")
           return err
       }
       
       // ... transaction operations
       
       if err := tx.Commit(); err != nil {
           span.RecordError(err)
           span.SetStatus(codes.Error, "Failed to commit transaction")
           return err
       }
       
       return nil
   }
   ```

### Phase 3: API/Handler Layer (Days 6-7)

#### 3.1 HTTP Handlers

**Files to modify:**
- `app/server/handlers/plans_exec.go`
- `app/server/handlers/plans_context.go`
- `app/server/handlers/plans.go`

**Implementation steps:**

1. Update `TellPlanHandler` in `plans_exec.go`:
   ```go
   func TellPlanHandler(w http.ResponseWriter, r *http.Request) {
       // r.Context() already contains the span from otelhttp.NewHandler
       ctx := r.Context()
       
       // Extract request parameters
       // ...
       
       // Pass context to Tell
       err = plan.Tell(ctx, clients, dbPlan, branch, auth, &req)
       if err != nil {
           // Error handling
           return
       }
       
       // ...
   }
   ```

2. Update `BuildPlanHandler` in `plans_exec.go`:
   ```go
   func BuildPlanHandler(w http.ResponseWriter, r *http.Request) {
       ctx := r.Context()
       
       // Extract request parameters
       // ...
       
       // Pass context to Build
       err = plan.Build(ctx, clients, dbPlan, branch, auth, &req)
       if err != nil {
           // Error handling
           return
       }
       
       // ...
   }
   ```

3. Update `LoadContextHandler` in `plans_context.go`:
   ```go
   func LoadContextHandler(w http.ResponseWriter, r *http.Request) {
       ctx := r.Context()
       
       // Extract request parameters
       // ...
       
       // Pass context to loadContexts
       contexts, dbContexts, err := loadContexts(ctx, loadContextsParams{
           plan:       dbPlan,
           branchName: branch,
           loadReq:    &req,
           autoLoaded: false,
       })
       
       // ...
   }
   ```

### Phase 4: Async Operations (Days 8-9)

#### 4.1 Goroutines and Background Tasks

**Files to modify:**
- `app/server/model/plan/tell_exec.go` (for goroutines)
- `app/server/model/plan/build_structured_edits.go`
- Any other files with goroutines

**Implementation steps:**

1. Update goroutine in `tell_exec.go`:
   ```go
   // Instead of:
   go execTellPlanWithContext(ctx, params)
   
   // Use a function that captures the context:
   go func(ctx context.Context, p execTellPlanParams) {
       execTellPlanWithContext(ctx, p)
   }(ctx, params)
   ```

2. Update `buildStructuredEdits` in `build_structured_edits.go`:
   ```go
   func (fileState *activeBuildStreamFileState) buildStructuredEdits() error {
       // Use the context from activePlan
       ctx := fileState.activePlan.Ctx
       tracer := otel.Tracer("plandex-server")
       ctx, span := tracer.Start(ctx, "syntax.apply_structured_edits",
           trace.WithAttributes(
               attribute.String("file.path", fileState.filePath),
               attribute.Int("original_file_size", len(fileState.originalContent)),
               attribute.Int("proposed_content_size", len(fileState.proposedContent)),
               attribute.Bool("has_parser", fileState.parser != nil),
           ),
       )
       defer span.End()
       
       // Existing implementation with error handling
       if err != nil {
           span.RecordError(err)
           span.SetStatus(codes.Error, "Failed to apply structured edits")
           return err
       }
       
       return nil
   }
   ```

3. For any background tasks that run independently:
   ```go
   func startBackgroundTask() {
       // Create a new context with global tracer
       ctx, span := otel.Tracer("plandex-server").Start(context.Background(), "background.task")
       defer span.End()
       
       // Run task with context
       performBackgroundWork(ctx)
   }
   ```

### Phase 5: CLI Integration (Day 10)

#### 5.1 CLI Operations

**Files to modify:**
- `app/cli/lib/context_auto_load.go`
- `app/cli/api/methods.go`
- `app/cli/cmd/tell.go`
- `app/cli/cmd/build.go`

**Implementation steps:**

1. Update `AutoLoadContextFiles` in `context_auto_load.go`:
   ```go
   func AutoLoadContextFiles(ctx context.Context, files []string) (string, error) {
       // If no context is passed, create one
       if ctx == nil {
           ctx = context.Background()
       }
       
       tracer := otel.Tracer("plandex-cli")
       ctx, span := tracer.Start(ctx, "cli.AutoLoadContextFiles",
           trace.WithAttributes(
               attribute.String("plan.id", CurrentPlanId),
               attribute.String("branch", CurrentBranch),
               attribute.Int("files.count", len(files)),
           ),
       )
       defer span.End()
       
       // Pass context to API calls
       res, apiErr := api.Client.AutoLoadContext(ctx, CurrentPlanId, CurrentBranch, loadContextReqs)
       
       // ...
   }
   ```

2. Update API methods in `methods.go`:
   ```go
   func (a *Api) TellPlan(ctx context.Context, planId, branch string, req shared.TellPlanRequest) (*shared.TellPlanResponse, *shared.ApiError) {
       // If no context is passed, create one
       if ctx == nil {
           ctx = context.Background()
       }
       
       serverUrl := fmt.Sprintf("%s/plans/%s/%s/tell", GetApiHost(), planId, branch)
       reqBytes, err := json.Marshal(req)
       if err != nil {
           return nil, &shared.ApiError{Type: shared.ApiErrorTypeOther, Msg: fmt.Sprintf("error marshalling request: %v", err)}
       }
       
       // Create request with context
       httpReq, err := http.NewRequestWithContext(ctx, "POST", serverUrl, bytes.NewBuffer(reqBytes))
       if err != nil {
           return nil, &shared.ApiError{Type: shared.ApiErrorTypeOther, Msg: fmt.Sprintf("error creating request: %v", err)}
       }
       
       // ...
   }
   ```

3. Update CLI commands in `tell.go` and `build.go`:
   ```go
   func tellCmd() *cobra.Command {
       // ...
       
       cmd.RunE = func(cmd *cobra.Command, args []string) error {
           // Create a context for the CLI command
           ctx := context.Background()
           
           // Pass context to API calls
           _, apiErr := api.Client.TellPlan(ctx, planId, branch, req)
           
           // ...
       }
       
       // ...
   }
   ```

## Testing Strategy

### Unit Tests

1. Update existing unit tests to pass context:
   ```go
   func TestActivatePlan(t *testing.T) {
       // Create a test context
       ctx := context.Background()
       
       // Pass context to function under test
       activePlan, err := activatePlan(ctx, testClients, testPlan, "main", testAuth, "test prompt", false, false, "test-session")
       
       // Assertions...
   }
   ```

### Integration Tests

1. Create a test that verifies trace propagation:
   ```go
   func TestTracePropagation(t *testing.T) {
       // Set up in-memory exporter
       exporter := inmemory.New()
       tp := sdktrace.NewTracerProvider(
           sdktrace.WithSampler(sdktrace.AlwaysSample()),
           sdktrace.WithBatcher(exporter),
       )
       otel.SetTracerProvider(tp)
       
       // Create root context with span
       ctx, rootSpan := tp.Tracer("test").Start(context.Background(), "test.root")
       
       // Call function that should create child spans
       err := plan.Tell(ctx, testClients, testPlan, "main", testAuth, &testReq)
       
       // End root span and flush
       rootSpan.End()
       tp.ForceFlush(context.Background())
       
       // Get exported spans
       spans := exporter.GetSpans()
       
       // Verify parent-child relationships
       // ...
   }
   ```

### Manual Testing

1. Run the server with Logfire integration enabled
2. Execute a complete workflow: tell → build → context loading
3. Verify in Logfire UI that:
   - All spans appear in a single trace