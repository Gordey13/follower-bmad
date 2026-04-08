package audit

import "context"

type ActorType string

const (
	ActorTypeSystem          ActorType = "system"
	ActorTypeAdminOperator   ActorType = "admin_operator"
	ActorTypeInternalProcess ActorType = "internal_process"
)

type Actor struct {
	Type ActorType
	ID   string
}

type actorContextKey struct{}

func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, normalizeActor(actor))
}

func ActorFromContext(ctx context.Context) Actor {
	if ctx == nil {
		return Actor{
			Type: ActorTypeSystem,
			ID:   "system",
		}
	}

	actor, ok := ctx.Value(actorContextKey{}).(Actor)
	if !ok {
		return Actor{
			Type: ActorTypeSystem,
			ID:   "system",
		}
	}

	return normalizeActor(actor)
}

func normalizeActor(actor Actor) Actor {
	if actor.Type == "" {
		actor.Type = ActorTypeSystem
	}
	if actor.ID == "" {
		actor.ID = "system"
	}

	return actor
}
