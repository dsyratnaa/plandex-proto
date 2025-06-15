This file is a merged representation of a subset of the codebase, containing specifically included files, combined into a single document by Repomix.

<file_summary>
This section contains a summary of this file.

<purpose>
This file contains a packed representation of the entire repository's contents.
It is designed to be easily consumable by AI systems for analysis, code review,
or other automated processes.
</purpose>

<file_format>
The content is organized as follows:
1. This summary section
2. Repository information
3. Directory structure
4. Repository files (if enabled)
4. Repository files, each consisting of:
  - File path as an attribute
  - Full contents of the file
</file_format>

<usage_guidelines>
- This file should be treated as read-only. Any changes should be made to the
  original repository files, not this packed version.
- When processing this file, use the file path to distinguish
  between different files in the repository.
- Be aware that this file may contain sensitive information. Handle it with
  the same level of security as you would the original repository.
</usage_guidelines>

<notes>
- Some files may have been excluded based on .gitignore rules and Repomix's configuration
- Binary files are not included in this packed representation. Please refer to the Repository Structure section for a complete list of file paths, including binary files
- Only files matching these patterns are included: app/server/internal/tracing/tracing.go, app/server/model/plan/activate.go, app/server/model/plan/tell_exec.go, app/server/model/plan/build_exec.go, app/server/model/plan/build_load.go, app/server/model/plan/build_structured_edits.go, app/server/handlers/plans_exec.go, app/server/start_with_tracing.sh
- Files matching patterns in .gitignore are excluded
- Files matching default ignore patterns are excluded
- Files are sorted by Git change count (files with more changes are at the bottom)
</notes>

<additional_info>

</additional_info>

</file_summary>

<directory_structure>
app/
  server/
    handlers/
      plans_exec.go
    internal/
      tracing/
        tracing.go
    model/
      plan/
        activate.go
        build_exec.go
        build_load.go
        build_structured_edits.go
        tell_exec.go
    start_with_tracing.sh
</directory_structure>

<files>
This section contains the contents of the repository's files.

<file path="app/server/start_with_tracing.sh">
#!/bin/bash

# Plandex Server Startup Script with Logfire Tracing
# This script starts the Plandex server with OpenTelemetry tracing enabled

echo "🚀 Starting Plandex Server with Logfire Tracing..."

# Set the Logfire token (write token for sending traces)
export LOGFIRE_TOKEN=pylf_v1_us_1Th1dn0glpzT1PZTJ4FFXZldbvWqSr9WVfk51qDLHy9V

# Optional: Set service name (defaults to "plandex-server")
export OTEL_SERVICE_NAME=plandex-server

# Optional: Set custom OTLP endpoint (defaults to Logfire)
# export OTEL_EXPORTER_OTLP_ENDPOINT=https://logfire-api.pydantic.dev

# Database configuration (using Docker PostgreSQL on port 5433)
export DATABASE_URL="postgres://plandex:plandex@localhost:5433/plandex?sslmode=disable"
export GOENV=development
export LOCAL_MODE=1
export PLANDEX_BASE_DIR=/plandex-server

echo "📊 Configuration:"
echo "  - Service Name: ${OTEL_SERVICE_NAME:-plandex-server}"
echo "  - Logfire Token: ${LOGFIRE_TOKEN:0:20}..."
echo "  - OTLP Endpoint: ${OTEL_EXPORTER_OTLP_ENDPOINT:-https://logfire-api.pydantic.dev (default)}"
echo "  - Database URL: ${DATABASE_URL}"
echo "  - Local Mode: ${LOCAL_MODE}"
echo ""

# Build and start the server
echo "🔨 Building server..."
go build -o plandex-server-traced .

if [ $? -eq 0 ]; then
    echo "✅ Build successful!"
    echo "🌟 Starting server with tracing enabled..."
    echo "📈 View traces at: https://logfire.pydantic.dev/"
    echo ""
    ./plandex-server-traced
else
    echo "❌ Build failed!"
    exit 1
fi
</file>

<file path="app/server/handlers/plans_exec.go">
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"plandex-server/db"
	"plandex-server/hooks"
	"plandex-server/host"
	modelPlan "plandex-server/model/plan"
	"plandex-server/notify"
	"plandex-server/types"
	"time"

	shared "plandex-shared"

	"github.com/gorilla/mux"
)

func TellPlanHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request for TellPlanHandler", "ip:", host.Ip)

	auth := Authenticate(w, r, true)
	if auth == nil {
		return
	}

	vars := mux.Vars(r)
	planId := vars["planId"]
	branch := vars["branch"]

	log.Println("planId: ", planId)

	plan := authorizePlanExecUpdate(w, planId, auth)
	if plan == nil {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error reading request body: %v", err))
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer func() {
		log.Println("Closing request body")
		r.Body.Close()
	}()

	var requestBody shared.TellPlanRequest
	if err := json.Unmarshal(body, &requestBody); err != nil {
		log.Printf("Error parsing request body: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error parsing request body: %v", err))
		http.Error(w, "Error parsing request body", http.StatusBadRequest)
		return
	}

	_, apiErr := hooks.ExecHook(hooks.WillTellPlan, hooks.HookParams{
		Auth: auth,
		Plan: plan,
	})
	if apiErr != nil {
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error executing will tell plan hook: %v", apiErr))
		writeApiError(w, *apiErr)
		return
	}

	clients := initClients(
		initClientsParams{
			w:           w,
			auth:        auth,
			apiKey:      requestBody.ApiKey,
			apiKeys:     requestBody.ApiKeys,
			endpoint:    requestBody.Endpoint,
			openAIBase:  requestBody.OpenAIBase,
			openAIOrgId: requestBody.OpenAIOrgId,
			plan:        plan,
		},
	)
	err = modelPlan.Tell(clients, plan, branch, auth, &requestBody)

	if err != nil {
		log.Printf("Error telling plan: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error telling plan: %v", err))
		http.Error(w, "Error telling plan", http.StatusInternalServerError)
		return
	}

	if requestBody.ConnectStream {
		startResponseStream(r.Context(), w, auth, planId, branch, false)
	}

	log.Println("Successfully processed request for TellPlanHandler")
}

func BuildPlanHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request for BuildPlanHandler", "ip:", host.Ip)
	auth := Authenticate(w, r, true)
	if auth == nil {
		return
	}

	vars := mux.Vars(r)
	planId := vars["planId"]
	branch := vars["branch"]

	log.Println("planId: ", planId)
	plan := authorizePlanExecUpdate(w, planId, auth)
	if plan == nil {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error reading request body: %v", err))
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer func() {
		log.Println("Closing request body")
		r.Body.Close()
	}()

	var requestBody shared.BuildPlanRequest
	if err := json.Unmarshal(body, &requestBody); err != nil {
		log.Printf("Error parsing request body: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error parsing request body: %v", err))
		http.Error(w, "Error parsing request body", http.StatusBadRequest)
		return
	}

	clients := initClients(
		initClientsParams{
			w:           w,
			auth:        auth,
			apiKey:      requestBody.ApiKey,
			apiKeys:     requestBody.ApiKeys,
			endpoint:    requestBody.Endpoint,
			openAIBase:  requestBody.OpenAIBase,
			openAIOrgId: requestBody.OpenAIOrgId,
			plan:        plan,
		},
	)
	numBuilds, err := modelPlan.Build(r.Context(), clients, plan, branch, auth, requestBody.SessionId)

	if err != nil {
		log.Printf("Error building plan: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error building plan: %v", err))
		http.Error(w, "Error building plan", http.StatusInternalServerError)
		return
	}

	if numBuilds == 0 {
		log.Println("No builds were executed")
		go notify.NotifyErr(notify.SeverityInfo, fmt.Errorf("no builds were executed"))
		http.Error(w, shared.NoBuildsErr, http.StatusNotFound)
		return
	}

	if requestBody.ConnectStream {
		startResponseStream(r.Context(), w, auth, planId, branch, false)
	}

	log.Println("Successfully processed request for BuildPlanHandler")
}

func ConnectPlanHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request for ConnectPlanHandler", "ip:", host.Ip)

	vars := mux.Vars(r)
	planId := vars["planId"]
	branch := vars["branch"]
	log.Println("planId: ", planId)
	log.Println("branch: ", branch)
	active := modelPlan.GetActivePlan(planId, branch)
	isProxy := r.URL.Query().Get("proxy") == "true"

	if active == nil {
		if isProxy {
			log.Println("No active plan on proxied request")
			go notify.NotifyErr(notify.SeverityInfo, fmt.Errorf("no active plan on proxied request"))
			http.Error(w, "No active plan", http.StatusNotFound)
			return
		}

		log.Println("No active plan -- proxying request")

		proxyActivePlanMethod(w, r, planId, branch, "connect")
		return
	}

	auth := Authenticate(w, r, true)
	if auth == nil {
		log.Println("No auth")
		return
	}

	plan := authorizePlan(w, planId, auth)
	if plan == nil {
		log.Println("No plan")
		return
	}

	startResponseStream(r.Context(), w, auth, planId, branch, true)

	log.Println("Successfully processed request for ConnectPlanHandler")
}

func StopPlanHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request for StopPlanHandler", "ip:", host.Ip)

	vars := mux.Vars(r)
	planId := vars["planId"]
	branch := vars["branch"]
	log.Println("planId: ", planId)
	log.Println("branch: ", branch)
	active := modelPlan.GetActivePlan(planId, branch)
	isProxy := r.URL.Query().Get("proxy") == "true"

	if active == nil {
		if isProxy {
			log.Println("No active plan on proxied request")
			http.Error(w, "No active plan", http.StatusNotFound)
			return
		}
		proxyActivePlanMethod(w, r, planId, branch, "stop")
		return
	}

	auth := Authenticate(w, r, true)
	if auth == nil {
		return
	}

	if authorizePlan(w, planId, auth) == nil {
		return
	}

	log.Println("Sending stream aborted message to client")

	active.Stream(shared.StreamMessage{
		Type: shared.StreamMessageAborted,
	})

	// give some time for stream message to be processed before canceling
	log.Println("Sleeping for 100ms before canceling")
	time.Sleep(100 * time.Millisecond)

	var err error
	ctx, cancel := context.WithCancel(r.Context())

	// this is here to ensure that the plan is stopped even if the db operation fails
	defer func() {
		err = modelPlan.Stop(planId, branch, auth.User.Id, auth.OrgId)

		if err != nil {
			log.Printf("Error stopping plan: %v\n", err)
		}

		log.Println("Successfully processed request for StopPlanHandler")
	}()

	err = db.ExecRepoOperation(db.ExecRepoOperationParams{
		OrgId:    auth.OrgId,
		UserId:   auth.User.Id,
		PlanId:   planId,
		Branch:   branch,
		Reason:   "stop plan",
		Scope:    db.LockScopeWrite,
		Ctx:      ctx,
		CancelFn: cancel,
	}, func(repo *db.GitRepo) error {
		log.Println("Stopping plan - storing partial reply")
		err = modelPlan.StorePartialReply(repo, planId, branch, auth.User.Id, auth.OrgId)
		return err
	})

	if err != nil {
		log.Printf("Error storing partial reply: %v\n", err)
		http.Error(w, "Error storing partial reply", http.StatusInternalServerError)
		return
	}
}

func RespondMissingFileHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request for RespondMissingFileHandler", "ip:", host.Ip)

	vars := mux.Vars(r)
	planId := vars["planId"]
	branch := vars["branch"]
	log.Println("planId: ", planId)
	log.Println("branch: ", branch)
	isProxy := r.URL.Query().Get("proxy") == "true"

	active := modelPlan.GetActivePlan(planId, branch)
	if active == nil {
		if isProxy {
			log.Println("No active plan on proxied request")
			http.Error(w, "No active plan", http.StatusNotFound)
			return
		}

		proxyActivePlanMethod(w, r, planId, branch, "respond_missing_file")
		return
	}

	auth := Authenticate(w, r, true)
	if auth == nil {
		return
	}

	plan := authorizePlan(w, planId, auth)
	if plan == nil {
		return
	}

	// read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v\n", err)
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var requestBody shared.RespondMissingFileRequest
	if err := json.Unmarshal(body, &requestBody); err != nil {
		log.Printf("Error parsing request body: %v\n", err)
		http.Error(w, "Error parsing request body", http.StatusBadRequest)
		return
	}

	log.Println("missing file choice:", requestBody.Choice)

	if requestBody.Choice == shared.RespondMissingFileChoiceLoad {
		log.Println("loading missing file")
		res, dbContexts := loadContexts(loadContextsParams{
			w:    w,
			r:    r,
			auth: auth,
			loadReq: &shared.LoadContextRequest{
				&shared.LoadContextParams{
					ContextType: shared.ContextFileType,
					Name:        requestBody.FilePath,
					FilePath:    requestBody.FilePath,
					Body:        requestBody.Body,
				},
			},
			plan:       plan,
			branchName: branch,
			autoLoaded: true,
		})
		if res == nil {
			return
		}

		dbContext := dbContexts[0]

		log.Println("loaded missing file:", dbContext.FilePath)

		modelPlan.UpdateActivePlan(planId, branch, func(activePlan *types.ActivePlan) {
			if activePlan == nil {
				log.Println("Active plan is nil")
				http.Error(w, "Active plan is nil", http.StatusInternalServerError)
				return
			}
			activePlan.Contexts = append(activePlan.Contexts, dbContext)
			activePlan.ContextsByPath[dbContext.FilePath] = dbContext
		})
	}

	// This will resume model stream
	log.Println("Resuming model stream")
	active.MissingFileResponseCh <- requestBody.Choice

	log.Println("Successfully processed request for RespondMissingFileHandler")
}

func AutoLoadContextHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received request for AutoLoadContextHandler", "ip:", host.Ip)

	vars := mux.Vars(r)
	planId := vars["planId"]
	branch := vars["branch"]
	log.Println("planId: ", planId)
	log.Println("branch: ", branch)

	isProxy := r.URL.Query().Get("proxy") == "true"

	active := modelPlan.GetActivePlan(planId, branch)
	if active == nil {
		if isProxy {
			log.Println("No active plan on proxied request")
			http.Error(w, "No active plan", http.StatusNotFound)
			return
		}

		proxyActivePlanMethod(w, r, planId, branch, "auto_load_context")
		return
	}

	auth := Authenticate(w, r, true)
	if auth == nil {
		return
	}

	plan := authorizePlan(w, planId, auth)
	if plan == nil {
		return
	}

	var err error
	defer func() {
		if err == nil {
			active.AutoLoadContextCh <- struct{}{}
		} else {
			active.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Error in AutoLoadContextHandler: " + err.Error(),
			}
		}
	}()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v\n", err)
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var requestBody shared.LoadContextRequest
	if err := json.Unmarshal(body, &requestBody); err != nil {
		log.Printf("Error parsing request body: %v\n", err)
		http.Error(w, "Error parsing request body", http.StatusBadRequest)
		return
	}

	log.Println("AutoLoadContextHandler - loading contexts")

	var res *shared.LoadContextResponse
	var dbContexts []*db.Context
	if len(requestBody) > 0 {
		res, dbContexts = loadContexts(loadContextsParams{
			w:          w,
			r:          r,
			auth:       auth,
			loadReq:    &requestBody,
			plan:       plan,
			branchName: branch,
			autoLoaded: true,
		})
	}

	if res == nil {
		// the client will treat this as a no-op
		markdownRes := shared.LoadContextResponse{
			TokensAdded:       0,
			TotalTokens:       0,
			MaxTokensExceeded: false,
			MaxTokens:         0,
			Msg:               "",
		}

		bytes, err := json.Marshal(markdownRes)
		if err != nil {
			log.Printf("Error marshalling response: %v\n", err)
			http.Error(w, "Error marshalling response", http.StatusInternalServerError)
			return
		}

		w.Write(bytes)
		return
	}

	log.Println("AutoLoadContextHandler - updating active plan")

	modelPlan.UpdateActivePlan(planId, branch, func(activePlan *types.ActivePlan) {
		if activePlan == nil {
			log.Println("Active plan is nil")
			http.Error(w, "Active plan is nil", http.StatusInternalServerError)
			return
		}
		activePlan.Contexts = append(activePlan.Contexts, dbContexts...)
		for _, dbContext := range dbContexts {
			activePlan.ContextsByPath[dbContext.FilePath] = dbContext
		}
	})

	log.Println("AutoLoadContextHandler - updated active plan")

	var apiContexts []*shared.Context
	for _, dbContext := range dbContexts {
		apiContexts = append(apiContexts, dbContext.ToApi())
	}

	msg := shared.SummaryForLoadContext(apiContexts, res.TokensAdded, res.TotalTokens)
	msg += "\n\n" + shared.TableForLoadContext(apiContexts, true)

	markdownRes := shared.LoadContextResponse{
		TokensAdded:       res.TokensAdded,
		TotalTokens:       res.TotalTokens,
		MaxTokensExceeded: res.MaxTokensExceeded,
		MaxTokens:         res.MaxTokens,
		Msg:               msg,
	}

	bytes, err := json.Marshal(markdownRes)
	if err != nil {
		log.Printf("Error marshalling response: %v\n", err)
		http.Error(w, "Error marshalling response", http.StatusInternalServerError)
		return
	}

	w.Write(bytes)

	log.Println("Successfully processed request for AutoLoadContextHandler")
}

func GetBuildStatusHandler(w http.ResponseWriter, r *http.Request) {
	// logs are too chatty on this function, uncomment if you need to debug
	// log.Println("Received request for GetBuildStatusHandler", "ip:", host.Ip)

	vars := mux.Vars(r)
	planId := vars["planId"]
	branch := vars["branch"]

	isProxy := r.URL.Query().Get("proxy") == "true"

	active := modelPlan.GetActivePlan(planId, branch)
	if active == nil {
		if isProxy {
			log.Println("No active plan on proxied request")
			http.Error(w, "No active plan", http.StatusNotFound)
			return
		}

		proxyActivePlanMethod(w, r, planId, branch, "auto_load_context")
		return
	}

	auth := Authenticate(w, r, true)
	if auth == nil {
		return
	}

	plan := authorizePlan(w, planId, auth)
	if plan == nil {
		return
	}

	response := shared.GetBuildStatusResponse{
		BuiltFiles:       active.BuiltFiles,
		IsBuildingByPath: active.IsBuildingByPath,
	}

	bytes, err := json.Marshal(response)
	if err != nil {
		log.Printf("Error marshalling response: %v\n", err)
		http.Error(w, "Error marshalling response", http.StatusInternalServerError)
		return
	}

	w.Write(bytes)

	// log.Println("Successfully processed request for GetBuildStatusHandler")
}

func authorizePlanExecUpdate(w http.ResponseWriter, planId string, auth *types.ServerAuth) *db.Plan {
	plan := authorizePlan(w, planId, auth)
	if plan == nil {
		return nil
	}

	if plan.OwnerId != auth.User.Id && !auth.HasPermission(shared.PermissionUpdateAnyPlan) {
		log.Println("User does not have permission to update plan")
		http.Error(w, "User does not have permission to update plan", http.StatusForbidden)
		return nil
	}

	return plan
}
</file>

<file path="app/server/model/plan/activate.go">
package plan

import (
	"context"
	"fmt"
	"log"
	"plandex-server/db"
	"plandex-server/host"
	"plandex-server/model"
	"plandex-server/types"
	"time"

	shared "plandex-shared"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func activatePlan(
	ctx context.Context,
	clients map[string]model.ClientInfo,
	plan *db.Plan,
	branch string,
	auth *types.ServerAuth,
	prompt string,
	buildOnly,
	autoContext bool,
	sessionId string,
) (*types.ActivePlan, error) {
	// Start OpenTelemetry span for activatePlan operation
	tracer := otel.Tracer("plandex-server")
	ctx, span := tracer.Start(ctx, "plan.activatePlan")
	defer span.End()

	// Set span attributes
	span.SetAttributes(
		attribute.String("plan.id", plan.Id),
		attribute.String("plan.branch", branch),
		attribute.String("user.id", auth.User.Id),
		attribute.String("org.id", auth.OrgId),
		attribute.String("prompt", prompt),
		attribute.Bool("build_only", buildOnly),
		attribute.Bool("auto_context", autoContext),
		attribute.String("session.id", sessionId),
	)

	log.Printf("Activate plan: plan ID %s on branch %s (TraceID: %s)\n", plan.Id, branch, span.SpanContext().TraceID().String())

	// Just in case this request was made immediately after another stream finished, wait a little to allow for cleanup
	log.Println("Waiting 100ms before checking for active plan")
	time.Sleep(100 * time.Millisecond)
	log.Println("Done waiting, checking for active plan")

	active := GetActivePlan(plan.Id, branch)
	if active != nil {
		log.Printf("Tell: Active plan found for plan ID %s on branch %s\n", plan.Id, branch) // Log if an active plan is found
		err := fmt.Errorf("plan %s branch %s already has an active stream on this host", plan.Id, branch)
		span.RecordError(err)
		span.SetStatus(codes.Error, "Active plan already exists")
		return nil, err
	}

	modelStream, err := db.GetActiveModelStream(plan.Id, branch)
	if err != nil {
		log.Printf("Error getting active model stream: %v\n", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to get active model stream")
		return nil, fmt.Errorf("error getting active model stream: %v", err)
	}

	if modelStream != nil {
		log.Printf("Tell: Active model stream found for plan ID %s on branch %s on host %s\n", plan.Id, branch, modelStream.InternalIp) // Log if an active model stream is found
		err := fmt.Errorf("plan %s branch %s already has an active stream on host %s", plan.Id, branch, modelStream.InternalIp)
		span.RecordError(err)
		span.SetStatus(codes.Error, "Active model stream already exists")
		return nil, err
	}

	active = CreateActivePlan(
		auth.OrgId,
		auth.User.Id,
		plan.Id,
		branch,
		prompt,
		buildOnly,
		autoContext,
		sessionId,
	)

	modelStream = &db.ModelStream{
		OrgId:      auth.OrgId,
		PlanId:     plan.Id,
		InternalIp: host.Ip,
		Branch:     branch,
	}
	err = db.StoreModelStream(modelStream, active.Ctx, active.CancelFn)
	if err != nil {
		log.Printf("Tell: Error storing model stream for plan ID %s on branch %s: %v\n", plan.Id, branch, err) // Log error storing model stream
		log.Printf("Error storing model stream: %v\n", err)
		log.Printf("Tell: Error storing model stream: %v\n", err) // Log error storing model stream

		active.StreamDoneCh <- &shared.ApiError{Msg: fmt.Sprintf("Error storing model stream: %v", err)}

		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to store model stream")
		return nil, fmt.Errorf("error storing model stream: %v", err)
	}

	active.ModelStreamId = modelStream.Id

	log.Printf("Tell: Model stream stored with ID %s for plan ID %s on branch %s\n", modelStream.Id, plan.Id, branch) // Log successful storage of model stream
	log.Println("Model stream id:", modelStream.Id)

	span.SetStatus(codes.Ok, "Plan activated successfully")
	return active, nil
}
</file>

<file path="app/server/model/plan/build_exec.go">
package plan

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"plandex-server/db"
	"plandex-server/hooks"
	"plandex-server/model"
	"plandex-server/notify"
	"plandex-server/types"
	"runtime/debug"
	"time"

	shared "plandex-shared"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func Build(
	ctx context.Context,
	clients map[string]model.ClientInfo,
	plan *db.Plan,
	branch string,
	auth *types.ServerAuth,
	sessionId string,
) (int, error) {
	// Start OpenTelemetry span for Build operation
	tracer := otel.Tracer("plandex-server")
	ctx, span := tracer.Start(ctx, "plan.build")
	defer span.End()

	// Set span attributes
	span.SetAttributes(
		attribute.String("plan.id", plan.Id),
		attribute.String("plan.branch", branch),
		attribute.String("user.id", auth.User.Id),
		attribute.String("org.id", auth.OrgId),
		attribute.String("session.id", sessionId),
	)

	log.Printf("Build: Called with plan ID %s on branch %s (TraceID: %s)\n", plan.Id, branch, span.SpanContext().TraceID().String())
	log.Println("Build: Starting Build operation")

	state := activeBuildStreamState{
		clients:       clients,
		auth:          auth,
		currentOrgId:  auth.OrgId,
		currentUserId: auth.User.Id,
		plan:          plan,
		branch:        branch,
	}

	streamDone := func() {
		active := GetActivePlan(plan.Id, branch)
		if active != nil {
			active.StreamDoneCh <- nil
		}
	}

	onErr := func(err error) (int, error) {
		log.Printf("Build error: %v\n", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "Build failed")
		streamDone()
		return 0, err
	}

	pendingBuildsByPath, err := state.loadPendingBuilds(ctx, sessionId)
	if err != nil {
		return onErr(err)
	}

	if len(pendingBuildsByPath) == 0 {
		log.Println("No pending builds")
		span.SetStatus(codes.Ok, "No pending builds")
		streamDone()
		return 0, nil
	}

	err = db.SetPlanStatus(plan.Id, branch, shared.PlanStatusBuilding, "")

	if err != nil {
		log.Printf("Error setting plan status to building: %v\n", err)
		return onErr(fmt.Errorf("error setting plan status to building: %v", err))
	}

	log.Printf("Starting %d builds\n", len(pendingBuildsByPath))

	for _, pendingBuilds := range pendingBuildsByPath {
		go state.queueBuilds(pendingBuilds)
	}

	span.SetStatus(codes.Ok, "Build initiated successfully")
	return len(pendingBuildsByPath), nil
}

func (state *activeBuildStreamState) queueBuild(activeBuild *types.ActiveBuild) {
	planId := state.plan.Id
	branch := state.branch

	filePath := activeBuild.Path

	// log.Printf("Queue:")
	// spew.Dump(activePlan.BuildQueuesByPath[filePath])

	var isBuilding bool

	UpdateActivePlan(planId, branch, func(active *types.ActivePlan) {
		active.BuildQueuesByPath[filePath] = append(active.BuildQueuesByPath[filePath], activeBuild)
		isBuilding = active.IsBuildingByPath[filePath]
	})
	log.Printf("Queued build for file %s\n", filePath)

	if isBuilding {
		log.Printf("Already building file %s\n", filePath)
		return
	} else {
		log.Printf("Not building file %s\n", filePath)

		active := GetActivePlan(planId, branch)
		if active == nil {
			log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
			return
		}

		UpdateActivePlan(planId, branch, func(active *types.ActivePlan) {
			active.IsBuildingByPath[filePath] = true
		})

		go state.execPlanBuild(activeBuild)
	}
}

func (state *activeBuildStreamState) queueBuilds(activeBuilds []*types.ActiveBuild) {
	log.Printf("Queueing %d builds\n", len(activeBuilds))

	for _, activeBuild := range activeBuilds {
		state.queueBuild(activeBuild)
	}
}

func (buildState *activeBuildStreamState) execPlanBuild(activeBuild *types.ActiveBuild) {
	if activeBuild == nil {
		log.Println("No active build")
		return
	}

	log.Printf("execPlanBuild - %s\n", activeBuild.Path)
	// log.Println(spew.Sdump(activeBuild))

	planId := buildState.plan.Id
	branch := buildState.branch

	activePlan := GetActivePlan(planId, branch)
	if activePlan == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("execPlanBuild: Panic: %v\n%s\n", r, string(debug.Stack()))

			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("execPlanBuild: Panic: %v\n%s", r, string(debug.Stack())))

			activePlan.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Panic in execPlanBuild",
			}
		}
	}()

	filePath := activeBuild.Path

	if !activePlan.IsBuildingByPath[filePath] {
		UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
			ap.IsBuildingByPath[filePath] = true
		})
	}

	fileState := &activeBuildStreamFileState{
		activeBuildStreamState: buildState,
		filePath:               filePath,
		activeBuild:            activeBuild,
		builderRun: hooks.DidFinishBuilderRunParams{
			StartedAt: time.Now(),
			PlanId:    activePlan.Id,
			FilePath:  filePath,
			FileExt:   filepath.Ext(filePath),
		},
	}

	log.Printf("execPlanBuild - %s - calling fileState.loadBuildFile()\n", filePath)
	err := fileState.loadBuildFile(activeBuild)
	if err != nil {
		log.Printf("Error loading build file: %v\n", err)
		fileState.onBuildFileError(fmt.Errorf("error loading build file: %v", err))
		return
	}

	fileState.resolvePreBuildState()

	// unless it's a file operation, stream initial status to client
	if !activeBuild.IsFileOperation() && !fileState.isNewFile {
		log.Printf("execPlanBuild - %s - streaming initial build info\n", filePath)
		// spew.Dump(activeBuild)
		buildInfo := &shared.BuildInfo{
			Path:      filePath,
			NumTokens: 0,
			Finished:  false,
		}
		activePlan.Stream(shared.StreamMessage{
			Type:      shared.StreamMessageBuildInfo,
			BuildInfo: buildInfo,
		})
	} else if activeBuild.IsFileOperation() {
		log.Printf("execPlanBuild - %s - file operation - won't stream initial build info\n", filePath)
	} else if fileState.isNewFile {
		log.Printf("execPlanBuild - %s - new file - won't stream initial build info\n", filePath)
	}

	log.Printf("execPlanBuild - %s - calling fileState.buildFile()\n", filePath)
	fileState.buildFile()
}

func (fileState *activeBuildStreamFileState) buildFile() {
	filePath := fileState.filePath
	activeBuild := fileState.activeBuild
	planId := fileState.plan.Id
	branch := fileState.branch
	currentOrgId := fileState.currentOrgId
	build := fileState.build

	activePlan := GetActivePlan(planId, branch)

	if activePlan == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
		return
	}

	log.Printf("Building file %s\n", filePath)
	log.Printf("%d files in context\n", len(activePlan.ContextsByPath))
	// log.Println("activePlan.ContextsByPath files:")
	// for k := range activePlan.ContextsByPath {
	// 	log.Println(k)
	// }

	if activeBuild.IsMoveOp {
		log.Printf("File %s is a move operation. Moving to %s\n", filePath, activeBuild.MoveDestination)

		// For move operations, we split it into two separate builds:
		// 1. A removal build for the source file
		// 2. A creation build for the destination file with the current content
		// This is simpler than handling moves in a single build since our build system
		// is designed around operating on one path at a time
		fileState.activeBuildStreamState.queueBuilds([]*types.ActiveBuild{
			{
				ReplyId:    activeBuild.ReplyId,
				Path:       activeBuild.Path,
				IsRemoveOp: true,
			},
			{
				ReplyId:           activeBuild.ReplyId,
				Path:              activeBuild.MoveDestination,
				FileContent:       fileState.preBuildState,
				FileContentTokens: 0,
			},
		})

		// Mark this move operation as successful since we've queued the actual work
		activeBuild.Success = true

		UpdateActivePlan(planId, branch, func(active *types.ActivePlan) {
			active.IsBuildingByPath[filePath] = false
			active.BuiltFiles[filePath] = true
		})

		// Process the next build in queue (which will be our removal build)
		// We need to explicitly advance the queue for the source path since this
		// current build is holding the 'building' state open
		// The create build for the destination will be handled automatically by the queue logic
		fileState.buildNextInQueue()
		return
	}

	if activeBuild.IsRemoveOp {
		log.Printf("File %s is a remove operation. Removing file.\n", filePath)

		log.Printf("streaming remove build info for file %s\n", filePath)
		buildInfo := &shared.BuildInfo{
			Path:      filePath,
			NumTokens: 0,
			Removed:   true,
			Finished:  true,
		}

		activePlan.Stream(shared.StreamMessage{
			Type:      shared.StreamMessageBuildInfo,
			BuildInfo: buildInfo,
		})

		planRes := &db.PlanFileResult{
			OrgId:          currentOrgId,
			PlanId:         planId,
			PlanBuildId:    build.Id,
			ConvoMessageId: build.ConvoMessageId,
			Path:           filePath,
			Content:        "",
			RemovedFile:    true,
		}
		fileState.onFinishBuildFile(planRes)
		return
	}

	if activeBuild.IsResetOp {
		log.Printf("File %s is a reset operation. Resetting file.\n", filePath)

		err := db.ExecRepoOperation(db.ExecRepoOperationParams{
			OrgId:       currentOrgId,
			UserId:      fileState.currentUserId,
			PlanId:      planId,
			Branch:      branch,
			PlanBuildId: build.Id,
			Scope:       db.LockScopeWrite,
			Reason:      "reset file op",
			Ctx:         activePlan.Ctx,
			CancelFn:    activePlan.CancelFn,
		}, func(repo *db.GitRepo) error {
			now := time.Now()
			return db.RejectPlanFile(currentOrgId, planId, filePath, now)
		})

		if err != nil {
			log.Printf("Error rejecting plan file: %v\n", err)
			fileState.onBuildFileError(fmt.Errorf("error rejecting plan file: %v", err))
			return
		}

		buildInfo := &shared.BuildInfo{
			Path:      filePath,
			NumTokens: 0,
			Finished:  true,
			Removed:   fileState.contextPart == nil,
		}

		activePlan.Stream(shared.StreamMessage{
			Type:      shared.StreamMessageBuildInfo,
			BuildInfo: buildInfo,
		})

		time.Sleep(200 * time.Millisecond)

		fileState.onBuildProcessed(activeBuild)
		return
	}

	if fileState.preBuildState == "" {
		log.Printf("File %s not found in model context or current plan. Creating new file.\n", filePath)

		buildInfo := &shared.BuildInfo{
			Path:      filePath,
			NumTokens: 0,
			Finished:  true,
		}

		log.Printf("streaming new file build info for file %s\n", filePath)

		activePlan.Stream(shared.StreamMessage{
			Type:      shared.StreamMessageBuildInfo,
			BuildInfo: buildInfo,
		})

		// new file
		planRes := &db.PlanFileResult{
			OrgId:          currentOrgId,
			PlanId:         planId,
			PlanBuildId:    build.Id,
			ConvoMessageId: build.ConvoMessageId,
			Path:           filePath,
			Content:        activeBuild.FileContent,
		}

		// log.Println("build exec - new file result")
		// spew.Dump(planRes)
		fileState.onFinishBuildFile(planRes)
		return
	} else {
		currentNumTokens := shared.GetNumTokensEstimate(fileState.preBuildState)

		log.Printf("Current state num tokens: %d\n", currentNumTokens)

		activeBuild.CurrentFileTokens = currentNumTokens
		activePlan.DidEditFiles = true
	}

	// build structured edits strategy now works regardless of language/tree-sitter support
	log.Println("buildFile - building structured edits")
	fileState.buildStructuredEdits()
}

func (fileState *activeBuildStreamFileState) resolvePreBuildState() {
	filePath := fileState.filePath
	currentPlan := fileState.currentPlanState
	planId := fileState.plan.Id
	branch := fileState.branch

	activePlan := GetActivePlan(planId, branch)

	if activePlan == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
		return
	}
	contextPart := activePlan.ContextsByPath[filePath]

	var currentState string
	currentPlanFile, fileInCurrentPlan := currentPlan.CurrentPlanFiles.Files[filePath]

	// log.Println("plan files:")
	// spew.Dump(currentPlan.CurrentPlanFiles.Files)

	if fileInCurrentPlan {
		log.Printf("File %s found in current plan.\n", filePath)
		fileState.isNewFile = false
		currentState = currentPlanFile
		// log.Println("\n\nCurrent state:\n", currentState, "\n\n")

	} else if contextPart != nil {
		log.Printf("File %s found in model context. Using context state.\n", filePath)
		fileState.isNewFile = false
		currentState = contextPart.Body
		// log.Println("\n\nCurrent state:\n", currentState, "\n\n")
	} else {
		fileState.isNewFile = true
	}

	fileState.preBuildState = currentState
	fileState.contextPart = contextPart
}
</file>

<file path="app/server/model/plan/build_load.go">
package plan

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"plandex-server/db"
	"plandex-server/notify"
	"plandex-server/syntax"
	"plandex-server/types"

	shared "plandex-shared"
)

func (state *activeBuildStreamState) loadPendingBuilds(ctx context.Context, sessionId string) (map[string][]*types.ActiveBuild, error) {
	clients := state.clients
	plan := state.plan
	branch := state.branch
	auth := state.auth

	active, err := activatePlan(ctx, clients, plan, branch, auth, "", true, false, sessionId)

	if err != nil {
		log.Printf("Error activating plan: %v\n", err)
	}

	modelStreamId := active.ModelStreamId
	state.modelStreamId = modelStreamId

	var modelContext []*db.Context
	var pendingBuildsByPath map[string][]*types.ActiveBuild
	var settings *shared.PlanSettings

	err = db.ExecRepoOperation(db.ExecRepoOperationParams{
		OrgId:    auth.OrgId,
		UserId:   auth.User.Id,
		PlanId:   plan.Id,
		Branch:   branch,
		Scope:    db.LockScopeRead,
		Ctx:      active.Ctx,
		CancelFn: active.CancelFn,
		Reason:   "load pending builds",
	}, func(repo *db.GitRepo) error {
		errCh := make(chan error)

		go func() {
			res, err := db.GetPlanContexts(auth.OrgId, plan.Id, true, false)
			if err != nil {
				log.Printf("Error getting plan modelContext: %v\n", err)
				errCh <- fmt.Errorf("error getting plan modelContext: %v", err)
				return
			}
			modelContext = res

			errCh <- nil
		}()

		go func() {
			res, err := active.PendingBuildsByPath(auth.OrgId, auth.User.Id, nil)

			if err != nil {
				log.Printf("Error getting pending builds by path: %v\n", err)
				errCh <- fmt.Errorf("error getting pending builds by path: %v", err)
				return
			}

			pendingBuildsByPath = res

			errCh <- nil
		}()

		go func() {
			res, err := db.GetPlanSettings(plan, true)
			if err != nil {
				log.Printf("Error getting plan settings: %v\n", err)
				errCh <- fmt.Errorf("error getting plan settings: %v", err)
				return
			}

			settings = res
			errCh <- nil
		}()

		for i := 0; i < 3; i++ {
			err = <-errCh
			if err != nil {
				log.Printf("Error getting plan data: %v\n", err)
				return err
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error getting plan data: %v", err)
	}

	UpdateActivePlan(plan.Id, branch, func(ap *types.ActivePlan) {
		ap.Contexts = modelContext
		for _, context := range modelContext {
			if context.FilePath != "" {
				ap.ContextsByPath[context.FilePath] = context
			}
		}
	})

	state.modelContext = modelContext
	state.settings = settings

	return pendingBuildsByPath, nil
}

func (state *activeBuildStreamFileState) loadBuildFile(activeBuild *types.ActiveBuild) error {
	currentOrgId := state.currentOrgId
	planId := state.plan.Id
	branch := state.branch
	filePath := state.filePath

	activePlan := GetActivePlan(planId, branch)

	if activePlan == nil {
		return fmt.Errorf("active plan not found")
	}

	convoMessageId := activeBuild.ReplyId

	parser, lang, fallbackParser, fallbackLang := syntax.GetParserForPath(filePath)

	if parser != nil {
		validationRes, err := syntax.ValidateWithParsers(activePlan.Ctx, lang, parser, fallbackLang, fallbackParser, state.preBuildState)
		if err != nil {
			log.Printf(" error validating original file syntax: %v\n", err)
			return fmt.Errorf("error validating original file syntax: %v", err)
		}

		state.language = validationRes.Lang
		state.parser = validationRes.Parser

		state.builderRun.Lang = string(validationRes.Lang)

		if validationRes.TimedOut {
			state.syntaxCheckTimedOut = true
		} else if !validationRes.Valid {
			state.preBuildStateSyntaxInvalid = true
		}
	}

	build := &db.PlanBuild{
		OrgId:          currentOrgId,
		PlanId:         planId,
		ConvoMessageId: convoMessageId,
		FilePath:       filePath,
	}
	err := db.StorePlanBuild(build)

	if err != nil {
		log.Printf("Error storing plan build: %v\n", err)
		UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
			ap.IsBuildingByPath[filePath] = false
		})
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error storing plan build: %v", err))

		activePlan.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Error storing plan build: " + err.Error(),
		}
		return err
	}

	var currentPlan *shared.CurrentPlanState
	var convo []*db.ConvoMessage

	log.Println("Locking repo for load build file")

	err = db.ExecRepoOperation(db.ExecRepoOperationParams{
		OrgId:       currentOrgId,
		UserId:      state.activeBuildStreamState.currentUserId,
		PlanId:      planId,
		Branch:      branch,
		PlanBuildId: build.Id,
		Scope:       db.LockScopeRead,
		Ctx:         activePlan.Ctx,
		CancelFn:    activePlan.CancelFn,
		Reason:      "load build file",
	}, func(repo *db.GitRepo) error {
		errCh := make(chan error)

		go func() {
			log.Println("loadBuildFile - Getting current plan state")
			res, err := db.GetCurrentPlanState(db.CurrentPlanStateParams{
				OrgId:  currentOrgId,
				PlanId: planId,
			})
			if err != nil {
				log.Printf("Error getting current plan state: %v\n", err)
				UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
					ap.IsBuildingByPath[filePath] = false
				})
				go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error getting current plan state: %v", err))

				activePlan.StreamDoneCh <- &shared.ApiError{
					Type:   shared.ApiErrorTypeOther,
					Status: http.StatusInternalServerError,
					Msg:    "Error getting current plan state: " + err.Error(),
				}
				errCh <- fmt.Errorf("error getting current plan state: %v", err)
				return
			}
			currentPlan = res

			log.Println("Got current plan state")
			errCh <- nil
		}()

		go func() {
			res, err := db.GetPlanConvo(currentOrgId, planId)
			if err != nil {
				log.Printf("Error getting plan convo: %v\n", err)
				errCh <- fmt.Errorf("error getting plan convo: %v", err)
				return
			}
			convo = res

			errCh <- nil
		}()

		for i := 0; i < 2; i++ {
			err = <-errCh
			if err != nil {
				log.Printf("Error getting plan data: %v\n", err)
				return err
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("Error loading build file: %v\n", err)
		UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
			ap.IsBuildingByPath[filePath] = false
		})
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error loading build file: %v", err))

		activePlan.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Error loading build file: " + err.Error(),
		}
		return err
	}

	state.filePath = filePath
	state.convoMessageId = convoMessageId
	state.build = build
	state.currentPlanState = currentPlan
	state.convo = convo

	return nil

}
</file>

<file path="app/server/model/plan/build_structured_edits.go">
package plan

import (
	"context"
	"fmt"
	"log"
	"plandex-server/db"
	diff_pkg "plandex-server/diff"
	"plandex-server/hooks"
	"plandex-server/syntax"
	"plandex-server/utils"
	"strings"
	"time"

	shared "plandex-shared"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func (fileState *activeBuildStreamFileState) buildStructuredEdits() {
	filePath := fileState.filePath
	activeBuild := fileState.activeBuild
	planId := fileState.plan.Id
	branch := fileState.branch
	originalFile := fileState.preBuildState
	parser := fileState.parser

	if parser == nil {
		log.Printf("buildStructuredEdits - tree-sitter parser is nil for file %s\n", filePath)
	}

	activePlan := GetActivePlan(planId, branch)
	if activePlan == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
		fileState.onBuildFileError(fmt.Errorf("active plan not found for plan ID %s and branch %s", planId, branch))
		return
	}

	// Start OpenTelemetry span for buildStructuredEdits operation
	tracer := otel.Tracer("plandex-server")
	ctx, span := tracer.Start(activePlan.Ctx, "syntax.apply_structured_edits")
	defer span.End()

	// Set span attributes
	span.SetAttributes(
		attribute.String("plan.id", planId),
		attribute.String("plan.branch", branch),
		attribute.String("file.path", filePath),
		attribute.String("file.description", activeBuild.FileDescription),
		attribute.Bool("has_parser", parser != nil),
		attribute.Int("original_file_size", len(originalFile)),
		attribute.Int("proposed_content_size", len(activeBuild.FileContent)),
	)

	log.Printf("buildStructuredEdits - %s - starting structured edits (TraceID: %s)\n", filePath, span.SpanContext().TraceID().String())

	buildCtx, cancelBuild := context.WithCancel(ctx)

	proposedContent := activeBuild.FileContent
	desc := activeBuild.FileDescription

	descLower := strings.ToLower(desc)
	isReplaceOrRemove := strings.Contains(descLower, "type: replace") || strings.Contains(descLower, "type: remove") || strings.Contains(descLower, "type: overwrite")

	var autoApplyRes *syntax.ApplyChangesResult
	var autoApplySyntaxErrors []string

	calledFastApply := false
	var fastApplyRes string
	fastApplyCh := make(chan string, 1)

	callFastApply := func() {
		log.Printf("buildStructuredEdits - %s - calling fast apply hook\n", filePath)
		fileState.builderRun.DidFastApply = true
		fileState.builderRun.FastApplyStartedAt = time.Now()
		calledFastApply = true

		go func() {
			res, err := hooks.ExecHook(hooks.CallFastApply, hooks.HookParams{
				FastApplyParams: &hooks.FastApplyParams{
					InitialCode: originalFile,
					EditSnippet: proposedContent,
					Language:    fileState.language,
					Ctx:         buildCtx,
				},
			})

			if err != nil {
				log.Printf("buildStructuredEdits - error executing fast apply hook: %v\n", err)
				// empty string acts as a no-op
				fastApplyCh <- ""
				return
			} else if res.FastApplyResult == nil {
				log.Printf("buildStructuredEdits - fast apply hook returned nil result\n")
				// empty string acts as a no-op
				fastApplyCh <- ""
				return
			}

			fastApplyRes = res.FastApplyResult.MergedCode
			log.Printf("buildStructuredEdits - %s - got fast apply hook result\n", filePath)
			// fmt.Printf("buildStructuredEdits - fastApplyRes:\n%s", fastApplyRes)

			fileState.builderRun.FastApplyFinishedAt = time.Now()

			fastApplyCh <- fastApplyRes
		}()
	}

	if isReplaceOrRemove {
		callFastApply()
	}

	log.Printf("buildStructuredEdits - %s - applying changes\n", filePath)
	// Apply plan logic
	log.Printf("buildStructuredEdits - %s - calling ApplyChanges\n", filePath)
	autoApplyRes = syntax.ApplyChanges(
		buildCtx,
		syntax.ApplyChangesParams{
			Original:               originalFile,
			Proposed:               proposedContent,
			Desc:                   desc,
			AddMissingStartEndRefs: true,
			Parser:                 fileState.parser,
			Language:               fileState.language,
		},
	)
	log.Printf("buildStructuredEdits - %s - got ApplyChanges result\n", filePath)
	// log.Printf("buildStructuredEdits - autoApplyRes.NewFile:\n\n%s", autoApplyRes.NewFile)
	log.Println("buildStructuredEdits - autoApplyRes.NeedsVerifyReasons:", autoApplyRes.NeedsVerifyReasons)

	autoApplySyntaxErrors = fileState.validateSyntax(buildCtx, autoApplyRes.NewFile)

	hasNeedsVerifyReasons := len(autoApplyRes.NeedsVerifyReasons) > 0

	autoApplyHasSyntaxErrors := len(autoApplySyntaxErrors) > 0
	autoApplyIsValid := !autoApplyHasSyntaxErrors && !hasNeedsVerifyReasons

	if !autoApplyIsValid && !calledFastApply {
		callFastApply()
	}

	log.Printf("buildStructuredEdits - %s - autoApplyHasSyntaxErrors: %t, hasNeedsVerifyReasons: %t, autoApplyIsValid: %t\n",
		filePath, autoApplyHasSyntaxErrors, hasNeedsVerifyReasons, autoApplyIsValid)

	updated := autoApplyRes.NewFile

	// If no problems, we trust the direct ApplyChanges result
	if autoApplyIsValid {
		log.Printf("buildStructuredEdits - %s - changes are valid, using ApplyChanges result\n", filePath)
		fileState.builderRun.AutoApplySuccess = true
	} else {
		log.Printf("buildStructuredEdits - %s - auto apply has syntax errors or NeedsVerifyReasons", filePath)
		fileState.builderRun.AutoApplyValidationReasons = make([]string, len(autoApplyRes.NeedsVerifyReasons))
		for i, reason := range autoApplyRes.NeedsVerifyReasons {
			fileState.builderRun.AutoApplyValidationReasons[i] = string(reason)
		}

		fileState.builderRun.AutoApplyValidationSyntaxErrors = autoApplySyntaxErrors

		buildRaceParams := buildRaceParams{
			updated:         updated,
			proposedContent: proposedContent,
			desc:            desc,
			reasons:         autoApplyRes.NeedsVerifyReasons,
			syntaxErrors:    autoApplySyntaxErrors,

			didCallFastApply: calledFastApply,
			fastApplyCh:      fastApplyCh,

			sessionId: activePlan.SessionId,
		}

		buildRaceResult, err := fileState.buildRace(buildCtx, cancelBuild, buildRaceParams)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "Build race failed")
			if apiErr, ok := err.(*shared.ApiError); ok {
				activePlan.StreamDoneCh <- apiErr
				return
			} else {
				log.Printf("buildStructuredEdits - %s - error building race: %v\n", filePath, err)
				fileState.onBuildFileError(fmt.Errorf("error building race: %v", err))
			}
			return
		}

		updated = buildRaceResult.content
	}

	// output diff and store build results
	buildInfo := &shared.BuildInfo{
		Path:      filePath,
		NumTokens: 0,
		Finished:  true,
	}
	log.Printf("streaming build info for finished file %s\n", filePath)
	activePlan.Stream(shared.StreamMessage{
		Type:      shared.StreamMessageBuildInfo,
		BuildInfo: buildInfo,
	})
	time.Sleep(50 * time.Millisecond)

	// strip any blank lines from beginning/end of updated file
	updated = utils.StripAddedBlankLines(originalFile, updated)

	log.Printf("buildStructuredEdits - %s - getting diff replacements\n", filePath)
	replacements, err := diff_pkg.GetDiffReplacements(originalFile, updated)
	if err != nil {
		log.Printf("buildStructuredEdits - error getting diff replacements: %v\n", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to get diff replacements")
		fileState.onBuildFileError(fmt.Errorf("error getting diff replacements: %v", err))
		return
	}
	log.Printf("buildStructuredEdits - %s - got %d replacements\n", filePath, len(replacements))

	// Add replacement count to span attributes
	span.SetAttributes(attribute.Int("replacements.count", len(replacements)))

	for _, replacement := range replacements {
		replacement.Summary = strings.TrimSpace(desc)
	}

	res := db.PlanFileResult{
		TypeVersion:    1,
		OrgId:          fileState.plan.OrgId,
		PlanId:         fileState.plan.Id,
		PlanBuildId:    fileState.build.Id,
		ConvoMessageId: fileState.convoMessageId,
		Content:        "",
		Path:           filePath,
		Replacements:   replacements,
	}

	log.Printf("buildStructuredEdits - %s - finishing build file\n", filePath)
	span.SetStatus(codes.Ok, "Structured edits completed successfully")
	fileState.onFinishBuildFile(&res)
}

func (fileState *activeBuildStreamFileState) validateSyntax(buildCtx context.Context, updated string) []string {
	if fileState.parser != nil && !fileState.preBuildStateSyntaxInvalid && !fileState.syntaxCheckTimedOut {
		validationRes, err := syntax.ValidateWithParsers(buildCtx, fileState.language, fileState.parser, "", nil, updated) // fallback parser was already set as fileState.parser if needed during initial preBuildState syntax check
		if err != nil {
			log.Printf("buildStructuredEdits - error validating updated file: %v\n", err)
		} else if validationRes.TimedOut {
			log.Printf("buildStructuredEdits - syntax check timed out for updated file\n")
			fileState.syntaxCheckTimedOut = true
			return nil
		} else {
			return validationRes.Errors
		}
	}

	return nil
}
</file>

<file path="app/server/internal/tracing/tracing.go">
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
	// The library handles adding the scheme. Just provide the host.
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint("logfire-api.pydantic.dev"), // The library will add https:// automatically
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
</file>

<file path="app/server/model/plan/tell_exec.go">
package plan

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	"plandex-server/db"
	"plandex-server/hooks"
	"plandex-server/model"
	"plandex-server/notify"
	"plandex-server/types"

	shared "plandex-shared"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func Tell(clients map[string]model.ClientInfo, plan *db.Plan, branch string, auth *types.ServerAuth, req *shared.TellPlanRequest) error {
	// Start OpenTelemetry span for Tell operation
	tracer := otel.Tracer("plandex-server")
	ctx, span := tracer.Start(context.Background(), "plan.Tell",
		trace.WithAttributes(
			attribute.String("plan.id", plan.Id),
			attribute.String("plan.branch", branch),
			attribute.String("user.id", auth.User.Id),
			attribute.String("org.id", auth.OrgId),
			attribute.String("prompt", req.Prompt),
			attribute.Bool("auto_context", req.AutoContext),
			attribute.Bool("is_chat_only", req.IsChatOnly),
			attribute.String("build_mode", string(req.BuildMode)),
		),
	)
	defer span.End()

	log.Printf("Tell: Called with plan ID %s on branch %s (TraceID: %s)\n", plan.Id, branch, span.SpanContext().TraceID().String())

	_, err := activatePlan(
		ctx,
		clients,
		plan,
		branch,
		auth,
		req.Prompt,
		false,
		req.AutoContext,
		req.SessionId,
	)

	if err != nil {
		log.Printf("Error activating plan: %v\n", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to activate plan")
		return err
	}

	// Pass context to execTellPlan goroutine
	go execTellPlanWithContext(ctx, execTellPlanParams{
		clients:            clients,
		plan:               plan,
		branch:             branch,
		auth:               auth,
		req:                req,
		iteration:          0,
		shouldBuildPending: !req.IsChatOnly && req.BuildMode == shared.BuildModeAuto,
	})

	log.Printf("Tell: Tell operation completed successfully for plan ID %s on branch %s (TraceID: %s)\n", plan.Id, branch, span.SpanContext().TraceID().String())
	span.SetStatus(codes.Ok, "Tell operation initiated successfully")
	return nil
}

type execTellPlanParams struct {
	clients                    map[string]model.ClientInfo
	plan                       *db.Plan
	branch                     string
	auth                       *types.ServerAuth
	req                        *shared.TellPlanRequest
	iteration                  int
	missingFileResponse        shared.RespondMissingFileChoice
	shouldBuildPending         bool
	unfinishedSubtaskReasoning string
}

// execTellPlanWithContext wraps execTellPlan with OpenTelemetry context propagation
func execTellPlanWithContext(ctx context.Context, params execTellPlanParams) {
	// Start a new span for the execTellPlan operation
	tracer := otel.Tracer("plandex-server")
	_, span := tracer.Start(ctx, "plan.execTellPlan",
		trace.WithAttributes(
			attribute.String("plan.id", params.plan.Id),
			attribute.String("plan.branch", params.branch),
			attribute.Int("iteration", params.iteration),
			attribute.Bool("should_build_pending", params.shouldBuildPending),
			attribute.String("session.id", params.req.SessionId),
			attribute.String("missing_file_response", string(params.missingFileResponse)),
			attribute.String("unfinished_subtask_reasoning", params.unfinishedSubtaskReasoning),
		),
	)
	defer span.End()

	log.Printf("[TellExec] Starting iteration %d for plan %s on branch %s (TraceID: %s)",
		params.iteration, params.plan.Id, params.branch, span.SpanContext().TraceID().String())

	// Call the original execTellPlan function
	execTellPlan(params)

	span.SetStatus(codes.Ok, "execTellPlan completed")
}

func execTellPlan(params execTellPlanParams) {
	clients := params.clients
	plan := params.plan
	branch := params.branch
	auth := params.auth
	req := params.req
	iteration := params.iteration
	missingFileResponse := params.missingFileResponse
	shouldBuildPending := params.shouldBuildPending
	unfinishedSubtaskReasoning := params.unfinishedSubtaskReasoning

	log.Printf("[TellExec] Starting iteration %d for plan %s on branch %s", iteration, plan.Id, branch)

	currentUserId := auth.User.Id
	currentOrgId := auth.OrgId

	active := GetActivePlan(plan.Id, branch)

	if active == nil {
		log.Printf("execTellPlan: Active plan not found for plan ID %s on branch %s\n", plan.Id, branch)
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("execTellPlan: Panic: %v\n%s\n", r, string(debug.Stack()))

			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("execTellPlan: Panic: %v\n%s", r, string(debug.Stack())))

			active.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Panic in execTellPlan",
			}
		}
	}()

	if missingFileResponse == "" {
		log.Println("Executing WillExecPlanHook")
		_, apiErr := hooks.ExecHook(hooks.WillExecPlan, hooks.HookParams{
			Auth: auth,
			Plan: plan,
		})

		if apiErr != nil {
			time.Sleep(100 * time.Millisecond)
			active.StreamDoneCh <- apiErr
			return
		}
	}

	planId := plan.Id
	log.Println("execTellPlan - Setting plan status to replying")
	err := db.SetPlanStatus(planId, branch, shared.PlanStatusReplying, "")
	if err != nil {
		log.Printf("Error setting plan %s status to replying: %v\n", planId, err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error setting plan %s status to replying: %v", planId, err))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Error setting plan status to replying",
		}

		log.Printf("execTellPlan: execTellPlan operation completed for plan ID %s on branch %s, iteration %d\n", plan.Id, branch, iteration)
		return
	}
	log.Println("execTellPlan - Plan status set to replying")

	state := &activeTellStreamState{
		modelStreamId:       active.ModelStreamId,
		clients:             clients,
		req:                 req,
		auth:                auth,
		currentOrgId:        currentOrgId,
		currentUserId:       currentUserId,
		plan:                plan,
		branch:              branch,
		iteration:           iteration,
		missingFileResponse: missingFileResponse,
	}

	log.Println("execTellPlan - Loading tell plan")
	err = state.loadTellPlan()
	if err != nil {
		return
	}
	log.Println("execTellPlan - Tell plan loaded")

	activatePaths, activatePathsOrdered := state.resolveCurrentStage()

	var tentativeModelConfig shared.ModelRoleConfig
	var tentativeMaxTokens int
	if state.currentStage.TellStage == shared.TellStagePlanning {
		if state.currentStage.PlanningPhase == shared.PlanningPhaseContext {
			log.Println("Tell plan - isContextStage - setting modelConfig to context loader")
			tentativeModelConfig = state.settings.ModelPack.GetArchitect()
			tentativeMaxTokens = state.settings.GetArchitectEffectiveMaxTokens()
		} else {
			plannerConfig := state.settings.ModelPack.Planner
			tentativeModelConfig = plannerConfig.ModelRoleConfig
			tentativeMaxTokens = state.settings.GetPlannerEffectiveMaxTokens()
		}
	} else if state.currentStage.TellStage == shared.TellStageImplementation {
		tentativeModelConfig = state.settings.ModelPack.GetCoder()
		tentativeMaxTokens = state.settings.GetCoderEffectiveMaxTokens()
	} else {
		log.Printf("Tell plan - execTellPlan - unknown tell stage: %s\n", state.currentStage.TellStage)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("execTellPlan: unknown tell stage: %s", state.currentStage.TellStage))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Unknown tell stage",
		}
		return
	}

	ok, tokensWithoutContext := state.dryRunCalculateTokensWithoutContext(tentativeMaxTokens, unfinishedSubtaskReasoning)
	if !ok {
		return
	}

	var planStageSharedMsgs []*types.ExtendedChatMessagePart
	var planningPhaseOnlyMsgs []*types.ExtendedChatMessagePart
	var implementationMsgs []*types.ExtendedChatMessagePart

	if state.currentStage.TellStage == shared.TellStageImplementation {
		implementationMsgs = state.formatModelContext(formatModelContextParams{
			includeMaps:         false,
			smartContextEnabled: req.SmartContext,
			includeApplyScript:  req.ExecEnabled,
		})
	} else if state.currentStage.TellStage == shared.TellStagePlanning {
		// add the shared context between planning and context phases first so it can be cached
		// this is just for the map and any manually loaded contexts - auto contexts will be added later
		planStageSharedMsgs = state.formatModelContext(formatModelContextParams{
			includeMaps:         true,
			smartContextEnabled: req.SmartContext,
			includeApplyScript:  req.ExecEnabled,
			baseOnly:            true,
			cacheControl:        true,
		})

		if state.currentStage.PlanningPhase == shared.PlanningPhaseTasks {
			if req.AutoContext {
				msg := types.ExtendedChatMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: []types.ExtendedChatMessagePart{},
				}
				for _, part := range planStageSharedMsgs {
					msg.Content = append(msg.Content, *part)
				}
				sharedMsgsTokens := model.GetMessagesTokenEstimate(msg)

				tokensRemaining := tentativeMaxTokens - (sharedMsgsTokens + tokensWithoutContext)

				if tokensRemaining < 0 {
					log.Println("tokensRemaining is negative")
					go notify.NotifyErr(notify.SeverityError, fmt.Errorf("tokensRemaining is negative"))

					active.StreamDoneCh <- &shared.ApiError{
						Type:   shared.ApiErrorTypeOther,
						Status: http.StatusInternalServerError,
						Msg:    "Max tokens exceeded before adding context",
					}
					return
				}

				planningPhaseOnlyMsgs = state.formatModelContext(formatModelContextParams{
					includeMaps:          false,
					smartContextEnabled:  req.SmartContext,
					includeApplyScript:   false, // already included in planStageSharedMsgs
					activeOnly:           true,
					activatePaths:        activatePaths,
					activatePathsOrdered: activatePathsOrdered,
					maxTokens:            int(float64(tokensRemaining) * 0.95), // leave a little extra room
				})
			} else {
				// if auto context is disabled, just dump in any remaining auto contexts, since all basic contexts have already been added in planStageSharedMsgs
				planningPhaseOnlyMsgs = state.formatModelContext(formatModelContextParams{
					includeMaps:         false,
					smartContextEnabled: req.SmartContext,
					includeApplyScript:  false, // already included in planStageSharedMsgs
					autoOnly:            true,
				})
			}
		}
	}

	getTellSysPromptParams := getTellSysPromptParams{
		planStageSharedMsgs:   planStageSharedMsgs,
		planningPhaseOnlyMsgs: planningPhaseOnlyMsgs,
		implementationMsgs:    implementationMsgs,
		contextTokenLimit:     tentativeMaxTokens,
	}

	// log.Println("getTellSysPromptParams:\n", spew.Sdump(getTellSysPromptParams))

	sysParts, err := state.getTellSysPrompt(getTellSysPromptParams)
	if err != nil {
		log.Printf("Error getting tell sys prompt: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error getting tell sys prompt: %v", err))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    err.Error(),
		}
		return
	}

	// log.Println("**sysPrompt:**\n", spew.Sdump(sysParts))

	state.messages = []types.ExtendedChatMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: sysParts,
		},
	}

	promptMessage, ok := state.resolvePromptMessage(unfinishedSubtaskReasoning)
	if !ok {
		return
	}

	// log.Println("messages:\n\n", spew.Sdump(state.messages))

	// log.Println("promptMessage:", spew.Sdump(promptMessage))

	state.tokensBeforeConvo =
		model.GetMessagesTokenEstimate(state.messages...) +
			model.GetMessagesTokenEstimate(*promptMessage) +
			state.latestSummaryTokens +
			model.TokensPerRequest

	// print out breakdown of token usage
	log.Printf("Latest summary tokens: %d\n", state.latestSummaryTokens)
	log.Printf("Total tokens before convo: %d\n", state.tokensBeforeConvo)

	var effectiveMaxTokens int
	if state.currentStage.TellStage == shared.TellStagePlanning {
		if state.currentStage.PlanningPhase == shared.PlanningPhaseContext {
			effectiveMaxTokens = state.settings.GetArchitectEffectiveMaxTokens()
		} else {
			effectiveMaxTokens = state.settings.GetPlannerEffectiveMaxTokens()
		}
	} else if state.currentStage.TellStage == shared.TellStageImplementation {
		effectiveMaxTokens = state.settings.GetCoderEffectiveMaxTokens()
	}

	if state.tokensBeforeConvo > effectiveMaxTokens {
		// token limit already exceeded before adding conversation
		err := fmt.Errorf("token limit exceeded before adding conversation")
		log.Printf("Error: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("token limit exceeded before adding conversation"))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Token limit exceeded before adding conversation",
		}
		return
	}

	if !state.addConversationMessages() {
		return
	}

	// add the prompt message to the end of the messages slice
	if promptMessage != nil {
		state.messages = append(state.messages, *promptMessage)
	} else {
		log.Println("promptMessage is nil")
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("promptMessage is nil"))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Prompt message isn't set",
		}
		return
	}

	state.replyId = uuid.New().String()
	state.replyParser = types.NewReplyParser()

	if missingFileResponse != "" && !state.handleMissingFileResponse(unfinishedSubtaskReasoning) {
		return
	}

	// filter out any messages that are empty
	state.messages = model.FilterEmptyMessages(state.messages)

	log.Printf("\n\nMessages: %d\n", len(state.messages))
	// for _, message := range state.messages {
	// 	log.Printf("%s: %v\n", message.Role, message.Content)
	// }

	requestTokens := model.GetMessagesTokenEstimate(state.messages...) + model.TokensPerRequest
	state.totalRequestTokens = requestTokens

	modelConfig := tentativeModelConfig

	log.Println("Tell plan - setting modelConfig")
	log.Println("Tell plan - requestTokens:", requestTokens)
	log.Println("Tell plan - state.currentStage.TellStage:", state.currentStage.TellStage)
	log.Println("Tell plan - state.currentStage.PlanningPhase:", state.currentStage.PlanningPhase)

	if state.currentStage.TellStage == shared.TellStagePlanning {
		if state.currentStage.PlanningPhase == shared.PlanningPhaseContext {
			log.Println("Tell plan - isContextStage - setting modelConfig to context loader")
			modelConfig = state.settings.ModelPack.GetArchitect().GetRoleForInputTokens(requestTokens)
			log.Println("Tell plan - got modelConfig for context phase")
		} else if state.currentStage.PlanningPhase == shared.PlanningPhaseTasks {
			modelConfig = state.settings.ModelPack.Planner.GetRoleForInputTokens(requestTokens)
			log.Println("Tell plan - got modelConfig for tasks phase")
		}
	} else if state.currentStage.TellStage == shared.TellStageImplementation {
		modelConfig = state.settings.ModelPack.GetCoder().GetRoleForInputTokens(requestTokens)
		log.Println("Tell plan - got modelConfig for implementation stage")
	}

	// log.Println("Tell plan - modelConfig:", spew.Sdump(modelConfig))
	state.modelConfig = &modelConfig

	// if the model doesn't support cache control, remove the cache control spec from the messages
	if !modelConfig.BaseModelConfig.SupportsCacheControl {
		for i := range state.messages {
			for j := range state.messages[i].Content {
				if state.messages[i].Content[j].CacheControl != nil {
					state.messages[i].Content[j].CacheControl = nil
				}
			}
		}
	}

	// if the model doesn't support images, remove any image parts from the messages
	if !modelConfig.BaseModelConfig.HasImageSupport {
		log.Println("Tell exec - model doesn't support images. Removing image parts from messages. File name will still be included.")

		for i := range state.messages {
			filteredContent := []types.ExtendedChatMessagePart{}
			for _, part := range state.messages[i].Content {
				if part.Type != openai.ChatMessagePartTypeImageURL {
					filteredContent = append(filteredContent, part)
				}
			}
			state.messages[i].Content = filteredContent
		}
	}

	log.Println("tell exec - will send model request with:", spew.Sdump(map[string]interface{}{
		"provider": modelConfig.BaseModelConfig.Provider,
		"model":    modelConfig.BaseModelConfig.ModelName,
		"tokens":   requestTokens,
	}))

	_, apiErr := hooks.ExecHook(hooks.WillSendModelRequest, hooks.HookParams{
		Auth: auth,
		Plan: plan,
		WillSendModelRequestParams: &hooks.WillSendModelRequestParams{
			InputTokens:  requestTokens,
			OutputTokens: modelConfig.BaseModelConfig.MaxOutputTokens - requestTokens,
			ModelName:    modelConfig.BaseModelConfig.ModelName,
			IsUserPrompt: true,
		},
	})
	if apiErr != nil {
		active.StreamDoneCh <- apiErr
		return
	}

	state.doTellRequest()

	if shouldBuildPending {
		go state.queuePendingBuilds()
	}

	UpdateActivePlan(planId, branch, func(ap *types.ActivePlan) {
		ap.CurrentStreamingReplyId = state.replyId
		ap.CurrentReplyDoneCh = make(chan bool, 1)
	})

}

func (state *activeTellStreamState) doTellRequest() {
	clients := state.clients
	modelConfig := state.modelConfig
	active := state.activePlan

	fallbackRes := modelConfig.GetFallbackForModelError(state.numErrorRetry, state.modelErr)
	modelConfig = fallbackRes.ModelRoleConfig
	stop := []string{"<PlandexFinish/>"}

	// log.Println("Stop:", stop)
	// spew.Dump(state.messages)

	// log.Println("modelConfig:", spew.Sdump(modelConfig))

	if state.noCacheSupportErr {
		log.Println("Tell exec - request failed with cache support error. Removing cache control breakpoints from messages.")
		for i := range state.messages {
			for j := range state.messages[i].Content {
				if state.messages[i].Content[j].CacheControl != nil {
					state.messages[i].Content[j].CacheControl = nil
				}
			}
		}
	}

	modelReq := types.ExtendedChatCompletionRequest{
		Model:    modelConfig.BaseModelConfig.ModelName,
		Messages: state.messages,
		Stream:   true,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
		Temperature: modelConfig.Temperature,
		TopP:        modelConfig.TopP,
	}

	if modelConfig.BaseModelConfig.StopDisabled {
		state.manualStop = stop
	} else {
		modelReq.Stop = stop
	}

	// update state
	state.fallbackRes = fallbackRes
	state.requestStartedAt = time.Now()
	state.originalReq = &modelReq
	state.modelConfig = modelConfig

	// output the modelReq to a json file
	// if jsonData, err := json.MarshalIndent(modelReq, "", "  "); err == nil {
	// 	timestamp := time.Now().Format("2006-01-02-150405")
	// 	filename := fmt.Sprintf("generations/model-request-%s.json", timestamp)
	// 	if err := os.WriteFile(filename, jsonData, 0644); err != nil {
	// 		log.Printf("Error writing model request to file: %v\n", err)
	// 	}
	// } else {
	// 	log.Printf("Error marshaling model request to JSON: %v\n", err)
	// }

	log.Printf("[Tell] doTellRequest retry=%d fallbackRetry=%d using model=%s",
		state.numErrorRetry, state.numFallbackRetry, state.modelConfig.BaseModelConfig.ModelName)

	// start the stream
	stream, err := model.CreateChatCompletionStream(clients, modelConfig, active.ModelStreamCtx, modelReq)
	if err != nil {
		log.Printf("Error starting reply stream: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error starting reply stream: %v", err))
		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Error starting reply stream: " + err.Error(),
		}
		return
	}

	// handle stream chunks
	go state.listenStream(stream)
}

func (state *activeTellStreamState) dryRunCalculateTokensWithoutContext(tentativeMaxTokens int, unfinishedSubtaskReasoning string) (bool, int) {
	clone := &activeTellStreamState{
		modelStreamId:       state.modelStreamId,
		clients:             state.clients,
		req:                 state.req,
		auth:                state.auth,
		currentOrgId:        state.currentOrgId,
		currentUserId:       state.currentUserId,
		plan:                state.plan,
		branch:              state.branch,
		iteration:           state.iteration,
		missingFileResponse: state.missingFileResponse,
		settings:            state.settings,
		currentStage:        state.currentStage,
		subtasks:            state.subtasks,
		currentSubtask:      state.currentSubtask,
		convo:               state.convo,
		summaries:           state.summaries,
		latestSummaryTokens: state.latestSummaryTokens,
		userPrompt:          state.userPrompt,
		promptMessage:       state.promptMessage,
		hasContextMap:       state.hasContextMap,
		contextMapEmpty:     state.contextMapEmpty,
		hasAssistantReply:   state.hasAssistantReply,
		modelContext:        state.modelContext,
		activePlan:          state.activePlan,
	}

	sysParts, err := clone.getTellSysPrompt(getTellSysPromptParams{
		contextTokenLimit:    tentativeMaxTokens,
		dryRunWithoutContext: true,
	})

	if err != nil {
		log.Printf("error getting tell sys prompt for dry run token calculation: %v", err)

		msg := "Error getting tell sys prompt for dry run token calculation"
		if err.Error() == AllTasksCompletedMsg {
			msg = "There's no current task to implement. Try a prompt instead of the 'continue' command."
			go notify.NotifyErr(notify.SeverityInfo, msg)
		} else {
			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error getting tell sys prompt for dry run token calculation: %v", err))
		}

		state.activePlan.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    msg,
		}
		return false, 0
	}

	clone.messages = []types.ExtendedChatMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: sysParts,
		},
	}

	promptMessage, ok := clone.resolvePromptMessage(unfinishedSubtaskReasoning)
	if !ok {
		return false, 0
	}

	clone.tokensBeforeConvo =
		model.GetMessagesTokenEstimate(clone.messages...) +
			model.GetMessagesTokenEstimate(*promptMessage) +
			clone.latestSummaryTokens +
			model.TokensPerRequest

	var effectiveMaxTokens int
	if clone.currentStage.TellStage == shared.TellStagePlanning {
		if clone.currentStage.PlanningPhase == shared.PlanningPhaseContext {
			effectiveMaxTokens = clone.settings.GetArchitectEffectiveMaxTokens()
		} else {
			effectiveMaxTokens = clone.settings.GetPlannerEffectiveMaxTokens()
		}
	} else if clone.currentStage.TellStage == shared.TellStageImplementation {
		effectiveMaxTokens = clone.settings.GetCoderEffectiveMaxTokens()
	}

	if clone.tokensBeforeConvo > effectiveMaxTokens {
		log.Println("tokensBeforeConvo exceeds max tokens during dry run")
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("tokensBeforeConvo exceeds max tokens during dry run"))

		state.activePlan.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Max tokens exceeded before adding conversation",
		}
		return false, 0
	}

	if !clone.addConversationMessages() {
		return false, 0
	}

	clone.messages = append(clone.messages, *promptMessage)

	return true, model.GetMessagesTokenEstimate(clone.messages...) + model.TokensPerRequest
}
</file>

</files>
