package account

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/adrianmross/appkit/actor"
)

// BetterAuth accepts the JWTs of a Better Auth (Neon Auth) instance. This is
// not a redirect flow like OIDC: the dashboard signs the user in with Better
// Auth's own client SDK and sends its JWT here as a bearer token. A bearer
// that looks like a JWT (two dots, no TokenMarker) is verified against the
// instance's keys, and stands for the user with its verified email; a new
// email gets a user when Accounts.MultiTenant has sign-ups open.
// POST /v1/auth/session exchanges one for a sign-in token, because the JWT
// lives about 15 minutes.
type BetterAuth struct {
	// BaseURL is where the instance serves /.well-known/jwks.json.
	BaseURL string
	// Issuer is the JWT's iss. Default BaseURL's origin: Better Auth's iss is
	// the bare scheme and host, not the path its endpoints live under.
	Issuer string
	// Audience, when set, is checked against aud. Empty skips the check.
	Audience string

	once     sync.Once
	verifier *oidc.IDTokenVerifier
}

// Origin is the instance's scheme and host: what the dashboard's CSP
// connect-src must allow for the client SDK.
func (b *BetterAuth) Origin() string {
	if u, err := url.Parse(b.BaseURL); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return b.BaseURL
}

// identity checks the JWT's signature, issuer and audience, and returns the
// email it carries, if Better Auth verified it. A signature only proves the
// caller signed up with that email, not that they own the inbox; without the
// verified claim anyone could sign up as a stranger and be matched into the
// stranger's account.
func (b *BetterAuth) identity(ctx context.Context, raw string) (email, name string, ok bool) {
	b.once.Do(func() {
		base := strings.TrimRight(b.BaseURL, "/")
		issuer := b.Issuer
		if issuer == "" {
			issuer = b.Origin()
		}
		// The key set keeps this context to refresh, so it must outlive the request.
		keys := oidc.NewRemoteKeySet(context.WithoutCancel(ctx), base+"/.well-known/jwks.json")
		// Better Auth signs with EdDSA, not go-oidc's RS256-only default.
		b.verifier = oidc.NewVerifier(issuer, keys, &oidc.Config{SkipClientIDCheck: b.Audience == "", ClientID: b.Audience, SupportedSigningAlgs: []string{"EdDSA"}})
	})
	tok, err := b.verifier.Verify(ctx, raw)
	if err != nil {
		return "", "", false
	}
	var claims struct {
		Email          string `json:"email"`
		EmailVerified  bool   `json:"email_verified"`
		EmailVerified2 bool   `json:"emailVerified"`
		Name           string `json:"name"`
	}
	if err := tok.Claims(&claims); err != nil {
		return "", "", false
	}
	email = strings.TrimSpace(claims.Email)
	return email, claims.Name, email != "" && (claims.EmailVerified || claims.EmailVerified2)
}

func (a *Accounts) isBetterAuthJWT(raw string) bool {
	return a.BetterAuth != nil && !strings.HasPrefix(raw, a.TokenMarker) && strings.Count(raw, ".") == 2
}

// betterAuthUser is the user a Better Auth JWT stands for.
func (a *Accounts) betterAuthUser(ctx context.Context, raw string) (User, error) {
	email, name, ok := a.BetterAuth.identity(ctx, raw)
	if !ok {
		return User{}, a.Unauthorized()
	}
	return a.signupUser(ctx, email, name)
}

func (a *Accounts) betterAuthActor(ctx context.Context, raw string) (actor.Actor, error) {
	user, err := a.betterAuthUser(ctx, raw)
	if err != nil {
		return actor.Actor{}, err
	}
	return actor.Actor{UserID: user.ID, Name: "betterauth", Scopes: append([]string(nil), a.scopes()...)}, nil
}

// betterAuthLogin exchanges a Better Auth JWT for a dashboard sign-in token.
func (a *Accounts) betterAuthLogin(ctx context.Context, raw string) (string, error) {
	user, err := a.betterAuthUser(ctx, raw)
	if err != nil {
		return "", err
	}
	var secret string
	err = a.DB.WithTx(ctx, func(ctx context.Context) error {
		secret, err = a.mintLogin(ctx, user.ID, "oidc login token", nil)
		return err
	})
	return secret, err
}
