# Engram

**English** | [中文](README.zh.md)

A web-first, multi-user, self-hosted spaced-repetition (SRS) service. Cards can be written by
hand in the browser, or pushed in by an external system or agent through a per-user API key or
the built-in MCP server. Scheduling uses FSRS v6. The service itself does not parse any external
note system.

> The project name is **Engram** and the module path is `example.com/engram`; the licence is
> still a placeholder. See `AGENTS.md` backlog B-2 and `DESIGN.md` §13.

## What it is not

- **Not Anki-compatible.** It does not read or write `.apkg` / `.colpkg` packages, and it has no
  importer for any note system.
- **Not an offline app.** Reviewing requires a network connection. The PWA caches the static
  shell only; no answer data is stored on the device.
- **Not a collaborative editor.** Decks can be shared (read-only, read-write, revocable), but two
  people never edit the same deck content at the same time.
- **Not a hosted service.** No third-party dependency, no telemetry, no public content market.

## Features

- Multi-user accounts with per-user progress. A deck's content is shared; each user's scheduling
  state stays private.
- Deck sharing with three roles — owner, editor (read-write), reader (read-only) — plus share
  links and clone (fork). Revocation takes effect on the next request.
- Ten built-in card types, managed by a registry: `basic`, `basic_both`, `cloze`, `list`
  (self-graded); `typed`, `numeric`, `choice_single`, `choice_multi`, `true_false`
  (machine-graded); and `short_answer` (self-graded for now). Adding a type means adding one file
  and registering it; core code does not change.
- FSRS v6 scheduling, with a configurable desired retention (default 0.90), learning steps,
  maximum interval, and fuzz.
- Markdown plus TeX rendering (MathJax 3, self-hosted) with an HTML allowlist.
- Interface in Chinese and English, driven entirely by translation catalogs.
- PWA shell: add to home screen, standalone window, static assets cached only.
- Deck packages (`.fdeck`): a self-contained export/import format for backup, migration, and
  offline hand-off.
- Parameter optimisation triggered from the web UI and executed in a subprocess.
- REST API (`/api/v1`) and a built-in MCP server (HTTP only) for external agents.
- Admin panel in the browser: users, registration policy, OIDC, upload limits, audit log, jobs,
  health.

## Quick start (SQLite, development)

Requires Go 1.26 or newer. SQLite is the development mode; no external database is needed.

```bash
# 1. Generate the templ code and the Tailwind CSS bundle. Both are gitignored,
#    so a fresh checkout will not build without this step.
go generate ./...

# 2. Create the data directory that holds the SQLite file and the media files.
#    It is gitignored, and the service does not create it for you.
mkdir -p data

# 3. Configure the environment. ENCRYPTION_KEY must be base64 of exactly 32 bytes;
#    any other value makes the server exit at startup.
export DB_DRIVER=sqlite
export DB_DSN=data/engram.db
export AUTO_MIGRATE=1
export SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}"
export ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}"

# 4. Start the server (the default subcommand is serve).
go run ./cmd/engram serve
```

Then open `http://localhost:8080/`. On a fresh instance the first visit goes to `/setup` to
create the first admin account. `HTTP_ADDR` defaults to `127.0.0.1:8080` and `BASE_URL` to
`http://localhost:8080`.

Check that it is up, and print the version:

```bash
curl -fsS http://localhost:8080/healthz
# {"status":"ok","database":"ok","schema_version":0}

go run ./cmd/engram version
# dev
```

If `ENCRYPTION_KEY` is not base64 of 32 bytes, startup fails loudly, for example:

```
level=ERROR msg=fatal error="invalid secret master key: ENCRYPTION_KEY must be base64 of 32 bytes (openssl rand -base64 32)"
```

## Deploying with PostgreSQL

PostgreSQL is the default deployment database. Either build the container image from
`Containerfile`, or run the binary directly with the same environment.

### Environment variables

Every variable the service reads at startup. This table mirrors `.env.example`.

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `HTTP_ADDR` | no | `127.0.0.1:8080` | Listen address. |
| `BASE_URL` | no | `http://localhost:8080` | Public URL. Its scheme sets the session cookie's `Secure` flag, so production must use an `https://` URL. |
| `DB_DRIVER` | yes | — | `postgres` or `sqlite`. |
| `DB_DSN` | yes | — | Connection string (PostgreSQL) or file path (SQLite). |
| `SESSION_SECRET` | yes | — | Session-signing secret; generate with `openssl rand -base64 32`. |
| `ENCRYPTION_KEY` | yes | — | Master key for encrypted settings; must be base64 of exactly 32 bytes (`openssl rand -base64 32`). Anything else fails startup. |
| `AUTO_MIGRATE` | no | `0` | Run migrations at startup (`1` / `true`). |
| `BOOTSTRAP_ADMIN_EMAIL` | no | — | Pre-fills the first-admin setup form. |
| `MEDIA_DIR` | no | `data/media` | Local media directory. |
| `MEDIA_MAX_BYTES` | no | setting `media_max_bytes`, else 10 MiB | Per-file upload limit override. |

Example with the container image (`localhost` in `DB_DSN` is a placeholder — point it at your
database host):

```bash
docker build -t engram:local .

export SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}"
export ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}"

docker run -d --name engram \
  -p 8080:8080 \
  -e HTTP_ADDR=0.0.0.0:8080 \
  -e BASE_URL=https://engram.example.com/ \
  -e DB_DRIVER=postgres \
  -e DB_DSN="postgres://engram:CHANGE_ME@localhost:5432/engram?sslmode=disable" \
  -e SESSION_SECRET="${SESSION_SECRET}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY}" \
  -e AUTO_MIGRATE=0 \
  -v engram-media:/data/media \
  engram:local serve
```

### Migrations at startup

`AUTO_MIGRATE=1` runs AutoMigrate plus the registered destructive migrations when the process
starts. It is convenient for a single node and is what the development quick start uses.

For production, leave `AUTO_MIGRATE=0` and run migrations explicitly, before starting or rolling
out the new version:

```bash
engram schema sync
```

This applies the same migrations and logs the resulting schema version, so a failed migration is
visible as a failed command instead of a half-started server. Inside a container, run it as a
one-off with the same environment (for example `docker run --rm ... engram:local schema sync`).

## Backup and restore

There are two databases and two procedures. In both cases also back up the media directory
(`MEDIA_DIR`, default `data/media`): it holds the uploaded bytes, while the database stores only
their paths and hashes.

### PostgreSQL

Backup (custom format, compressed):

```bash
pg_dump -Fc -f engram.dump "$DB_DSN"
```

Restore into a fresh or existing database:

```bash
pg_restore --clean --if-exists -d "$DB_DSN" engram.dump
```

Stop the service during the restore. `DB_DSN` is the same connection string the service uses.

### SQLite

Use SQLite's own online backup, which is consistent even while the service is running:

```bash
sqlite3 "$DB_DSN" "VACUUM INTO 'engram-backup.db'"
```

Restore:

```bash
# 1. Stop the service.
# 2. Replace the database file (the service must not be running).
cp engram-backup.db "$DB_DSN"
# 3. Start the service again.
```

A plain file copy also works, but only while the service is stopped. `VACUUM INTO` is the safe
option when it is running.

### Media

```bash
tar czf engram-media.tgz "$MEDIA_DIR"
```

Restore by extracting the archive next to the database backup. Media is content-addressed by
sha256, so restoring an older snapshot over a newer directory only adds files; it never corrupts
existing ones.

### Deck packages

For a per-deck backup that a non-admin can make from the browser, use a deck package: export it
from the deck page, or from the CLI (which needs the same environment as the service):

```bash
engram export --deck 1 --package deck-1.fdeck
engram import --package deck-1.fdeck --dry-run
```

## External integration (REST API and MCP)

The service is a card store, a scheduler, and an API. Producing cards from source material happens
outside it — in a script, an agent, or any other system — and reaches it through one of two
equivalent transports:

- **REST API** under `/api/v1`, authenticated with a per-user API key
  (`Authorization: Bearer fcard_...`).
- **Built-in MCP server**, mounted at `POST /mcp` (HTTP only — no stdio), authenticated with the
  same API key.

Both call the same service methods, so validation and scheduling rules cannot drift. A key carries
scopes (`read`, `write`, `review`, `admin`); the MCP handshake only exposes the tools its scopes
allow. Create and manage keys under Settings in the browser. The plaintext key is shown once.

A typical loop, entirely outside the service: an agent reads source material, drafts
question/answer pairs, previews them with `dry_run`, writes them, and a human reviews them in the
browser. The service only keeps the books.

```bash
curl -fsS https://engram.example.com/api/v1/decks \
  -H "Authorization: Bearer $FLCARD_KEY"
```

Request and response schemas for bulk import and deck packages live in `schema/`
(`note-import.schema.json`, `deck-package.schema.json`).

## Development

Build, vet, format, and test (AGENTS.md §4):

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./...
```

Generated code and assets are gitignored and must be regenerated before a build:

```bash
templ generate
tailwindcss -i ./internal/web/static/css/input.css \
            -o ./internal/web/static/css/tailwind.css --minify
```

`go generate ./...` runs both of the above.

Run locally with SQLite:

```bash
mkdir -p data
DB_DRIVER=sqlite DB_DSN=data/engram.db AUTO_MIGRATE=1 \
SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
go run ./cmd/engram serve
```

Repository checks (run them after `git add`, because they scan tracked files only):

```bash
bash scripts/checks/no-private-data.sh
bash scripts/checks/no-template-literals.sh
```

The Tailwind standalone CLI is a glibc binary, so the container builder stage must use a glibc base
image, not Alpine.

## License and project name

The project name and module path are settled; the licence is still a placeholder:

- Project name and module path: `Engram` / `example.com/engram`.
- Licence: not chosen yet (MIT / Apache-2.0 / AGPL-3.0). `LICENSE` currently holds a placeholder
  (AGENTS.md B-2, DESIGN.md §13 #2).