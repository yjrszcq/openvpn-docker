// Package auditactor carries authenticated mutation identity through service calls.
package auditactor

import "context"

type Actor struct {
	Kind string
	ID   string
}

type contextKey struct{}

func With(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, contextKey{}, actor)
}

func From(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(contextKey{}).(Actor)
	return actor, ok && actor.Kind != ""
}

func LocalCLI(ctx context.Context) context.Context {
	return With(ctx, Actor{Kind: "local-cli"})
}
