package account

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/events"
	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/paging"
	"github.com/adrianmross/appkit/session"
)

// The fields ?fields may name on each response.
var (
	TokenFields = []string{"id", "name", "prefix", "scopes", "created_at", "last_used_at", "revoked_at", "client", "ip", "current", "client_id", "expires_at"}
	AuditFields = []string{"id", "token_id", "actor_name", "action", "entity_type", "entity_id", "summary", "metadata", "created_at"}
	EventFields = []string{"id", "subject_type", "subject_id", "kind", "data", "title", "body", "occurred_at", "created_at", "actor"}
)

func (a *Accounts) cookie() session.Cookie {
	name := a.CookieName
	if name == "" {
		name = a.App + "_session"
	}
	_, max := a.ttl()
	return session.Cookie{Name: name, MaxAge: max}
}

// Routes registers the account routes on mux:
//
//	GET    /healthz, /readyz, /version
//	GET    /v1/auth/providers
//	POST   /v1/auth/session     {"token": "..."} to a session cookie
//	DELETE /v1/auth/session
//	GET    /v1/me, PATCH /v1/me
//	GET    /v1/tokens, POST /v1/tokens, DELETE /v1/tokens/{id}
//	GET    /v1/audit
//	GET    /v1/events
//
// Serve the mux through Handler, which authenticates every /v1 request.
func (a *Accounts) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]string{"version": a.Version})
	})
	mux.HandleFunc("GET /v1/auth/providers", a.authProviders)
	mux.HandleFunc("POST /v1/auth/session", a.createSession)
	mux.HandleFunc("DELETE /v1/auth/session", a.deleteSession)
	mux.HandleFunc("GET /v1/me", a.me)
	mux.HandleFunc("PATCH /v1/me", a.updateMe)
	mux.HandleFunc("GET /v1/tokens", a.listTokens)
	mux.HandleFunc("POST /v1/tokens", a.createToken)
	mux.HandleFunc("DELETE /v1/tokens/{id}", a.revokeToken)
	mux.HandleFunc("GET /v1/audit", a.listAudit)
	mux.HandleFunc("GET /v1/events", a.listEvents)
}

// Handler wraps the app's mux with the request log, panic recovery, CORS
// and authentication. A handler behind it reads the caller with Require.
func (a *Accounts) Handler(next http.Handler) http.Handler {
	allow := a.AllowOrigin
	if allow == nil {
		allow = func(string) bool { return false }
	}
	return httpx.Trace{Log: a.log}.Wrap(httpx.CORS(allow, a.authorize(next)))
}

func (a *Accounts) public(path string) bool {
	return !strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/v1/auth/") || (a.Public != nil && a.Public(path))
}

func crossSite() error {
	return &apperr.Error{Code: "forbidden", Message: "cross-origin request refused", Hint: "send Authorization: Bearer <token>, or make the change from the dashboard itself", Status: 403}
}

func (a *Accounts) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.public(r.URL.Path) {
			gate := session.Gate{Cookie: a.cookie(), Authenticate: a.Authenticate, Unauthorized: a.Unauthorized, CrossSite: crossSite}
			act, err := gate.Actor(w, r)
			if err != nil {
				httpx.WriteErr(w, err)
				return
			}
			r = r.WithContext(actor.WithActor(r.Context(), act))
		}
		next.ServeHTTP(w, r)
	})
}

// ReadBody reads a request body of at most limit bytes (1MB when 0). It
// answers the request itself when it returns false.
func ReadBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	if limit <= 0 {
		limit = 1 << 20
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		httpx.WriteErr(w, apperr.Validation("read body: %s", err.Error()))
		return nil, false
	}
	if int64(len(b)) > limit {
		httpx.WriteErr(w, apperr.Validation("request body is too large"))
		return nil, false
	}
	return b, true
}

// Mutate runs a write the way every write route does: ?fields, ?dry_run and
// Idempotency-Key, through httpx.Mutate on this app's store.
func (a *Accounts) Mutate(w http.ResponseWriter, r *http.Request, status int, fields []string, body []byte, fn func(ctx context.Context) (any, error)) {
	httpx.Mutate(a.Store(), w, r, status, fields, body, fn)
}

func (a *Accounts) ready(w http.ResponseWriter, r *http.Request) {
	if !a.Ready.Load() {
		httpx.WriteErr(w, &apperr.Error{Code: "unavailable", Message: "not ready", Hint: "wait until bootstrap finishes", Status: 503})
		return
	}
	if err := a.DB.Ping(r.Context()); err != nil {
		httpx.WriteErr(w, &apperr.Error{Code: "unavailable", Message: "database unreachable", Hint: "check " + a.EnvPrefix + "DATABASE_URL", Status: 503})
		return
	}
	httpx.WriteJSON(w, 200, map[string]string{"status": "ready"})
}

// authProviders tells the sign-in page what it can offer. This layer only
// has token sign-in.
func (a *Accounts) authProviders(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, 200, map[string]any{"tenant_mode": "single", "providers": []any{}, "allow_token_login": true, "demo_available": false, "signups_open": false, "passkeys": false})
}

// createSession checks a token the way any request would, stores it in the
// session cookie and answers like GET /v1/me. Same-origin JSON only, so
// another site can't sign the browser into an account it chose.
func (a *Accounts) createSession(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		httpx.WriteErr(w, apperr.Validation("Content-Type must be application/json"))
		return
	}
	if !session.SameOrigin(r) {
		httpx.WriteErr(w, crossSite())
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Token) == "" {
		httpx.WriteErr(w, apperr.Validation(`body must be {"token": "..."}`))
		return
	}
	token := strings.TrimSpace(in.Token)
	act, err := a.Authenticate(r.Context(), token)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	a.cookie().Set(w, r, token)
	a.me(w, r.WithContext(actor.WithActor(r.Context(), act)))
}

// deleteSession signs the dashboard out. It revokes the cookie's token only
// when sign-in minted it (named "login"); any other token stays valid, since
// the CLI may use it too.
func (a *Accounts) deleteSession(w http.ResponseWriter, r *http.Request) {
	if !session.SameOrigin(r) {
		httpx.WriteErr(w, crossSite())
		return
	}
	if c := a.cookie().Value(r); c != "" {
		if act, err := a.Authenticate(r.Context(), c); err == nil && act.Name == LoginName {
			if _, err := a.RevokeToken(actor.WithActor(r.Context(), act), act.TokenID); err != nil {
				a.log().Warn("revoke login token on sign-out", "err", err.Error())
			}
		}
	}
	a.cookie().Clear(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (a *Accounts) me(w http.ResponseWriter, r *http.Request) {
	if _, err := httpx.Query(r, "fields"); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	me, err := a.Me(r.Context())
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.WriteProjected(w, 200, me, httpx.FieldsOf(r.URL.Query()))
}

func (a *Accounts) updateMe(w http.ResponseWriter, r *http.Request) {
	if _, err := httpx.Query(r, "dry_run", "fields"); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	body, ok := ReadBody(w, r, 0)
	if !ok {
		return
	}
	a.Mutate(w, r, 200, nil, body, func(ctx context.Context) (any, error) {
		var in MePatch
		if err := httpx.Decode(body, &in); err != nil {
			return nil, err
		}
		return a.UpdateMe(ctx, in)
	})
}

func (a *Accounts) createToken(w http.ResponseWriter, r *http.Request) {
	if _, err := httpx.Query(r, "dry_run", "fields"); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	body, ok := ReadBody(w, r, 0)
	if !ok {
		return
	}
	a.Mutate(w, r, 201, nil, body, func(ctx context.Context) (any, error) {
		var in TokenInput
		if err := httpx.Decode(body, &in); err != nil {
			return nil, err
		}
		return a.CreateToken(ctx, in)
	})
}

func (a *Accounts) revokeToken(w http.ResponseWriter, r *http.Request) {
	if _, err := httpx.Query(r, "dry_run"); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	a.Mutate(w, r, 200, nil, nil, func(ctx context.Context) (any, error) {
		return a.RevokeToken(ctx, r.PathValue("id"))
	})
}

// List answers a paged list route: it checks the query parameters against
// params (limit, cursor, sort and fields are always allowed) and ?fields
// against fields, then writes what load returns.
func List(w http.ResponseWriter, r *http.Request, params, fields []string, load func(ctx context.Context, q map[string][]string, p paging.Request) (any, error)) {
	q, err := httpx.Query(r, append([]string{"limit", "cursor", "sort", "fields"}, params...)...)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	p, err := paging.ParseRequest(q)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	want := httpx.FieldsOf(q)
	if err := paging.ValidateFields(want, fields); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	obj, err := load(r.Context(), q, p)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.WriteProjected(w, 200, obj, want)
}

func (a *Accounts) listTokens(w http.ResponseWriter, r *http.Request) {
	List(w, r, nil, TokenFields, func(ctx context.Context, _ map[string][]string, p paging.Request) (any, error) {
		return a.ListTokens(ctx, p)
	})
}

func auditTime(raw, name string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, apperr.Validation("invalid %s", name).WithHint(name + " is a date (2026-10-30) or an RFC 3339 time")
}

func (a *Accounts) listAudit(w http.ResponseWriter, r *http.Request) {
	List(w, r, []string{"q", "since", "until", "actor", "action", "entity_type", "entity_id"}, AuditFields, func(ctx context.Context, q map[string][]string, p paging.Request) (any, error) {
		f := AuditFilter{Q: httpx.First(q, "q"), Actor: httpx.First(q, "actor"), Action: httpx.First(q, "action"), EntityType: httpx.First(q, "entity_type"), EntityID: httpx.First(q, "entity_id")}
		var err error
		if f.Since, err = auditTime(httpx.First(q, "since"), "since"); err != nil {
			return nil, err
		}
		if f.Until, err = auditTime(httpx.First(q, "until"), "until"); err != nil {
			return nil, err
		}
		return a.ListAudit(ctx, f, p)
	})
}

func (a *Accounts) listEvents(w http.ResponseWriter, r *http.Request) {
	List(w, r, []string{"subject_type", "subject_id", "after", "wait_ms"}, EventFields, func(ctx context.Context, q map[string][]string, p paging.Request) (any, error) {
		wait := time.Duration(0)
		if raw := httpx.First(q, "wait_ms"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				return nil, apperr.Validation("invalid wait_ms").WithHint("wait_ms is a non-negative integer up to 60000")
			}
			wait = time.Duration(n) * time.Millisecond
		}
		f := events.Filter{SubjectType: httpx.First(q, "subject_type"), SubjectID: httpx.First(q, "subject_id")}
		return a.ListEvents(ctx, f, httpx.First(q, "after"), p, wait)
	})
}
