// Package events is the activity feed: one row per thing that happened to
// one of a user's records. An event names its subject by type and id and
// keeps what is particular to its kind in Data, a JSON object the app owns.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/paging"
	"github.com/adrianmross/appkit/sqldb"
)

type Event struct {
	ID          string          `json:"id"`
	UserID      string          `json:"-"`
	SubjectType string          `json:"subject_type"`
	SubjectID   string          `json:"subject_id"`
	Kind        string          `json:"kind"`
	Data        json.RawMessage `json:"data"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	OccurredAt  time.Time       `json:"occurred_at"`
	CreatedAt   time.Time       `json:"created_at"`
	Actor       actor.ActorRef  `json:"actor"`
}

// Store reads and writes the events table of the platform migration set.
type Store struct{ DB *sqldb.DB }

const cols = `id, user_id, subject_type, subject_id, kind, data, title, body, occurred_at, created_at, actor_token_id, actor_name`

func (s Store) Create(ctx context.Context, e Event) error {
	data := string(e.Data)
	if data == "" {
		data = "{}"
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO events (`+cols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.UserID, e.SubjectType, e.SubjectID, e.Kind, data, e.Title, e.Body,
		sqldb.FormatTime(e.OccurredAt), sqldb.FormatTime(e.CreatedAt), e.Actor.TokenID, e.Actor.Name)
	return err
}

func (s Store) Get(ctx context.Context, userID, id string) (Event, error) {
	e, err := scan(s.DB.QueryRow(ctx, `SELECT `+cols+` FROM events WHERE user_id=? AND id=?`, userID, id))
	if err == sql.ErrNoRows {
		return Event{}, apperr.NotFound("event")
	}
	return e, err
}

// Filter narrows After. The zero value is every event of the user.
type Filter struct {
	SubjectType string
	SubjectID   string
	// Kinds keeps only events of these kinds; empty keeps all.
	Kinds []string
	// AfterTime and AfterID are the occurred_at and id of the last event
	// the caller has seen.
	AfterTime time.Time
	AfterID   string
	// Limit defaults to 50.
	Limit int
}

func (f Filter) where(userID string) (string, []any) {
	where, args := ` WHERE user_id=?`, []any{userID}
	if f.SubjectType != "" {
		where, args = where+` AND subject_type=?`, append(args, f.SubjectType)
	}
	if f.SubjectID != "" {
		where, args = where+` AND subject_id=?`, append(args, f.SubjectID)
	}
	if !f.AfterTime.IsZero() {
		at := sqldb.FormatTime(f.AfterTime)
		where, args = where+` AND (occurred_at > ? OR (occurred_at = ? AND id > ?))`, append(args, at, at, f.AfterID)
	}
	if len(f.Kinds) > 0 {
		where += ` AND kind IN (?` + strings.Repeat(`, ?`, len(f.Kinds)-1) + `)`
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	return where, args
}

// After lists a user's events oldest first, which is the order a feed
// follows them in.
// ponytail: no sort choice and no total; the app's own list endpoint adds
// those if it needs them.
func (s Store) After(ctx context.Context, userID string, f Filter) ([]Event, error) {
	where, args := f.where(userID)
	q := `SELECT ` + cols + ` FROM events` + where
	if f.Limit <= 0 {
		f.Limit = 50
	}
	rows, err := s.DB.Query(ctx, q+` ORDER BY occurred_at, id LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteSubject removes a record's events. Nothing cascades to them, so the
// app calls this in the transaction that deletes the record.
func (s Store) DeleteSubject(ctx context.Context, userID, subjectType, subjectID string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM events WHERE user_id=? AND subject_type=? AND subject_id=?`, userID, subjectType, subjectID)
	return err
}

func scan(row interface{ Scan(...any) error }) (Event, error) {
	var e Event
	var data, occurred, created string
	err := row.Scan(&e.ID, &e.UserID, &e.SubjectType, &e.SubjectID, &e.Kind, &data, &e.Title, &e.Body, &occurred, &created, &e.Actor.TokenID, &e.Actor.Name)
	if err != nil {
		return Event{}, err
	}
	e.Data = json.RawMessage(data)
	if e.OccurredAt, err = sqldb.ParseTime(occurred); err != nil {
		return Event{}, err
	}
	e.CreatedAt, err = sqldb.ParseTime(created)
	return e, err
}

// List pages a user's events, newest first unless the request sorts
// otherwise (occurred_at or created_at). The filter's After fields apply
// too, which is how a feed asks for what came after the last event it saw.
func (s Store) List(ctx context.Context, userID string, f Filter, r paging.Request) (paging.Page[Event], error) {
	expr, desc, err := r.Order(map[string]string{"occurred_at": "occurred_at", "created_at": "created_at"}, "-occurred_at")
	if err != nil {
		return paging.Page[Event]{}, err
	}
	where, args := f.where(userID)
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM events`+where, args...).Scan(&total); err != nil {
		return paging.Page[Event]{}, err
	}
	if c := r.Cursor; c != nil && c.ID != "" {
		where, args = where+` AND `+paging.CursorSQL(expr, "id", desc), append(args, c.K, c.K, c.ID)
	}
	rows, err := s.DB.Query(ctx, `SELECT `+cols+` FROM events`+where+` `+paging.OrderSQL(expr, "id", desc)+` LIMIT ?`, append(args, r.Limit+1)...)
	if err != nil {
		return paging.Page[Event]{}, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return paging.Page[Event]{}, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return paging.Page[Event]{}, err
	}
	created := strings.HasSuffix(r.Sort, "created_at")
	return paging.Finish(r, out, total, func(e Event) string {
		if created {
			return sqldb.FormatTime(e.CreatedAt)
		}
		return sqldb.FormatTime(e.OccurredAt)
	}, func(e Event) string { return e.ID }), nil
}
