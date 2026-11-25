package humax

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/paging"
	"github.com/adrianmross/appkit/sqldb"
)

// Fields is embedded in the input of an operation registered with Get, which
// then honors ?fields=a,b.
type Fields struct {
	Fields []string `query:"fields" doc:"Keep only these fields of the object, or of each item of a page."`
}

type requestKey struct{}

// keepRequest is the middleware New adds: Get and Mutate need the request
// (its URL, the answer's headers) and a Huma handler is given only a context.
func keepRequest(ctx huma.Context, next func(huma.Context)) {
	next(huma.WithValue(ctx, requestKey{}, ctx))
}

func request(ctx context.Context) huma.Context {
	hctx, ok := ctx.Value(requestKey{}).(huma.Context)
	if !ok {
		panic("humax: Get and Mutate need an API built by humax.New")
	}
	return hctx
}

func (f *Fields) fieldsOf() *Fields { return f }

// Write is embedded in the input of an operation registered with Mutate,
// which then honors ?fields, ?dry_run and Idempotency-Key.
type Write struct {
	Fields
	DryRun         bool   `query:"dry_run" doc:"Run the write and roll it back; the answer is {dry_run, hint, data}."`
	DryRunHeader   string `header:"X-Dry-Run" doc:"true or 1, as dry_run=true."`
	IdempotencyKey string `header:"Idempotency-Key" maxLength:"200" doc:"A repeat of the same request with the same key replays the first answer."`
}

func (w *Write) writeOf() *Write { return w }

// raw is what Get and Mutate hand Huma: a body already encoded, because a
// projection and a replay are not a T. Its headers are set on the request's
// huma.Context, so the document does not list them on every answer.
type raw struct {
	Status int
	Body   []byte
}

func answer(hctx huma.Context, status int, body []byte) *raw {
	hctx.SetHeader("Content-Type", "application/json")
	return &raw{Status: status, Body: body}
}

// Get registers a read whose answer is fn's T, as huma.Register would, and
// honors ?fields: only fields in allowed may be asked for. I embeds Fields.
func Get[I, T any](api huma.API, op huma.Operation, allowed []string, fn func(context.Context, *I) (T, error)) {
	if _, ok := any(new(I)).(interface{ fieldsOf() *Fields }); !ok {
		panic("humax.Get: " + reflect.TypeFor[I]().String() + " does not embed humax.Fields")
	}
	status := respond[T](api, &op)
	huma.Register(api, op, func(ctx context.Context, in *I) (*raw, error) {
		f := any(in).(interface{ fieldsOf() *Fields }).fieldsOf()
		if err := checkFields(f.Fields, allowed); err != nil {
			return nil, err
		}
		v, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		hctx := request(ctx)
		b, err := encode(api, hctx, f.Fields, status, v)
		if err != nil {
			return nil, err
		}
		return answer(hctx, status, b), nil
	})
}

// Mutate registers a write whose answer is fn's T, with what httpx.Mutate
// gives a plain handler: ?fields, a dry run (fn runs with sqldb.WithDryRun,
// so its transaction rolls back), and Idempotency-Key (fn runs in the
// transaction that records the answer; a repeat replays it). I embeds Write.
func Mutate[I, T any](api huma.API, st httpx.IdemStore, op huma.Operation, allowed []string, fn func(context.Context, *I) (T, error)) {
	if _, ok := any(new(I)).(interface{ writeOf() *Write }); !ok {
		panic("humax.Mutate: " + reflect.TypeFor[I]().String() + " does not embed humax.Write")
	}
	status := respond[T](api, &op)
	huma.Register(api, op, func(ctx context.Context, in *I) (*raw, error) {
		w := any(in).(interface{ writeOf() *Write }).writeOf()
		if err := checkFields(w.Fields.Fields, allowed); err != nil {
			return nil, err
		}
		hctx := request(ctx)
		run := func(ctx context.Context) ([]byte, error) {
			v, err := fn(ctx, in)
			if err != nil {
				return nil, err
			}
			return encode(api, hctx, w.Fields.Fields, status, v)
		}
		code := status
		var body []byte
		var err error
		switch {
		case w.DryRun || strings.EqualFold(w.DryRunHeader, "true") || w.DryRunHeader == "1":
			code = 200
			if body, err = run(sqldb.WithDryRun(ctx)); err == nil {
				body = append(append([]byte(`{"data":`), body...), `,"dry_run":true,"hint":"no changes were written"}`...)
			}
		case w.IdempotencyKey == "":
			body, err = run(ctx)
		default:
			// ponytail: the hash is over the decoded body re-encoded, not the
			// bytes sent, so a key first used through a plain httpx handler does
			// not match here. Hash RawBody if an app ports routes under live keys.
			var sent []byte
			if b := reflect.ValueOf(in).Elem().FieldByName("Body"); b.IsValid() {
				if sent, err = httpx.Marshal(b.Interface()); err != nil {
					return nil, err
				}
			}
			u := hctx.URL()
			var replayed bool
			body, code, replayed, err = httpx.Idempotent(ctx, st, w.IdempotencyKey, hctx.Method(), u.Path,
				httpx.RequestHash(hctx.Method(), u.Path, u.RawQuery, sent), status, run)
			if replayed {
				hctx.SetHeader("Idempotent-Replayed", "true")
			}
		}
		if err != nil {
			return nil, err
		}
		return answer(hctx, code, body), nil
	})
}

// respond documents T as op's answer, since raw says nothing of it, and
// answers the status the operation succeeds with.
func respond[T any](api huma.API, op *huma.Operation) int {
	if op.DefaultStatus == 0 {
		op.DefaultStatus = http.StatusOK
	}
	if op.Responses == nil {
		op.Responses = map[string]*huma.Response{}
	}
	schema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[T](), true, "")
	op.Responses[strconv.Itoa(op.DefaultStatus)] = &huma.Response{
		Description: http.StatusText(op.DefaultStatus),
		Content:     map[string]*huma.MediaType{"application/json": {Schema: schema}},
	}
	return op.DefaultStatus
}

func checkFields(fields, allowed []string) error {
	if len(fields) > 0 && len(allowed) == 0 {
		return apperr.Validation("fields are not supported on this response")
	}
	return paging.ValidateFields(fields, allowed)
}

// encode is v as Huma would write it ("$schema" included), then projected.
// A projection keeps "$schema" only when it is asked for.
func encode(api huma.API, hctx huma.Context, fields []string, status int, v any) ([]byte, error) {
	v, err := api.Transform(hctx, strconv.Itoa(status), v)
	if err != nil {
		return nil, err
	}
	b, err := httpx.Marshal(v)
	if err != nil {
		return nil, err
	}
	return httpx.ProjectBytes(b, fields)
}
