package account

import (
	"context"
	"database/sql"
	"errors"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/adrianmross/oauthlite"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/session"
	"github.com/adrianmross/appkit/sqldb"
)

// ApproveScope is the scope of a human signed in as themselves. Only its
// holder approves a CLI login or manages passkeys, and OAuth never grants
// it, so an agent cannot approve its own login. An app that turns OAuth or
// passkeys on lists it in Accounts.Scopes.
const ApproveScope = "approve"

// OAuth turns on sign-in for the app's CLI: the app is its own
// authorization server, a human signed in to the dashboard approves, and the
// CLI gets a normal API token. Two grants share the oauth_grants table:
// loopback authorization code + PKCE (RFC 8252/7636) and device
// authorization (RFC 8628). The flows run in oauthlite.
//
// Public, form-encoded, RFC-shaped errors ({"error": "..."}):
//
//	POST /v1/auth/device_authorization  RFC 8628
//	POST /v1/auth/token                 authorization_code + PKCE, device_code
//	POST /v1/auth/revoke                RFC 7009
//
// For the dashboard's /authorize and /device pages, JSON, from a session
// that holds ApproveScope:
//
//	GET  /v1/oauth/authorize?...        validate a loopback request for the page
//	POST /v1/oauth/authorize            loopback consent; returns redirect_to
//	GET  /v1/oauth/device?user_code=    what a device code is asking for
//	POST /v1/oauth/device/approve       approve or deny a device code
//
// There is no verification_uri_complete (the human types the code), the
// redirect is loopback only, and there is one client: the CLI. Clients lets
// others in.
type OAuth struct {
	// ClientID is the CLI's client_id. Default App + "-cli".
	ClientID string
	// ClientName is what the consent page calls it. Default App + " CLI".
	ClientName string
	// AllowScope may refuse a scope the app's CLI should never hold.
	// ApproveScope is always refused.
	AllowScope func(scope string) bool
	// Clients turns on clients other than the CLI, with refresh tokens and
	// the discovery documents. Nil leaves them off.
	Clients *OAuthClients
}

const oauthTokenName = "cli"

func (a *Accounts) oauthClientID() string {
	if a.OAuth.ClientID != "" {
		return a.OAuth.ClientID
	}
	return a.App + "-cli"
}

func oauthErr(code, desc string) *oauthlite.Error {
	return &oauthlite.Error{Code: code, Description: desc}
}

// oauthScopes accepts space- or comma-separated scopes. Empty means every
// scope OAuth may carry.
func (a *Accounts) oauthScopes(raw string) ([]string, error) {
	ok := func(sc string) bool {
		return sc != ApproveScope && (a.OAuth.AllowScope == nil || a.OAuth.AllowScope(sc))
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == ',' })
	if a.OAuth.Clients != nil {
		parts = slices.DeleteFunc(parts, func(p string) bool { return p == OfflineAccess })
	}
	if len(parts) == 0 {
		for _, sc := range a.scopes() {
			if ok(sc) {
				parts = append(parts, sc)
			}
		}
	}
	for _, p := range parts {
		known := false
		for _, sc := range a.scopes() {
			known = known || sc == p
		}
		if !known || !ok(p) {
			if len(p) > 32 {
				p = p[:32]
			}
			return nil, oauthErr("invalid_scope", "the scope "+strings.Map(func(r rune) rune {
				if r < 0x20 || r == '"' {
					return '?'
				}
				return r
			}, p)+" cannot be granted through OAuth")
		}
	}
	return a.normalizeScopes(parts)
}

// human is the actor allowed to approve a CLI login or manage passkeys.
func (a *Accounts) human(ctx context.Context, hint string) (actor.Actor, error) {
	act, ok := actor.ActorFrom(ctx)
	if !ok || act.UserID == "" {
		return actor.Actor{}, a.Unauthorized()
	}
	if !act.Can(ApproveScope) || act.Demo || act.Proxy {
		return actor.Actor{}, apperr.Forbidden(ApproveScope).WithHint(hint)
	}
	return act, nil
}

func (a *Accounts) oauth() *oauthlite.Server {
	var approver actor.Actor
	return &oauthlite.Server{
		Grants: grantStore{a.DB},
		Approver: func(ctx context.Context) (oauthlite.Approver, error) {
			act, err := a.human(ctx, "approve CLI logins from the dashboard while signed in as yourself")
			if err == nil {
				err = a.sessions().RequireFresh(ctx, act)
			}
			approver = act
			return oauthlite.Approver{UserID: act.UserID, TokenID: act.TokenID, Can: act.Can}, err
		},
		Scopes: a.oauthScopes,
		Issue:  a.issueOAuthToken,
		Audit: func(ctx context.Context, e oauthlite.Event) error {
			return a.Audit(ctx, approver, e.Action, "oauth_grant", e.GrantID, e.Summary, e.Meta)
		},
		Tx:    a.DB.WithTx,
		Now:   a.now,
		NewID: a.ID,
		Client: func(ctx context.Context, id string) (oauthlite.Client, error) {
			c, err := a.resolveClient(ctx, id)
			if err != nil {
				return oauthlite.Client{}, err
			}
			out := oauthlite.Client{ID: c.ID, Name: c.Name}
			// Builtin clients keep the strict loopback rule.
			if c.Source != "builtin" {
				out.Unverified = true
				out.RedirectOK = func(uri string) bool { return redirectMatches(c.RedirectURIs, uri) }
			}
			return out, nil
		},
		ClientIDOK: func(id string) bool {
			if a.OAuth.Clients == nil {
				return id == a.oauthClientID()
			}
			_, ok := a.normalizeClientID(id)
			return ok
		},
		DefaultClientID: a.oauthClientID(),
		RetryDeviceHint: "run " + a.App + " auth login --device again",
	}
}

// oauthRefusal shows a refused dashboard request as the API's own error.
func oauthRefusal(err error) error {
	var r *oauthlite.Refusal
	if !errors.As(err, &r) {
		return err
	}
	switch r.Kind {
	case oauthlite.InvalidScope:
		return &apperr.Error{Code: "invalid_scope", Message: r.Message, Hint: ApproveScope + " is never granted to the CLI", Status: 400}
	case oauthlite.Forbidden:
		return apperr.Forbidden(r.Scope).WithHint(r.Hint)
	case oauthlite.NotFound:
		e := apperr.NotFound("device code")
		if r.Hint != "" {
			e = e.WithHint(r.Hint)
		}
		return e
	case oauthlite.Conflict:
		return apperr.Conflict(r.Message, r.Hint)
	}
	return apperr.Validation("%s", r.Message)
}

func (a *Accounts) issueOAuthToken(ctx context.Context, g oauthlite.Grant, flow string) (oauthlite.Token, error) {
	if a.OAuth.Clients != nil {
		if client, err := a.getOAuthClient(ctx, g.ClientID); err == nil {
			return a.issueClientTokens(ctx, clientGrant{
				userID: g.UserID, approverTokenID: g.ApproverTokenID, resource: g.Resource, grantID: g.ID, flow: flow,
				client: client, scopes: g.Scopes, family: a.ID(), absoluteAt: a.now().Add(RefreshMaxTTL),
			})
		} else if err != sql.ErrNoRows {
			return oauthlite.Token{}, err
		}
	}
	secret, prefix, hash, err := session.Generate(a.TokenMarker)
	if err != nil {
		return oauthlite.Token{}, err
	}
	tok := session.Token{ID: a.ID(), UserID: g.UserID, Name: oauthTokenName, Hash: hash, Prefix: prefix, Scopes: g.Scopes, CreatedAt: a.now()}
	act := actor.Actor{UserID: g.UserID, TokenID: tok.ID, Name: tok.Name, Scopes: g.Scopes}
	err = a.DB.WithTx(ctx, func(ctx context.Context) error {
		if err := a.Store().CreateToken(ctx, tok); err != nil {
			return err
		}
		return a.Audit(ctx, act, "token.create", "token", tok.ID, "cli login token ("+flow+")", map[string]any{"prefix": prefix, "scopes": g.Scopes, "client_id": g.ClientID, "grant_id": g.ID, "approver_token_id": g.ApproverTokenID})
	})
	if err != nil {
		return oauthlite.Token{}, err
	}
	return oauthlite.Token{AccessToken: secret, TokenType: "Bearer", Scope: strings.Join(g.Scopes, " ")}, nil
}

// oauthRevoke is RFC 7009: holding the token is the authority to revoke it,
// and an unknown token is not an error.
func (a *Accounts) oauthRevoke(ctx context.Context, secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return oauthErr("invalid_request", "token is required")
	}
	if done, err := a.revokeRefresh(ctx, secret); done {
		return err
	}
	tok, err := a.Store().GetTokenByHash(ctx, session.Hash(secret))
	if apperr.IsCode(err, "not_found") || (err == nil && tok.RevokedAt != nil) {
		return nil
	}
	if err != nil {
		return err
	}
	return a.DB.WithTx(ctx, func(ctx context.Context) error {
		changed, err := a.Store().RevokeToken(ctx, tok.UserID, tok.ID, a.now())
		if err != nil || !changed {
			return err
		}
		if tok.FamilyID != "" {
			if err := a.revokeTokenFamily(ctx, tok.FamilyID); err != nil {
				return err
			}
		}
		act := actor.Actor{UserID: tok.UserID, TokenID: tok.ID, Name: tok.Name, Scopes: tok.Scopes}
		return a.Audit(ctx, act, "token.revoke", "token", tok.ID, "revoked token "+tok.Name+" (logout)", nil)
	})
}

// grantStore keeps oauthlite's grants in oauth_grants, whose times are text.
type grantStore struct{ db *sqldb.DB }

const grantCols = `id, kind, code_hash, user_code_hash, client_id, scopes, redirect_uri, code_challenge, user_id, approver_token_id, status, interval_seconds, last_polled_at, expires_at, created_at, requested_from, resource`

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func grantTime(s string) time.Time {
	t, _ := sqldb.ParseTime(s) // "" (never polled) is the zero time
	return t
}

func (g grantStore) Create(ctx context.Context, in oauthlite.Grant) error {
	_, err := g.db.Exec(ctx, `INSERT INTO oauth_grants (`+grantCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.Kind, in.CodeHash, nullString(in.UserCodeHash), in.ClientID, strings.Join(in.Scopes, ","), in.RedirectURI,
		in.CodeChallenge, nullString(in.UserID), in.ApproverTokenID, in.Status, in.Interval, sqldb.FormatTime(in.LastPolledAt),
		sqldb.FormatTime(in.ExpiresAt), sqldb.FormatTime(in.CreatedAt), in.RequestedFrom, in.Resource)
	return mapWriteErr(err)
}

func (g grantStore) get(ctx context.Context, cond string, args ...any) (oauthlite.Grant, error) {
	var out oauthlite.Grant
	var userCode, userID sql.NullString
	var scopes, polled, expires, created string
	err := g.db.QueryRow(ctx, `SELECT `+grantCols+` FROM oauth_grants WHERE `+cond, args...).Scan(
		&out.ID, &out.Kind, &out.CodeHash, &userCode, &out.ClientID, &scopes, &out.RedirectURI, &out.CodeChallenge,
		&userID, &out.ApproverTokenID, &out.Status, &out.Interval, &polled, &expires, &created, &out.RequestedFrom, &out.Resource)
	if err == sql.ErrNoRows {
		return oauthlite.Grant{}, oauthlite.ErrNotFound
	}
	if err != nil {
		return oauthlite.Grant{}, err
	}
	out.UserCodeHash, out.UserID = userCode.String, userID.String
	if scopes != "" {
		out.Scopes = strings.Split(scopes, ",")
	}
	out.LastPolledAt, out.ExpiresAt, out.CreatedAt = grantTime(polled), grantTime(expires), grantTime(created)
	return out, nil
}

func (g grantStore) ByCode(ctx context.Context, kind, codeHash string) (oauthlite.Grant, error) {
	return g.get(ctx, `kind=? AND code_hash=?`, kind, codeHash)
}

func (g grantStore) ByUserCode(ctx context.Context, userCodeHash string) (oauthlite.Grant, error) {
	return g.get(ctx, `kind='device' AND user_code_hash=?`, userCodeHash)
}

func (g grantStore) one(ctx context.Context, query string, args ...any) (bool, error) {
	res, err := g.db.Exec(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (g grantStore) Decide(ctx context.Context, id, userID, approverTokenID, status string, now time.Time) (bool, error) {
	return g.one(ctx, `UPDATE oauth_grants SET status=?, user_id=?, approver_token_id=? WHERE id=? AND kind='device' AND status='pending' AND expires_at > ?`, status, userID, approverTokenID, id, sqldb.FormatTime(now))
}

func (g grantStore) Use(ctx context.Context, id string) (bool, error) {
	return g.one(ctx, `UPDATE oauth_grants SET status='used' WHERE id=? AND status='approved'`, id)
}

func (g grantStore) TouchPoll(ctx context.Context, id string, prev, now time.Time, interval int) (bool, error) {
	return g.one(ctx, `UPDATE oauth_grants SET last_polled_at=?, interval_seconds=? WHERE id=? AND last_polled_at=?`, sqldb.FormatTime(now), interval, id, sqldb.FormatTime(prev))
}

func (g grantStore) DeleteExpired(ctx context.Context, before time.Time) error {
	_, err := g.db.Exec(ctx, `DELETE FROM oauth_grants WHERE expires_at < ?`, sqldb.FormatTime(before))
	return err
}

func (a *Accounts) oauthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/device_authorization", a.limited("device", 10, true, a.oauthDeviceAuthorization))
	mux.HandleFunc("POST /v1/auth/token", a.limited("token", 60, true, a.oauthToken))
	mux.HandleFunc("POST /v1/auth/revoke", a.oauthRevokeHTTP)
	mux.HandleFunc("GET /v1/oauth/authorize", a.oauthAuthorizeCheck)
	mux.HandleFunc("POST /v1/oauth/authorize", a.oauthAuthorize)
	mux.HandleFunc("GET /v1/oauth/device", a.oauthDeviceLookup)
	mux.HandleFunc("POST /v1/oauth/device/approve", a.oauthDeviceApprove)
	if a.OAuth.Clients != nil {
		a.oauthClientRoutes(mux)
	}
}

func oauthForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		writeOAuthErr(w, oauthErr("invalid_request", "send an application/x-www-form-urlencoded body"))
		return false
	}
	return true
}

// writeOAuthErr writes an RFC 6749 section 5.2 error. Other errors become
// server_error without leaking details.
func writeOAuthErr(w http.ResponseWriter, err error) {
	var oe *oauthlite.Error
	if !errors.As(err, &oe) {
		oe = oauthErr("server_error", "internal error")
	}
	status := http.StatusBadRequest
	if oe.Code == "server_error" {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, status, oe)
}

// logOAuth records unexpected failures only, never codes or tokens.
func (a *Accounts) logOAuth(endpoint string, err error) {
	var oe *oauthlite.Error
	if !errors.As(err, &oe) {
		a.log().Error("oauth", "endpoint", endpoint, "err", err.Error())
	}
}

// PublicBase is the origin the caller reached the server on.
func PublicBase(r *http.Request) string {
	scheme := "http"
	if session.IsHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = strings.TrimSpace(strings.Split(fh, ",")[0])
	}
	return scheme + "://" + host
}

func (a *Accounts) oauthDeviceAuthorization(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	out, err := a.oauth().DeviceStart(r.Context(), r.PostForm.Get("client_id"), r.PostForm.Get("scope"), PublicBase(r)+"/device", a.clientIP(r))
	if err != nil {
		a.logOAuth("device_authorization", err)
		writeOAuthErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 200, out)
}

func (a *Accounts) oauthToken(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	f := r.PostForm
	var out oauthlite.Token
	var err error
	switch f.Get("grant_type") {
	case "authorization_code":
		out, err = a.oauth().ExchangeCode(r.Context(), f.Get("code"), f.Get("code_verifier"), f.Get("redirect_uri"), f.Get("client_id"), f.Get("resource"))
	case oauthlite.DeviceCodeGrantType:
		out, err = a.oauth().ExchangeDevice(r.Context(), f.Get("device_code"), f.Get("client_id"))
	case "refresh_token":
		if a.OAuth.Clients != nil {
			out, err = a.oauthRefresh(r.Context(), f.Get("refresh_token"), f.Get("client_id"), f.Get("scope"), f.Get("resource"))
			break
		}
		fallthrough
	default:
		grants := "authorization_code or "
		if a.OAuth.Clients != nil {
			grants = "authorization_code, refresh_token or "
		}
		err = oauthErr("unsupported_grant_type", "use "+grants+oauthlite.DeviceCodeGrantType)
	}
	if err != nil {
		a.logOAuth("token", err)
		writeOAuthErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	httpx.WriteJSON(w, 200, out)
}

func (a *Accounts) oauthRevokeHTTP(w http.ResponseWriter, r *http.Request) {
	if !oauthForm(w, r) {
		return
	}
	if err := a.oauthRevoke(r.Context(), r.PostForm.Get("token")); err != nil {
		a.logOAuth("revoke", err)
		writeOAuthErr(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"revoked": true})
}

// sameOriginJSON is the CSRF guard for a POST only the dashboard page makes:
// a JSON body (not a simple form or text request), from this origin.
func sameOriginJSON(r *http.Request) error {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return apperr.Validation("Content-Type must be application/json")
	}
	if !session.SameOrigin(r) {
		return &apperr.Error{Code: "forbidden", Message: "cross-origin request refused", Hint: "approve from the dashboard page itself", Status: 403}
	}
	return nil
}

func (a *Accounts) oauthAuthorizeCheck(w http.ResponseWriter, r *http.Request) {
	q, err := httpx.Query(r, "response_type", "resource", "client_id", "redirect_uri", "code_challenge", "code_challenge_method", "state", "scope")
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	out, err := a.oauth().AuthorizeCheck(r.Context(), oauthlite.AuthorizeInput{
		ClientID: httpx.First(q, "client_id"), RedirectURI: httpx.First(q, "redirect_uri"), CodeChallenge: httpx.First(q, "code_challenge"),
		CodeChallengeMethod: httpx.First(q, "code_challenge_method"), State: httpx.First(q, "state"), Scope: httpx.First(q, "scope"),
		Resource: httpx.First(q, "resource"), Issuer: a.oauthIssuer(r),
	})
	if err != nil {
		httpx.WriteErr(w, oauthRefusal(err))
		return
	}
	httpx.WriteJSON(w, 200, out)
}

// consentBody reads the JSON body of a consent POST into dst.
func consentBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := sameOriginJSON(r); err != nil {
		httpx.WriteErr(w, err)
		return false
	}
	body, ok := ReadBody(w, r, 64<<10)
	if !ok {
		return false
	}
	if err := httpx.Decode(body, dst); err != nil {
		httpx.WriteErr(w, err)
		return false
	}
	return true
}

func (a *Accounts) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	var in oauthlite.AuthorizeInput
	if !consentBody(w, r, &in) {
		return
	}
	in.Issuer = a.oauthIssuer(r)
	to, err := a.oauth().Authorize(r.Context(), in)
	if err != nil {
		httpx.WriteErr(w, oauthRefusal(err))
		return
	}
	if in.Approve && a.OAuth.Clients != nil { // no row for a builtin client, so nothing changes
		_, _ = a.DB.Exec(r.Context(), `UPDATE oauth_clients SET last_used_at=? WHERE id=?`, sqldb.FormatTime(a.now()), in.ClientID)
	}
	httpx.WriteJSON(w, 200, map[string]string{"redirect_to": to})
}

func (a *Accounts) oauthDeviceLookup(w http.ResponseWriter, r *http.Request) {
	q, err := httpx.Query(r, "user_code")
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	out, err := a.oauth().DeviceLookup(r.Context(), httpx.First(q, "user_code"))
	if err != nil {
		httpx.WriteErr(w, oauthRefusal(err))
		return
	}
	httpx.WriteJSON(w, 200, out)
}

func (a *Accounts) oauthDeviceApprove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserCode string `json:"user_code"`
		Approve  bool   `json:"approve"`
	}
	if !consentBody(w, r, &in) {
		return
	}
	status, err := a.oauth().DeviceDecide(r.Context(), in.UserCode, in.Approve)
	if err != nil {
		httpx.WriteErr(w, oauthRefusal(err))
		return
	}
	httpx.WriteJSON(w, 200, map[string]string{"status": status})
}
