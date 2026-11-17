package account

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/httpx"
)

// RateAllow counts one request by subject against a named rule and reports
// whether it is within limit per window. The counters live in the database,
// so the limit holds across every instance, which an in-memory counter
// cannot do on serverless. It is a fixed window: a burst straddling a window
// edge can reach twice limit, which is fine for abuse control. A database
// error lets the request through, since refusing sign-ins because the
// limiter is down is the worse failure.
func (a *Accounts) RateAllow(ctx context.Context, rule, subject string, limit int, window time.Duration) bool {
	now := a.now().Unix()
	secs := int64(window / time.Second)
	bucket := now / secs
	// One statement, so concurrent instances agree on the count.
	var hits int
	err := a.DB.QueryRow(ctx, `INSERT INTO rate_limits (key, hits, expires_at) VALUES (?, 1, ?)
		ON CONFLICT(key) DO UPDATE SET hits = rate_limits.hits + 1 RETURNING hits`,
		rule+"|"+subject+"|"+strconv.FormatInt(bucket, 10), (bucket+2)*secs).Scan(&hits)
	if err != nil {
		return true
	}
	if hits == 1 {
		// A key that is new also deletes the windows that have expired, so there is no sweeper.
		_, _ = a.DB.Exec(ctx, `DELETE FROM rate_limits WHERE expires_at < ?`, now)
	}
	return hits <= limit
}

// clientIP is the address rate limits are keyed on and the device page shows
// the approver. The peer address unless the app set ClientIP.
func (a *Accounts) clientIP(r *http.Request) string {
	if a.ClientIP != nil {
		return a.ClientIP(r)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limited refuses the calls past limit a minute from one address. These are
// the routes anyone can reach without a token: each is a guess at a secret
// or a row written for a stranger. oauth answers in RFC 6749's error shape.
func (a *Accounts) limited(rule string, limit int, oauth bool, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.RateAllow(r.Context(), rule, a.clientIP(r), limit, time.Minute) {
			h(w, r)
			return
		}
		w.Header().Set("Retry-After", "60")
		if oauth {
			httpx.WriteJSON(w, http.StatusTooManyRequests, map[string]string{"error": "slow_down", "error_description": "too many requests from this address; wait a minute"})
			return
		}
		httpx.WriteErr(w, apperr.RateLimited("too many sign-in attempts from this address; wait a minute and try again"))
	}
}
