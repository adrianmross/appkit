package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianmross/appkit/account"
	"github.com/adrianmross/appkit/migrations"
	"github.com/adrianmross/appkit/sqldb"
)

// `auth login` against a real account server, by browser and by device
// code, with the human's part played by the owner token; then status and
// logout, which revokes.
func TestAuthCommands(t *testing.T) {
	ctx := context.Background()
	db, err := sqldb.Open("sqlite:" + filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.SQL.Close()
	if err := migrations.Up(ctx, db.SQL, db.Dialect, 0); err != nil {
		t.Fatal(err)
	}
	acc := &account.Accounts{DB: db, App: "demo", EnvPrefix: "DEMO_", TokenMarker: "dmo_", Scopes: []string{"read", "write", account.ApproveScope}, OAuth: &account.OAuth{}}
	mux := http.NewServeMux()
	acc.Routes(mux)
	srv := httptest.NewServer(acc.Handler(mux))
	defer srv.Close()
	owner, err := acc.Bootstrap(ctx, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	human := func(method, path, body string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+owner)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != 200 {
			t.Fatalf("%s %s: %d %v", method, path, res.StatusCode, out)
		}
		return out
	}

	creds := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv("DEMO_CREDENTIALS", creds)
	t.Setenv("DEMO_AUTH_SECRET_STORE", "file")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	t.Setenv("DISPLAY", ":0")
	info := AppInfo{Name: "demo", EnvPrefix: "DEMO_"}
	type state struct{ a Auth }
	root := &Command[state]{Name: "demo", Children: []*Command[state]{AuthCommand(info, func(s *state) Auth { return s.a })}}
	run := func(args ...string) (int, map[string]any, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := Run(info, root, func(x *Exec, _ *Command[state]) (*state, error) {
			a := Auth{Exec: x, Server: srv.URL, Source: "none"}
			if c, ok, _ := info.LoadCredential(srv.URL); ok {
				a.Token, a.Source = c.Token, "stored"
			}
			return &state{a}, nil
		}, args, &stdout, &stderr)
		var out map[string]any
		_ = json.Unmarshal(stdout.Bytes(), &out)
		return code, out, stderr.String()
	}

	if code, _, _ := run("auth", "status"); code != 5 {
		t.Fatalf("status before login: exit %d", code)
	}
	if code, _, errText := run("auth", "login", "--scope", "read,approve"); code != 2 || !strings.Contains(errText, "never grants approve") {
		t.Fatalf("asking for approve: exit %d %s", code, errText)
	}

	// Browser: the "browser" is handed the authorize URL, approves as the
	// owner and follows the redirect back to the CLI's listener.
	defer func(o func(string) error) { openBrowser = o }(openBrowser)
	openBrowser = func(u string) error {
		q, _ := url.Parse(u)
		if !strings.HasPrefix(u, srv.URL+"/authorize?") {
			t.Errorf("authorize URL %s", u)
		}
		in := map[string]any{"approve": true}
		for k, v := range q.Query() {
			if k != "response_type" {
				in[k] = v[0]
			}
		}
		body, _ := json.Marshal(in)
		go func() {
			res, err := http.Get(human("POST", "/v1/oauth/authorize", string(body))["redirect_to"].(string))
			if err == nil {
				res.Body.Close()
			}
		}()
		return nil
	}
	code, out, errText := run("auth", "login", "--scope", "read")
	if code != 0 || out["auth_method"] != "browser" || len(out["scopes"].([]any)) != 1 {
		t.Fatalf("browser login: exit %d %v %s", code, out, errText)
	}
	if st, _ := os.Stat(creds); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file mode: %v", st)
	}
	if code, out, _ := run("auth", "status"); code != 0 || out["source"] != "stored" || out["token"].(map[string]any)["name"] != "cli" {
		t.Fatalf("status: exit %d %v", code, out)
	}
	first, _, _ := info.LoadCredential(srv.URL)

	// Device: the human types the code the CLI printed while it sleeps.
	defer func(s func(time.Duration)) { sleep = s }(sleep)
	var stderr *bytes.Buffer
	approved := false
	sleep = func(time.Duration) {
		if approved {
			return
		}
		approved = true
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		userCode := strings.TrimSpace(lines[len(lines)-2])
		body, _ := json.Marshal(map[string]any{"user_code": userCode, "approve": true})
		human("POST", "/v1/oauth/device/approve", string(body))
	}
	var stdout bytes.Buffer
	stderr = &bytes.Buffer{}
	if code := Run(info, root, func(x *Exec, _ *Command[state]) (*state, error) {
		return &state{Auth{Exec: x, Server: srv.URL + "/", Source: "none"}}, nil
	}, []string{"auth", "login", "--device"}, &stdout, stderr); code != 0 || !strings.Contains(stdout.String(), `"auth_method":"device"`) {
		t.Fatalf("device login: exit %d %s %s", code, stdout.String(), stderr.String())
	}

	// Logout revokes the stored token on the server and forgets it.
	second, _, _ := info.LoadCredential(srv.URL)
	if second.Token == "" || second.Token == first.Token {
		t.Fatalf("stored credential after second login: %+v", second)
	}
	if code, out, _ := run("auth", "logout"); code != 0 || out["revoked"] != true || out["removed"] != true {
		t.Fatalf("logout: exit %d %v", code, out)
	}
	if _, err := acc.Authenticate(ctx, second.Token); err == nil {
		t.Fatal("the token still works after logout")
	}
	if _, ok, _ := info.LoadCredential(srv.URL); ok {
		t.Fatal("the credential is still stored after logout")
	}
}
