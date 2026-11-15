package account

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/adrianmross/appkit/sqldb"
)

// idp is a one-user identity provider: discovery, keys, and a token
// endpoint that vouches for email with the nonce it was last asked for.
type idp struct {
	*httptest.Server
	email    string
	verified bool
	nonce    string
}

func newIDP(t *testing.T) *idp {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	p := &idp{verified: true}
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token", "jwks_uri": p.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		write(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		claims, _ := json.Marshal(map[string]any{"iss": p.URL, "aud": "client-1", "sub": "u1", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"email": p.email, "email_verified": p.verified, "nonce": p.nonce})
		sig, err := signer.Sign(claims)
		if err != nil {
			t.Error(err)
		}
		id, _ := sig.CompactSerialize()
		write(w, map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": id})
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

// A provider signs in the user who already has the email it vouches for,
// and nobody else.
func TestOIDC(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		p := newIDP(t)
		a := &Accounts{DB: db, App: "demo", EnvPrefix: "DEMO_", TokenMarker: "dmo_", Scopes: []string{"read", "write", ApproveScope},
			OIDC: &OIDC{Providers: []OIDCProvider{{Name: "corp", Label: "Corp SSO", IssuerURL: p.URL, ClientID: "client-1", ClientSecret: "s"}}, After: "/home"}}
		mux := http.NewServeMux()
		a.Routes(mux)
		srv := httptest.NewServer(a.Handler(mux))
		defer srv.Close()
		if _, err := a.Bootstrap(ctx, "Ada", "ada@example.com", "", ""); err != nil {
			t.Fatal(err)
		}
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		get := func(path string, cookies ...*http.Cookie) *http.Response {
			t.Helper()
			req, _ := http.NewRequest("GET", srv.URL+path, nil)
			for _, c := range cookies {
				req.AddCookie(c)
			}
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			return res
		}
		// round runs start and callback as a browser would and returns the callback's answer.
		round := func(tamper func(state *string)) *http.Response {
			t.Helper()
			res := get("/v1/auth/corp/start")
			to, _ := url.Parse(res.Header.Get("Location"))
			if res.StatusCode != 302 || !strings.HasPrefix(to.String(), p.URL+"/authorize") || to.Query().Get("redirect_uri") != srv.URL+"/v1/auth/corp/callback" || len(res.Cookies()) != 1 {
				t.Fatalf("start: %d %s", res.StatusCode, to)
			}
			p.nonce = to.Query().Get("nonce")
			state := to.Query().Get("state")
			if tamper != nil {
				tamper(&state)
			}
			return get("/v1/auth/corp/callback?code=c&state="+url.QueryEscape(state), res.Cookies()[0])
		}

		var out map[string]any
		res, _ := http.Get(srv.URL + "/v1/auth/providers")
		_ = json.NewDecoder(res.Body).Decode(&out)
		res.Body.Close()
		if list, _ := out["providers"].([]any); len(list) != 1 || list[0].(map[string]any)["label"] != "Corp SSO" {
			t.Fatalf("providers: %v", out)
		}
		if res := get("/v1/auth/nope/start"); res.StatusCode != 404 {
			t.Fatalf("unknown provider: %d", res.StatusCode)
		}

		p.email = "ADA@example.com"
		res = round(nil)
		var session *http.Cookie
		for _, c := range res.Cookies() {
			if c.Name == "demo_session" {
				session = c
			}
		}
		if res.StatusCode != 302 || res.Header.Get("Location") != "/home" || session == nil {
			t.Fatalf("callback: %d %s %v", res.StatusCode, res.Header.Get("Location"), res.Cookies())
		}
		if act, err := a.Authenticate(ctx, session.Value); err != nil || act.Name != LoginName || !act.Can(ApproveScope) {
			t.Fatalf("the sign-in: %+v %v", act, err)
		}

		if res := round(func(s *string) { *s += "x" }); res.StatusCode != 401 {
			t.Fatalf("wrong state: %d", res.StatusCode)
		}
		p.email = "stranger@example.com"
		if res := round(nil); res.StatusCode != 401 {
			t.Fatalf("an email nobody has signed in: %d", res.StatusCode)
		}
		p.email, p.verified = "ada@example.com", false
		if res := round(nil); res.StatusCode != 401 {
			t.Fatalf("an unverified email signed in: %d", res.StatusCode)
		}
		if n, _ := a.Store().CountUsers(ctx); n != 1 {
			t.Fatalf("sign-in created a user: %d", n)
		}
	})
}
