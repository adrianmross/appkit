package account

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/events"
	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/migrations"
	"github.com/adrianmross/appkit/paging"
	"github.com/adrianmross/appkit/session"
	"github.com/adrianmross/appkit/sqldb"
)

// each runs fn on SQLite, and on Postgres (in its own schema) when
// APPKIT_TEST_DATABASE_URL is set.
func each(t *testing.T, fn func(t *testing.T, db *sqldb.DB)) {
	ctx := context.Background()
	open := func(t *testing.T, url string) *sqldb.DB {
		db, err := sqldb.Open(url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.SQL.Close() })
		return db
	}
	t.Run("sqlite", func(t *testing.T) {
		db := open(t, "sqlite:"+filepath.Join(t.TempDir(), "a.db"))
		if err := migrations.Up(ctx, db.SQL, db.Dialect, 0); err != nil {
			t.Fatal(err)
		}
		fn(t, db)
	})
	url := os.Getenv("APPKIT_TEST_DATABASE_URL")
	if url == "" {
		return
	}
	t.Run("postgres", func(t *testing.T) {
		schema := "appkit_account_" + strings.ToLower(strings.NewReplacer("/", "_").Replace(t.Name()))
		admin := open(t, url)
		drop := func() { _, _ = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") }
		drop()
		t.Cleanup(drop)
		if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
			t.Fatal(err)
		}
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		db := open(t, url+sep+"search_path="+schema)
		if err := migrations.Up(ctx, db.SQL, db.Dialect, 0); err != nil {
			t.Fatal(err)
		}
		fn(t, db)
	})
}

func TestStore(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		s := Store{DB: db}
		day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
		if err := s.CreateUser(ctx, User{ID: "u", Name: "Ada", Email: "Ada@localhost", CreatedAt: day(1)}); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateUser(ctx, User{ID: "v", Name: "Dup", Email: "Ada@localhost", CreatedAt: day(1)}); !apperr.IsCode(err, "conflict") {
			t.Fatalf("duplicate email: %v", err)
		}
		if u, err := s.FindUserByEmail(ctx, "ada@LOCALHOST"); err != nil || u.ID != "u" || !u.CreatedAt.Equal(day(1)) {
			t.Fatalf("find: %+v %v", u, err)
		}
		if _, err := s.GetUser(ctx, "nope"); !apperr.IsCode(err, "not_found") {
			t.Fatalf("missing user: %v", err)
		}

		exp := day(20)
		for i, name := range []string{"b", "a", LoginName} {
			tok := session.Token{ID: "t" + name, UserID: "u", Name: name, Hash: "h" + name, Prefix: "p", Scopes: []string{"read", "write"}, CreatedAt: day(2 + i)}
			if name == "a" {
				tok.ExpiresAt = &exp
			}
			if err := s.CreateToken(ctx, tok); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.GetTokenByHash(ctx, "ha")
		if err != nil || got.ID != "ta" || len(got.Scopes) != 2 || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) || got.LastUsedAt != nil {
			t.Fatalf("by hash: %+v %v", got, err)
		}
		if err := s.TouchToken(ctx, "ta", day(5), day(4)); err != nil {
			t.Fatal(err)
		}
		_ = s.TouchToken(ctx, "ta", day(6), day(4)) // already newer than staleBefore: no change
		if got, _ = s.GetToken(ctx, "u", "ta"); got.LastUsedAt == nil || !got.LastUsedAt.Equal(day(5)) {
			t.Fatalf("touch: %+v", got.LastUsedAt)
		}
		// Pages of two by name, then the rest by cursor.
		page, err := s.ListTokens(ctx, "u", paging.Request{Limit: 2, Sort: "name"})
		if err != nil || page.Page.Total != 3 || !page.Page.HasMore || page.Data[0].Name != "a" || page.Data[1].Name != "b" || page.Data[0].Hash != "" {
			t.Fatalf("page 1: %+v %v", page, err)
		}
		cur, _ := paging.DecodeCursor(page.Page.NextCursor)
		if page, err = s.ListTokens(ctx, "u", paging.Request{Limit: 2, Sort: "name", Cursor: &cur}); err != nil || len(page.Data) != 1 || page.Data[0].Name != LoginName || page.Page.HasMore {
			t.Fatalf("page 2: %+v %v", page, err)
		}
		// The sign-in created on day 4 is idle by day 10; the others are not sign-ins.
		if err := s.RevokeExpiredLogins(ctx, LoginName, day(10), day(9), day(1)); err != nil {
			t.Fatal(err)
		}
		if got, _ = s.GetToken(ctx, "u", "t"+LoginName); got.RevokedAt == nil {
			t.Fatal("idle sign-in not revoked")
		}
		if ok, err := s.RevokeToken(ctx, "u", "tb", day(10)); err != nil || !ok {
			t.Fatalf("revoke: %v %v", ok, err)
		}
		if ok, _ := s.RevokeToken(ctx, "u", "tb", day(11)); ok {
			t.Fatal("revoked twice")
		}

		for i, action := range []string{"token.create", "listing.note"} {
			if err := s.CreateAudit(ctx, Audit{ID: "a" + action, UserID: "u", ActorName: "Owner", Action: action, EntityType: "x", Summary: "Did 50% of it", Metadata: json.RawMessage(`{"k":1}`), CreatedAt: day(3 + i)}); err != nil {
				t.Fatal(err)
			}
		}
		audit, err := s.ListAudit(ctx, "u", AuditFilter{Q: "50%", Actor: "owner", Since: day(4)}, paging.Request{Limit: 10})
		if err != nil || len(audit.Data) != 1 || audit.Data[0].Action != "listing.note" || string(audit.Data[0].Metadata) != `{"k":1}` {
			t.Fatalf("audit: %+v %v", audit, err)
		}

		rec := httpx.IdemRecord{TokenID: "ta", UserID: "u", Key: "k", Method: "POST", Path: "/x", RequestHash: "h", CreatedAt: day(3)}
		if err := s.InsertIdempotency(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if err := s.InsertIdempotency(ctx, rec); err != httpx.ErrIdempotencyRace {
			t.Fatalf("second insert: %v", err)
		}
		if err := s.FinishIdempotency(ctx, "ta", "k", 201, `{"a":1}`); err != nil {
			t.Fatal(err)
		}
		if r, ok, err := s.GetIdempotency(ctx, "ta", "k"); err != nil || !ok || r.StatusCode != 201 || r.ResponseBody != `{"a":1}` || !r.CreatedAt.Equal(day(3)) {
			t.Fatalf("idempotency: %+v %v %v", r, ok, err)
		}
	})
}

func TestHTTP(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		seeded := ""
		a := &Accounts{DB: db, App: "demo", EnvPrefix: "DEMO_", TokenMarker: "dmo_", Version: "1.2.3",
			OnUserCreated: func(ctx context.Context, u User) error { seeded = u.Email; return nil }}
		mux := http.NewServeMux()
		a.Routes(mux)
		srv := httptest.NewServer(a.Handler(mux))
		defer srv.Close()

		secret, err := a.Bootstrap(ctx, "", "", "", "")
		if err != nil || !strings.HasPrefix(secret, "dmo_") || seeded != "owner@localhost" {
			t.Fatalf("bootstrap: %q %q %v", secret, seeded, err)
		}
		if again, err := a.Bootstrap(ctx, "", "", "", ""); err != nil || again != "" {
			t.Fatalf("second bootstrap issued %q, %v", again, err)
		}

		call := func(method, path, token string, body any, hdr ...string) (int, map[string]any, *http.Response) {
			t.Helper()
			var buf bytes.Buffer
			if body != nil {
				_ = json.NewEncoder(&buf).Encode(body)
			}
			req, _ := http.NewRequest(method, srv.URL+path, &buf)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
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
		at := func(m map[string]any, path ...string) any {
			var v any = m
			for _, p := range path {
				mm, _ := v.(map[string]any)
				v = mm[p]
			}
			return v
		}

		if code, out, _ := call("GET", "/v1/me", "", nil); code != 401 || at(out, "error", "code") != "unauthorized" || !strings.Contains(at(out, "error", "hint").(string), "DEMO_TOKEN") {
			t.Fatalf("no token: %d %v", code, out)
		}
		if code, _, _ := call("GET", "/readyz", "", nil); code != 503 {
			t.Fatalf("readyz before ready: %d", code)
		}
		a.Ready.Store(true)
		if code, out, _ := call("GET", "/readyz", "", nil); code != 200 || out["status"] != "ready" {
			t.Fatalf("readyz: %d %v", code, out)
		}
		code, out, _ := call("GET", "/v1/me", secret, nil)
		if code != 200 || at(out, "user", "email") != "owner@localhost" || at(out, "token", "name") != "owner" {
			t.Fatalf("me: %d %v", code, out)
		}

		// A token is created, replayed for the same Idempotency-Key, and revoked.
		code, out, _ = call("POST", "/v1/tokens", secret, map[string]any{"name": "ci", "scopes": []string{"write"}}, "Idempotency-Key", "k1")
		minted, _ := at(out, "secret").(string)
		id, _ := at(out, "token", "id").(string)
		if code != 201 || !strings.HasPrefix(minted, "dmo_") || id == "" {
			t.Fatalf("create token: %d %v", code, out)
		}
		if code, again, _ := call("POST", "/v1/tokens", secret, map[string]any{"name": "ci", "scopes": []string{"write"}}, "Idempotency-Key", "k1"); code != 201 || at(again, "token", "id") != id {
			t.Fatalf("replay: %d %v", code, again)
		}
		if code, out, _ := call("POST", "/v1/tokens", secret, map[string]any{"name": "x", "scopes": []string{"admin"}}); code != 400 {
			t.Fatalf("unknown scope: %d %v", code, out)
		}
		// A read-only token cannot mint.
		_, ro, _ := call("POST", "/v1/tokens", secret, map[string]any{"name": "ro"})
		if code, out, _ := call("POST", "/v1/tokens", ro["secret"].(string), map[string]any{"name": "more"}); code != 403 {
			t.Fatalf("read-only minted: %d %v", code, out)
		}
		if code, out, _ := call("GET", "/v1/tokens?fields=id,name,current", minted, nil); code != 200 || len(out["data"].([]any)) != 3 {
			t.Fatalf("list tokens: %d %v", code, out)
		}
		if code, out, _ := call("DELETE", "/v1/tokens/"+id, secret, nil); code != 200 || out["revoked_at"] == nil {
			t.Fatalf("revoke: %d %v", code, out)
		}
		if code, _, _ := call("GET", "/v1/me", minted, nil); code != 401 {
			t.Fatalf("revoked token still works: %d", code)
		}
		code, out, _ = call("GET", "/v1/audit?action=token.revoke", secret, nil)
		if rows, _ := out["data"].([]any); code != 200 || len(rows) != 1 || at(rows[0].(map[string]any), "entity_id") != id {
			t.Fatalf("audit: %d %v", code, out)
		}
		if code, out, _ := call("GET", "/v1/audit?nope=1", secret, nil); code != 400 {
			t.Fatalf("unknown parameter: %d %v", code, out)
		}

		// The token signs the dashboard in: same-origin only, then the cookie works alone.
		if code, _, _ := call("POST", "/v1/auth/session", "", map[string]any{"token": secret}); code != 403 {
			t.Fatalf("cross-site sign-in: %d", code)
		}
		code, out, res := call("POST", "/v1/auth/session", "", map[string]any{"token": secret}, "Sec-Fetch-Site", "same-origin")
		if code != 200 || at(out, "user", "name") != "Owner" || len(res.Cookies()) != 1 || res.Cookies()[0].Name != "demo_session" || !res.Cookies()[0].HttpOnly {
			t.Fatalf("sign-in: %d %v %v", code, out, res.Cookies())
		}
		cookie := res.Cookies()[0].Name + "=" + res.Cookies()[0].Value
		if code, _, _ := call("GET", "/v1/me", "", nil, "Cookie", cookie); code != 200 {
			t.Fatalf("cookie me: %d", code)
		}
		if code, _, _ := call("PATCH", "/v1/me", "", map[string]any{"name": "Ada"}, "Cookie", cookie); code != 403 {
			t.Fatalf("cross-site cookie write: %d", code)
		}
		if code, out, _ := call("PATCH", "/v1/me", "", map[string]any{"name": "Ada"}, "Cookie", cookie, "Sec-Fetch-Site", "same-origin"); code != 200 || out["name"] != "Ada" {
			t.Fatalf("update me: %d %v", code, out)
		}

		// The feed: a waiting request gets the event written while it waits.
		owner, _ := a.Store().FirstUser(ctx)
		write := func(id string, d int) {
			at := time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC)
			if err := a.Events().Create(ctx, events.Event{ID: id, UserID: owner.ID, SubjectType: "thing", SubjectID: "1", Kind: "made", OccurredAt: at, CreatedAt: at}); err != nil {
				t.Error(err)
			}
		}
		write("e1", 1)
		go func() { time.Sleep(300 * time.Millisecond); write("e2", 2) }()
		start := time.Now()
		code, out, _ = call("GET", "/v1/events?after=e1&wait_ms=5000", secret, nil)
		if rows, _ := out["data"].([]any); code != 200 || len(rows) != 1 || at(rows[0].(map[string]any), "id") != "e2" || time.Since(start) > 4*time.Second {
			t.Fatalf("follow: %d %v after %s", code, out, time.Since(start))
		}
		if code, out, _ := call("GET", "/v1/events?subject_type=thing", secret, nil); code != 200 || at(out["data"].([]any)[0].(map[string]any), "id") != "e2" {
			t.Fatalf("events newest first: %d %v", code, out)
		}
	})
}
