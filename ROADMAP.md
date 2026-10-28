# ROADMAP.md — Task backlog and release ledger

This file holds the **open** work and the goals completed since the last release: section 1 is
the backlog, section 2 the release ledger. The rules for writing, counting and closing an entry
— the checkbox form, ID retirement, the acceptance evidence, the release cut — live in
`AGENTS.md` section 7; read that first.

---

## 1. Task backlog

### M15 — Deck pause

- [ ] **M15-1 Pause a deck for oneself** — a user pauses a whole deck they can see, so it leaves
  their default study scope without losing any progress.
  *Data:* a new table `deck_pauses` keyed `(deck_id, user_id)` with `paused_at`; a row means the
  user paused the deck, resuming deletes the row. The owner and every member (reader included)
  pause for themselves only; another member's study is never affected. The table is additive
  (AutoMigrate). Deleting the deck deletes its rows (`internal/store/deck.go`), and revoking a
  grant or leaving a share deletes that member's row (`internal/store/grant.go`).
  `decks.archived_at` is not used.
  *Excluded from (only when no deck is named):* the default review queue (`scopeDeckIDs` in
  `internal/schedule/queue.go`, which also serves MCP `get_due_cards`); `due` in the stats summary
  (`API.Stats`, the home "due" number and the start-review button); the due forecast with no deck
  filter (`StatsStore.DueForecast` and its recomputation in `internal/store/retro.go`, which the
  weekly digest reuses); the reminder candidates (`store.ReminderCandidates`); the home deck list;
  the sidebar deck list.
  *Unchanged:* naming a paused deck explicitly (`/review?deck=…`, batch review, a stats deck filter)
  studies or shows it as usual; review history, retention, the deck count and the card counts
  keep every visible deck.
  *Transports:* `PUT /api/v1/decks/:id/paused` with `{"paused": bool}` (scope `review`, at least
  reader, idempotent) and the MCP tool `set_deck_paused` (scope `review`) call one service method,
  which writes the audit actions `deck.pause` / `deck.resume`. `GET /api/v1/decks`, `list_decks` and
  every other deck response gain `paused`. The deck list page shows paused decks with a badge and a
  switch; the deck detail page has the same switch, with a hint that cards keep falling due while the
  deck is paused.
  *Acceptance:* tests show, on SQLite and on PostgreSQL via `TEST_PG_DSN`, that a paused deck
  leaves the default queue, the summary `due`, the unfiltered forecast and the reminder count while
  an explicit deck scope still returns its cards; that one member pausing a shared deck leaves the
  owner's queue unchanged; that pausing twice and resuming twice both succeed; that an invisible
  deck answers `404` and a key without the `review` scope answers `403`, over REST and MCP alike;
  and that deleting the deck or revoking the grant removes the row. Frontend tests cover the
  switch, the badge, and the hidden home and sidebar rows.

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

---

## 2. Release ledger

### 2.1 Completed since v0.2.0 (2026-10-26)

**Everything shipped since v0.2.0 is additive: no route, MCP tool, table, configuration key or
file format was added, removed or renamed.** The v0.2.0 release notes cover everything before it.

- The only payload change is additive: each entry of the session-only
  `GET /api/v1/decks/queue-counts` response gains `studied_count` and `remaining_count`. The public
  deck shape of REST (`GET /api/v1/decks`) and MCP (`list_decks`) is unchanged.

| Goal | What shipped | Breaking change |
|---|---|---|
| Deck progress | The deck list, the home deck list and the deck detail header show how many of the deck's cards the user has studied and how many new cards are left, as `Studied N · Left M`. On the home deck list each deck gets a second line of numbers: today's new and review counts as badges, which turn grey at zero, and a progress bar beside the studied and remaining text, which turns to the success colour once no new card is left. The counts are per user, so a shared deck never shows another member's progress, and they ignore the daily limits. A suspended new card is not counted as left; a suspended studied card still counts as studied. | No — two fields added to a session-only response |
