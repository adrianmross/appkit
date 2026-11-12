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
| `sqldb` | Postgres or SQLite behind one query style: `?` placeholders, a transaction in the context, one database-wide lock |
| `migrations` | the schema of the platform tables, as a ratchet set named `appkit` |

Run the tests with `go test ./...`. Set `APPKIT_TEST_DATABASE_URL` to a Postgres URL to run the schema test there too; it works in its own schema, `appkit_migrations_test`.
