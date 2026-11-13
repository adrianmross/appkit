package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/paging"
	"github.com/adrianmross/appkit/session"
	"github.com/adrianmross/appkit/sqldb"
)

type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	// Avatar is a small data: URL, or "".
	Avatar string `json:"avatar"`
	// AnnouncementsSeen is the publish time of the newest announcement the
	// user dismissed (RFC 3339, UTC), or "".
	AnnouncementsSeen string    `json:"announcements_seen"`
	CreatedAt         time.Time `json:"created_at"`
}

// Audit is one line of the audit log: who changed what.
type Audit struct {
	ID         string          `json:"id"`
	UserID     string          `json:"-"`
	TokenID    string          `json:"token_id"`
	ActorName  string          `json:"actor_name"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Summary    string          `json:"summary"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Store reads and writes the users, api_tokens, audit_log and
// idempotency_keys tables of the platform migration set. It satisfies
// session.Store and httpx.IdemStore.
type Store struct{ DB *sqldb.DB }

var (
	_ session.Store   = Store{}
	_ httpx.IdemStore = Store{}
)

func (s Store) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return s.DB.WithTx(ctx, fn)
}

func mapWriteErr(err error) error {
	if err != nil && sqldb.IsUnique(err) {
		return apperr.Conflict("unique constraint violated", "reuse the existing resource or choose a different value")
	}
	return err
}

func (s Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s Store) CreateUser(ctx context.Context, u User) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO users (id, name, email, created_at) VALUES (?, ?, ?, ?)`, u.ID, u.Name, u.Email, sqldb.FormatTime(u.CreatedAt))
	return mapWriteErr(err)
}

const userCols = `id, name, email, avatar, announcements_seen, created_at`

func (s Store) user(ctx context.Context, where string, args ...any) (User, error) {
	var u User
	var created string
	err := s.DB.QueryRow(ctx, `SELECT `+userCols+` FROM users `+where, args...).Scan(&u.ID, &u.Name, &u.Email, &u.Avatar, &u.AnnouncementsSeen, &created)
	if err == sql.ErrNoRows {
		return User{}, apperr.NotFound("user")
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt, err = sqldb.ParseTime(created)
	return u, err
}

func (s Store) GetUser(ctx context.Context, id string) (User, error) {
	return s.user(ctx, `WHERE id=?`, id)
}

func (s Store) FindUserByEmail(ctx context.Context, email string) (User, error) {
	return s.user(ctx, `WHERE lower(email)=lower(?)`, email)
}

// FirstUser is the oldest user: the owner of a single-user deployment.
func (s Store) FirstUser(ctx context.Context) (User, error) {
	return s.user(ctx, `ORDER BY created_at LIMIT 1`)
}

func (s Store) UpdateUser(ctx context.Context, u User) error {
	_, err := s.DB.Exec(ctx, `UPDATE users SET name=?, avatar=?, announcements_seen=? WHERE id=?`, u.Name, u.Avatar, u.AnnouncementsSeen, u.ID)
	return err
}

func (s Store) CountTokens(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM api_tokens`).Scan(&n)
	return n, err
}

const tokenCols = `id, user_id, name, token_hash, token_prefix, scopes, created_at, last_used_at, revoked_at, client, ip, client_id, resource, expires_at, family_id`

func (s Store) CreateToken(ctx context.Context, t session.Token) error {
	expires := ""
	if t.ExpiresAt != nil {
		expires = sqldb.FormatTime(*t.ExpiresAt)
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO api_tokens (id, user_id, name, token_hash, token_prefix, scopes, created_at, client, ip, client_id, resource, expires_at, family_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.UserID, t.Name, t.Hash, t.Prefix, strings.Join(t.Scopes, ","), sqldb.FormatTime(t.CreatedAt), t.Client, t.IP, t.ClientID, t.Resource, expires, t.FamilyID)
	return mapWriteErr(err)
}

func (s Store) token(ctx context.Context, where string, args ...any) (session.Token, error) {
	t, err := scanToken(s.DB.QueryRow(ctx, `SELECT `+tokenCols+` FROM api_tokens `+where, args...))
	if err == sql.ErrNoRows {
		return session.Token{}, apperr.NotFound("token")
	}
	return t, err
}

func (s Store) GetTokenByHash(ctx context.Context, hash string) (session.Token, error) {
	return s.token(ctx, `WHERE token_hash=?`, hash)
}

func (s Store) GetToken(ctx context.Context, userID, id string) (session.Token, error) {
	return s.token(ctx, `WHERE user_id=? AND id=?`, userID, id)
}

func optTime(v sql.NullString) (*time.Time, error) {
	if !v.Valid || v.String == "" {
		return nil, nil
	}
	t, err := sqldb.ParseTime(v.String)
	return &t, err
}

func scanToken(row interface{ Scan(...any) error }) (session.Token, error) {
	var t session.Token
	var scopes, created string
	var last, revoked, expires sql.NullString
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Hash, &t.Prefix, &scopes, &created, &last, &revoked, &t.Client, &t.IP, &t.ClientID, &t.Resource, &expires, &t.FamilyID)
	if err != nil {
		return session.Token{}, err
	}
	t.Scopes = []string{}
	if scopes != "" {
		t.Scopes = strings.Split(scopes, ",")
	}
	if t.CreatedAt, err = sqldb.ParseTime(created); err != nil {
		return session.Token{}, err
	}
	if t.LastUsedAt, err = optTime(last); err != nil {
		return session.Token{}, err
	}
	if t.RevokedAt, err = optTime(revoked); err != nil {
		return session.Token{}, err
	}
	t.ExpiresAt, err = optTime(expires)
	return t, err
}

// ListTokens pages a user's tokens, newest first by default; it also sorts
// by name.
func (s Store) ListTokens(ctx context.Context, userID string, r paging.Request) (paging.Page[session.Token], error) {
	expr, desc, err := r.Order(map[string]string{"created_at": "created_at", "name": "name"}, "-created_at")
	if err != nil {
		return paging.Page[session.Token]{}, err
	}
	where, args := `WHERE user_id=?`, []any{userID}
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM api_tokens `+where, args...).Scan(&total); err != nil {
		return paging.Page[session.Token]{}, err
	}
	if c := r.Cursor; c != nil && c.ID != "" {
		where, args = where+` AND `+paging.CursorSQL(expr, "id", desc), append(args, c.K, c.K, c.ID)
	}
	rows, err := s.DB.Query(ctx, `SELECT `+tokenCols+` FROM api_tokens `+where+` `+paging.OrderSQL(expr, "id", desc)+` LIMIT ?`, append(args, r.Limit+1)...)
	if err != nil {
		return paging.Page[session.Token]{}, err
	}
	defer rows.Close()
	var out []session.Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return paging.Page[session.Token]{}, err
		}
		t.Hash = ""
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return paging.Page[session.Token]{}, err
	}
	byName := strings.HasSuffix(r.Sort, "name")
	return paging.Finish(r, out, total, func(t session.Token) string {
		if byName {
			return t.Name
		}
		return sqldb.FormatTime(t.CreatedAt)
	}, func(t session.Token) string { return t.ID }), nil
}

// RevokeToken reports whether the token was live until now.
func (s Store) RevokeToken(ctx context.Context, userID, id string, at time.Time) (bool, error) {
	res, err := s.DB.Exec(ctx, `UPDATE api_tokens SET revoked_at=? WHERE user_id=? AND id=? AND revoked_at IS NULL`, sqldb.FormatTime(at), userID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s Store) RevokeExpiredLogins(ctx context.Context, name string, at, idleBefore, maxBefore time.Time) error {
	_, err := s.DB.Exec(ctx, `UPDATE api_tokens SET revoked_at=? WHERE name=? AND revoked_at IS NULL AND (COALESCE(last_used_at, created_at) < ? OR created_at < ?)`,
		sqldb.FormatTime(at), name, sqldb.FormatTime(idleBefore), sqldb.FormatTime(maxBefore))
	return err
}

func (s Store) TouchToken(ctx context.Context, id string, at, staleBefore time.Time) error {
	_, err := s.DB.Exec(ctx, `UPDATE api_tokens SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at < ?)`, sqldb.FormatTime(at), id, sqldb.FormatTime(staleBefore))
	return err
}

func (s Store) CreateAudit(ctx context.Context, a Audit) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO audit_log (id, user_id, token_id, actor_name, action, entity_type, entity_id, summary, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.UserID, a.TokenID, a.ActorName, a.Action, a.EntityType, a.EntityID, a.Summary, string(a.Metadata), sqldb.FormatTime(a.CreatedAt))
	return err
}

// AuditFilter narrows ListAudit. Since and Until bound created_at.
type AuditFilter struct {
	Q, Actor, Action, EntityType, EntityID string
	Since, Until                           time.Time
}

func likeContains(s string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(s)) + "%"
}

func (s Store) ListAudit(ctx context.Context, userID string, f AuditFilter, r paging.Request) (paging.Page[Audit], error) {
	expr, desc, err := r.Order(map[string]string{"created_at": "created_at"}, "-created_at")
	if err != nil {
		return paging.Page[Audit]{}, err
	}
	where, args := `WHERE user_id=?`, []any{userID}
	add := func(clause string, a ...any) { where, args = where+` AND `+clause, append(args, a...) }
	if f.Actor != "" {
		add("lower(actor_name)=lower(?)", f.Actor)
	}
	if f.Action != "" {
		add("action=?", f.Action)
	}
	if f.EntityType != "" {
		add("entity_type=?", f.EntityType)
	}
	if f.EntityID != "" {
		add("entity_id=?", f.EntityID)
	}
	if !f.Since.IsZero() {
		add("created_at>=?", sqldb.FormatTime(f.Since))
	}
	if !f.Until.IsZero() {
		add("created_at<=?", sqldb.FormatTime(f.Until))
	}
	if f.Q != "" {
		p := likeContains(f.Q)
		add(`(lower(summary) LIKE ? ESCAPE '\' OR lower(action) LIKE ? ESCAPE '\' OR lower(entity_type) LIKE ? ESCAPE '\' OR lower(entity_id) LIKE ? ESCAPE '\' OR lower(actor_name) LIKE ? ESCAPE '\' OR lower(metadata) LIKE ? ESCAPE '\')`, p, p, p, p, p, p)
	}
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log `+where, args...).Scan(&total); err != nil {
		return paging.Page[Audit]{}, err
	}
	if c := r.Cursor; c != nil && c.ID != "" {
		add(paging.CursorSQL(expr, "id", desc), c.K, c.K, c.ID)
	}
	rows, err := s.DB.Query(ctx, `SELECT id, user_id, token_id, actor_name, action, entity_type, entity_id, summary, metadata, created_at FROM audit_log `+where+` `+paging.OrderSQL(expr, "id", desc)+` LIMIT ?`, append(args, r.Limit+1)...)
	if err != nil {
		return paging.Page[Audit]{}, err
	}
	defer rows.Close()
	var out []Audit
	for rows.Next() {
		var a Audit
		var meta, created string
		if err := rows.Scan(&a.ID, &a.UserID, &a.TokenID, &a.ActorName, &a.Action, &a.EntityType, &a.EntityID, &a.Summary, &meta, &created); err != nil {
			return paging.Page[Audit]{}, err
		}
		if meta != "" {
			a.Metadata = json.RawMessage(meta)
		}
		if a.CreatedAt, err = sqldb.ParseTime(created); err != nil {
			return paging.Page[Audit]{}, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return paging.Page[Audit]{}, err
	}
	return paging.Finish(r, out, total, func(a Audit) string { return sqldb.FormatTime(a.CreatedAt) }, func(a Audit) string { return a.ID }), nil
}

func (s Store) GetIdempotency(ctx context.Context, tokenID, key string) (httpx.IdemRecord, bool, error) {
	var rec httpx.IdemRecord
	var created string
	err := s.DB.QueryRow(ctx, `SELECT token_id, user_id, idem_key, method, path, request_hash, status_code, response_body, created_at FROM idempotency_keys WHERE token_id=? AND idem_key=?`, tokenID, key).
		Scan(&rec.TokenID, &rec.UserID, &rec.Key, &rec.Method, &rec.Path, &rec.RequestHash, &rec.StatusCode, &rec.ResponseBody, &created)
	if err == sql.ErrNoRows {
		return httpx.IdemRecord{}, false, nil
	}
	if err != nil {
		return httpx.IdemRecord{}, false, err
	}
	rec.CreatedAt, err = sqldb.ParseTime(created)
	return rec, err == nil, err
}

func (s Store) InsertIdempotency(ctx context.Context, rec httpx.IdemRecord) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO idempotency_keys (token_id, user_id, idem_key, method, path, request_hash, status_code, response_body, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.TokenID, rec.UserID, rec.Key, rec.Method, rec.Path, rec.RequestHash, rec.StatusCode, rec.ResponseBody, sqldb.FormatTime(rec.CreatedAt))
	if err != nil && sqldb.IsUnique(err) {
		return httpx.ErrIdempotencyRace
	}
	return err
}

func (s Store) FinishIdempotency(ctx context.Context, tokenID, key string, status int, body string) error {
	_, err := s.DB.Exec(ctx, `UPDATE idempotency_keys SET status_code=?, response_body=? WHERE token_id=? AND idem_key=?`, status, body, tokenID, key)
	return err
}
