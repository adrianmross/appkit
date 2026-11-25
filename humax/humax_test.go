package humax

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/httpx"
)

type thing struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

type createIn struct {
	Body struct {
		Name string `json:"name" minLength:"1"`
	}
}

var errConflict = apperr.Conflict("thing exists", "pick another name")

func newAPI() http.Handler {
	mux := http.NewServeMux()
	api := New(mux, "test", "0.0.0")
	huma.Register(api, huma.Operation{Method: "POST", Path: "/v1/things", DefaultStatus: 201}, func(ctx context.Context, in *createIn) (*struct{ Body thing }, error) {
		switch in.Body.Name {
		case "dup":
			return nil, errConflict
		case "boom":
			return nil, errors.New("disk on fire")
		}
		act, _ := actor.ActorFrom(ctx)
		return &struct{ Body thing }{thing{ID: "t1", Name: in.Body.Name, Owner: act.UserID}}, nil
	})
	// Stands in for the app's session middleware.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(actor.WithActor(r.Context(), actor.Actor{UserID: "u1"})))
	})
}

// viaHTTPX is what a plain httpx handler answers for err.
func viaHTTPX(err error) (int, string, string) {
	w := httptest.NewRecorder()
	httpx.WriteErr(w, err)
	return w.Code, w.Header().Get("Content-Type"), strings.TrimSpace(w.Body.String())
}

func TestContract(t *testing.T) {
	h := newAPI()
	post := func(path, body string) (int, string, string) {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Header().Get("Content-Type"), strings.TrimSpace(w.Body.String())
	}

	// The actor reaches the handler, and the body names its schema.
	if code, ct, body := post("/v1/things", `{"name":"a"}`); code != 201 || ct != "application/json" || body != `{"$schema":"https://example.com/schemas/Thing.json","id":"t1","name":"a","owner":"u1"}` {
		t.Errorf("ok: got %d %s %s", code, ct, body)
	}

	// Handler errors answer exactly as httpx.WriteErr does.
	for name, err := range map[string]error{"dup": errConflict, "boom": errors.New("disk on fire")} {
		wc, wct, wb := viaHTTPX(err)
		if code, ct, body := post("/v1/things", `{"name":"`+name+`"}`); code != wc || ct != wct || body != wb {
			t.Errorf("%s: got %d %s %s, httpx writes %d %s %s", name, code, ct, body, wc, wct, wb)
		}
	}

	// Refused requests are a 400 validation with httpx's hint, not a 422.
	_, _, ref := viaHTTPX(apperr.Validation("x"))
	hint := ref[strings.Index(ref, `"hint"`):]
	for name, c := range map[string][2]string{
		"schema":             {"/v1/things", `{"name":""}`},
		"unknown body field": {"/v1/things", `{"name":"a","nope":1}`},
		"unknown query":      {"/v1/things?nope=1", `{"name":"a"}`},
		"bad json":           {"/v1/things", `{`},
	} {
		code, ct, body := post(c[0], c[1])
		if code != 400 || ct != "application/json" || !strings.HasPrefix(body, `{"error":{"code":"validation","message":"`) || !strings.HasSuffix(body, hint) {
			t.Errorf("%s: got %d %s %s", name, code, ct, body)
		}
	}
	if _, _, body := post("/v1/things", `{"name":""}`); !strings.Contains(body, "body.name") {
		t.Errorf("validation message does not name the field: %s", body)
	}

	// The spec is served, the CDN docs page is not.
	for path, want := range map[string]int{"/openapi.json": 200, "/docs": 404} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Errorf("GET %s = %d, want %d", path, w.Code, want)
		}
	}
}
