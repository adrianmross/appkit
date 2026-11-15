package cli

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/adrianmross/oauthlite"
	"github.com/adrianmross/tokenkeep"
)

// Credential is what `auth login` keeps for one server. The token itself
// lives in the OS keyring when one is available (macOS Keychain, Linux
// Secret Service); otherwise it is in the credentials file, which is always
// mode 0600.
type Credential struct {
	Token     string   `json:"token,omitempty"`
	Storage   string   `json:"storage"`
	Method    string   `json:"method,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
}

type credentialsFile struct {
	Servers map[string]Credential `json:"servers"`
}

func (a AppInfo) keyring() tokenkeep.Keyring { return tokenkeep.Keyring{Service: a.Name} }

// CredentialsPath is ~/.config/<name>/credentials.json; <PREFIX>CREDENTIALS
// overrides it.
func (a AppInfo) CredentialsPath() (string, error) {
	if v := strings.TrimSpace(os.Getenv(a.EnvPrefix + "CREDENTIALS")); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", a.Name, "credentials.json"), nil
}

// ServerKey normalizes a server URL so http://h:1/ and http://h:1 share a credential.
func ServerKey(server string) string {
	return strings.TrimRight(strings.TrimSpace(server), "/")
}

// keyringBackend names the OS keyring in use, or "" for the file.
// <PREFIX>AUTH_SECRET_STORE=file forces the file.
func (a AppInfo) keyringBackend() string {
	if strings.ToLower(strings.TrimSpace(os.Getenv(a.EnvPrefix+"AUTH_SECRET_STORE"))) == "file" {
		return ""
	}
	return tokenkeep.Backend()
}

func (a AppInfo) readCredentials() (credentialsFile, string, error) {
	cf := credentialsFile{Servers: map[string]Credential{}}
	path, err := a.CredentialsPath()
	if err != nil {
		return cf, "", err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cf, path, nil
	}
	if err != nil {
		return cf, path, err
	}
	if err := json.Unmarshal(b, &cf); err != nil {
		return cf, path, err
	}
	if cf.Servers == nil {
		cf.Servers = map[string]Credential{}
	}
	return cf, path, nil
}

func writeCredentials(path string, cf credentialsFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// LoadCredential returns the stored credential for server, with Token
// filled from the keyring when that is where it lives. An app's connect
// calls it when neither the flag nor the environment gave a token.
func (a AppInfo) LoadCredential(server string) (Credential, bool, error) {
	cf, _, err := a.readCredentials()
	if err != nil {
		return Credential{}, false, err
	}
	c, ok := cf.Servers[ServerKey(server)]
	if !ok {
		return Credential{}, false, nil
	}
	if c.Storage != "file" {
		tok, err := a.keyring().Read(c.Storage, ServerKey(server))
		if errors.Is(err, tokenkeep.ErrNotFound) {
			return Credential{}, false, nil
		}
		if err != nil {
			return Credential{}, false, err
		}
		c.Token = tok
	}
	return c, c.Token != "", nil
}

// SaveCredential stores c for server: token in the keyring if possible,
// else in the 0600 file. It returns the storage used.
func (a AppInfo) SaveCredential(server string, c Credential) (string, error) {
	key := ServerKey(server)
	cf, path, err := a.readCredentials()
	if err != nil {
		return "", err
	}
	c.Storage = "file"
	if backend := a.keyringBackend(); backend != "" {
		if a.keyring().Write(backend, key, c.Token) == nil {
			c.Storage = backend
			c.Token = ""
		}
	}
	cf.Servers[key] = c
	return c.Storage, writeCredentials(path, cf)
}

// DeleteCredential removes server's credential from the keyring and file.
func (a AppInfo) DeleteCredential(server string) (bool, error) {
	key := ServerKey(server)
	cf, path, err := a.readCredentials()
	if err != nil {
		return false, err
	}
	c, ok := cf.Servers[key]
	if !ok {
		return false, nil
	}
	// A keyring item that can't be deleted (locked, access denied) is an
	// error, and the file entry stays so the two remain in step.
	if c.Storage != "file" {
		if err := a.keyring().Delete(c.Storage, key); err != nil {
			return false, err
		}
	}
	delete(cf.Servers, key)
	return true, writeCredentials(path, cf)
}

// Auth is what the auth commands need to know about an invocation.
type Auth struct {
	Exec *Exec
	// Server is the server's URL.
	Server string
	// Token is the token this invocation sends, and Source where it came
	// from: "flag", "env", "stored" or "none". A flag or environment token
	// always wins over a stored login.
	Token, Source string
}

// Test seams. openBrowser returns an error when no opener could start.
var (
	openBrowser  = defaultOpenBrowser
	sleep        = time.Sleep
	loginTimeout = 5 * time.Minute
)

const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// AuthCommand is `auth login`, `auth logout` and `auth status` against a
// server whose account layer has OAuth on. of reads the invocation's state.
// The CLI's client_id is the app's name + "-cli".
func AuthCommand[E any](info AppInfo, of func(*E) Auth) *Command[E] {
	n := info.Name
	return &Command[E]{Name: "auth", Summary: "sign the CLI in to a server", Children: []*Command[E]{
		{
			Name: "login", Summary: "sign in through the dashboard and store a token for this server",
			Usage:   n + " auth login [--server URL] [--device] [--scope read,write]",
			Example: n + " auth login --device",
			Flags: []Flag{
				{Name: "device", Type: "bool", Help: "use a device code instead of a browser callback (default when there is no display or over SSH)"},
				{Name: "scope", Type: "string", Help: "the scopes to ask for, comma separated (default: all the CLI may hold); approve is never granted to the CLI"},
			},
			Run: func(e *E) error { return authLogin(info, of(e)) },
		},
		{Name: "logout", Summary: "revoke and delete the stored token for this server", Usage: n + " auth logout [--server URL]", Run: func(e *E) error { return authLogout(info, of(e)) }},
		{Name: "status", Summary: "show server, identity, scopes, and where the token came from", Usage: n + " auth status [--server URL]", Run: func(e *E) error { return authStatus(info, of(e)) }},
	}}
}

func (e *Exec) printValue(v any) error {
	b, err := e.JSONBody(v)
	if err != nil {
		return err
	}
	return e.Print(b, "application/json")
}

type whoIs struct {
	User struct {
		ID, Name, Email string
	}
	Token struct {
		ID, Name, Prefix string
		Scopes           []string
	}
}

// request calls the server directly: the login flow runs before there is a
// token for Exec.Do to send.
func request(method, u, token string, form url.Values) (int, []byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return 0, nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, b, err
}

func whoami(info AppInfo, server, token string) (whoIs, error) {
	var me whoIs
	status, body, err := request("GET", server+"/v1/me", token, nil)
	if err != nil {
		return me, &ExitError{Code: 1, Message: err.Error(), Hint: info.serverHint()}
	}
	if status >= 400 {
		return me, &ExitError{Code: ExitStatus(status), Raw: body}
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return me, &ExitError{Code: 1, Message: "unexpected /v1/me response"}
	}
	return me, nil
}

// postForm calls a public OAuth endpoint without any bearer token.
func postForm(info AppInfo, server, path string, vals url.Values) (int, map[string]any, error) {
	status, body, err := request("POST", server+path, "", vals)
	if err != nil {
		return 0, nil, &ExitError{Code: 1, Message: err.Error(), Hint: info.serverHint()}
	}
	var doc map[string]any
	_ = json.NewDecoder(bytes.NewReader(body)).Decode(&doc)
	return status, doc, nil
}

func str(doc map[string]any, k string) string {
	s, _ := doc[k].(string)
	return s
}

func tokenFrom(info AppInfo, status int, doc map[string]any, flow string) (string, error) {
	if status == 200 && str(doc, "access_token") != "" {
		return str(doc, "access_token"), nil
	}
	msg := flow + " login failed"
	if code := str(doc, "error"); code != "" {
		msg += ": " + code
		if d := str(doc, "error_description"); d != "" {
			msg += " (" + d + ")"
		}
	} else {
		msg += fmt.Sprintf(": HTTP %d", status)
	}
	return "", &ExitError{Code: 5, Message: msg, Hint: "run " + info.Name + " auth login again"}
}

func authLogin(info AppInfo, a Auth) error {
	e, server := a.Exec, ServerKey(a.Server)
	scope := strings.Join(strings.FieldsFunc(e.Flag("scope"), func(r rune) bool { return r == ' ' || r == ',' }), " ")
	if strings.Contains(" "+strings.ToLower(scope)+" ", " approve ") {
		return &ExitError{Code: 2, Message: info.Name + " auth login never grants approve", Hint: "approve in the dashboard, or have a human mint an approve token and pass it as " + info.EnvPrefix + "TOKEN"}
	}
	method := "browser"
	if e.Flag("device") == "true" || headless() {
		method = "device"
	}
	var token string
	var err error
	if method == "browser" {
		token, err = loopbackLogin(info, e, server, scope)
	} else {
		token, err = deviceLogin(info, e, server, scope)
	}
	if err != nil {
		return err
	}
	me, err := whoami(info, server, token)
	if err != nil {
		return err
	}
	storage, err := info.SaveCredential(server, Credential{
		Token: token, Method: method, Scopes: me.Token.Scopes, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	path, _ := info.CredentialsPath()
	if err != nil {
		return &ExitError{Code: 1, Message: "save credentials: " + err.Error(), Hint: "check permissions on " + filepath.Dir(path)}
	}
	preferred := info.keyringBackend()
	if preferred == "" {
		preferred = "file"
	}
	fmt.Fprintln(e.Stderr, "Logged in to "+server+" as "+me.User.Email+".")
	return e.printValue(map[string]any{
		"status": "logged_in", "server": server, "auth_method": method,
		"user":   map[string]string{"id": me.User.ID, "name": me.User.Name, "email": me.User.Email},
		"scopes": me.Token.Scopes, "token_prefix": me.Token.Prefix,
		"token_storage":    map[string]string{"stored": storage, "preferred": preferred},
		"credentials_path": path,
	})
}

func authLogout(info AppInfo, a Auth) error {
	e, server := a.Exec, ServerKey(a.Server)
	hint := "unlock the keychain and run " + info.Name + " auth logout again"
	c, ok, err := info.LoadCredential(server)
	if err != nil {
		return &ExitError{Code: 1, Message: "cannot read the stored token, so nothing was revoked or removed: " + err.Error(), Hint: hint}
	}
	revoked := false
	if ok {
		status, _, err := postForm(info, server, "/v1/auth/revoke", url.Values{"token": {c.Token}})
		revoked = err == nil && status == 200
		if !revoked {
			fmt.Fprintln(e.Stderr, "Could not revoke the token on "+server+"; deleting it locally anyway.")
		}
	}
	removed, err := info.DeleteCredential(server)
	if err != nil {
		return &ExitError{Code: 1, Message: fmt.Sprintf("revoked=%v, but the stored credential was not removed: %s", revoked, err.Error()), Hint: hint}
	}
	out := map[string]any{"status": "logged_out", "server": server, "revoked": revoked, "removed": removed}
	if a.Source == "env" || a.Source == "flag" {
		out["note"] = "a token from " + a.Source + " is still set; it was not changed"
	}
	return e.printValue(out)
}

func authStatus(info AppInfo, a Auth) error {
	server := ServerKey(a.Server)
	if a.Token == "" {
		return &ExitError{Code: 5, Message: "not logged in to " + server, Hint: "run " + info.Name + " auth login, or set " + info.EnvPrefix + "TOKEN"}
	}
	me, err := whoami(info, server, a.Token)
	if err != nil {
		return err
	}
	return a.Exec.printValue(map[string]any{
		"logged_in": true, "server": server, "source": a.Source,
		"user":   map[string]string{"id": me.User.ID, "name": me.User.Name, "email": me.User.Email},
		"token":  map[string]any{"id": me.Token.ID, "name": me.Token.Name, "prefix": me.Token.Prefix},
		"scopes": me.Token.Scopes,
	})
}

// loopbackLogin is RFC 8252 authorization code + PKCE with a one-shot
// listener on 127.0.0.1. The dashboard's /authorize page does sign-in and
// consent, then sends the browser back to our /callback.
func loopbackLogin(info AppInfo, e *Exec, server, scope string) (string, error) {
	clientID := info.Name + "-cli"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", &ExitError{Code: 1, Message: "listen on 127.0.0.1: " + err.Error(), Hint: "use " + info.Name + " auth login --device"}
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	verifier, err := oauthlite.RandomURLToken(48)
	if err != nil {
		return "", err
	}
	state, err := oauthlite.RandomURLToken(24)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"code_challenge": {oauthlite.PKCEChallenge(verifier)}, "code_challenge_method": {"S256"}, "state": {state},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	authURL := server + "/authorize?" + q.Encode()

	type result struct{ code, errCode string }
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/callback" || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		res := result{code: q.Get("code"), errCode: q.Get("error")}
		msg := "The " + info.Name + " CLI is signed in. You can close this tab."
		if res.code == "" {
			msg = "Sign-in was not completed (" + res.errCode + "). You can close this tab."
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>%s</title><p style=\"font:16px system-ui;margin:3rem\">%s</p>", html.EscapeString(info.Name), html.EscapeString(msg))
		select {
		case done <- res:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	if err := openBrowser(authURL); err != nil {
		fmt.Fprintln(e.Stderr, "Could not open a browser. Open this URL to sign in:")
	} else {
		fmt.Fprintln(e.Stderr, "Opened your browser. If it did not open, visit:")
	}
	fmt.Fprintln(e.Stderr, "  "+authURL)
	fmt.Fprintln(e.Stderr, "Waiting for the browser on "+redirect+" (or run "+info.Name+" auth login --device) ...")

	var res result
	select {
	case res = <-done:
	case <-time.After(loginTimeout):
		return "", &ExitError{Code: 5, Message: "timed out waiting for the browser", Hint: "run " + info.Name + " auth login --device"}
	}
	if res.code == "" {
		code := res.errCode
		if code == "" {
			code = "no code returned"
		}
		return "", &ExitError{Code: 5, Message: "browser login failed: " + code, Hint: "run " + info.Name + " auth login again"}
	}
	status, doc, err := postForm(info, server, "/v1/auth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {res.code}, "code_verifier": {verifier},
		"redirect_uri": {redirect}, "client_id": {clientID},
	})
	if err != nil {
		return "", err
	}
	return tokenFrom(info, status, doc, "browser")
}

// deviceLogin is RFC 8628: show a code, the human approves it in the
// dashboard, and we poll until the server answers.
func deviceLogin(info AppInfo, e *Exec, server, scope string) (string, error) {
	clientID := info.Name + "-cli"
	retry := "run " + info.Name + " auth login --device again"
	start := url.Values{"client_id": {clientID}}
	if scope != "" {
		start.Set("scope", scope)
	}
	status, d, err := postForm(info, server, "/v1/auth/device_authorization", start)
	if err != nil {
		return "", err
	}
	deviceCode := str(d, "device_code")
	if status != 200 || deviceCode == "" {
		_, err := tokenFrom(info, status, d, "device")
		return "", err
	}
	interval := 5
	if n, ok := d["interval"].(float64); ok && n > 0 {
		interval = int(n)
	}
	expires := 600
	if n, ok := d["expires_in"].(float64); ok && n > 0 {
		expires = int(n)
	}
	fmt.Fprintln(e.Stderr, "Open "+str(d, "verification_uri")+" while signed in to the dashboard and enter:")
	fmt.Fprintln(e.Stderr, "  "+str(d, "user_code"))
	fmt.Fprintln(e.Stderr, "Waiting for approval ...")
	deadline := time.Now().Add(time.Duration(expires) * time.Second)
	for time.Now().Before(deadline) {
		sleep(time.Duration(interval) * time.Second)
		status, doc, err := postForm(info, server, "/v1/auth/token", url.Values{"grant_type": {deviceGrantType}, "device_code": {deviceCode}, "client_id": {clientID}})
		if err != nil {
			return "", err
		}
		switch str(doc, "error") {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5
			continue
		case "expired_token":
			return "", &ExitError{Code: 5, Message: "the device code expired before it was approved", Hint: retry}
		case "access_denied":
			return "", &ExitError{Code: 5, Message: "the login was denied in the dashboard", Hint: retry + " to retry"}
		}
		return tokenFrom(info, status, doc, "device")
	}
	return "", &ExitError{Code: 5, Message: "the device code expired before it was approved", Hint: retry}
}

// headless guesses that a browser on this machine can't reach us: an SSH
// session, or Linux with no display server.
func headless() bool {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return true
	}
	return runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
}

func defaultOpenBrowser(u string) error {
	var cmd *osexec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = osexec.Command("open", u)
	case "windows":
		cmd = osexec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = osexec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
