# ARCHITECTURE.md — How engram is built

This file is a map of the current code: the processes, the packages, and how a request or a
review travels through them. It changes whenever the structure changes. The rules for changing
the repository live in `AGENTS.md`; the task backlog and the release ledger live in `ROADMAP.md`.

The code decides. If this file and the code disagree, the code is right and this file is stale:
fix this file in the same change that notices the disagreement.

---

## 1. Shape of the system

- **One Go binary.** `cmd/engram` is the only entry point. Its subcommands are `serve`,
  `schema`, `export`, `import`, and `version`. Every dependency is constructed and wired by hand
  in `cmd/engram/main.go`; there is no dependency-injection framework.
- **One single-page frontend.** `frontend/` is a Svelte 5 application built by Vite into
  `frontend/dist`. `frontend/embed.go` embeds that directory into the binary with `go:embed`, and
  `internal/web` serves it. The build output is gitignored, so it must be built before the Go
  build (see `AGENTS.md` section 4).
- **Two databases, one model set.** GORM talks to PostgreSQL (the default deployment) or SQLite
  (single node and development). `AutoMigrate` covers additive changes; anything else is an
  explicit versioned migration in `internal/store/migrate.go`.
- **One subprocess.** FSRS parameter optimisation runs the Rust adapter in `tools/optimizer` as a
  child process, started by `internal/jobs`. The optimiser algorithm never enters the Go code.

## 2. Repository layout

```
engram/
├── AGENTS.md                 # rules for changing the repository
├── ARCHITECTURE.md           # this file: the current structure
├── ROADMAP.md                # task backlog and release ledger
├── CONTRIBUTING.md           # guide for external contributors
├── README.md / README.zh.md  # English is the primary document; Chinese is parallel
├── cmd/engram/               # the binary: subcommands and explicit wiring
├── internal/                 # all Go packages (section 3)
├── frontend/                 # Svelte 5 SPA, its tests, and embed.go
├── schema/                   # JSON Schemas for note import, bulk notes, and deck packages
├── decks/                    # bundled deck library
├── tools/optimizer/          # Rust FSRS optimiser adapter
├── Dockerfile, Dockerfile.release, docker-compose.yaml
└── go.mod                    # Go module definition
```

There is no top-level `test/` directory. Integration tests live beside the code as
`internal/<package>/*_test.go`; frontend tests live in `frontend/src/tests/`.

## 3. Go packages

| Package | Responsibility |
|---|---|
| `internal/config` | Startup configuration. Precedence is environment variable, then the `settings` table, then the built-in default; every accessor can report which source won. |
| `internal/store` | GORM models (`AllModels` in `models.go`) and the concrete stores that wrap database access, including deck package import and export. |
| `internal/cardtype` | The card type registry and one file per card type. Field names match `schema/note-import.schema.json`. |
| `internal/schedule` | The go-fsrs wrapper: card state machine, queue builder, daily budgets, review submission, and undo. |
| `internal/auth` | Local accounts, password hashing, sessions, CSRF, TOTP, OIDC, API keys, roles, and deck access checks. |
| `internal/api` | `/api/v1` REST handlers, their authentication and rate-limit middleware, and the service layer (`service.go` and the methods on `API`) that REST and MCP share. |
| `internal/mcp` | The built-in MCP server at `/mcp` (streamable HTTP). It accepts API keys only, and every tool calls an `internal/api` service method. |
| `internal/web` | The gin router: security headers, session and locale middleware, the session-and-CSRF JSON endpoints the SPA uses, the admin API, media proxying, the SPA shell, and the not-found fallback. It mounts `internal/api` and `internal/mcp`. |
| `internal/media` | Content-addressed local file storage (`<sha256[:2]>/<sha256>.<ext>`) and media metadata. |
| `internal/mediatype` | Leaf package with the media type detection and allowlist shared by `internal/media` and `internal/store`. |
| `internal/render` | Markdown to HTML (goldmark), including the `{{name:body|key=value}}` shortcode form, followed by allowlist sanitising (bluemonday). Card content and mail bodies share the pipeline; the mail allowlist additionally permits headings, and the mail renderer turns a shortcode into plain text instead of an element. |
| `internal/texmath` | The single rule for locating TeX math, shared by `internal/render` and `internal/cardtype`. |
| `internal/i18n` | Server-side translation catalogs (`internal/i18n/locales/*.yaml`), request language resolution, and the localiser placed in the request context. |
| `internal/mail` | Mail templates (built-in defaults and admin overrides share one Markdown renderer), the fixed inline-styled HTML shell, the outbox, SMTP transport, and the delivery worker with backoff retry. |
| `internal/reminder` | Worker that emails users who have due cards. |
| `internal/digest` | Worker that sends the weekly digest email. |
| `internal/retention` | Worker that deletes expired sessions, used or expired action tokens, expired invites, and old login fingerprints. |
| `internal/worker` | The shared start, stop, and tick loop used by the periodic workers. |
| `internal/jobs` | Single-concurrency background jobs persisted in the `jobs` table; runs the optimiser subprocess with timeout and progress capture. |
| `internal/urlfetch` | Guarded HTTPS download used to import a deck package from a URL. |
| `internal/pgtest` | Test helper that gives each test its own PostgreSQL schema when `TEST_PG_DSN` is set. |

### Dependency direction

Imports between internal packages, as reported by `go list`:

```
cmd/engram → web, api, mcp, auth, config, digest, i18n, jobs, mail, media, reminder, retention, store
web        → api, mcp, auth, cardtype, config, i18n, jobs, mail, media, mediatype, reminder, render, schedule, store
mcp        → api, store
api        → auth, cardtype, media, schedule, store, urlfetch
digest     → auth, i18n, mail, reminder, schedule, store, worker
reminder   → auth, i18n, mail, schedule, store, worker
jobs       → schedule, store
mail       → config, render, store
schedule   → store
auth       → store
media      → mediatype, store
store      → cardtype, mediatype
cardtype   → texmath
render     → texmath
retention  → worker
config, i18n, mediatype, texmath, urlfetch, worker, pgtest → (no internal imports)
```

Regenerate it with
`go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/... ./cmd/...` when packages
change.

## 4. Request paths

- **Browser (SPA).** The browser authenticates with a session cookie. Writes carry a CSRF token
  bound to the session. Pages are client-side routes served by the SPA shell; data comes from JSON
  endpoints registered by `internal/web` and from `/api/v1`.
- **REST clients.** `/api/v1` (`internal/api`) accepts either the session channel or a bearer API
  key. An API key carries scopes, and each route declares the scope it requires.
- **MCP clients.** `/mcp` (`internal/mcp`) accepts API keys only and re-resolves the identity on
  every request.
- **One business layer.** A REST handler and the MCP tool for the same operation call the same
  `internal/api` service method, so validation and results cannot drift between transports.
  Service errors (`ServiceError`) carry an HTTP status, a stable English `code`, and a message.

## 5. Data model

- **Content and progress are separate.** `notes` and `cards` hold content. Per-user scheduling
  state lives in `card_states`, keyed `(card_id, user_id)`. A shared deck therefore never mixes
  two users' progress.
- **Review log.** `reviews` records every rating with the full before-and-after state. It is the
  input to parameter optimisation. Undoing a review deletes that one row and writes a
  `review.undo` audit row.
- **Decks and sharing.** A deck has an owner. Grants give other users a role on the deck. Each
  member chooses their own preset and study settings for a shared deck.
- **Deck pause.** A user pauses a deck for themselves in `deck_pauses`, keyed `(deck_id, user_id)`;
  the owner and every member decide alone, and no `decks` column is involved. Visibility and
  the default study scope are two predicates in `internal/store`: `visibleDeckIDsQuery` (owned or
  granted) decides what a user can see, and `activeDeckIDsQuery` (visible minus paused) decides what
  is studied when no deck is named — the whole-library queue, the summary `due`, the unfiltered due
  forecast, and the weekly digest built on it. The reminder query applies the same exclusion.
- **Presets.** A preset holds the FSRS weights, desired retention, learning and relearning
  steps, and fuzz switch that the scheduler for a deck uses.
- **Audit.** Security-relevant and settings-changing actions append rows to the audit log; the
  action names are constants in `internal/store/audit.go`.

## 6. Scheduling

- `internal/schedule` builds a go-fsrs scheduler per deck from the member's effective preset.
- **Queue.** The queue builder selects due reviews and new cards for one or more decks and a tag
  filter, for the user's current review day. With no deck named it covers the user's active decks
  (`DeckStore.ActiveIDs`), so a paused deck only enters the queue when it is named.
- **Daily budgets.** `deckBudget` in `internal/schedule/queue.go` is the only implementation of
  the per-deck daily new and review limits. The review queue, the deck list counts, and the deck
  settings page all call it, so the three numbers always agree.
- **Daily limits are goals, not caps.** After the daily new-card goal is met, a user can add
  another batch of new cards to one deck for the current review day. The extra amount is stored
  per deck, per user, per review day in `deck_daily_extras`, and `deckBudget` adds it to the new
  card limit. It expires with the review day. REST (`/api/v1/decks/:id/extra-new`) and MCP
  (`add_extra_new_cards`) share one service method.
- **Deck progress.** `DeckProgress` in `internal/schedule/deck_progress.go` counts, per deck and
  per user, the cards studied and the new cards left, independent of the daily budgets. A new card
  follows the queue's rule: no state row, or state new. A suspended new card is not counted as
  left; a suspended studied card still counts as studied. The SPA reads both numbers from the
  session-only `/api/v1/decks/queue-counts`, next to the daily budget counts.
- **Interval preview.** The review screen shows the next interval for each rating. The preview
  uses a scheduler with fuzz disabled, because go-fsrs seeds its fuzz with the current
  millisecond and a fuzzed preview could never match the submitted result. With fuzz enabled on
  the preset, the real interval can therefore differ slightly from the preview.
- **Submission.** Submitting a review checks `expected_version`, returns `409` on mismatch, and
  writes the state update and the review row in one transaction.

## 7. Background work

`cmd/engram serve` starts these alongside the HTTP server and stops them on shutdown:

- the retention worker (`internal/retention`), always;
- the mail delivery worker (`internal/mail`) and the reminder and digest workers, when the mail
  outbox is wired; the reminder, digest, and retention workers share `internal/worker`;
- the jobs runner (`internal/jobs`), which runs at most one optimisation job at a time and
  answers `409` when another job is already unfinished.

## 8. Frontend

- `frontend/src/lib/views/` holds one view per page, `frontend/src/lib/components/` the shared
  components, and `frontend/src/lib/api/` the typed client for the JSON endpoints.
- UI text comes from `frontend/src/lib/i18n/locales/{zh-CN,en}.ts`. Text produced by the
  server (emails, server-side messages) comes from `internal/i18n/locales/{zh-CN,en}.yaml`.
- Tests run in two vitest suites: an SSR suite and a happy-dom suite for `*.client.ts` files.
  `svelte-check` type-checks the components.
