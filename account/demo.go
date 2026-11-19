package account

import (
	"context"
	"net/http"
	"time"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/session"
)

// Demo turns on "try it without an account": POST /v1/demo gives any visitor
// a throwaway user and a token for it, which the browser then uses like any
// other. Minting again abandons the old sandbox for a new one.
type Demo struct {
	// Seed fills the new sandbox so it isn't empty. ctx carries its actor.
	Seed func(ctx context.Context) error
}

// DemoName names a demo sandbox's token. Authenticate sets Actor.Demo on a
// token with this name: a plain name, not a column, since the flag only
// withholds things (data is isolated by user either way).
const DemoName = "demo"

// CreateDemoSession makes a throwaway user (an unroutable
// demo-<id>@<App>.invalid address, so the app can find and sweep them), a
// token holding every scope, and runs Seed.
func (a *Accounts) CreateDemoSession(ctx context.Context) (string, actor.Actor, error) {
	secret, prefix, hash, err := session.Generate(a.TokenMarker)
	if err != nil {
		return "", actor.Actor{}, err
	}
	var act actor.Actor
	err = a.DB.WithTx(ctx, func(ctx context.Context) error {
		user, err := a.CreateUser(ctx, "Demo visitor", "demo-"+a.ID()+"@"+a.App+".invalid")
		if err != nil {
			return err
		}
		tok := session.Token{ID: a.ID(), UserID: user.ID, Name: DemoName, Hash: hash, Prefix: prefix, Scopes: a.scopes(), CreatedAt: a.now()}
		act = actor.Actor{UserID: user.ID, TokenID: tok.ID, Name: tok.Name, Scopes: tok.Scopes, Demo: true}
		return a.Store().CreateToken(ctx, tok)
	})
	if err != nil {
		return "", actor.Actor{}, err
	}
	if a.Demo != nil && a.Demo.Seed != nil {
		if err := a.Demo.Seed(actor.WithActor(ctx, act)); err != nil {
			return "", actor.Actor{}, err
		}
	}
	return secret, act, nil
}

// demoLimit is how many sandboxes one address may mint a minute: each writes
// a user, a token and the seed data.
const demoLimit = 5

func (a *Accounts) startDemo(w http.ResponseWriter, r *http.Request) {
	if a.Demo == nil {
		httpx.WriteErr(w, apperr.NotFound("demo"))
		return
	}
	if !a.RateAllow(r.Context(), "demo", a.clientIP(r), demoLimit, time.Minute) {
		httpx.WriteErr(w, apperr.RateLimited("too many demo sessions from this address; wait a minute and try again"))
		return
	}
	secret, act, err := a.CreateDemoSession(r.Context())
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"token": secret, "user_id": act.UserID})
}
