package httpx

import "testing"

// Fields are picked from the object itself, or from each item of a page.
func TestProjectBytes(t *testing.T) {
	for in, want := range map[string]string{
		`{"id":"a","name":"n","x":1}`:                         `{"id":"a"}`,
		`{"data":[{"id":"a","x":1}],"page":{"total":1}}`:      `{"data":[{"id":"a"}],"page":{"total":1}}`,
		`{"data":[{"id":"a","x":1}],"id":"b","other":"kept"}`: `{"id":"b"}`,
	} {
		got, err := ProjectBytes([]byte(in), []string{"id"})
		if err != nil || string(got) != want {
			t.Errorf("%s: got %s %v, want %s", in, got, err, want)
		}
	}
}
