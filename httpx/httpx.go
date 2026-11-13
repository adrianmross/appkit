// Package httpx is the JSON API plumbing a handler needs: reading and
// checking a request, and writing results and errors.
package httpx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/adrianmross/appkit/apperr"
)

// Query returns the query parameters, refusing any not in allowed.
func Query(r *http.Request, allowed ...string) (map[string][]string, error) {
	allow := map[string]bool{}
	for _, k := range allowed {
		allow[k] = true
	}
	var unknown []string
	for k := range r.URL.Query() {
		if !allow[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return nil, apperr.Validation("unknown query parameter %s", strings.Join(unknown, ", ")).WithHint("allowed: " + strings.Join(allowed, ", "))
	}
	return r.URL.Query(), nil
}

// FieldsOf is the fields parameter split on commas, nil when absent.
func FieldsOf(q map[string][]string) []string {
	if q == nil {
		return nil
	}
	raw := strings.TrimSpace(strings.Join(q["fields"], ","))
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// First is the first value of a query parameter, "" when absent.
func First(q map[string][]string, k string) string {
	if len(q[k]) == 0 {
		return ""
	}
	return q[k][0]
}

// Decode reads a JSON body into dst, refusing unknown fields.
func Decode(body []byte, dst any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return apperr.Validation("json body is required").WithHint("see /openapi.json for the request shape")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return apperr.Validation("invalid json: %s", err.Error()).WithHint("see /openapi.json for the request shape")
	}
	return nil
}

// DryRun reports whether the request asked for a dry run.
func DryRun(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("X-Dry-Run"), "true") || r.Header.Get("X-Dry-Run") == "1" {
		return true
	}
	v := strings.ToLower(r.URL.Query().Get("dry_run"))
	return v == "1" || v == "true"
}

// RequestHash identifies a request for idempotency replay.
func RequestHash(method, path, rawQuery string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write([]byte(rawQuery))
	h.Write([]byte{0})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// WriteProjected writes v as JSON, keeping only fields when any are given.
func WriteProjected(w http.ResponseWriter, status int, v any, fields []string) {
	b, err := Marshal(v)
	if err != nil {
		WriteErr(w, err)
		return
	}
	b, err = ProjectBytes(b, fields)
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteRaw(w, status, b)
}

// Project keeps only fields of v, or of each item when v is a page.
func Project(v any, fields []string) (any, error) {
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	b, err = ProjectBytes(b, fields)
	if err != nil {
		return nil, err
	}
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// ProjectBytes is Project over encoded JSON.
func ProjectBytes(b []byte, fields []string) ([]byte, error) {
	if len(fields) == 0 {
		return b, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	v = projectValue(v, fields)
	return Marshal(v)
}

func projectValue(v any, fields []string) any {
	if list, ok := v.([]any); ok {
		for i, item := range list {
			if im, ok := item.(map[string]any); ok {
				list[i] = pick(im, fields)
			}
		}
		return list
	}
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if data, ok := m["data"].([]any); ok {
		if _, has := m["page"]; has {
			for i, item := range data {
				if im, ok := item.(map[string]any); ok {
					data[i] = pick(im, fields)
				}
			}
			m["data"] = data
			return m
		}
	}
	return pick(m, fields)
}

func pick(m map[string]any, fields []string) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		if v, ok := m[f]; ok {
			out[f] = v
		}
	}
	return out
}

// Marshal encodes v as JSON without HTML escaping or a trailing newline.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := Marshal(v)
	if err != nil {
		http.Error(w, `{"error":{"code":"internal","message":"encode failed"}}`, 500)
		return
	}
	WriteRaw(w, status, b)
}

// WriteRaw writes encoded JSON as a response.
func WriteRaw(w http.ResponseWriter, status int, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

// WriteErr writes err as the JSON error envelope. Anything that is not an
// apperr.Error is logged and answered as a 500.
func WriteErr(w http.ResponseWriter, err error) {
	de, ok := apperr.As(err)
	if !ok {
		// The client only sees "internal error"; the cause goes to the server log.
		slog.Error("request failed", "err", err.Error())
		de = &apperr.Error{Code: "internal", Message: "internal error", Hint: "check server logs", Status: 500}
	}
	if de.Status == 0 {
		de.Status = 500
	}
	WriteJSON(w, de.Status, map[string]any{"error": de})
}

// VersionParam is the version from the query or the If-Match header.
func VersionParam(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("version"))
	if raw == "" {
		raw = strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	}
	if raw == "" {
		return 0, apperr.Validation("version is required").WithHint("pass version as a query parameter or If-Match header")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, apperr.Validation("invalid version").WithHint("version is a positive integer from the latest get")
	}
	return n, nil
}
