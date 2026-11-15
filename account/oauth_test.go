package account

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/adrianmross/appkit/sqldb"
)

// A CLI signs in by device code and by loopback code + PKCE; only a session
// holding approve can let it in, and it never comes out holding approve.
func TestOAuth(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		a := &Accounts{DB: db, App: "demo", EnvPrefix: "DEMO_", TokenMarker: "dmo_", Scopes: []string{"read", "write", ApproveScope},
			OAuth: &OAuth{}, Now: func() time.Time { return now }}
		mux := http.NewServeMux()
		a.Routes(mux)
		srv := httptest.NewServer(a.Handler(mux))
		defer srv.Close()
		owner, err := a.Bootstrap(ctx, "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}

		do := func(req *http.Request) (int, map[string]any) {
			t.Helper()
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var out map[string]any
			_ = json.NewDecoder(res.Body).Decode(&out)
			return res.StatusCode, out
		}
		form := func(path string, v url.Values) (int, map[string]any) {
			req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(v.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			return do(req)
		}
		api := func(method, path, token, body string) (int, map[string]any) {
			req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token)
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Sec-Fetch-Site", "same-origin")
			}
			return do(req)
		}
		_, minted := api("POST", "/v1/tokens", owner, `{"name":"agent","scopes":["write"]}`)
		agent, _ := minted["secret"].(string)

		// Device flow.
		if code, out := form("/v1/auth/device_authorization", url.Values{"scope": {"read approve"}}); code != 400 || out["error"] != "invalid_scope" {
			t.Fatalf("approve through OAuth: %d %v", code, out)
		}
		if code, out := form("/v1/auth/device_authorization", url.Values{"client_id": {"someone-else"}}); code != 400 {
			t.Fatalf("unknown client: %d %v", code, out)
		}
		code, dev := form("/v1/auth/device_authorization", url.Values{"client_id": {"demo-cli"}})
		userCode, _ := dev["user_code"].(string)
		deviceCode, _ := dev["device_code"].(string)
		if code != 200 || userCode == "" || deviceCode == "" || dev["verification_uri"] != srv.URL+"/device" || dev["verification_uri_complete"] != nil {
			t.Fatalf("device start: %d %v", code, dev)
		}
		poll := func() (int, map[string]any) {
			now = now.Add(6 * time.Second)
			return form("/v1/auth/token", url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {deviceCode}, "client_id": {"demo-cli"}})
		}
		if code, out := poll(); code != 400 || out["error"] != "authorization_pending" {
			t.Fatalf("poll before approval: %d %v", code, out)
		}
		approve := `{"user_code":"` + userCode + `","approve":true}`
		if code, out := api("POST", "/v1/oauth/device/approve", agent, approve); code != 403 {
			t.Fatalf("agent approved its own login: %d %v", code, out)
		}
		if code, out := api("GET", "/v1/oauth/device?user_code="+url.QueryEscape(userCode), owner, ""); code != 200 || out["status"] != "pending" || out["client_id"] != "demo-cli" {
			t.Fatalf("lookup: %d %v", code, out)
		}
		if code, out := api("POST", "/v1/oauth/device/approve", owner, approve); code != 200 || out["status"] != "approved" {
			t.Fatalf("approve: %d %v", code, out)
		}
		code, tok := poll()
		cli, _ := tok["access_token"].(string)
		if code != 200 || !strings.HasPrefix(cli, "dmo_") || tok["scope"] != "read write" {
			t.Fatalf("device token: %d %v", code, tok)
		}
		if code, out := poll(); code != 400 || out["access_token"] != nil {
			t.Fatalf("device code used twice: %d %v", code, out)
		}
		if code, me := api("GET", "/v1/me", cli, ""); code != 200 || me["token"].(map[string]any)["name"] != "cli" {
			t.Fatalf("me with the cli token: %d %v", code, me)
		}

		// Loopback code + PKCE.
		verifier := strings.Repeat("v", 64)
		sum := sha256.Sum256([]byte(verifier))
		q := url.Values{"response_type": {"code"}, "client_id": {"demo-cli"}, "redirect_uri": {"http://127.0.0.1:7777/callback"},
			"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "state": {"s1"}, "scope": {"read"}}
		if code, out := api("GET", "/v1/oauth/authorize?"+q.Encode(), owner, ""); code != 200 || out["client_name"] != "demo CLI" || len(out["scopes"].([]any)) != 1 {
			t.Fatalf("authorize check: %d %v", code, out)
		}
		bad := url.Values{}
		for k, v := range q {
			bad[k] = v
		}
		bad.Set("redirect_uri", "http://localhost:7777/callback")
		if code, out := api("GET", "/v1/oauth/authorize?"+bad.Encode(), owner, ""); code != 400 {
			t.Fatalf("localhost redirect: %d %v", code, out)
		}
		body, _ := json.Marshal(map[string]any{"client_id": "demo-cli", "redirect_uri": q.Get("redirect_uri"), "code_challenge": q.Get("code_challenge"),
			"code_challenge_method": "S256", "state": "s1", "scope": "read", "approve": true})
		code, out := api("POST", "/v1/oauth/authorize", owner, string(body))
		to, _ := url.Parse(out["redirect_to"].(string))
		if code != 200 || to.Host != "127.0.0.1:7777" || to.Query().Get("state") != "s1" || to.Query().Get("code") == "" {
			t.Fatalf("authorize: %d %v", code, out)
		}
		exchange := url.Values{"grant_type": {"authorization_code"}, "code": {to.Query().Get("code")}, "code_verifier": {verifier}, "redirect_uri": {q.Get("redirect_uri")}, "client_id": {"demo-cli"}}
		code, tok = form("/v1/auth/token", exchange)
		browser, _ := tok["access_token"].(string)
		if code != 200 || browser == "" || tok["scope"] != "read" {
			t.Fatalf("code exchange: %d %v", code, tok)
		}
		if code, out := form("/v1/auth/token", exchange); code != 400 || out["error"] != "invalid_grant" {
			t.Fatalf("code used twice: %d %v", code, out)
		}

		// Logout revokes; a second revoke is not an error.
		for range 2 {
			if code, out := form("/v1/auth/revoke", url.Values{"token": {browser}}); code != 200 {
				t.Fatalf("revoke: %d %v", code, out)
			}
		}
		if code, _ := api("GET", "/v1/me", browser, ""); code != 401 {
			t.Fatalf("revoked token still works: %d", code)
		}

		// A server-side owner token holds every scope.
		again, err := a.MintOwnerToken(ctx, "recovery")
		if act, aerr := a.Authenticate(ctx, again); err != nil || aerr != nil || !act.Can(ApproveScope) {
			t.Fatalf("owner token: %v %v %+v", err, aerr, act)
		}
	})
}
