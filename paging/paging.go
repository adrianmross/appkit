// Package paging holds the list envelope, cursors and field projection checks.
package paging

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/adrianmross/appkit/apperr"
)

type PageMeta struct {
	Limit      int    `json:"limit"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor"`
	Total      int    `json:"total"`
}

type Page[T any] struct {
	Data []T      `json:"data"`
	Page PageMeta `json:"page"`
}

type Cursor struct {
	V    int    `json:"v"`
	Sort string `json:"sort"`
	K    string `json:"k"`
	ID   string `json:"id"`
}

func EncodeCursor(sort, key, id string) string {
	b, _ := json.Marshal(Cursor{V: 1, Sort: sort, K: key, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string) (Cursor, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			return Cursor{}, apperr.Validation("invalid cursor").WithHint("pass next_cursor from the previous page unchanged")
		}
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil || c.V != 1 || c.ID == "" {
		return Cursor{}, apperr.Validation("invalid cursor").WithHint("pass next_cursor from the previous page unchanged")
	}
	return c, nil
}

func CheckCursorSort(c Cursor, sort string) error {
	if c.ID == "" {
		return nil
	}
	if c.Sort != sort {
		return apperr.Validation("cursor does not match sort %q", sort).WithHint("reuse the cursor only with the same sort, or omit the cursor")
	}
	return nil
}

func ValidateFields(requested, allowed []string) error {
	allow := map[string]bool{}
	for _, f := range allowed {
		allow[f] = true
	}
	var unknown []string
	for _, f := range requested {
		if !allow[f] {
			unknown = append(unknown, f)
		}
	}
	if len(unknown) > 0 {
		return apperr.Validation("unknown field %s", strings.Join(unknown, ", ")).WithHint("allowed fields: " + strings.Join(allowed, ", "))
	}
	return nil
}
