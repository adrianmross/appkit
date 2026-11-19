package account

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/adrianmross/appkit/sqldb"
)

const (
	cimdID       = "https://app.example.com/oauth/client.json"
	testVerifier = "vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv"
	testLoopback = "http://127.0.0.1:7777/callback"
)

// clientEnv is a server with third-party clients on and a clock to move.
type clientEnv struct {
	t     *testing.T
	a     *Accounts
	url   string
	owner string
	now   time.Time
}

func newClientEnv(t *testing.T, db *sqldb.DB) *clientEnv {
	e := &clientEnv{t: t, now: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	e.a = &Accounts{DB: db, App: "demo", EnvPrefix: "DEMO_", TokenMarker: "dmo_", Scopes: []string{"read", "write", ApproveScope},
		OAuth: &OAuth{Clients: &OAuthClients{}}, Now: func() time.Time { return e.now }}
	mux := http.NewServeMux()
	e.a.Routes(mux)
	srv := httptest.NewServer(e.a.Handler(mux))
	t.Cleanup(srv.Close)
	e.url = srv.URL
	var err error
	if e.owner, err = e.a.Bootstrap(context.Background(), "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *clientEnv) do(req *http.Request) (int, map[string]any, http.Header) {
	e.t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out, res.Header
}

func (e *clientEnv) form(path string, v url.Values) (int, map[string]any) {
	req, _ := http.NewRequest("POST", e.url+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	code, out, _ := e.do(req)
	return code, out
}

// api calls as token; a body is sent as the dashboard page would send it.
func (e *clientEnv) api(method, path, token string, body any) (int, map[string]any) {
	var rd *strings.Reader = strings.NewReader("")
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, e.url+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	code, out, _ := e.do(req)
	return code, out
}

func (e *clientEnv) register(body string) (int, map[string]any) {
	req, _ := http.NewRequest("POST", e.url+"/v1/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	code, out, _ := e.do(req)
	return code, out
}

func pkceChallenge() string {
	sum := sha256.Sum256([]byte(testVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func authzFor(clientID, redirect, scope string, approve bool) map[string]any {
	return map[string]any{"client_id": clientID, "redirect_uri": redirect, "code_challenge": pkceChallenge(),
		"code_challenge_method": "S256", "state": "st-123", "scope": scope, "approve": approve}
}

func (e *clientEnv) authorize(body map[string]any) (int, map[string]any) {
	return e.api("POST", "/v1/oauth/authorize", e.owner, body)
}

func (e *clientEnv) exchange(clientID, redirect string, out map[string]any, extra url.Values) (int, map[string]any) {
	e.t.Helper()
	u, _ := url.Parse(out["redirect_to"].(string))
	v := url.Values{"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "code_verifier": {testVerifier},
		"redirect_uri": {redirect}, "client_id": {clientID}}
	for k, vs := range extra {
		v[k] = vs
	}
	return e.form("/v1/auth/token", v)
}

// connect registers a client and runs the code flow, returning the token response.
func (e *clientEnv) connect(scope string) (clientID string, tok map[string]any) {
	e.t.Helper()
	_, reg := e.register(`{"client_name":"Claude","redirect_uris":["https://claude.ai/cb"]}`)
	clientID = reg["client_id"].(string)
	code, out := e.authorize(authzFor(clientID, "https://claude.ai/cb", scope, true))
	if code != 200 {
		e.t.Fatalf("authorize: %d %v", code, out)
	}
	status, tok := e.exchange(clientID, "https://claude.ai/cb", out, nil)
	if status != 200 {
		e.t.Fatalf("token: %d %v", status, tok)
	}
	return clientID, tok
}

func (e *clientEnv) refresh(clientID, refresh string, extra url.Values) (int, map[string]any) {
	v := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}
	for k, vs := range extra {
		v[k] = vs
	}
	return e.form("/v1/auth/token", v)
}

func (e *clientEnv) me(token string) int {
	code, _ := e.api("GET", "/v1/me", token, nil)
	return code
}

func clientDocJSON(id string, redirects ...string) []byte {
	b, _ := json.Marshal(map[string]any{"client_id": id, "client_name": "Example App", "redirect_uris": redirects, "token_endpoint_auth_method": "none"})
	return b
}

func TestOAuthClientCIMD(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		e := newClientEnv(t, db)
		fetches := 0
		doc := clientDocJSON(cimdID, "https://app.example.com/callback", "http://localhost/callback")
		e.a.OAuth.Clients.FetchDoc = func(_ context.Context, u string) ([]byte, error) {
			fetches++
			if u != cimdID {
				t.Errorf("fetched %q", u)
			}
			return doc, nil
		}

		// A registered https redirect works end to end, with the client's state kept.
		code, out := e.authorize(authzFor(cimdID, "https://app.example.com/callback", "read", true))
		if code != 200 {
			t.Fatalf("authorize: %d %v", code, out)
		}
		to, _ := url.Parse(out["redirect_to"].(string))
		if to.Host != "app.example.com" || to.Query().Get("code") == "" || to.Query().Get("state") != "st-123" {
			t.Fatalf("redirect_to %v", out["redirect_to"])
		}
		if status, tok := e.exchange(cimdID, "https://app.example.com/callback", out, nil); status != 200 || tok["access_token"] == "" || tok["scope"] != "read" {
			t.Fatalf("token: %d %v", status, tok)
		}

		// The consent check names the client and marks it unverified.
		q := url.Values{"client_id": {cimdID}, "redirect_uri": {"https://app.example.com/callback"}, "code_challenge": {pkceChallenge()},
			"code_challenge_method": {"S256"}, "state": {"s"}, "scope": {"read"}}
		if status, chk := e.api("GET", "/v1/oauth/authorize?"+q.Encode(), e.owner, nil); status != 200 || chk["client_name"] != "Example App" || chk["unverified"] != true {
			t.Fatalf("check: %d %v", status, chk)
		}
		if fetches != 1 {
			t.Errorf("document fetched %d times; the second request should use the cache", fetches)
		}

		// Loopback redirects ignore the port.
		if code, out := e.authorize(authzFor(cimdID, "http://localhost:49152/callback", "read", true)); code != 200 {
			t.Errorf("loopback with port: %d %v", code, out)
		}
		// Anything not declared is refused.
		for _, bad := range []string{"https://evil.example.com/callback", "https://app.example.com/other", "http://127.0.0.1:5000/callback", "https://app.example.com/callback#x"} {
			if code, out := e.authorize(authzFor(cimdID, bad, "read", true)); code != 400 {
				t.Errorf("%s: %d %v", bad, code, out)
			}
		}

		// The document is fetched again after an hour, and a changed one applies.
		e.now = e.now.Add(2 * time.Hour)
		doc = clientDocJSON(cimdID, "https://app.example.com/new")
		if code, out := e.authorize(authzFor(cimdID, "https://app.example.com/new", "read", true)); code != 200 {
			t.Errorf("after refresh: %d %v", code, out)
		}
		if fetches != 2 {
			t.Errorf("fetches = %d, want 2", fetches)
		}

		for name, tc := range map[string]struct {
			id  string
			doc []byte
		}{
			"id mismatch":      {cimdID, clientDocJSON("https://other.example.com/c.json", "https://app.example.com/cb")},
			"no redirect uris": {cimdID, clientDocJSON(cimdID)},
			"custom scheme":    {cimdID, clientDocJSON(cimdID, "myapp://cb")},
			"plain http":       {cimdID, clientDocJSON(cimdID, "http://app.example.com/cb")},
			"not json":         {cimdID, []byte("<html>")},
			"confidential":     {cimdID, []byte(`{"client_id":"` + cimdID + `","redirect_uris":["https://app.example.com/cb"],"token_endpoint_auth_method":"client_secret_basic"}`)},
			"no path":          {"https://app.example.com", nil},
			"dot segment":      {"https://app.example.com/a/../c.json", nil},
			"credentials":      {"https://u:p@app.example.com/c.json", nil},
			"other port":       {"https://app.example.com:8443/c.json", nil},
			"fragment":         {"https://app.example.com/c.json#f", nil},
		} {
			e.now = e.now.Add(2 * time.Hour) // past the cached document
			e.a.OAuth.Clients.FetchDoc = func(context.Context, string) ([]byte, error) { return tc.doc, nil }
			if code, out := e.authorize(authzFor(tc.id, "https://app.example.com/cb", "read", true)); code != 400 {
				t.Errorf("%s: %d %v", name, code, out)
			}
		}
	})
}

func TestOAuthClientRegistration(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		e := newClientEnv(t, db)
		code, doc := e.register(`{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none","scope":"read"}`)
		id, _ := doc["client_id"].(string)
		if code != 201 || !strings.HasPrefix(id, "c_") || doc["token_endpoint_auth_method"] != "none" {
			t.Fatalf("register: %d %v", code, doc)
		}
		if code, out := e.authorize(authzFor(id, "https://claude.ai/api/mcp/auth_callback", "read", true)); code != 200 {
			t.Fatalf("authorize with registered client: %d %v", code, out)
		}
		if code, out := e.authorize(authzFor(id, "https://claude.ai/other", "read", true)); code != 400 {
			t.Errorf("unregistered redirect: %d %v", code, out)
		}
		// OAuth never grants approve, to a registered client either.
		for _, scope := range []string{"approve", "read approve", "read,write,approve"} {
			if code, out := e.authorize(authzFor(id, "https://claude.ai/api/mcp/auth_callback", scope, true)); code != 400 || out["error"].(map[string]any)["code"] != "invalid_scope" {
				t.Errorf("scope %q: %d %v", scope, code, out)
			}
		}

		// A plain client_id nobody registered is refused, unless the app keeps
		// such tools, which then stay loopback only.
		if code, out := e.authorize(authzFor("some-local-tool", testLoopback, "read", true)); code != 400 {
			t.Errorf("unregistered plain client: %d %v", code, out)
		}
		e.a.OAuth.Clients.AnyLoopback = true
		if code, out := e.authorize(authzFor("some-local-tool", testLoopback, "read", true)); code != 200 {
			t.Errorf("unregistered plain client over loopback: %d %v", code, out)
		}
		if code, out := e.authorize(authzFor("some-local-tool", "https://example.com/callback", "read", true)); code != 400 {
			t.Errorf("unregistered plain client over https: %d %v", code, out)
		}

		for name, body := range map[string]string{
			"no redirect":       `{"client_name":"x"}`,
			"custom scheme":     `{"redirect_uris":["myapp://cb"]}`,
			"http non-loopback": `{"redirect_uris":["http://example.com/cb"]}`,
			"fragment":          `{"redirect_uris":["https://example.com/cb#f"]}`,
			"too many":          `{"redirect_uris":["https://a.com/1","https://a.com/2","https://a.com/3","https://a.com/4","https://a.com/5","https://a.com/6"]}`,
		} {
			if code, doc := e.register(body); code != 400 || doc["error"] != "invalid_redirect_uri" {
				t.Errorf("%s: %d %v", name, code, doc)
			}
		}
		for name, body := range map[string]string{
			"secret client": `{"redirect_uris":["https://a.com/cb"],"token_endpoint_auth_method":"client_secret_post"}`,
			"implicit":      `{"redirect_uris":["https://a.com/cb"],"response_types":["token"]}`,
			"password":      `{"redirect_uris":["https://a.com/cb"],"grant_types":["password"]}`,
			"not json":      `nope`,
		} {
			if code, doc := e.register(body); code != 400 || doc["error"] != "invalid_client_metadata" {
				t.Errorf("%s: %d %v", name, code, doc)
			}
		}
		req, _ := http.NewRequest("POST", e.url+"/v1/auth/register", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "text/plain")
		if code, _, _ := e.do(req); code != 400 {
			t.Errorf("non-JSON content type: %d", code)
		}

		e.now = e.now.Add(2 * time.Minute) // a fresh rate-limit window
		var last int
		for i := 0; i < 25; i++ {
			last, _ = e.register(`{"redirect_uris":["https://a.com/cb"]}`)
		}
		if last != http.StatusTooManyRequests {
			t.Errorf("25 registrations in a minute: last status %d", last)
		}
	})
}

func TestFetchClientDocRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) }))
	srv.TLS = &tls.Config{}
	srv.StartTLS()
	defer srv.Close()
	if _, err := FetchClientDoc(context.Background(), srv.URL+"/c.json"); err == nil {
		t.Fatal("fetched a loopback address")
	}
	for _, ip := range []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "fd00::1", "::ffff:10.0.0.1", "224.0.0.1"} {
		if !BlockedIP(netip.MustParseAddr(ip)) {
			t.Errorf("%s should be blocked", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "160.79.104.1", "2606:4700::1111"} {
		if BlockedIP(netip.MustParseAddr(ip)) {
			t.Errorf("%s should be allowed", ip)
		}
	}
}

func TestOAuthIssAndResource(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		e := newClientEnv(t, db)
		iss := e.url
		redirect := func(code int, out map[string]any) *url.URL {
			t.Helper()
			if code != 200 {
				t.Fatalf("authorize: %d %v", code, out)
			}
			u, _ := url.Parse(out["redirect_to"].(string))
			return u
		}

		// Approval and denial both carry iss.
		body := authzFor("demo-cli", testLoopback, "read", true)
		body["resource"] = iss + "/mcp"
		if u := redirect(e.authorize(body)); u.Query().Get("iss") != iss {
			t.Errorf("approved iss = %q, want %q", u.Query().Get("iss"), iss)
		}
		body["approve"] = false
		if u := redirect(e.authorize(body)); u.Query().Get("iss") != iss || u.Query().Get("error") != "access_denied" {
			t.Errorf("denied redirect: %v", u)
		}

		// A resource on another host is refused.
		body["resource"] = "https://evil.example.com/mcp"
		if code, out := e.authorize(body); code != 400 {
			t.Errorf("foreign resource: %d %v", code, out)
		}

		// The token request must name the resource the code was issued for.
		body["resource"], body["approve"] = iss+"/mcp", true
		_, out := e.authorize(body)
		if _, doc := e.exchange("demo-cli", testLoopback, out, url.Values{"resource": {iss + "/other"}}); doc["error"] != "invalid_target" {
			t.Errorf("wrong resource: %v", doc)
		}
		// The wrong-resource attempt burned the code, as any failed exchange does.
		_, out = e.authorize(body)
		status, tok := e.exchange("demo-cli", testLoopback, out, url.Values{"resource": {iss + "/mcp"}})
		if status != 200 || tok["access_token"] == "" {
			t.Fatalf("matching resource: %d %v", status, tok)
		}
		// The CLI's tokens still never expire.
		if tok["expires_in"] != nil || tok["refresh_token"] != nil {
			t.Fatalf("cli token: %v", tok)
		}
		e.now = e.now.Add(400 * 24 * time.Hour)
		if code := e.me(tok["access_token"].(string)); code != 200 {
			t.Errorf("cli token after 400 days: %d", code)
		}

		// A configured public URL is the issuer instead of the request's origin.
		e.a.OAuth.Clients.PublicURL = "https://demo.example.com/"
		body["resource"] = "https://demo.example.com/mcp"
		if u := redirect(e.authorize(body)); u.Query().Get("iss") != "https://demo.example.com" {
			t.Errorf("configured iss: %v", u)
		}
	})
}

func TestOAuthRefreshRotation(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		e := newClientEnv(t, db)
		clientID, tok := e.connect("read write offline_access")
		access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)
		if tok["expires_in"] != float64(3600) || !strings.HasPrefix(refresh, "dmo_r_") || tok["scope"] != "read write" {
			t.Fatalf("token: %v", tok)
		}

		// The access token dies at an hour.
		e.now = e.now.Add(59 * time.Minute)
		if code := e.me(access); code != 200 {
			t.Fatalf("within the hour: %d", code)
		}
		e.now = e.now.Add(2 * time.Minute)
		if code := e.me(access); code != 401 {
			t.Fatalf("after the hour: %d", code)
		}

		// A refresh answers a new pair and retires the old refresh token.
		status, next := e.refresh(clientID, refresh, nil)
		if status != 200 || next["access_token"] == access || next["refresh_token"] == refresh || next["expires_in"] != float64(3600) {
			t.Fatalf("refresh: %d %v", status, next)
		}
		if code := e.me(next["access_token"].(string)); code != 200 {
			t.Fatalf("new access token: %d", code)
		}

		// Replaying the old one after the grace window is a theft signal: the whole connection goes.
		e.now = e.now.Add(RefreshGrace + time.Second)
		if status, doc := e.refresh(clientID, refresh, nil); status != 400 || doc["error"] != "invalid_grant" {
			t.Fatalf("replay: %d %v", status, doc)
		}
		if code := e.me(next["access_token"].(string)); code != 401 {
			t.Errorf("access token survived reuse detection: %d", code)
		}
		if _, doc := e.refresh(clientID, next["refresh_token"].(string), nil); doc["error"] != "invalid_grant" {
			t.Errorf("newest refresh token survived reuse detection: %v", doc)
		}
	})
}

func TestOAuthRefreshLimits(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		e := newClientEnv(t, db)
		clientID, tok := e.connect("read write")
		refresh := tok["refresh_token"].(string)

		if _, doc := e.refresh("c_someone-else", refresh, nil); doc["error"] != "invalid_grant" {
			t.Errorf("other client: %v", doc)
		}
		if _, doc := e.refresh(clientID, "dmo_r_nope", nil); doc["error"] != "invalid_grant" {
			t.Errorf("unknown token: %v", doc)
		}
		if _, doc := e.refresh(clientID, refresh, url.Values{"scope": {"approve"}}); doc["error"] != "invalid_scope" {
			t.Errorf("approve: %v", doc)
		}
		if _, doc := e.refresh(clientID, refresh, url.Values{"resource": {e.url + "/other"}}); doc["error"] != "invalid_target" {
			t.Errorf("resource: %v", doc)
		}

		// Narrowing is allowed and sticks.
		status, narrow := e.refresh(clientID, refresh, url.Values{"scope": {"read"}})
		if status != 200 || narrow["scope"] != "read" {
			t.Fatalf("narrow: %d %v", status, narrow)
		}
		if _, doc := e.refresh(clientID, narrow["refresh_token"].(string), url.Values{"scope": {"read write"}}); doc["error"] != "invalid_scope" {
			t.Errorf("widening: %v", doc)
		}

		// 31 idle days kill the chain even though 90 haven't passed.
		id2, tok2 := e.connect("read")
		e.now = e.now.Add(31 * 24 * time.Hour)
		if _, doc := e.refresh(id2, tok2["refresh_token"].(string), nil); doc["error"] != "invalid_grant" {
			t.Errorf("idle: %v", doc)
		}

		// Active use extends the idle limit but never the 90 day cap.
		id3, tok3 := e.connect("read")
		r := tok3["refresh_token"].(string)
		for i := 0; i < 3; i++ {
			e.now = e.now.Add(29 * 24 * time.Hour)
			status, next := e.refresh(id3, r, nil)
			if status != 200 {
				t.Fatalf("refresh %d: %d %v", i, status, next)
			}
			r = next["refresh_token"].(string)
		}
		e.now = e.now.Add(4 * 24 * time.Hour) // day 91
		if _, doc := e.refresh(id3, r, nil); doc["error"] != "invalid_grant" {
			t.Errorf("absolute cap: %v", doc)
		}
	})
}

func TestOAuthConnectionRevoke(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		e := newClientEnv(t, db)
		live := func(clientID string) (ids []string) {
			_, list := e.api("GET", "/v1/tokens?limit=100", e.owner, nil)
			for _, it := range list["data"].([]any) {
				m := it.(map[string]any)
				if m["client_id"] != nil && m["revoked_at"] == nil && (clientID == "" || m["client_id"] == clientID) {
					ids = append(ids, m["id"].(string))
				}
			}
			return ids
		}

		// Refreshing leaves one live token per connection, listed under its app.
		id, tok := e.connect("read")
		_, next := e.refresh(id, tok["refresh_token"].(string), nil)
		ids := live(id)
		if len(ids) != 1 {
			t.Fatalf("live app tokens after a refresh = %d, want 1", len(ids))
		}
		if code := e.me(tok["access_token"].(string)); code != 401 {
			t.Errorf("the previous access token should be revoked: %d", code)
		}

		// Revoking its token in Settings kills the refresh token too.
		if code, out := e.api("DELETE", "/v1/tokens/"+ids[0], e.owner, nil); code != 200 {
			t.Fatalf("revoke: %d %v", code, out)
		}
		if _, doc := e.refresh(id, next["refresh_token"].(string), nil); doc["error"] != "invalid_grant" {
			t.Errorf("refresh after revoke: %v", doc)
		}

		// RFC 7009: revoking the refresh token ends the connection.
		_, tok = e.connect("read")
		if status, _ := e.form("/v1/auth/revoke", url.Values{"token": {tok["refresh_token"].(string)}}); status != 200 {
			t.Fatalf("revoke refresh: %d", status)
		}
		if code := e.me(tok["access_token"].(string)); code != 401 {
			t.Errorf("access token after revoking its refresh token: %d", code)
		}
		// And so does revoking the access token.
		id, tok = e.connect("read")
		if status, _ := e.form("/v1/auth/revoke", url.Values{"token": {tok["access_token"].(string)}}); status != 200 {
			t.Fatalf("revoke access: %d", status)
		}
		if _, doc := e.refresh(id, tok["refresh_token"].(string), nil); doc["error"] != "invalid_grant" {
			t.Errorf("refresh after revoking the access token: %v", doc)
		}
		if n := len(live("")); n != 0 {
			t.Errorf("live app tokens = %d, want 0", n)
		}
	})
}

func TestOAuthRefreshGraceWindow(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		e := newClientEnv(t, db)
		clientID, tok := e.connect("read write")
		refresh := tok["refresh_token"].(string)

		// Two requests carrying the same refresh token, as a client retrying or
		// refreshing from two threads sends, both succeed and both stay usable.
		s1, a := e.refresh(clientID, refresh, nil)
		e.now = e.now.Add(2 * time.Second)
		s2, b := e.refresh(clientID, refresh, nil)
		if s1 != 200 || s2 != 200 || a["refresh_token"] == b["refresh_token"] {
			t.Fatalf("parallel refresh: %d %v / %d %v", s1, a, s2, b)
		}
		for _, r := range []map[string]any{a, b} {
			if code := e.me(r["access_token"].(string)); code != 200 {
				t.Errorf("access token from a parallel refresh: %d", code)
			}
		}
		// Each child is single-use like any other.
		if status, _ := e.refresh(clientID, a["refresh_token"].(string), nil); status != 200 {
			t.Errorf("child of the first refresh: %d", status)
		}

		// Past the window the same token is reuse and the connection is revoked.
		e.now = e.now.Add(RefreshGrace + time.Second)
		if _, doc := e.refresh(clientID, refresh, nil); doc["error"] != "invalid_grant" {
			t.Errorf("late replay: %v", doc)
		}
		if _, doc := e.refresh(clientID, b["refresh_token"].(string), nil); doc["error"] != "invalid_grant" {
			t.Errorf("sibling survived reuse detection: %v", doc)
		}
	})
}

func TestOAuthMetadata(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		e := newClientEnv(t, db)
		get := func(path string) map[string]any {
			t.Helper()
			req, _ := http.NewRequest("GET", e.url+path, nil)
			code, doc, h := e.do(req)
			if code != 200 {
				t.Fatalf("GET %s: %d %v", path, code, doc)
			}
			if h.Get("Access-Control-Allow-Origin") != "*" {
				t.Errorf("GET %s: no CORS header", path)
			}
			return doc
		}

		as := get("/.well-known/oauth-authorization-server")
		iss := e.url
		if as["issuer"] != iss || as["token_endpoint"] != iss+"/v1/auth/token" || as["authorization_endpoint"] != iss+"/authorize" {
			t.Errorf("server metadata: %v", as)
		}
		if m, _ := as["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
			t.Errorf("code_challenge_methods_supported: %v", m)
		}
		if as["authorization_response_iss_parameter_supported"] != true || as["client_id_metadata_document_supported"] != true || as["registration_endpoint"] != iss+"/v1/auth/register" {
			t.Errorf("advertised features: %v", as)
		}
		// approve is never on offer.
		if got, _ := json.Marshal(as["scopes_supported"]); string(got) != `["read","write","offline_access"]` {
			t.Errorf("scopes_supported: %s", got)
		}
		// Every advertised endpoint must be a route that answers.
		for _, k := range []string{"token_endpoint", "revocation_endpoint", "device_authorization_endpoint", "registration_endpoint"} {
			req, _ := http.NewRequest("POST", as[k].(string), nil)
			if code, _, _ := e.do(req); code == 404 || code == 405 {
				t.Errorf("%s answered %d", k, code)
			}
		}

		rs := get("/.well-known/oauth-protected-resource")
		if rs["resource"] != iss || rs["authorization_servers"].([]any)[0] != iss {
			t.Errorf("resource metadata: %v", rs)
		}
		if rs := get("/.well-known/oauth-protected-resource/mcp"); rs["resource"] != iss+"/mcp" {
			t.Errorf("path-suffixed resource: %v", rs["resource"])
		}
	})
}

// With Clients off nothing new is served: no registration, no discovery
// documents, no refresh grant, and offline_access is not a scope.
func TestOAuthClientsOff(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		e := newClientEnv(t, db)
		e.a.OAuth.Clients = nil
		mux := http.NewServeMux()
		e.a.Routes(mux)
		srv := httptest.NewServer(e.a.Handler(mux))
		defer srv.Close()
		e.url = srv.URL
		if code, _ := e.register(`{"redirect_uris":["https://a.com/cb"]}`); code != 404 {
			t.Errorf("register: %d", code)
		}
		req, _ := http.NewRequest("GET", e.url+"/.well-known/oauth-authorization-server", nil)
		if code, _, _ := e.do(req); code != 404 {
			t.Errorf("metadata: %d", code)
		}
		if _, doc := e.refresh("demo-cli", "dmo_r_x", nil); doc["error"] != "unsupported_grant_type" {
			t.Errorf("refresh grant: %v", doc)
		}
		if code, doc := e.form("/v1/auth/device_authorization", url.Values{"scope": {"read offline_access"}}); code != 400 || doc["error"] != "invalid_scope" {
			t.Errorf("offline_access: %d %v", code, doc)
		}
		if code, out := e.authorize(authzFor(cimdID, "https://app.example.com/callback", "read", true)); code != 400 {
			t.Errorf("url client: %d %v", code, out)
		}
	})
}
