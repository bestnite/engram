# ROADMAP.md — Task backlog and release ledger

This file holds the **open** work and the goals completed since the last release: section 1 is
the backlog, section 2 the release ledger. The rules for writing, counting and closing an entry
— the checkbox form, ID retirement, the acceptance evidence, the release cut — live in
`AGENTS.md` section 7; read that first.

---

## 1. Task backlog

### M10 — Future work (not part of the current release)

These tasks are recorded so the design keeps room for them. They are not part of the current
release; a task moves into a release milestone when it is scheduled.

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
- [ ] **M10-7 Multiple OIDC providers** — configure more than one provider (say a company IdP
  next to a personal one) and list them all on the login page. The schema is already ready:
  `identities` is keyed `(provider, subject)` and the provider is recorded on every binding.
  What blocks it is the singular configuration: the settings keys are `oidc.issuer` /
  `oidc.client_id` / …, `auth.LoadOIDCConfig` returns one config, the admin page is a single
  form, and the callback path is fixed at `/auth/oidc/callback`. Deciding the callback shape
  comes first: either per-provider paths (`/auth/oidc/<slug>/callback`) or one callback that
  resolves the provider from the `state` row.
  *Acceptance:* two providers configured in the admin panel; the login page offers both; an
  identity bound through one is stored with that provider and does not affect the other;
  unlinking one leaves the other intact; a single-provider setup keeps working, its existing
  settings read as the first row.
  *Do not start until the callback shape is decided.*

### M16 — Search correctness

- [ ] **M16-1 Note search over a derived `search_text` column** — note search today runs
  `LOWER(notes.fields_json) LIKE '%q%'` on the JSON encoding, so it matches field keys (`front`
  hits every basic note), cannot match `<`, `>` or `&` (`json.Marshal` stores them as `\u003c`,
  `\u003e`, `\u0026`), cannot match across a newline or a quote (stored as `\n`, `\"`), and lowers
  non-ASCII letters on PostgreSQL but not on SQLite. Add a nullable `notes.search_text` TEXT column
  (`json:"-"`, not in any payload) holding the note's string field values, collected recursively
  through maps and arrays in sorted key order, joined with `\n` and lowered in Go with
  `strings.ToLower`. Numbers and booleans are left out. `prepareNoteFields` in
  `internal/store/note.go` computes it, so every write path (`Save`, import, clone) keeps it current.
  Versioned migration `0011_backfill_note_search_text` fills it for existing rows, soft-deleted
  ones included, in primary-key batches. `NoteStore.List` splits the query on whitespace and
  requires every term: one `notes.search_text LIKE ? ESCAPE '\'` per term, joined with AND. Order
  stays `created_at DESC, id DESC`. Files: `internal/store/models.go`, `internal/store/note.go`,
  `internal/store/migrate.go`, new tests in `internal/store/`.
  *Acceptance:* a table-driven test in `internal/store` creates notes through `NoteStore.Create`
  and shows that `front` and `back` hit nothing, while `a<b`, `AT&T`, a quoted phrase, a
  two-line value, `ÉTÉ` against `été`, a CJK substring, and a two-term query whose terms are not
  adjacent each hit the expected note; `%` and `_` match literally. A migration test writes rows
  with an empty `search_text`, runs 0011, and the same queries then hit. The `internal/store`
  tests pass on SQLite and, with `TEST_PG_DSN` set, on PostgreSQL.
- [ ] **M16-2 Escape wildcards in the admin user search** — `UserStore.ListForAdmin` in
  `internal/store/user_admin.go` builds `'%' + query + '%'` without escaping, so `_` matches any
  character and `%` matches anything. Escape the query with `escapeLike`, add `ESCAPE '\'` to the
  three conditions, and lower the query in Go as note search does.
  *Acceptance:* a table-driven test in `internal/store` shows that `_` and `%` match only users
  whose username, email or display name contains that character; a plain substring still matches
  case-insensitively; an empty query returns every user.

---

## 2. Release ledger

### 2.1 Completed since v0.2.0 (2026-10-26)

**Everything shipped since v0.2.0 is additive except one removal: the unused `archived_at` deck field
and its column. Nothing was renamed, and no configuration key or file format changed.** The v0.2.0
release notes cover everything before it.

- One REST route was added, `PUT /api/v1/decks/:id/paused` (scope `review`), and one MCP tool,
  `set_deck_paused` (scope `review`).
- One table was added by AutoMigrate, `deck_pauses`; no versioned migration was needed.
- Payload changes are additive: every deck response (`GET /api/v1/decks`, `list_decks` and the other
  deck-returning endpoints) gains `paused`, and each entry of the session-only
  `GET /api/v1/decks/queue-counts` response gains `studied_count` and `remaining_count`.
- Every deck response (`GET /api/v1/decks`, `list_decks` and the other deck-returning endpoints)
  drops `archived_at`, and the versioned migration `0010_drop_deck_archived_at` drops the
  `decks.archived_at` column. No route, tool or page ever set it, so the field was always absent
  from responses (`omitempty`) unless a row had been edited by hand.
- With no deck named, `GET /api/v1/review/due` and `get_due_cards` leave out the decks the caller
  paused, and so do `due` in the stats summary, the unfiltered due forecast, the weekly digest and the
  reminder mail. Until a user pauses a deck, every result is the same as before.

| Goal | What shipped | Breaking change |
|---|---|---|
| Deck progress | The deck list, the home deck list and the deck detail header show how many of the deck's cards the user has studied and how many new cards are left, as `Studied N · Left M`. On the home deck list each deck gets a second line of numbers: today's new and review counts as badges, which turn grey at zero, and a progress bar beside the studied and remaining text, which turns to the success colour once no new card is left. The counts are per user, so a shared deck never shows another member's progress, and they ignore the daily limits. A suspended new card is not counted as left; a suspended studied card still counts as studied. | No — two fields added to a session-only response |
| Deck pause | A user pauses a deck for themselves from the deck list (a toggle beside delete or leave) or the deck detail header; the owner and every member, readers included, decide alone. A paused deck keeps a `Paused` badge on the deck list and disappears from the home deck list and the sidebar. It stays out of the default review queue, the home due count, the unfiltered forecast, the weekly digest and the reminder mail, while naming it (`/review?deck=…`, batch review, a stats deck filter) still studies or shows it. Progress is kept and cards keep falling due. Pausing and resuming write the audit actions `deck.pause` and `deck.resume`. | No — additive route, tool, table and field; nothing changes until a user pauses a deck |
| Deck archive removal | The deck archive state is removed: it had no route, tool or page that set it, the sidebar's archived filter could never match, and pausing a deck already covers hiding one. The `archived_at` field leaves every deck response and the column leaves the database through a versioned migration. | Yes — HTTP API and MCP payload (`archived_at` removed) and database schema (column dropped by migration 0010) |
