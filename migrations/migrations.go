// Package migrations is the schema of appkit's platform tables: users, API
// tokens, the audit log, idempotency records, rate-limit counters, OAuth
// grants, clients and refresh tokens, and passkeys. An app applies it before
// its own migrations.
package migrations

import (
	"context"
	"database/sql"

	"github.com/adrianmross/ratchet"
)

// Set is the name the versions are recorded under: "appkit/001", and so on.
const Set = "appkit"

// Up applies every pending platform migration in one transaction. dialect is
// "postgres" or "sqlite". lockKey is the Postgres advisory lock to take; 0
// uses ratchet's default. Pass the app's own key to serialize with its
// migrations and bootstrap.
func Up(ctx context.Context, db *sql.DB, dialect string, lockKey int64) error {
	return ratchet.Runner{Dialect: dialect, Set: Set, LockKey: lockKey}.Up(ctx, db, All)
}

// All is the platform set. Timestamps are RFC 3339 text. Once a migration
// has shipped, never edit it: add another.
var All = []ratchet.Migration{{
	Version: "001",
	Stmts: []string{
		// webauthn_user_handle is random, not the email or the id, so an
		// authenticator holds nothing that names the person.
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL,
			avatar TEXT NOT NULL DEFAULT '',
			webauthn_user_handle TEXT NOT NULL DEFAULT '',
			announcements_seen TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS users_webauthn_handle_uidx ON users(webauthn_user_handle) WHERE webauthn_user_handle <> ''`,

		// Only the hash of a token is stored. client and ip say where a
		// dashboard sign-in came from. client_id, resource, expires_at and
		// family_id are set on tokens issued to a registered OAuth client;
		// the family is the consent they came from, so revoking one revokes
		// the grant.
		`CREATE TABLE IF NOT EXISTS api_tokens (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id),
			name TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			token_prefix TEXT NOT NULL,
			scopes TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_used_at TEXT,
			revoked_at TEXT,
			client TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			client_id TEXT NOT NULL DEFAULT '',
			resource TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL DEFAULT '',
			family_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS api_tokens_family_idx ON api_tokens(family_id) WHERE family_id <> ''`,

		`CREATE TABLE IF NOT EXISTS idempotency_keys (
			token_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			idem_key TEXT NOT NULL,
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			status_code BIGINT NOT NULL,
			response_body TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (token_id, idem_key)
		)`,

		`CREATE TABLE IF NOT EXISTS audit_log (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			token_id TEXT NOT NULL DEFAULT '',
			actor_name TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS audit_user_created_idx ON audit_log(user_id, created_at, id)`,

		// Fixed-window request counters shared by every instance, so a rate
		// limit holds on serverless where each instance has its own memory.
		// The window index is part of the key; expires_at (unix seconds) lets
		// old windows be deleted.
		`CREATE TABLE IF NOT EXISTS rate_limits (
			key TEXT PRIMARY KEY,
			hits BIGINT NOT NULL,
			expires_at BIGINT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS rate_limits_expires_idx ON rate_limits(expires_at)`,

		// OAuth grants. kind is code (loopback + PKCE) or device. Codes are
		// stored as SHA-256 hashes only.
		`CREATE TABLE IF NOT EXISTS oauth_grants (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			code_hash TEXT NOT NULL UNIQUE,
			user_code_hash TEXT UNIQUE,
			client_id TEXT NOT NULL,
			scopes TEXT NOT NULL,
			redirect_uri TEXT NOT NULL DEFAULT '',
			code_challenge TEXT NOT NULL DEFAULT '',
			user_id TEXT REFERENCES users(id),
			status TEXT NOT NULL,
			interval_seconds BIGINT NOT NULL DEFAULT 0,
			last_polled_at TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			approver_token_id TEXT NOT NULL DEFAULT '',
			requested_from TEXT NOT NULL DEFAULT '',
			resource TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS oauth_grants_expires_idx ON oauth_grants(expires_at)`,

		// OAuth clients that are not the app's own CLI. source is cimd (a
		// client metadata document at an https client_id, refetched after
		// expires_at) or dcr (dynamically registered). redirect_uris is
		// newline separated.
		`CREATE TABLE IF NOT EXISTS oauth_clients (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			redirect_uris TEXT NOT NULL,
			source TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_used_at TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL DEFAULT ''
		)`,

		// Refresh tokens rotate: each is used once and answered with the next.
		// A second use of one revokes the whole family. Only hashes are stored.
		`CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
			id TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL UNIQUE,
			family_id TEXT NOT NULL,
			client_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			scopes TEXT NOT NULL,
			resource TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			absolute_at TEXT NOT NULL,
			used_at TEXT NOT NULL DEFAULT '',
			revoked_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS oauth_refresh_family_idx ON oauth_refresh_tokens(family_id)`,

		// Passkeys. id is the credential ID (base64url). credential is the
		// whole go-webauthn Credential record as JSON: public key, sign
		// count, flags, AAGUID, transports, attestation.
		`CREATE TABLE IF NOT EXISTS webauthn_credentials (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			credential TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_used_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS webauthn_credentials_user_idx ON webauthn_credentials(user_id, created_at)`,

		// One row per begin, keyed by the challenge. Single use (used_at)
		// and short-lived (expires_at), like oauth_grants.
		`CREATE TABLE IF NOT EXISTS webauthn_challenges (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			session TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			used_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS webauthn_challenges_expires_idx ON webauthn_challenges(expires_at)`,
	},
}}
