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
`156065b`) found. They also land before `M18-5`. `M18-29` changes `internal/store/anki_import.go`, so
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

- [ ] **M18-28 The release ledger row for `M18-2` records what `M18-13` changed** — `M18-22` writes
  each ledger row from the commits whose messages name that row's ID, and the `M18-13` commit
  (`2b1f2e8`) names only `M18-13`. The `M18-2` row would therefore leave out a change visible through
  the HTTP API, the MCP tools and the CLI: since `M18-13`, an `.apkg` import accepts packages it
  rejected with `package_bad_format` before. A REAL value in a scheduling column is rounded, an
  undecodable deck configuration leaves the preset defaults, and an unreadable revlog no longer rejects
  a package imported with `include_progress` off. The fix: when `M18-22` writes the `M18-2` row, that
  row also states these three points, names `M18-13`, and names each of `M18-23` to `M18-26` that has
  landed by then. Only the parent edits this file, with the maintainer present. *Acceptance:* the ledger row that
  names `M18-2` also names `M18-13`. Evidence is the output of `grep -n 'M18-13' ROADMAP.md` limited to
  the ledger section.

- [ ] **M18-35 Release ledger and `AGENTS.md` record `M18-4`** — the commit that closed `M18-4`
  (`5ff830d`) removed the backlog entry but added no ledger row, and `M18-22` lists only `M18-1`,
  `M18-2`, `M18-3` and `M18-9`. `M18-4` changes the HTTP API (the multipart import and the upload
  ticket gain `preset_mode` and `preset_id`, and so does the URL import once `M18-31` lands; the import
  report gains `preset_mode` and `preset_name`), the MCP tools (`import_deck` and
  `create_import_upload` gain the same two inputs) and the CLI (`--preset-mode`, `--preset-id`). Every
  change is additive: the default mode `new` keeps the earlier behaviour. `AGENTS.md` section 2.7,
  under "Importing an `.apkg`", still says "the package's deck configuration becomes the new deck's
  preset"; since `M18-4` that holds only for `preset_mode` `new`. The fix: the ledger gains one `M18-4`
  row that states the points above, is marked not breaking, and names each of `M18-29` to `M18-31`
  that has landed by then; the sentence in `AGENTS.md` section 2.7 names the three modes. Only the
  parent edits these two files, with the maintainer present. *Acceptance:* the ledger table holds a row
  naming `M18-4`; the sentence in `AGENTS.md` section 2.7 names `reuse` and `copy`. Evidence is the
  output of `grep -n 'M18-4\b' ROADMAP.md` limited to the ledger section and the `AGENTS.md` diff.

- [ ] **M18-45 The `M18-4` ledger row states what `M18-29` to `M18-33` changed** — `M18-35` was
  written before `M18-29` to `M18-33` landed. It describes the import report as gaining
  `preset_mode` and `preset_name`, it names only `M18-29` to `M18-31`, and it names no refusal. Since
  then four behaviours have changed. First, the report carries both fields only for `new_deck` and omits
  them for `into_deck` and `replace_deck` (`M18-29`). Second, a `preset_id` sent with `preset_mode`
  `new` or with no `preset_mode` is refused with 400 `invalid_request` for `new_deck` (`M18-30`), and
  for every target once `M18-42` lands. Third, `POST /api/v1/decks/import-url` takes `preset_mode` and
  `preset_id` and refuses an invalid selection before the rate limiter and before the download
  (`M18-31`). Fourth, a store failure while resolving `preset_id` answers 500 `internal` instead of 400
  `invalid_request` (`M18-33`). None of the four is breaking, because `preset_mode` and `preset_id`
  exist in no release yet. The fix: the `M18-4` ledger row that `M18-35` adds states each of the four
  points and names `M18-29`, `M18-30`, `M18-31` and `M18-33`, and also `M18-42` if it has landed by
  then. Only the parent edits this file, with the maintainer present, in the same change as `M18-35`.
  *Acceptance:* the ledger row that names `M18-4` also names `M18-29`, `M18-30`, `M18-31` and `M18-33`.
  Evidence is the output of `grep -n -E 'M18-(29|30|31|33)\b' ROADMAP.md` limited to the ledger
  section.

- [ ] **M18-46 README says where the preset choice is taken and for which target** — the sentence that
  `M18-34` wrote on line 47 of `README.md` describes `preset_mode`, `preset_id` and `apply_weights`
  without saying which import entries take the first two, while line 43 names the Import page first.
  The Import page sends neither `preset_mode` nor `preset_id`: `importDeckPackage` and
  `importDeckPackageURL` in `frontend/src/lib/api/client.ts` do not send them, and `M18-6` adds them.
  The CLI takes them as `--preset-mode` and `--preset-id`. The sentence also omits two refusals that
  `ValidateImportPreset` makes: `reuse` and `copy` are refused with a target other than `new_deck`, and
  a `preset_id` is refused unless the mode is `reuse` or `copy` (for `new_deck` today, and for every
  target once `M18-42` lands). The fix rewrites that sentence only. It names the entries that take the
  choice: `POST /api/v1/decks/import`, `POST /api/v1/decks/import-url`, `POST /api/v1/decks/import-uploads`,
  the CLI flags and the `import_deck` and `create_import_upload` MCP tools. It states that the choice
  applies to `new_deck` only, and it states both refusals. This entry lands after `M18-42` and never
  runs beside `M18-21`, which edits the same file. The commit that lands `M18-6` adds the Import page
  to the sentence. *Acceptance:* each statement of the rewritten sentence matches `ValidateImportPreset`
  in `internal/store/package_import.go`, the flags in `cmd/engram/package.go`, the two tool inputs in
  `internal/mcp/tools.go` and the two request functions in `frontend/src/lib/api/client.ts`. Evidence
  is the `README.md` diff.

- [ ] **M18-48 A card in a filtered deck is placed in its home deck** — `planAnkiDecks` and the card
  loop (`plan.Paths[card.DeckID]`) in `internal/store/anki_import.go` place a card by `did`. For a card
  in a filtered deck, `did` names the filtered deck. Since `M18-12` the importer reads such a card's due
  date from `odue`, because a filtered deck is temporary, but the card's deck still comes from `did`.
  Reproduced on `main`: the stock fixture's card, moved to a deck named `Filtered Deck 1` (id 999) with
  `odid` naming its home deck `Default`, is imported as a new deck named `Filtered Deck 1`. The filtered
  deck also supplies the deck description, and `ankiTopConfig` looks up the filtered deck's
  configuration instead of the home deck's. engram has no filtered decks. The fix: wherever the importer
  uses a card's deck, a card with `odid` ≠ 0 uses `odid`. That covers the card counts and the shared
  prefix in `planAnkiDecks`, the card's `deck_path`, and the deck whose configuration `ankiTopConfig`
  reads. A deck that then holds no card takes no part, as today. The golden that `M18-47` adds records
  the deck Anki assigns each filtered card after import, and it shows that Anki keeps such a card in
  the filtered deck, which engram cannot represent; the maintainer therefore decided that a card with
  `odid` ≠ 0 belongs to its home deck, and the filtered deck itself is not created.
  *Acceptance:* a table test in `internal/store/anki_progress_import_test.go` covers four
  packages. A package whose only card sits in a filtered deck with `odid` naming `Home` imports as a
  deck named `Home`, and the card has no `deck_path`. A card whose home deck is `Home::Sub` and which
  sits in a filtered deck gets `deck_path` `Sub`, in a package where another card sits in `Home`. A
  package whose cards are all in their home decks imports as before. The preset of a new deck comes from
  the home deck's configuration. Evidence is the raw scoped `go test` and `go vet` output.

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

- [ ] **M18-50 The first learning row after a reset was given to a new card** — `importAnkiProgress`
  in `internal/store/anki_import.go` stores `state_before` 1 for a learning row (revlog type 0) when the
  card has any earlier revlog row. Rows up to and including a reset (type 4) count as earlier rows.
  After a reset the card is new, and since `M18-37` the replay already drops the rows up to the reset.
  Reproduced on `main`: a learning card with the rows learning Good, review Good (+1 day), a type 4 row
  with ease 0 and factor 0 (+200 days) and learning Again (+201 days) stores the last row with
  `state_before` 1. The wrong value breaks undo. A review card with the rows learning Good, review Good
  (+1 day, `ivl` 3), a type 4 reset row, learning Good and review Good (+3 days, `ivl` 7) is imported
  with progress, and `Rollback` runs twice. The card ends in state `learning` with stability 0,
  difficulty 0 and the due date of the pre-reset review plus 3 days. `Preview` then fails with `fsrs:
  invalid stability: 0 (minimum 0.001 for non-New cards)`. The fix: as the importer walks the revlog in
  order, a reset row clears its card's earlier-row mark, so the first learning row after it gets
  `state_before` 0. The reset test is the expression the replay uses to drop rows, shared rather than
  copied, so that `M18-49` changes both at once. *Acceptance:* a table test in
  `internal/store/anki_progress_import_test.go` stores `state_before` 0, 2 and 0 for the three graded
  rows of the first history above. A test in `internal/schedule` imports the second history, runs
  `Rollback` twice and gets state `new`, after which `Preview` returns no error. Evidence is the raw
  scoped `go test` and `go vet` output.

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

- [ ] **M18-52 The `M18-36` tests pass on PostgreSQL, with the output recorded** — the commit that
  closed `M18-36` (`56b787e`) contains no PostgreSQL output. It says the postgres subtests passed "in
  the subagent run recorded at" a file under `/tmp`. That file is not in the repository: item 7 of the
  definition of done in section 4 of `AGENTS.md` forbids citing it, and section 7 requires the evidence
  to be output. `TestAnkiProgressCarriedMemoryValidation` in `internal/store` and
  `TestImportedAnkiMemoryPreview` in `internal/schedule` both have a postgres branch. This task runs
  tests and changes no file. A PostgreSQL case that fails is recorded as a new entry that names the
  failure; it is not fixed under this ID. *Acceptance:*
  `go test -count=1 -v -run 'TestAnkiProgressCarriedMemoryValidation|TestImportedAnkiMemoryPreview' ./internal/store/ ./internal/schedule/`
  with `TEST_PG_DSN` set passes, and its output shows the `postgres` subtests of both tests as `PASS`,
  none as `SKIP`. Evidence is that raw output, quoted in the closing commit.

- [ ] **M18-53 The round trip compares a `duration_days` that is not NULL** — `M18-10` required
  `TestExportAnkiPackageRoundTrip` to compare `interval_days` and `duration_days`. The landed change
  removed `DurationDays: Ptr(24.0)` from the only review `seedAnkiExportDeck` seeds in
  `internal/store/anki_export_test.go`. The source value and the imported value are therefore both
  NULL, and the comparison passes whatever duration the importer computes. The fix: the seed gives the
  card two reviews by the owner. The second review's `duration_days` equals the fractional number of
  days between the two `reviewed_at` values. The round trip compares `interval_days` and
  `duration_days` of both rows, the second row's `duration_days` not NULL. Every other assertion of the
  test keeps its meaning. *Acceptance:* the test passes on both drivers; with the `duration =
  &value` assignment in `importAnkiProgress` removed for one run, the test fails. Evidence is the raw
  scoped `go test` and `go vet` output of both runs, the PostgreSQL one with `TEST_PG_DSN` set.

- [ ] **M18-54 Comments state the imported progress rules** — the code `M18-10`, `M18-11` and `M18-36`
  added states its rules without saying why, and `M18-11` left one comment and one error text stale. The
  gaps are four. First, `usableAnkiMemory` in `internal/store/anki_import.go` has no doc comment. The
  comment states each bound and its reason: go-fsrs v4 refuses a non-new card with stability below
  0.001 or difficulty below 1, and 10 is the highest difficulty FSRS produces. Second, the last-review
  chain in `importAnkiProgress` has no comment. The chain takes `lrt`, then the last kept revlog row,
  then the due date minus `ivl` for a review or relearning card, and leaves a learning card NULL. The
  comment states why each step comes in that order and why a learning card stays NULL. Third, the
  columns of an imported review row have no comment. `interval_days` is `ivl`, a negative `ivl` being
  seconds; `duration_days` counts days from the previous kept row; stability and difficulty come from
  the replay. The comment states what engram's own review rows mean by each column, so the reader sees
  why the importer fills them this way. Fourth, in `internal/anki/write.go`, the comment above the card
  loop says that `cards.data` retains memory state, but the column now also carries `lrt`. The error
  text `encode card %d memory state` becomes `encode card %d data`. *Acceptance:* the diff adds or
  corrects each of the four comments and the error text; the scoped `go vet` and `go test` of
  `internal/store` and `internal/anki` pass. Evidence is the diff and the raw scoped output.

- [ ] **M18-55 The `M18-1` and `M18-2` ledger rows record what `M18-10` to `M18-12`, `M18-36` and
  `M18-37` changed** — `M18-22` writes each ledger row from the commits whose messages name that row's
  ID. The commits of `M18-10`, `M18-11`, `M18-12`, `M18-36` and `M18-37` name only their own IDs, so
  the `M18-1` and `M18-2` rows would leave out changes visible through every import and export entry.
  For `M18-1`: an `.apkg` export writes `lrt`, in seconds, into `cards.data` for every card with a last
  review time (`M18-11`). For `M18-2`, there are five points. First, an imported review row's
  `interval_days` is the revlog `ivl` (a negative `ivl` converted from seconds to days), where `M18-2`
  had written `ivl` into `duration_days`. Its `duration_days` is the fractional number of days since
  the card's previous kept row, and its stability and difficulty come from the replay (`M18-10`).
  Second, a card's last review time comes from `lrt`, then from the last kept revlog row, then from the
  due date minus `ivl` (`M18-11`). Third, an interday learning card's due date counts days from
  collection creation, and a filtered-deck card's due date comes from `odue` (`M18-12`). Fourth, an
  unusable `s`/`d` is treated as missing (`M18-36`). Fifth, the replay drops the rows up to the last
  reset, and a history that begins with a review row takes the SM-2 state (`M18-37`). None of these is
  breaking, because `M18-1` and `M18-2` are in no release. The rows also name each of `M18-47` to
  `M18-51` that has landed by then. Only the parent edits this file, with the maintainer present, in
  the same change as `M18-22`. *Acceptance:* the ledger row that names `M18-1` also names `M18-11`, and
  the ledger row that names `M18-2` also names `M18-10`, `M18-11`, `M18-12`, `M18-36` and `M18-37`.
  Evidence is the output of `grep -n -E 'M18-(10|11|12|36|37)\b' ROADMAP.md` limited to the ledger
  section.

- [ ] **M18-56 A progress import never overwrites a review committed during the import** — since
  `M18-16`, `importAnkiProgress` in `internal/store/anki_import.go` loads the importer's `card_states`
  rows once, in batches, before its card loop, and later updates a row with
  `Where("card_id = ? AND user_id = ?")`, without a lock and without a version check. Since `M18-15`, the
  decision whether the local row wins is also taken from that early copy. On PostgreSQL a review
  submission for a card of the import can commit between the load and the update:
  `loadStateForUpdate` in `internal/schedule/submit.go` finds the row unlocked. The import then writes
  the package state over the review's state and stores `version` as the loaded version plus 1. That is
  the same number the submission wrote, so a client holding it gets no `409`. The package's review rows
  are also appended after the submission's row, so an undo removes an imported row instead of the
  user's own review, the case `M18-15` excluded. A deployment on SQLite is not affected, because
  `store.Open` gives SQLite one connection, so a submission cannot run inside an import. Reproduced on
  `main` for the state, on both drivers, with `importAnkiProgress` called outside a transaction. A GORM
  callback runs right after the batched `card_states` load and applies the update a submission makes
  (reps 99, `version` 0 to 1, `last_review_at` now). The import of a review card with `lrt` 1600000000
  then stores reps 7, `last_review_at` 2020-09-13 and `version` 1, with `progress_applied` 1. The fix
  has two parts. First,
  on PostgreSQL the batched load takes `clause.Locking{Strength: "UPDATE"}`, as `loadStateForUpdate`
  does, so a submission for a loaded card waits for the import's commit and then fails its version
  check with `409`. Second, the create of a state row that did not exist at the load uses
  `clause.OnConflict{DoNothing: true}`. When that create inserts no row, the card counts in
  `states_skipped`, and none of its package review rows is written. *Acceptance:* two tests in
  `internal/store/anki_progress_import_test.go`. The first runs on PostgreSQL only, gated by
  `internal/pgtest`. It runs `importAnkiProgress` inside `db.Transaction`, for a card whose package
  `lrt` is later than the importer's `last_review_at`, so the import writes the package state. A
  callback registered after the `card_states` load starts a goroutine. On a second connection, the
  goroutine runs `UPDATE card_states SET version = version + 1 WHERE card_id = ? AND user_id = ? AND
  version = ?` with the loaded version. The callback then waits 200 ms. After the import commits, that
  update has affected 0 rows, and the stored row holds the package state. The second test
  runs on both drivers. A callback inserts a state row, after the load, for a card that had none. The
  import counts that card in `states_skipped`, keeps the inserted row and writes none of the card's
  package review rows. Evidence is the raw scoped `go test` and `go vet` output, the PostgreSQL run with
  `TEST_PG_DSN` set.

- [ ] **M18-57 A learning card's package time is not its due date** — `M18-15` keeps the importer's
  `card_states` row unless the package time is later than the row's `last_review_at`. The package time
  is `state.LastReviewAt`, which the `M18-11` chain sets from `lrt`, then from the last kept revlog row,
  then from the due date minus `ivl` for a review or relearning card. A learning card that has none of
  these keeps NULL, by the `M18-11` rule. When `state.LastReviewAt` is NULL and the card has a due date,
  the comparison computes the due date minus `ivl` a second time, in its own copy of that code. Only a
  learning card reaches that copy. For a learning card, `ivl` is 0, so the package time becomes the
  card's next due date. That date lies after the package's last review.
  Reproduced on `main` on both drivers: a learning card (type 1, queue 1, `due` 1600000600, `ivl` 0,
  `cards.data` `{"s":3,"d":5}`, no `lrt`, no revlog row). First, the importer's row with reps 42 and
  `last_review_at` 1600000300 is replaced by the package state, with reps 9, `last_review_at` NULL and
  `progress_applied` 1. Second, each later import of the same package again reports `progress_applied`
  1 and raises `version`, from 1 to 2 and then to 3. A repeated import therefore invalidates the
  `expected_version` of an open review session, which `M18-8` had removed. Two leftovers sit in the same
  block. `equalAnkiState`, which `M18-8` added, has had no caller since `M18-15`. The `reviews` rows
  loaded into `reviewKeys` can never match: `M18-15` skips every package row that is not later than the
  importer's latest review, and every loaded row is at or before it. The state comparison also has no
  comment, so the rule the maintainer chose for `M18-15` is written only in a commit message. The fix: the
  comparison uses `state.LastReviewAt` as the chain leaves it, and the second due-minus-`ivl` block is
  deleted. A NULL package time counts as the zero time, the way a new card already counts. It therefore
  never replaces an existing row, and a zero time on both sides writes nothing. `equalAnkiState` is
  deleted. The batched `reviews` query feeds only `latestReview`, and `reviewKeys` holds only the rows
  this import appends. A comment on the comparison states the rule and its reasons. A later local review
  wins, because it is newer progress. A later package review writes the package state. Equal times
  write nothing, so a repeated import changes no `version`. A NULL package time counts as oldest,
  because the package has no evidence of a later review. *Acceptance:* a table test in
  `internal/store/anki_progress_import_test.go` imports the learning card above. With the importer's row
  in place, the row is unchanged and `states_skipped` is 1. With no row, three imports in a row leave
  `version` at its value after the first import, and the second and third report `progress_applied` 0
  and `states_skipped` 1. `TestAnkiProgressLocalWins` and `TestUndoImportedReviewMemory` still pass.
  `git grep -n equalAnkiState` is empty. Evidence is the raw scoped `go test` and `go vet` output, the
  PostgreSQL run with `TEST_PG_DSN` set, and the grep output.

- [ ] **M18-59 Stored presets the scheduler cannot use are repaired** — v0.2.0 and v0.2.1 accepted every
  positive `maximum_interval_days` on `POST` and `PATCH /api/v1/presets`, because
  `validatePresetForWrite` checked only `<= 0`. `.apkg` imports before `238ede0` copied the package's
  maximum interval and steps unchecked, and `.edeck` imports do so until `M18-58` lands. A deployed
  database can therefore hold presets that go-fsrs v4.0.0 refuses. A maximum interval above 36 500, or a
  negative step, makes `NewFSRS` replace every parameter with `fsrs.DefaultParam()`. A step stored as
  `NaNm` or `+Infm` makes `schedule.NewScheduler` return an error, so every preview and every review
  submission in the deck fails. Since `M18-17`, such a preset also cannot be saved. Reproduced on `main`:
  `PresetStore.Update` of a preset with 50 000 days refuses a rename with
  `maximum interval days must be between 1 and 36500: 50000`. The fix is a versioned migration, numbered
  after the last entry of `BuiltinMigrations` in `internal/store/migrate.go` when the task starts. It
  sets `maximum_interval_days` to 36 500 where it is above 36 500. It sets `learning_steps` to
  `DefaultLearningSteps` where a part of the stored text is refused by `ankiStepMinutes` or is negative.
  The same rule applies to `relearning_steps`, with `DefaultRelearningSteps`. It changes no other
  column. The maintainer has decided that a versioned migration may rewrite stored preset values: the
  migration clamps a maximum interval above 36 500 to 36 500, replaces a negative or non-finite step set
  with the default steps, and records in its doc comment how many rows it changed. Rewriting is allowed
  because a preset the scheduler refuses is unusable as stored, and the alternative — leaving rows that
  go-fsrs replaces wholesale at schedule time yet display as imported values — is the failure this entry
  exists to remove. The migration must be idempotent and must not touch a preset whose values are already
  acceptable.
  *Acceptance:* a migration test in `internal/store`, on both drivers, seeds presets with 50 000 days,
  `-1m`, `NaNm`, `+Infm` and one valid preset. After the migration, the first four hold the repaired
  values and the valid preset is unchanged. A second run changes nothing. A test in `internal/schedule`
  builds the scheduler from each repaired value and asserts that its go-fsrs parameters hold the
  preset's maximum interval, steps and first weight, which shows that `NewFSRS` did not reset them.
  Evidence is the raw scoped `go test` and `go vet` output, the PostgreSQL run with `TEST_PG_DSN` set.

- [ ] **M18-60 The preset page and API state the 36 500-day bound** — since `M18-17` the store refuses a
  maximum interval above 36 500. The layers in front of it still describe the old rule.
  `parsePresetRequest` in `internal/web/presets_api.go` refuses only `<= 0`, and its doc comment says
  "maximum interval a positive integer". The English fallback message for `preset_max_interval_invalid`
  says "The maximum interval must be a positive number of days." `frontend/src/lib/views/PresetsView.svelte`
  refuses only `<= 0` on the client. It shows `presets.form.error.max_interval` ("Maximum interval must
  be a positive whole number of days." / "最大间隔必须是正整数天数。") for the server's
  `preset_max_interval_invalid` as well. A user who types 40 000 therefore sees a message that 40 000
  satisfies. The fix: `parsePresetRequest` returns `preset_max_interval_invalid` for a value above
  36 500 as well, and its doc comment says so. The fallback message and both catalogs' text for
  `presets.form.error.max_interval` state the range 1 to 36 500. `PresetsView.svelte` refuses a value
  above 36 500 before sending. The catalog lines keep their keys and their positions; only their text
  changes, and the two catalogs keep identical key sets. *Acceptance:* a handler test in
  `internal/web/presets_api_test.go` shows that 36 501 is refused with the new message on create and on
  update. A component test shows that 36 501 displays the error and sends no request, and that 36 500
  is sent. Evidence is the raw scoped `go test` and `go vet` output and the component test output.

- [ ] **M18-61 The import report labels say what an `.apkg` import did** — `M18-14` made
  `progress_discarded` true for an `.apkg` import that carries progress and has `include_progress` off.
  `frontend/src/lib/views/ImportView.svelte` labels that row `package.report.progress_discarded`, which
  reads "Other users’ progress dropped" / "舍弃他人进度". That text describes the `.edeck` case only, where
  the field means that another user's progress was refused. An Anki package has no users. The same view
  prints `weights_applied`, `weights_discarded` and, since `M18-18`, `weights_invalid` as `1` or `0`, while
  `dry_run` and `progress_discarded` print the catalog's yes and no. In
  `internal/store/package_import.go`, `ProgressDiscarded` has no doc comment for its two meanings, and
  `WeightsInvalid` has none at all, while its neighbours have one. The fix: the text of
  `package.report.progress_discarded` in both catalogs states that the package's progress was not
  imported, without naming other users. The line keeps its key and position, and the two catalogs keep
  identical key sets. The view prints the three weight rows as yes or no. `ProgressDiscarded` gains a
  comment that states the `.edeck` meaning and the `.apkg` meaning. `WeightsInvalid` gains a comment
  that states the four conditions `M18-18` defined. *Acceptance:* a component test shows the new
  `progress_discarded` text, and shows yes or no in each of the three weight rows for `true` and `false`.
  The scoped `go vet` of `internal/store` passes. Evidence is the component test output and the diff.

- [ ] **M18-62 The batching test measures many cards and skipped notes** — `TestAnkiProgressBatchStatements`
  in `internal/store/anki_progress_import_test.go` imports one card with 5 000 revlog rows. It catches a
  query per revlog row. It cannot catch a query per card: with one card, the per-card `card_states`
  lookup that `M18-16` removed runs one statement, as the batched load does. It never runs the batched
  lookup of skipped notes' cards, because it calls `importAnkiProgress` directly and that lookup lives in
  `importAnkiInTx`. The fix adds a case. A package with 1 000 cards, each with one review row, is
  imported once, and then imported again through `importAnkiInTx` with `into_deck`, `on_conflict`
  `skip` and `include_progress` on, so every note is skipped. A GORM query callback counts the
  `SELECT` statements on `card_states`, `reviews` and `cards` during the second import. *Acceptance:* in
  the second import, the `SELECT` statements on `card_states` and `reviews` number at most 4. The second
  import of 1 000 cards runs at most 2 more `SELECT` statements on `cards` than the same import of 10
  cards. With the batched
  `card_states` load replaced by a per-card query for one run, the test fails. Evidence is the raw
  scoped `go test` and `go vet` output of both runs, the PostgreSQL one with `TEST_PG_DSN` set.

- [ ] **M18-63 The release ledger and `AGENTS.md` record `M18-14` to `M18-18`** — `M18-22` writes each
  ledger row from the commits whose messages name that row's ID. The commits of `M18-14`, `M18-15`,
  `M18-17` and `M18-18` name only their own IDs. Their changes are visible through the HTTP API, the
  MCP tools and the CLI. One of them changes a route that v0.2.0 and v0.2.1 already shipped. That route
  is `POST` and `PATCH /api/v1/presets`, which since `M18-17` refuse a `maximum_interval_days` above
  36 500 with 400 `preset_max_interval_invalid`. That needs its own ledger row, marked not breaking in
  shape: a request that was accepted is now refused. The `M18-2` row would leave out four points. First,
  with `include_progress` off, `states_discarded` counts only cards with scheduling state, and
  `progress_discarded` is true only when a state or a review was discarded (`M18-14`). Second, a
  re-import with `include_progress` on keeps the importer's row when its `last_review_at` is not
  earlier than the package's, and writes only the review rows later than the importer's latest review
  (`M18-15`). Third, an `.apkg` preset stores at most 36 500 days and keeps the default steps when the
  package's steps are negative or not finite (`M18-17`). Fourth, the report gains `weights_invalid`
  (`M18-18`). The rows also name each of `M18-56` to `M18-62` that has landed by then. `AGENTS.md`
  section 2.7, under "Importing an `.apkg`", says that card states and the review log are applied when
  the import asks for them. It does not state the `M18-15` rule. A decision that must outlive its change
  belongs in a committed file (section 1 of `AGENTS.md`). The fix: the ledger gains the preset row, the
  `M18-2` row states the four points, and section 2.7 gains one sentence that states the `M18-15` rule.
  Only the parent edits these two files, with the maintainer present, in the same change as `M18-22`
  and `M18-55`. *Acceptance:* the ledger row that names `M18-2` also names `M18-14`, `M18-15`, `M18-17`
  and `M18-18`; one ledger row names `preset_max_interval_invalid`; section 2.7 of `AGENTS.md` names
  `last_review_at`. Evidence is the output of `grep -n -E 'M18-(14|15|17|18)\b|preset_max_interval_invalid' ROADMAP.md`
  limited to the ledger section, and the `AGENTS.md` diff.

- [ ] **M18-64 README states the re-import progress rule and `weights_invalid`** — `README.md` line 65
  says that a re-import keeps "their cards and progress". It does not say what happens when the
  re-import carries progress: since `M18-15`, the importer's own newer progress wins, and older package
  review rows are not written. The paragraph on line 47 names `apply_weights` but not the report field
  `weights_invalid`, which `M18-18` added to tell a refused weight set from an unused one. The fix: the
  line 65 paragraph gains the `M18-15` rule, in the form `M18-57` leaves it. A card whose last review in
  engram is not earlier than the package's keeps its engram state. A package review is written only
  when it is later than the importer's latest review of that card. The line 47 paragraph states that
  `weights_invalid` is true when `apply_weights` was on and the package's weights were refused. This
  entry lands after `M18-21`, `M18-46` and `M18-57`, and never runs beside `M18-21` or `M18-46`.
  *Acceptance:* each new statement matches `importAnkiProgress` and `createAnkiPreset` in
  `internal/store/anki_import.go`. Evidence is the `README.md` diff.

- [ ] **M18-65 Decide what happens to a skipped card's review rows with `include_progress` on** — since `M18-26`, `readCards` in `internal/anki/sqlite.go` skips a card row whose `type`, `queue`, `odid`
  or `flags` holds a REAL value and counts it in `Package.CardsSkipped`. The revlog rows of that card are
  still readable, but `importAnkiProgress` in `internal/store/anki_import.go` finds no engram card for
  them, counts them in `reviews_skipped` and writes no `reviews` row. `M18-23` fails an import that has
  `include_progress` on when one revlog row cannot be read, because a dropped review row cannot be
  recovered later and the review log is the only input to parameter optimisation. A skipped card loses
  its readable review rows in the same way, yet the import succeeds. Reproduced on `main` on both
  drivers: the schema-11 stock package with one revlog row, whose card has `queue` = 1.5 and `type` = 2,
  imported with `include_progress` on, reports `cards_skipped` 1, `reviews_skipped` 1 and
  `reviews_applied` 0, and the importer has no `reviews` row. The `M18-26` rule says that the rest of the
  import continues; it does not say whether that also holds with `include_progress` on. The maintainer has
  decided rule (b), with one addition: the import continues with `include_progress` on, the skipped card's
  review rows are counted in `reviews_discarded` and logged at warn level so they are visible rather than
  silently gone, and the comment on the skip in `readCards` states that neither the card nor its review
  rows are imported. Rule (a) was rejected because `M18-26` exists precisely so that one corrupt row does
  not stop a content import, and a card row that cannot be read is as unrecoverable as an unreadable
  revlog row — refusing the whole package for it would reintroduce the failure `M18-13` removed.
  *Acceptance:* under rule (a),
  a table test in `internal/store` imports the package above through `ImportPackage`. With
  `include_progress` on it returns a `PackageError` with code `package_bad_format` and adds no deck, note,
  card, `card_states` or `reviews` row and no media file. With `include_progress` off it creates every
  other card of the package and reports `cards_skipped` 1. Under rule (b), the same test shows that with
  `include_progress` on the import succeeds and reports `reviews_skipped` 1. Evidence is the raw scoped
  `go test` and `go vet` output, the PostgreSQL run with `TEST_PG_DSN` set.

- [ ] **M18-67 The import page shows `cards_skipped`** — since `M18-26` the import report carries
  `cards_skipped`, the number of package card rows that could not be read and were not imported.
  `PackageImportReport` in `frontend/src/lib/api/types.ts` has no such field, and `reportRows` in
  `frontend/src/lib/views/ImportView.svelte` does not list it, so a user who imports on the web page is
  not told that cards are missing. The fix: `types.ts` gains `cards_skipped: number`. `reportRows` adds
  one row keyed `package.report.cards_skipped` only when the value is above 0, the way it adds the
  `weights_invalid` row. The key is appended at the end of `frontend/src/lib/i18n/locales/en.ts` and
  `zh-CN.ts`, and the two key sets stay identical. Its text states that this many cards of the package
  could not be read and were not imported. *Acceptance:* a component test shows the row with the count
  for `cards_skipped` 2 and hides it for 0. Evidence is the component test output and the output of
  `npm --prefix frontend run build`.

- [ ] **M18-68 The report says what the reader dropped** — the reader drops data in two more places, and
  the report shows neither. First, `readReviews` in `internal/anki/scheduling.go` keeps no count of the
  revlog rows it skips. With `include_progress` off, `importAnkiProgress` in
  `internal/store/anki_import.go` sets `reviews_discarded` to `len(pkg.Reviews)`, which leaves the
  unreadable rows out, and `progress_discarded` follows that count. Reproduced on `main` on both drivers:
  the schema-11 stock package whose only revlog row has `ease` = 2.5, imported with `include_progress`
  off, reports `reviews_discarded` 0 and `progress_discarded` false. Second, when the top deck's
  configuration was skipped, `ankiTopConfig` returns nil (since `M18-24`) and the new preset holds the
  `NewPreset` values. `resolveAnkiTargetDeck` then reports `weights_applied`, `weights_discarded` and
  `weights_invalid` all false, which is the report of a package without weights, also when
  `apply_weights` was on. No field says that the package's deck options were not read. The maintainer has
  decided the second point too: the report gains the field — it is additive, the values are lies without
  it, and the alternative (reusing the weights flags) would overload three flags with a meaning they do
  not have. The fix has three parts. First, `anki.Package` in `internal/anki/model.go` gains
  `ReviewsUnreadable int`, which `readReviews` increments for each row it skips, and with
  `include_progress` off `reviews_discarded` is `len(pkg.Reviews) + pkg.ReviewsUnreadable`. Second, the
  report gains `DeckConfigSkipped` (JSON `deck_config_skipped`), which is true only when the target is
  `new_deck`, `preset_mode` is `new`, `DeckConfigsSkipped` is true and `ankiTopConfig` returned nil.
  Third, `frontend/src/lib/api/types.ts` gains the field, and `ImportView.svelte` shows one catalog line
  when it is true; the key `package.report.deck_config_skipped` is appended at the end of both catalogs,
  and the two key sets stay identical. *Acceptance:* a table test in `internal/store` imports through
  `ImportPackage`. The package above, with `include_progress` off, reports `reviews_discarded` 1 and
  `progress_discarded` true. The `top invalid` package of `TestAnkiImportSkippedDeckConfigs` reports
  `deck_config_skipped` true; its `other invalid` and `both valid` packages report it false. A component
  test shows the line for true and hides it for false. Evidence is the raw scoped `go test` and `go vet`
  output, the PostgreSQL run with `TEST_PG_DSN` set, and the component test output.

- [ ] **M18-69 One weight check serves both package formats** — `M18-19` checks `.edeck` weights twice.
  The closure that `resolveTargetDeck` in `internal/store/package_import.go` passes to
  `selectImportPreset` calls `fsrs.MigrateWeights` to set `weights_invalid`, and `createPresetFromPackage`
  calls it again, unconditionally, to decide whether to write the weights. `createAnkiPreset` in
  `internal/store/anki_import.go` holds the same rule a third time, together with the `slog.Warn` line that
  `M18-18` requires. The `.edeck` path writes no such line, so a refused `.edeck` weight set leaves no
  trace in the log. The doc comment of `createPresetFromPackage` says only that `applyWeights` false keeps
  the weights out; it does not say that a refused set is kept out too. The fix: one function in
  `internal/store/preset_weights.go` takes the owner, the package's weight list, `apply_weights` and the
  report. It returns the JSON text to store, or nil. It sets `weights_invalid` when `apply_weights` is on
  and `fsrs.MigrateWeights` refuses a non-empty list, and then writes the `slog.Warn` line carrying the
  user id and the error. `createAnkiPreset` and `createPresetFromPackage` both call it. The closure in
  `resolveTargetDeck` only calls `createPresetFromPackage`. `createPresetFromPackage` writes
  `weights_review_count` and `weights_optimized_at` only when the function returned weights, and its doc
  comment states that rule. If `M18-5` lands first, this entry is deleted with a commit message that
  names `M18-5`. *Acceptance:* `git grep -n 'fsrs.MigrateWeights' -- internal/store/anki_import.go
  internal/store/package_import.go` prints nothing; every case of `TestImportPackageApplyWeights` in
  `internal/store/package_weights_test.go` and of the weights table in
  `internal/store/anki_preset_validation_test.go` keeps its result; a test captures the `slog.Warn` line
  for a refused `.edeck` set. Evidence is the grep output and the raw scoped `go test` and `go vet`
  output.

- [ ] **M18-70 Comments state the reader's skip rules without a task ID** — the code of `M18-24` to
  `M18-27` carries four comment defects. First, the comment on `roundedIntegers` in
  `internal/anki/sqlite.go` gives as its reason "before M18-13 one such value rejected the whole package";
  `M18-13` is a retired ID that no committed file defines any more, so the reason resolves only through
  `git log`. Second, `rejectRealIntegers` has no doc comment, and the doc comment of `readCards` does not
  say that a card row is skipped and counted. Third, in `internal/anki/model.go`, `Package.CardsSkipped`
  and `Package.DeckConfigsSkipped` have no comment, while `RevlogErr` beside them has one. Fourth, in
  `internal/store/package_import.go`, `PackageImportReport.CardsSkipped` has no comment, so nothing says
  that it counts package card rows that were not imported. The fix: the `roundedIntegers` comment states
  the reason in its own words, namely that one REAL value would otherwise reject a package whose content
  is readable, and cites no task ID. `rejectRealIntegers` gains a doc comment that begins with its name
  and states why a REAL value in an identifier or code column is refused instead of rounded: rounding it
  invents a reference, a rating or a state. `readCards` states the skip rule. The three fields gain one
  comment each. This entry changes comments only. *Acceptance:* `git grep -n 'M18-' -- internal/anki`
  prints nothing; the scoped `go vet` of `internal/anki` and `internal/store` passes. Evidence is the grep
  output, the diff and the raw scoped `go vet` output.

- [ ] **M18-71 The import tests share one fixture helper and record what they measure** — the commits of
  `M18-23` to `M18-27` added four copies of one procedure: open a fixture `.apkg`, run SQL on its
  collection, and write the archive again. Two copies are inline in `TestAnkiImportReportsSkippedCard` and
  `TestAnkiImportUnreadableRevlog` in `internal/store/anki_progress_import_test.go`; the others are
  `schedulingToleranceArchive` in `internal/store/anki_scheduling_tolerance_test.go` and
  `skippedConfigArchive` in `internal/store/anki_skipped_config_test.go`. `ankiFixtureWithoutTemplate` in
  `internal/store/anki_import_test.go` is a fifth, older copy. The copies differ:
  `TestAnkiImportReportsSkippedCard` and `skippedConfigArchive` ignore the error of `Close`.
  `TestAnkiImportUnreadableRevlog` has two more defects. It expects `len(pkg.Decks)` new decks, but an
  import creates exactly one deck, so a fixture with a second package deck fails it for a reason unrelated
  to the revlog. Its `t.Logf` line prints the expected counts under the label `deltas`, not the measured
  ones, so the evidence in the commit that closed `M18-23` (`19a2505`) is the expectation, not a
  measurement. `TestAnkiImportReportsSkippedCard` checks only the count; it does not check that the
  package's other cards were created. The fix: one helper in `internal/store/anki_import_test.go` takes a
  fixture name and SQL statements, handles the three collection layouts, checks every `Close` error and
  returns the archive bytes, and the five sites call it. `TestAnkiImportUnreadableRevlog` expects one new
  deck and logs the measured counts. `TestAnkiImportReportsSkippedCard` moves to
  `internal/store/anki_scheduling_tolerance_test.go` and also checks that `cards_created` equals the
  package's card count minus 1. This entry changes tests only. *Acceptance:* in
  `internal/store/anki_*_test.go`, `zip.NewWriter` appears only in the new helper and in
  `minimalPackageZipForTest`; every test named here passes on both drivers. Evidence is the output of
  `git grep -n 'zip.NewWriter' -- 'internal/store/anki_*_test.go'` and the raw scoped `go test` and
  `go vet` output, the PostgreSQL run with `TEST_PG_DSN` set.

- [ ] **M18-72 The release ledger records `M18-19` and what `M18-23` to `M18-26` changed** — `M18-28` asks
  the `M18-2` ledger row to name each of `M18-23` to `M18-26` that has landed, but not to state what they
  changed, and no entry asks for a row for `M18-19`. All five have landed, and each changes what an import
  does through the HTTP API, the MCP tools and the CLI. The `M18-2` row would leave out four points.
  First, with `include_progress` on, a revlog row that cannot be read fails the import with
  `package_bad_format` (`M18-23`). Second, when the top deck's configuration is not found and at least one
  deck configuration of the package was skipped, the new preset holds the defaults instead of the
  package's single remaining configuration (`M18-24`). Third, one malformed entry of a schema-11
  `col.dconf` drops that entry only (`M18-25`). Fourth, a REAL value in `cards.nid`, `cards.did` or
  `cards.ord` fails the import with `package_bad_format`; a REAL value in `revlog.cid`, `revlog.ease` or
  `revlog.type` makes that revlog row unreadable; a REAL value in `cards.type`, `cards.queue`,
  `cards.odid` or `cards.flags` leaves that card out; and the report gains `cards_skipped` (`M18-26`). A
  new row for `M18-19` states that an `.edeck` import with `apply_weights` on writes the package's weights
  only when `fsrs.MigrateWeights` accepts them, and that `weights_invalid` then describes an `.edeck`
  import too. Each row marks a breaking change as section 7 of `AGENTS.md` requires; the new report field
  is additive. The rows also name each of `M18-65` to `M18-71` that has landed by then. Only the parent
  edits this file, with the maintainer present, in the same change as `M18-22`, `M18-28`, `M18-55` and
  `M18-63`. *Acceptance:* the ledger row that names `M18-2` also names `M18-23`, `M18-24`, `M18-25`,
  `M18-26` and `cards_skipped`; one ledger row names `M18-19`. Evidence is the output of
  `grep -n -E 'M18-(19|23|24|25|26)\b|cards_skipped' ROADMAP.md` limited to the ledger section.

- [ ] **M18-73 The export follows the scheduler's weight rule, and the rule is stated where it lives** —
  `M18-38` moved the scheduler's weight rule into `PresetWeights` in `internal/store/preset_weights.go`.
  `ankiWeights` in `internal/store/anki_export.go` still decodes the same column with its own copy of the
  rule, and the two copies disagree on one value. `PresetWeights` passes an empty JSON array `[]` to
  `fsrs.MigrateWeights`, which refuses it, so no scheduler can be built from that preset. `ankiWeights`
  returns no weights for `[]`, so the export succeeds. The comment `M18-39` wrote on `ankiWeights` says
  that the export refuses a value "because the scheduler refuses the same value"; for `[]` the scheduler
  refuses and the export accepts. Reproduced on `main` on both drivers: the preset of `seedAnkiExportDeck`
  with `weights_json` set to `[]`, exported with `IncludeWeights` and `IncludeDeckConfigs` on, returns no
  error, while `PresetWeights` on the same text returns `store: migrate preset weights: fsrs: invalid
  weights slice length: must be 17, 19, or 21`. No write path in the current code stores `[]`, so only a
  row written by hand or by an earlier release reaches this case. Three comment and error gaps sit beside
  it. First, the doc comment of `PresetWeights` names NULL, empty and `null`, but not a whitespace-only
  value, which the code also treats as the defaults. It also does not say why the function lives in
  `internal/store`: the progress import derives a memory state that the scheduler later continues from,
  so both must decode the weights with one rule, and `internal/schedule` imports `internal/store`, so the
  rule cannot live in the scheduler. Second, the comments on `NewScheduler` and `Parameters` in
  `internal/schedule/scheduler.go` say that the default weights apply "when NULL" and name no other value.
  Third, `Parameters` returns the error of `PresetWeights` without context, so a scheduler failure reads
  `store: migrate preset weights: …`, while every other error of that function begins with `schedule:`.
  The fix: `ankiWeights` keeps returning no weights for an empty string and for `null`, both after
  trimming, and passes every other value to `PresetWeights`, returning its error wrapped with `%w`.
  `ankiWeights` then calls neither `json.Unmarshal` nor `fsrs.MigrateWeights` itself, and `[]` fails the
  export. The comment of `ankiWeights` states that the export accepts exactly the values the scheduler
  accepts, and that the empty string and `null` write no weights. The `PresetWeights` comment names the
  whitespace-only value and states the reason above. The `NewScheduler` and `Parameters` comments name
  the four values that give the default weights: NULL, an empty string, a whitespace-only string and
  `null`. `Parameters` wraps the error as `schedule: preset weights: %w`. `presetToPackage` in
  `internal/store/package.go`, the `.edeck` export, keeps its own rule, because `M18-5` deletes it.
  *Acceptance:* `grep -nE 'fsrs\.MigrateWeights\(|json\.Unmarshal\(' internal/store/anki_export.go`
  prints nothing. A table test in `internal/store/preset_weights_test.go` calls `ankiWeights` with `""`,
  `" null "`, `[]`, a 21-value set, a 17-value set, `[1,2,3]` and `not json`. It gets no weights and no
  error for the first two, an error for `[]`, the `fsrs.MigrateWeights` result for the two sets, and an
  error for the last two. A test in the same file exports the `seedAnkiExportDeck` deck with `weights_json`
  `[]` on both drivers: with `IncludeWeights` on it gets an error, and with `IncludeWeights` off it gets a
  package. A test in `internal/schedule` builds a scheduler from a preset whose `weights_json` is
  `not json` and gets an error whose text begins with `schedule:`. `TestPresetWeights` and
  `TestExportAnkiPackageRoundTrip` keep their results. Evidence is the grep output and the raw scoped
  `go test` and `go vet` output of `internal/store` and `internal/schedule`, the PostgreSQL run with
  `TEST_PG_DSN` set.

- [ ] **M18-74 The `M18-42` refusal is tested on every transport, and the refusal cases carry names** —
  `M18-42` named five entries that dropped a `preset_id` sent with an existing-deck target and no
  `preset_mode`: the multipart route, the URL import, the upload ticket, and the `import_deck` and
  `create_import_upload` MCP tools. The landed tests cover the multipart route
  (`TestImportMultipartRejectsPresetOptions`) and the upload ticket (`TestImportUploadPresetModes`) only.
  `TestImportURLPresetSelection` in `internal/api/package_url_test.go` and
  `TestImportToolsRejectPresetOptions` in `internal/mcp/package_upload_test.go` have no such case. If one
  of those entries stopped passing the request through `ValidateImportPreset`, it would drop the id again
  and no test would fail. The behaviour itself is correct on `main`. Measured with a temporary test:
  `POST /api/v1/decks/import-url` with `target` `into_deck:<public id>` of a deck the caller owns and the
  caller's own `preset_id` answers 400 `invalid_request` and makes no fetcher call, and both MCP tools
  answer `invalid_request: The request is invalid. — invalid preset selection`. Second, the refusal cases
  of `TestImportPresetModes` in `internal/store/package_preset_mode_test.go` run as subtests named `#00`
  to `#13`. `fmtOptions` encodes `PackageImportOptions` with `json.Marshal`, which fails on the `Now`
  function field, and it discards the error. Every name is therefore empty, and a failing case cannot be
  identified from the output. The fix adds and changes tests only. `TestImportURLPresetSelection` gains
  a refused case with `target` `into_deck:<public id>` of a deck the caller owns, the caller's own
  `preset_id` and no `preset_mode`. Like the existing refused cases, it is answered with 400
  `invalid_request`, makes no fetcher call and spends no rate-limit quota. `TestImportToolsRejectPresetOptions`
  gains, for both tools, a case with `target` `into_deck:<public id>` of a deck the caller owns, the
  caller's own `preset_id` and no `preset_mode`. It is answered with `invalid_request` and changes none of
  the row counts the test checks. Each refusal case of `TestImportPresetModes` gets a name that states its
  target, its mode, which preset it names and its weights flag, and `fmtOptions` is deleted.
  *Acceptance:* the new cases pass; the output of
  `go test -count=1 -v -run TestImportPresetModes ./internal/store/` holds no subtest name that ends in
  `#` followed by two digits; every existing case keeps its result. Evidence is that output and the raw
  scoped `go test` and `go vet` output of `internal/store`, `internal/api` and `internal/mcp`, the
  PostgreSQL run with `TEST_PG_DSN` set.

- [ ] **M18-75 Preset-choice texts state the existing-deck rule, and the remaining preset errors are
  wrapped** — since `M18-42`, `ValidateImportPreset` in `internal/store/package_import.go` treats the two
  kinds of target differently. For `new_deck`, an empty `preset_mode` becomes `new`; `preset_id` is
  required with `reuse` and `copy` and refused with `new`. For `into_deck` and `replace_deck`, an empty
  mode and `new` are accepted and change nothing; `reuse`, `copy`, any other mode and any non-zero
  `preset_id` are refused. Five texts do not state this, and four errors are returned unwrapped. First, the
  `jsonschema` texts that `M18-44` wrote for `preset_mode` and `preset_id` of `importDeckIn` and
  `createImportUploadIn` in `internal/mcp/tools.go` say "for new_deck only" and "refused for any other
  mode". An agent cannot tell from them whether a `preset_id` sent with `into_deck` is ignored or refused.
  It is refused whatever the mode is, and the silent drop of that id was the `M18-42` defect. The
  `preset_mode` text also omits that an empty value means `new`. The Go comments of the four fields do not
  state the existing-deck rule either. Second, the help texts of `--preset-mode` and `--preset-id` in
  `cmd/engram/package.go` read "preset mode: new, reuse or copy" and "public id of the preset to reuse or
  copy", and state no target rule. Third, `M18-44` replaced the comment of `ValidateImportPreset`, and the
  new comment no longer says that the function writes `new` into `opts.PresetMode` for `new_deck`.
  Callers depend on that write: `selectImportPreset` calls the create function only when the mode equals
  `new`, so an empty mode that skipped validation would reach the preset lookup with `PresetID` 0 and be
  refused. Fourth, the comment `M18-44` added to `validateImportPreset` in `internal/api/package_upload.go`
  says when the check runs but not why, and the `M18-44` entry asked for the reason. The reason is that a
  request the import would refuse must not spend rate-limit quota, an import slot or an outbound download,
  and must not obtain an upload ticket that the upload would refuse. Fifth, the comment of
  `ImportDeckPackageURL` in `internal/api/package_url.go` says that the authorization `ImportDeckPackage`
  repeats after the download preserves "the invariant that authorization precedes fetching bytes". The
  authorization before the download keeps that invariant. The repeated one re-checks a decision that a
  role change during the download could have invalidated. The unwrapped errors: `ValidateImportPreset`
  returns the error of its preset count as it is, and `selectImportPreset` returns the errors of its preset
  lookup, of `uniqueOwnerName` and of the copy's `Create` as they are; section 2.4 of `AGENTS.md` requires
  wrapping with `%w`. The fix: the two `jsonschema` texts and the four Go comments state the `new_deck`
  rule and the existing-deck rule separately. They say that an empty mode means `new` for `new_deck`, and
  that a `preset_id` sent with `into_deck` or `replace_deck` is refused whatever the mode is. The two CLI
  help texts state the same two rules. The `ValidateImportPreset` comment states the write into
  `opts.PresetMode` and why `selectImportPreset` needs it. The `validateImportPreset` comment states the
  reason above. The `ImportDeckPackageURL` comment states that the first authorization keeps the invariant
  and why the second one runs. The four errors are wrapped with `%w` and an English context; a
  `PackageError` is still returned unwrapped, so the transports keep answering it with 400. No status code
  and no error code changes. *Acceptance:* each statement of each changed text and comment matches
  `ValidateImportPreset`, `selectImportPreset`, `ImportDeckPackageURL` and `CreateImportUpload`;
  `TestImportPresetModes`, `TestImportPresetID`, `TestImportUploadPresetModes`,
  `TestImportMultipartRejectsPresetOptions`, `TestImportURLPresetSelection` and
  `TestImportToolsRejectPresetOptions` keep their results. Evidence is the diff and the raw scoped
  `go test` and `go vet` output of `internal/store`, `internal/api`, `internal/mcp` and `cmd/engram`, the
  PostgreSQL run with `TEST_PG_DSN` set.

- [ ] **M18-76 `deriveAnkiMemory` derives memory only, and its comment states its contract** — `M18-40`
  moved the memory-state derivation of `importAnkiProgress` in `internal/store/anki_import.go` into
  `deriveAnkiMemory`. That function also decodes `cards.data` and returns the card's `lrt`. The `lrt`
  feeds the last-review chain of `importAnkiProgress` and is not part of the memory state. The function
  returns `lrt` on the skip path too, where the caller discards it. Its comment states the order of the
  rules but not the contract. It does not say that the function writes the card's stability and
  difficulty into `state`, that it writes entries into `rowMemory` for the card's kept revlog rows, or
  what its three results mean. It says the function "skips a card", but the function returns false and
  the caller skips the card and counts it in `states_skipped`. Two more statements are missing or
  misplaced. When `HistoricalMemoryStates` fails, the function discards the error and the card's earlier
  rows keep NULL memory; the comment does not say so or why. The sentence about `DeltaT` describes code in
  the revlog loop of `importAnkiProgress`, not code in this function. The fix: `importAnkiProgress`
  decodes `cards.data` before it calls `deriveAnkiMemory`, keeps the error text `import Anki card %d data:
  %w`, and passes the decoded stability and difficulty. `deriveAnkiMemory` returns only whether the card's
  state is usable. Its comment states what it writes into `state` and into `rowMemory`, what the result
  means, and what happens when `HistoricalMemoryStates` fails, as the code then has it. The `DeltaT`
  sentence moves to the revlog loop. Behaviour does not change. *Acceptance:* `deriveAnkiMemory` returns
  one `bool` and contains no `json.Unmarshal` call; the progress tests in `internal/store` and
  `internal/schedule` keep their results on both drivers. Evidence is the diff and the raw scoped
  `go test` and `go vet` output of `internal/store` and `internal/schedule`, the PostgreSQL run with
  `TEST_PG_DSN` set.

- [ ] **M18-77 The `M18-41` tests check which rule produced the state, and one preview test covers both
  drivers** — `M18-41` added four cases to `TestAnkiProgressMissingMemory` in
  `internal/store/anki_progress_import_test.go`: `relearning history`, `relearning SM2`,
  `review stability only` and `review difficulty only`. Like the four older cases, they check only that
  stability is at least 0.001 and that difficulty lies from 1 to 10. Any rule that yields a value in that
  range passes. A relearning card with history that took the SM-2 path would pass, and so would a card
  whose single package value was kept. Measured on `main` with the default weights: the three new cases
  with history store stability 11.6875 and difficulty 1.0000, the replay of Good and Easy one day apart;
  `relearning SM2` stores stability 7.0000 and difficulty 7.1498, the result of
  `MemoryStateFromSM2(2.5, 7, 0.9)`. Second, `M18-41` moved `TestAnkiImportedMemoryPreview` into
  `internal/schedule/anki_memory_import_test.go`, beside `TestImportedAnkiMemoryPreview` in
  `internal/schedule/anki_memory_preview_test.go`, which `M18-36` added. The two names differ only in word
  order. Both import a package and check that `Preview` accepts each stored state. The moved test runs on
  SQLite only, while its sibling also runs on PostgreSQL. The moved test's four card shapes repeat the
  first four cases of `TestAnkiProgressMissingMemory`. The fix changes tests only. Each case of
  `TestAnkiProgressMissingMemory` that is not skipped compares the stored stability and difficulty, within
  1e-4, with the value its rule gives, computed in the test with go-fsrs. For a case with history, that
  value is the replay of the case's revlog rows. For a case without history, it is `MemoryStateFromSM2`
  from the case's factor and interval with retention 0.9. The cases of `TestAnkiImportedMemoryPreview`
  become cases of `TestImportedAnkiMemoryPreview`, which runs them on both drivers, and
  `internal/schedule/anki_memory_import_test.go` is deleted. *Acceptance:* in one run with the replay
  branch of `deriveAnkiMemory` replaced by the SM-2 branch, `TestAnkiProgressMissingMemory` fails;
  without that change it passes. `TestImportedAnkiMemoryPreview` passes and its output shows a `postgres`
  subtest for each moved case. `internal/schedule/anki_memory_import_test.go` no longer exists. Evidence is
  the raw scoped `go test` and `go vet` output of both runs, the PostgreSQL run with `TEST_PG_DSN` set.

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
