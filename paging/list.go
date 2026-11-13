package paging

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/adrianmross/appkit/apperr"
)

// Request is the paging part of a list call: ?limit, ?cursor and ?sort.
type Request struct {
	Limit  int
	Cursor *Cursor
	Sort   string
}

// ParseRequest reads limit, cursor and sort from a parsed query string.
func ParseRequest(q map[string][]string) (Request, error) {
	var out Request
	first := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	if v := first("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return out, apperr.Validation("invalid limit").WithHint("limit is an integer from 1 to 200")
		}
		out.Limit = n
	}
	if v := first("cursor"); v != "" {
		c, err := DecodeCursor(v)
		if err != nil {
			return out, err
		}
		out.Cursor = &c
	}
	out.Sort = first("sort")
	return out, nil
}

// Order checks the request against the sortable columns (name to SQL
// expression) and returns the expression and direction to sort by. def is
// the sort used when the request names none, with a leading - for
// descending. It also fills in the default limit of 50 and canonicalizes
// r.Sort, which Finish needs.
func (r *Request) Order(cols map[string]string, def string) (expr string, desc bool, err error) {
	if r.Limit == 0 {
		r.Limit = 50
	}
	if r.Limit < 1 || r.Limit > 200 {
		return "", false, apperr.Validation("invalid limit").WithHint("limit is an integer from 1 to 200")
	}
	sort := r.Sort
	if strings.TrimSpace(sort) == "" {
		sort = def
	}
	desc = strings.HasPrefix(sort, "-")
	name := strings.TrimPrefix(sort, "-")
	expr, ok := cols[name]
	if !ok || name == "" {
		return "", false, apperr.Validation("invalid sort %q", sort).WithHint("sort is a field name, with an optional leading - for descending")
	}
	r.Sort = name
	if desc {
		r.Sort = "-" + name
	}
	if r.Cursor != nil && r.Cursor.ID != "" {
		if err := CheckCursorSort(*r.Cursor, r.Sort); err != nil {
			return "", false, err
		}
	}
	return expr, desc, nil
}

// CursorSQL is the WHERE clause that resumes after a cursor. It takes three
// arguments: the cursor's key twice, then its id.
func CursorSQL(expr, idExpr string, desc bool) string {
	op := ">"
	if desc {
		op = "<"
	}
	return fmt.Sprintf("(%s %s ? OR (%s = ? AND %s %s ?))", expr, op, expr, idExpr, op)
}

// OrderSQL is the matching ORDER BY, with the id as the tie-break.
func OrderSQL(expr, idExpr string, desc bool) string {
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	return fmt.Sprintf("ORDER BY %s %s, %s %s", expr, dir, idExpr, dir)
}

// Finish builds the page from rows fetched with a limit of r.Limit+1, after
// r.Order. key is the value an item sorts by, as the cursor carries it.
func Finish[T any](r Request, items []T, total int, key, id func(T) string) Page[T] {
	has := len(items) > r.Limit
	if has {
		items = items[:r.Limit]
	}
	next := ""
	if has {
		last := items[len(items)-1]
		next = EncodeCursor(r.Sort, key(last), id(last))
	}
	if items == nil {
		items = []T{}
	}
	return Page[T]{Data: items, Page: PageMeta{Limit: r.Limit, HasMore: has, NextCursor: next, Total: total}}
}
