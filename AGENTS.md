# AGENTS.md — Development Guide

Read this file before making any change to this repository. It records the rules that are
not obvious from the code, the definition of "done", and the task backlog with stable IDs.

---

## 1. Source of truth

- `DESIGN.md` is the single source of truth for behaviour, data model, and scope.
- If code and `DESIGN.md` disagree, one of them is a bug. Fix the code, or change
  `DESIGN.md` in the same commit — never leave them inconsistent.
- Anything not decided in `DESIGN.md` (section 13 holds the open questions) must be asked
  or added there, not invented in code.

---

## 2. Non-negotiable rules

Each rule exists because breaking it is expensive to undo. Follow them literally.

### 2.1 Text language

| Kind of text | Language | Notes |
|---|---|---|
| Logs (`slog` messages and fields, job and subprocess output, CLI diagnostics, `panic` text, internal error strings) | **English** | Never localised, never taken from a translation catalog. |
| Code comments (package/function docs and inline explanations) | **Chinese** | Explain *why*, not *what*. |
| User-facing text (UI labels, form validation, API/MCP `message`) | **Translation catalog** (`zh-CN`, `en`) | Never hardcode. |
| Identifiers (log messages, catalog keys, error `code` values) | **English** | Keeps catalogs and logs searchable and avoids mixed-language keys. |

Decision rule: machine and developer facing → English; reader of the source → Chinese;
end user → translation catalog.

### 2.2 Repository sanitisation (open-source repo)

- No real domain names, host names, private IP ranges, personal email addresses, tokens,
  passwords, or descriptions of private infrastructure in **any** committed file —
  including examples, fixtures, comments, and documentation.
- Use placeholders: `example.com`, `localhost`, `CHANGE_ME`.
- Local data, credentials, generated files, and build output stay out of git
  (see `.gitignore`). Run `git status` before every commit and check nothing unintended
  is staged.
- The CI sanitisation scan must stay green; if a needed string trips it, change the
  placeholder, do not weaken the scan.

### 2.3 Domain invariants (do not break these)

1. **Content and progress are separate.** `notes`/`cards` hold content only; per-user
   scheduling lives in `card_states`, keyed `(card_id, user_id)`. Sharing a deck must
   never mix two users' progress.
2. **`reviews` is append-only.** Every column listed in `DESIGN.md` §2.2 is written from
   the first commit — it is the only fuel for parameter optimisation and cannot be
   reconstructed later.
3. **Ratings and states are integers**: `rating` 1–4 (Again/Hard/Good/Easy),
   `state_before` 0–3 (New/Learning/Review/Relearning). This matches the FSRS ecosystem
   log format and keeps optimiser export trivial.
4. **Both databases must work**: PostgreSQL (default deployment) and SQLite (single-node
   and development). No PG-only types (`jsonb`, `serial`, `array`), JSON stored as `TEXT`,
   `clause.OnConflict` for upserts, `LIMIT/OFFSET` for paging. See `DESIGN.md` §2.3.
5. **AutoMigrate is additive only.** Column type changes, drops, and new non-null
   constraints require an explicit, versioned migration function.
6. **One business layer, two transports.** REST handlers and MCP tools call the same
   service methods. Never duplicate validation or scheduling logic per transport.
7. **Card types are registered, not hardcoded.** Adding a type means adding one file and
   registering it; core code must not change.
8. **Review submission is idempotent.** Enforce the `expected_version` check and return
   `409` on mismatch; write the state update and the review row in one transaction.

### 2.4 Go style in this repo

- Plain monolith, organised by business package; concrete types over interfaces;
  dependencies wired explicitly in `cmd/flashcard`.
- **One model serves business, GORM, and JSON/CSV.** No entity/DTO mapping layers.
- Introduce an interface only when there are multiple implementations or a concrete
  replacement need; define the minimal interface **at the consumer**.
- Concrete `Store` types wrap GORM access. Add a `Service` only for genuinely complex
  flows.
- Blocking operations take `context.Context` as the first parameter.
- Errors: wrap with `%w`; API/MCP errors carry a stable English `code` plus a localised
  `message`.
- No new dependency without a reason that survives the selection principle in
  `DESIGN.md` §10.1 (low complexity, prefer mature libraries over invented ones).

### 2.5 Tests and commits

- Table-driven tests; use a real SQLite database (in-memory or temp file) — do not mock
  the database. Core coverage targets: `internal/schedule` and `internal/cardtype`.
- Handler tests use `httptest`. Negative cases are required, not optional: permission
  denials, CSRF failures, expired keys, scope violations.
- Commits are signed (`user.signingkey` is set per repository) and use Conventional
  Commits prefixes: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`.
- One logical change per commit; message body explains why, not what.

### 2.6 Writing requirements in this repository

Every written requirement, instruction, or document line must have exactly **one**
possible reading. After writing, re-read each line; if a second reading is possible,
split it into explicit statements. This applies to code comments, task descriptions in
this file, commit messages, and `DESIGN.md`.

---

## 3. Layout

```
flashcard/
├── AGENTS.md                 # this file
├── DESIGN.md                 # specification of record
├── README.md / README.zh.md  # English is the primary document; Chinese is parallel
├── LICENSE
├── go.mod                    # module <module-path>
├── cmd/flashcard/main.go     # subcommand entry point
├── internal/
│   ├── config/               # env vars + settings-table overlay and precedence
│   ├── store/                # GORM models and business stores
│   ├── schedule/             # go-fsrs wrapper: state machine, queue, review submit
│   ├── cardtype/             # card type registry and type implementations
│   ├── auth/                 # local accounts, sessions, CSRF, OIDC, API keys, roles
│   ├── media/                # local file storage: dedupe, write, proxy read
│   ├── api/                  # /api/v1 handlers
│   ├── mcp/                  # MCP server: tool definitions that call the services
│   ├── i18n/                 # locales/{zh-CN,en}.yaml plus loader and translator
│   └── web/                  # gin routes, handlers, templ views, static assets
└── test/                     # integration tests
```

---

## 4. Build and verify

```bash
# code
go build ./... && go vet ./... && gofmt -l . && go test ./...

# generated code and assets (templ emits *_templ.go; Tailwind emits the CSS bundle)
templ generate
tailwindcss -i ./internal/web/static/css/input.css \
            -o ./internal/web/static/css/tailwind.css --minify

# run locally (SQLite is fine for development)
DB_DRIVER=sqlite DB_DSN=data/flashcard.db AUTO_MIGRATE=1 go run ./cmd/flashcard serve
```

Run the code checks **and** the two generation steps before committing anything that
touches templates or styles, because both outputs are gitignored and a stale build is
invisible in `git status`.

### Definition of done

A task is done only when all of the following hold:

1. The acceptance criteria written next to the task in section 5 pass, and the evidence
   is a command output or a captured response — not a claim.
2. `go build ./... && go vet ./... && gofmt -l . && go test ./...` are clean.
3. New behaviour has tests, including the negative cases named in the task.
4. Logs are English, comments are Chinese, user-facing strings come from the catalog.
5. The change is committed with a signed, conventional commit; `git status` is clean.
6. If the change alters behaviour or decisions, `DESIGN.md` is updated in the same commit.

---

## 5. Task backlog

Conventions:

- `- [ ]` open, `- [x]` done. Sections are milestones from `DESIGN.md` §12.
- IDs are stable. Never renumber or reuse an ID; append new tasks at the end of their
  milestone as `M<n>-<next number>`.
- Each task is written to be handed to one subagent without extra context: it names the
  files, the acceptance criteria, and how to verify them.
- A task may be split, but each resulting task keeps a new ID and its own criteria.
- Progress is reported as `M<n>: done/total` (see section 6).

### M0 — Skeleton

- [x] **M0-1 Module bootstrap** — create `go.mod` (module `<module-path>`, Go 1.25+),
  `cmd/flashcard/main.go` with subcommand dispatch (`serve`, `schema sync`, `export`,
  `optimize`, `version`), `LICENSE` placeholder, `.env.example` kept in sync with
  `internal/config`.
  *Acceptance:* `go build ./...` succeeds; `flashcard version` prints a version string.
- [x] **M0-2 Config loader** — `internal/config`: parse the environment variables listed
  in `.env.example`; load the `settings` table overlay; expose a single accessor that
  reports the effective value and its source (`env` or `db`).
  *Acceptance:* unit test asserts env-over-db precedence and that a missing required
  variable fails startup with an English error.
- [x] **M0-3 GORM models** — `internal/store/models.go`: every table from `DESIGN.md`
  §2.2 with tags that work on both PostgreSQL and SQLite (no `jsonb`, no `serial`).
  *Acceptance:* AutoMigrate on both drivers creates all tables; a test asserts table
  count and the unique constraints on `notes (deck_id, external_ref)`,
  `cards (note_id, template)`, `identities (provider, subject)`, `media (sha256)`.
- [x] **M0-4 Schema version and destructive migrations** — a `schema_version` row plus
  an ordered list of explicit migration functions for changes AutoMigrate cannot make.
  *Acceptance:* test that an unnamed destructive change is refused by `schema sync` and
  that a registered migration runs exactly once.
- [x] **M0-5 HTTP skeleton** — `internal/web`: gin router, request logging middleware
  (English, `slog`), panic recovery, graceful shutdown, `GET /healthz` returning JSON
  with database connectivity and schema version.
  *Acceptance:* `curl /healthz` returns `200` with both fields; shutdown logs one English
  line and exits within two seconds.
- [ ] **M0-6 Templ toolchain** — `templ generate` wired into the build, `base.templ`
  layout, one sample page, generated files gitignored.
  *Acceptance:* `templ generate && go build ./...` succeeds; the sample page renders.
- [ ] **M0-7 Tailwind toolchain** — standalone CLI config plus `input.css`, output at
  `internal/web/static/css/tailwind.css` (gitignored), embedded via `go:embed`.
  *Acceptance:* the build produces a CSS bundle; the sample page uses at least one
  utility class that survives the build.
- [ ] **M0-8 i18n loader** — `internal/i18n`: `go-i18n` with `locales/zh-CN.yaml` and
  `locales/en.yaml`, locale detection from the user setting first and `Accept-Language`
  second, translator placed into the request context.
  *Acceptance:* unit test asserts detection order and that a missing key in one catalog
  fails the test build (parity check).
- [ ] **M0-9 Static assets embedding** — content-hashed paths for CSS/JS plus self-hosted
  htmx and MathJax; a helper generates the hashed URLs for templates.
  *Acceptance:* page source references hashed paths; changing an asset changes its hash.
- [ ] **M0-10 CI pipeline** — workflow running `go build`, `go vet`, `gofmt -l`, `go test`,
  the sanitisation keyword scan, the i18n parity check, and a check that no user-facing
  literal appears in templates.
  *Acceptance:* the pipeline fails on a deliberately planted violation of each check.
- [ ] **M0-11 Container build (optional)** — multi-stage `Containerfile` producing a
  single static binary image; no private registry, host names, or deployment specifics.
  *Acceptance:* image builds locally and serves `/healthz`.

### M1 — Identity and users

- [ ] **M1-1 User store and password hashing** — `internal/auth`: user CRUD, argon2id
  hashing with parameters recorded per hash, password policy check.
  *Acceptance:* test asserts a correct password verifies, a wrong one does not, and that
  hash parameters are stored in a versioned format.
- [ ] **M1-2 Session middleware** — signed HttpOnly cookie sessions, `Secure` and
  `SameSite=Lax`, server-side invalidation on logout, password change, and user disable.
  *Acceptance:* integration test asserts the disabled user's existing session is rejected
  on the next request.
- [ ] **M1-3 CSRF middleware** — token issued per session, required on every non-GET
  request via form field or `X-CSRF-Token`.
  *Acceptance:* test asserts a POST without a token returns `403` and with a valid token
  succeeds.
- [ ] **M1-4 Register, login, logout pages** — templ pages plus handlers, all strings from
  the catalog.
  *Acceptance:* a new user can register, log in, and log out through the browser flow
  exercised by `httptest`.
- [ ] **M1-5 First-admin bootstrap** — `/setup` wizard available only while no admin
  exists; `BOOTSTRAP_ADMIN_EMAIL` honoured as a fallback.
  *Acceptance:* test asserts `/setup` is reachable only before an admin exists and returns
  `404` afterwards.
- [ ] **M1-6 Registration policy** — `settings` value `open` | `invite` | `closed` plus an
  optional email-domain allowlist, enforced at registration.
  *Acceptance:* table-driven test covers every policy and the allowlist denial case.
- [ ] **M1-7 Invites** — create, list, revoke, accept; one-time token, optional email
  restriction, optional expiry.
  *Acceptance:* test asserts a used, expired, or revoked token is rejected and that an
  accepted invite creates exactly one user.
- [ ] **M1-8 Personal settings page** — locale, timezone, day cutoff, display name,
  password change.
  *Acceptance:* changing the locale switches the returned page language; changing the
  cutoff moves `review_day` boundaries in the next test run.
- [ ] **M1-9 Login rate limiting and lockout** — per account and per IP, increasing delay,
  audit entries for failures.
  *Acceptance:* test asserts the delay grows and that a successful login resets it.
- [ ] **M1-10 Audit helper** — one function used by every mutation, writing user, optional
  API key, action, target, and JSON detail.
  *Acceptance:* test asserts one row per mutation with the expected action string.
- [ ] **M1-11 OIDC login** — configuration read from `settings`, discovery document fetch
  with caching, Authorization Code + PKCE, `state` and `nonce` validation, callback
  handler, and a "test connection" action that surfaces the failure reason.
  *Acceptance:* test against a stub provider asserts a successful login and that a wrong
  `state` is rejected; the test connection returns the provider error text.
- [ ] **M1-12 Identity binding** — lookup by `(provider, subject)`, auto-link by verified
  email, policy-gated account creation, and unlink in the admin panel.
  *Acceptance:* table-driven test covers all three branches of `DESIGN.md` §4.5 plus the
  unlink path.
- [ ] **M1-13 Auth test suite** — negative cases for CSRF, policy, binding, session
  invalidation, and rate limiting in one place.
  *Acceptance:* `go test ./internal/auth/...` passes with every negative case present.

### M2 — Decks, notes, cards

- [ ] **M2-1 Deck store and CRUD** — create, read, update, archive, visibility, owner
  assignment.
  *Acceptance:* test asserts a non-owner cannot modify a deck before grants exist
  (foundation for `M5-1`).
- [ ] **M2-2 Preset store and CRUD** — scheduling parameters with documented defaults
  (`desired_retention` 0.90, `learning_steps` `1m,10m`, `relearning_steps` `10m`,
  `enable_fuzz` on).
  *Acceptance:* test asserts defaults are applied and that values round-trip.
- [ ] **M2-3 Card type registry** — `internal/cardtype`: `Validate`, `Cards`, `Render`,
  optional `Grade`, optional `PromptContext`, optional `ReferenceRefs`, `Label`; registry
  lookup by `kind`.
  *Acceptance:* test registers a fake type and asserts the core pipeline handles it
  without any change outside the new file.
- [ ] **M2-4 Memory types** — `basic`, `basic_both`, `cloze`, `list`, including the cloze
  parser for `{{cN::text}}` and `{{cN::text::hint}}`.
  *Acceptance:* cloze tests cover nested braces, escapes, repeated indices, and a note
  producing two cards from two indices.
- [ ] **M2-5 Note and card pipeline** — create a note, generate its cards, enforce
  `(note_id, template)` uniqueness, and support soft delete plus restore.
  *Acceptance:* test asserts updating a note's fields keeps existing cards and their ids.
- [ ] **M2-6 Renderer** — goldmark to HTML, bluemonday allowlist, MathJax delimiters
  `\(` … `\)` and `\[` … `\]`, self-hosted MathJax.
  *Acceptance:* test asserts `script`, event attributes, and `javascript:` URLs are
  stripped while tables, code blocks, and inline math survive.
- [ ] **M2-7 Card list and editor pages** — paging, search, tag filter, live preview,
  bulk actions.
  *Acceptance:* page renders 100 notes with paging intact; preview updates over htmx
  without a full reload.
- [ ] **M2-8 Media storage** — sha256 dedupe, `<sha256[:2]>/<sha256>.<ext>` layout,
  temp-file plus rename writes, `GET /media/:id` proxy with `ETag` and immutable caching,
  mime plus magic-byte validation, admin-configured size limit.
  *Acceptance:* uploading the same file twice stores one blob; an oversized or
  wrong-magic file is rejected with a stable error code.
- [ ] **M2-9 Media surface in the editor** — upload and insert into a card field.
  *Acceptance:* an uploaded image renders in the preview and survives a page reload.

### M3 — Review loop

- [ ] **M3-1 FSRS wrapper** — `internal/schedule`: construct the scheduler from preset
  weights or `DefaultWeights()`, preview four ratings, submit one rating.
  *Acceptance:* test asserts `Repeat` returns four options and `Next` advances the card
  state as documented in `DESIGN.md` §3.2.
- [ ] **M3-2 Queue builder** — learning cards first, then due reviews ordered by
  retrievability (default) or due date, then new cards limited by the daily cap and
  counted from the `reviews` table.
  *Acceptance:* test asserts ordering and that the daily caps are respected across a
  simulated day boundary at 03:59 and 04:00 local time.
- [ ] **M3-3 Review submission** — single transaction writing the state update and the
  review row, with the `expected_version` check returning `409` on mismatch.
  *Acceptance:* test asserts a duplicated submission changes nothing and returns `409`,
  and that a failing transaction leaves no review row.
- [ ] **M3-4 Undo, suspend, bury** — rollback via the last review log, suspend a card,
  bury it for the current day.
  *Acceptance:* test asserts Undo restores the previous due date and interval exactly.
- [ ] **M3-5 Review page** — templ plus htmx: show answer, rate, keyboard shortcuts
  (`space`, `1`–`4`, `u`, `e`, `s`, `b`), swipe on touch devices, server-side next-card
  prefetch in the same response, remaining counters.
  *Acceptance:* an `httptest` walk of 20 cards completes with no extra round trip per
  rating and shows correct counters.
- [ ] **M3-6 Graded types** — `typed`, `numeric`, `choice_single`, `choice_multi`,
  `true_false` with their graders, plus the configurable score-to-rating mapping stored
  in the preset.
  *Acceptance:* tolerance tests cover case, whitespace, multiple accepted answers,
  absolute and relative numeric tolerance, and partial-credit mapping to `Hard`.
- [ ] **M3-7 Schedule test suite** — state transitions, queue priority, version conflict,
  day boundary, undo fidelity, fuzz determinism.
  *Acceptance:* `go test ./internal/schedule/...` passes with each listed case present
  as its own named test.

### M4 — External integration

- [ ] **M4-1 API key store** — generation (`fcard_` prefix plus base64url), sha256 storage,
  display prefix, scopes, expiry, revoke, `last_used_at`.
  *Acceptance:* test asserts the plaintext is never persisted and that a revoked key
  fails authentication immediately.
- [ ] **M4-2 Bearer authentication middleware** — accepts session cookie or bearer key,
  resolves both to a user, enforces scopes, applies per-key rate limiting, writes audit
  rows with `api_key_id`.
  *Acceptance:* table-driven test covers missing scope, expired key, revoked key, and
  rate-limit exhaustion.
- [ ] **M4-3 REST endpoints** — the `/api/v1` surface in `DESIGN.md` §7.3, including
  `dry_run`, idempotent bulk create by `external_ref`, and the documented error envelope.
  *Acceptance:* a repeated bulk import creates no duplicates and reports
  `created`/`updated`/`skipped` counts correctly.
- [ ] **M4-4 Bulk import internals** — batching (200 rows per transaction), per-row error
  reporting with indices, resumable on failure.
  *Acceptance:* a batch with one invalid row imports the valid rows and reports the index
  of the failing row.
- [ ] **M4-5 Export** — JSON and CSV, optional progress columns, streamed for large decks.
  *Acceptance:* export of 10k notes streams without buffering the whole set in memory
  (asserted by a peak-allocation check or a streaming test double).
- [ ] **M4-6 MCP server** — `modelcontextprotocol/go-sdk` over HTTP at `/mcp`, bearer key
  authentication, tool list filtered by the key's scopes at handshake, scope re-checked
  on every call.
  *Acceptance:* a client with only `read` sees no write tools in `tools/list` and gets a
  permission error when calling one by name.
- [ ] **M4-7 MCP tools** — `list_decks`, `search_notes`, `get_stats`, `export_deck`
  (read); `create_notes`, `update_note`, `delete_note`, `import_deck` (write);
  `get_due_cards`, `submit_review` (review).
  *Acceptance:* each tool has a test calling the same service method as its REST
  counterpart, and both paths produce identical results for the same input.
- [ ] **M4-8 Import JSON Schema** — `schema/note-import.schema.json`, referenced by the
  docs and validated on import.
  *Acceptance:* a note that violates the schema is rejected with the offending field
  named in the error.
- [ ] **M4-9 Error catalogue** — English `code` constants with a localised `message`
  resolved per `Accept-Language` for both REST and MCP.
  *Acceptance:* test asserts the same `code` yields Chinese and English messages for the
  two `Accept-Language` values.

### M5 — Sharing and permissions

- [ ] **M5-1 Grants and role checks** — `deck_grants` store plus one `requireRole` helper
  used by every handler that touches a deck.
  *Acceptance:* test asserts a `reader` cannot modify a note and an `editor` cannot change
  deck settings or grants.
- [ ] **M5-2 Sharing UI** — grant, revoke, and change a role, with the current grant list.
  *Acceptance:* revoking a grant denies the next request from that user in the same test.
- [ ] **M5-3 Share links** — create with optional password and expiry, revoke individually
  or all at once, public read-only view, and a prompt to log in when starting a review.
  *Acceptance:* test asserts a revoked or expired link returns `404` and that the password
  gate rejects a wrong password.
- [ ] **M5-4 Deck clone** — copy notes and cards into the caller's account with no
  progress carried over.
  *Acceptance:* test asserts the clone has the same note count and zero `card_states`
  rows for the new owner.
- [ ] **M5-5 Visibility** — `private`, `unlisted`, `public` with correct listing behaviour.
  *Acceptance:* test asserts `unlisted` decks never appear in any listing but resolve by
  direct id.
- [ ] **M5-6 Deck package export** — the `.fdeck` zip described in `DESIGN.md` §7.6
  (`manifest.json`, `notes.json`, `cards.json`, `preset.json`, optional `progress.json`,
  optional `media/` with `media.json`), exposed through the deck page, `GET
  /api/v1/decks/:id/package`, MCP `export_deck`, and the CLI.
  *Acceptance:* an exported package validates against
  `schema/deck-package.schema.json`; a test with two users on one deck proves the package
  never contains the other user's progress; with `include_media=0` no media entries are
  written and the manifest says so.
- [ ] **M5-7 Deck package import** — upload, API, MCP and CLI entry points; the three
  targets (`new_deck`, `into_deck:<id>`, `replace_deck:<id>`); `dry_run`; conflict policy;
  id remapping; progress rules; media handling; archive safety.
  *Acceptance:* a round trip (export then import into a fresh database) yields identical
  note count, fields, tags, and formulas; importing the same package twice creates no
  duplicate cards; an archive with a path-traversal entry or an oversized decompression is
  rejected; an unknown `kind` fails with the offending entry listed; `progress.json` from
  another user is discarded and reported unless the admin setting enables it.


### M6 — Admin panel and system settings

- [ ] **M6-1 Admin shell** — layout, navigation, and an access guard limited to `role = admin`.
  *Acceptance:* test asserts a non-admin gets `403` on every `/admin/*` route.
- [ ] **M6-2 User management** — list and search, create, disable and enable, reset
  password, change role, force logout, delete, and per-user deck and usage counts.
  *Acceptance:* each action has a test; disable and delete both invalidate sessions.
- [ ] **M6-3 Registration and invites UI** — switch the policy, edit the email-domain
  allowlist, create and revoke invites, and show usage of each invite.
  *Acceptance:* changing the policy takes effect on the next registration attempt without
  a restart.
- [ ] **M6-4 OIDC configuration UI** — enable switch, issuer, client id and secret, claim
  mapping, "test connection" that prints the provider's error text, bound identity list
  with unlink.
  *Acceptance:* a wrong issuer shows the discovery error on the page; a correct
  configuration completes a stub login.
- [ ] **M6-5 System settings UI** — site name, default locale, upload size limit, allowed
  mime list, media directory usage, full-database export button.
  *Acceptance:* each setting is applied without a restart and the page shows whether the
  effective value comes from the environment or the database.
- [ ] **M6-6 Jobs UI** — list jobs with status, stage, log tail, cancel action.
  *Acceptance:* a running job can be cancelled and its status becomes `failed` with a
  cancellation reason.
- [ ] **M6-7 Audit search UI** — filter by user, action, target, and date range.
  *Acceptance:* a query with each filter returns exactly the expected rows in a seeded
  fixture.
- [ ] **M6-8 Health page** — database connectivity and schema version, disk usage of the
  media directory, current due-queue size.
  *Acceptance:* each value matches an independently computed value in the test.
- [ ] **M6-9 API key overview** — every key's name, prefix, scopes, last use, and state;
  plaintext never displayed.
  *Acceptance:* test asserts no handler or template can return a plaintext key value
  after creation.
- [ ] **M6-10 Secret storage** — AES-GCM encryption for OIDC secrets and future provider
  keys, key from the environment, UI shows configured or not configured only.
  *Acceptance:* test asserts a stored secret round-trips and that a wrong master key
  fails decryption loudly instead of returning an empty value.

### M7 — Statistics

- [ ] **M7-1 Statistics queries** — review volume by period, due forecast buckets,
  retention by stability bucket, time spent, per-deck and per-tag breakdowns, and
  `grade_source` distribution.
  *Acceptance:* every number is asserted by an independent SQL computation over a seeded
  fixture.
- [ ] **M7-2 Streak and learning curve** — consecutive review days and daily new-versus-review
  counts.
  *Acceptance:* boundary test at the day cutoff asserts the streak only breaks when a
  whole review day is skipped.
- [ ] **M7-3 Statistics page** — HTML and CSS bars first, self-hosted chart script optional.
  *Acceptance:* the page renders with no external network request; an integration test
  fails if any third-party host appears in the rendered HTML.
- [ ] **M7-4 Retrospective check** — a script or test that recomputes the page numbers
  from raw tables, so drift is caught rather than argued about.
  *Acceptance:* deliberately corrupting one aggregate makes the check fail.

### M8 — Mobile experience, PWA, i18n completion

- [ ] **M8-1 Touch interactions** — swipe to reveal and rate, disabled double-tap zoom and
  long-press selection, tap targets of at least 44 px.
  *Acceptance:* a scripted touch sequence rates a card without triggering a context menu
  (verified with a browser automation check or a documented manual checklist).
- [ ] **M8-2 PWA shell** — manifest, icons, standalone display, service worker caching
  static assets only.
  *Acceptance:* the service worker cache contains no API response; a test asserts the
  cache list contains only static asset paths.
- [ ] **M8-3 i18n completion** — every template string through the translator, key parity
  between catalogs, and a lint that fails on a user-facing literal in templates.
  *Acceptance:* planting a hardcoded Chinese or English string in a template fails CI.
- [ ] **M8-4 Language pack completeness report** — admin view of translation coverage per
  locale.
  *Acceptance:* a catalog with a missing key reports less than 100% and names the key.
- [ ] **M8-5 Accessibility pass** — keyboard-only flow for review and editing, visible
  focus, labels on inputs.
  *Acceptance:* a documented checklist run against the review, deck, and settings pages.
- [ ] **M8-6 Mobile smoke checklist** — a written procedure covering review, editing,
  offline message, and home-screen launch.
  *Acceptance:* the checklist exists, is dated, and each item states the observed result.

### M9 — Parameter optimisation

- [ ] **M9-1 Job runner** — single-flight worker, job store, subprocess launch, timeout,
  kill of the process group, log tail capture.
  *Acceptance:* a second request while a job runs returns `409`; a hung adapter is killed
  at the configured timeout and the job is marked failed.
- [ ] **M9-2 Optimiser adapter** — a small Rust binary around the `fsrs-rs` optimiser with
  a documented contract: reads the standard review-log format, writes the 21-element
  weight array as JSON.
  *Acceptance:* an adapter run on a fixed fixture produces the same weights on two
  consecutive runs.
- [ ] **M9-3 Review-log export** — the standard schema (`card_id`, `review_time` in UTC
  milliseconds, `review_rating` 1–4, `review_state` 0–3, `review_duration`, `timezone`,
  `day_start`) written for the adapter.
  *Acceptance:* test asserts the exported file parses against the upstream documented
  schema.
- [ ] **M9-4 Optimise UI** — button on the preset page, polling status, result summary,
  revert to default weights.
  *Acceptance:* the page shows the same weights the database holds after completion, and
  revert restores `NULL`.
- [ ] **M9-5 Threshold and fit report** — refuse below the configured review count with
  the remaining number shown; report the fit metric before and after.
  *Acceptance:* a preset with too few reviews is refused with the shortfall named; the
  metric is stored on the job row.
- [ ] **M9-6 Optimisation test suite** — single-flight, timeout, kill, weight round-trip,
  threshold, revert.
  *Acceptance:* `go test ./internal/schedule/... -run Optimise` passes with each case
  present.

### M10 — Future work (not part of the current release)

These tasks are recorded so the design keeps room for them. They are excluded from the
completion percentage until they are moved into a release milestone.

- [ ] **M10-1 LLM provider settings** — OpenAI-compatible base URL, model, encrypted key,
  system-wide or per-user (BYOK) credentials, global off switch, monthly call cap.
- [ ] **M10-2 Asynchronous grading path** — a `pending` grading state, queue, retry, and
  page polling, so an LLM call never blocks review submission.
- [ ] **M10-3 `short_answer` grading** — prompt assembly from the card, the user's answer,
  the reference answer, and retrieved snippets; structured result mapped to a 1–4 rating;
  `grade_source = 'llm'` with detail stored.
- [ ] **M10-4 Knowledge base binding** — `reference_refs` on notes, deck-level defaults,
  keyword retrieval first, optional `pgvector` later.
- [ ] **M10-5 Feedback review UI** — grading history per card, self-rating versus LLM
  rating comparison.

### Backlog (no milestone yet)

- [ ] **B-1 Project name and module path** — replace the `<module-path>` placeholder once
  the name is decided (blocked on a decision).
- [ ] **B-2 LICENSE** — pick and add the licence file (blocked on a decision: permissive
  versus copyleft).
- [ ] **B-3 README pair** — `README.md` in English and `README.zh.md` in Chinese, covering
  what it is, screenshots later, self-hosting, backup and restore for both databases, and
  the API key plus MCP quick start.
- [ ] **B-4 CONTRIBUTING.md** — how to build, test, and submit changes, restating the rules
  in section 2.
- [ ] **B-5 Commit author identity** — use a neutral author email for this public
  repository before publishing.
- [ ] **B-6 TOTP two-factor authentication** — optional second factor for local accounts.
- [ ] **B-7 SMTP** — optional mail sending for invites and password reset.
- [ ] **B-8 Per-user media quota** — only if media growth becomes a problem.
- [ ] **B-9 Deployment notes outside the repository** — hosting-specific details stay
  private; only generic container instructions belong in the README.
- [ ] **B-10 English `DESIGN.md`** — the specification is currently Chinese only; for a
  public repository either translate it to English and keep the Chinese version as
  `DESIGN.zh.md`, or state explicitly that Chinese is the primary language of the
  specification (blocked on a decision).

---

## 6. Progress tracking and subagent dispatch

### 6.1 Counting

- Count open and done tasks per milestone with:
  `grep -c '^- \[ \]' AGENTS.md` and `grep -c '^- \[x\]' AGENTS.md`.
- Milestone-level counts: `grep -c '^- \[ \] \*\*M3-' AGENTS.md` (replace the prefix).
- Report progress as one line per milestone, for example
  `M0 0/11 · M1 0/13 · M2 0/9 · M3 0/7 · M4 0/9 · M5 0/7 · M6 0/10 · M7 0/4 · M8 0/6 · M9 0/6
  · M10 0/5 (excluded) · backlog 0/10 (excluded)`.
- Completion percentage covers milestones `M0`–`M9` only. `M10` and the backlog are
  reported separately and never inflate the number.

### 6.2 Marking a task done

- Change `- [ ]` to `- [x]` in the same commit that completes the task.
- A task may be marked done only when the acceptance criteria pass with evidence
  (command output or captured response) and section 4's definition of done holds.
- If a task turns out to be wrong or unnecessary, do not delete it silently: mark it
  done with a note, or add a `B-` task describing what replaced it, and update `DESIGN.md`
  if the change is behavioural.

### 6.3 Dispatching tasks to subagents

- One task per subagent. Pass the task's ID, its full text from this file, and the
  relevant `DESIGN.md` sections as context; subagents do not share this conversation.
- Require the subagent to run the verification commands from section 4 and to report the
  raw output, not a summary claim.
- The parent verifies the reported evidence before marking the checkbox; a subagent's
  self-report is not proof.
- A subagent that discovers missing design information must report it instead of
  inventing behaviour; the parent then records the decision in `DESIGN.md`.
- Keep one commit per task so progress can be audited with `git log`.

### 6.4 Parallel development with git worktrees

A branch alone isolates nothing: `HEAD`, the index, and the working tree are single per
checkout, so two writers in one directory will overwrite each other. Every parallel writer
therefore gets its own worktree. The rules below were verified on git 2.55.

**Layout and naming**

- The main checkout stays on `main` and is used only as the integration point. Do not
  develop in it.
- Worktrees live **outside** the repository directory, for example
  `../flashcard-wt/<task-id>`, so the main checkout's `git status` stays clean.
- Branch: `feat/<task-id>-<slug>` (for example `feat/m3-2-queue-builder`).
  Directory: `flashcard-wt/<task-id>`. Both use the task IDs from section 5.

```bash
git worktree add -b feat/m3-2 ../flashcard-wt/m3-2 main
# ... work and commit inside ../flashcard-wt/m3-2 ...
git worktree remove ../flashcard-wt/m3-2   # or keep the worktree for review
git worktree prune                         # drop stale entries
```

**Working rules**

- One task = one worktree = one commit. The subagent works inside its worktree; the parent
  performs the merge serially (rebase onto `main`, run the section 4 checks, then merge).
  A subagent never merges or pushes.
- Concurrency budget: at most three writers at once, and only when the two tasks touch
  **disjoint file sets**. Parallelism is decided by file overlap, nothing else.
- Single-writer hotspots — one writer at a time, other tasks wait for the next phase:
  `internal/store/models.go`, `internal/config/`, `internal/i18n/locales/`,
  `internal/web/views/`.
- Generated artifacts are per worktree: run `templ generate` and the Tailwind build inside
  each worktree. The Go module cache and build cache are shared and safe for concurrent
  use. Never run repository maintenance commands (`git gc`, `git prune`, `git repack`)
  from a worktree.
- Subagents are session-scoped and are killed when the session ends. Require an early
  commit, even a work-in-progress one, so the work lands in the shared object database
  instead of an orphaned directory.

**Pitfalls (each reproduced on git 2.55)**

1. A branch can be checked out in only one worktree at a time, and `--force` does not
   override this. This is what enforces one task per branch, and it also means two writers
   can never share a branch.
2. Deleting a worktree directory does not remove the worktree. Until `git worktree prune`
   runs, the entry stays listed as `prunable` and the branch still counts as checked out,
   which blocks creating another worktree for that branch.
3. Hooks and scripts exist in a worktree only if they are committed. An untracked
   `.githooks/` does not run and produces no error, so commits can silently lose their
   signature.
4. Repository configuration is shared from the main repository: `user.signingkey` and
   `core.hooksPath` are visible inside every worktree, so signing needs no per-worktree
   setup.

**When to use separate clones instead**

Use `git clone --local` only when a writer needs its own `.git/config`, its own ignore
rules, or the freedom to run `git clean -xdf`. That is not the case in this repository;
worktrees are the default.

**Phase plan — which milestones may run in parallel**

| Phase | Lanes | Constraint |
|---|---|---|
| A | M0 alone | Everything depends on it; run it serially |
| B | M1 (auth) ∥ M2 (store, cardtype, web) | Disjoint packages |
| C | M3 (schedule) ∥ M4 (api, mcp) | Freeze the shared store models; neither lane may change them |
| D | M5 (grants) ∥ M7 (statistics) | Disjoint packages |
| E | M6 (admin views) **or** M8 (mobile, i18n) | Both rewrite `internal/web/views/`; run them in sequence. If they must overlap, split M8 into "gestures and JS" and "view text", and keep the view-text half exclusive with M6 |
| F | M9 last | Needs real review data produced by M3 |
