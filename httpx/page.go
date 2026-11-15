package httpx

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/paging"
)

// BoolParam reads a true/false query parameter called name; absent is false.
func BoolParam(v, name string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false":
		return false, nil
	case "1", "true":
		return true, nil
	default:
		return false, apperr.Validation("invalid %s", name).WithHint(name + " is true or false")
	}
}

// PageSlice answers a list the app loads whole. Without ?limit it returns
// every row; with it, it pages by offset. params are the query parameters
// the route takes besides limit, cursor and fields. A nil fieldAllow means
// the response takes no ?fields.
// ponytail: offset cursor over an in-memory slice, fine for short lists; use paging.Request with SQL keyset paging for one that grows large.
func PageSlice[T any](w http.ResponseWriter, r *http.Request, fieldAllow []string, load func(context.Context) ([]T, error), params ...string) {
	q, err := Query(r, append([]string{"limit", "cursor", "fields"}, params...)...)
	if err != nil {
		WriteErr(w, err)
		return
	}
	pr, err := paging.ParseRequest(q)
	if err != nil {
		WriteErr(w, err)
		return
	}
	if pr.Limit < 0 || pr.Limit > 200 {
		WriteErr(w, apperr.Validation("invalid limit").WithHint("limit is an integer from 1 to 200"))
		return
	}
	fields := FieldsOf(q)
	if len(fields) > 0 && fieldAllow == nil {
		WriteErr(w, apperr.Validation("fields are not supported on this response"))
		return
	}
	if err := paging.ValidateFields(fields, fieldAllow); err != nil {
		WriteErr(w, err)
		return
	}
	offset := 0
	if pr.Cursor != nil {
		if err := paging.CheckCursorSort(*pr.Cursor, "offset"); err != nil {
			WriteErr(w, err)
			return
		}
		offset, err = strconv.Atoi(pr.Cursor.ID)
		if err != nil || offset < 0 {
			WriteErr(w, apperr.Validation("invalid cursor").WithHint("pass next_cursor from the previous page unchanged"))
			return
		}
	}
	rows, err := load(r.Context())
	if err != nil {
		WriteErr(w, err)
		return
	}
	total := len(rows)
	rows = rows[min(offset, total):]
	limit := pr.Limit
	if limit == 0 {
		limit = len(rows)
	}
	out := paging.Page[T]{Data: rows[:min(limit, len(rows))], Page: paging.PageMeta{Limit: limit, Total: total}}
	if out.Data == nil {
		out.Data = []T{}
	}
	if next := offset + len(out.Data); next < total {
		out.Page.HasMore = true
		out.Page.NextCursor = paging.EncodeCursor("offset", "", strconv.Itoa(next))
	}
	WriteProjected(w, 200, out, fields)
}

// GetOne answers a read of the record named by the {id} path value, honoring ?fields.
func GetOne(w http.ResponseWriter, r *http.Request, allowed []string, load func(ctx context.Context, id string) (any, error)) {
	if _, err := Query(r, "fields"); err != nil {
		WriteErr(w, err)
		return
	}
	fields := FieldsOf(r.URL.Query())
	if err := paging.ValidateFields(fields, allowed); err != nil {
		WriteErr(w, err)
		return
	}
	obj, err := load(r.Context(), r.PathValue("id"))
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteProjected(w, 200, obj, fields)
}

// DeleteOne answers a delete of the record named by the {id} path value. The
// caller must name the version it saw (see VersionParam).
func DeleteOne(st IdemStore, w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, id string, version int) (any, error)) {
	if _, err := Query(r, "version", "dry_run"); err != nil {
		WriteErr(w, err)
		return
	}
	version, err := VersionParam(r)
	if err != nil {
		WriteErr(w, err)
		return
	}
	id := r.PathValue("id")
	Mutate(st, w, r, 200, nil, []byte(strconv.Itoa(version)), func(ctx context.Context) (any, error) {
		return fn(ctx, id, version)
	})
}
