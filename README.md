# Engram

**English** | [中文](README.zh.md)

Engram is a web-first, multi-user, self-hosted spaced-repetition (SRS) service. You write cards in
the browser, or an external script or agent pushes them in through a per-user API key or the
built-in MCP server. Scheduling uses FSRS v6. The service never parses any external note system.

## What you get

- Multi-user accounts with private per-user scheduling state; deck content is shared, with three
  roles (owner, editor, reader), share links, and clone.
- Ten built-in card types (cloze, typed, numeric, single- and multiple-choice, true/false, and
  more), managed by a registry, scheduled with FSRS v6 (configurable desired retention — default
  0.90 — learning steps, maximum interval, and fuzz).
- Markdown and TeX rendering (self-hosted MathJax 3) behind an HTML allowlist, and a Chinese and
  English interface driven by translation catalogs.
- A PWA shell (add to home screen, standalone window, static assets cached only), deck packages
  (`.edeck`) for backup and migration, a REST API (`/api/v1`), a built-in MCP server, and an admin
  panel for users, registration policy, OIDC, upload limits, audit log, jobs, and health.
- Parameter optimisation: retrain the FSRS parameters from your own review history, triggered from
  the preset page, run in a background job, and revertible once finished.

## Screenshots

<!-- Screenshots to be added. -->

## Quick start

The container image defaults to SQLite, runs migrations at startup, and stores everything under
`/data`.

```bash
docker build -t engram:local .

docker run -d --name engram -p 8080:8080 \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -v engram-data:/data \
  engram:local
```

Open `http://localhost:8080/`; the first visit goes to `/setup` to create the first admin account.

## First steps

1. **Create the admin.** The first visit lands on `/setup`; enter an email and password there.
2. **Create a deck.** From the deck list, add a deck to hold the cards.
3. **Add cards.** Write them by hand in the editor, or push them in through the API or MCP server.
4. **Start reviewing.** Open the review queue and grade each card; the schedule adapts to your
   answers.

## Deploying with PostgreSQL

PostgreSQL is the default deployment database. Build the container image from `Dockerfile`, or
run the binary with the same environment.

```bash
docker run -d --name engram -p 8080:8080 \
  -e BASE_URL=https://engram.example.com/ \
  -e DB_DRIVER=postgres \
  -e DB_DSN="postgres://engram:CHANGE_ME@localhost:5432/engram?sslmode=disable" \
  -e SESSION_SECRET="${SESSION_SECRET:-$(openssl rand -base64 32)}" \
  -e ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -base64 32)}" \
  -e AUTO_MIGRATE=0 \
  -v engram-media:/data/media \
  engram:local
```

`BASE_URL` must use `https://` in production: its scheme decides the session cookie's `Secure` flag.

### Environment variables

Every variable the service reads at startup. This table mirrors `.env.example`.

| Variable                 | Required | Default                  | Meaning                                                         |
| ------------------------ | -------- | ------------------------ | --------------------------------------------------------------- |
| `HTTP_ADDR`              | no       | `127.0.0.1:8080`         | Listen address.                                                 |
| `BASE_URL`               | no       | `http://localhost:8080`  | Public URL; its scheme sets the session cookie's `Secure` flag. |
| `DB_DRIVER`              | yes      | —                        | `postgres` or `sqlite`.                                         |
| `DB_DSN`                 | yes      | —                        | Connection string (PostgreSQL) or file path (SQLite).           |
| `SESSION_SECRET`         | yes      | —                        | Session-signing secret; `openssl rand -base64 32`.              |
| `ENCRYPTION_KEY`         | yes      | —                        | Master key for encrypted settings; base64 of exactly 32 bytes.  |
| `AUTO_MIGRATE`           | no       | `0`                      | Run migrations at startup (`1` / `true`).                       |
| `BOOTSTRAP_ADMIN_EMAIL`  | no       | —                        | Pre-fills the first-admin setup form.                           |
| `MEDIA_DIR`              | no       | `data/media`             | Local media directory.                                          |
| `MEDIA_MAX_BYTES`        | no       | setting, else 10 MiB     | Per-file upload limit override.                                 |
| `MEDIA_USER_QUOTA_BYTES` | no       | setting, `0` = unlimited | Per-user media quota override.                                  |
| `MEDIA_ALLOWED_MIMES`    | no       | built-in list            | Allowed upload MIME types override.                             |
| `SMTP_HOST`              | no       | —                        | SMTP host; empty means unconfigured.                            |
| `SMTP_PORT`              | no       | `587`                    | SMTP port.                                                      |
| `SMTP_USERNAME`          | no       | —                        | SMTP username.                                                  |
| `SMTP_PASSWORD`          | no       | —                        | SMTP password.                                                  |
| `SMTP_FROM`              | no       | —                        | Sender address.                                                 |
| `SMTP_TLS_MODE`          | no       | `starttls`               | `none`, `starttls`, or `implicit`.                              |

`MEDIA_*` and `SMTP_*` are normally configured in the admin panel and take effect immediately; the
environment variables only override those values.

## Configuration

- Four variables are required at startup: `DB_DRIVER`, `DB_DSN`, `SESSION_SECRET`, and
  `ENCRYPTION_KEY`; `ENCRYPTION_KEY` must be base64 of exactly 32 bytes, or the server exits.
- Registration policy, OIDC, upload limits, and email are changed in the admin panel, and take
  effect on the next request with no restart.

## Upgrading

Run migrations explicitly before starting or rolling out a new version:

```bash
engram schema sync
```

In production, leave `AUTO_MIGRATE=0` so a failed migration surfaces as a failed command instead of
a half-started server. In a container, run it as a one-off with the same environment
(`docker run --rm ... engram:local schema sync`).

## Backup and restore

Also back up the media directory (`MEDIA_DIR`), which holds the uploaded bytes; the database stores
only their paths and hashes.

**PostgreSQL** — `pg_dump -Fc -f engram.dump "$DB_DSN"`; restore with
`pg_restore --clean --if-exists -d "$DB_DSN" engram.dump` (stop the service first).

**SQLite** — `sqlite3 "$DB_DSN" "VACUUM INTO 'engram-backup.db'"` is consistent while the service
runs; to restore, stop the service and replace the file (a plain copy works only while stopped).

**Media** — `tar czf engram-media.tgz "$MEDIA_DIR"`; extract it next to the database backup. Media
is content-addressed by sha256, so an older snapshot never corrupts newer files.

**Deck packages** — a per-deck backup any user can make from the deck page, or from the CLI
(`engram export --deck 1 --package deck-1.edeck`, then
`engram import --package deck-1.edeck --user admin --dry-run`).

## API and MCP

The service is a card store, a scheduler, and an API. Cards are produced from source material
outside it and reach it through two equivalent transports: a **REST API** under `/api/v1`, and a
**built-in MCP server** at `POST /mcp` (HTTP only, no stdio). Both authenticate with a per-user API
key and call the same service methods, so validation and scheduling rules cannot drift. Keys carry
scopes (`read`, `write`, `review`, `admin`) and are managed under Settings. Schemas live in
[`schema/`](schema/).

## Roadmap

Planned work, not implemented: none of it is part of the current release. The task-level
breakdown, with acceptance criteria, lives in [`ROADMAP.md`](ROADMAP.md).

- **LLM-assisted grading.** Point the service at an OpenAI-compatible provider (system-wide key
  or bring your own, with a global off switch and a monthly call cap) and have free-text answers
  graded automatically: the card, your answer, the reference answer and any linked reference
  material are assembled into the prompt, and the model's verdict is mapped onto the usual 1-4
  rating. Grading runs asynchronously, so a model call never blocks review submission.
- **Reference material bound to cards.** Attach source documents to a note, or to a whole deck,
  for grading to cite; keyword retrieval first, vector search later.
- **Grading history.** A per-card record of machine grades next to your own ratings, so you can
  see where the two disagree.
- **No external optimiser binary.** Once `go-fsrs` ships its own parameter optimiser, the Rust
  helper and its build step go away and optimisation runs in-process. The job, the weights it
  writes back, and the error messages stay the same.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

[AGPL-3.0](LICENSE).
