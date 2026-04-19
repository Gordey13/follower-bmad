package httptransport

import (
	"context"
	stdhttp "net/http"
	"strings"

	"follower/internal/audit"
	"follower/internal/observability"

	"github.com/google/uuid"
)

const (
	correlationIDHeader = "X-Correlation-ID"
	adminActorHeader    = "X-Admin-Actor"
)

func prepareAdminMutationContext(
	r *stdhttp.Request,
	taskID string,
	adminAction string,
) (context.Context, adminMetaEnvelope) {
	correlationID := strings.TrimSpace(r.Header.Get(correlationIDHeader))
	if correlationID == "" {
		correlationID = uuid.NewString()
	}

	normalizedTaskID := strings.TrimSpace(taskID)
	if normalizedTaskID == "" {
		normalizedTaskID = "n/a"
	}

	ctx := r.Context()
	ctx = observability.WithAdminRequestContext(ctx, observability.AdminRequestContext{
		CorrelationID: correlationID,
		AdminAction:   strings.TrimSpace(adminAction),
		TaskID:        normalizedTaskID,
	})
	ctx = audit.WithActor(ctx, adminActorFromRequest(r))

	return ctx, adminMetaEnvelope{
		CorrelationID: correlationID,
	}
}

func adminActorFromRequest(r *stdhttp.Request) audit.Actor {
	actorID := strings.TrimSpace(r.Header.Get(adminActorHeader))
	if actorID == "" {
		actorID = strings.TrimSpace(r.Header.Get("User-Agent"))
	}
	if actorID == "" {
		actorID = "system"
	}

	return audit.Actor{
		Type: audit.ActorTypeAdminOperator,
		ID:   actorID,
	}
}
