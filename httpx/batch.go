package httpx

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/adrianmross/appkit/apperr"
)

// MaxBatch is the most requests one batch may hold.
const MaxBatch = 100

// Batch answers a batch route: body lists requests, and each runs through h
// as a direct call would, with the batch request's context (so the same
// actor), which makes scopes, validation, idempotency and dry-run apply per
// item. Items run in order and are not atomic. A path outside /v1/, the batch
// route itself, or one refuse reports is answered 400 without being run.
// ponytail: sequential and non-atomic; wrap in one tx if partial batches bite.
func Batch(h http.Handler, w http.ResponseWriter, r *http.Request, body []byte, refuse func(path string) bool) {
	var in struct {
		Requests []struct {
			Method         string          `json:"method"`
			Path           string          `json:"path"`
			Body           json.RawMessage `json:"body"`
			IdempotencyKey string          `json:"idempotency_key"`
		} `json:"requests"`
		StopOnError bool `json:"stop_on_error"`
	}
	if err := Decode(body, &in); err != nil {
		WriteErr(w, err)
		return
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) != "" {
		WriteErr(w, apperr.Validation("batch does not take an Idempotency-Key header").WithHint("put idempotency_key on each item"))
		return
	}
	if len(in.Requests) == 0 || len(in.Requests) > MaxBatch {
		WriteErr(w, apperr.Validation("requests must hold 1 to %d items", MaxBatch).WithHint("split larger batches"))
		return
	}
	type result struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body,omitempty"`
	}
	results := make([]result, 0, len(in.Requests))
	failed := 0
	for _, item := range in.Requests {
		method := strings.ToUpper(strings.TrimSpace(item.Method))
		if method == "" {
			method = http.MethodGet
		}
		u, err := url.Parse(item.Path)
		if err != nil || !strings.HasPrefix(u.Path, "/v1/") || u.Path == r.URL.Path || (refuse != nil && refuse(u.Path)) || u.Host != "" {
			b, _ := Marshal(map[string]any{"error": apperr.Validation("invalid batch path %q", item.Path).WithHint("paths are /v1/... API routes other than " + r.URL.Path)})
			results = append(results, result{Status: 400, Body: b})
			failed++
			if in.StopOnError {
				break
			}
			continue
		}
		var rdr io.Reader = http.NoBody
		if len(item.Body) > 0 && string(item.Body) != "null" {
			rdr = bytes.NewReader(item.Body)
		}
		sub, err := http.NewRequestWithContext(r.Context(), method, u.RequestURI(), rdr)
		if err != nil {
			WriteErr(w, apperr.Validation("%s", err.Error()))
			return
		}
		sub.Header.Set("Content-Type", "application/json")
		if DryRun(r) {
			sub.Header.Set("X-Dry-Run", "true")
		}
		if item.IdempotencyKey != "" {
			sub.Header.Set("Idempotency-Key", item.IdempotencyKey)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, sub)
		out := bytes.TrimSpace(rec.Body.Bytes())
		if len(out) > 0 && !json.Valid(out) {
			out, _ = Marshal(string(out))
		}
		results = append(results, result{Status: rec.Code, Body: out})
		if rec.Code >= 400 {
			failed++
			if in.StopOnError {
				break
			}
		}
	}
	WriteJSON(w, 200, map[string]any{"results": results, "failed": failed})
}
