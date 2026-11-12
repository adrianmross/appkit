// Package session checks API tokens and runs dashboard sign-ins on top of
// them: a sign-in is a token with an idle and an absolute lifetime.
package session

import (
	"context"
	"strings"
	"time"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/apperr"
)

type Token struct {
	ID         string     `json:"id"`
	UserID     string     `json:"-"`
	Name       string     `json:"name"`
	Hash       string     `json:"-"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	// Client and IP say where a dashboard sign-in came from; empty for other tokens.
	Client string `json:"client,omitempty"`
	IP     string `json:"ip,omitempty"`
	// Current is true on the token that made this request.
	Current bool `json:"current,omitempty"`
	// ClientID, ExpiresAt, Resource and FamilyID are set on tokens issued to a
	// registered OAuth client; the family is the consent they all came from.
	ClientID  string     `json:"client_id,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Resource  string     `json:"-"`
	FamilyID  string     `json:"-"`
}

// Store is the token storage a Manager needs. GetTokenByHash answers an
// apperr "not_found" error for an unknown hash.
type Store interface {
	GetTokenByHash(ctx context.Context, hash string) (Token, error)
	CreateToken(ctx context.Context, t Token) error
	// TouchToken sets last_used_at unless it is already newer than staleBefore.
	TouchToken(ctx context.Context, id string, at, staleBefore time.Time) error
	// RevokeExpiredLogins revokes live tokens with this name last used before
	// idleBefore or created before maxBefore.
	RevokeExpiredLogins(ctx context.Context, name string, at, idleBefore, maxBefore time.Time) error
}

// Manager authenticates tokens and mints sign-ins. The app fills in every
// field; HasStepUp may stay nil.
type Manager struct {
	Store Store
	Now   func() time.Time
	NewID func() string
	// LoginName is the name of a token that is a dashboard sign-in. DemoName
	// is the name of a demo tenant's token.
	// ponytail: the kind of a token is its name, as the tokens table stores it
	// today; a kind column when a user-named token must not collide.
	LoginName, DemoName string
	// IdleTTL is how long a sign-in survives without a request, MaxTTL how
	// long it survives at all.
	IdleTTL, MaxTTL time.Duration
	// StepUpWindow is how recently a sign-in must have happened to pass RequireFresh.
	StepUpWindow time.Duration
	// Scopes are what a sign-in may do, stored as given.
	Scopes []string
	// TokenMarker starts every secret Mint generates, for example "pst_".
	TokenMarker string
	// HasStepUp reports whether the user can re-prove who they are (has a
	// passkey). Nil means nobody can, so RequireFresh always passes.
	HasStepUp func(ctx context.Context, userID string) (bool, error)
	// Unauthorized builds the error for a missing, unknown, revoked or expired token.
	Unauthorized func() error
}

// Authenticate turns a token secret into the actor it stands for.
func (m *Manager) Authenticate(ctx context.Context, secret string) (actor.Actor, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return actor.Actor{}, m.Unauthorized()
	}
	tok, err := m.Store.GetTokenByHash(ctx, Hash(secret))
	if apperr.IsCode(err, "not_found") || (err == nil && tok.RevokedAt != nil) {
		return actor.Actor{}, m.Unauthorized()
	}
	if err != nil {
		return actor.Actor{}, err
	}
	now := m.Now()
	login := tok.Name == m.LoginName
	if login {
		last := tok.CreatedAt
		if tok.LastUsedAt != nil {
			last = *tok.LastUsedAt
		}
		if now.Sub(last) > m.IdleTTL || now.Sub(tok.CreatedAt) > m.MaxTTL {
			return actor.Actor{}, m.Unauthorized()
		}
	}
	if tok.ExpiresAt != nil && !now.Before(*tok.ExpiresAt) {
		return actor.Actor{}, m.Unauthorized()
	}
	_ = m.Store.TouchToken(ctx, tok.ID, now, now.Add(-time.Minute))
	a := actor.Actor{UserID: tok.UserID, TokenID: tok.ID, Name: tok.Name, Scopes: tok.Scopes, Demo: tok.Name == m.DemoName, Resource: tok.Resource}
	if login {
		a.AuthAt = tok.CreatedAt
	}
	return a, nil
}

// Mint creates a sign-in token for userID and returns its secret. The caller
// audits it.
func (m *Manager) Mint(ctx context.Context, userID string) (string, Token, error) {
	raw, prefix, hash, err := Generate(m.TokenMarker)
	if err != nil {
		return "", Token{}, err
	}
	now := m.Now()
	// Opportunistic cleanup instead of a sweeper; failure is harmless.
	m.Sweep(ctx, now)
	o, _ := ctx.Value(originKey{}).(Origin)
	tok := Token{
		ID: m.NewID(), UserID: userID, Name: m.LoginName, Hash: hash,
		Prefix: prefix, Scopes: append([]string(nil), m.Scopes...), CreatedAt: now, Client: clientLabel(o.UserAgent), IP: o.IP,
	}
	if err := m.Store.CreateToken(ctx, tok); err != nil {
		return "", Token{}, err
	}
	return raw, tok, nil
}

// Sweep revokes sign-ins past their idle or absolute lifetime. Failure is harmless.
func (m *Manager) Sweep(ctx context.Context, now time.Time) {
	_ = m.Store.RevokeExpiredLogins(ctx, m.LoginName, now, now.Add(-m.IdleTTL), now.Add(-m.MaxTTL))
}

// RequireFresh asks a dashboard sign-in to re-prove itself before a
// sensitive action, but only when the user has a passkey to do it with:
// without one the sign-in keeps working as before. Other tokens pass.
func (m *Manager) RequireFresh(ctx context.Context, a actor.Actor) error {
	if a.Name != m.LoginName || m.HasStepUp == nil || m.Now().Sub(a.AuthAt) <= m.StepUpWindow {
		return nil
	}
	ok, err := m.HasStepUp(ctx, a.UserID)
	if err != nil || !ok {
		return err
	}
	return &apperr.Error{Code: "step_up_required", Message: "confirm it's you with your passkey to continue", Hint: "sign in again with a passkey, then retry", Status: 403}
}

type originKey struct{}

// Origin is where a sign-in request came from.
type Origin struct{ IP, UserAgent string }

// WithOrigin lets Mint record where a sign-in came from.
func WithOrigin(ctx context.Context, o Origin) context.Context {
	return context.WithValue(ctx, originKey{}, o)
}

// clientLabel turns a User-Agent into "Chrome on macOS"; unknown parts drop out.
func clientLabel(ua string) string {
	browser := ""
	for _, b := range [][2]string{{"Edg/", "Edge"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"}} {
		if strings.Contains(ua, b[0]) {
			browser = b[1]
			break
		}
	}
	os := ""
	for _, o := range [][2]string{{"iPhone", "iOS"}, {"iPad", "iOS"}, {"Android", "Android"}, {"Mac OS X", "macOS"}, {"Windows", "Windows"}, {"Linux", "Linux"}} {
		if strings.Contains(ua, o[0]) {
			os = o[1]
			break
		}
	}
	switch {
	case browser != "" && os != "":
		return browser + " on " + os
	case browser != "":
		return browser
	}
	return os
}
