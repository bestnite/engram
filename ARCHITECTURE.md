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
├── tools/anki-oracle/        # Python oracle that generates the Anki reference fixtures
├── Dockerfile, Dockerfile.release, docker-compose.yaml
└── go.mod                    # Go module definition
```

There is no top-level `test/` directory. Integration tests live beside the code as
`internal/<package>/*_test.go`; frontend tests live in `frontend/src/tests/`.

## 3. Go packages

| Package | Responsibility |
|---|---|
| `internal/config` | Startup configuration. Precedence is environment variable, then the `settings` table, then the built-in default; every accessor can report which source won. |
| `internal/store` | GORM models (`AllModels` in `models.go`) and the concrete stores that wrap database access, including deck package import and export. `MediaStore.Put` is the single content-addressed media write (`<sha256[:2]>/<sha256>.<ext>`, temp file + rename, dedup-hit rules) used by both upload and import. |
| `internal/cardtype` | The card type registry and one file per card type. Field names match `schema/note-import.schema.json`. |
| `internal/notetype` | The note type data model, validation and Anki's six stock definitions, plus the template engine that renders one side of a card: template syntax and special fields, the built-in template filters, and the cloze and image-occlusion markup. |
| `internal/reviewday` | Leaf package with the single definition of a user's review day: timezone resolution (unknown names fall back to UTC), the day cutoff hour, the review-day boundaries, and the `YYYY-MM-DD` day string written to `reviews.review_day`. Grading, burying, budgets, statistics, reminders and digests all read it. |
| `internal/schedule` | The go-fsrs wrapper: card state machine, queue builder, daily budgets, review submission, and undo. |
| `internal/auth` | Local accounts, password hashing, sessions, CSRF, TOTP, OIDC, API keys, roles, and deck access checks. |
| `internal/api` | `/api/v1` REST handlers, their authentication and rate-limit middleware, and the service layer (`service.go` and the methods on `API`) that REST and MCP share. |
| `internal/mcp` | The built-in MCP server at `/mcp` (streamable HTTP). It accepts API keys only, and every tool calls an `internal/api` service method. |
| `internal/web` | The gin router: security headers, session and locale middleware, the session-and-CSRF JSON endpoints the SPA uses, the admin API, media proxying, the SPA shell, and the not-found fallback. It mounts `internal/api` and `internal/mcp`. |
| `internal/media` | Local media storage around `store.MediaStore.Put`: streaming and validating uploads, serving files, and reclaiming orphans. |
| `internal/mediatype` | Leaf package with the media type detection, the mime-to-extension table and the allowlist shared by `internal/media` and `internal/store`. |
| `internal/render` | Markdown to HTML (goldmark), including the `{{name:body|key=value}}` shortcode form, followed by allowlist sanitising (bluemonday). Card content and mail bodies share the pipeline; the mail allowlist additionally permits headings, and the mail renderer turns a shortcode into plain text instead of an element. |
| `internal/texmath` | The single rule for locating TeX math, shared by `internal/render` and `internal/cardtype`. |
| `internal/i18n` | Server-side translation catalogs (`internal/i18n/locales/*.yaml`), request language resolution, and the localiser placed in the request context. |
| `internal/mail` | Mail templates (built-in defaults and admin overrides share one Markdown renderer), the fixed inline-styled HTML shell, the outbox, SMTP transport, and the delivery worker with backoff retry. `Mailer` is the single send path: it checks SMTP and the recipient's preference, resolves the site name and template, adds the footer and the unsubscribe link, and enqueues. The web layer builds the process's one `Mailer` and the reminder and digest workers receive it from `cmd/engram`. |
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
web        → api, mcp, auth, cardtype, config, i18n, jobs, mail, media, mediatype, reminder, render, reviewday, schedule, store
mcp        → api, store
api        → auth, cardtype, media, reviewday, schedule, store, urlfetch
digest     → i18n, mail, reminder, reviewday, schedule, store, worker
reminder   → i18n, mail, reviewday, schedule, store, worker
jobs       → schedule, store
mail       → config, render, store, worker
schedule   → reviewday, store
auth       → store
media      → mediatype, store
store      → cardtype, mediatype, reviewday
cardtype   → texmath
render     → texmath
retention  → worker
config, i18n, mediatype, notetype, reviewday, texmath, urlfetch, worker, pgtest → (no internal imports)
```

Regenerate it with
`go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/... ./cmd/...` when packages
change.

## 4. Request paths

- **Browser (SPA).** The browser authenticates with a session cookie. Writes carry a CSRF token
  bound to the session. Pages are client-side routes served by the SPA shell; data comes from JSON
  endpoints registered by `internal/web` and from `/api/v1`.
- **Card frame.** `GET /card-frame/<build-version>` serves the review iframe document
  (`internal/web/card_frame.go`). The version segment only busts the iframe cache and is otherwise
  ignored. The route replaces the site headers with the frame's own sandbox CSP, sends
  `Referrer-Policy: no-referrer`, and drops `X-Frame-Options`; the parent page embeds the document
  in a sandboxed iframe and communicates with it only through `postMessage`.
- **Frame media.** `GET /fm/<token>/<name>` (`internal/web/frame_media.go`) serves media to the
  sandboxed frame, which has no session cookie. The token is an HMAC over `(user_id, deck_id,
  expiry)`; its key lives in the encrypted settings channel under `frame_media_key` and is
  created on first use. Each request checks the signature, the expiry, the token user's current
  status, the user's current read access to the token's deck and the media readability rule, and
  every failure is the same bare 404. Tokens live six hours. A card-face endpoint that is asked
  for frame output with the request header `X-Engram-Frame: 1` renders its front and back HTML
  with relative media names; the response also carries `media_base` (`/fm/<token>/`), which the
  frame document sets as its `<base href>`. A request without that header keeps the absolute
  `/media/<sha256>` URLs, because the SPA has no base element to resolve a relative name against.
  Choice options stay absolute in both cases: they remain in the parent page in this phase.
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
  is studied when no deck is named — the whole-library queue, the "due now" number, and the unfiltered
  due forecast.
- **Presets.** A preset holds the FSRS weights, desired retention, learning and relearning
  steps, and fuzz switch that the scheduler for a deck uses.
- **Audit.** Security-relevant and settings-changing actions append rows to the audit log; the
  action names are constants in `internal/store/audit.go`.

## 6. Scheduling

- `internal/schedule` builds a go-fsrs scheduler per deck from the member's effective preset.
- **Card predicates.** `internal/store/card_scope.go` is the only definition of which cards a user can
  study (live and not suspended), which are due (learning or review state with `due_at` reached) and
  which are new (no state row, or state new; a buried new card is still new, never due). The queue,
  the deck list counts, deck progress, the due forecast, the health page and the reminder prefilter
  all build on it.
- **Due now.** `QueueBuilder.DueNow` sums the review half of `DeckCounts` over the active decks. It is
  the only "due" number shown to a user outside a deck: the home summary (REST and MCP), the review
  reminder mail and the weekly digest. The stats page's per-deck table shows `DeckCounts` too, so every
  page agrees with what `/review` serves.
- **Queue.** The queue builder selects due reviews and new cards for one or more decks and a tag
  filter, for the user's current review day. With no deck named it covers the user's active decks
  (`DeckStore.ActiveIDs`), so a paused deck only enters the queue when it is named.
- **Daily budgets.** `deckBudget` in `internal/schedule/queue.go` is the only implementation of
  the per-deck daily new and review limits. The review queue, the deck list counts, and the deck
  settings page all call it, so the three numbers always agree.
- **Daily limits are goals, not caps.** After the daily new-card goal is met, a user can add
  another batch of new cards to one deck for the current review day. The extra amount is stored
  per deck, per user, per review day in `deck_daily_extras`, and `deckBudget` adds it to the new
  card limit. It expires with the review day. Review days themselves come from `internal/reviewday`. REST (`/api/v1/decks/:id/extra-new`) and MCP
  (`add_extra_new_cards`) share one service method.
- **Deck progress.** `DeckProgress` in `internal/schedule/deck_progress.go` counts, per deck and
  per user, the cards studied and the new cards left, independent of the daily budgets. The new
  cards left are `store.UserNewCards`, the queue's own definition. A suspended new card is not counted as
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
  outbox is wired; the mail outbox and the reminder, digest, and retention workers share
  `internal/worker`;
- the jobs runner (`internal/jobs`), which runs at most one optimisation job at a time and
  answers `409` when another job is already unfinished.

## 8. Frontend

- `frontend/src/lib/views/` holds one view per page, `frontend/src/lib/components/` the shared
  components, and `frontend/src/lib/api/` the typed client for the JSON endpoints.
- Vite has two entries: `frontend/index.html` (the SPA shell) and `frontend/card-frame.html`
  (the card frame document). `frontend/src/cardframe/` is the frame runtime: it holds no Svelte component
  and reads no cookie; it writes the content sent by the parent page into `#qa`, re-executes the content
  scripts, typesets the face, attaches the shortcode controls to it, and posts height, keys, answers
  and links back to the parent. Its language is the `<html lang>` the server resolved for the request.
- `frontend/src/lib/components/CardFrame.svelte` is the only way a view embeds a card face: it owns
  the sandboxed iframe, sends `show` and `theme`, and forwards the frame's key presses to the view.
  Card-face HTML therefore never enters the parent DOM. The SPA's remaining HTML sinks are the ones
  the parent still renders (the choice options and the admin mail preview), and
  `frontend/src/tests/html-sink-allowlist.test.ts` lists each of them with its reason.
- `frontend/src/lib/format.ts` is the only place numbers, percentages, byte counts, dates and raw field
  values become text; components pass the current locale.
- UI text comes from `frontend/src/lib/i18n/locales/{zh-CN,en}.ts`. Text produced by the
  server (emails, server-side messages) comes from `internal/i18n/locales/{zh-CN,en}.yaml`.
- Tests run in two vitest suites: an SSR suite and a happy-dom suite for `*.client.ts` files.
  `svelte-check` type-checks the components.
