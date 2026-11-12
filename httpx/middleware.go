package httpx

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/adrianmross/appkit/apperr"
)

// Trace gives every request an X-Request-Id, logs it when it ends, and turns
// a panic into a 500.
type Trace struct {
	// Log returns the logger for a request.
	Log func() *slog.Logger
	// OnFailure, when set, sees every panic (v != nil) and every 5xx response.
	OnFailure func(r *http.Request, requestID string, status int, v any)
}

func (t Trace) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		rec := &statusRec{ResponseWriter: w, code: 200}
		start := time.Now()
		defer func() {
			if p := recover(); p != nil {
				t.Log().Error("panic", "err", p, "request_id", id)
				if t.OnFailure != nil {
					t.OnFailure(r, id, http.StatusInternalServerError, p)
				}
				// Best effort: the handler may already have written a status.
				WriteErr(w, &apperr.Error{Code: "internal", Message: "internal error", Hint: "retry the request", Status: http.StatusInternalServerError})
				rec.code = http.StatusInternalServerError
			} else if t.OnFailure != nil && rec.code >= 500 {
				t.OnFailure(r, id, rec.code, nil)
			}
			t.Log().Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.code, "ms", time.Since(start).Milliseconds(), "request_id", id)
		}()
		next.ServeHTTP(rec, r)
	})
}

type statusRec struct {
	http.ResponseWriter
	code int
}

func (s *statusRec) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

// CORS answers a browser's cross-origin request, and its preflight, for an
// origin that allow accepts. Other requests pass through untouched.
func CORS(allow func(origin string) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && allow(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Dry-Run, If-Match")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Expose-Headers", "Idempotent-Replayed, X-Request-Id")
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// OriginAllowed reports whether origin matches one of the patterns: "*", an
// exact origin, or a prefix ending in "*".
func OriginAllowed(patterns []string, origin string) bool {
	for _, o := range patterns {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
		if strings.HasSuffix(o, "*") {
			prefix := strings.TrimSuffix(o, "*")
			if prefix != "" && strings.HasPrefix(strings.ToLower(origin), strings.ToLower(prefix)) {
				return true
			}
		}
	}
	return false
}
