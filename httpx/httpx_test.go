package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianmross/appkit/apperr"
	"github.com/adrianmross/appkit/paging"
)

// Fields are picked from the object itself, or from each item of a page.
func TestProjectBytes(t *testing.T) {
	for in, want := range map[string]string{
		`{"id":"a","name":"n","x":1}`:                         `{"id":"a"}`,
		`{"data":[{"id":"a","x":1}],"page":{"total":1}}`:      `{"data":[{"id":"a"}],"page":{"total":1}}`,
		`{"data":[{"id":"a","x":1}],"id":"b","other":"kept"}`: `{"id":"b"}`,
		`[{"id":"a","x":1},{"id":"b"}]`:                       `[{"id":"a"},{"id":"b"}]`,
	} {
		got, err := ProjectBytes([]byte(in), []string{"id"})
		if err != nil || string(got) != want {
			t.Errorf("%s: got %s %v, want %s", in, got, err, want)
		}
	}
}

func do(h http.HandlerFunc, method, target, body string) (int, string) {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

// Each item runs through the handler as its own request; a path that is
// refused, off /v1/ or the batch route itself is answered without running.
func TestBatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/things", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		WriteJSON(w, 201, map[string]any{"got": string(b), "key": r.Header.Get("Idempotency-Key"), "dry": DryRun(r)})
	})
	mux.HandleFunc("GET /v1/missing", func(w http.ResponseWriter, r *http.Request) { WriteErr(w, apperr.NotFound("thing")) })
	batch := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		Batch(mux, w, r, b, func(p string) bool { return p == "/v1/secret" })
	}
	code, out := do(batch, "POST", "/v1/batch?dry_run=1", `{"requests":[
		{"method":"post","path":"/v1/things","body":{"a":1},"idempotency_key":"k"},
		{"path":"/v1/missing"},
		{"path":"/v1/batch"},{"path":"/v1/secret"},{"path":"/healthz"},{"path":"https://x.test/v1/things"}]}`)
	want := `{"failed":5,"results":[{"status":201,"body":{"dry":true,"got":"{\"a\":1}","key":"k"}},{"status":404,"body":`
	if code != 200 || !strings.HasPrefix(out, want) || strings.Count(out, `"status":400`) != 4 || !strings.Contains(out, "other than /v1/batch") {
		t.Fatalf("%d %s", code, out)
	}
	if _, out = do(batch, "POST", "/v1/batch", `{"stop_on_error":true,"requests":[{"path":"/v1/missing"},{"method":"POST","path":"/v1/things"}]}`); !strings.Contains(out, `"failed":1`) || strings.Contains(out, "201") {
		t.Fatalf("stop_on_error: %s", out)
	}
	if code, _ = do(batch, "POST", "/v1/batch", `{"requests":[]}`); code != 400 {
		t.Fatalf("empty batch: %d", code)
	}
}

// No limit returns everything; a limit pages by offset and the cursor resumes.
func TestPageSlice(t *testing.T) {
	type row struct {
		ID string `json:"id"`
		N  int    `json:"n"`
	}
	list := func(w http.ResponseWriter, r *http.Request) {
		PageSlice(w, r, []string{"id"}, func(context.Context) ([]row, error) { return []row{{"a", 1}, {"b", 2}, {"c", 3}}, nil })
	}
	if _, out := do(list, "GET", "/v1/rows", ""); out != `{"data":[{"id":"a","n":1},{"id":"b","n":2},{"id":"c","n":3}],"page":{"limit":3,"has_more":false,"next_cursor":"","total":3}}` {
		t.Fatal(out)
	}
	next := paging.EncodeCursor("offset", "", "2")
	if _, out := do(list, "GET", "/v1/rows?limit=2&fields=id", ""); out != `{"data":[{"id":"a"},{"id":"b"}],"page":{"has_more":true,"limit":2,"next_cursor":"`+next+`","total":3}}` {
		t.Fatal(out)
	}
	if _, out := do(list, "GET", "/v1/rows?limit=2&cursor="+next, ""); !strings.HasPrefix(out, `{"data":[{"id":"c","n":3}],"page":{"limit":2,"has_more":false`) {
		t.Fatal(out)
	}
	for _, bad := range []string{"?limit=201", "?fields=n", "?other=1", "?cursor=" + paging.EncodeCursor("-name", "k", "1")} {
		if code, out := do(list, "GET", "/v1/rows"+bad, ""); code != 400 {
			t.Fatalf("%s: %d %s", bad, code, out)
		}
	}
}

func TestGetOneDeleteOneBoolParam(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/rows/{id}", func(w http.ResponseWriter, r *http.Request) {
		GetOne(w, r, []string{"id"}, func(_ context.Context, id string) (any, error) { return map[string]any{"id": id, "n": 1}, nil })
	})
	mux.HandleFunc("DELETE /v1/rows/{id}", func(w http.ResponseWriter, r *http.Request) {
		// No Idempotency-Key is sent, so the store is never used.
		DeleteOne(nil, w, r, func(_ context.Context, id string, version int) (any, error) {
			return map[string]any{"deleted": id, "version": version}, nil
		})
	})
	if _, out := do(mux.ServeHTTP, "GET", "/v1/rows/r1?fields=id", ""); out != `{"id":"r1"}` {
		t.Fatal(out)
	}
	if _, out := do(mux.ServeHTTP, "DELETE", "/v1/rows/r1?version=3", ""); out != `{"deleted":"r1","version":3}` {
		t.Fatal(out)
	}
	if code, _ := do(mux.ServeHTTP, "DELETE", "/v1/rows/r1", ""); code != 400 {
		t.Fatalf("delete without a version: %d", code)
	}
	if v, err := BoolParam(" TRUE ", "stale"); !v || err != nil {
		t.Fatal(v, err)
	}
	if _, err := BoolParam("maybe", "stale"); err == nil {
		t.Fatal("want an error")
	}
}
