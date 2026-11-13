// Package cli is an agent-facing command line as data: a tree of Commands,
// the global flags, `schema` and `--help --json`, compact JSON when piped,
// JSON errors on stderr, and the exit codes 0 ok, 2 usage, 3 not found,
// 4 conflict, 5 auth, 6 validation, 1 other. The app supplies its name, its
// command tree and how one invocation reaches its server.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// AppInfo names the app in help, hints and schema output.
type AppInfo struct {
	// Name is the binary, for example "myapp".
	Name string
	// EnvPrefix starts the app's environment variables, for example "MYAPP_".
	EnvPrefix string
}

func (a AppInfo) serverHint() string {
	return "check that " + a.Name + " serve is running and " + a.EnvPrefix + "SERVER is correct"
}

type Flag struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Help       string `json:"help"`
	Repeatable bool   `json:"repeatable,omitempty"`
}

type Arg struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Help     string `json:"help,omitempty"`
	Many     bool   `json:"many,omitempty"`
}

// Command is one node of the tree. E is the app's per-invocation state,
// which usually embeds *Exec.
type Command[E any] struct {
	Name     string         `json:"name"`
	Summary  string         `json:"summary"`
	Usage    string         `json:"usage,omitempty"`
	Example  string         `json:"example,omitempty"`
	Mutating bool           `json:"mutating,omitempty"`
	Args     []Arg          `json:"args,omitempty"`
	Flags    []Flag         `json:"flags,omitempty"`
	Children []*Command[E]  `json:"-"`
	Run      func(*E) error `json:"-"`
}

// Globals are the flags every command takes.
type Globals struct {
	JSON     bool
	Text     bool
	Help     bool
	DryRun   bool
	AllPages bool
	Server   string
	Token    string
	Config   string
	Idem     string
	Fields   string
}

// Options and Response are one API call, as Exec.Do sees it.
type Options struct {
	IdempotencyKey string
	DryRun         bool
}

type Response struct {
	Status      int
	Body        []byte
	ContentType string
}

// Exec is one parsed invocation.
type Exec struct {
	Info   AppInfo
	Args   []string
	Flags  map[string][]string
	G      Globals
	JSON   bool
	Stdout io.Writer
	Stderr io.Writer
	// Do sends one API request. The app's connect sets it.
	Do func(method, path string, q url.Values, body []byte, contentType string, opt Options) (Response, error)
}

// ExitError ends the invocation with Code. Raw, when set, is the server's
// JSON error and is written as it is; otherwise Message and Hint are. Name
// overrides the error code name that Code implies.
type ExitError struct {
	Code    int
	Name    string
	Raw     []byte
	Message string
	Hint    string
}

func (e *ExitError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "exit " + strconv.Itoa(e.Code)
}

// Run executes one CLI invocation. stdout and stderr are separate so tests
// can assert the JSON error channel. connect runs after the arguments parse:
// it loads the app's config and credentials, sets Exec.Do and returns the
// state the command runs with.
func Run[E any](info AppInfo, root *Command[E], connect func(*Exec, *Command[E]) (*E, error), args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	name := info.Name
	rest, g, err := splitGlobals(args)
	if err != nil {
		writeErr(stderr, 2, "usage", err.Error(), "run "+name+" --help")
		return 2
	}
	if len(rest) == 0 {
		if g.Help && g.JSON {
			writeSchemaIndex(name, root, stdout)
			return 0
		}
		if g.Help {
			fmt.Fprint(stdout, helpText(root, name))
			return 0
		}
		writeErr(stderr, 2, "usage", "missing command", "run "+name+" --help")
		return 2
	}
	if rest[0] == "schema" {
		return runSchema(name, root, rest[1:], stdout, stderr)
	}
	cmd, left, err := walk(root, rest)
	if err != nil {
		writeErr(stderr, 2, "usage", err.Error(), "run "+name+" --help")
		return 2
	}
	path := commandPath(name, root, rest)
	if g.Help {
		if g.JSON {
			enc := json.NewEncoder(stdout)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(schemaFor(cmd, path))
			return 0
		}
		fmt.Fprint(stdout, helpText(cmd, path))
		return 0
	}
	if cmd.Run == nil {
		writeErr(stderr, 2, "usage", path+" needs a subcommand", "run "+path+" --help")
		return 2
	}
	parsed, err := parseFlags(left, cmd.Flags)
	if err != nil {
		writeErr(stderr, 2, "usage", err.Error(), "run "+path+" --help")
		return 2
	}
	if err := checkArgs(cmd, parsed.args); err != nil {
		writeErr(stderr, 2, "usage", err.Error(), "run "+path+" --help")
		return 2
	}
	if g.DryRun && !cmd.Mutating {
		writeErr(stderr, 2, "usage", "--dry-run is only valid on mutations", "remove --dry-run")
		return 2
	}
	jsonOut := g.JSON || !isTTY(stdout)
	if g.Text {
		jsonOut = false
	}
	if g.JSON {
		jsonOut = true
	}
	x := &Exec{Info: info, Args: parsed.args, Flags: parsed.flags, G: g, JSON: jsonOut, Stdout: stdout, Stderr: stderr}
	e, err := connect(x, cmd)
	if err == nil {
		err = cmd.Run(e)
	}
	if err != nil {
		var ex *ExitError
		if errors.As(err, &ex) {
			if ex.Raw != nil {
				writeRawErr(stderr, ex.Raw)
			} else if ex.Message != "" {
				name := ex.Name
				if name == "" {
					name = codeName(ex.Code)
				}
				writeErr(stderr, ex.Code, name, ex.Message, ex.Hint)
			}
			return ex.Code
		}
		writeErr(stderr, 1, "internal", err.Error(), "")
		return 1
	}
	return 0
}

func codeName(code int) string {
	switch code {
	case 2:
		return "usage"
	case 3:
		return "not_found"
	case 4:
		return "conflict"
	case 5:
		return "unauthorized"
	case 6:
		return "validation"
	default:
		return "error"
	}
}

func writeErr(w io.Writer, _ int, code, message, hint string) {
	payload := map[string]any{"code": code, "message": message}
	if hint != "" {
		payload["hint"] = hint
	}
	b, _ := json.Marshal(map[string]any{"error": payload})
	fmt.Fprintf(w, "%s\n", b)
}

func writeRawErr(w io.Writer, b []byte) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		writeErr(w, 1, "error", string(b), "")
		return
	}
	if b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	_, _ = w.Write(b)
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func splitGlobals(args []string) ([]string, Globals, error) {
	var g Globals
	var rest []string
	for i := 0; i < len(args); i++ {
		name, val, has := splitEq(args[i])
		switch name {
		case "--json":
			on, n, err := takeBool(val, has, args, i)
			if err != nil {
				return nil, g, err
			}
			i += n
			g.JSON = on
		case "--text":
			on, n, err := takeBool(val, has, args, i)
			if err != nil {
				return nil, g, err
			}
			i += n
			g.Text = on
		case "--help", "-h":
			on, n, err := takeBool(val, has, args, i)
			if err != nil {
				return nil, g, err
			}
			i += n
			g.Help = on
		case "--dry-run":
			on, n, err := takeBool(val, has, args, i)
			if err != nil {
				return nil, g, err
			}
			i += n
			g.DryRun = on
		case "--all-pages":
			on, n, err := takeBool(val, has, args, i)
			if err != nil {
				return nil, g, err
			}
			i += n
			g.AllPages = on
		case "--server", "--token", "--config", "--idempotency-key", "--fields":
			v, n, err := takeVal(name, val, has, args, i)
			if err != nil {
				return nil, g, err
			}
			i += n
			switch name {
			case "--server":
				g.Server = v
			case "--token":
				g.Token = v
			case "--config":
				g.Config = v
			case "--idempotency-key":
				g.Idem = v
			case "--fields":
				g.Fields = v
			}
		default:
			rest = append(rest, args[i])
		}
	}
	return rest, g, nil
}

func splitEq(a string) (name, val string, has bool) {
	if !strings.HasPrefix(a, "-") {
		return a, "", false
	}
	if i := strings.IndexByte(a, '='); i >= 0 {
		return a[:i], a[i+1:], true
	}
	return a, "", false
}

func takeBool(val string, has bool, args []string, i int) (bool, int, error) {
	if has {
		return parseBool(val)
	}
	if i+1 < len(args) {
		if b, _, err := parseBool(args[i+1]); err == nil && (args[i+1] == "true" || args[i+1] == "false") {
			return b, 1, nil
		}
	}
	return true, 0, nil
}

func parseBool(s string) (bool, int, error) {
	switch strings.ToLower(s) {
	case "true", "1":
		return true, 0, nil
	case "false", "0":
		return false, 0, nil
	default:
		return false, 0, fmt.Errorf("invalid boolean %q", s)
	}
}

func takeVal(name, val string, has bool, args []string, i int) (string, int, error) {
	if has {
		if val == "" {
			return "", 0, fmt.Errorf("%s needs a value", name)
		}
		return val, 0, nil
	}
	if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
		return "", 0, fmt.Errorf("%s needs a value", name)
	}
	return args[i+1], 1, nil
}

func walk[E any](root *Command[E], args []string) (*Command[E], []string, error) {
	cur := root
	i := 0
	for i < len(args) {
		if strings.HasPrefix(args[i], "-") {
			break
		}
		var next *Command[E]
		for _, ch := range cur.Children {
			if ch.Name == args[i] {
				next = ch
				break
			}
		}
		if next == nil {
			break
		}
		cur = next
		i++
	}
	if cur == root {
		return nil, nil, fmt.Errorf("unknown command %q", args[0])
	}
	if i < len(args) && !strings.HasPrefix(args[i], "-") && len(cur.Children) > 0 {
		return nil, nil, fmt.Errorf("unknown command %q", strings.Join(args[:i+1], " "))
	}
	return cur, args[i:], nil
}

func commandPath[E any](name string, root *Command[E], args []string) string {
	parts := []string{name}
	cur := root
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			break
		}
		var next *Command[E]
		for _, ch := range cur.Children {
			if ch.Name == a {
				next = ch
				break
			}
		}
		if next == nil {
			break
		}
		parts = append(parts, next.Name)
		cur = next
	}
	return strings.Join(parts, " ")
}

func parseFlags(args []string, flags []Flag) (struct {
	args  []string
	flags map[string][]string
}, error) {
	known := map[string]Flag{}
	for _, f := range flags {
		known["--"+f.Name] = f
	}
	out := struct {
		args  []string
		flags map[string][]string
	}{flags: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out.args = append(out.args, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") {
			out.args = append(out.args, a)
			continue
		}
		name, val, has := splitEq(a)
		f, ok := known[name]
		if !ok {
			return out, fmt.Errorf("unknown flag %s", name)
		}
		key := f.Name
		if f.Type == "bool" {
			on, n, err := takeBool(val, has, args, i)
			if err != nil {
				return out, err
			}
			i += n
			if on {
				out.flags[key] = []string{"true"}
			}
			continue
		}
		v, n, err := takeVal(name, val, has, args, i)
		if err != nil {
			return out, err
		}
		i += n
		if !f.Repeatable && len(out.flags[key]) > 0 {
			return out, fmt.Errorf("flag --%s was given twice", key)
		}
		out.flags[key] = append(out.flags[key], v)
	}
	return out, nil
}

func checkArgs[E any](cmd *Command[E], args []string) error {
	need := 0
	many := false
	for _, a := range cmd.Args {
		if a.Required {
			need++
		}
		if a.Many {
			many = true
		}
	}
	if len(args) < need {
		return fmt.Errorf("%s requires %s", cmd.Name, cmd.Args[0].Name)
	}
	if !many && len(cmd.Args) > 0 && len(args) > len(cmd.Args) {
		return fmt.Errorf("unexpected argument %q", args[len(cmd.Args)])
	}
	if len(cmd.Args) == 0 && len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}
	return nil
}

func helpText[E any](cmd *Command[E], path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n\n", path, cmd.Summary)
	if cmd.Usage != "" {
		fmt.Fprintf(&b, "Usage:\n  %s\n", cmd.Usage)
	} else {
		fmt.Fprintf(&b, "Usage:\n  %s\n", path)
	}
	if len(cmd.Children) > 0 {
		b.WriteString("\nCommands:\n")
		for _, ch := range cmd.Children {
			fmt.Fprintf(&b, "  %-16s %s\n", ch.Name, ch.Summary)
		}
	}
	if len(cmd.Flags) > 0 {
		b.WriteString("\nFlags:\n")
		for _, f := range cmd.Flags {
			extra := ""
			if f.Repeatable {
				extra = ", repeatable"
			}
			fmt.Fprintf(&b, "  --%-14s %s (%s%s)\n", f.Name, f.Help, f.Type, extra)
		}
	}
	b.WriteString("\nGlobal: --json --text --fields --all-pages --dry-run --idempotency-key --server --token\n")
	if cmd.Example != "" {
		fmt.Fprintf(&b, "\nExample:\n  %s\n", cmd.Example)
	}
	return b.String()
}

type schemaNode struct {
	Name     string        `json:"name"`
	Summary  string        `json:"summary"`
	Usage    string        `json:"usage,omitempty"`
	Example  string        `json:"example,omitempty"`
	Mutating bool          `json:"mutating,omitempty"`
	Args     []Arg         `json:"args,omitempty"`
	Flags    []Flag        `json:"flags,omitempty"`
	Children []schemaChild `json:"children,omitempty"`
}

type schemaChild struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

func schemaFor[E any](cmd *Command[E], path string) schemaNode {
	n := schemaNode{
		Name: path, Summary: cmd.Summary, Usage: cmd.Usage, Example: cmd.Example,
		Mutating: cmd.Mutating, Args: cmd.Args, Flags: cmd.Flags,
	}
	for _, ch := range cmd.Children {
		n.Children = append(n.Children, schemaChild{Name: ch.Name, Summary: ch.Summary})
	}
	return n
}

func runSchema[E any](name string, root *Command[E], args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeSchemaIndex(name, root, stdout)
		return 0
	}
	cmd, left, err := walk(root, args)
	if err != nil || len(left) > 0 {
		msg := "unknown command"
		if err != nil {
			msg = err.Error()
		}
		writeErr(stderr, 2, "usage", msg, "run "+name+" schema")
		return 2
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(schemaFor(cmd, name+" "+strings.Join(args, " ")))
	return 0
}

func writeSchemaIndex[E any](name string, root *Command[E], w io.Writer) {
	var items []schemaChild
	var walkFn func(prefix string, c *Command[E])
	walkFn = func(prefix string, c *Command[E]) {
		for _, ch := range c.Children {
			name := strings.TrimSpace(prefix + " " + ch.Name)
			items = append(items, schemaChild{Name: name, Summary: ch.Summary})
			walkFn(name, ch)
		}
	}
	walkFn(name, root)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{"commands": items})
}

func (e *Exec) Flag(name string) string {
	if v := e.Flags[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func (e *Exec) Send(method, path string, q url.Values, body []byte, contentType string, mut, fields bool) error {
	if fields && e.G.Fields != "" {
		if q == nil {
			q = url.Values{}
		}
		q.Set("fields", e.G.Fields)
	}
	opt := Options{}
	if mut {
		opt.DryRun = e.G.DryRun
		opt.IdempotencyKey = e.G.Idem
	}
	if method == "GET" && e.G.AllPages {
		return e.sendAllPages(path, q)
	}
	resp, err := e.Do(method, path, q, body, contentType, opt)
	if err != nil {
		return &ExitError{Code: 1, Message: strings.TrimPrefix(err.Error(), "unavailable: "), Hint: e.Info.serverHint()}
	}
	if resp.Status >= 400 {
		return &ExitError{Code: ExitStatus(resp.Status), Raw: resp.Body}
	}
	return e.Print(resp.Body, resp.ContentType)
}

// sendAllPages follows next_cursor and prints one page holding every item.
func (e *Exec) sendAllPages(path string, q url.Values) error {
	if q == nil {
		q = url.Values{}
	}
	if q.Get("limit") == "" {
		q.Set("limit", "200")
	}
	var all []json.RawMessage
	for {
		resp, err := e.Do("GET", path, q, nil, "", Options{})
		if err != nil {
			return &ExitError{Code: 1, Message: strings.TrimPrefix(err.Error(), "unavailable: "), Hint: e.Info.serverHint()}
		}
		if resp.Status >= 400 {
			return &ExitError{Code: ExitStatus(resp.Status), Raw: resp.Body}
		}
		var pg struct {
			Data []json.RawMessage `json:"data"`
			Page *struct {
				HasMore    bool   `json:"has_more"`
				NextCursor string `json:"next_cursor"`
			} `json:"page"`
		}
		if err := json.Unmarshal(resp.Body, &pg); err != nil || pg.Page == nil {
			return e.Print(resp.Body, resp.ContentType)
		}
		all = append(all, pg.Data...)
		if !pg.Page.HasMore || pg.Page.NextCursor == "" {
			break
		}
		q.Set("cursor", pg.Page.NextCursor)
	}
	if all == nil {
		all = []json.RawMessage{}
	}
	b, _ := json.Marshal(map[string]any{"data": all, "page": map[string]any{"has_more": false, "next_cursor": "", "total": len(all)}})
	return e.Print(b, "application/json")
}

func ExitStatus(code int) int {
	switch code {
	case 400:
		return 6
	case 401, 403:
		return 5
	case 404:
		return 3
	case 409:
		return 4
	default:
		return 1
	}
}

func (e *Exec) Print(body []byte, contentType string) error {
	if e.JSON {
		if strings.Contains(contentType, "text/csv") {
			b, _ := json.Marshal(map[string]string{"format": "simplify-csv", "csv": string(body)})
			fmt.Fprintf(e.Stdout, "%s\n", b)
			return nil
		}
		body = bytes.TrimSpace(body)
		if len(body) == 0 {
			body = []byte("null")
		}
		fmt.Fprintf(e.Stdout, "%s\n", body)
		return nil
	}
	if strings.Contains(contentType, "text/csv") {
		_, err := e.Stdout.Write(body)
		return err
	}
	return e.printText(body)
}

func (e *Exec) printText(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		_, err := e.Stdout.Write(body)
		return err
	}
	if m, ok := v.(map[string]any); ok {
		if data, ok := m["data"].([]any); ok {
			return printTable(e.Stdout, data)
		}
	}
	return printObject(e.Stdout, v, "")
}

func printTable(w io.Writer, rows []any) error {
	if len(rows) == 0 {
		fmt.Fprintln(w, "(empty)")
		return nil
	}
	first, ok := rows[0].(map[string]any)
	if !ok {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(rows)
	}
	cols := make([]string, 0, len(first))
	for k := range first {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(cols, "\t"))
	for _, row := range rows {
		m, _ := row.(map[string]any)
		line := make([]string, len(cols))
		for i, c := range cols {
			line[i] = fmt.Sprint(m[c])
		}
		fmt.Fprintln(tw, strings.Join(line, "\t"))
	}
	return tw.Flush()
}

func printObject(w io.Writer, v any, indent string) error {
	m, ok := v.(map[string]any)
	if !ok {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		val := m[k]
		if child, ok := val.(map[string]any); ok {
			fmt.Fprintf(w, "%s%s:\n", indent, k)
			if err := printObject(w, child, indent+"  "); err != nil {
				return err
			}
			continue
		}
		fmt.Fprintf(w, "%s%s: %s\n", indent, k, fmt.Sprint(val))
	}
	return nil
}

func (e *Exec) JSONBody(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func (e *Exec) VersionOf(path string) (int, error) {
	resp, err := e.Do("GET", path, nil, nil, "", Options{})
	if err != nil {
		return 0, &ExitError{Code: 1, Message: err.Error(), Hint: e.Info.serverHint()}
	}
	if resp.Status >= 400 {
		return 0, &ExitError{Code: ExitStatus(resp.Status), Raw: resp.Body}
	}
	var obj struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(resp.Body, &obj); err != nil || obj.Version < 1 {
		return 0, &ExitError{Code: 1, Message: "response did not include version", Hint: "pass --version explicitly"}
	}
	return obj.Version, nil
}
