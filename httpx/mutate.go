package httpx

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/paging"
	"github.com/adrianmross/appkit/sqldb"
)

// ErrIdempotencyRace means another request claimed the key first.
var ErrIdempotencyRace = errors.New("idempotency race")

var errBusy = errors.New("idempotency in progress")

// IdemRecord is one Idempotency-Key a token has used. StatusCode is 0 while
// the first request is still running.
type IdemRecord struct {
	TokenID      string
	UserID       string
	Key          string
	Method       string
	Path         string
	RequestHash  string
	StatusCode   int
	ResponseBody string
	CreatedAt    time.Time
}

// IdemStore keeps idempotency records. InsertIdempotency answers
// ErrIdempotencyRace when the key already exists.
type IdemStore interface {
	WithTx(ctx context.Context, fn func(context.Context) error) error
	GetIdempotency(ctx context.Context, tokenID, key string) (IdemRecord, bool, error)
	InsertIdempotency(ctx context.Context, rec IdemRecord) error
	FinishIdempotency(ctx context.Context, tokenID, key string, status int, body string) error
}

// Mutate runs a write and answers with its result. It honors ?fields, a dry
// run (fn runs in a transaction that is rolled back), and Idempotency-Key: a
// repeat of the same request replays the stored response, and the same key
// on a different request is refused.
func Mutate(st IdemStore, w http.ResponseWriter, r *http.Request, status int, allowed []string, body []byte, fn func(ctx context.Context) (any, error)) {
	fields := FieldsOf(r.URL.Query())
	if err := paging.ValidateFields(fields, allowed); err != nil && len(fields) > 0 {
		if len(allowed) == 0 {
			WriteErr(w, apperr.Validation("fields are not supported on this response"))
			return
		}
		WriteErr(w, err)
		return
	}
	ctx := r.Context()
	if DryRun(r) {
		obj, err := fn(sqldb.WithDryRun(ctx))
		if err != nil {
			WriteErr(w, err)
			return
		}
		payload, err := Project(obj, fields)
		if err != nil {
			WriteErr(w, err)
			return
		}
		WriteJSON(w, 200, map[string]any{"dry_run": true, "hint": "no changes were written", "data": payload})
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		obj, err := fn(ctx)
		if err != nil {
			WriteErr(w, err)
			return
		}
		WriteProjected(w, status, obj, fields)
		return
	}
	if len(key) > 200 {
		WriteErr(w, apperr.Validation("idempotency key is too long").WithHint("keep Idempotency-Key under 200 characters"))
		return
	}
	actor, _ := actor.ActorFrom(ctx)
	hash := RequestHash(r.Method, r.URL.Path, r.URL.RawQuery, body)
	var replay []byte
	var replayCode int
	var replayed bool
	for attempt := 0; attempt < 8; attempt++ {
		err := st.WithTx(ctx, func(ctx context.Context) error {
			rec, found, err := st.GetIdempotency(ctx, actor.TokenID, key)
			if err != nil {
				return err
			}
			if found {
				if rec.RequestHash != hash {
					return apperr.IdempotencyMismatch()
				}
				if rec.StatusCode == 0 {
					return errBusy
				}
				replay = []byte(rec.ResponseBody)
				replayCode = rec.StatusCode
				replayed = true
				return nil
			}
			if err := st.InsertIdempotency(ctx, IdemRecord{
				TokenID: actor.TokenID, UserID: actor.UserID, Key: key, Method: r.Method,
				Path: r.URL.Path, RequestHash: hash, StatusCode: 0, ResponseBody: "", CreatedAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
			obj, err := fn(ctx)
			if err != nil {
				return err
			}
			b, err := Marshal(obj)
			if err != nil {
				return err
			}
			b, err = ProjectBytes(b, fields)
			if err != nil {
				return err
			}
			if err := st.FinishIdempotency(ctx, actor.TokenID, key, status, string(b)); err != nil {
				return err
			}
			replay = b
			replayCode = status
			replayed = false
			return nil
		})
		if errors.Is(err, ErrIdempotencyRace) || errors.Is(err, errBusy) {
			time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
			continue
		}
		if err != nil {
			WriteErr(w, err)
			return
		}
		if replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		WriteRaw(w, replayCode, replay)
		return
	}
	WriteErr(w, apperr.Conflict("idempotency key is in progress", "retry this request shortly"))
}
