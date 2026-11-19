package account

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/sqldb"
)

// testCall sends one request and decodes the JSON answer.
func testCall(t *testing.T, method, url, token, body string, hdr ...string) (int, map[string]any, *http.Response) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out, res
}

// Demo mode mints an isolated, seeded sandbox per call, flags its actor, is
// rate-limited per address, and is a 404 when off.
func TestDemo(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		seeded := []actor.Actor{}
		a := &Accounts{DB: db, App: "demo", TokenMarker: "dmo_", Scopes: []string{"read", "write", ApproveScope}, Now: func() time.Time { return now }}
		mux := http.NewServeMux()
		a.Routes(mux)
		srv := httptest.NewServer(a.Handler(mux))
		defer srv.Close()

		if code, out, _ := testCall(t, "POST", srv.URL+"/v1/demo", "", ""); code != 404 {
			t.Fatalf("demo off: %d %v", code, out)
		}
		if _, out, _ := testCall(t, "GET", srv.URL+"/v1/auth/providers", "", ""); out["demo_available"] != false {
			t.Fatalf("providers, demo off: %v", out)
		}
		a.Demo = &Demo{Seed: func(ctx context.Context) error {
			act, _ := actor.ActorFrom(ctx)
			seeded = append(seeded, act)
			return nil
		}}
		if _, out, _ := testCall(t, "GET", srv.URL+"/v1/auth/providers", "", ""); out["demo_available"] != true {
			t.Fatalf("providers, demo on: %v", out)
		}
		start := func() (token, userID string) {
			t.Helper()
			code, out, _ := testCall(t, "POST", srv.URL+"/v1/demo", "", "")
			token, _ = out["token"].(string)
			userID, _ = out["user_id"].(string)
			if code != 201 || !strings.HasPrefix(token, "dmo_") || userID == "" {
				t.Fatalf("start demo: %d %v", code, out)
			}
			return token, userID
		}
		me := func(token string) (userID, tokenName, email string) {
			t.Helper()
			code, out, _ := testCall(t, "GET", srv.URL+"/v1/me", token, "")
			if code != 200 {
				t.Fatalf("me: %d %v", code, out)
			}
			u, tk := out["user"].(map[string]any), out["token"].(map[string]any)
			return u["id"].(string), tk["name"].(string), u["email"].(string)
		}

		tokenA, userA := start()
		if id, name, email := me(tokenA); id != userA || name != DemoName || !strings.HasPrefix(email, "demo-") || !strings.HasSuffix(email, "@demo.invalid") {
			t.Fatalf("sandbox A: %s %s %s", id, name, email)
		}
		if len(seeded) != 1 || seeded[0].UserID != userA || !seeded[0].Demo || !seeded[0].Can("write") {
			t.Fatalf("seed actor: %+v", seeded)
		}
		// A demo actor is flagged on every later request, so it never approves a CLI login.
		if act, err := a.Authenticate(context.Background(), tokenA); err != nil || !act.Demo {
			t.Fatalf("authenticate demo token: %+v %v", act, err)
		}
		// Replay: a new sandbox, and the old token still works.
		tokenB, userB := start()
		if userB == userA {
			t.Fatal("replaying reused the previous sandbox")
		}
		if id, _, _ := me(tokenA); id != userA {
			t.Fatal("the first sandbox's token stopped working after a replay")
		}
		if id, _, _ := me(tokenB); id != userB {
			t.Fatal("second sandbox")
		}

		for i := 2; i < demoLimit; i++ {
			start()
		}
		if code, out, _ := testCall(t, "POST", srv.URL+"/v1/demo", "", ""); code != 429 {
			t.Fatalf("mint past the limit: %d %v", code, out)
		}
	})
}

// fakeBetterAuth serves a JWKS for one Ed25519 key and signs JWTs with it.
func fakeBetterAuth(t *testing.T) (base string, sign func(email string, verified bool) string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	jwks := `{"keys":[{"kty":"OKP","crv":"Ed25519","alg":"EdDSA","use":"sig","kid":"k1","x":"` + b64(pub) + `"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwks))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func(email string, verified bool) string {
		now := time.Now().Unix()
		claims, _ := json.Marshal(map[string]any{"iss": srv.URL, "sub": email, "email": email, "email_verified": verified, "name": "Neon User", "iat": now, "exp": now + 900})
		in := b64([]byte(`{"alg":"EdDSA","typ":"JWT","kid":"k1"}`)) + "." + b64(claims)
		return in + "." + b64(ed25519.Sign(priv, []byte(in)))
	}
}

func TestBetterAuth(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		base, sign := fakeBetterAuth(t)
		a := &Accounts{DB: db, App: "demo", EnvPrefix: "DEMO_", TokenMarker: "dmo_", Scopes: []string{"read", "write", ApproveScope}}
		mux := http.NewServeMux()
		a.Routes(mux)
		srv := httptest.NewServer(a.Handler(mux))
		defer srv.Close()
		owner, err := a.Bootstrap(ctx, "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		same := []string{"Content-Type", "application/json", "Sec-Fetch-Site", "same-origin"}
		post := func(token string) (int, map[string]any, string) {
			t.Helper()
			code, out, res := testCall(t, "POST", srv.URL+"/v1/auth/session", "", `{"token":"`+token+`"}`, same...)
			return code, out, res.Header.Get("Set-Cookie")
		}

		// Off: a JWT-shaped bearer is just a bad token, and nothing is fetched.
		if code, _, _ := testCall(t, "GET", srv.URL+"/v1/me", "a.b.c", ""); code != 401 {
			t.Fatalf("jwt-shaped token with Better Auth off: %d", code)
		}

		a.BetterAuth = &BetterAuth{BaseURL: base + "/", SignupsOpen: true}
		if a.BetterAuth.Origin() != base {
			t.Fatalf("origin %q", a.BetterAuth.Origin())
		}
		if _, out, _ := testCall(t, "GET", srv.URL+"/v1/auth/providers", "", ""); out["tenant_mode"] != "multi" || out["signups_open"] != true || out["allow_token_login"] != false {
			t.Fatalf("providers: %v", out)
		}

		jwt := sign("neon@example.com", true)
		code, out, set := post(jwt)
		if code != 200 || out["user"].(map[string]any)["email"] != "neon@example.com" {
			t.Fatalf("sign-in: %d %v", code, out)
		}
		if !strings.HasPrefix(set, "demo_session=dmo_") || strings.Contains(set, jwt) {
			t.Fatalf("cookie should hold a dmo_ token, not the JWT: %q", set)
		}
		cookie := []string{"Cookie", strings.SplitN(set, ";", 2)[0], "Sec-Fetch-Site", "same-origin"}
		if code, out, _ := testCall(t, "GET", srv.URL+"/v1/me", "", "", cookie...); code != 200 || out["token"].(map[string]any)["name"] != LoginName {
			t.Fatalf("me via exchanged cookie: %d %v", code, out)
		}
		// The JWT is also a bearer token itself, for the same user.
		if code, out2, _ := testCall(t, "GET", srv.URL+"/v1/me", jwt, ""); code != 200 || out2["user"].(map[string]any)["id"] != out["user"].(map[string]any)["id"] || out2["token"].(map[string]any)["name"] != "betterauth" {
			t.Fatalf("me via jwt bearer: %d %v", code, out2)
		}
		// Sign-out revokes the minted sign-in.
		if code, _, _ := testCall(t, "DELETE", srv.URL+"/v1/auth/session", "", "", cookie...); code != 204 {
			t.Fatalf("sign-out: %d", code)
		}
		if code, _, _ := testCall(t, "GET", srv.URL+"/v1/me", "", "", cookie...); code != 401 {
			t.Fatalf("sign-in should be revoked after sign-out: %d", code)
		}

		if code, _, _ := post(sign("unverified@example.com", false)); code != 401 {
			t.Fatalf("unverified email: %d", code)
		}
		if code, _, _ := post(jwt[:len(jwt)-4] + "AAAA"); code != 401 {
			t.Fatalf("bad signature: %d", code)
		}
		a.BetterAuth.SignupsOpen = false
		if code, out, _ := post(sign("stranger@example.com", true)); code != 403 || !strings.Contains(out["error"].(map[string]any)["message"].(string), "paused") {
			t.Fatalf("sign-ups closed, unknown email: %d %v", code, out)
		}
		if code, _, _ := testCall(t, "GET", srv.URL+"/v1/me", sign("stranger@example.com", true), ""); code != 403 {
			t.Fatalf("sign-ups closed, unknown email as bearer: %d", code)
		}
		if _, err := a.Store().FindUserByEmail(ctx, "stranger@example.com"); err == nil {
			t.Fatal("a user was created while sign-ups were closed")
		}
		// An existing user still signs in while sign-ups are closed.
		if code, out, _ := post(sign("neon@example.com", true)); code != 200 {
			t.Fatalf("existing user, sign-ups closed: %d %v", code, out)
		}
		// The app's own token is stored as is, and sign-out leaves it valid.
		code, _, set = post(owner)
		if code != 200 || !strings.Contains(set, "demo_session="+owner) {
			t.Fatalf("own token passthrough: %d %q", code, set)
		}
		if code, _, _ := testCall(t, "DELETE", srv.URL+"/v1/auth/session", "", "", "Cookie", "demo_session="+owner, "Sec-Fetch-Site", "same-origin"); code != 204 {
			t.Fatalf("owner sign-out: %d", code)
		}
		if code, _, _ := testCall(t, "GET", srv.URL+"/v1/me", owner, ""); code != 200 {
			t.Fatalf("owner token should survive sign-out: %d", code)
		}
	})
}
