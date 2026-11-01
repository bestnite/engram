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
- [ ] **M10-8 Note type entry from the note editor** — the note editor builds its form from the deck's
  note types, but the only way to the note type page (`/decks/:id/note-types`) is the "Manage note
  types" button on the deck's Settings tab, so a writer who needs another field or card layout has to
  leave the editor and find it there. The editor (`frontend/src/lib/views/NoteEditView.svelte`) may
  offer a link to that page next to the note type selector, or wherever the selector is hidden
  because the deck has a single type.
  *Acceptance:* from the note editor, one click reaches the deck's note type page, and returning
  lands back on the editor with the draft intact or after an explicit warning that it is discarded.
  *Do not start until the maintainer decides how custom note types are meant to be used, which
  decides whether the entry belongs in the editor at all.*

### M18 — Deck package: `.apkg` becomes the only format

`M18-1` (landed) wrote the `.apkg` export, `M18-2` (landed) the import side and `M18-3` (landed) the
transports; `M18-4` (landed) added the import-time preset choice, `M18-6` adds its page, and `M18-5` removes
`.edeck`. `M18-5` must not start before the others land: a user has to be able to export a deck
before the export they have today disappears. The release that carries `M18-5` breaks the `.edeck`
file format, the package export route and the `export_deck` MCP tool. The parent updates `AGENTS.md`
section 2.7 with the maintainer present (`M18-1` moved `.apkg` export into it and `M18-2` moved
`.apkg` progress import into it); a subagent must not edit that file.

`M18-7` to `M18-16` fix defects that a review of the landed `M18-1` and `M18-2` found. They land
before `M18-5`, because `M18-5` makes `.apkg` the only format. Every one of them that changes
`internal/store/anki_import.go` runs alone: `M18-7`, `M18-8`, `M18-9`, `M18-10`, `M18-11`, `M18-12`,
`M18-14`, `M18-15` and `M18-16`. `M18-13` changes only `internal/anki` and may run beside one of
them. `M18-9` lands before `M18-4`, because both change how an `.apkg` import builds its preset.

`M18-17` to `M18-22` fix defects that a review of the landed `M18-9` (commit `6a7beb8`) found. They
also land before `M18-5`. `M18-17` and `M18-18` change `internal/store/anki_import.go`, so each runs
alone like the entries above. `M18-18` and `M18-19` both change `internal/store/package_import.go`,
so they never run at the same time. `M18-20` changes only tests and the oracle fixtures and lands
after `M18-17`. `M18-21` changes only `README.md` and may run beside any other task. `M18-22` edits
this file, so only the parent does it, with the maintainer present.

`M18-23` to `M18-28` fix defects that a review of the landed `M18-13` (commit `2b1f2e8`) found. They
also land before `M18-5`. `M18-23` and `M18-24` change `internal/store/anki_import.go`, so each runs
alone like the entries above. `M18-24`, `M18-25`, `M18-26` and `M18-27` all change `internal/anki`,
so no two of them run at the same time. `M18-26` waits for a maintainer decision. `M18-28` edits this
file, so only the parent does it, with the maintainer present.

`M18-29` to `M18-35` fix defects that a review of the landed `M18-4` (commits `2c2f15a`, `253a2bb` and
`156065b`) found. `M18-47` to `M18-55` were to land before `M18-5`; `M18-49` and `M18-51` are deferred until Anki's
replay rule is measured (`M18-79`), and the maintainer lets `M18-5` proceed meanwhile, so the removal of
`.edeck` lands with those two defects still open in the Anki importer. `M18-29` changes `internal/store/anki_import.go`, so
it runs alone like the entries above. `M18-29`, `M18-30` and `M18-33` all change
`internal/store/package_import.go`, so no two of them run at the same time. `M18-31` and `M18-33` both
change `internal/mcp/tools.go`, so they never run at the same time. `M18-32` changes only tests and
lands after `M18-30` and `M18-31`, so that it tests their final behaviour. `M18-34` changes only
`README.md` and never runs beside `M18-21`, which edits the same file. `M18-35` edits this file and
`AGENTS.md`, so only the parent does it, with the maintainer present.

`M18-36` to `M18-41` fix defects that a review of the landed `M18-7` (commit `5c18c80`) found. They
also land before `M18-5`. `M18-36`, `M18-37`, `M18-38` and `M18-40` change
`internal/store/anki_import.go`, so each runs alone like the entries above. `M18-37` waits for a
maintainer decision. `M18-39` changes only `internal/store/anki_export.go` and may run beside any
entry that does not touch that file. `M18-41` changes only tests and lands after `M18-36`, which
adds a test in `internal/schedule` beside the one `M18-41` moves there.

`M18-42` to `M18-46` fix defects that a review of the landed `M18-29` to `M18-34` (commits `e229080`,
`51697a1`, `a31c634`, `d3938d5`, `a89a9b1` and `9791bf9`) found. They also land before `M18-5`.
`M18-42` and `M18-44` both change `internal/store/package_import.go`, so they never run at the same
time, and `M18-44` lands after `M18-42`. `M18-43` runs tests and changes no file, so it may run beside
any entry. `M18-45` edits this file, so only the parent does it, with the maintainer present, in the
same change as `M18-35`. `M18-46` changes only `README.md`, lands after `M18-42`, and never runs beside
`M18-21`, which edits the same file.

`M18-47` to `M18-55` fix defects that a review of the landed `M18-43`, `M18-36`, `M18-37`, `M18-10`,
`M18-11` and `M18-12` (commits `fcda88f` to `29b2979`) found; the review found nothing to fix in
`M18-43`. They also land before `M18-5`. `M18-47`, `M18-48`, `M18-49`, `M18-50`, `M18-51` and `M18-54`
change `internal/store/anki_import.go`, so each runs alone like the entries above. `M18-47` and `M18-49`
both change `tools/anki-oracle/generate.py` and the goldens under `internal/notetype/testdata/anki/`, so
they never run at the same time. `M18-48` lands after `M18-47`, whose fixture it uses. `M18-51` waits
for a maintainer decision and lands after `M18-49`. `M18-52` runs tests and changes no file, so it may
run beside any entry. `M18-53` changes only `internal/store/anki_export_test.go` and may run beside any
entry that does not touch that file. `M18-54` lands after `M18-47` to `M18-51`, which change the code it
comments. `M18-55` edits this file, so only the parent does it, with the maintainer present, in the same
change as `M18-22`.

`M18-56` to `M18-64` fix defects that a review of the landed `M18-8`, `M18-14`, `M18-15`, `M18-16`,
`M18-17` and `M18-18` (commits `a0e9326` to `c3888b8`) found. They also land before `M18-5`. `M18-56` and
`M18-57` change `internal/store/anki_import.go`, so each runs alone like the entries above, and `M18-57`
lands after `M18-56`, because both change the same block of `importAnkiProgress`. `M18-58` and `M18-61`
both change `internal/store/package_import.go`, so they never run at the same time. `M18-59` waits for a
maintainer decision. `M18-60` and `M18-61` both change `frontend/src/lib/views/` and the locale catalogs,
so they never run at the same time, and neither runs beside `M18-6`. `M18-62` changes only tests and
lands after `M18-56` and `M18-57`, which change the code it measures. `M18-63` edits this file and
`AGENTS.md`, so only the parent does it, with the maintainer present, in the same change as `M18-22` and
`M18-55`. `M18-64` changes only `README.md`, lands after `M18-21`, `M18-46` and `M18-57`, and never runs
beside `M18-21` or `M18-46`, which edit the same file.

`M18-65` to `M18-72` fix defects that a review of the landed `M18-19`, `M18-23`, `M18-24`, `M18-25`,
`M18-26` and `M18-27` (commits `00de153` to `ed75945`) found. They also land before `M18-5`. `M18-65`,
`M18-68` and `M18-69` change `internal/store/anki_import.go`, so each runs alone like the entries above.
`M18-65` and `M18-68` wait for a maintainer decision. `M18-66`, `M18-68` and `M18-70` all change
`internal/anki`, so no two of them run at the same time, and `M18-70` lands after `M18-66` and `M18-68`,
whose code it comments. `M18-69` and `M18-70` both change `internal/store/package_import.go`, so they
never run at the same time, and neither runs beside `M18-58` or `M18-61`, which change the same file.
`M18-67` and `M18-68` both change `frontend/src/lib/views/` and the locale catalogs, so they never run at
the same time, neither runs beside `M18-6`, `M18-60` or `M18-61`, and `M18-68` lands after `M18-67`,
because both add a row to the same report list. `M18-71` changes only tests and lands before `M18-65`
and `M18-68`, so that their tests use the helper it adds. `M18-72` edits this file, so only the parent
does it, with the maintainer present, in the same change as `M18-22`, `M18-28`, `M18-55` and `M18-63`.

`M18-73` to `M18-77` fix defects that a review of the landed `M18-38`, `M18-39`, `M18-40`, `M18-41`,
`M18-42` and `M18-44` (commits `b072db7` to `1b8a651`) found. They also land before `M18-5`. `M18-73`
changes `internal/store/preset_weights.go` and `internal/store/preset_weights_test.go`, so it never runs
beside `M18-69`, which changes the same file. `M18-74` and `M18-77` change only tests. `M18-75` changes
`internal/store/package_import.go`, so it never runs beside `M18-58`, `M18-61`, `M18-69` or `M18-70`,
which change the same file, and it lands before `M18-46`, whose `README.md` sentence must match the texts
`M18-75` changes. `M18-76` changes `internal/store/anki_import.go`, so it runs alone like the entries
above, and it lands after `M18-49` and `M18-51`, which change the function it reshapes. `M18-77` lands
after `M18-49`, which changes the replay its expected values come from, and never runs beside another
entry that changes `internal/store/anki_progress_import_test.go`.

`M18-87` to `M18-102` fix defects that a review of the commits `f22a626` to `027bd6a` found: the landed
`M18-20`, `M18-21`, `M18-46`, `M18-52` to `M18-55`, `M18-59` to `M18-64`, `M18-67`, `M18-68`, `M18-70`
to `M18-78` and `M18-81`, and the release ledger rows those commits wrote. `M18-87`, `M18-88`, `M18-89`
and `M18-102` edit this file or `AGENTS.md`, so only the parent does them, with the maintainer present,
and `M18-87` and `M18-88` land in one change. `M18-90` changes `README.md`, `internal/mcp/tools.go` and
no other file. `M18-91` changes only `README.zh.md` and lands after `M18-90`, whose `README.md` text it
follows. `M18-92` changes
`internal/store/anki_import.go`, so it runs alone like the entries above; `M18-93` also runs alone when the
maintainer's choice changes that file. `M18-93`, `M18-94` and `M18-95` all change
`internal/store/package_import.go`, so no two of them run at the same time. `M18-93` and `M18-94` both
change `frontend/src/lib/views/ImportView.svelte` and the locale catalogs, so they never run at the same
time, and `M18-94` lands after `M18-93`. `M18-94` and `M18-95` both change `internal/anki`, so they never
run at the same time. `M18-98` changes `frontend/src/lib/views/PresetsView.svelte`, so it never runs beside
`M18-93` or `M18-94`. `M18-98` and `M18-99` both change `internal/store/migrate.go` and
`internal/store/migrate_preset_limits.go`, so they never run at the same time, and `M18-99` lands after
`M18-98`. `M18-96` and `M18-97` change only tests. `M18-100` and `M18-101` both
change `tools/anki-oracle/generate.py` and `tools/anki-oracle/README.md`, so they never run at the same
time. `M18-93` and `M18-99` wait for a maintainer decision.

- [ ] **M18-49 The replay selects the rows Anki selects** — `deriveAnkiMemory` in
  `internal/store/anki_import.go` replays the kept rows from an empty state, except when the first kept
  row is a review row (revlog type 1): then it builds SM-2 from the card's `factor` and `ivl` and
  ignores every row. A row is dropped when it lies at or before the card's last revlog type 4 row,
  whatever its factor. The `memory-replay` golden that settled `M18-37` holds one review-start card with
  a single review row and one reset row with factor 0, so it cannot tell these rules from Anki's on
  longer histories. Measured on `main` with the default weights, on a review card with `ivl` 30, factor
  2500 and no `s`/`d`, with four histories. First, the review-start histories Good, Again (+10 days),
  Again (+11 days) and Good, Easy (+10 days), Easy (+40 days) both store stability 30 and difficulty
  6.093. That is the same as a single Good row and as `MemoryStateFromSM2(2.5, 30, 0.9)`, so the later
  ratings have no effect. Second, a history that begins with a relearning row (type 2), Again then Good
  (+1 day), is replayed from an empty state as a first-ever Again and gives stability 1.887 and
  difficulty 6.402. Third, learning Good, review Good (+3 days) and review Good (+20 days) give stability
  63.20. Inserting a type 4 row with ease 0 and factor 2500 at +4 days drops the first two rows and
  gives the SM-2 state 30; Anki writes a type 4 row for manual rescheduling, and the fixture shows which
  factor it writes for Set due date and which for Forget. Fourth, inserting a type 3 row with ease 1 and
  factor 0 at +4 days into the same history replays it as a real Again and gives stability 10.77; the
  fixture shows which type and factor a review in a filtered deck with rescheduling off writes. The fix
  has two parts. First, `build_memory_fixture` in `tools/anki-oracle/generate.py` adds one card per
  history above. It also adds a card with a learning run, reviews and a second learning run with no
  reset row between them. Anki's own operations produce the Set due date, Forget and filtered-deck rows
  (`set_due_date`, forgetting the card, a filtered deck with rescheduling off); the script does not
  write those rows by hand. `memory-replay.golden.json` records Anki's `compute_memory_state()` for
  each card. Second, the Go code changes how it selects the starting state and the replayed rows until
  every card's stored state equals its golden within 1e-4, the tolerance `TestAnkiProgressReplayStart`
  already uses. The earlier rows' memory written since `M18-10` follows the same selection. The comment
  on `deriveAnkiMemory` states the selection rule and why. *Acceptance:* `TestAnkiProgressReplayStart`
  has one case per new golden card and passes on both drivers; its three existing cases still pass.
  Evidence is the raw scoped `go test` and `go vet` output, the PostgreSQL run with `TEST_PG_DSN` set,
  and the oracle script's output.

- [ ] **M18-51 Undoing an imported review never restores an unusable memory state** — since `M18-10`,
  an imported review row carries stability and difficulty in two cases only: the card's last kept row
  carries the card's state, and an earlier kept row carries `HistoricalMemoryStates` when the importer
  replayed the history. When the package carries a usable `s`/`d`, which every engram export does, or
  when the card takes the SM-2 path, the earlier rows stay NULL. `Rollback` in
  `internal/schedule/actions.go` takes the restored memory from the previous row, so undoing the last
  imported row writes stability 0 and difficulty 0 on a review card, and every later preview and
  submission fails. Reproduced on `main` with `anki.WriteArchive`. A review card with
  `{"s":5,"d":5}` and the rows learning Good and review Good (+3 days) is imported with progress, and
  `Rollback` runs once. The card ends in state `review` with stability 0 and difficulty 0, and
  `Preview` fails with `fsrs: invalid stability: 0 (minimum 0.001 for non-New cards)`. The same happens
  with no `s`/`d` and two review rows, which takes the SM-2 path. With no `s`/`d` and a history that
  begins with a learning row, the restored stability is 2.3065 and `Preview` succeeds. Two fixes are
  possible. (B) `Rollback` refuses with `ErrNothingToUndo`, writing nothing, when the restored state is
  not new and the previous row carries no stability or no difficulty. (A) In addition to (B), every kept
  row carries the memory state after that row under the `M18-49` selection: a replayed row carries the
  replayed state, the row the replay starts from carries the starting state, and the last kept row
  carries the card's state. This holds also when the card's own state comes from `s`/`d`. With (A), (B)
  is left only for rows before a reset and rows of a card whose replay failed. The maintainer has decided
  (A) with (B): the common engram round trip then restores the real state instead of refusing, and (B)
  stays as the guard for the rows (A) cannot fill. *Acceptance:* both reproduced cases end in a state
  that `Preview` accepts, or in a refusal that leaves `card_states`, `reviews` and `audit_log`
  unchanged, as the chosen fix requires; a negative test shows that the refusal deletes no review and
  writes no `review.undo` audit row. Evidence is the raw scoped `go test` and `go vet` output.
  *Do not start until the maintainer chooses between (B) and (A) with (B).*

- [ ] **M18-79 Anki's replay selection rule is measured before the importer copies it** — `M18-49`
  was attempted twice without a result, and its oracle work is kept on an unmerged branch. Measured
  against Anki 26.09.3: a review card with Good, Easy (+40 days), Easy (+60 days) gives stability
  177.09 and difficulty 3.01, while replaying the kept rows gives 222.90 and 1.00; a review card with
  Good, Again (+10 days), Again (+11 days) gives 0.493 and 9.558 against 0.256 and 9.129; a card whose
  history begins with a relearning row gives 1.00 and 8.216 against 1.887 and 6.402. The two histories
  that already had goldens (`partial` 300.0/3.799, `reset` 1.8868/6.402) still match, so the gap is in
  row selection or in how a row becomes a review entry, not in the FSRS parameters. Three selection
  rules were tried and each failed on the same cards: replaying every readable row from an empty
  state; treating only a type 4 row whose factor is 0 as a reset; and ignoring type 3 rows. Truncating
  the history at a learning row was tried as well and broke `partial_review`, which used to pass. The
  fixture the attempt produced is not yet the evidence the entry asks for either: its Set due date,
  Forget and filtered-off cards were built from learning/review histories instead of the single
  review-start history the entry names, so the rows those rows produce cannot be compared with the
  histories the entry specifies. The fix: settle, from Anki's own values for the six named histories,
  which rows are replayed, how `DeltaT` is measured, and what the type 2, 3 and 4 rows mean; only then
  change the importer and `M18-51`, and rebuild the fixture on the histories the entry names.
  *Acceptance:* a note in the committed oracle README states the measured rule with the values that
  establish it, and `M18-49` can then be executed against a fixture whose histories are the ones it
  names. Evidence is the oracle's printed values and the golden.

- [ ] **M18-106 Settle the subday replay DeltaT before closing memory-state alignment** —
  the `subday` card in `internal/notetype/testdata/anki/memory-replay.anki2.apkg` has two Good
  rows: type 0 at Unix milliseconds `1699920000000` and type 1 at `1700002800000`, both on
  2023-11-14 UTC, at 00:00 and 23:00. Anki 26.09.3 `compute_memory_state()` returns stability
  `7.315299987792969` and difficulty `2.1112141609191895`, recorded in the golden. The attempted
  M18-49 implementation used the importing user's UTC timezone and midnight cutoff and returned
  `2.3065` and `2.11121423578539`; that implementation was reverted after the mismatch. The
  observed collection `next_day_at=1793563200` is not evidence of the historical cutoff. Determine
  how Anki constructs DeltaT for these rows and add oracle controls that distinguish elapsed-time
  rounding from review-day boundaries. Keep the `1e-4` tolerance. Update the oracle README with
  the measured rule, then add this case to `TestAnkiProgressReplayStart` and fix the replay.
  This blocks closing M18-49 and M18-79; M18-51 still follows M18-49. *Acceptance:* regenerated
  subday and boundary-control goldens match the imported memory states within `1e-4` on SQLite
  and PostgreSQL; the existing replay cases still pass. Evidence is raw memory-replay-only oracle
  output, scoped `go test ./internal/store/ -run '^TestAnkiProgressReplayStart$' -count=1` output
  on both drivers, and `go vet ./internal/store/` output. Do not run a whole-module gate.

- [ ] **M18-107 Resolve exact-rollover ownership before implementing replay DeltaT** — the
  `memory-replay-controls.golden.json` controls generated by Anki 26.9.3 with timezone
  `Asia/Shanghai` and rollover 04:00 put exactly 04:00:00 on the earlier side: Good review-start
  controls at 03:00:00→04:00:00 have stability 30, while 04:00:00→05:00:00 and
  04:00:00→04:00:01 have stability 32.14255905151367. The proposed
  `floor((local_seconds - 4*3600) / 86400)` gives the opposite ownership at exactly 04:00:00.
  Both independent oracle runs reproduce all 23 controls and leave the 14 original memory-replay
  golden states unchanged. Do not copy that formula into Go unchanged. Obtain a maintainer decision
  on matching the measured boundary, and add millisecond controls through the oracle if the chosen
  rule depends on subsecond precision. This blocks the Go portion of M18-106 and closing M18-49
  and M18-79; M18-51 still follows M18-49. *Acceptance:* the oracle README and Go comments state
  the same measured boundary rule; all original memory-replay and boundary-control imported states
  match Anki within the unchanged `1e-4` tolerance on SQLite and PostgreSQL. Evidence is raw
  memory-replay-only oracle output, targeted store test output on both drivers, and scoped store
  vet output. Never edit golden values by hand or run a whole-module gate for this task.


---

## 2. Release ledger

### 2.1 Completed since v0.2.0 (2026-10-26)

**The M17 content-model merge is a breaking release of the HTTP API, the MCP tools, the database
schema and the `.edeck` file format, which `M18-5` removed when it replaced the format with Anki's
`.apkg`. Everything else listed here is additive except two removals:
the unused `archived_at` deck field and its column, and the `media_allowed_mimes` configuration key
with its environment variable.** The v0.2.0 release
notes cover everything before it.

#### Anki-compatible content model (M17 phases 1–3, breaking)

Note types are now data (`internal/notetype`): fields, templates, CSS and Anki's note type options,
with eight registered answer controls in `internal/answer`. `internal/cardtype` and the
bluemonday-sanitised `internal/render` are gone.

- **HTTP API.** A note is written as `{"note_type": "<public id>", "fields": {"<field name>":
  "<HTML>"}}`; a card-face response carries `front`/`back` objects `{html, css, body_class, side,
  media_base}` plus the template ordinal; note types have list/get/create/update/delete endpoints
  under a deck (update is versioned, `409` on a stale version); the `card-types` catalog was
  removed later by M17-36. New routes: `/card-frame/<version>`, `/edit-frame/<version>`, `/fm/<token>/<name>`.
  New stable codes: `note_type_in_use`, `package_format_obsolete`.
- **MCP.** `list_card_types` is replaced by `list_note_types` (deck-scoped) and `get_note_type`;
  `create_notes` and `update_note` take `note_type`, and field values are HTML.
- **Database schema.** New `note_types`, `media_names` and `media_note_types`; `notes` gains
  `note_type_id` (not null after `0013`, re-tightened by `0016` when an earlier start
  loosened it), `source_url` and `extra_json`, and loses `kind`;
  `notes.fields_json` is a field-id → HTML map; `cards` gains `deck_path`. Migrations `0012` and
  `0013` convert existing notes; `0014` converts stored custom mail templates from Markdown to HTML;
  `0016` re-tightens `notes.note_type_id` on a database where a later start loosened it.
- **File format.** The `.edeck` format this bullet describes is removed by `M18-5`. It was format 2
  only (`note_types.json`, notes by field id, `cards.deck_path`, media names), format 1 was rejected,
  and that was the one-time exception to the N-1 rule in `schema/README.md`. A deck package is now
  an Anki `.apkg`.
- **Rendering and isolation.** Stored HTML is shown only inside the sandboxed card frame and edit
  frame; the server sanitises nothing. The frame renders the Anki reviewer environment (body classes,
  browser classes, `$`/`jQuery`, `pycmd`, MathJax CHTML) and posts answer values back to the page.

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
- One nullable column was added by AutoMigrate, `notes.search_text`, and the versioned migration
  `0011_backfill_note_search_text` fills it for existing notes. It is not part of any payload.
- With no deck named, `GET /api/v1/review/due` and `get_due_cards` leave out the decks the caller
  paused, and so do `due` in the stats summary, the unfiltered due forecast, the weekly digest and the
  reminder mail. Until a user pauses a deck, every result is the same as before.

| Goal | What shipped | Breaking change |
|---|---|---|
| Anki-compatible content model (M17 phases 1–3) | Note types as data with eight registered answer controls; the `{note_type, fields}` REST/MCP note payload, the note type endpoints, the sandboxed card and edit frames, `/fm/` media tokens; migrations 0012–0014; `.edeck` format 2 and the regenerated bundled decks, both removed with the format by `M18-5`. Details above. | Yes — HTTP API, MCP, database schema, file format |
| Media by file name and by any type (M17-22) | Media is classified (image, audio, video, font, script, style, other) instead of allowlisted; upload and import accept every class within the quota. `/fm/<token>/<name>` resolves a deck-scoped name through `media_names`, so CSS-only `_` assets are served. Every media response carries `Content-Security-Policy: sandbox` and everything not image/audio/video is an attachment. Note-type `_`-prefixed references are kept by garbage collection (migration 0015 backfills them) and travel with an export. An import whose name holds different bytes renames the new file as Anki does. A note write turns the editor's `/media/<sha256>` and `[sound:<sha256>]` into deck media names, so the frame, references, garbage collection and export resolve them. | Yes — the `media_allowed_mimes` configuration key and its environment variable are removed; every `/media/` and `/fm/` response gains a sandbox CSP and a download disposition for non-inline classes |
| Note type styles and deck special fields in review (M17-33, M17-44) | Every card frame in the review page and the note preview now takes its HTML, body class and media base from the server's per-side face object, and its style is the engram face sheet followed by the note type's CSS, so a note type rule overrides the default typography. `{{Deck}}` renders the deck name joined with the card's subdeck path (`Lesson 1::Verbs`) and `{{Subdeck}}` its last component; a card outside a subdeck renders the deck name for both. | No — response shapes are unchanged |
| Review of any note type (M17-34, M17-41) | The due-card payload carries `answer_control`, `machine_graded` and `field_order`, and the review page chooses the self-rating, essay or machine-graded flow from them instead of the card-kind catalog, so a note type that matches no stock kind reviews normally. A note type write (REST create/update, package import, stock creation, migration) now validates every template's answer spec: the control must be registered, every required role bound, and every binding must name a declared field; a bad REST write returns 400 naming the template and role, and a bad package is `package_bad_format` with no rows written. | No — three fields added to the due-card response; previously accepted invalid note types are now rejected |
| Anki `.apkg` import (M17-23, M17-24) | `internal/anki` reads all three Anki package layouts (legacy `collection.anki2`, `collection.anki21`, and zstd-compressed `collection.anki21b` with protobuf note type/deck configuration) under its own caps; M17-53 later made those caps the three `package_*` settings and streamed the archive. The import page, `POST` package import, upload tickets and the MCP `import_deck` accept `.apkg`: the top-level package deck becomes one engram deck, subdecks fill `cards.deck_path`, note types arrive unchanged with no answer spec, notes carry `external_ref = "anki:<guid>"`, media is registered by name, and the package's cards are created as they exist. Re-importing into the same deck matches notes by guid and note types by `anki_origin.notetype_id`, keeping cards and progress; an equal field/template structure is overwritten in place, a changed one creates a new note type and moves the notes. The report lists missing media, unplayable video, external scripts/stylesheets and missing LaTeX images. | No — a new input format is accepted and the import report gains four lists |
| Anki import deck choice (M17-47) | An `.apkg` is named after its real deck tree, not Anki's usually empty `Default` deck: only decks that hold package cards take part, the top name is the longest `::` component prefix they share (or the largest branch when they share none), cards in it get no subdeck path, and the package description is adopted only when the deck rules accept it. | No |
| Note field HTML in responses and note type card removal (M17-49, M17-50) | Every note response (REST get/list/search, MCP note tools, due-card fields) returns each field's stored HTML string unchanged instead of reshaping stock-kind fields into arrays, booleans and numbers, so opening and saving a choice note no longer deletes the formatting of its options. A note type update soft-deletes only the cards of templates the new config no longer declares; a declared template whose front is now empty keeps its cards, and a cloze update removes none. | Yes — note response field values are HTML strings for every note type where some stock kinds used to return arrays, booleans or numbers |
| Type-in in the frame (M17-26) | A template with `{{type:F}}` and no answer spec draws `input#typeans` in the frame, in the field's font and size, and posts the typed value on Enter or input; revealing renders the back through Anki's comparison (good/bad/missed) while the rating stays one of the four self-ratings. The render request takes an optional `answer` and the response reports `type_in`. | No — two optional fields on the review render request/response |
| Empty cards survive a note edit (M17-45) | A note write creates the cards the note requires and restores a previously removed one, but soft-deletes nothing: clearing the reverse field or removing a cloze marker keeps the card and its progress. When an existing card's front renders empty, the review response shows a localised notice saying the card is empty and that the Empty Cards cleanup removes it. | No |
| Structural re-import removes stale cards (M17-51) | A re-import that moves a note to a new note type soft-deletes that note's cards whose template the new type no longer declares, keeping their `card_states` and `reviews`; the import report gains `cards_removed`, and a dry run reports the count. | No — one report field is added |
| Image occlusion in review (M17-30) | Ported Anki's image-occlusion review runtime into the card frame: `anki.imageOcclusion.setup()` (and `anki.setupImageCloze`) draws rectangle, ellipse, polygon and text masks on a canvas over the image, supports hide-all-guess-one and hide-one-guess-one (`oi=1`), and wires the toggle-masks button and `M`. | No |
| Deck description as plain text (M17-29, M17-57) | A deck description stays one line of plain text that every client prints as text, never as markup, so no page needs the sandboxed frame for it. Every deck response gains `description_text`, the description on one line, which the deck list and the home deck list print. An Anki package's HTML description is imported as the text a reader saw instead of being dropped when it held a line break. Migration 0018 converts any stored description with markup to that text and drops the `description_markdown` column an unreleased build had added; the header and the create dialog join line breaks into spaces, because the server rejects them. | No — one response field is added; a description that holds a tag or a character reference is rewritten to its text by migration 0018 |
| `.apkg` re-import restores deleted notes (M17-48) | A re-import matches a guid against soft-deleted notes of the target deck, restores the row in the transaction and updates it, so `into_deck` after a delete and `replace_deck` both succeed instead of failing on the unique external reference; the note keeps its id, cards and progress. | No |
| Editor on note types (M17-35, M17-40) | The note editor lists the deck's note types, builds the form from the selected type's fields in config order (field options included) and saves `{note_type, fields}` without a kind; the preview renders the stored type's own templates and CSS and answers 404 for a type of another deck. A note type update regenerates the cards of every live note of the type in one transaction: an added template creates the missing cards, a removed template soft-deletes their cards while `card_states` and `reviews` stay. The edit body takes `dry_run` and `confirm_card_removal`; a removal without confirmation is 409 `card_removal_unconfirmed` with the three counts, a kind change is 400, and a successful update writes one audit row with the counts. | Yes — the note preview request takes `note_type` instead of `kind`; the note type update response gains four fields and a new 409 code |
| Audio, video and TTS playback (M17-25) | The review frame plays the server's AV tag list: the question side autoplays its tags in order, the answer side plays only the tags it adds (audio inserted by `{{FrontSide}}` is not replayed), and both follow the deck's new `autoplay` setting (default on, owner-only). `r` and `F5` replay; an mp4 or webm `[sound:]` plays in an inline video; TTS maps `lang`, `voices`, `speed` and `cloze_blank` onto `speechSynthesis`; `span.engram-sound` start/end/rate/loop reach the player, and the editor's insert-audio dialog sets them. | No — a deck field is added and the card-face payload gains `av_tags` and `autoplay` |
| Note type management (M17-27) | A deck page lists, creates (from a stock definition or by copying) and edits note types: fields with options and Anki-style template-reference rewriting, templates with browser variants, CSS, the answer-spec control and its field bindings, and live sandboxed previews. A save previews the card impact with `dry_run` and asks before removing cards. Stock definitions and answer controls come from two additive read endpoints. | No — a page and two read endpoints are added |
| Deck progress | The deck list, the home deck list and the deck detail header show how many of the deck's cards the user has studied and how many new cards are left, as `Studied N · Left M`. On the home deck list each deck gets a second line of numbers: today's new and review counts as badges, which turn grey at zero, and a progress bar beside the studied and remaining text, which turns to the success colour once no new card is left. The counts are per user, so a shared deck never shows another member's progress, and they ignore the daily limits. A suspended new card is not counted as left; a suspended studied card still counts as studied. | No — two fields added to a session-only response |
| Deck pause | A user pauses a deck for themselves from the deck list (a toggle beside delete or leave) or the deck detail header; the owner and every member, readers included, decide alone. A paused deck keeps a `Paused` badge on the deck list and disappears from the home deck list and the sidebar. It stays out of the default review queue, the home due count, the unfiltered forecast, the weekly digest and the reminder mail, while naming it (`/review?deck=…`, batch review, a stats deck filter) still studies or shows it. Progress is kept and cards keep falling due. Pausing and resuming write the audit actions `deck.pause` and `deck.resume`. | No — additive route, tool, table and field; nothing changes until a user pauses a deck |
| Note search on field text | Note search (`GET /api/v1/decks/:id/notes?q=`, `search_notes` and the card list) matches the text of the fields instead of their JSON encoding. A field key such as `front` no longer matches every note; `<`, `>`, `&`, quotes and text across a line break can be found; case folding of non-ASCII letters is the same on SQLite and PostgreSQL. A query is split on whitespace and every term must occur, in any order. | No — results change, the request and response shapes do not |
| Admin user search wildcards | `_` and `%` in the admin user search match only themselves instead of acting as LIKE wildcards. | No |
| Deck archive removal | The deck archive state is removed: it had no route, tool or page that set it, the sidebar's archived filter could never match, and pausing a deck already covers hiding one. The `archived_at` field leaves every deck response and the column leaves the database through a versioned migration. | Yes — HTTP API and MCP payload (`archived_at` removed) and database schema (column dropped by migration 0010) |
| Package note index on `.apkg` import (M17-52) | The card loop resolves each package note through an index built once before it, so the import no longer spends cards × notes comparisons; the mapping, the card identity and the report counts are unchanged. | No |
| `check-migration` on a database without deck public ids (M17-46) | The pre-flight check probes `decks.public_id` first, so a database created before the column existed gets a report naming each deck by its numeric id instead of an SQL error. | No |
| Empty cards and change note type (M17-28) | A deck's note type page lists the cards whose front renders empty, previews the removal and then soft-deletes them, so the cards leave the queue while their `card_states` and `reviews` stay; the cleanup writes `card.empty_cleanup`. "Change note type" moves every note of one type to another type of the same deck through a field mapping and a template mapping: a mapped card keeps its id and progress and takes the target template's ordinal, an unmapped template's cards are soft-deleted after confirmation, and the operation writes `note_type.change`. Both writes take `dry_run` and `confirm_card_removal` and report the counts, and the list and both writes need the editor role. | No — three routes and two audit actions are added |
| Cloze type-in compares its own deletion (M17-55) | `{{type:cloze:F}}` compares the typed value with the deletion the card shows: the web layer's card ordinal is already the 1-based cloze number, so a `c1` card no longer compares the `c2` deletion. | No |
| Literal field renames (M17-58) | Renaming a field writes the new name verbatim in `{{Old}}`, `{{#Old}}`, `{{/Old}}` and `{{text:Old}}`, so `$&`, `` $` ``, `$'` and `$$` in the new name no longer act as replacement patterns. | No |
| Server-rendered note type preview (M17-56) | The note type editor previews an unsaved config through `POST /api/v1/decks/:id/note-types/preview` (session and CSRF, editor role): the server validates the config with the rule every note type write applies, renders the selected template for both sides and returns the front and back card-face objects with the config's CSS. The second frontend template renderer (`renderPreview`, `resolveConditionals`) is deleted. | No — one route is added |
| Dead shortcode inserter and stale sanitising comments (M17-39) | The shortcode inserter, its source builder, the media picker it alone used and the catalog keys that only they read are deleted. Comments that claimed the server, goldmark or bluemonday sanitise card-face HTML now state that the sandboxed frame and its CSP are the only boundary, and the note type's card ordinal comment states that a cloze card's ordinal is its cloze number minus one. The live audio and speech control layer stays and is no longer described as a shortcode renderer. | No |
| Note type deletion counts soft-deleted notes (M17-43) | A note type used by any note, soft-deleted ones included, is refused with `ErrNoteTypeInUse`, because a soft-deleted note is restorable content and would lose the field and template definitions it relies on. M17-59 adds the confirmed purge path that removes the dead end. | No — a delete that used to succeed is now refused |
| Editing frame without inline script (M17-42) | Both frame documents share one CSP builder. The editing frame's `script-src` names only the origin, because its runtime is a same-origin module and it never typesets MathJax; an inline event handler in a shared deck's field therefore stays inert and cannot post `upload` messages the parent would upload under the editing user's session. The card frame keeps its inline and eval policy for authored scripts and MathJax. | No |
| Only the note types Anki imports (M17-54) | The import drops package note types that no note uses, because Anki 26.09.3's importer creates only the types its notes reference. The oracle gained a fixture that carries an unused type together with the golden names Anki adds for it, and an import test pins that result, so importing a deck no longer leaves an empty `Basic` note type behind. | No — the set of created note types changes; no payload shape changes |
| Purge deleted notes with their note type (M17-59) | A note type used by a live note is still refused with `note_type_in_use`. A type whose notes are all soft-deleted is refused with 409 `note_type_purge_unconfirmed` and the counts `deleted_notes`, `cards` and `reviews`, and `DELETE …?confirm_purge=true` hard-deletes those notes, their cards, `card_states` and `media_notes` rows through the same cascade deck deletion uses, keeping `reviews`. The note type view shows the counts before confirming. | No — a delete that was refused now succeeds after a confirmation; one new 409 code |
| A kind alias keeps the note type (M17-37) | An update with no `note_type` keeps the note's current type when the posted `kind` is empty or names the type's own shape, so a deck holding two note types of one shape no longer moves the note into the other one on an unrelated edit; a `kind` naming a different shape still resolves to the deck's stock type of that shape. | No |
| Streaming large `.apkg` imports (M17-53) | Three settings cap a package — `package_max_bytes` (2 GiB), `package_max_entries` (100 000) and `package_max_unpacked_bytes` (4 GiB), each resolved as environment variable, then settings row, then default. The multipart, URL and upload-ticket transports spool the body to a temporary file removed on every path; the reader opens the zip from that file, the collection entry lands in a temporary SQLite file, and media is hashed once and written to the content-addressed store as a stream, so memory no longer grows with the media. The `.edeck` reader kept its in-memory caps until `M18-5` removed the format and with it that reader. | No — three configuration keys are added; packages far larger than the single-media limit are now accepted |
| Image occlusion editor (M17-31) | The note editor opens an image-occlusion note type on a `fabric` canvas: rectangles, ellipses, polygons and text can be drawn, moved, resized and grouped under a cloze number, both occlusion modes are selectable, and the field is written as Anki's one-deletion-per-shape `{{cN::image-occlusion:…}}` markup. An untouched deletion is written back verbatim, so an unchanged note round-trips byte for byte. | No — a frontend dependency (`fabric`) and a page behaviour |
| Transitional card-kind layer removed (M17-36) | `GET /api/v1/card-types`, the description catalog and every `kind` alias are gone: a note write, the list filter, `DueCard`, `ExportRow`, the MCP tools and the exports speak note types only, and the flat `front_html`, `back_html`, `options_html`, `option_order`, `blanks` and top-level `media_base` fields are replaced by the `front` and `back` face objects. A note write that carries `kind` without `note_type` returns 400 `invalid_request`, the import schema requires `note_type`, and the deck list loses its kind filter and column. | Yes — the `kind` field, the card-types endpoint and the flat face fields are removed from the HTTP API, the MCP tools, the CSV/JSON export and the import schema |
| Ruby shortcode migration (M17-38) | Migration `0017` rewrites `{{ruby:B\|R}}` in every note field as `<ruby>B<rt>R</rt></ruby>`, converting a wrapping `{{tts:…}}` afterwards with the base as its speech text; a marker the old renderer rejected stays literal, `plain_text` fields are untouched, `search_text` is recomputed for changed notes and `updated_at` stays. | No — a versioned migration is added; stored content changes |
| Cards generated on a note type change (M17-60) | Changing a note's type creates the cards the target config generates for it and the note lacks, through the note write's card sync, so a basic note moved to a basic-and-reversed type gains its reverse card. A template-id permutation between two types succeeds through a two-phase write inside the transaction; a soft-deleted row that already holds a mapped target template is still refused. The impact, the 409 body, the audit row and the dialog carry `cards_added`. | No — one response field is added |
| Anki memory state is approximate (M18-37, M18-80, M18-92) | An `.apkg` import derives a missing FSRS memory state from SM-2 only when the card has no kept revlog row or its first kept row is a review row; a history that begins with a relearning row or a filtered-deck row is replayed from an empty state. Neither value is aligned with the state Anki computes for the same history, so an imported card can be scheduled earlier or later than Anki would schedule it. The import writes one warning naming how many cards were approximated whenever a card's first kept row is not a learning row, which is every start that uses SM-2 and every replay from an empty state (M18-80, M18-92). Measuring Anki's own selection rule and replacing the approximation stay in the backlog. | No — the warning is additive; the scheduling difference is a known gap |
| Import media outside the transaction, bounded imports (M17-61) | An `.apkg` import places its new media files at their content address before the database transaction opens, and removes exactly those files when the transaction rolls back, when the run is a dry run, or when placement fails; a file that existed before is never removed. `package_max_concurrent_imports` (environment variable, then settings row, then default 2) bounds simultaneous imports per process; the multipart, URL, upload-ticket and MCP entries take a slot before reading or downloading and answer 429 `deck_import_busy` at once when none is free. The CLI import takes no slot. | No — one configuration key and one 429 code are added; imported media is now written before the transaction |
| Every image-occlusion shape checked against Anki (M17-62) | The oracle builds six image-occlusion notes through Anki's own API — rect, ellipse, polygon, text, two shapes under one cloze number, and inactive masks (`oi=1`, the hide-all guess-one mode) — and records their fields and rendered faces. The editor's shape model rebuilds each field byte for byte, so parsing and saving an untouched note reproduces Anki's stored value exactly. | No |
| Unmapped templates the target declares keep their cards (M17-63) | A template the mapping leaves out keeps its card when the target config declares the same template id: the card keeps its id, template and `card_states` and counts only in `cards_moved`, so a change between two types that share `forward` and `reverse` no longer asks to confirm a removal that removes nothing. An unmapped template the target does not declare is still removed after confirmation. | No |
| Orphan marks cleared for media an import reuses (M17-64) | Before the import transaction opens, `placeAnkiMedia` clears `orphaned_at` on every media row the package will reuse, so a garbage-collection run during a long import cannot delete the row and its file and fail the transaction. A dry run clears nothing, and an import still removes only the files it created. | No |
| Legacy `.apkg` export (M18-1, with M18-11) | A deck export writes an Anki package the reference release imports: legacy schema 11 in `collection.anki21`, a `meta` entry and the media table; one switch each for scheduling, deck configurations, media and FSRS weights; the weights travel in `fsrsParams6`, answer specs in the `engram` note type key, and a card with a last review time carries `lrt`, in seconds, in `cards.data` (M18-11). An export with `IncludeWeights` on fails when the preset's `weights_json` is `[]`, instead of writing an empty weight list (M18-73). The package is byte-for-byte deterministic. | No — a new export format, with the export switches on the HTTP route, the CLI and the MCP tool |
| The `.edeck` deck package format is removed (M18-5, in parts M18-83 to M18-86) | A deck package is an Anki `.apkg` and nothing else: the import readers, the REST and web exports, the CLI, the MCP tools and the export dialog accept and produce `.apkg`; the old format's reader, writer, JSON document, schema files and MCP export tool are gone. A package carries no `external_ref`, `extra` or `source_url`, because Anki drops `notes.data` on import. | Yes — a file format is removed; a `.edeck` file can no longer be imported or exported |
| `.apkg` import of scheduling data (M18-2, with M18-10 to M18-13, M18-36, M18-37, M18-47, M18-48, M18-50, the reader rules of M18-23 to M18-26, M18-14 to M18-18, M18-56 to M18-62, and M18-65 to M18-71) | An `.apkg` import applies the package's deck configuration to the new deck's preset and, when `include_progress` is on and off by default, the package's card states and review log to the importing account. An imported review row's `interval_days` is the revlog `ivl` (a negative `ivl` converted from seconds to days), its `duration_days` counts fractional days since the card's previous kept row, and its stability and difficulty come from the replay (M18-10). A card's last review time comes from `lrt`, then the last kept revlog row, then the due date minus `ivl` (M18-11). An interday learning card's due date counts days from collection creation, a filtered-deck card's due date comes from `odue`, and a learning or relearning due value is read as seconds above one billion and as days otherwise in every queue (M18-12, M18-47); such a card is placed in its home deck by `odid` (M18-48). An unusable `s`/`d` counts as missing (M18-36). The replay drops the rows up to the last reset, a history that begins with a review row takes the SM-2 state (M18-37), and the first learning row after a reset records state_before 0 (M18-50). Since M18-13 the reader tolerates layouts it used to reject with `package_bad_format`: a REAL value in a scheduling column is rounded, an undecodable deck configuration leaves the preset defaults, and an unreadable revlog no longer rejects a package imported with `include_progress` off (with it on, the import fails, as M18-23 requires). With `include_progress` off, `states_discarded` counts only cards with scheduling state and `progress_discarded` is true only when a state or a review was discarded (M18-14). A re-import with the option on keeps the importer's row when its `last_review_at` is not earlier than the package's and writes only the review rows later than the importer's latest review (M18-15). The new preset stores at most 36500 days and keeps the default steps when the package's steps are negative or not finite (M18-17). The report gains `weights_invalid` (M18-18). With `include_progress` on, a revlog row that cannot be read fails the import with `package_bad_format` (M18-23); a skipped card's review rows count in `reviews_discarded` with a warning (M18-65). When the top deck's configuration is not found and a configuration was skipped, the new preset holds the defaults rather than the single remaining one (M18-24), and one malformed `col.dconf` entry drops only that entry (M18-25). A REAL value in `cards.nid`, `cards.did` or `cards.ord` fails the import; in `revlog.cid`, `revlog.ease` or `revlog.type` it makes that revlog row unreadable; in `cards.type`, `cards.queue`, `cards.odid` or `cards.flags` it leaves that card out and counts in the new `cards_skipped` field (M18-26). Since M18-66 a TEXT value in `cards.type`, `cards.queue`, `cards.odid` or `cards.flags` fails the import instead of leaving the card out, and the error for a refused REAL value names its column. With `include_progress` off, `reviews_discarded` also counts the revlog rows the reader could not read, and the report gains `deck_config_skipped` (M18-68). The Import page shows `cards_skipped` (M18-67). | No — a new option and new report fields; imported cards still start as new when the option is off |
| `.apkg` exports on every transport (M18-3) | The deck export route, the web download, the CLI and the MCP export take `format=apkg`, and `create_export_download` issues a single-use download ticket for the same package. The `export_deck` MCP tool this row was written for is removed by `M18-5`, which leaves `create_export_download` as the only MCP export tool. | No — one new format value and one ticket route |
| Imported FSRS weights are opt-in and validated (M18-9) | An `.apkg` import writes the package's FSRS weights only when `apply_weights` is on, the target is `new_deck` and `fsrs.MigrateWeights` accepts them; the report's `weights_applied` and `weights_discarded` now describe an `.apkg` import as well. | No — a new option and report meaning |
| Preset choice on import (M18-4, with M18-29 to M18-33 and M18-42) | The multipart import, the upload ticket and the URL import take `preset_mode` and `preset_id`; the MCP tools `import_deck` and `create_import_upload` take the same two inputs and the CLI takes `--preset-mode` and `--preset-id`. `new` (the default) builds the preset from the package, `reuse` points the new deck at the caller's preset, and `copy` points it at a copy of that preset. The report carries `preset_mode` and `preset_name` for `new_deck` only and omits them for `into_deck` and `replace_deck` (M18-29). A `preset_id` sent with mode `new` or with no mode is refused for `new_deck` (M18-30) and for every target (M18-42); `reuse` and `copy` are refused with any target other than `new_deck`, and an invalid selection on the URL import is refused before the rate limiter and before the download (M18-31). A store failure while resolving `preset_id` answers 500 `internal` (M18-33). Since M18-82 every preset refusal carries the code `preset_selection_invalid` with status 400, so none of them reports `invalid_request`. | No — `preset_mode` and `preset_id` exist in no release yet, and the default mode keeps the earlier behaviour |
| Preset limits enforced on write (M18-17, with M18-60) | `POST` and `PATCH /api/v1/presets` refuse a `maximum_interval_days` above 36500 with 400 `preset_max_interval_invalid`, the preset page refuses a value above 36500 before sending it (M18-60), and an `.apkg` import stores at most 36500 days and keeps the preset's default steps when the package's step list is negative or not finite. | No breaking shape — a request that used to be accepted is now refused with an explicit code |
| `.edeck` weights validated on import (M18-19; the format is removed by M18-5) | An `.edeck` import with `apply_weights` on writes the package's weights only when `fsrs.MigrateWeights` accepts them, sets `weights_applied` only when they were written, `weights_discarded` when a carried set was not written, and `weights_invalid` when the set was refused. `M18-5` removed that import path; an `.apkg` import applies the same validation and reports the same three fields (M18-9). | No — a new option and report fields |
| Preset repair on upgrade (M18-59, with M18-99) | Versioned migration `0020_repair_preset_scheduler_limits` rewrites stored presets on upgrade: a `maximum_interval_days` outside 1 to 36500 becomes 36500, a `desired_retention` outside (0, 1] becomes 0.9, and a learning or relearning step list the scheduler refuses becomes the default steps. It counts each changed row once, leaves every other column intact, and changes nothing on a second run. The rewrite is needed because go-fsrs replaces every supplied parameter with its defaults for a maximum interval above 36500 or a negative step, and the scheduler refuses a non-finite step, so such a preset is unusable as stored. | No — a versioned migration is added; stored preset values change |
| Import page preset choice (M18-6) | The Import page offers `preset_mode` `new`, `reuse` and `copy`, a preset picker bound to `preset_id`, the `include_progress` checkbox, and the report counters the import returns, among them `cards_skipped`, `reviews_discarded` and `deck_config_skipped`. | No — a page behaviour |
