package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/adrianmross/oauthlite"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/httpx"
	"github.com/adrianmross/appkit/session"
	"github.com/adrianmross/appkit/sqldb"
)

// OAuthClients turns on clients other than the app's own CLI: a ChatGPT
// app, a Claude connector. They identify themselves two ways. A client ID
// that is an https URL names a metadata document the server fetches (CIMD).
// Anything else registers first through POST /v1/auth/register (RFC 7591).
// Both are public clients: PKCE, no secret, and marked unverified on the
// consent page.
//
// Their tokens are not the CLI's forever tokens. The access token lasts an
// hour. A refresh token replaces it, is used once, and is answered with the
// next one. Using one a second time means it leaked, so the whole consent is
// revoked. The chain dies after 30 idle days or 90 days in all.
//
// It adds, all public:
//
//	POST /v1/auth/register                              RFC 7591 (JSON)
//	POST /v1/auth/token  grant_type=refresh_token
//	GET  /.well-known/oauth-authorization-server        RFC 8414
//	GET  /.well-known/oauth-protected-resource[/path]   RFC 9728
type OAuthClients struct {
	// PublicURL is the issuer. Empty uses the origin of the request.
	PublicURL string
	// RefreshMarker starts every refresh token. Default TokenMarker + "r_".
	RefreshMarker string
	// IDMarker starts the ID of a registered client. Default "c_".
	IDMarker string
	// AnyLoopback accepts a plain client_id nobody registered as a local
	// tool held to the CLI's loopback redirect rule, instead of refusing it.
	AnyLoopback bool
	// FetchDoc fetches a client metadata document. Nil uses FetchClientDoc.
	FetchDoc func(ctx context.Context, url string) ([]byte, error)
}

const (
	// OfflineAccess asks for a refresh token. Registered clients always get
	// one, so it is accepted and dropped.
	OfflineAccess   = "offline_access"
	ClientAccessTTL = time.Hour
	RefreshIdleTTL  = 30 * 24 * time.Hour
	RefreshMaxTTL   = 90 * 24 * time.Hour
	// RefreshGrace is how long after a refresh token is used that presenting
	// it again is taken as a client retry or a parallel request, not theft.
	// It gets another valid pair; after the window the whole connection goes.
	RefreshGrace = 10 * time.Second

	maxClientID       = 64
	maxClientIDURL    = 512
	clientDocMaxBytes = 64 << 10
	clientDocTTL      = time.Hour
	maxRedirectURIs   = 5
	maxDCRClients     = 5000
	dcrUnusedTTL      = 30 * 24 * time.Hour
)

func (a *Accounts) clients() *OAuthClients {
	if a.OAuth == nil {
		return nil
	}
	return a.OAuth.Clients
}

func (a *Accounts) refreshMarker() string {
	if m := a.clients().RefreshMarker; m != "" {
		return m
	}
	return a.TokenMarker + "r_"
}

// oauthIssuer is the configured public URL, else the origin of the request.
func (a *Accounts) oauthIssuer(r *http.Request) string {
	if c := a.clients(); c != nil && c.PublicURL != "" {
		return strings.TrimRight(c.PublicURL, "/")
	}
	return PublicBase(r)
}

// oauthClient is who is asking for a token, as the consent page shows it.
type oauthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
	// Source is builtin (the CLI and loopback tools), cimd or dcr.
	Source     string
	CreatedAt  string
	LastUsedAt string
	// ExpiresAt is when a cimd document must be fetched again; empty for dcr.
	ExpiresAt string
}

func isClientIDURL(id string) bool { return strings.HasPrefix(id, "https://") }

func (a *Accounts) normalizeClientID(c string) (string, bool) {
	if c == "" {
		return a.oauthClientID(), true
	}
	if isClientIDURL(c) {
		return c, len(c) <= maxClientIDURL
	}
	return c, len(c) <= maxClientID
}

// resolveClient finds the client for a client_id. Builtin clients keep the
// strict loopback rule.
func (a *Accounts) resolveClient(ctx context.Context, id string) (oauthClient, error) {
	id, ok := a.normalizeClientID(id)
	if !ok {
		return oauthClient{}, oauthErr("invalid_request", fmt.Sprintf("client_id is at most %d characters", maxClientID))
	}
	if id == a.oauthClientID() {
		name := a.OAuth.ClientName
		if name == "" {
			name = a.App + " CLI"
		}
		return oauthClient{ID: id, Name: name, Source: "builtin"}, nil
	}
	if a.clients() == nil {
		return oauthClient{}, oauthErr("invalid_client", "unknown client_id; this server signs in "+a.oauthClientID())
	}
	row, err := a.getOAuthClient(ctx, id)
	if err != nil && err != sql.ErrNoRows {
		return oauthClient{}, err
	}
	if !isClientIDURL(id) {
		if err == nil {
			return row, nil
		}
		if a.clients().AnyLoopback {
			return oauthClient{ID: id, Name: id, Source: "builtin"}, nil
		}
		return oauthClient{}, oauthErr("invalid_client", "unknown client_id; register it at /v1/auth/register first")
	}
	if verr := validClientIDURL(id); verr != nil {
		return oauthClient{}, oauthErr("invalid_client", verr.Error())
	}
	if err == nil && row.ExpiresAt > sqldb.FormatTime(a.now()) {
		return row, nil
	}
	return a.fetchClient(ctx, id)
}

// validClientIDURL applies the client ID metadata document rules: https,
// a path, no credentials, fragment or dot segments, and the default port.
func validClientIDURL(raw string) error {
	u, err := url.Parse(raw)
	bad := errors.New("client_id must be an https URL with a path, no credentials, fragment or dot segments, on port 443")
	if err != nil || len(raw) > maxClientIDURL || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" ||
		u.Path == "" || u.Path == "/" || (u.Port() != "" && u.Port() != "443") {
		return bad
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return bad
		}
	}
	return nil
}

func (a *Accounts) fetchClient(ctx context.Context, id string) (oauthClient, error) {
	fetch := a.clients().FetchDoc
	if fetch == nil {
		fetch = FetchClientDoc
	}
	body, err := fetch(ctx, id)
	if err != nil {
		return oauthClient{}, oauthErr("invalid_client", "could not fetch the client metadata document: "+err.Error())
	}
	var doc struct {
		ClientID     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return oauthClient{}, oauthErr("invalid_client", "the client metadata document is not JSON")
	}
	if doc.ClientID != id {
		return oauthClient{}, oauthErr("invalid_client", "client_id in the document does not match the URL it was fetched from")
	}
	if doc.AuthMethod != "" && doc.AuthMethod != "none" {
		return oauthClient{}, oauthErr("invalid_client", "only public clients (token_endpoint_auth_method none) are supported")
	}
	if err := validRedirects(doc.RedirectURIs); err != nil {
		return oauthClient{}, oauthErr("invalid_client", err.Error())
	}
	now := a.now()
	row := oauthClient{
		ID: id, Name: cleanClientName(doc.ClientName, id), RedirectURIs: doc.RedirectURIs, Source: "cimd",
		CreatedAt: sqldb.FormatTime(now), ExpiresAt: sqldb.FormatTime(now.Add(clientDocTTL)),
	}
	return row, a.upsertOAuthClient(ctx, row)
}

// cleanClientName drops control characters and caps the length, since the
// name is shown on the consent page and the client chose it.
func cleanClientName(name, fallback string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if r := []rune(name); len(r) > 100 {
		name = string(r[:100])
	}
	if name == "" {
		return fallback
	}
	return name
}

// FetchClientDoc downloads a client metadata document. The URL is chosen
// by whoever starts an authorization, so the fetch must not reach our own
// network: the dial itself refuses private, loopback and link-local
// addresses (checked on the resolved IP, so DNS tricks don't help), there
// are no redirects and no proxy, and time and size are capped.
func FetchClientDoc(ctx context.Context, rawURL string) ([]byte, error) {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || BlockedIP(ip) {
				return errors.New("address not allowed")
			}
			return nil
		},
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: nil, DialContext: dialer.DialContext, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, clientDocMaxBytes+1))
	if err != nil {
		return nil, errors.New("read failed")
	}
	if len(body) > clientDocMaxBytes {
		return nil, errors.New("document is larger than 64 KB")
	}
	return body, nil
}

var extraBlocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
}

// BlockedIP reports addresses a client metadata fetch must never reach.
func BlockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, p := range extraBlocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func loopbackHost(h string) bool { return h == "127.0.0.1" || h == "::1" || h == "localhost" }

// ValidClientRedirect is the rule for a registered client's redirect URI:
// an https URL, or an http loopback URL (any port, or none) for a local app.
func ValidClientRedirect(raw string) error {
	u, err := url.Parse(raw)
	bad := errors.New("redirect_uris must be https URLs, or http://127.0.0.1, http://[::1] or http://localhost, with no credentials or fragment")
	if err != nil || len(raw) > 512 || u.User != nil || u.Fragment != "" || u.Hostname() == "" {
		return bad
	}
	if u.Scheme == "https" || (u.Scheme == "http" && loopbackHost(u.Hostname())) {
		return nil
	}
	return bad
}

func validRedirects(uris []string) error {
	if len(uris) == 0 || len(uris) > maxRedirectURIs {
		return fmt.Errorf("redirect_uris needs between 1 and %d entries", maxRedirectURIs)
	}
	for _, u := range uris {
		if err := ValidClientRedirect(u); err != nil {
			return err
		}
	}
	return nil
}

// redirectMatches is exact, except that a loopback redirect ignores the port
// (RFC 8252 section 7.3): a local app picks one at run time.
func redirectMatches(registered []string, got string) bool {
	g, err := url.Parse(got)
	if err != nil {
		return false
	}
	for _, r := range registered {
		if r == got {
			return true
		}
		ru, err := url.Parse(r)
		if err == nil && ru.Scheme == "http" && g.Scheme == "http" && loopbackHost(ru.Hostname()) &&
			ru.Hostname() == g.Hostname() && ru.Path == g.Path && ru.RawQuery == g.RawQuery {
			return true
		}
	}
	return false
}

// ClientRegistration is an RFC 7591 request. Unknown members are ignored.
type ClientRegistration struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
}

// ClientInfo is the RFC 7591 response.
type ClientInfo struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
}

// RegisterOAuthClient is open dynamic client registration. Anyone can
// register, so a client gets nothing but the right to ask: the consent page
// still shows the human what is being requested, and OAuth never grants
// ApproveScope.
func (a *Accounts) RegisterOAuthClient(ctx context.Context, in ClientRegistration) (ClientInfo, error) {
	bad := func(code, msg string) (ClientInfo, error) { return ClientInfo{}, oauthErr(code, msg) }
	if err := validRedirects(in.RedirectURIs); err != nil {
		return bad("invalid_redirect_uri", err.Error())
	}
	if m := in.TokenEndpointAuthMethod; m != "" && m != "none" {
		return bad("invalid_client_metadata", "only public clients (token_endpoint_auth_method none) are supported")
	}
	for _, r := range in.ResponseTypes {
		if r != "code" {
			return bad("invalid_client_metadata", "response_types must be code")
		}
	}
	for _, g := range in.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return bad("invalid_client_metadata", "grant_types may be authorization_code and refresh_token")
		}
	}
	now := a.now()
	_, _ = a.DB.Exec(ctx, `DELETE FROM oauth_clients WHERE source='dcr' AND last_used_at='' AND created_at < ?`, sqldb.FormatTime(now.Add(-dcrUnusedTTL)))
	var n int
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM oauth_clients WHERE source='dcr'`).Scan(&n); err != nil {
		return ClientInfo{}, err
	}
	if n >= maxDCRClients {
		return bad("temporarily_unavailable", "too many registered clients; try again later")
	}
	secret, err := oauthlite.RandomURLToken(16)
	if err != nil {
		return ClientInfo{}, err
	}
	marker := a.clients().IDMarker
	if marker == "" {
		marker = "c_"
	}
	row := oauthClient{
		ID: marker + secret, Name: cleanClientName(in.ClientName, "Unnamed app"), RedirectURIs: in.RedirectURIs,
		Source: "dcr", CreatedAt: sqldb.FormatTime(now),
	}
	if err := a.upsertOAuthClient(ctx, row); err != nil {
		return ClientInfo{}, err
	}
	return ClientInfo{
		ClientID: row.ID, ClientName: row.Name, RedirectURIs: row.RedirectURIs, TokenEndpointAuthMethod: "none",
		GrantTypes: []string{"authorization_code"}, ResponseTypes: []string{"code"}, ClientIDIssuedAt: now.Unix(),
	}, nil
}

const oauthClientCols = `id, name, redirect_uris, source, created_at, last_used_at, expires_at`

// upsertOAuthClient inserts a client, or refreshes the name, redirect URIs
// and expiry of a cimd client whose document was fetched again.
func (a *Accounts) upsertOAuthClient(ctx context.Context, c oauthClient) error {
	_, err := a.DB.Exec(ctx, `INSERT INTO oauth_clients (`+oauthClientCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, redirect_uris=excluded.redirect_uris, expires_at=excluded.expires_at`,
		c.ID, c.Name, strings.Join(c.RedirectURIs, "\n"), c.Source, c.CreatedAt, c.LastUsedAt, c.ExpiresAt)
	return mapWriteErr(err)
}

// getOAuthClient returns sql.ErrNoRows for a client that is not stored.
func (a *Accounts) getOAuthClient(ctx context.Context, id string) (oauthClient, error) {
	var c oauthClient
	var uris string
	err := a.DB.QueryRow(ctx, `SELECT `+oauthClientCols+` FROM oauth_clients WHERE id=?`, id).Scan(
		&c.ID, &c.Name, &uris, &c.Source, &c.CreatedAt, &c.LastUsedAt, &c.ExpiresAt)
	if err != nil {
		return oauthClient{}, err
	}
	if uris != "" {
		c.RedirectURIs = strings.Split(uris, "\n")
	}
	return c, nil
}

// refreshToken is one link in a rotating chain. Hash is the only form of the
// secret that is stored.
type refreshToken struct {
	ID         string
	Hash       string
	FamilyID   string
	ClientID   string
	UserID     string
	Scopes     []string
	Resource   string
	CreatedAt  string
	ExpiresAt  string // idle limit: 30 days from issue
	AbsoluteAt string // the family's hard limit
	UsedAt     string
	RevokedAt  string
}

const refreshCols = `id, token_hash, family_id, client_id, user_id, scopes, resource, created_at, expires_at, absolute_at, used_at, revoked_at`

// getRefreshToken returns sql.ErrNoRows for an unknown token.
func (a *Accounts) getRefreshToken(ctx context.Context, secret string) (refreshToken, error) {
	var r refreshToken
	var scopes string
	err := a.DB.QueryRow(ctx, `SELECT `+refreshCols+` FROM oauth_refresh_tokens WHERE token_hash=?`, session.Hash(secret)).Scan(
		&r.ID, &r.Hash, &r.FamilyID, &r.ClientID, &r.UserID, &scopes, &r.Resource, &r.CreatedAt, &r.ExpiresAt, &r.AbsoluteAt, &r.UsedAt, &r.RevokedAt)
	if scopes != "" {
		r.Scopes = strings.Split(scopes, ",")
	}
	return r, err
}

// revokeTokenFamily kills every access and refresh token issued from one consent.
func (a *Accounts) revokeTokenFamily(ctx context.Context, familyID string) error {
	at := sqldb.FormatTime(a.now())
	if _, err := a.DB.Exec(ctx, `UPDATE api_tokens SET revoked_at=? WHERE family_id=? AND revoked_at IS NULL`, at, familyID); err != nil {
		return err
	}
	_, err := a.DB.Exec(ctx, `UPDATE oauth_refresh_tokens SET revoked_at=? WHERE family_id=? AND revoked_at=''`, at, familyID)
	return err
}

type clientGrant struct {
	userID, approverTokenID, resource, grantID, flow string
	client                                           oauthClient
	scopes                                           []string
	family                                           string
	absoluteAt                                       time.Time
}

// issueClientTokens mints an access token and the next refresh token in a family.
func (a *Accounts) issueClientTokens(ctx context.Context, g clientGrant) (oauthlite.Token, error) {
	secret, prefix, hash, err := session.Generate(a.TokenMarker)
	if err != nil {
		return oauthlite.Token{}, err
	}
	raw, err := oauthlite.RandomURLToken(32)
	if err != nil {
		return oauthlite.Token{}, err
	}
	refresh := a.refreshMarker() + raw
	now := a.now()
	expires := now.Add(ClientAccessTTL)
	tok := session.Token{
		ID: a.ID(), UserID: g.userID, Name: g.client.Name, Hash: hash, Prefix: prefix, Scopes: g.scopes, CreatedAt: now,
		ClientID: g.client.ID, Resource: g.resource, ExpiresAt: &expires, FamilyID: g.family,
	}
	idle := now.Add(RefreshIdleTTL)
	if idle.After(g.absoluteAt) {
		idle = g.absoluteAt
	}
	old := sqldb.FormatTime(now.Add(-time.Hour))
	_, _ = a.DB.Exec(ctx, `DELETE FROM oauth_refresh_tokens WHERE absolute_at < ? OR (revoked_at <> '' AND revoked_at < ?)`, old, old)
	act := actor.Actor{UserID: g.userID, TokenID: tok.ID, Name: tok.Name, Scopes: g.scopes}
	err = a.DB.WithTx(ctx, func(ctx context.Context) error {
		if err := a.Store().CreateToken(ctx, tok); err != nil {
			return err
		}
		_, err := a.DB.Exec(ctx, `INSERT INTO oauth_refresh_tokens (`+refreshCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
			a.ID(), session.Hash(refresh), g.family, g.client.ID, g.userID, strings.Join(g.scopes, ","), g.resource,
			sqldb.FormatTime(now), sqldb.FormatTime(idle), sqldb.FormatTime(g.absoluteAt))
		if err != nil {
			return mapWriteErr(err)
		}
		if g.flow == "refresh" { // not refresh-grace: see oauthRefresh
			// A refresh leaves a single live access token per connection.
			if _, err := a.DB.Exec(ctx, `UPDATE api_tokens SET revoked_at=? WHERE family_id=? AND id<>? AND revoked_at IS NULL`, sqldb.FormatTime(now), g.family, tok.ID); err != nil {
				return err
			}
		}
		return a.Audit(ctx, act, "token.create", "token", tok.ID, g.client.Name+" token ("+g.flow+")",
			map[string]any{"prefix": prefix, "scopes": g.scopes, "client_id": g.client.ID, "family_id": g.family, "grant_id": g.grantID, "approver_token_id": g.approverTokenID, "resource": g.resource})
	})
	if err != nil {
		return oauthlite.Token{}, err
	}
	return oauthlite.Token{
		AccessToken: secret, TokenType: "Bearer", Scope: strings.Join(g.scopes, " "),
		ExpiresIn: int(ClientAccessTTL.Seconds()), RefreshToken: refresh,
	}, nil
}

// oauthRefresh is grant_type=refresh_token. scope may only narrow what was
// granted, and resource must be the one the consent named.
func (a *Accounts) oauthRefresh(ctx context.Context, secret, clientID, scope, resource string) (oauthlite.Token, error) {
	if secret == "" {
		return oauthlite.Token{}, oauthErr("invalid_request", "refresh_token is required")
	}
	clientID, ok := a.normalizeClientID(clientID)
	if !ok {
		return oauthlite.Token{}, oauthErr("invalid_request", "client_id is too long")
	}
	dead := oauthErr("invalid_grant", "the refresh token is invalid, expired or already used; authorize again")
	rt, err := a.getRefreshToken(ctx, secret)
	if err == sql.ErrNoRows {
		return oauthlite.Token{}, dead
	}
	if err != nil {
		return oauthlite.Token{}, err
	}
	now := a.now()
	nowS := sqldb.FormatTime(now)
	switch {
	case rt.ClientID != clientID, rt.RevokedAt != "":
		return oauthlite.Token{}, dead
	case rt.UsedAt != "" && !withinGrace(rt, now):
		return oauthlite.Token{}, a.refreshReuse(ctx, rt, dead)
	case nowS >= rt.ExpiresAt, nowS >= rt.AbsoluteAt:
		return oauthlite.Token{}, dead
	case resource != "" && resource != rt.Resource:
		return oauthlite.Token{}, oauthErr("invalid_target", "resource does not match the one that was authorized")
	}
	scopes := rt.Scopes
	if scope != "" {
		want, err := a.oauthScopes(scope)
		if err != nil {
			return oauthlite.Token{}, err
		}
		for _, w := range want {
			if !slices.Contains(rt.Scopes, w) {
				return oauthlite.Token{}, oauthErr("invalid_scope", "scope is wider than the one that was granted")
			}
		}
		scopes = want
	}
	client, err := a.getOAuthClient(ctx, rt.ClientID)
	if err != nil {
		return oauthlite.Token{}, dead
	}
	flow := "refresh"
	used := false
	if rt.UsedAt == "" {
		// Exactly one caller across all replicas marks it used; any other is a replay.
		res, err := a.DB.Exec(ctx, `UPDATE oauth_refresh_tokens SET used_at=? WHERE id=? AND used_at='' AND revoked_at=''`, nowS, rt.ID)
		if err != nil {
			return oauthlite.Token{}, err
		}
		n, _ := res.RowsAffected()
		used = n == 1
	}
	if !used {
		// Used already: by an earlier request inside the grace window, or by
		// one that won a race just now. Re-read to see which, and whether it
		// was revoked meanwhile.
		cur, err := a.getRefreshToken(ctx, secret)
		if err != nil || cur.RevokedAt != "" {
			return oauthlite.Token{}, dead
		}
		if !withinGrace(cur, now) {
			return oauthlite.Token{}, a.refreshReuse(ctx, cur, dead)
		}
		// A parallel request is holding the family's live access token, so
		// this one must not revoke it.
		flow = "refresh-grace"
	}
	abs, err := sqldb.ParseTime(rt.AbsoluteAt)
	if err != nil {
		return oauthlite.Token{}, err
	}
	return a.issueClientTokens(ctx, clientGrant{
		userID: rt.UserID, resource: rt.Resource, client: client, scopes: scopes, family: rt.FamilyID, absoluteAt: abs, flow: flow,
	})
}

// refreshReuse revokes the family of a replayed refresh token and returns
// the error to answer with. Either the client or a thief holds the other
// copy, and we can't tell which.
func (a *Accounts) refreshReuse(ctx context.Context, rt refreshToken, dead error) error {
	err := a.DB.WithTx(ctx, func(ctx context.Context) error {
		if err := a.revokeTokenFamily(ctx, rt.FamilyID); err != nil {
			return err
		}
		return a.Audit(ctx, actor.Actor{UserID: rt.UserID, Name: "oauth"}, "oauth.reuse", "token_family", rt.FamilyID, "refresh token reused; revoked the connection", map[string]any{"client_id": rt.ClientID})
	})
	if err != nil {
		return err
	}
	return dead
}

func withinGrace(rt refreshToken, now time.Time) bool {
	used, err := sqldb.ParseTime(rt.UsedAt)
	return err == nil && now.Sub(used) <= RefreshGrace
}

// revokeRefresh handles RFC 7009 for a refresh token: revoke its family.
func (a *Accounts) revokeRefresh(ctx context.Context, secret string) (bool, error) {
	if a.clients() == nil || !strings.HasPrefix(secret, a.refreshMarker()) {
		return false, nil
	}
	rt, err := a.getRefreshToken(ctx, secret)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return true, a.DB.WithTx(ctx, func(ctx context.Context) error {
		if err := a.revokeTokenFamily(ctx, rt.FamilyID); err != nil {
			return err
		}
		return a.Audit(ctx, actor.Actor{UserID: rt.UserID, Name: "oauth"}, "oauth.revoke", "token_family", rt.FamilyID, "revoked a connection (refresh token)", map[string]any{"client_id": rt.ClientID})
	})
}

func (a *Accounts) oauthRegister(w http.ResponseWriter, r *http.Request) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeOAuthErr(w, oauthErr("invalid_client_metadata", "send an application/json body"))
		return
	}
	var in ClientRegistration
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		writeOAuthErr(w, oauthErr("invalid_client_metadata", "the body is not valid JSON"))
		return
	}
	out, err := a.RegisterOAuthClient(r.Context(), in)
	if err != nil {
		a.logOAuth("register", err)
		writeOAuthErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusCreated, out)
}

// Discovery documents for OAuth clients that find the server from a URL
// alone. Both list only what is served.
func writeMetadata(w http.ResponseWriter, doc map[string]any) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	// Browser-based clients fetch these cross-origin.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	httpx.WriteJSON(w, 200, doc)
}

func (a *Accounts) oauthServerMetadata(w http.ResponseWriter, r *http.Request) {
	iss := a.oauthIssuer(r)
	scopes, _ := a.oauthScopes("")
	writeMetadata(w, map[string]any{
		"issuer":                                         iss,
		"authorization_endpoint":                         iss + "/authorize",
		"token_endpoint":                                 iss + "/v1/auth/token",
		"revocation_endpoint":                            iss + "/v1/auth/revoke",
		"registration_endpoint":                          iss + "/v1/auth/register",
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
		"device_authorization_endpoint":                  iss + "/v1/auth/device_authorization",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token", oauthlite.DeviceCodeGrantType},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"scopes_supported":                               append(scopes, OfflineAccess),
	})
}

func (a *Accounts) oauthResourceMetadata(w http.ResponseWriter, r *http.Request) {
	iss := a.oauthIssuer(r)
	resource := iss
	if p := strings.Trim(r.PathValue("path"), "/"); p != "" {
		resource += "/" + p
	}
	scopes, _ := a.oauthScopes("")
	writeMetadata(w, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{iss},
		"scopes_supported":         scopes,
		"bearer_methods_supported": []string{"header"},
	})
}

func (a *Accounts) oauthClientRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/register", a.limited("register", 20, true, a.oauthRegister))
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", a.oauthServerMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", a.oauthResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/{path...}", a.oauthResourceMetadata)
}
