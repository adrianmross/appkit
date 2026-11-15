package account

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/paging"
	"github.com/adrianmross/appkit/session"
	"github.com/adrianmross/appkit/sqldb"
)

// Passkeys turns on WebAuthn sign-in for existing users (go-webauthn does
// every check: challenge, origin, RP ID, signature, flags). A passkey is
// only added by a session holding ApproveScope and only signs that user in
// again; login never creates a user. A token stays the recovery path.
//
//	POST   /v1/passkeys/register/begin   -> {publicKey: ...} for navigator.credentials.create
//	POST   /v1/passkeys/register/finish  {name, credential}
//	GET    /v1/passkeys
//	DELETE /v1/passkeys/{id}
//	POST   /v1/auth/passkey/login/begin  -> {publicKey: ...} for navigator.credentials.get
//	POST   /v1/auth/passkey/login/finish credential JSON; sets the session cookie
//
// Once a user has a passkey, a passkey sign-in older than StepUpWindow must
// sign in again before it approves a CLI login or changes passkeys.
// ponytail: the public login routes have no rate limit; put one in front
// (or port pursuit's rate_limits counter) before exposing this to the internet.
type Passkeys struct {
	// RPID is the host the dashboard is served from, without scheme or
	// port: "localhost", "homes.example.com". A passkey is bound to it.
	RPID string
	// Origins are the dashboard's origins: "http://localhost:8788".
	Origins []string
	// DisplayName is what the authenticator shows. Default App.
	DisplayName string

	once sync.Once
	wa   *webauthn.WebAuthn
	err  error
}

// StepUpWindow is how recently a passkey sign-in must have happened to do
// something only its owner should.
const StepUpWindow = 15 * time.Minute

// passkeyChallengeTTL bounds a begin -> finish ceremony.
const passkeyChallengeTTL = 5 * time.Minute

var b64 = base64.RawURLEncoding

func (a *Accounts) webAuthn() (*webauthn.WebAuthn, error) {
	p := a.Passkeys
	p.once.Do(func() {
		name := p.DisplayName
		if name == "" {
			name = a.App
		}
		yes := true
		p.wa, p.err = webauthn.New(&webauthn.Config{
			RPID: strings.TrimSpace(p.RPID), RPDisplayName: name, RPOrigins: p.Origins,
			AuthenticatorSelection: protocol.AuthenticatorSelection{
				ResidentKey:        protocol.ResidentKeyRequirementRequired,
				RequireResidentKey: &yes,
				UserVerification:   protocol.VerificationRequired,
			},
		})
	})
	return p.wa, p.err
}

// Passkey is a passkey as the API shows it. The key itself never leaves the server.
type Passkey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	// Synced is the backup state flag: the passkey is in a cloud keychain
	// rather than bound to one device.
	Synced bool `json:"synced"`
}

type passkeyRow struct {
	id, name, created, lastUsed string
}

type passkeyUser struct {
	handle []byte
	user   User
	creds  []webauthn.Credential
	rows   []passkeyRow
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u *passkeyUser) WebAuthnName() string                       { return u.user.Email }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.user.Name }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func passkeyRefused() error {
	return &apperr.Error{Code: "unauthorized", Message: "passkey not accepted", Hint: "start again; sign in with a token if this keeps failing", Status: 401}
}

func (a *Accounts) passkeyOwner(ctx context.Context) (actor.Actor, error) {
	return a.human(ctx, "manage passkeys from the dashboard while signed in as yourself")
}

func (a *Accounts) hasPasskey(ctx context.Context, userID string) (bool, error) {
	var n int
	err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM webauthn_credentials WHERE user_id=?`, userID).Scan(&n)
	return n > 0, err
}

// loadPasskeyUser reads a user and their credentials. With create, a user
// without a WebAuthn handle gets one.
func (a *Accounts) loadPasskeyUser(ctx context.Context, userID string, create bool) (*passkeyUser, error) {
	user, err := a.Store().GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	handle := func() (h string, err error) {
		err = a.DB.QueryRow(ctx, `SELECT webauthn_user_handle FROM users WHERE id=?`, userID).Scan(&h)
		return h, err
	}
	h, err := handle()
	if err != nil {
		return nil, err
	}
	if h == "" && create {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		// Only if it has none, so two concurrent registrations can't give one user two handles.
		if _, err := a.DB.Exec(ctx, `UPDATE users SET webauthn_user_handle=? WHERE id=? AND webauthn_user_handle=''`, b64.EncodeToString(raw), userID); err != nil {
			return nil, err
		}
		if h, err = handle(); err != nil {
			return nil, err
		}
	}
	u := &passkeyUser{user: user}
	if u.handle, err = b64.DecodeString(h); err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx, `SELECT id, name, credential, created_at, last_used_at FROM webauthn_credentials WHERE user_id=? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r passkeyRow
		var raw string
		var c webauthn.Credential
		if err := rows.Scan(&r.id, &r.name, &raw, &r.created, &r.lastUsed); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		u.rows, u.creds = append(u.rows, r), append(u.creds, c)
	}
	return u, rows.Err()
}

func (a *Accounts) saveChallenge(ctx context.Context, kind, userID string, sess *webauthn.SessionData) error {
	now := a.now()
	_, _ = a.DB.Exec(ctx, `DELETE FROM webauthn_challenges WHERE expires_at < ?`, sqldb.FormatTime(now))
	raw, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO webauthn_challenges (id, kind, user_id, session, expires_at) VALUES (?, ?, ?, ?, ?)`,
		sess.Challenge, kind, userID, string(raw), sqldb.FormatTime(now.Add(passkeyChallengeTTL)))
	return mapWriteErr(err)
}

// takeChallenge spends a challenge: exactly one caller across all replicas
// gets it. A replayed, expired or unknown one is refused.
func (a *Accounts) takeChallenge(ctx context.Context, kind, challenge string) (string, webauthn.SessionData, error) {
	var sess webauthn.SessionData
	now := sqldb.FormatTime(a.now())
	res, err := a.DB.Exec(ctx, `UPDATE webauthn_challenges SET used_at=? WHERE id=? AND kind=? AND used_at='' AND expires_at > ?`, now, challenge, kind, now)
	if err != nil {
		return "", sess, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", sess, &apperr.Error{Code: "unauthorized", Message: "this passkey request expired or was already used", Hint: "start again", Status: 401}
	}
	var userID, raw string
	if err := a.DB.QueryRow(ctx, `SELECT user_id, session FROM webauthn_challenges WHERE id=?`, challenge).Scan(&userID, &raw); err != nil {
		return "", sess, err
	}
	return userID, sess, json.Unmarshal([]byte(raw), &sess)
}

// BeginPasskeyRegistration returns the options for navigator.credentials.create.
func (a *Accounts) BeginPasskeyRegistration(ctx context.Context) (*protocol.CredentialCreation, error) {
	act, err := a.passkeyOwner(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.sessions().RequireFresh(ctx, act); err != nil {
		return nil, err
	}
	wa, err := a.webAuthn()
	if err != nil {
		return nil, err
	}
	u, err := a.loadPasskeyUser(ctx, act.UserID, true)
	if err != nil {
		return nil, err
	}
	creation, sess, err := wa.BeginRegistration(u, webauthn.WithExclusions(webauthn.Credentials(u.creds).CredentialDescriptors()))
	if err != nil {
		return nil, err
	}
	return creation, a.saveChallenge(ctx, "register", act.UserID, sess)
}

// FinishPasskeyRegistration verifies the authenticator's response and stores the passkey.
func (a *Accounts) FinishPasskeyRegistration(ctx context.Context, name string, response json.RawMessage) (Passkey, error) {
	act, err := a.passkeyOwner(ctx)
	if err != nil {
		return Passkey{}, err
	}
	if name, err = requireText("name", name, 80); err != nil {
		return Passkey{}, err
	}
	wa, err := a.webAuthn()
	if err != nil {
		return Passkey{}, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return Passkey{}, apperr.Validation("credential is not a WebAuthn registration response").WithHint("send the browser's PublicKeyCredential.toJSON() as credential")
	}
	userID, sess, err := a.takeChallenge(ctx, "register", parsed.Response.CollectedClientData.Challenge)
	if err != nil {
		return Passkey{}, err
	}
	if userID != act.UserID {
		return Passkey{}, passkeyRefused()
	}
	u, err := a.loadPasskeyUser(ctx, act.UserID, false)
	if err != nil {
		return Passkey{}, err
	}
	cred, err := wa.CreateCredential(u, sess, parsed)
	if err != nil {
		return Passkey{}, passkeyRefused()
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return Passkey{}, err
	}
	now, id := a.now(), b64.EncodeToString(cred.ID)
	err = a.DB.WithTx(ctx, func(ctx context.Context) error {
		if _, err := a.DB.Exec(ctx, `INSERT INTO webauthn_credentials (id, user_id, name, credential, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, '')`,
			id, act.UserID, name, string(raw), sqldb.FormatTime(now)); err != nil {
			return mapWriteErr(err)
		}
		return a.Audit(ctx, act, "passkey.create", "passkey", id, "added passkey "+name, map[string]any{"synced": cred.Flags.BackupState})
	})
	return Passkey{ID: id, Name: name, CreatedAt: now, Synced: cred.Flags.BackupState}, err
}

// BeginPasskeyLogin returns options for navigator.credentials.get with no
// allow list: the authenticator offers whichever passkeys it holds for this
// RP, so the server never learns or reveals which accounts exist.
func (a *Accounts) BeginPasskeyLogin(ctx context.Context) (*protocol.CredentialAssertion, error) {
	wa, err := a.webAuthn()
	if err != nil {
		return nil, err
	}
	assertion, sess, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	return assertion, a.saveChallenge(ctx, "login", "", sess)
}

// mintLogin creates a dashboard sign-in for userID and audits it.
func (a *Accounts) mintLogin(ctx context.Context, userID, summary string, meta map[string]any) (string, error) {
	raw, tok, err := a.sessions().Mint(ctx, userID)
	if err != nil {
		return "", err
	}
	act := actor.Actor{UserID: userID, TokenID: tok.ID, Name: tok.Name, Scopes: tok.Scopes}
	m := map[string]any{"prefix": tok.Prefix}
	for k, v := range meta {
		m[k] = v
	}
	return raw, a.Audit(ctx, act, "token.create", "token", tok.ID, summary, m)
}

// FinishPasskeyLogin verifies an assertion and mints a dashboard sign-in for
// the passkey's owner. An unknown passkey, or one whose user is gone, is
// refused; nothing is created.
func (a *Accounts) FinishPasskeyLogin(ctx context.Context, response json.RawMessage) (string, error) {
	wa, err := a.webAuthn()
	if err != nil {
		return "", err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return "", passkeyRefused()
	}
	_, sess, err := a.takeChallenge(ctx, "login", parsed.Response.CollectedClientData.Challenge)
	if err != nil {
		return "", err
	}
	var found *passkeyUser
	handler := func(_, userHandle []byte) (webauthn.User, error) {
		var userID string
		err := a.DB.QueryRow(ctx, `SELECT id FROM users WHERE webauthn_user_handle=? AND webauthn_user_handle<>''`, b64.EncodeToString(userHandle)).Scan(&userID)
		if err != nil {
			return nil, err
		}
		found, err = a.loadPasskeyUser(ctx, userID, false)
		return found, err
	}
	_, cred, err := wa.ValidatePasskeyLogin(handler, sess, parsed)
	if err != nil {
		return "", passkeyRefused()
	}
	// The counter went backwards: two copies of the key may exist. Synced
	// passkeys report 0 every time and never trip this.
	if cred.Authenticator.CloneWarning {
		return "", &apperr.Error{Code: "unauthorized", Message: "passkey not accepted: its signature counter went backwards", Hint: "sign in another way, then remove this passkey in Settings and add it again", Status: 401}
	}
	raw, err := json.Marshal(cred)
	if err != nil {
		return "", err
	}
	id := b64.EncodeToString(cred.ID)
	var secret string
	err = a.DB.WithTx(ctx, func(ctx context.Context) error {
		if _, err := a.DB.Exec(ctx, `UPDATE webauthn_credentials SET credential=?, last_used_at=? WHERE user_id=? AND id=?`, string(raw), sqldb.FormatTime(a.now()), found.user.ID, id); err != nil {
			return err
		}
		secret, err = a.mintLogin(ctx, found.user.ID, "passkey login token", map[string]any{"via": "passkey", "passkey_id": id})
		return err
	})
	return secret, err
}

func (a *Accounts) ListPasskeys(ctx context.Context) ([]Passkey, error) {
	act, err := a.passkeyOwner(ctx)
	if err != nil {
		return nil, err
	}
	u, err := a.loadPasskeyUser(ctx, act.UserID, false)
	if err != nil {
		return nil, err
	}
	out := make([]Passkey, len(u.rows))
	for i, r := range u.rows {
		created, _ := sqldb.ParseTime(r.created)
		out[i] = Passkey{ID: r.id, Name: r.name, CreatedAt: created, Synced: u.creds[i].Flags.BackupState}
		if t, err := sqldb.ParseTime(r.lastUsed); err == nil && r.lastUsed != "" {
			out[i].LastUsedAt = &t
		}
	}
	return out, nil
}

// DeletePasskey removes one passkey and signs out the user's other
// dashboard sign-ins: whoever relied on it may still hold a session.
func (a *Accounts) DeletePasskey(ctx context.Context, id string) error {
	act, err := a.passkeyOwner(ctx)
	if err != nil {
		return err
	}
	if err := a.sessions().RequireFresh(ctx, act); err != nil {
		return err
	}
	return a.DB.WithTx(ctx, func(ctx context.Context) error {
		res, err := a.DB.Exec(ctx, `DELETE FROM webauthn_credentials WHERE user_id=? AND id=?`, act.UserID, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return apperr.NotFound("passkey")
		}
		if err := a.Audit(ctx, act, "passkey.delete", "passkey", id, "removed a passkey", nil); err != nil {
			return err
		}
		res, err = a.DB.Exec(ctx, `UPDATE api_tokens SET revoked_at=? WHERE user_id=? AND name=? AND id<>? AND revoked_at IS NULL`, sqldb.FormatTime(a.now()), act.UserID, LoginName, act.TokenID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return a.Audit(ctx, act, "session.revoke_others", "token", act.TokenID, fmt.Sprintf("signed out %d other sessions: a passkey was removed", n), map[string]any{"count": n})
		}
		return nil
	})
}

func (a *Accounts) passkeyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/passkeys/register/begin", a.passkeyRegisterBegin)
	mux.HandleFunc("POST /v1/passkeys/register/finish", a.passkeyRegisterFinish)
	mux.HandleFunc("GET /v1/passkeys", a.listPasskeys)
	mux.HandleFunc("DELETE /v1/passkeys/{id}", a.deletePasskey)
	mux.HandleFunc("POST /v1/auth/passkey/login/begin", a.passkeyLoginBegin)
	mux.HandleFunc("POST /v1/auth/passkey/login/finish", a.passkeyLoginFinish)
}

// passkeyBody reads a same-origin JSON body.
func passkeyBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if err := sameOriginJSON(r); err != nil {
		httpx.WriteErr(w, err)
		return nil, false
	}
	return ReadBody(w, r, 0)
}

func writeNoStore(w http.ResponseWriter, v any, err error) {
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 200, v)
}

func (a *Accounts) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if _, ok := passkeyBody(w, r); !ok {
		return
	}
	out, err := a.BeginPasskeyRegistration(r.Context())
	writeNoStore(w, out, err)
}

func (a *Accounts) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	body, ok := passkeyBody(w, r)
	if !ok {
		return
	}
	var in struct {
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := httpx.Decode(body, &in); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	out, err := a.FinishPasskeyRegistration(r.Context(), in.Name, in.Credential)
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.WriteJSON(w, 201, out)
}

// ponytail: one page; nobody has more passkeys than fit in it.
func (a *Accounts) listPasskeys(w http.ResponseWriter, r *http.Request) {
	if _, err := httpx.Query(r, "limit"); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	keys, err := a.ListPasskeys(r.Context())
	if err != nil {
		httpx.WriteErr(w, err)
		return
	}
	httpx.WriteJSON(w, 200, paging.Page[Passkey]{Data: keys, Page: paging.PageMeta{Limit: len(keys), Total: len(keys)}})
}

func (a *Accounts) deletePasskey(w http.ResponseWriter, r *http.Request) {
	if _, err := httpx.Query(r, "dry_run"); err != nil {
		httpx.WriteErr(w, err)
		return
	}
	id := r.PathValue("id")
	a.Mutate(w, r, 200, nil, nil, func(ctx context.Context) (any, error) {
		return map[string]any{"id": id, "deleted": true}, a.DeletePasskey(ctx, id)
	})
}

func (a *Accounts) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if _, ok := passkeyBody(w, r); !ok {
		return
	}
	out, err := a.BeginPasskeyLogin(r.Context())
	writeNoStore(w, out, err)
}

// signIn puts a freshly minted sign-in in the httpOnly cookie, never in the
// page, retires the sign-in the browser held before, and answers like
// GET /v1/me.
func (a *Accounts) signIn(w http.ResponseWriter, r *http.Request, secret string) (actor.Actor, error) {
	act, err := a.Authenticate(r.Context(), secret)
	if err != nil {
		return act, err
	}
	if c := a.cookie().Value(r); c != "" && c != secret {
		if old, err := a.Authenticate(r.Context(), c); err == nil && old.Name == LoginName && old.UserID == act.UserID {
			_, _ = a.Store().RevokeToken(r.Context(), old.UserID, old.TokenID, a.now())
		}
	}
	a.cookie().Set(w, r, secret)
	return act, nil
}

func originCtx(r *http.Request) context.Context {
	return session.WithOrigin(r.Context(), session.Origin{IP: clientIP(r), UserAgent: r.UserAgent()})
}

func (a *Accounts) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	body, ok := passkeyBody(w, r)
	if !ok {
		return
	}
	secret, err := a.FinishPasskeyLogin(originCtx(r), body)
	if err == nil {
		var act actor.Actor
		if act, err = a.signIn(w, r, secret); err == nil {
			a.me(w, r.WithContext(actor.WithActor(r.Context(), act)))
			return
		}
	}
	if err == sql.ErrNoRows {
		err = passkeyRefused()
	}
	httpx.WriteErr(w, err)
}
