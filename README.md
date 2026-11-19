# appkit

The shared platform under [pursuit](https://github.com/adrianmross/pursuit): sign-in and sessions, API tokens, the HTTP and CLI plumbing, the store core, and the dashboard's reusable UI.

- Go module `github.com/adrianmross/appkit`
- npm package `@adrianmross/appkit-ui` in `ui/`

It builds on three small libraries: [ratchet](https://github.com/adrianmross/ratchet) (migrations), [tokenkeep](https://github.com/adrianmross/tokenkeep) (keyring) and [oauthlite](https://github.com/adrianmross/oauthlite) (OAuth server).

## Go packages

| Package | What it is |
| --- | --- |
| `actor` | who authorized a request, carried in the context |
| `apperr` | the structured error every transport returns |
| `paging` | the list envelope, cursors and the `fields` check |
| `session` | API token checks, dashboard sign-ins with idle and absolute lifetimes, the session cookie and the request gate |
| `httpx` | JSON request and response helpers, request tracing, CORS, and writes with dry run and `Idempotency-Key` replay |
| `humax` | A Huma v2 API on the standard mux that keeps the error envelope and strict request checks of `httpx`, for an app that wants `/openapi.json` generated from typed operations |
| `sqldb` | Postgres or SQLite behind one query style: `?` placeholders, a transaction in the context, one database-wide lock |
| `account` | users, API tokens, dashboard sign-in, the audit log, and the HTTP routes for them (`/v1/me`, `/v1/tokens`, `/v1/audit`, `/v1/events`, `/v1/auth/session`). Optional, each off until its field on `Accounts` is set: `OAuth` (the app's CLI signs in by loopback code with PKCE or a device code; `OAuth.Clients` also lets in other apps, by client metadata URL or dynamic registration, with refresh tokens and the discovery documents), `Passkeys` (WebAuthn sign-in) and `OIDC` (sign-in through an identity provider) |
| `events` | the activity feed: events about a subject, with JSON data the app owns |
| `cli` | the command line framework: commands as data, `schema`, `--help --json`, field projection and exit codes; `AuthCommand` adds `auth login`, `logout` and `status`, with the token in the OS keyring or a 0600 file |
| `migrations` | the schema of the platform tables, as a ratchet set named `appkit` |

An app that turns on `OAuth` or `Passkeys` lists `approve` in `Accounts.Scopes`. It is the scope of a human signed in as themselves: only its holder approves a CLI login or manages passkeys, OAuth never grants it, and a passkey or OIDC sign-in holds it. `Accounts.MintOwnerToken` gives a database that predates the scope a token that has it.

Run the tests with `go test ./...`. Set `APPKIT_TEST_DATABASE_URL` to a Postgres URL to run the schema test there too; it works in its own schema, `appkit_migrations_test`.
