package session

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adrianmross/appkit/actor"
)

// Cookie holds the dashboard's token where page scripts can't read it
// (httpOnly), instead of localStorage. Over HTTPS the cookie is __Host-
// prefixed, which browsers only accept with Secure, Path=/ and no Domain, so
// a sibling subdomain can't plant one. Plain http can't set Secure, so it
// keeps the bare name.
type Cookie struct {
	Name   string
	MaxAge time.Duration
}

func (c Cookie) name(r *http.Request) string {
	if IsHTTPS(r) {
		return "__Host-" + c.Name
	}
	return c.Name
}

// Value is the request's session token, "" without one.
func (c Cookie) Value(r *http.Request) string {
	if ck, err := r.Cookie(c.name(r)); err == nil {
		return ck.Value
	}
	return ""
}

func (c Cookie) Set(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.name(r),
		Value:    token,
		Path:     "/",
		MaxAge:   int(c.MaxAge.Seconds()),
		HttpOnly: true,
		Secure:   IsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// Clear also clears the pre-prefix name, so a browser signed in before the
// rename doesn't keep a dead cookie.
func (c Cookie) Clear(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{c.Name, "__Host-" + c.Name} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: IsHTTPS(r), SameSite: http.SameSiteLaxMode})
	}
}

// IsHTTPS is true behind a TLS-terminating proxy too.
func IsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// SameOrigin reports whether a browser sent r from this origin. Requests
// without either header (curl, the CLI) are not same-origin.
func SameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		return true
	}
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err == nil && strings.EqualFold(u.Host, r.Host) {
			return true
		}
	}
	return false
}

// BearerToken reads the token from an Authorization header value.
func BearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	raw := strings.TrimSpace(header[len(prefix):])
	if raw == "" {
		return "", false
	}
	return raw, true
}

func unsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// Gate decides who a request is from: a bearer token, else the session
// cookie, else Fallback.
type Gate struct {
	Cookie       Cookie
	Authenticate func(ctx context.Context, token string) (actor.Actor, error)
	// Fallback answers for a request that carries no credential, for a
	// deployment whose proxy already authenticated the caller. Nil for none.
	Fallback func(ctx context.Context) (actor.Actor, error)
	// Unauthorized and CrossSite build the refusals.
	Unauthorized, CrossSite func() error
}

// Actor is who sent r. The cookie and the fallback are browser credentials,
// so a write must come from the dashboard's own origin (CSRF). A cookie that
// fails is cleared.
func (g Gate) Actor(w http.ResponseWriter, r *http.Request) (actor.Actor, error) {
	if raw, ok := BearerToken(r.Header.Get("Authorization")); ok {
		return g.Authenticate(r.Context(), raw)
	}
	c := g.Cookie.Value(r)
	if c == "" && g.Fallback == nil {
		return actor.Actor{}, g.Unauthorized()
	}
	if unsafeMethod(r.Method) && !SameOrigin(r) {
		return actor.Actor{}, g.CrossSite()
	}
	if c == "" {
		return g.Fallback(r.Context())
	}
	a, err := g.Authenticate(r.Context(), c)
	if err != nil {
		g.Cookie.Clear(w, r)
	}
	return a, err
}
