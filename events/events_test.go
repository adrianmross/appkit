package events

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/migrations"
	"github.com/adrianmross/appkit/sqldb"
)

func TestFeed(t *testing.T) {
	ctx := context.Background()
	db, err := sqldb.Open("sqlite:" + filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.SQL.Close()
	if err := migrations.Up(ctx, db.SQL, db.Dialect, 0); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"u", "v"} {
		if _, err := db.Exec(ctx, `INSERT INTO users (id, name, email, created_at) VALUES (?, 'A', ?, ?)`, u, u+"@localhost", "2026-01-01T00:00:00.000000000Z"); err != nil {
			t.Fatal(err)
		}
	}
	s := Store{DB: db}
	day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	for _, e := range []Event{
		{ID: "e1", UserID: "u", SubjectType: "listing", SubjectID: "l1", Kind: "saved", Data: json.RawMessage(`{"price":1}`), OccurredAt: day(1), CreatedAt: day(1)},
		{ID: "e2", UserID: "u", SubjectType: "listing", SubjectID: "l2", Kind: "saved", OccurredAt: day(2), CreatedAt: day(2)},
		{ID: "e3", UserID: "u", SubjectType: "listing", SubjectID: "l1", Kind: "toured", OccurredAt: day(2), CreatedAt: day(2)},
		{ID: "e4", UserID: "v", SubjectType: "listing", SubjectID: "l9", Kind: "saved", OccurredAt: day(3), CreatedAt: day(3)},
	} {
		if err := s.Create(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(f Filter) string {
		t.Helper()
		got, err := s.After(ctx, "u", f)
		if err != nil {
			t.Fatal(err)
		}
		out := ""
		for _, e := range got {
			out += e.ID + " "
		}
		return out
	}
	if got := ids(Filter{}); got != "e1 e2 e3 " {
		t.Fatalf("all: %q", got)
	}
	if got := ids(Filter{AfterTime: day(2), AfterID: "e2"}); got != "e3 " {
		t.Fatalf("after e2: %q", got)
	}
	if got := ids(Filter{SubjectType: "listing", SubjectID: "l1"}); got != "e1 e3 " {
		t.Fatalf("subject: %q", got)
	}
	e, err := s.Get(ctx, "u", "e1")
	if err != nil || string(e.Data) != `{"price":1}` || !e.OccurredAt.Equal(day(1)) {
		t.Fatalf("get: %+v %v", e, err)
	}
	if e, _ := s.Get(ctx, "u", "e2"); string(e.Data) != "{}" {
		t.Fatalf("empty data: %q", e.Data)
	}
	if _, err := s.Get(ctx, "u", "e4"); !apperr.IsCode(err, "not_found") {
		t.Fatalf("another user's event: %v", err)
	}
	if err := s.DeleteSubject(ctx, "u", "listing", "l1"); err != nil {
		t.Fatal(err)
	}
	if got := ids(Filter{}); got != "e2 " {
		t.Fatalf("after delete: %q", got)
	}
}
