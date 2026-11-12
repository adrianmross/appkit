package migrations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/adrianmross/appkit/sqldb"
)

type column struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	NotNull bool    `json:"notnull"`
	Default *string `json:"default"`
	PK      bool    `json:"pk"`
}

type table struct {
	Columns []column `json:"columns"`
	Indexes []string `json:"indexes"`
}

// want is what a fresh pursuit database has for the same tables
// (testdata/pursuit_schema.py writes it).
func want(t *testing.T) map[string]table {
	t.Helper()
	b, err := os.ReadFile("testdata/pursuit_columns.json")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]table
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// The set, applied to an empty SQLite database, gives every platform table
// the columns, defaults, keys and indexes pursuit's migrations end at.
func TestMatchesPursuitSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := sqldb.Open("sqlite:" + filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.SQL.Close()
	for range 2 { // the second run finds nothing pending
		if err := Up(ctx, db.SQL, db.Dialect, 0); err != nil {
			t.Fatal(err)
		}
	}
	var version string
	if err := db.QueryRow(ctx, "SELECT version FROM schema_migrations").Scan(&version); err != nil || version != "appkit/001" {
		t.Fatalf("recorded version %q, %v", version, err)
	}
	space := regexp.MustCompile(`\s+`)
	for name, w := range want(t) {
		var got table
		rows, err := db.Query(ctx, "SELECT name, type, \"notnull\", dflt_value, pk FROM pragma_table_info(?) ORDER BY name", name)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var c column
			var pk int
			if err := rows.Scan(&c.Name, &c.Type, &c.NotNull, &c.Default, &pk); err != nil {
				t.Fatal(err)
			}
			c.Type, c.PK = strings.ToUpper(c.Type), pk > 0
			got.Columns = append(got.Columns, c)
		}
		rows.Close()
		rows, err = db.Query(ctx, "SELECT sql FROM sqlite_master WHERE type='index' AND tbl_name=? AND sql IS NOT NULL", name)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			got.Indexes = append(got.Indexes, space.ReplaceAllString(s, " "))
		}
		rows.Close()
		sort.Strings(got.Indexes)
		if len(got.Columns) != len(w.Columns) {
			t.Errorf("%s: %d columns, want %d", name, len(got.Columns), len(w.Columns))
			continue
		}
		for i, c := range got.Columns {
			wc := w.Columns[i]
			if c.Name != wc.Name || c.Type != wc.Type || c.NotNull != wc.NotNull || c.PK != wc.PK || deref(c.Default) != deref(wc.Default) {
				t.Errorf("%s.%s: got %+v (default %s), want %+v (default %s)", name, c.Name, c, deref(c.Default), wc, deref(wc.Default))
			}
		}
		if !reflect.DeepEqual(got.Indexes, w.Indexes) && (len(got.Indexes) > 0 || len(w.Indexes) > 0) {
			t.Errorf("%s indexes:\n got %q\nwant %q", name, got.Indexes, w.Indexes)
		}
	}
}

// The same statements run on Postgres and give the same column names, types
// and nullability. It works in its own schema.
func TestMatchesPursuitPostgres(t *testing.T) {
	url := os.Getenv("APPKIT_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("APPKIT_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	const schema = "appkit_migrations_test"
	admin, err := sqldb.Open(url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.SQL.Close()
	drop := func() { _, _ = admin.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE") }
	drop()
	defer drop()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	db, err := sqldb.Open(url + sep + "search_path=" + schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.SQL.Close()
	for range 2 {
		if err := Up(ctx, db.SQL, db.Dialect, 0); err != nil {
			t.Fatal(err)
		}
	}
	for name, w := range want(t) {
		rows, err := db.Query(ctx, "SELECT column_name, upper(data_type), is_nullable = 'NO' FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY column_name", schema, name)
		if err != nil {
			t.Fatal(err)
		}
		var got []column
		for rows.Next() {
			var c column
			if err := rows.Scan(&c.Name, &c.Type, &c.NotNull); err != nil {
				t.Fatal(err)
			}
			got = append(got, c)
		}
		rows.Close()
		if len(got) != len(w.Columns) {
			t.Errorf("%s: %d columns, want %d", name, len(got), len(w.Columns))
			continue
		}
		for i, c := range got {
			wc := w.Columns[i]
			// A primary key is NOT NULL on Postgres whether or not it says so.
			if c.Name != wc.Name || c.Type != wc.Type || c.NotNull != (wc.NotNull || wc.PK) {
				t.Errorf("%s.%s: got %+v, want %+v", name, c.Name, c, wc)
			}
		}
	}
}
