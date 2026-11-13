package cli

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
)

type state struct{ *Exec }

func TestRunNamesTheApp(t *testing.T) {
	info := AppInfo{Name: "demo", EnvPrefix: "DEMO_"}
	root := &Command[state]{Name: "demo", Children: []*Command[state]{
		{Name: "ping", Summary: "check liveness", Run: func(e *state) error {
			return e.Send("GET", "/healthz", nil, nil, "", false, false)
		}},
		{Name: "gone", Summary: "always missing", Run: func(e *state) error {
			return &ExitError{Code: 3, Message: "no such thing", Hint: "run demo ping"}
		}},
	}}
	connect := func(x *Exec, _ *Command[state]) (*state, error) {
		x.Do = func(method, path string, _ url.Values, _ []byte, _ string, _ Options) (Response, error) {
			return Response{Status: 200, Body: []byte(`{"ok":true}` + "\n"), ContentType: "application/json"}, nil
		}
		return &state{x}, nil
	}
	run := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := Run(info, root, connect, args, &out, &errb)
		return code, out.String(), errb.String()
	}

	if code, out, _ := run("ping"); code != 0 || out != "{\"ok\":true}\n" {
		t.Fatalf("ping: %d %q", code, out)
	}
	if code, _, errs := run("gone"); code != 3 || !strings.Contains(errs, `"code":"not_found"`) {
		t.Fatalf("gone: %d %q", code, errs)
	}
	if code, _, errs := run("nope"); code != 2 || !strings.Contains(errs, "run demo --help") {
		t.Fatalf("unknown command: %d %q", code, errs)
	}
	if code, out, _ := run("schema", "ping"); code != 0 || !strings.Contains(out, `"name":"demo ping"`) {
		t.Fatalf("schema: %d %q", code, out)
	}
	if _, out, _ := run("--help"); !strings.HasPrefix(out, "demo — ") {
		t.Fatalf("help: %q", out)
	}
}
