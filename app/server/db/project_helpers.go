package db

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func ProjectExists(orgId, projectId string) (bool, error) {
	return ProjectExistsWithContext(context.Background(), orgId, projectId)
}

func ProjectExistsWithContext(ctx context.Context, orgId, projectId string) (bool, error) {
	// Start OpenTelemetry span for project existence check
	tracer := otel.Tracer("plandex-server")
	ctx, span := tracer.Start(ctx, "db.ProjectExists")
	defer span.End()

	// Set span attributes
	span.SetAttributes(
		attribute.String("org.id", orgId),
		attribute.String("project.id", projectId),
	)

	var count int
	err := Conn.QueryRow("SELECT COUNT(*) FROM projects WHERE org_id = $1 AND id = $2", orgId, projectId).Scan(&count)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to check project existence")
		return false, fmt.Errorf("error checking if project exists: %v", err)
	}

	exists := count > 0
	span.SetAttributes(
		attribute.Bool("project.exists", exists),
	)
	span.SetStatus(codes.Ok, "Project existence checked successfully")

	return exists, nil
}

func CreateProject(orgId, name string, tx *sqlx.Tx) (string, error) {
	var projectId string
	err := tx.QueryRow("INSERT INTO projects (org_id, name) VALUES ($1, $2) RETURNING id", orgId, name).Scan(&projectId)

	if err != nil {
		return "", fmt.Errorf("error creating project: %v", err)
	}

	return projectId, nil
}
