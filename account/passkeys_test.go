package account

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/adrianmross/appkit/actor"
	"github.com/adrianmross/appkit/sqldb"
)

// softKey is a hand-rolled platform authenticator: one P-256 passkey,
// "none" attestation, user presence and verification always set.
type softKey struct {
	t       *testing.T
	key     *ecdsa.PrivateKey
	id      []byte
	handle  []byte
	counter uint32
}

func newSoftKey(t *testing.T) *softKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softKey{t: t, key: key, id: id}
}

func (k *softKey) authData(flags byte, attested bool) []byte {
	rp := sha256.Sum256([]byte("localhost"))
	out := append(rp[:], flags)
	out = binary.BigEndian.AppendUint32(out, k.counter)
	if attested {
		pub, err := k.key.PublicKey.Bytes() // 0x04 || X || Y
		if err != nil {
			k.t.Fatal(err)
		}
		cose, err := webauthncbor.Marshal(webauthncose.EC2PublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{KeyType: 2, Algorithm: -7},
			Curve:         1, XCoord: pub[1:33], YCoord: pub[33:],
		})
		if err != nil {
			k.t.Fatal(err)
		}
		out = append(out, make([]byte, 16)...) // AAGUID
		out = binary.BigEndian.AppendUint16(out, uint16(len(k.id)))
		out = append(out, k.id...)
		out = append(out, cose...)
	}
	return out
}

func (k *softKey) clientData(kind string, options []byte) []byte {
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &opts); err != nil || opts.PublicKey.Challenge == "" {
		k.t.Fatalf("options %s: %v", options, err)
	}
	if opts.PublicKey.User.ID != "" {
		h, err := b64.DecodeString(opts.PublicKey.User.ID)
		if err != nil {
			k.t.Fatal(err)
		}
		k.handle = h
	}
	b, _ := json.Marshal(map[string]any{"type": kind, "challenge": opts.PublicKey.Challenge, "origin": "http://localhost", "crossOrigin": false})
	return b
}

// create answers navigator.credentials.create for the given begin options.
func (k *softKey) create(options []byte) json.RawMessage {
	cdj := k.clientData("webauthn.create", options)
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(0x45, true)})
	if err != nil {
		k.t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64.EncodeToString(cdj), "attestationObject": b64.EncodeToString(att), "transports": []string{"internal"}},
		"clientExtensionResults": map[string]any{}, "authenticatorAttachment": "platform",
	})
	return b
}

// get answers navigator.credentials.get, bumping the signature counter.
func (k *softKey) get(options []byte) json.RawMessage {
	k.counter++
	cdj := k.clientData("webauthn.get", options)
	ad := k.authData(0x05, false)
	cdh := sha256.Sum256(cdj)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.key, digest[:])
	if err != nil {
		k.t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON": b64.EncodeToString(cdj), "authenticatorData": b64.EncodeToString(ad),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(k.handle),
		},
		"clientExtensionResults": map[string]any{},
	})
	return b
}

// The owner adds a passkey, signs in with it, and that sign-in holds every
// scope; a stale one must sign in again before it changes passkeys.
func TestPasskeys(t *testing.T) {
	each(t, func(t *testing.T, db *sqldb.DB) {
		ctx := context.Background()
		now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
		a := &Accounts{DB: db, App: "demo", EnvPrefix: "DEMO_", TokenMarker: "dmo_", Scopes: []string{"read", "write", ApproveScope},
			Passkeys: &Passkeys{RPID: "localhost", Origins: []string{"http://localhost"}}, Now: func() time.Time { return now }}
		mux := http.NewServeMux()
		a.Routes(mux)
		srv := httptest.NewServer(a.Handler(mux))
		defer srv.Close()
		owner, err := a.Bootstrap(ctx, "", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		// auth is "Bearer ..." or a Cookie header value.
		call := func(method, path, bearer, cookie string, body []byte) (int, []byte, *http.Response) {
			t.Helper()
			req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			if bearer != "" {
				req.Header.Set("Authorization", "Bearer "+bearer)
			}
			if cookie != "" {
				req.Header.Set("Cookie", cookie)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			b, _ := io.ReadAll(res.Body)
			return res.StatusCode, b, res
		}
		empty := []byte("{}")

		if code, b, _ := call("GET", "/v1/auth/providers", "", "", nil); code != 200 || !bytes.Contains(b, []byte(`"passkeys":true`)) {
			t.Fatalf("providers: %d %s", code, b)
		}
		ownerActor, _ := a.Authenticate(ctx, owner)
		agent, err := a.CreateToken(actor.WithActor(ctx, ownerActor), TokenInput{Name: "agent", Scopes: []string{"write"}})
		if err != nil {
			t.Fatal(err)
		}
		if code, b, _ := call("POST", "/v1/passkeys/register/begin", agent.Secret, "", empty); code != 403 {
			t.Fatalf("agent began a registration: %d %s", code, b)
		}

		key := newSoftKey(t)
		code, opts, _ := call("POST", "/v1/passkeys/register/begin", owner, "", empty)
		if code != 200 {
			t.Fatalf("register begin: %d %s", code, opts)
		}
		reg, _ := json.Marshal(map[string]any{"name": "laptop", "credential": key.create(opts)})
		if code, b, _ := call("POST", "/v1/passkeys/register/finish", owner, "", reg); code != 201 || !bytes.Contains(b, []byte(`"name":"laptop"`)) {
			t.Fatalf("register finish: %d %s", code, b)
		}
		if code, b, _ := call("POST", "/v1/passkeys/register/finish", owner, "", reg); code != 401 {
			t.Fatalf("registration replayed: %d %s", code, b)
		}

		// held is the session cookie the browser already has, if any.
		signIn := func(held string) string {
			t.Helper()
			code, opts, _ := call("POST", "/v1/auth/passkey/login/begin", "", "", empty)
			if code != 200 {
				t.Fatalf("login begin: %d %s", code, opts)
			}
			assertion := key.get(opts)
			code, b, res := call("POST", "/v1/auth/passkey/login/finish", "", held, assertion)
			if code != 200 || len(res.Cookies()) != 1 || !bytes.Contains(b, []byte(`"name":"login"`)) || !bytes.Contains(b, []byte(`"approve"`)) {
				t.Fatalf("login finish: %d %s", code, b)
			}
			if code, b, _ := call("POST", "/v1/auth/passkey/login/finish", "", "", assertion); code != 401 {
				t.Fatalf("assertion replayed: %d %s", code, b)
			}
			return res.Cookies()[0].Name + "=" + res.Cookies()[0].Value
		}
		first := signIn("")
		if code, b, _ := call("GET", "/v1/passkeys", "", first, nil); code != 200 || !bytes.Contains(b, []byte(`"total":1`)) || bytes.Contains(b, []byte(`"last_used_at":null`)) {
			t.Fatalf("list: %d %s", code, b)
		}
		if code, b, _ := call("GET", "/v1/passkeys?limit=1", "", first, nil); code != 200 || !bytes.Contains(b, []byte(`"limit":1`)) {
			t.Fatalf("list, paged: %d %s", code, b)
		}

		// A stranger's key is refused without saying why.
		code, opts, _ = call("POST", "/v1/auth/passkey/login/begin", "", "", empty)
		other := newSoftKey(t)
		other.handle = []byte("nobody")
		if code, b, _ := call("POST", "/v1/auth/passkey/login/finish", "", "", other.get(opts)); code != 401 {
			t.Fatalf("unknown passkey: %d %s", code, b)
		}

		// Past the step-up window the sign-in still reads, but must sign in again to remove a passkey.
		now = now.Add(StepUpWindow + time.Minute)
		id := b64.EncodeToString(key.id)
		if code, b, _ := call("DELETE", "/v1/passkeys/"+id, "", first, nil); code != 403 || !bytes.Contains(b, []byte("step_up_required")) {
			t.Fatalf("stale sign-in removed a passkey: %d %s", code, b)
		}
		second := signIn(first)
		if code, _, _ := call("GET", "/v1/me", "", first, nil); code != 401 {
			t.Fatalf("the replaced sign-in still works: %d", code)
		}
		third := func() string { // another browser
			code, opts, _ := call("POST", "/v1/auth/passkey/login/begin", "", "", empty)
			_, _, res := call("POST", "/v1/auth/passkey/login/finish", "", "", key.get(opts))
			if code != 200 || len(res.Cookies()) != 1 {
				t.Fatal("third sign-in failed")
			}
			return res.Cookies()[0].Name + "=" + res.Cookies()[0].Value
		}()
		if code, b, _ := call("DELETE", "/v1/passkeys/"+id, "", second, nil); code != 200 || !bytes.Contains(b, []byte(`"deleted":true`)) {
			t.Fatalf("delete: %d %s", code, b)
		}
		if code, _, _ := call("GET", "/v1/me", "", third, nil); code != 401 {
			t.Fatalf("removing a passkey left the other sign-in alive: %d", code)
		}
		if code, _, _ := call("GET", "/v1/me", "", second, nil); code != 200 {
			t.Fatalf("removing a passkey signed out the session that did it: %d", code)
		}
	})
}
