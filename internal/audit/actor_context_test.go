package audit

import (
	"context"
	"testing"
)

func TestActorFromContextDefaultsToSystem(t *testing.T) {
	t.Parallel()

	actor := ActorFromContext(context.Background())
	if actor.Type != ActorTypeSystem {
		t.Fatalf("expected default actor type %q, got %q", ActorTypeSystem, actor.Type)
	}
	if actor.ID != "system" {
		t.Fatalf("expected default actor id %q, got %q", "system", actor.ID)
	}
}

func TestActorFromContextUsesProvidedActor(t *testing.T) {
	t.Parallel()

	ctx := WithActor(context.Background(), Actor{
		Type: ActorTypeAdminOperator,
		ID:   "admin-01",
	})

	actor := ActorFromContext(ctx)
	if actor.Type != ActorTypeAdminOperator {
		t.Fatalf("expected actor type %q, got %q", ActorTypeAdminOperator, actor.Type)
	}
	if actor.ID != "admin-01" {
		t.Fatalf("expected actor id %q, got %q", "admin-01", actor.ID)
	}
}
