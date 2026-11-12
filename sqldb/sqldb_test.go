package sqldb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// Placeholders are numbered for Postgres only; a transaction commits, rolls
// back on an error, and rolls back on a dry run.
func TestTxAndRebind(t *testing.T) {
	if got := Rebind("postgres", "a=? AND b=?"); got != "a=$1 AND b=$2" {
		t.Fatalf("rebind: %s", got)
	}
	if got := Rebind("sqlite", "a=?"); got != "a=?" {
		t.Fatalf("rebind sqlite: %s", got)
	}
	ctx := context.Background()
	db, err := Open("sqlite://" + filepath.Join(t.TempDir(), "sub", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.SQL.Close()
	if _, err := db.Exec(ctx, `CREATE TABLE t (v TEXT)`); err != nil {
		t.Fatal(err)
	}
	insert := func(ctx context.Context) error {
		_, err := db.Exec(ctx, `INSERT INTO t (v) VALUES (?)`, "x")
		return err
	}
	boom := errors.New("boom")
	_ = db.WithTx(ctx, insert)
	_ = db.WithTx(WithDryRun(ctx), insert)
	_ = db.WithTx(ctx, func(ctx context.Context) error { _ = insert(ctx); return boom })
	if err := db.WithLock(ctx, func(ctx context.Context) error { return db.WithTx(ctx, insert) }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows = %d %v, want 2 (plain and locked)", n, err)
	}
}
