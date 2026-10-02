# ROADMAP.md — Task backlog and progress

This file holds the work: every task with a stable ID, grouped by milestone, plus how progress is
counted and marked. The rules for changing this repository live in `AGENTS.md`; read that first.

A subagent must never edit this file: writing it requires user approval, which a leaf subagent
cannot obtain.

---

## 1. Task backlog


Conventions:

- `- [ ]` open, `- [x]` done. Sections are milestones from `DESIGN.md` §12.
- IDs are stable. Never renumber or reuse an ID; append new tasks at the end of their
  milestone as `M<n>-<next number>`.
- Each task is written to be handed to one subagent without extra context: it names the
  files, the acceptance criteria, and how to verify them.
- A task may be split, but each resulting task keeps a new ID and its own criteria.
- Progress is reported as `M<n>: done/total` (see section 2 of this file).

### M0 — Skeleton

- [x] **M0-1 Module bootstrap** — create `go.mod` (module `example.com/engram`, Go 1.25+),
  `cmd/engram/main.go` with subcommand dispatch (`serve`, `schema sync`, `export`,
  `optimize`, `version`), `LICENSE` placeholder, `.env.example` kept in sync with
  `internal/config`.
  *Acceptance:* `go build ./...` succeeds; `engram version` prints a version string.
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
- [x] **M0-6 Templ toolchain** — `templ generate` wired into the build, `base.templ`
  layout, one sample page, generated files gitignored.
  *Acceptance:* `templ generate && go build ./...` succeeds; the sample page renders.
- [x] **M0-7 Tailwind toolchain** — standalone CLI config plus `input.css`, output at
  `internal/web/static/css/tailwind.css` (gitignored), embedded via `go:embed`.
  *Acceptance:* the build produces a CSS bundle; the sample page uses at least one
  utility class that survives the build.
- [x] **M0-8 i18n loader** — `internal/i18n`: `go-i18n` with `locales/zh-CN.yaml` and
  `locales/en.yaml`, locale detection from the user setting first and `Accept-Language`
  second, translator placed into the request context.
  *Acceptance:* unit test asserts detection order and that a missing key in one catalog
  fails the test build (parity check).
- [x] **M0-9 Static assets embedding** — content-hashed paths for CSS/JS plus self-hosted
  htmx and MathJax; a helper generates the hashed URLs for templates.
  *Acceptance:* page source references hashed paths; changing an asset changes its hash.
- [x] **M0-10 CI pipeline** — workflow running `go build`, `go vet`, `gofmt -l`, `go test`,
  the sanitisation keyword scan, the i18n parity check, and a check that no user-facing
  literal appears in templates.
  *Acceptance:* the pipeline fails on a deliberately planted violation of each check.
- [x] **M0-11 Container build (optional)** — multi-stage `Containerfile` producing a
  single static binary image; no private registry, host names, or deployment specifics.
  *Acceptance:* image builds locally and serves `/healthz`.

- [x] **M0-12 README (English primary, Chinese parallel)** — `AGENTS.md` section 3 lists
  `README.md` and `README.zh.md` in the layout, and `DESIGN.md` §11 requires the backup and restore
  procedure for both databases to be documented there, but neither file exists. An open-source
  repository with no README has no front door: nobody can tell what the service is, how to run it
  locally, or how to deploy and back it up.
  *Acceptance:* both files exist, the Chinese file is a parallel translation rather than a stub,
  each carries a language switch line, every command in them runs as written, and both describe the
  PostgreSQL and SQLite backup and restore paths separately. Placeholders only — no real host,
  domain, or credential.

- [x] **M0-13 A clear failure when the SQLite parent directory is missing** — starting with a
  `DB_DSN` whose parent directory does not exist fails with
  `open database: unable to open database file: out of memory (14)`. Measured, not inferred: the
  message names neither the path nor the directory, and `out of memory` is a SQLite errno artefact
  that sends a first-time deployer looking in the wrong place. The README has to warn about
  `mkdir -p data` precisely because the error does not explain itself.
  *Acceptance:* a start with a missing parent directory either creates it for the SQLite driver or
  fails with an English error that names the path and says the directory is missing. In neither case
  may the message contain `out of memory`. A test covers the chosen behaviour.
### M1 — Identity and users

- [x] **M1-1 User store and password hashing** — `internal/auth`: user CRUD, argon2id
  hashing with parameters recorded per hash, password policy check.
  *Acceptance:* test asserts a correct password verifies, a wrong one does not, and that
  hash parameters are stored in a versioned format.
- [x] **M1-2 Session middleware** — signed HttpOnly cookie sessions, `Secure` and
  `SameSite=Lax`, server-side invalidation on logout, password change, and user disable.
  *Acceptance:* integration test asserts the disabled user's existing session is rejected
  on the next request.
- [x] **M1-3 CSRF middleware** — token issued per session, required on every non-GET
  request via form field or `X-CSRF-Token`.
  *Acceptance:* test asserts a POST without a token returns `403` and with a valid token
  succeeds.
- [x] **M1-4 Register, login, logout pages** — templ pages plus handlers, all strings from
  the catalog.
  *Acceptance:* a new user can register, log in, and log out through the browser flow
  exercised by `httptest`.
- [x] **M1-5 First-admin bootstrap** — `/setup` wizard available only while no admin
  exists; `BOOTSTRAP_ADMIN_EMAIL` honoured as a fallback.
  *Acceptance:* test asserts `/setup` is reachable only before an admin exists and returns
  `404` afterwards.
- [x] **M1-6 Registration policy** — `settings` value `open` | `invite` | `closed` plus an
  optional email-domain allowlist, enforced at registration.
  *Acceptance:* table-driven test covers every policy and the allowlist denial case.
- [x] **M1-7 Invites** — create, list, revoke, accept; one-time token, optional email
  restriction, optional expiry.
  *Acceptance:* test asserts a used, expired, or revoked token is rejected and that an
  accepted invite creates exactly one user.
- [x] **M1-16 TOTP two-factor authentication** — optional second factor for local accounts.
  Moved out of the backlog into the final state at the user's request (2026-10-02).
  *Do not start until the user says so.*
  *Acceptance:* a user with TOTP enabled cannot finish login with only a password; recovery codes are
  single-use; disabling it requires the password.
- [x] **M1-17 SMTP transport and settings** — admin-panel SMTP configuration (host, port, username,
  password through the existing `SecretCodec`, from address, TLS mode) plus a "test connection" that
  shows the server's error text, and an outbox with a background worker that retries with backoff.
  Mail is never sent in the request path, and a send failure must not fail the operation that
  triggered it. When SMTP is not configured, every mail-dependent flow is **disabled and says so**,
  rather than logging quietly.
  *Held: do not start until the user says so.*
  *Acceptance:* with no configuration the invite-mail and reset paths are unavailable and explain why;
  a wrong host surfaces the server error; the password shows as configured or not configured; a
  transient send failure is retried and the last error is visible in the admin panel.
- [x] **M1-18 Email type catalog and per-user preferences** — one catalog, one definition (see
  `DESIGN.md` 4.7), shared by the preferences page and every sender so the two cannot disagree.
  Class A cannot be switched off, class B defaults on, class C defaults off. Per-user preferences need
  storage: decide between a JSON column and a table, remembering that `models.go` is a single-writer
  hotspot.
  *Held: do not start until the user says so.*
  *Acceptance:* a user's choice for an optional type survives a restart; refusing to disable a class A
  type; a type that is off is not sent even when its trigger fires.
- [x] **M1-19 Transactional security mail (class A)** — password reset, email verification and
  email-change confirmation, new-device or new-IP sign-in notice, password or TOTP or recovery-code
  change notice, and account disabled or deleted notice. None of them is opt-out and none carries an
  unsubscribe header.
  *Held: do not start until the user says so.*
  *Acceptance:* each of the five is delivered on its trigger; a reset link works once and expires;
  links carry only digests in the database and never appear in logs.
- [x] **M1-20 Invite delivery by email (class B)** — the admin can have an invite mailed to its
  address instead of copying the link by hand. This is the user's first requested type.
  *Held: do not start until the user says so.*
  *Acceptance:* an invite created for an address is delivered and can be accepted; a revoked or
  expired invite link is refused; the invite still works when copied manually.
- [x] **M1-21 Review reminder with quiet hours and a daily cap (class C)** — event-driven, computed
  from the user's timezone and day cutoff, never sent between 23:00 and 07:00 local, at most one per
  user per day. This is the user's second requested type.
  *Held: do not start until the user says so.*
  *Acceptance:* a reminder that would fall inside the quiet window is held until it opens and then sent
  once; a second reminder the same day is not sent; opting out stops it immediately.
- [x] **M1-22 One-click unsubscribe (RFC 8058)** — `List-Unsubscribe` and `List-Unsubscribe-Post`
  headers on optional types only, backed by a token that needs no login and disables only the type it
  names.
  *Held: do not start until the user says so.*
  *Acceptance:* the one-click POST disables exactly that type; the token cannot be replayed against
  another type or another user; class A mail carries no unsubscribe header.
- [ ] **M1-23 Weekly study summary (class C)** — one message per week per user, in the user's
  timezone, showing reviews done, pass rate, current streak and the new-versus-due trend. Weekly
  rather than daily by default: a daily summary of a small personal collection is mostly noise.
  *Held: do not start until the user says so.*
  *Acceptance:* exactly one send per week per user; switching it off stops it; the numbers match the
  statistics page for the same period.
- [x] **M1-24 Admin notification mail (class D)** — job failure, and media quota or disk warning,
  addressed to the admins. This project has no approval queue and no backup feature, so neither is a
  trigger; the notification types that named them are gone from the catalog.
  *Held: do not start until the user says so.*
  *Acceptance:* each trigger reaches the admin address; a mail failure never breaks the triggering
  operation.
- [x] **M1-15 Rebuild the OIDC client on `zitadel/oidc/v3`** — M1-11 shipped a working, tested
  OIDC client built on the standard library, because that module was never in `go.mod` (nothing
  imported it) and the gap stayed invisible until someone implemented the flow. `DESIGN.md` names
  `github.com/zitadel/oidc/v3` in three places and that choice was deliberate, so the user ruled the
  hand-rolled client a violation of the "prefer a mature library over inventing one" principle and
  wants it rebuilt on the library.
  *Do not start this until the user says so.* The standard-library implementation and its stub
  provider tests stay in place until then, as the acceptance baseline for the rebuild.
  *Acceptance:* the flow runs on `github.com/zitadel/oidc/v3` with the existing stub provider tests
  still passing; no hand-rolled discovery, PKCE, or ID-token verification remains.
- [x] **M1-8 Personal settings page** — locale, timezone, day cutoff, display name,
  password change.
  *Acceptance:* changing the locale switches the returned page language; changing the
  cutoff moves `review_day` boundaries in the next test run.
- [x] **M1-9 Login rate limiting and lockout** — per account and per IP, increasing delay,
  audit entries for failures.
  *Acceptance:* test asserts the delay grows and that a successful login resets it.
- [x] **M1-10 Audit helper** — one function used by every mutation, writing user, optional
  API key, action, target, and JSON detail.
  *Acceptance:* test asserts one row per mutation with the expected action string.
- [x] **M1-11 OIDC login** — configuration read from `settings`, discovery document fetch
  with caching, Authorization Code + PKCE, `state` and `nonce` validation, callback
  handler, and a "test connection" action that surfaces the failure reason.
  *Acceptance:* test against a stub provider asserts a successful login and that a wrong
  `state` is rejected; the test connection returns the provider error text.
- [x] **M1-12 Identity binding** — lookup by `(provider, subject)`, auto-link by verified
  email, policy-gated account creation, and unlink in the admin panel.
  *Acceptance:* table-driven test covers all three branches of `DESIGN.md` §4.5 plus the
  unlink path.
- [x] **M1-14 Wire authentication into the binary** — `cmd/engram/main.go` currently starts the
  web server without constructing the auth services, so `/login`, `/register` and `/setup` are not
  registered at all (verified: a running container answers `GET /login` with `404`). Read
  `SESSION_SECRET`, `ENCRYPTION_KEY` and `BOOTSTRAP_ADMIN_EMAIL` through `internal/config`, build the
  user/session stores and the auth services, pass them into `web.Deps`, and keep `/healthz` working.
  *Acceptance:* a locally started server answers `GET /login` with `200` and renders the catalog text;
  `GET /setup` is reachable while no admin exists and `404` afterwards; `POST /login` with a wrong
  password returns the documented error code; existing tests stay green.
- [x] **M1-13 Auth test suite** — negative cases for CSRF, policy, binding, session
  invalidation, and rate limiting in one place.
  *Acceptance:* `go test ./internal/auth/...` passes with every negative case present.

### M2 — Decks, notes, cards

- [x] **M2-1 Deck store and CRUD** — create, read, update, archive, visibility, owner
  assignment.
  *Acceptance:* test asserts a non-owner cannot modify a deck before grants exist
  (foundation for `M5-1`).
- [x] **M2-2 Preset store and CRUD** — scheduling parameters with documented defaults
  (`desired_retention` 0.90, `learning_steps` `1m,10m`, `relearning_steps` `10m`,
  `enable_fuzz` on).
  *Acceptance:* test asserts defaults are applied and that values round-trip.
- [x] **M2-3 Card type registry** — `internal/cardtype`: `Validate`, `Cards`, `Render`,
  optional `Grade`, optional `PromptContext`, optional `ReferenceRefs`, `Label`; registry
  lookup by `kind`.
  *Acceptance:* test registers a fake type and asserts the core pipeline handles it
  without any change outside the new file.
- [x] **M2-4 Memory types** — `basic`, `basic_both`, `cloze`, `list`, including the cloze
  parser for `{{cN::text}}` and `{{cN::text::hint}}`.
  *Acceptance:* cloze tests cover nested braces, escapes, repeated indices, and a note
  producing two cards from two indices.
- [x] **M2-5 Note and card pipeline** — create a note, generate its cards, enforce
  `(note_id, template)` uniqueness, and support soft delete plus restore.
  *Acceptance:* test asserts updating a note's fields keeps existing cards and their ids.
- [x] **M2-6 Renderer** — goldmark to HTML, bluemonday allowlist, MathJax delimiters
  `\(` … `\)` and `\[` … `\]`, self-hosted MathJax.
  *Acceptance:* test asserts `script`, event attributes, and `javascript:` URLs are
  stripped while tables, code blocks, and inline math survive.
- [x] **M2-7 Card list and editor pages** — paging, search, tag filter, live preview,
  bulk actions.
  *Acceptance:* page renders 100 notes with paging intact; preview updates over htmx
  without a full reload.
- [x] **M2-8 Media storage** — sha256 dedupe, `<sha256[:2]>/<sha256>.<ext>` layout,
  temp-file plus rename writes, `GET /media/:id` proxy with `ETag` and immutable caching,
  mime plus magic-byte validation, admin-configured size limit.
  *Acceptance:* uploading the same file twice stores one blob; an oversized or
  wrong-magic file is rejected with a stable error code.
- [x] **M2-10 Card type labels in the catalogs** — `internal/cardtype` returns translation keys
  It now covers **all ten registered types** (`basic`, `basic_both`, `cloze`, `list`, `typed`,
  `numeric`, `choice_single`, `choice_multi`, `true_false`, `short_answer`), and the editor also
  needs the field labels for the graded types (`note.field.question`, `.answer`, `.accept`,
  `.options`, `.answers`, `.statement`, `.value`, `.unit`, `.tolerance_absolute`,
  `.tolerance_relative`, `.ignore_case`, `.ignore_whitespace`, `.regex`). Add every key to
  `internal/i18n/locales/zh-CN.yaml` and `en.yaml`.
  *Acceptance:* a test enumerating `Registry.Kinds()` fails if any type label is missing from either
  catalog, and the note editor renders a label (not a raw key) for every field of every type.
- [x] **M2-11 Deck list and deck creation UI** — `DESIGN.md` §8.1 lists `/decks` as a page and the
  store layer can create decks, but no route or template exists: a smoke run of the real binary
  answers `GET /decks` with `404`, so a user cannot create a deck in the browser at all. Add the
  deck list page and a creation form (name, description, preset selection), with the usual i18n,
  CSRF and audit wiring, and link it from the home page.
  *Acceptance:* a logged-in user can create a deck in the browser and sees it listed; the new deck
  is selectable when creating notes; an anonymous request is redirected to login.
- [x] **M2-12 Create-note UI** — there is no way to create a note in the browser: the editor only
  edits notes that already exist, so the deck page cannot be used to author cards at all (noted by a
  lane that tried to satisfy M2-11's "the new deck is selectable when creating notes"). Add a create
  form on the deck page: pick a card type, fill its fields (per `DESIGN.md` §6.2), preview through
  `internal/render`, save through `NoteStore`. Card types come from `Registry.Kinds()`.
  *Acceptance:* a logged-in user creates one note per card type in the browser and sees the expected
  number of cards per note; the form rejects an invalid field set with a localised message.
- [x] **M2-9 Media surface in the editor** — upload and insert into a card field.
  *Acceptance:* an uploaded image renders in the preview and survives a page reload.

- [x] **M2-13 Per-user media quota** — alongside the single-file limit, a per-user total cap that
  the admin panel can set. Moved out of the backlog into the final state at the user's request
  (2026-10-02). *Do not start until the user says so.*
  *Acceptance:* exceeding the cap is refused with a stable code and a localised message naming the
  limit; deleting media frees the quota again; the default is off rather than a number nobody chose.

### M3 — Review loop

- [x] **M3-1 FSRS wrapper** — `internal/schedule`: construct the scheduler from preset
  weights or `DefaultWeights()`, preview four ratings, submit one rating.
  *Acceptance:* test asserts `Repeat` returns four options and `Next` advances the card
  state as documented in `DESIGN.md` §3.2.
- [x] **M3-2 Queue builder** — learning cards first, then due reviews ordered by
  retrievability (default) or due date, then new cards limited by the daily cap and
  counted from the `reviews` table.
  *Acceptance:* test asserts ordering and that the daily caps are respected across a
  simulated day boundary at 03:59 and 04:00 local time.
- [x] **M3-3 Review submission** — single transaction writing the state update and the
  review row, with the `expected_version` check returning `409` on mismatch.
  *Acceptance:* test asserts a duplicated submission changes nothing and returns `409`,
  and that a failing transaction leaves no review row.
- [x] **M3-4 Undo, suspend, bury** — rollback via the last review log, suspend a card,
  bury it for the current day.
  *Acceptance:* test asserts Undo restores the previous due date and interval exactly.
- [x] **M3-5 Review page** — templ plus htmx: show answer, rate, keyboard shortcuts
  (`space`, `1`–`4`, `u`, `e`, `s`, `b`), swipe on touch devices, server-side next-card
  prefetch in the same response, remaining counters.
  *Acceptance:* an `httptest` walk of 20 cards completes with no extra round trip per
  rating and shows correct counters.
- [x] **M3-6 Graded types** — `typed`, `numeric`, `choice_single`, `choice_multi`,
  `true_false` with their graders, plus the configurable score-to-rating mapping stored
  in the preset.
  *Acceptance:* tolerance tests cover case, whitespace, multiple accepted answers,
  absolute and relative numeric tolerance, and partial-credit mapping to `Hard`.
- [x] **M3-8 Deck-level daily caps in the schema** — `DESIGN.md` §3.3 defines `new_per_day` and
  `reviews_per_day` as deck settings and §2.2 now lists both columns, but the `decks` model does
  not have them, so the schedule package currently receives them through `QueueOptions` with
  hard-coded defaults. Add the columns (`INTEGER NOT NULL DEFAULT 20` / `200`), expose them in the
  deck store, and make the queue builder read them from the deck. Requires editing
  `internal/store/models.go`, a single-writer hotspot: schedule it with an exclusive owner.
  *Acceptance:* the queue builder honours a deck whose caps differ from the defaults; existing
  schedule tests stay green; both databases migrate without a destructive change.
- [x] **M3-9 Snapshot the learning step before a rating** — Undo currently cannot restore
  learning-step progress because `reviews` stores no pre-rating step snapshot; `Rollback` zeroes
  `step_index`. Add `reviews.step_index_before`, write it in the submit path, and rebuild the
  rollback input from it. Requires editing `internal/store/models.go`, a single-writer hotspot.
  *Acceptance:* a test rates a learning card two steps forward, undoes twice, and asserts
  `step_index` returns to its original value at each step.
- [x] **M3-10 Atomic first submission for a brand-new card** — the row lock does not cover a
  `card_states` row that does not exist yet, so two concurrent first submissions of the same new
  card can both pass the version check on PostgreSQL (SQLite is serialised by its single write
  connection). Replace the read-check-write sequence with a conditional upsert
  (`clause.OnConflict` plus a `version = ?` guard, or an `INSERT ... ON CONFLICT DO UPDATE ...
  WHERE`).
  *Acceptance:* a test issuing two concurrent first submissions against PostgreSQL asserts exactly
  one review row and one state row survive, and the loser receives the conflict sentinel.
- [x] **M3-11 Register `short_answer`** — `DESIGN.md` §6.2 lists ten built-in types but
  `internal/cardtype/builtin.go` registered only nine. Implemented as a self-graded free-text type
  (`prompt` plus optional `reference`), deliberately without a `Grader`, so the review flow falls
  back to the four buttons; the LLM grader stays in §14.
- [x] **M3-12 Wire the graders into the review page** — `internal/web/review.go` had no `Grade` call,
  so the five graded types were unreachable in the UI. The review page now renders a text, numeric
  (text plus `inputmode=decimal`, so a unit suffix still parses), single-choice, multi-choice or
  true/false widget per type, grades through the optional `Grader` interface, maps the score to a
  rating with the preset mapping, and stores `grade_source='typed'` with the score in
  `grade_detail_json`.
- [x] **M3-7 Schedule test suite** — state transitions, queue priority, version conflict,
  day boundary, undo fidelity, fuzz determinism.
  *Acceptance:* `go test ./internal/schedule/...` passes with each listed case present
  as its own named test.

### M4 — External integration

- [x] **M4-1 API key store** — generation (`fcard_` prefix plus base64url), sha256 storage,
  display prefix, scopes, expiry, revoke, `last_used_at`.
  *Acceptance:* test asserts the plaintext is never persisted and that a revoked key
  fails authentication immediately.
- [x] **M4-2 Bearer authentication middleware** — accepts session cookie or bearer key,
  resolves both to a user, enforces scopes, applies per-key rate limiting, writes audit
  rows with `api_key_id`.
  *Acceptance:* table-driven test covers missing scope, expired key, revoked key, and
  rate-limit exhaustion.
- [x] **M4-3 REST endpoints** — the `/api/v1` surface in `DESIGN.md` §7.3, including
  `dry_run`, idempotent bulk create by `external_ref`, and the documented error envelope.
  *Acceptance:* a repeated bulk import creates no duplicates and reports
  `created`/`updated`/`skipped` counts correctly.
- [x] **M4-4 Bulk import internals** — batching (200 rows per transaction), per-row error
  reporting with indices, resumable on failure.
  *Acceptance:* a batch with one invalid row imports the valid rows and reports the index
  of the failing row.
- [x] **M4-5 Export** — JSON and CSV, optional progress columns, streamed for large decks.
  *Acceptance:* export of 10k notes streams without buffering the whole set in memory
  (asserted by a peak-allocation check or a streaming test double).
- [x] **M4-6 MCP server** — `modelcontextprotocol/go-sdk` over HTTP at `/mcp`, bearer key
  authentication, tool list filtered by the key's scopes at handshake, scope re-checked
  on every call.
  *Acceptance:* a client with only `read` sees no write tools in `tools/list` and gets a
  permission error when calling one by name.
- [x] **M4-7 MCP tools** — `list_decks`, `search_notes`, `get_stats`, `export_deck`
  (read); `create_notes`, `update_note`, `delete_note`, `import_deck` (write);
  `get_due_cards`, `submit_review` (review).
  *Acceptance:* each tool has a test calling the same service method as its REST
  counterpart, and both paths produce identical results for the same input.
- [x] **M4-8 Import JSON Schema** — `schema/note-import.schema.json`, referenced by the
  docs and validated on import.
  *Acceptance:* a note that violates the schema is rejected with the offending field
  named in the error.
- [x] **M4-9 Error catalogue** — English `code` constants with a localised `message`
  resolved per `Accept-Language` for both REST and MCP.
  *Acceptance:* test asserts the same `code` yields Chinese and English messages for the
  two `Accept-Language` values.

### M5 — Sharing and permissions

- [x] **M5-1 Grants and role checks** — `deck_grants` store plus one `requireRole` helper
  used by every handler that touches a deck.
  *Acceptance:* test asserts a `reader` cannot modify a note and an `editor` cannot change
  deck settings or grants.
- [x] **M5-2 Sharing UI** — grant, revoke, and change a role, with the current grant list.
  *Acceptance:* revoking a grant denies the next request from that user in the same test.
- [x] **M5-3 Share links** — create with optional password and expiry, revoke individually
  or all at once, public read-only view, and a prompt to log in when starting a review.
  *Acceptance:* test asserts a revoked or expired link returns `404` and that the password
  gate rejects a wrong password.
- [x] **M5-4 Deck clone** — copy notes and cards into the caller's account with no
  progress carried over.
  *Acceptance:* test asserts the clone has the same note count and zero `card_states`
  rows for the new owner.
- [x] **M5-5 Visibility** — `private`, `unlisted`, `public` with correct listing behaviour.
  *Acceptance:* test asserts `unlisted` decks never appear in any listing but resolve by
  direct id.
- [x] **M5-6 Deck package export** — the `.edeck` zip described in `DESIGN.md` §7.6
  (`manifest.json`, `notes.json`, `cards.json`, `preset.json`, optional `progress.json`,
  optional `media/` with `media.json`), exposed through the deck page, `GET
  /api/v1/decks/:id/package`, MCP `export_deck`, and the CLI.
  *Acceptance:* an exported package validates against
  `schema/deck-package.schema.json`; a test with two users on one deck proves the package
  never contains the other user's progress; with `include_media=0` no media entries are
  written and the manifest says so.
- [x] **M5-7 Deck package import** — upload, API, MCP and CLI entry points; the three
  targets (`new_deck`, `into_deck:<id>`, `replace_deck:<id>`); `dry_run`; conflict policy;
  id remapping; progress rules; media handling; archive safety.
  *Acceptance:* a round trip (export then import into a fresh database) yields identical
  note count, fields, tags, and formulas; importing the same package twice creates no
  duplicate cards; an archive with a path-traversal entry or an oversized decompression is
  rejected; an unknown `kind` fails with the offending entry listed; `progress.json` from
  another user is discarded and reported unless the admin setting enables it.
- [x] **M5-8 Deck package entry points beyond REST** — M5-6/M5-7 shipped the store layer and
  `GET /api/v1/decks/:id/package` + `POST /api/v1/decks/import`, but the task text also named the
  deck page, the MCP tools, and the CLI, and none of those exist yet: `export_deck` /
  `import_deck` still go through the older bulk note-creation path, there is no CLI subcommand,
  and there is no upload page. Their absence is invisible today because no UI links to them, so
  it is exactly the kind of gap that only shows up after a release.
  *Acceptance:* an MCP `export_deck` call returns a package and `import_deck` accepts one; a CLI
  subcommand round-trips a file; the web page uploads a package and reports the import summary.
  While doing this, replace the hand-rolled draft-2020-12 subset validator M5-6 wrote with a real
  JSON Schema library if the module proxy is reachable.
- [x] **M5-9 Deck package web page** — M5-8 did the MCP tools and the CLI but deliberately skipped
  the browser path, because the locale catalog was owned by another lane that round. A browser-only
  user still cannot move a deck in or out: the deck page has no export control and there is no
  upload page.
  *Acceptance:* the deck page offers an export control that downloads a `.edeck`; `/import` accepts
  an upload and reports the same summary the REST response returns; a malformed package produces a
  readable error message rather than a 500.
- [x] **M5-10 Deck package and statistics test hardening** — the package work shipped with two
  blind spots it reported about itself: nothing exercises the package export, import, round trip or
  unsafe-archive rejection on PostgreSQL, and media handling has no round-trip test at all (only
  `include_media=0` is asserted) — so the one path that writes files to disk is the least covered.
  The retrospective check has the same gap: it only ever ran on SQLite.
  *Acceptance:* the package and retrospective cases run on PostgreSQL through `internal/pgtest`;
  a package with media imports into a fresh database with the files present and byte-identical,
  with both values of `skip_missing_media` asserted; any real bug the new tests expose is fixed
  rather than encoded into the expectation.
- [x] **M5-11 Keep package media consistent with the import transaction** — the media fix in M5-10
  made metadata and files share the import transaction, but the file writes themselves are not
  transactional: if the transaction fails after `SaveBytes`, the metadata row rolls back while the
  bytes stay on disk, leaving blobs nothing references. Imports that fail repeatedly can quietly
  grow the media directory.
  *Acceptance:* a test forces a failure after the media write and asserts no orphan file remains
  (either the write is deferred until after commit, or the failure path removes what it wrote).


- [x] **M5-12 Require `--user` for CLI import** — the CLI import currently falls back to the
  earliest admin when `--user` is omitted, which can file a deck under somebody else's account. The
  user ruled that identity must be explicit: the package feature exists for sharing between people,
  and guessing the owner is the wrong default. Export keeps the optional flag because it only reads.
  *Acceptance:* `engram import` without `--user` exits non-zero with a message that says how to
  pass it, and no deck is created; with it, the import lands under the named account.

### M6 — Admin panel and system settings

- [x] **M6-1 Admin shell** — layout, navigation, and an access guard limited to `role = admin`.
  *Acceptance:* test asserts a non-admin gets `403` on every `/admin/*` route.
- [x] **M6-2 User management** — list and search, create, disable and enable, reset
  password, change role, force logout, delete, and per-user deck and usage counts.
  *Acceptance:* each action has a test; disable and delete both invalidate sessions.
- [x] **M6-3 Registration and invites UI** — switch the policy, edit the email-domain
  allowlist, create and revoke invites, and show usage of each invite.
  *Acceptance:* changing the policy takes effect on the next registration attempt without
  a restart.
- [x] **M6-4 OIDC configuration UI** — enable switch, issuer, client id and secret, claim
  mapping, "test connection" that prints the provider's error text, bound identity list
  with unlink.
  *Acceptance:* a wrong issuer shows the discovery error on the page; a correct
  configuration completes a stub login.
- [x] **M6-5 System settings UI** — site name, default locale, upload size limit, allowed
  mime list, media directory usage, full-database export button.
  *Acceptance:* each setting is applied without a restart and the page shows whether the
  effective value comes from the environment or the database.
- [x] **M6-6 Jobs UI** — list jobs with status, stage, log tail, cancel action.
  *Acceptance:* a running job can be cancelled and its status becomes `failed` with a
  cancellation reason.
- [x] **M6-7 Audit search UI** — filter by user, action, target, and date range.
  *Acceptance:* a query with each filter returns exactly the expected rows in a seeded
  fixture.
- [x] **M6-8 Health page** — database connectivity and schema version, disk usage of the
  media directory, current due-queue size.
  *Acceptance:* each value matches an independently computed value in the test.
- [x] **M6-9 API key overview** — every key's name, prefix, scopes, last use, and state;
  plaintext never displayed.
  *Acceptance:* test asserts no handler or template can return a plaintext key value
  after creation.
- [x] **M6-10 Secret storage** — AES-GCM encryption for OIDC secrets and future provider
  keys, key from the environment, UI shows configured or not configured only.
  *Acceptance:* test asserts a stored secret round-trips and that a wrong master key
  fails decryption loudly instead of returning an empty value.

### M7 — Statistics

- [x] **M7-1 Statistics queries** — review volume by period, due forecast buckets,
  retention by stability bucket, time spent, per-deck and per-tag breakdowns, and
  `grade_source` distribution.
  *Acceptance:* every number is asserted by an independent SQL computation over a seeded
  fixture.
- [x] **M7-2 Streak and learning curve** — consecutive review days and daily new-versus-review
  counts.
  *Acceptance:* boundary test at the day cutoff asserts the streak only breaks when a
  whole review day is skipped.
- [x] **M7-3 Statistics page** — HTML and CSS bars first, self-hosted chart script optional.
  *Acceptance:* the page renders with no external network request; an integration test
  fails if any third-party host appears in the rendered HTML.
- [x] **M7-4 Retrospective check** — a script or test that recomputes the page numbers
  from raw tables, so drift is caught rather than argued about.
  *Acceptance:* deliberately corrupting one aggregate makes the check fail.

### M8 — Mobile experience, PWA, i18n completion

- [x] **M8-1 Touch interactions** — swipe to reveal and rate, disabled double-tap zoom and
  long-press selection, tap targets of at least 44 px.
  *Acceptance:* a scripted touch sequence rates a card without triggering a context menu
  (verified with a browser automation check or a documented manual checklist).
- [x] **M8-2 PWA shell** — manifest, icons, standalone display, service worker caching
  static assets only.
  *Acceptance:* the service worker cache contains no API response; a test asserts the
  cache list contains only static asset paths.
- [x] **M8-3 i18n completion** — every template string through the translator, key parity
  between catalogs, and a lint that fails on a user-facing literal in templates.
  *Acceptance:* planting a hardcoded Chinese or English string in a template fails CI.
- [x] **M8-4 Language pack completeness report** — admin view of translation coverage per
  locale.
  *Acceptance:* a catalog with a missing key reports less than 100% and names the key.
- [x] **M8-5 Accessibility pass** — keyboard-only flow for review and editing, visible
  focus, labels on inputs.
  *Acceptance:* a documented checklist run against the review, deck, and settings pages.
- [x] **M8-6 Mobile smoke checklist** — a written procedure covering review, editing,
  offline message, and home-screen launch.
  *Acceptance:* the checklist exists, is dated, and each item states the observed result.

### M9 — Parameter optimisation

- [x] **M9-1 Job runner** — single-flight worker, job store, subprocess launch, timeout,
  kill of the process group, log tail capture.
  *Acceptance:* a second request while a job runs returns `409`; a hung adapter is killed
  at the configured timeout and the job is marked failed.
- [x] **M9-2 Optimiser adapter** — a small Rust binary around the `fsrs-rs` optimiser with
  a documented contract: reads the standard review-log format, writes the 21-element
  weight array as JSON.
  *Acceptance:* an adapter run on a fixed fixture produces the same weights on two
  consecutive runs.
- [x] **M9-3 Review-log export** — the standard schema (`card_id`, `review_time` in UTC
  milliseconds, `review_rating` 1–4, `review_state` 0–3, `review_duration`, `timezone`,
  `day_start`) written for the adapter.
  *Acceptance:* test asserts the exported file parses against the upstream documented
  schema.
- [x] **M9-4 Optimise UI** — button on the preset page, polling status, result summary,
  revert to default weights.
  *Acceptance:* the page shows the same weights the database holds after completion, and
  revert restores `NULL`.
- [x] **M9-5 Threshold and fit report** — refuse below the configured review count with
  the remaining number shown; report the fit metric before and after.
  *Acceptance:* a preset with too few reviews is refused with the shortfall named; the
  metric is stored on the job row.
- [x] **M9-6 Optimisation test suite** — single-flight, timeout, kill, weight round-trip,
  threshold, revert.
  *Acceptance:* `go test ./internal/schedule/... -run Optimise` passes with each case
  present.

- [x] **M9-7 Recover stale jobs at startup** — the runner has no recovery for jobs left in
  `running` when the process dies (restart, crash, OOM): they stay `running` forever and keep
  `Enqueue` returning `409`, so the feature looks permanently busy. On startup, mark every
  `running` job as `failed` with a reason such as `interrupted by restart` (the subprocess is gone
  by definition) and keep its log tail for diagnosis.
  *Acceptance:* a test seeds a `running` row, constructs the runner, and asserts the row becomes
  `failed` with the reason recorded and that a new enqueue then succeeds.
  *Verified:* the runner recovers at startup; the WARN log names the count and the reason.

- [x] **M9-8 Recover queued jobs as well** — M9-7 rescues only `running` jobs, but `Store.Active`
  treats `queued` as in-flight too (`status IN ('queued','running')`) while the queue itself lives
  in memory. A crash between insert and start therefore leaves a `queued` row that nothing will
  ever execute, while every later `Enqueue` keeps returning `409` — the same permanent-busy symptom
  M9-7 set out to remove.
  *Acceptance:* a test seeds a `queued` row, runs the startup recovery, and asserts it becomes
  `failed` with a reason saying the job never started, and that a fresh enqueue then succeeds.
- [x] **M9-9 Link the preset page from the navigation** — M9-4 built `/presets` and it works, but
  it was left out of the site navigation to avoid touching a template another lane was editing, so
  the page is reachable only by typing the URL. A feature nobody can find is not finished.
  *Acceptance:* the navigation offers the preset page to every signed-in user, and a test asserts
  the link is present on a rendered page.

- [x] **M9-10 Wire the optimiser adapter into the server** — every M9 part works on its own and
  nothing connects them, so in production the feature always fails. `main.go` builds the runner
  without a `Command`, so `jobs.New` falls back to `DefaultCommandBuilder()`, which re-executes
  `engram optimize --job <id>`; that subcommand is a stub returning `optimize is not implemented
  yet`, so the preset page's button queues a job that can never succeed. `ExportOptimizerLog` (M9-3)
  has no caller either. Wire the chain: export the owner's review log with
  `ReviewStore.ExportOptimizerLog`, run the adapter (`<bin> <log.jsonl> --out <weights.json>`, exit
  code 2 on a usage error) as the job's command, parse the weights it writes, and hand the result to
  `Runner.FinishOptimize` so the page's existing polling sees a real result. Make the adapter path
  configurable with a default beside the server binary, falling back to the repository build path
  `tools/optimizer/target/release/optimizer` that the adapter test uses; a missing or failing adapter
  must fail the job with a message naming the path. The CLI's `optimize` subcommand and its help line
  must stop advertising an unimplemented command: it becomes the real worker entry point or it is
  removed.
  *Acceptance:* an end-to-end test drives a preset with enough reviews through the runner and the
  real adapter, asserting the job ends `succeeded` with 21 weights on the job row and in the preset;
  a missing adapter fails the job naming the path; and no help text advertises a command that reports
  itself unimplemented.

- [ ] **M9-11 Compute the fit metrics in Go** — M9-5 asks the job row to carry a before/after fit
  comparison and the preset page renders it, but nothing computes it: `FitBefore`/`FitAfter` stay
  zero, so `Improved()` (`after < before`) is false on every run and the page tells the user "not
  improved" even when the weights did improve. Compute both metrics in Go from the same review log
  the adapter trained on: replay each card's reviews through `go-fsrs` with the weight set under test
  (`fsrs.NewFSRS`), take the predicted recall probability before each review with
  `(*FSRS).Retrievability`, and compare it with the outcome (a rating above 1 is a recall). LogLoss
  is the mean negative log likelihood, RMSE the root mean squared error, both over the same items;
  a card's first review is skipped because it has no prior memory state. Reuse the scheduling
  package's parameter assembly so the replay matches how the app schedules, and evaluate one item set
  under two weight sets: the preset's weights before the run and the adapter's new weights.
  *Acceptance:* a test drives a fixed review log whose better weight set is known and asserts the
  direction of `Improved()`; both metrics cover the same item count; a card's first review is
  excluded; an end-to-end run through the real adapter leaves both metrics non-zero on the job row;
  and when no item is predictable the page does not claim "not improved".

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
- [ ] **M10-6 Replace the Rust optimiser adapter with the library's pure-Go optimiser** — `go-fsrs`
  is gaining a pure-Go FSRS v6 parameter optimiser: upstream PR #34 (`ComputeParameters()`, all 21
  parameters, no CGO, Adam with cosine annealing), verified 2026-10-02 as an **open draft, not
  merged**. When a **released** `go-fsrs` version carries it, delete `tools/optimizer`, the Rust
  build step, and `OPTIMIZER_PATH`, and call the library in-process from the job runner, keeping the
  job row, the 21-weight result, and the failure messages unchanged.
  *Acceptance:* with no external binary present, a test drives the same review log that
  `internal/jobs/optimizer_wiring_test.go` uses, the job ends `succeeded` with 21 weights written
  back to the preset, and those weights are compared against the Rust adapter's recorded output
  using a tolerance written into the test. The adapter, its build step, and `OPTIMIZER_PATH` are
  gone.
  *Do not start until a released `go-fsrs` version contains the optimiser.*

### Backlog (no milestone yet)

- [ ] **B-1 Module path** — the project name is decided (2026-10-02): **Engram**. The module path
  still needs one fact before it can be replaced: where the repository will be published, because the
  path is `github.com/<account>/engram` or a self-hosted equivalent. Do **not** use a private domain in
  the path — `AGENTS.md` §2.2 forbids real domains in committed files and the sanitiser will catch it.
  Renaming is a single exclusive lane: it rewrites every import, the binary name, the Containerfile,
  CI, scripts and all documentation, so nothing else may be in flight while it runs.
- [x] **B-2 LICENSE** — decided (2026-10-02): **AGPL-3.0**. It matches the requirement that nobody may
  offer this as a *closed-source* SaaS while keeping the project open source. Recorded with the
  caveat that AGPL does not forbid charging: a competitor may still host it commercially, they simply
  cannot keep their changes closed. Replacing the placeholder licence file is the remaining step.
- [x] **B-3 README pair** — `README.md` in English and `README.zh.md` in Chinese, covering
  what it is, screenshots later, self-hosting, backup and restore for both databases, and
  the API key plus MCP quick start. *Done by M0-12; screenshots are still outstanding and are
  tracked there rather than here.*
- [x] **B-4 CONTRIBUTING.md** — how to build, test, and submit changes, restating the rules
  in `AGENTS.md` section 2.
- [x] **B-5 Commit author identity** — decided (2026-10-02): **leave the author email as it is**.
  Rewriting 200+ commits would invalidate every SSH signature, and the user judged that a worse trade
  than the address being visible. The literal address stays out of this file because it is a real
  address on a real domain (`AGENTS.md` §2.2); it is recorded in the local ledger.
- [x] **B-6 TOTP two-factor authentication** — *moved into the final state as M1-16 (2026-10-02).*
- [x] **B-7 SMTP** — *moved into the final state as M1-17 (2026-10-02).*
- [x] **B-8 Per-user media quota** — *moved into the final state as M2-13 (2026-10-02).*
- [x] **B-9 Deployment notes outside the repository** — hosting-specific details stay
  private; only generic container instructions belong in the README. *Satisfied by construction:
  the README documents only generic PostgreSQL deployment, backup and restore, and hosting
  specifics live in private notes rather than the repository.*
- [x] **B-13 CSRF for pre-session forms** — `/setup`, `/login` and `/register` submit without a
  CSRF token because there is no session to bind one to (documented in the handlers). The exposure
  is narrow but real: a setup race on a fresh instance and login-CSRF on an existing one. Fix with
  the double-submit cookie pattern (random token in a cookie, mirrored in the form, compared on
  submit), which needs no session.
  *Acceptance:* a pre-session form submitted without the mirrored cookie is rejected; the normal
  browser flow is unaffected; the existing session-bound CSRF path is unchanged.
- [x] **B-12 Make invite acceptance transactional** — the current flow atomically claims the
  invite token, then creates the user, then releases the token if creation fails. Concurrency is
  safe (one invite yields one user) but a crash between the two steps can leave a token released
  with no user created. `AccountService` and `InviteStore` each hold their own `*gorm.DB`, so the
  fix is a shared transaction boundary; do it when the service layer is next touched.
- [x] **B-11 `Preset.EnableFuzz` to `*bool`** — the model column carries `default:true`, so a
  zero-valued `false` is silently replaced by the database default; `PresetStore.Create`
  currently compensates with an explicit follow-up update. Convert the field (and any other
  boolean with a database default) to `*bool` and delete the compensation. Requires editing
  `internal/store/models.go`, which is a single-writer hotspot: schedule it with an exclusive
  owner.
- [ ] **B-10 English `DESIGN.md`** — the specification is currently Chinese only; for a
  public repository either translate it to English and keep the Chinese version as
  `DESIGN.zh.md`, or state explicitly that Chinese is the primary language of the
  specification (blocked on a decision).

---


---

## 2. Progress tracking

### 2.1 Counting


- Count open and done tasks per milestone with:
  `grep -c '^- \[ \]' AGENTS.md` and `grep -c '^- \[x\]' AGENTS.md`.
- Milestone-level counts: `grep -c '^- \[ \] \*\*M3-' ROADMAP.md` (replace the prefix).
- Report progress as one line per milestone, for example
  `M0 13/13 · M1 23/24 · M2 13/13 · M3 12/12 · M4 9/9 · M5 12/12 · M6 10/10 · M7 4/4 · M8 6/6 · M9 10/11
  · M10 0/6 (excluded) · backlog 6/13 (excluded)`.
- Completion percentage covers milestones `M0`–`M9` only. `M10` and the backlog are
  reported separately and never inflate the number.

### 2.2 Marking a task done

- Change `- [ ]` to `- [x]` in the same commit that completes the task.
- A task may be marked done only when the acceptance criteria pass with evidence
  (command output or captured response) and `AGENTS.md` section 4's definition of done holds.
- If a task turns out to be wrong or unnecessary, do not delete it silently: mark it
  done with a note, or add a `B-` task describing what replaced it, and update `DESIGN.md`
  if the change is behavioural.

