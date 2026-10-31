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
transports; `M18-4` adds the import-time preset choice, `M18-6` its page, and `M18-5` removes
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

- [ ] **M18-4 Preset choice on import: new, reuse or copy** — the import request takes
  `preset_mode` (`new`, `reuse`, `copy`) plus `preset_id` for the last two. `new` builds a preset
  from the package's deck configuration, taking its FSRS weights only when `apply_weights` is on;
  `reuse` points the new deck at the caller's chosen preset and writes nothing to it; `copy`
  duplicates the chosen preset into an independent row, which must never be named `Default` because
  that name identifies the account's own default. `preset_id` must be one of the caller's presets
  (400 otherwise), and `reuse` or `copy` together with `apply_weights` is refused with 400 instead of
  silently ignored, because writing package weights into a shared preset would change the scheduling
  of every other deck that points at it. The report gains `preset_mode` and `preset_name`. The import
  page UI is `M18-6`; this entry is transport and store only. *Acceptance:*
  table tests cover the three modes, a missing `preset_id`, a `preset_id` of another user, and
  `apply_weights` with reuse and with copy; a copy of `Default` gets another name and exactly one
  preset remains the default; the report names the preset used or created. Evidence is the raw test
  output and a captured import response.

- [ ] **M18-5 Remove `.edeck`** — delete the `.edeck` path: `internal/store/package.go`, the `.edeck`
  half of `internal/store/package_import.go`, `internal/store/package_document.go`,
  `schema/deck-package.schema.json`, `schema/examples/deck-package.example.json`, the `.edeck` section
  of `schema/README.md`, the export dialog of `frontend/src/lib/views/DeckDetailView.svelte`, which
  then offers only `.apkg` with the four switches, the `export_deck` MCP tool, the JSON document branch
  of the upload ticket, and the CLI's `.edeck` export. Keep in a package-named file everything the
  `.apkg` path still uses: `PackageError` and its codes, `PackageImportOptions`,
  `PackageImportReport`, `PackageImportError`, the package limits, `MediaQuotaBytes`,
  `uniqueOwnerName`, the archive spool and the `ImportPackage` entry. `README.md` and
  `ARCHITECTURE.md` record the single format and name what it does not carry (the note's
  `external_ref`, `extra` and `source_url`, because Anki drops `notes.data` on import), and the
  release ledger records the breaking removals. *Acceptance:* `git grep -n edeck -- '*.go' '*.ts' '*.svelte'` is
  empty; export and import both go through `.apkg` only; an archive named `.zip` whose entries hold
  an Anki collection is accepted, because the file name never decides the reader; the full-module gate
  passes. Evidence is the grep output, the test output, and a captured export-then-import round trip.

- [ ] **M18-6 Import page: choose how the preset is created** — this is UI work and belongs to the
  designer. The import page (`frontend/src/lib/views/ImportView.svelte`) offers the three
  `preset_mode` values with a preset picker for reuse and copy, and shows the FSRS weight checkbox
  only for new. The picker lists the caller's presets and sends `preset_id`; a refusal (another
  user's preset, or weights with reuse or copy) appears as a localised error next to the control and
  writes nothing. User-facing text comes from the catalog, with identical key sets in
  `frontend/src/lib/i18n/locales/en.ts` and `zh-CN.ts`. The same page gains the `include_progress`
  checkbox, off by default, which no page offers today although every other transport accepts it,
  and the report lists `states_skipped`, `states_discarded`, `reviews_applied`, `reviews_skipped` and
  `reviews_discarded`, which the page does not show today. *Acceptance:* the three modes are
  selectable; reuse and copy reveal the picker and hide the weight checkbox; a refused combination
  shows a localised error and leaves the server write count at zero; the progress checkbox sends
  `include_progress=1` only when ticked; the five counters appear in the report; a captured state of
  each mode is reported. Evidence is the component test output and the captured states.

- [ ] **M18-7 `.apkg` progress import: derive the memory state a package does not carry** — today
  `importAnkiProgress` in `internal/store/anki_import.go` reads stability and difficulty only from
  the `s` and `d` keys of `cards.data`. Anki writes those keys only when FSRS is enabled (a collection
  created with `anki==26.9.3` and reviewed with its defaults writes `{"pos":1,"lrt":…}`), so a learning,
  review or relearning card from such a package is stored with no memory state. go-fsrs rejects a
  non-new card whose stability is below 0.001 (`validateCard`), so the review page's preview and the
  review submission both fail for every such card. The fix: when `s` or `d` is missing on a non-new
  card, compute the state with `fsrs.MemoryState` over that card's kept revlog rows (type 0–3, ease
  1–4, in revlog order; `DeltaT` is the whole number of review days between consecutive rows, using
  the importing user's review-day boundaries), with the weights of the preset the importer studies
  the target deck with. When the card has no kept revlog row, use `MemoryStateFromSM2(factor/1000,
  ivl, 0.9)`. A non-new card for which neither computation succeeds is not written and is counted in
  `states_skipped`, so it stays new for the importer. *Acceptance:* table tests import a review card
  and a learning card with `data` = `{}` and two kept revlog rows each, a review card with `{}`, no
  revlog row and factor 2500, and a learning card with `{}`, no revlog row and factor 0; the first
  three get a stability of at least 0.001 and a difficulty between 1 and 10, the fourth is counted in
  `states_skipped` and has no state row; a test outside the store package calls the scheduler's
  preview on each written state and gets no error. Evidence is the raw scoped `go test` and `go vet`
  output.

- [ ] **M18-8 `.apkg` progress import is idempotent on PostgreSQL** — `importAnkiProgress` decides
  "unchanged" with `reflect.DeepEqual` on two `CardState` values. PostgreSQL returns `time.Time`
  values with a different location than the freshly built row, so a repeated import counts every state
  as changed, bumps `version` and adds to `progress_applied`. With `TEST_PG_DSN` set,
  `TestAnkiProgressImport` and `TestExportAnkiPackageRoundTrip` fail today on their repeat-import
  assertions, although the `M18-2` acceptance required the PostgreSQL cases. The fix compares the
  state columns one by one, time columns with `time.Time.Equal` and nullable columns by presence and
  value. *Acceptance:* both named tests pass on SQLite and with `TEST_PG_DSN` set; a repeated import
  leaves `version` unchanged on both drivers. Evidence is the raw `go test` output of both runs, the
  PostgreSQL one with `TEST_PG_DSN` set.

- [ ] **M18-10 `.apkg` review rows: fill `interval_days`, `duration_days` and the memory state with
  their real meanings** — `M18-2` wrote `revlog.ivl` into `duration_days`, as its entry specified,
  but that mapping was wrong: in engram `duration_days` is the number of days since the card's
  previous review (`reviewFromOutcome` in `internal/schedule/submit.go`), while `revlog.ivl` is the
  interval chosen by the rating, which is `interval_days`. Imported rows also leave `interval_days`,
  `stability` and `difficulty` NULL, so undoing the first review made after an import rebuilds the
  card from a previous row with no memory state (`rebuildReviewLog` in
  `internal/schedule/actions.go`) and restores a stability of 0. The fix: `interval_days` is `ivl`
  when it is positive or zero, and `-ivl / 86400` when it is negative, because a negative `ivl` counts
  seconds; `duration_days` is the fractional number of days between the revlog ids of this row and the
  card's previous kept row, and NULL on the card's first kept row; the card's last kept row carries the
  stability and difficulty written to the card's state; an earlier row carries the state that
  `fsrs.HistoricalMemoryStates` gives for it when `M18-7` replays the history, and NULL otherwise.
  This task needs `M18-7`. *Acceptance:* a table test checks the four columns on a card with a
  learning row (negative `ivl`), a review row and a relearning row; the round trip in
  `TestExportAnkiPackageRoundTrip` also compares `interval_days` and `duration_days`; a test imports a
  review card, submits one review, undoes it, and gets back the imported stability, difficulty and due
  date. Evidence is the raw scoped `go test` and `go vet` output.

- [ ] **M18-11 Last review time survives an `.apkg` round trip** — the importer sets
  `last_review_at` only from the `lrt` key of `cards.data`, and the exporter (`internal/anki/write.go`,
  `internal/store/anki_export.go`) never writes `lrt`, so an engram export imported back has no last
  review time; `TestExportAnkiPackageRoundTrip` does not compare it. go-fsrs counts zero elapsed days
  when the last review time is unset, so the first review after the import is scheduled as a same-day
  repeat and the interval barely grows. The fix: the exporter writes `lrt` in seconds next to `s` and
  `d` for every card with a last review time; when a package carries no `lrt`, the importer uses the
  card's last kept revlog row; when it has neither, a review or relearning card uses its due date
  minus `ivl` days, and a learning card keeps NULL. *Acceptance:* the round trip compares
  `last_review_at`; table tests cover a card with `lrt`, a card with only revlog rows, and a review
  card with neither; `tools/anki-oracle/verify_package.py` still opens the produced package with
  `anki==26.9.3`. Evidence is the raw scoped `go test` and `go vet` output and the script's output.

- [ ] **M18-12 `.apkg` due dates for interday learning and filtered decks** — the importer reads the
  `due` of every learning (type 1) and relearning (type 3) card as Unix seconds. For queue 3, Anki's
  interday learning queue, `due` counts days from collection creation instead: a card reviewed with
  `anki==26.9.3` under the steps `1 10 60 1440 2880` reports `type 1 queue 3 due 1`, which the
  importer stores as a due date in January 1970. A card in a filtered deck (`odid` ≠ 0) keeps its real
  due date in `odue`, which the reader decodes but the importer ignores. The fix: queue 3 uses
  collection creation plus `due` days; a card with `odid` ≠ 0 uses `odue` in place of `due`, with the
  same type and queue rules. The filtered-deck layout is verified against a package that
  `tools/anki-oracle` produces with `anki==26.9.3`, not assumed. *Acceptance:* table tests cover
  queue 1, queue 3 and a filtered-deck card of each type that has a due date; the oracle package
  imports with the due dates Anki shows for it. Evidence is the raw scoped `go test` and `go vet`
  output and the oracle script's output.

- [ ] **M18-13 A scheduling column Anki can hold does not block a content import** — since `M18-2`,
  `internal/anki` reads the whole revlog, every scheduling column of `cards`, and the deck
  configurations on every import, even with `include_progress` off, and scans them into Go integers. A
  single REAL value in one of those columns rejects the whole package: a revlog `ivl` of `7.5` fails
  with `converting driver.Value type float64 ("7.5") to a int`. A package that imported before
  `M18-2` can now fail. The fix: numeric scheduling columns are read so that a REAL value is rounded to
  the nearest integer instead of failing (`CAST(ROUND(x) AS INTEGER)` in the query, or a float scan);
  an undecodable deck configuration falls back to the preset defaults instead of failing the import;
  an unreadable revlog fails the import only when `include_progress` is on. Files:
  `internal/anki/scheduling.go`, `internal/anki/sqlite.go`. *Acceptance:* table tests cover a REAL
  value in revlog `ivl`, in `cards.due` and in `cards.factor`, a `col.dconf` that is not valid JSON,
  and a malformed `deck_config.config`; each one imports its content with `include_progress` off.
  Evidence is the raw scoped `go test` and `go vet` output.

- [ ] **M18-14 `.apkg` progress report counts and transport tests** — with `include_progress` off,
  `importAnkiProgress` sets `states_discarded` to the number of all package cards and
  `progress_discarded` to true for every package that has a card, including a package whose cards are
  all new and carry no progress. The fix: `states_discarded` counts only the cards whose type or queue
  is not 0, and `progress_discarded` is true only when `states_discarded` or `reviews_discarded` is
  greater than 0. `M18-2` added `include_progress` to the multipart route, the URL import, the upload
  ticket, the MCP `import_deck` and `create_import_upload` tools and the CLI without a test for any of
  them. *Acceptance:* a table test shows an all-new package reporting no discarded progress and a
  package with one review card reporting one discarded state; a handler test for each HTTP transport
  and each MCP tool shows that `include_progress` reaches the import and that leaving it out writes no
  `card_states` row; a negative test shows that a user with the reader role on the target deck gets
  403 on `into_deck` with `include_progress` on. Evidence is the raw scoped `go test` and `go vet`
  output.

- [ ] **M18-15 `.apkg` re-import with progress must not overwrite newer progress** — a repeated
  import with `include_progress` on overwrites the importer's `card_states` row with the package's
  state even when the importer has reviewed the card in engram since the package was made, and
  appends the package's review rows after the importer's newer rows, so an undo takes an old imported
  row as the latest grade (`Rollback` in `internal/schedule/actions.go` orders by `id`). *Do not start
  until the maintainer decides the rule: which side wins when both have progress for one card, and
  whether imported review rows older than the importer's latest review are written at all.*
  *Acceptance:* table tests cover the chosen rule for a card the importer reviewed after the export
  and for a card the importer never reviewed; an undo after such an import removes the importer's own
  latest review. Evidence is the raw scoped `go test` and `go vet` output.

- [ ] **M18-16 `.apkg` progress import without a query per row** — `importAnkiProgress` loads each
  existing card state with its own query and checks each review row for a duplicate with its own
  `COUNT` query, all inside the import transaction, and the card loop looks up each skipped note's
  card with its own query; a package with 100 000 revlog rows therefore runs more than 100 000
  queries in one transaction. The fix loads the existing states, the existing
  `(card_id, reviewed_at, rating)` keys and the skipped notes' cards with one query per batch of at
  most 500 ids, and inserts review rows in batches. The results and the report counts stay the same.
  *Acceptance:* the existing progress tests pass unchanged on SQLite and with `TEST_PG_DSN` set; a test
  imports a package with 5 000 revlog rows and asserts, through a GORM callback that counts statements,
  that the progress step runs fewer than 100 statements. Evidence is the raw `go test` output of both
  runs.

- [ ] **M18-17 `.apkg` deck configuration values the scheduler cannot use** — `M18-9` checks the
  package's FSRS weights, but `createAnkiPreset` in `internal/store/anki_import.go` still copies the
  learning steps, the relearning steps and the maximum interval without checking them against what
  the scheduler accepts. Two failures follow. First: a schema-18 package stores the steps as protobuf
  floats (`internal/anki/protobuf.go`), which can be NaN or infinite; `ankiSteps` formats such a step
  as `NaNm` or `+Infm`, `parseSteps` in `internal/schedule/scheduler.go` refuses that string, and
  `schedule.NewScheduler` returns an error for the deck's preset, so every preview and every review
  submission in the deck fails — the failure `M18-9` removed for weights. Second: a maximum interval
  above 36 500 days, or a negative step, passes `parseSteps`, but go-fsrs v4.0.0 `NewFSRS` then sees
  `Parameters.Validate` fail and replaces every parameter with `fsrs.DefaultParam()`. The deck is
  then scheduled with the default weights, retention, steps and maximum interval, while the report
  says `weights_applied` and the preset page shows the imported values. The preset write path has the
  second gap as well: `validatePresetForWrite` in `internal/store/preset.go` and `parsePresetRequest`
  in `internal/web/presets_api.go` accept every maximum interval above 0. The fix:
  `validatePresetForWrite` refuses a maximum interval above 36 500 with `ErrInvalidMaximumInterval`;
  `createAnkiPreset` stores 36 500 when the package's maximum interval is above 36 500; and
  `createAnkiPreset` keeps the preset's default learning steps when the package's learning step list
  holds a step that is NaN, infinite or negative, and applies the same rule to the relearning steps.
  *Acceptance:* table tests in `internal/store` call `createAnkiPreset` with a maximum interval of
  50 000, a learning step of −1, a NaN learning step and an infinite relearning step, and check the
  stored preset; a test in `internal/schedule` imports, through `ImportPackage`, legacy packages
  carrying the maximum interval of 50 000 and the step of −1, builds the scheduler from each resulting
  preset, and asserts that its go-fsrs parameters hold the preset's weights, retention, steps and
  maximum interval, which shows that `NewFSRS` did not reset them; a handler test in `internal/web`
  shows that creating a preset and updating a preset with `maximum_interval_days` 36 501 each return
  400 `preset_max_interval_invalid`. Evidence is the raw scoped `go test` and `go vet` output.

- [ ] **M18-18 `.apkg` import says why requested weights were not used** — with `apply_weights` on
  and the target `new_deck`, `createAnkiPreset` drops weights that `fsrs.MigrateWeights` refuses and
  throws the error away. The report then carries `weights_applied` false and `weights_discarded`
  true, which is the same pair as with `apply_weights` off, and no log line records the refusal. A
  user who ticked the checkbox reads "Package weights not used" and cannot tell that the package's
  weights were faulty. The fix: `PackageImportReport` in `internal/store/package_import.go` gains
  `WeightsInvalid` (JSON `weights_invalid`), which is true only when all four of these hold:
  `apply_weights` is on, the target is `new_deck`, the package carries weights, and
  `fsrs.MigrateWeights` refuses them. The importer writes one English `slog.Warn` line carrying the
  importing user's id and the `MigrateWeights` error. `frontend/src/lib/api/types.ts` gains the
  field, and `frontend/src/lib/views/ImportView.svelte` shows one catalog line when it is true; the
  key `package.report.weights_invalid` is appended at the end of
  `frontend/src/lib/i18n/locales/en.ts` and `zh-CN.ts`, and the two key sets stay identical. This
  entry changes `frontend/src/lib/views/`, so it never runs beside `M18-6`. *Acceptance:* the weights
  table test reports `weights_invalid` true for the refused set, and false for the option off, the
  accepted set, the package without weights and the existing-deck target; a component test shows the
  line when `weights_invalid` is true and hides it when it is false. Evidence is the raw scoped
  `go test` and `go vet` output and the component test output.

- [ ] **M18-19 `.edeck` import checks package weights the way `.apkg` does** —
  `createPresetFromPackage` in `internal/store/package_import.go` writes the `weights` of
  `preset.json` into the new preset whenever `apply_weights` is on and the list is not empty, and
  `importInTx` sets `weights_applied` from the option and the target alone. Nothing checks the list
  at import time: `schema/deck-package.schema.json` declares 21 values, but no import path validates a
  package against the schema. An `.edeck` whose `weights` is `[1,2,3]`, imported with
  `apply_weights` on, therefore yields a preset for which `schedule.NewScheduler` returns an error, so
  every preview and every review submission in the deck fails while the report says
  `weights_applied` — the defect `M18-9` fixed for `.apkg` only. The fix: the weights,
  `weights_review_count` and `weights_optimized_at` are written only when `fsrs.MigrateWeights`
  accepts the list; `weights_applied` is true only when they were written; `weights_discarded` is
  true when the package carried weights that were not written; once `M18-18` has landed,
  `weights_invalid` follows the rule that entry defines. If `M18-5` lands first, this entry is
  deleted with a commit message that names `M18-5`. *Acceptance:* the table of
  `TestImportPackageApplyWeights` in `internal/store/package_weights_test.go` gains a `[1,2,3]` case
  with `apply_weights` on, which stores no weights and no weight metadata and reports
  `weights_applied` false and `weights_discarded` true; every existing case keeps its result.
  Evidence is the raw scoped `go test` and `go vet` output.

- [ ] **M18-20 Weight import tests: older Anki weight sets, schema-18 packages and the scheduler's
  use of the weights** — `TestAnkiImportWeightsSchedule` in `internal/api/anki_weights_import_test.go`
  imports one 21-value set and one 3-value set, both through a schema-11 package written by
  `anki.WriteArchive`. Three inputs are not covered: a 17-value set under the `fsrsWeights` key and a
  19-value set under the `fsrsParams5` key of `col.dconf`, which older Anki releases write and which
  `fsrs.MigrateWeights` converts (`anki.WriteArchive` writes only `fsrsParams6`, so the test writes
  those keys itself); a schema-18 (`collection.anki21b`) package, whose weights
  `internal/anki/protobuf.go` decodes from protobuf floats; and a weight that is NaN or infinite,
  which only the protobuf path can carry. The test also does not show that the scheduler uses the
  adopted weights: it checks only that `Preview` returns no error, and that holds for the default
  weights as well. The file lives in `internal/api` although it calls no handler. The fix: move the
  test to `internal/schedule`, which can import `internal/store` and `internal/anki` without a cycle;
  add the 17-value and 19-value cases; assert that the scheduler built from each imported preset holds
  the result of `fsrs.MigrateWeights` on the package's set when the set was adopted, and the default
  weights when it was not; add a schema-18 package that `tools/anki-oracle` produces with
  `anki==26.9.3` and that carries FSRS parameters; cover the non-finite weight with a test in
  `internal/store` that calls `createAnkiPreset` directly. *Acceptance:* each case named above passes;
  `internal/api/anki_weights_import_test.go` no longer exists. Evidence is the raw scoped `go test`
  and `go vet` output and the output of the oracle script that produced the fixture.

- [ ] **M18-21 README describes what an `.apkg` import carries** — the "Anki `.apkg` packages"
  section of `README.md` lists "Scheduling state and review history" and "Anki's deck options and
  FSRS parameters" under the parts that are not imported, and says that a new deck starts from the
  default preset. The code does otherwise: `createAnkiPreset` builds the new deck's preset from the
  package's deck configuration; `importAnkiProgress` applies the card states and the review log when
  `include_progress` is on; and since `M18-9` the package's FSRS weights are adopted when
  `apply_weights` is on, the target is `new_deck` and `fsrs.MigrateWeights` accepts them. The
  paragraph just above that section already describes the deck configuration and `include_progress`,
  so the README contradicts itself. The fix rewrites the two list items to state the current
  behaviour, names `apply_weights` (off by default, `new_deck` only) and the report's
  `weights_applied` and `weights_discarded`, and leaves no statement that disagrees with the paragraph
  above. `M18-5` rewrites the format paragraphs later; this entry removes the contradiction now.
  *Acceptance:* the section no longer lists scheduling state, review history, deck options or FSRS
  parameters as parts that are not imported, and each of its statements matches `createAnkiPreset`,
  `importAnkiProgress` and `PackageImportOptions` in `internal/store`. Evidence is the `README.md`
  diff.

- [ ] **M18-22 Release ledger records the landed `M18` work** — the release ledger in section 2 of
  this file holds no row for `M18-1`, `M18-2`, `M18-3` or `M18-9`, although all four landed after
  v0.2.0 and change what an import or export does through the HTTP API, the MCP tools and the CLI.
  For `M18-9` the row states that an `.apkg` import writes the package's FSRS weights only when
  `apply_weights` is on, the target is `new_deck` and `fsrs.MigrateWeights` accepts them, and that
  the report's `weights_applied` and `weights_discarded` now describe an `.apkg` import too. Each row
  is written from the commits whose messages name that ID, and marks a breaking change as section 7
  of `AGENTS.md` requires. Only the parent edits this file, with the maintainer present.
  *Acceptance:* the ledger table holds a row naming each of the four IDs. Evidence is the output of
  `grep -n -E 'M18-(1|2|3|9)\b' ROADMAP.md` limited to the ledger section.

---

## 2. Release ledger

### 2.1 Completed since v0.2.0 (2026-10-26)

**The M17 content-model merge is a breaking release of the HTTP API, the MCP tools, the database
schema and the `.edeck` file format. Everything else listed here is additive except two removals:
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
- **File format.** `.edeck` is format 2 only (`note_types.json`, notes by field id, `cards.deck_path`,
  media names); format 1 is rejected. This is the one-time exception to the N-1 rule in
  `schema/README.md`.
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
| Anki-compatible content model (M17 phases 1–3) | Note types as data with eight registered answer controls; the `{note_type, fields}` REST/MCP note payload, the note type endpoints, the sandboxed card and edit frames, `/fm/` media tokens; migrations 0012–0014; `.edeck` format 2 and the regenerated bundled decks. Details above. | Yes — HTTP API, MCP, database schema, file format |
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
| Streaming large `.apkg` imports (M17-53) | Three settings cap a package — `package_max_bytes` (2 GiB), `package_max_entries` (100 000) and `package_max_unpacked_bytes` (4 GiB), each resolved as environment variable, then settings row, then default. The multipart, URL and upload-ticket transports spool the body to a temporary file removed on every path; the reader opens the zip from that file, the collection entry lands in a temporary SQLite file, and media is hashed once and written to the content-addressed store as a stream, so memory no longer grows with the media. The `.edeck` reader keeps its in-memory caps. | No — three configuration keys are added; packages far larger than the single-media limit are now accepted |
| Image occlusion editor (M17-31) | The note editor opens an image-occlusion note type on a `fabric` canvas: rectangles, ellipses, polygons and text can be drawn, moved, resized and grouped under a cloze number, both occlusion modes are selectable, and the field is written as Anki's one-deletion-per-shape `{{cN::image-occlusion:…}}` markup. An untouched deletion is written back verbatim, so an unchanged note round-trips byte for byte. | No — a frontend dependency (`fabric`) and a page behaviour |
| Transitional card-kind layer removed (M17-36) | `GET /api/v1/card-types`, the description catalog and every `kind` alias are gone: a note write, the list filter, `DueCard`, `ExportRow`, the MCP tools and the exports speak note types only, and the flat `front_html`, `back_html`, `options_html`, `option_order`, `blanks` and top-level `media_base` fields are replaced by the `front` and `back` face objects. A note write that carries `kind` without `note_type` returns 400 `invalid_request`, the import schema requires `note_type`, and the deck list loses its kind filter and column. | Yes — the `kind` field, the card-types endpoint and the flat face fields are removed from the HTTP API, the MCP tools, the CSV/JSON export and the import schema |
| Ruby shortcode migration (M17-38) | Migration `0017` rewrites `{{ruby:B\|R}}` in every note field as `<ruby>B<rt>R</rt></ruby>`, converting a wrapping `{{tts:…}}` afterwards with the base as its speech text; a marker the old renderer rejected stays literal, `plain_text` fields are untouched, `search_text` is recomputed for changed notes and `updated_at` stays. | No — a versioned migration is added; stored content changes |
| Cards generated on a note type change (M17-60) | Changing a note's type creates the cards the target config generates for it and the note lacks, through the note write's card sync, so a basic note moved to a basic-and-reversed type gains its reverse card. A template-id permutation between two types succeeds through a two-phase write inside the transaction; a soft-deleted row that already holds a mapped target template is still refused. The impact, the 409 body, the audit row and the dialog carry `cards_added`. | No — one response field is added |
| Import media outside the transaction, bounded imports (M17-61) | An `.apkg` import places its new media files at their content address before the database transaction opens, and removes exactly those files when the transaction rolls back, when the run is a dry run, or when placement fails; a file that existed before is never removed. `package_max_concurrent_imports` (environment variable, then settings row, then default 2) bounds simultaneous imports per process; the multipart, URL, upload-ticket and MCP entries take a slot before reading or downloading and answer 429 `deck_import_busy` at once when none is free. The CLI import takes no slot. | No — one configuration key and one 429 code are added; imported media is now written before the transaction |
| Every image-occlusion shape checked against Anki (M17-62) | The oracle builds six image-occlusion notes through Anki's own API — rect, ellipse, polygon, text, two shapes under one cloze number, and inactive masks (`oi=1`, the hide-all guess-one mode) — and records their fields and rendered faces. The editor's shape model rebuilds each field byte for byte, so parsing and saving an untouched note reproduces Anki's stored value exactly. | No |
| Unmapped templates the target declares keep their cards (M17-63) | A template the mapping leaves out keeps its card when the target config declares the same template id: the card keeps its id, template and `card_states` and counts only in `cards_moved`, so a change between two types that share `forward` and `reverse` no longer asks to confirm a removal that removes nothing. An unmapped template the target does not declare is still removed after confirmation. | No |
| Orphan marks cleared for media an import reuses (M17-64) | Before the import transaction opens, `placeAnkiMedia` clears `orphaned_at` on every media row the package will reuse, so a garbage-collection run during a long import cannot delete the row and its file and fail the transaction. A dry run clears nothing, and an import still removes only the files it created. | No |
