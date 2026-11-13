// Package account is what makes an app have accounts: users, API tokens,
// dashboard sign-in sessions, the audit log, and the HTTP routes for them.
// It works on the tables of the platform migration set.
package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/events"
	"github.com/adrianmross/appkit/paging"
	"github.com/adrianmross/appkit/session"
	"github.com/adrianmross/appkit/sqldb"
)

// Accounts is the account layer of one app. DB, App and TokenMarker are
// required; everything else has a default.
type Accounts struct {
	DB *sqldb.DB
	// App is the app's command name ("mvin") and EnvPrefix starts its
	// environment variables ("MVIN_"). Both only appear in error hints.
	App, EnvPrefix string
	// TokenMarker starts every token secret, for example "mvn_".
	TokenMarker string
	// Scopes are the scopes a token may hold, least first. Default read,
	// write. A token that holds any other scope also gets read, and (see
	// actor.Actor.Can) write includes read.
	Scopes []string
	// CookieName is the session cookie. Default App + "_session".
	CookieName string
	// IdleTTL and MaxTTL bound a dashboard sign-in. Default 30 and 90 days.
	IdleTTL, MaxTTL time.Duration
	// Version is what GET /version reports.
	Version string
	// OnUserCreated runs in the transaction that creates a user, so the app
	// can give them their starting rows.
	OnUserCreated func(ctx context.Context, u User) error
	// AllowOrigin says which other origins may call the API from a browser.
	// Nil allows none.
	AllowOrigin func(origin string) bool
	// Public marks paths that need no token, beyond the built-in ones
	// (everything outside /v1/, and /v1/auth/).
	Public func(path string) bool
	Log    *slog.Logger
	Now    func() time.Time
	NewID  func() string
	// Ready gates /readyz; set it once boot (migrations, bootstrap) is done.
	Ready atomic.Bool
}

// LoginName is the name of a token that is a dashboard sign-in.
const LoginName = "login"

func (a *Accounts) Store() Store         { return Store{DB: a.DB} }
func (a *Accounts) Events() events.Store { return events.Store{DB: a.DB} }

func (a *Accounts) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}

// ID returns a new row id.
func (a *Accounts) ID() string {
	if a.NewID != nil {
		return a.NewID()
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *Accounts) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

func (a *Accounts) scopes() []string {
	if len(a.Scopes) > 0 {
		return a.Scopes
	}
	return []string{"read", "write"}
}

func (a *Accounts) ttl() (idle, max time.Duration) {
	idle, max = a.IdleTTL, a.MaxTTL
	if idle == 0 {
		idle = 30 * 24 * time.Hour
	}
	if max == 0 {
		max = 90 * 24 * time.Hour
	}
	return idle, max
}

// Unauthorized is the error for a missing, unknown, revoked or expired token.
func (a *Accounts) Unauthorized() error {
	return &apperr.Error{Code: "unauthorized", Message: "invalid or revoked token", Status: 401,
		Hint: "set Authorization: Bearer to a " + a.App + " token, or pass --token / " + a.EnvPrefix + "TOKEN"}
}

func (a *Accounts) sessions() *session.Manager {
	idle, max := a.ttl()
	return &session.Manager{
		Store: a.Store(), Now: a.now, NewID: a.ID,
		LoginName: LoginName, IdleTTL: idle, MaxTTL: max,
		Scopes: a.scopes(), TokenMarker: a.TokenMarker, Unauthorized: a.Unauthorized,
	}
}

// Authenticate turns a token secret into the actor it stands for.
func (a *Accounts) Authenticate(ctx context.Context, secret string) (actor.Actor, error) {
	return a.sessions().Authenticate(ctx, secret)
}

// Require returns the request's actor if it holds scope.
func (a *Accounts) Require(ctx context.Context, scope string) (actor.Actor, error) {
	act, ok := actor.ActorFrom(ctx)
	if !ok || act.UserID == "" {
		return actor.Actor{}, a.Unauthorized()
	}
	if !act.Can(scope) {
		return actor.Actor{}, apperr.Forbidden(scope)
	}
	return act, nil
}

// Audit appends a line to the audit log. Call it in the transaction that
// makes the change.
func (a *Accounts) Audit(ctx context.Context, act actor.Actor, action, entityType, entityID, summary string, meta map[string]any) error {
	var raw json.RawMessage
	if meta != nil {
		b, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		raw = b
	}
	return a.Store().CreateAudit(ctx, Audit{
		ID: a.ID(), UserID: act.UserID, TokenID: act.TokenID, ActorName: act.Name,
		Action: action, EntityType: entityType, EntityID: entityID, Summary: summary,
		Metadata: raw, CreatedAt: a.now(),
	})
}

// normalizeScopes checks requested scopes against the app's and returns them
// in the app's order. None asked means read; any other scope brings read.
func (a *Accounts) normalizeScopes(in []string) ([]string, error) {
	all := a.scopes()
	set := map[string]bool{}
	for _, raw := range in {
		for _, part := range strings.Split(raw, ",") {
			s := strings.ToLower(strings.TrimSpace(part))
			if s == "" {
				continue
			}
			known := false
			for _, k := range all {
				known = known || k == s
			}
			if !known {
				return nil, apperr.Validation("unknown scope %q", s).WithHint("scopes are " + strings.Join(all, ", "))
			}
			set[s] = true
		}
	}
	set["read"] = true
	out := []string{}
	for _, k := range all {
		if set[k] {
			out = append(out, k)
		}
	}
	return out, nil
}

// CreateUser adds a user and runs OnUserCreated. Call it inside DB.WithLock
// or a transaction.
func (a *Accounts) CreateUser(ctx context.Context, name, email string) (User, error) {
	if strings.TrimSpace(name) == "" {
		name = email
	}
	u := User{ID: a.ID(), Name: name, Email: email, CreatedAt: a.now()}
	if err := a.Store().CreateUser(ctx, u); err != nil {
		return User{}, err
	}
	if a.OnUserCreated != nil {
		if err := a.OnUserCreated(ctx, u); err != nil {
			return User{}, err
		}
	}
	return u, nil
}

// Bootstrap creates the owner and the first token when the database has
// none. The returned secret is set only when a token was created; pass
// secret to choose it instead of generating one.
func (a *Accounts) Bootstrap(ctx context.Context, ownerName, ownerEmail, tokenName, secret string) (string, error) {
	if strings.TrimSpace(ownerName) == "" {
		ownerName = "Owner"
	}
	if strings.TrimSpace(ownerEmail) == "" {
		ownerEmail = "owner@localhost"
	}
	if strings.TrimSpace(tokenName) == "" {
		tokenName = "owner"
	}
	issued := ""
	st := a.Store()
	err := a.DB.WithLock(ctx, func(ctx context.Context) error {
		n, err := st.CountUsers(ctx)
		if err != nil {
			return err
		}
		var user User
		if n == 0 {
			user, err = a.CreateUser(ctx, ownerName, ownerEmail)
		} else {
			user, err = st.FirstUser(ctx)
		}
		if err != nil {
			return err
		}
		if tn, err := st.CountTokens(ctx); err != nil || tn > 0 {
			return err
		}
		var prefix, hash string
		if secret = strings.TrimSpace(secret); secret == "" {
			if secret, prefix, hash, err = session.Generate(a.TokenMarker); err != nil {
				return err
			}
		} else {
			prefix, hash = secret[:min(len(secret), len(a.TokenMarker)+8)], session.Hash(secret)
		}
		tok := session.Token{ID: a.ID(), UserID: user.ID, Name: tokenName, Hash: hash, Prefix: prefix, Scopes: a.scopes(), CreatedAt: a.now()}
		if err := st.CreateToken(ctx, tok); err != nil {
			return err
		}
		act := actor.Actor{UserID: user.ID, TokenID: tok.ID, Name: tok.Name, Scopes: tok.Scopes}
		if err := a.Audit(ctx, act, "token.create", "token", tok.ID, "bootstrap token "+tok.Name, map[string]any{"prefix": tok.Prefix}); err != nil {
			return err
		}
		issued = secret
		return nil
	})
	return issued, err
}

// Me is who a request is from.
type Me struct {
	User  User          `json:"user"`
	Token session.Token `json:"token"`
	// Proxy is true when a trusted proxy, not a token, authenticated the caller.
	Proxy bool `json:"proxy,omitempty"`
}

func (a *Accounts) Me(ctx context.Context) (Me, error) {
	act, err := a.Require(ctx, "read")
	if err != nil {
		return Me{}, err
	}
	user, err := a.Store().GetUser(ctx, act.UserID)
	if err != nil {
		return Me{}, err
	}
	if act.TokenID == "" {
		return Me{User: user, Token: session.Token{UserID: user.ID, Name: act.Name, Scopes: append([]string(nil), act.Scopes...)}, Proxy: act.Proxy}, nil
	}
	tok, err := a.Store().GetToken(ctx, act.UserID, act.TokenID)
	tok.Hash = ""
	return Me{User: user, Token: tok}, err
}

// MePatch changes the user's own profile. Email is the sign-in identity and
// is not changed here.
type MePatch struct {
	Name   *string `json:"name"`
	Avatar *string `json:"avatar"`
	// AnnouncementsSeen only moves forward.
	AnnouncementsSeen *string `json:"announcements_seen"`
}

const maxAvatar = 256 << 10

func (a *Accounts) UpdateMe(ctx context.Context, in MePatch) (User, error) {
	act, err := a.Require(ctx, "write")
	if err != nil {
		return User{}, err
	}
	user, err := a.Store().GetUser(ctx, act.UserID)
	if err != nil {
		return User{}, err
	}
	if in.Name != nil {
		if user.Name, err = requireText("name", *in.Name, 200); err != nil {
			return User{}, err
		}
	}
	if in.Avatar != nil {
		v := strings.TrimSpace(*in.Avatar)
		ok := v == ""
		for _, p := range []string{"data:image/png;base64,", "data:image/jpeg;base64,", "data:image/webp;base64,"} {
			ok = ok || strings.HasPrefix(v, p)
		}
		if !ok {
			return User{}, apperr.Validation("avatar must be a png, jpeg, or webp data URL").WithHint("data:image/png;base64,...")
		}
		if len(v) > maxAvatar {
			return User{}, apperr.Validation("avatar is too large").WithHint("keep it under 256KB; resize to about 256px")
		}
		user.Avatar = v
	}
	if in.AnnouncementsSeen != nil {
		t, err := time.Parse(time.RFC3339, *in.AnnouncementsSeen)
		if err != nil {
			return User{}, apperr.Validation("announcements_seen must be an RFC 3339 time").WithHint("2026-11-07T00:00:00Z")
		}
		if v := t.UTC().Format(time.RFC3339); v > user.AnnouncementsSeen {
			user.AnnouncementsSeen = v
		}
	}
	err = a.DB.WithTx(ctx, func(ctx context.Context) error {
		if err := a.Store().UpdateUser(ctx, user); err != nil {
			return err
		}
		return a.Audit(ctx, act, "user.update", "user", user.ID, "updated profile", nil)
	})
	return user, err
}

func requireText(name, value string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", apperr.Validation("%s is required", name).WithHint("set " + name)
	}
	if len(value) > max {
		return "", apperr.Validation("%s is too long", name)
	}
	return value, nil
}

type TokenInput struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// ExpiresInDays makes the token stop working after that many days; 0 never.
	ExpiresInDays int `json:"expires_in_days,omitempty"`
}

// IssuedToken carries the secret, which is shown once.
type IssuedToken struct {
	Token  session.Token `json:"token"`
	Secret string        `json:"secret"`
}

const maxTokenDays = 3650

// CreateToken mints an API token. A token never mints more than it holds.
// ponytail: no scope is reserved for a signed-in human (pursuit's approve
// rule); add a HumanOnly list when an app needs one.
func (a *Accounts) CreateToken(ctx context.Context, in TokenInput) (IssuedToken, error) {
	act, err := a.Require(ctx, "write")
	if err != nil {
		return IssuedToken{}, err
	}
	name, err := requireText("name", in.Name, 80)
	if err != nil {
		return IssuedToken{}, err
	}
	// A token is a dashboard sign-in because of its name, so nobody may
	// choose that name for an API token and get a sign-in's lifetimes.
	if name == LoginName {
		return IssuedToken{}, apperr.Validation("the name %q is reserved", LoginName).WithHint("pick another name")
	}
	scopes, err := a.normalizeScopes(in.Scopes)
	if err != nil {
		return IssuedToken{}, err
	}
	for _, sc := range scopes {
		if !act.Can(sc) {
			return IssuedToken{}, apperr.Forbidden(sc).WithHint("a token can only create tokens with scopes it already holds")
		}
	}
	if in.ExpiresInDays < 0 || in.ExpiresInDays > maxTokenDays {
		return IssuedToken{}, apperr.Validation("expires_in_days is 0 for never, or 1 to %d", maxTokenDays)
	}
	secret, prefix, hash, err := session.Generate(a.TokenMarker)
	if err != nil {
		return IssuedToken{}, err
	}
	tok := session.Token{ID: a.ID(), UserID: act.UserID, Name: name, Hash: hash, Prefix: prefix, Scopes: scopes, CreatedAt: a.now()}
	if in.ExpiresInDays > 0 {
		exp := tok.CreatedAt.AddDate(0, 0, in.ExpiresInDays)
		tok.ExpiresAt = &exp
	}
	err = a.DB.WithTx(ctx, func(ctx context.Context) error {
		if err := a.Store().CreateToken(ctx, tok); err != nil {
			return err
		}
		return a.Audit(ctx, act, "token.create", "token", tok.ID, "created token "+name, map[string]any{"prefix": prefix, "scopes": scopes, "expires_in_days": in.ExpiresInDays})
	})
	tok.Hash = ""
	return IssuedToken{Token: tok, Secret: secret}, err
}

func (a *Accounts) ListTokens(ctx context.Context, r paging.Request) (paging.Page[session.Token], error) {
	act, err := a.Require(ctx, "read")
	if err != nil {
		return paging.Page[session.Token]{}, err
	}
	a.sessions().Sweep(ctx, a.now())
	page, err := a.Store().ListTokens(ctx, act.UserID, r)
	for i := range page.Data {
		page.Data[i].Current = page.Data[i].ID == act.TokenID
	}
	return page, err
}

func (a *Accounts) RevokeToken(ctx context.Context, id string) (session.Token, error) {
	act, err := a.Require(ctx, "write")
	if err != nil {
		return session.Token{}, err
	}
	var out session.Token
	err = a.DB.WithTx(ctx, func(ctx context.Context) error {
		changed, err := a.Store().RevokeToken(ctx, act.UserID, id, a.now())
		if err != nil {
			return err
		}
		if out, err = a.Store().GetToken(ctx, act.UserID, id); err != nil {
			return err
		}
		out.Hash = ""
		if changed {
			return a.Audit(ctx, act, "token.revoke", "token", id, "revoked token "+out.Name, nil)
		}
		return nil
	})
	return out, err
}

func (a *Accounts) ListAudit(ctx context.Context, f AuditFilter, r paging.Request) (paging.Page[Audit], error) {
	act, err := a.Require(ctx, "read")
	if err != nil {
		return paging.Page[Audit]{}, err
	}
	if len(f.Q) > 200 {
		return paging.Page[Audit]{}, apperr.Validation("q is too long").WithHint("keep q under 200 characters")
	}
	return a.Store().ListAudit(ctx, act.UserID, f, r)
}

// ListEvents pages the user's feed. after is the id of the last event the
// caller saw: the page then holds what came later, oldest first. With wait,
// an empty page is held open until an event arrives or the wait (at most a
// minute) runs out, so a client can follow the feed without polling fast.
// ponytail: polls the table every 200ms while waiting; LISTEN/NOTIFY or an
// in-process signal if many clients follow at once.
func (a *Accounts) ListEvents(ctx context.Context, f events.Filter, after string, r paging.Request, wait time.Duration) (paging.Page[events.Event], error) {
	act, err := a.Require(ctx, "read")
	if err != nil {
		return paging.Page[events.Event]{}, err
	}
	if after != "" {
		ev, err := a.Events().Get(ctx, act.UserID, after)
		if err != nil {
			return paging.Page[events.Event]{}, err
		}
		f.AfterTime, f.AfterID = ev.OccurredAt, ev.ID
		if r.Sort == "" {
			r.Sort = "occurred_at"
		}
	}
	deadline := time.Now().Add(min(max(wait, 0), time.Minute))
	for {
		page, err := a.Events().List(ctx, act.UserID, f, r)
		if err != nil || len(page.Data) > 0 || wait <= 0 || time.Now().After(deadline) {
			return page, err
		}
		select {
		case <-ctx.Done():
			return page, nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}
