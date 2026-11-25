package humax

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/sqldb"
)

// memIdem is an IdemStore without a database.
type memIdem map[string]httpx.IdemRecord

func (m memIdem) WithTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }
func (m memIdem) GetIdempotency(_ context.Context, _, key string) (httpx.IdemRecord, bool, error) {
	r, ok := m[key]
	return r, ok, nil
}
func (m memIdem) InsertIdempotency(_ context.Context, r httpx.IdemRecord) error {
	m[r.Key] = r
	return nil
}
func (m memIdem) FinishIdempotency(_ context.Context, _, key string, status int, body string) error {
	r := m[key]
	r.StatusCode, r.ResponseBody = status, body
	m[key] = r
	return nil
}

// checked has a rule of its own, as an input may.
type checked struct {
	Write
	Body struct {
		Name string `json:"name"`
	}
}

func (c *checked) Resolve(ctx huma.Context) []error {
	if c.Body.Name == "no" {
		return []error{&huma.ErrorDetail{Location: "body.name", Message: "name is taken"}}
	}
	return nil
}

func TestGetAndMutate(t *testing.T) {
	mux := http.NewServeMux()
	api := New(mux, "test", "0.0.0")
	fields := []string{"id", "name"}
	wrote, dry := 0, 0
	Mutate(api, memIdem{}, huma.Operation{Method: "POST", Path: "/v1/things", DefaultStatus: 201}, fields,
		func(ctx context.Context, in *struct {
			Write
			Body struct {
				Name string `json:"name"`
			}
		}) (thing, error) {
			if sqldb.IsDryRun(ctx) {
				dry++
			} else {
				wrote++
			}
			return thing{ID: "t1", Name: in.Body.Name}, nil
		})
	Get(api, huma.Operation{Method: "GET", Path: "/v1/things/{id}"}, fields,
		func(_ context.Context, in *struct {
			Fields
			ID string `path:"id"`
		}) (thing, error) {
			return thing{ID: in.ID, Name: "a"}, nil
		})
	do := func(method, path, body, key string) (int, string, http.Header) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if key == "dry" {
			r.Header.Set("X-Dry-Run", "true")
		} else if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code, strings.TrimSpace(w.Body.String()), w.Header()
	}
	const full = `{"$schema":"https://example.com/schemas/Thing.json","id":"t1","name":"a","owner":""}`
	for _, c := range []struct {
		name, method, path, body, key string
		code                          int
		want                          string
	}{
		{"get", "GET", "/v1/things/t1", "", "", 200, full},
		{"get projected", "GET", "/v1/things/t1?fields=name", "", "", 200, `{"name":"a"}`},
		{"get unknown field", "GET", "/v1/things/t1?fields=owner", "", "", 400, ""},
		{"write", "POST", "/v1/things", `{"name":"a"}`, "", 201, full},
		{"write projected", "POST", "/v1/things?fields=id", `{"name":"a"}`, "", 201, `{"id":"t1"}`},
		{"dry run", "POST", "/v1/things?dry_run=true", `{"name":"a"}`, "", 200, `{"data":` + full + `,"dry_run":true,"hint":"no changes were written"}`},
		{"dry run header", "POST", "/v1/things", `{"name":"a"}`, "dry", 200, `{"data":` + full + `,"dry_run":true,"hint":"no changes were written"}`},
		{"keyed", "POST", "/v1/things", `{"name":"a"}`, "k1", 201, full},
		{"replay", "POST", "/v1/things", `{ "name": "a" }`, "k1", 201, full},
		{"key reused", "POST", "/v1/things", `{"name":"b"}`, "k1", 409, ""},
	} {
		code, body, h := do(c.method, c.path, c.body, c.key)
		if code != c.code || (c.want != "" && body != c.want) || h.Get("Content-Type") != "application/json" {
			t.Errorf("%s: got %d %s %s", c.name, code, h.Get("Content-Type"), body)
		}
		if got := h.Get("Idempotent-Replayed"); (got == "true") != (c.name == "replay") {
			t.Errorf("%s: Idempotent-Replayed = %q", c.name, got)
		}
	}
	// write, write projected and keyed ran; the replay and the refusal did not.
	if wrote != 3 || dry != 2 {
		t.Errorf("handler ran %d times for real and %d dry, want 3 and 2", wrote, dry)
	}
	// An input's own Resolve runs alongside the one Write brings.
	Mutate(api, memIdem{}, huma.Operation{Method: "POST", Path: "/v1/checked"}, nil, func(_ context.Context, in *checked) (thing, error) {
		return thing{ID: "c1"}, nil
	})
	if code, body, _ := do("POST", "/v1/checked", `{"name":"a"}`, ""); code != 200 || !strings.Contains(body, `"c1"`) {
		t.Errorf("own Resolve, ok: %d %s", code, body)
	}
	if code, body, _ := do("POST", "/v1/checked", `{"name":"no"}`, ""); code != 400 || !strings.Contains(body, "name is taken") {
		t.Errorf("own Resolve, refused: %d %s", code, body)
	}
	// Refusals Huma makes with other statuses are still a 400 validation.
	big := `{"name":"` + strings.Repeat("a", 1<<20) + `"}`
	if code, body, _ := do("POST", "/v1/things", big, ""); code != 400 || !strings.Contains(body, `"code":"validation"`) {
		t.Errorf("too large: %d %.200s", code, body)
	}
	for ct, want := range map[string]int{"application/x-www-form-urlencoded": 201, "text/csv": 400} {
		r := httptest.NewRequest("POST", "/v1/things", strings.NewReader(`{"name":"a"}`))
		r.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want || (want == 400 && !strings.Contains(w.Body.String(), `"code":"validation"`)) {
			t.Errorf("Content-Type %s: %d %s", ct, w.Code, w.Body.String())
		}
	}

	// The document describes the answer as a thing, and the schema is served.
	_, doc, _ := do("GET", "/openapi.json", "", "")
	for _, want := range []string{`"201":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Thing"}`, `"name":"Idempotency-Key"`, `"name":"dry_run"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("openapi.json lacks %s", want)
		}
	}
	if code, body, _ := do("GET", "/schemas/Thing.json", "", ""); code != 200 || !strings.Contains(body, `"owner"`) {
		t.Errorf("GET /schemas/Thing.json = %d %s", code, body)
	}
}
