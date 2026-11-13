package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adrianmross/appkit/apperr"
)

type memStore map[string]Token // by hash

func (m memStore) GetTokenByHash(_ context.Context, hash string) (Token, error) {
	t, ok := m[hash]
	if !ok {
		return Token{}, apperr.NotFound("token")
	}
	return t, nil
}
func (m memStore) CreateToken(_ context.Context, t Token) error { m[t.Hash] = t; return nil }
func (m memStore) TouchToken(_ context.Context, id string, at, _ time.Time) error {
	for h, t := range m {
		if t.ID == id {
			t.LastUsedAt = &at
			m[h] = t
		}
	}
	return nil
}
func (m memStore) RevokeExpiredLogins(context.Context, string, time.Time, time.Time, time.Time) error {
	return nil
}

// A sign-in lapses when idle and at its absolute limit, an ordinary token
// with the same age does not, and step-up applies only to a stale sign-in
// of a user who has a passkey.
func TestSessionLifetimes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	errUnauth := errors.New("unauthorized")
	store := memStore{}
	m := &Manager{
		Store: store, Now: func() time.Time { return now }, NewID: func() string { return "id" },
		LoginName: "login", DemoName: "demo", IdleTTL: 30 * 24 * time.Hour, MaxTTL: 90 * 24 * time.Hour,
		StepUpWindow: 15 * time.Minute, Scopes: []string{"read", "write"}, TokenMarker: "tk_",
		HasStepUp:    func(context.Context, string) (bool, error) { return true, nil },
		Unauthorized: func() error { return errUnauth },
	}
	secret, _, err := m.Mint(WithOrigin(ctx, Origin{UserAgent: "Mozilla Chrome/1 Mac OS X"}), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "tk_") {
		t.Fatalf("secret %q lacks the marker", secret)
	}
	store[Hash("plain")] = Token{ID: "t2", UserID: "u1", Name: "ci", Hash: Hash("plain"), CreatedAt: now}

	a, err := m.Authenticate(ctx, secret)
	if err != nil || a.UserID != "u1" || !a.AuthAt.Equal(now) {
		t.Fatalf("fresh sign-in: %+v %v", a, err)
	}
	if got := store[Hash(secret)].Client; got != "Chrome on macOS" {
		t.Fatalf("client = %q", got)
	}
	if err := m.RequireFresh(ctx, a); err != nil {
		t.Fatalf("fresh sign-in asked to step up: %v", err)
	}

	now = now.Add(16 * time.Minute)
	if err := m.RequireFresh(ctx, a); !apperr.IsCode(err, "step_up_required") {
		t.Fatalf("stale sign-in: %v", err)
	}
	m.HasStepUp = func(context.Context, string) (bool, error) { return false, nil }
	if err := m.RequireFresh(ctx, a); err != nil {
		t.Fatalf("no passkey, still asked: %v", err)
	}

	// Used 16 minutes in, then idle for 31 days.
	if _, err := m.Authenticate(ctx, secret); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * 24 * time.Hour)
	if _, err := m.Authenticate(ctx, secret); !errors.Is(err, errUnauth) {
		t.Fatalf("idle sign-in: %v", err)
	}
	if a, err := m.Authenticate(ctx, "plain"); err != nil || !a.AuthAt.IsZero() {
		t.Fatalf("ordinary token: %+v %v", a, err)
	}
	if _, err := m.Authenticate(ctx, "nope"); !errors.Is(err, errUnauth) {
		t.Fatalf("unknown token: %v", err)
	}
}
