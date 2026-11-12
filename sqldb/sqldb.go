// Package sqldb opens Postgres or SQLite and runs queries written once for
// both: `?` placeholders, a transaction carried in the context, and one
// database-wide lock.
package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type ctxKey int

const (
	keyTx ctxKey = iota + 1
	keyConn
	keyDry
)

// DB is an open database.
type DB struct {
	SQL *sql.DB
	// Dialect is "postgres" or "sqlite".
	Dialect string
	// LockKey is the Postgres advisory lock WithLock takes. SQLite needs none.
	LockKey int64
}

// Open connects to a postgres://, postgresql://, sqlite: or file: URL. A
// SQLite file's directory is created.
func Open(raw string) (*DB, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		db, err := sql.Open("pgx", raw)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(10)
		db.SetConnMaxLifetime(30 * time.Minute)
		return &DB{SQL: db, Dialect: "postgres"}, nil
	}
	path, err := sqlitePath(raw)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	return &DB{SQL: db, Dialect: "sqlite"}, nil
}

func (d *DB) Ping(ctx context.Context) error {
	var n int
	return d.QueryRow(ctx, "SELECT 1").Scan(&n)
}

// WithDryRun makes WithTx roll back instead of committing.
func WithDryRun(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyDry, true)
}

func IsDryRun(ctx context.Context) bool {
	v, _ := ctx.Value(keyDry).(bool)
	return v
}

// WithTx runs fn in a transaction, or in the one ctx already carries.
func (d *DB) WithTx(ctx context.Context, fn func(context.Context) error) error {
	if ctx.Value(keyTx) != nil || ctx.Value(keyConn) != nil {
		return fn(ctx)
	}
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	err = fn(context.WithValue(ctx, keyTx, tx))
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if IsDryRun(ctx) {
		return tx.Rollback()
	}
	return tx.Commit()
}

// LockTx holds the advisory lock key until tx ends. It is a transaction lock,
// not a session lock, so commit, rollback, or a dropped connection releases
// it, and it stays on one backend behind a transaction-mode pooler (Neon's
// pooled URL, PgBouncer). A session lock there can leak and hang every later
// boot. lock_timeout turns a stuck holder into an error instead of a 504.
func LockTx(ctx context.Context, tx *sql.Tx, key int64) error {
	if _, err := tx.ExecContext(ctx, "SET LOCAL lock_timeout = '15s'"); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", key)
	return err
}

// WithLock holds the database-wide lock for fn. Both dialects run the
// callback inside one transaction.
func (d *DB) WithLock(ctx context.Context, fn func(context.Context) error) error {
	if d.Dialect == "postgres" {
		tx, err := d.SQL.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := LockTx(ctx, tx, d.LockKey); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := fn(context.WithValue(ctx, keyTx, tx)); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	conn, err := d.SQL.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	err = fn(context.WithValue(ctx, keyConn, conn))
	if err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (d *DB) ex(ctx context.Context) execer {
	if tx, ok := ctx.Value(keyTx).(*sql.Tx); ok && tx != nil {
		return tx
	}
	if c, ok := ctx.Value(keyConn).(*sql.Conn); ok && c != nil {
		return c
	}
	return d.SQL
}

// Exec, Query and QueryRow take `?` placeholders in either dialect and run in
// the transaction ctx carries, when there is one.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.ex(ctx).ExecContext(ctx, Rebind(d.Dialect, query), args...)
}

func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.ex(ctx).QueryContext(ctx, Rebind(d.Dialect, query), args...)
}

func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.ex(ctx).QueryRowContext(ctx, Rebind(d.Dialect, query), args...)
}

// Rebind turns `?` placeholders into $1, $2, ... for Postgres.
func Rebind(dialect, query string) string {
	if dialect != "postgres" {
		return query
	}
	var b strings.Builder
	n := 1
	for _, r := range query {
		if r == '?' {
			b.WriteByte('$')
			b.WriteString(fmt.Sprint(n))
			n++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func sqlitePath(raw string) (string, error) {
	path := raw
	switch {
	case strings.HasPrefix(raw, "sqlite://"):
		path = strings.TrimPrefix(raw, "sqlite://")
	case strings.HasPrefix(raw, "sqlite:"):
		path = strings.TrimPrefix(raw, "sqlite:")
	case strings.HasPrefix(raw, "file:"):
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		path = u.Path
	default:
		return "", fmt.Errorf("unsupported database url %q", raw)
	}
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func sqliteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_txlock", "immediate")
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: q.Encode()}
	return u.String()
}

// IsUnique reports whether err is a unique-constraint violation.
func IsUnique(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}

// IsFK reports whether err is a foreign-key violation.
func IsFK(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23503" {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "foreign key")
}
