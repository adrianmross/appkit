package account

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/session"
)

// OIDC lets an existing user sign in to the dashboard through an identity
// provider (Google, a company's own). It only runs the code exchange and
// checks the ID token; the user is whoever already has that verified email.
// Nobody is created: a new person still needs the owner to add them.
//
//	GET /v1/auth/{provider}/start     redirects to the provider
//	GET /v1/auth/{provider}/callback  sets the session cookie, redirects to After
//
// The providers are listed in GET /v1/auth/providers; the callback to
// register with each is <dashboard origin>/v1/auth/<name>/callback.
type OIDC struct {
	Providers []OIDCProvider
	// CookieKey signs the short-lived state cookie of the redirect round
	// trip. Empty means a random key per process, which is fine for one
	// instance; set it when several share the sign-in.
	CookieKey []byte
	// After is where the browser lands once signed in. Default "/".
	After string

	mu       sync.Mutex
	resolved map[string]*oidcProvider
}

// OIDCProvider configures one provider. Name is the URL slug ("google").
type OIDCProvider struct {
	Name, Label            string
	IssuerURL              string
	ClientID, ClientSecret string
}

type oidcProvider struct {
	verifier *oidc.IDTokenVerifier
	oauth2   oauth2.Config
}

// provider resolves name's endpoints and keys from its issuer the first
// time it is used, so an identity provider that is down at boot does not
// keep the server from starting.
func (o *OIDC) provider(ctx context.Context, name string) (*oidcProvider, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.CookieKey) == 0 {
		o.CookieKey = make([]byte, 32)
		if _, err := rand.Read(o.CookieKey); err != nil {
			return nil, err
		}
	}
	if p := o.resolved[name]; p != nil {
		return p, nil
	}
	for _, sp := range o.Providers {
		if sp.Name != name {
			continue
		}
		// The provider keeps this context to refresh its keys, so it must outlive the request.
		p, err := oidc.NewProvider(context.WithoutCancel(ctx), sp.IssuerURL)
		if err != nil {
			return nil, fmt.Errorf("oidc provider %s: %w", name, err)
		}
		if o.resolved == nil {
			o.resolved = map[string]*oidcProvider{}
		}
		o.resolved[name] = &oidcProvider{
			verifier: p.Verifier(&oidc.Config{ClientID: sp.ClientID}),
			oauth2: oauth2.Config{
				ClientID: sp.ClientID, ClientSecret: sp.ClientSecret, Endpoint: p.Endpoint(),
				Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
			},
		}
		return o.resolved[name], nil
	}
	return nil, apperr.NotFound("provider")
}

const oidcStateCookie = "oidc_state"

type oidcState struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Exp      int64  `json:"e"`
}

func (o *OIDC) sign(sp oidcState) string {
	b, _ := json.Marshal(sp)
	body := b64.EncodeToString(b)
	mac := hmac.New(sha256.New, o.CookieKey)
	mac.Write([]byte(body))
	return body + "." + b64.EncodeToString(mac.Sum(nil))
}

func (o *OIDC) verify(cookie string) (oidcState, error) {
	var sp oidcState
	body, sig, ok := strings.Cut(cookie, ".")
	want, err := b64.DecodeString(sig)
	mac := hmac.New(sha256.New, o.CookieKey)
	mac.Write([]byte(body))
	if !ok || err != nil || !hmac.Equal(mac.Sum(nil), want) {
		return sp, fmt.Errorf("invalid state cookie")
	}
	b, err := b64.DecodeString(body)
	if err != nil {
		return sp, fmt.Errorf("malformed state cookie")
	}
	return sp, json.Unmarshal(b, &sp)
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b64.EncodeToString(b), nil
}

func (a *Accounts) oidcRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/auth/{provider}/start", a.oidcStart)
	mux.HandleFunc("GET /v1/auth/{provider}/callback", a.oidcCallback)
}

func (a *Accounts) oidcConfig(r *http.Request, p *oidcProvider) *oauth2.Config {
	cfg := p.oauth2
	cfg.RedirectURL = PublicBase(r) + "/v1/auth/" + r.PathValue("provider") + "/callback"
	return &cfg
}

// oidcStart redirects to the provider's consent screen, after setting a
// signed, short-lived cookie with the state and nonce the callback checks.
func (a *Accounts) oidcStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	p, err := a.OIDC.provider(r.Context(), name)
	if err != nil {
		a.oidcFailed(w, name, err)
		return
	}
	state, err := randomToken()
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookie, Value: a.OIDC.sign(oidcState{Provider: name, State: state, Nonce: nonce, Exp: a.now().Add(5 * time.Minute).Unix()}),
		Path: "/v1/auth", HttpOnly: true, Secure: session.IsHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: 300,
	})
	http.Redirect(w, r, a.oidcConfig(r, p).AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
}

// oidcFailed logs why and tells the browser nothing it could probe with.
func (a *Accounts) oidcFailed(w http.ResponseWriter, provider string, err error) {
	if apperr.IsCode(err, "not_found") {
		httpx.WriteErr(w, err)
		return
	}
	a.log().Warn("oidc", "provider", provider, "err", err.Error())
	httpx.WriteErr(w, a.Unauthorized())
}

// oidcIdentity validates the state cookie, exchanges the code and verifies
// the ID token (signature, issuer, audience, nonce, expiry). It returns the
// verified email.
func (a *Accounts) oidcIdentity(w http.ResponseWriter, r *http.Request, p *oidcProvider) (string, error) {
	raw, err := r.Cookie(oidcStateCookie)
	if err != nil {
		return "", fmt.Errorf("missing state cookie")
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/v1/auth", MaxAge: -1})
	sp, err := a.OIDC.verify(raw.Value)
	if err != nil {
		return "", err
	}
	if sp.Provider != r.PathValue("provider") || sp.Exp < a.now().Unix() || r.URL.Query().Get("state") != sp.State {
		return "", fmt.Errorf("state expired or mismatched")
	}
	tok, err := a.oidcConfig(r, p).Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		return "", err
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return "", fmt.Errorf("no id_token in response")
	}
	idToken, err := p.verifier.Verify(r.Context(), rawID)
	if err != nil {
		return "", err
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Nonce         string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", err
	}
	if claims.Nonce != sp.Nonce {
		return "", fmt.Errorf("nonce mismatch")
	}
	if email := strings.TrimSpace(claims.Email); claims.EmailVerified && email != "" {
		return email, nil
	}
	return "", fmt.Errorf("the provider did not vouch for an email")
}

func (a *Accounts) oidcCallback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	p, err := a.OIDC.provider(r.Context(), name)
	if err != nil {
		a.oidcFailed(w, name, err)
		return
	}
	email, err := a.oidcIdentity(w, r, p)
	if err != nil {
		a.oidcFailed(w, name, err)
		return
	}
	var secret string
	err = a.DB.WithTx(originCtx(r), func(ctx context.Context) error {
		user, err := a.Store().FindUserByEmail(ctx, email)
		if err != nil {
			return err
		}
		secret, err = a.mintLogin(ctx, user.ID, "oidc login token", map[string]any{"via": name})
		return err
	})
	if err == nil {
		_, err = a.signIn(w, r, secret)
	}
	if err != nil {
		// An email nobody here has is refused like any other failure.
		if apperr.IsCode(err, "not_found") {
			err = fmt.Errorf("no user has the email the provider returned")
		}
		a.oidcFailed(w, name, err)
		return
	}
	after := a.OIDC.After
	if after == "" {
		after = "/"
	}
	http.Redirect(w, r, after, http.StatusFound)
}
