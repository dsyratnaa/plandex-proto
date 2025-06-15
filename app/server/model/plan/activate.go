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
