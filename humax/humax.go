// Package humax sets up a Huma v2 API that keeps appkit's HTTP contract, for
// an app that describes its routes as typed operations and gets
// /openapi.json from them.
//
//	mux := http.NewServeMux()
//	api := humax.New(mux, "myapp", version)
//	huma.Register(api, huma.Operation{Method: "GET", Path: "/v1/things/{id}"},
//		func(ctx context.Context, in *struct {
//			ID string `path:"id"`
//		}) (*struct{ Body Thing }, error) {
//			act, _ := actor.ActorFrom(ctx) // set by the middleware around mux
//			t, err := things.Get(ctx, act.UserID, in.ID)
//			return &struct{ Body Thing }{t}, err // an *apperr.Error keeps its status, code and hint
//		})
//
// What differs from Huma's defaults: errors are the {"error":{code,message,
// hint}} envelope httpx.WriteErr writes, a request that fails validation is
// a 400 "validation" (not a 422), unknown query parameters are refused
// (unknown body fields already are), and the /docs page, which loads its
// script from a CDN, is off. Huma's "$schema" field stays: an object body
// names its schema, served under /schemas/.
//
// Get and Mutate register an operation with what httpx gives a plain
// handler: ?fields, ?dry_run and Idempotency-Key.
package humax

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/adrianmross/appkit/apperr"
)

// envelope is the error body, the same shape httpx.WriteErr writes. A map,
// not a struct, so Huma adds no "$schema" to it and an error reads the same
// from either kind of handler.
type envelope map[string]*apperr.Error

func (e envelope) Error() string  { return e["error"].Error() }
func (e envelope) GetStatus() int { return e["error"].Status }

// New returns a Huma API serving on mux. It replaces huma.NewError, which is
// process-wide, so every Huma API in the program answers with the envelope.
//
// Each opt may change the configuration before the API is built: an app that
// serves its own document sets cfg.OpenAPIPath to "".
func New(mux *http.ServeMux, title, version string, opts ...func(*huma.Config)) huma.API {
	huma.NewError = newError
	cfg := huma.DefaultConfig(title, version)
	cfg.RejectUnknownQueryParameters = true
	cfg.DocsPath = ""
	for _, opt := range opts {
		opt(&cfg)
	}
	return humago.New(mux, cfg)
}

// newError builds the envelope for a failure Huma reports (a request it
// refused, with errs naming each fault) or a handler returned (status 500
// and the handler's error in errs).
func newError(status int, msg string, errs ...error) huma.StatusError {
	if len(errs) == 1 {
		if e, ok := apperr.As(errs[0]); ok {
			if e.Status == 0 {
				cp := *e
				cp.Status, e = 500, &cp
			}
			return envelope{"error": e}
		}
	}
	var parts []string
	for _, err := range errs {
		if d, ok := err.(huma.ErrorDetailer); ok && d.ErrorDetail().Location != "" {
			parts = append(parts, d.ErrorDetail().Location+": "+d.ErrorDetail().Message)
		} else if err != nil {
			parts = append(parts, err.Error())
		}
	}
	detail := strings.Join(parts, "; ")
	switch {
	case status == 400 || status == 422:
		if detail != "" {
			msg += ": " + detail
		}
		return envelope{"error": apperr.Validation("%s", msg)}
	case status >= 500:
		// As httpx.WriteErr: the cause goes to the server log, not the client.
		slog.Error("request failed", "err", msg+": "+detail)
		return envelope{"error": &apperr.Error{Code: "internal", Message: "internal error", Hint: "check server logs", Status: status}}
	}
	if detail != "" {
		msg += ": " + detail
	}
	code := strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "_")
	return envelope{"error": &apperr.Error{Code: code, Message: msg, Status: status}}
}
