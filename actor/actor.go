// Package actor carries who authorized a request through the context.
package actor

import (
	"context"
	"time"
)

// Actor is the token that authorized a request.
type Actor struct {
	UserID  string
	TokenID string
	Name    string
	Scopes  []string
	// Demo is true for an anonymous preview visitor's throwaway tenant. The
	// app withholds from it whatever would let an anonymous caller drive
	// outbound HTTP from the server, an SSRF vector a verified sign-up
	// doesn't have.
	Demo bool
	// Proxy is true when a trusted proxy authenticated the caller and the
	// request carried no credential of its own: trusted for the dashboard,
	// but never allowed to approve an OAuth login.
	Proxy bool
	// AuthAt is when a dashboard login token last proved who its holder is
	// (its creation); zero for every other token.
	AuthAt time.Time
	// Resource is the RFC 8707 resource an OAuth token was issued for; empty for other tokens.
	Resource string
}

// Can reports whether the actor holds scope. "write" includes "read"; every
// other scope stands alone.
func (a Actor) Can(scope string) bool {
	for _, s := range a.Scopes {
		if s == scope || (s == "write" && scope == "read") {
			return true
		}
	}
	return false
}

type actorKey struct{}

func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

func ActorFrom(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(actorKey{}).(Actor)
	return a, ok
}

type ActorRef struct {
	TokenID string `json:"token_id,omitempty"`
	Name    string `json:"name,omitempty"`
}
