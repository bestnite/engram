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

---

### M19 — Repository review findings

A domain-by-domain review of the whole repository is being run with a second model, one domain per
session, the findings written straight into this section. Each entry names its domain by the packages
and files it cites, so the section does not need a running scoreboard: the open entries below are the
whole state.

The API and MCP domain was stopped once by the provider's content filter before it wrote anything, so
M19-1..M19-3 are the three leads it had already stated — static analysis that was read out of the code
but never reproduced — and the domain is being reviewed again from a rewrite that frames findings as
contract violations instead of intrusion steps. Every entry says whether it was reproduced or only read:
"unverified" means neither "probably fine" nor "confirmed broken".

- [x] **M19-1 A preset identifier is resolvable across accounts** — *Blocker.* The first pass reported
  that a request can name a preset belonging to another account and have it resolved instead of refused;
  the rewritten review **confirmed and reproduced** it. `POST /api/v1/decks` (and the MCP `create_deck`)
  resolves the other account's preset public id with no owner check and creates the deck on it, echoing
  that id back, while `ImportPresetID` refuses the same id with `invalid preset selection`
  (`internal/api/decks.go:114`, `internal/mcp/tools.go:243`, `internal/api/publicid_resolver.go:46-63`).
  *Acceptance:* a test in `internal/api` (or `internal/store` where the
  lookup lives) sends a write request naming another account's preset public id and shows that it is
  refused with the documented code and that no row is written; if the report turns out to be wrong, the
  test stays as the regression that pins the refusal. Evidence is the raw test output on both drivers.
  *Closed by `084f684`.* `CreateDeck` (`internal/api/service.go:121-141`) now loads a non-zero preset
  key and refuses one whose owner is not the caller, before any write; the transports' public-id
  resolution is unchanged, so the refusal is the `not_found` 404 this endpoint already documents for a
  preset it cannot resolve, and an unowned preset is indistinguishable from an unknown one. A
  `preset_selection_invalid` 400 was not used because that code documents the package-import choice,
  not deck create. `TestCreateDeckRefusesAnotherAccountsPreset`
  (`internal/api/deck_preset_owner_test.go`) sends the foreign public id over REST, asserts 404,
  `not_found`, no deck row and no `deck.create` audit row, and asserts the caller's own preset still
  creates the deck; it then calls the same service method the MCP tool uses with the foreign numeric key
  and expects the same refusal. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/api/ -run TestCreateDeckRefusesAnotherAccountsPreset -count=1 -v`:
  ```
  --- PASS: TestCreateDeckRefusesAnotherAccountsPreset (0.33s)
      --- PASS: TestCreateDeckRefusesAnotherAccountsPreset/sqlite (0.01s)
          --- PASS: TestCreateDeckRefusesAnotherAccountsPreset/sqlite/foreign_preset_is_refused_and_writes_nothing (0.00s)
          --- PASS: TestCreateDeckRefusesAnotherAccountsPreset/sqlite/own_preset_still_creates_the_deck (0.00s)
      --- PASS: TestCreateDeckRefusesAnotherAccountsPreset/postgres (0.32s)
          --- PASS: TestCreateDeckRefusesAnotherAccountsPreset/postgres/foreign_preset_is_refused_and_writes_nothing (0.01s)
          --- PASS: TestCreateDeckRefusesAnotherAccountsPreset/postgres/own_preset_still_creates_the_deck (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/api	0.348s
  ```
  The same run before the check reproduced the report: `POST /api/v1/decks with another account's
  preset = 201 {"id":"01a19859-d7fd-7bd0-bea5-8960bb3f258a",...,"preset_id":"01a19859-d7fd-73a0-b72b-a649e39b79ca",...},
  want 404`, with the second id belonging to the other account. `go test ./internal/api/ -count=1` and
  `go test ./internal/mcp/ -count=1` also pass.

- [x] **M19-2 Template media may be exportable by a caller who cannot read the deck** — the review
  reported that a deck export can carry media referenced only by a note type template, and that the
  export path does not apply the same media-readable check the single-card write path applies. The
  rewritten review **confirmed it by reading, without executing**: note-type create and update write
  `media_note_types` but never call `checkNewMediaRefs`, and `ankiMediaFiles` resolves name → sha → file
  with no readability check (`internal/store/notetype.go:119-145`, `internal/store/anki_export.go:690-741`).
  *Acceptance:* a test exports a deck containing templated media as a caller with read
  access and as a caller without it, and shows the second is refused (`media_not_readable` or the code
  the rules name) while the first succeeds; the export of an unreadable media name must not appear in
  the archive. Evidence is the raw test output.
  *Closed by `6e10423`.* The report is confirmed at the store layer and is reachable through the
  transport: `ExportAnkiPackage` checks deck read access, but the media rule is narrower than deck
  visibility, and a template reference is not covered by `media_notes`. `ankiMediaFiles`
  (`internal/store/anki_export.go:680`) now receives the exporting user, checks every resolved sha
  with `mediaReadableByUser` and refuses the whole export with `package_media_forbidden` listing the
  unreadable shas before any file is read. That is the package chain's existing code for this rule
  (`internal/store/package.go:27-30`), which `mapPackageError` answers as 403, so the REST and web
  export paths need no change. Note the exploit did not need an `_`-prefixed name: `noteTypeMediaNames`
  records only `_` names in `media_note_types`, while `ankiMediaFiles` collects every name a template
  writes. `TestExportAnkiPackageRefusesUnreadableTemplateMedia`
  (`internal/store/anki_export_media_access_test.go`) gives one user's `cover.png` to a deck, has a
  co-editor's note type reference it in its question template with no note referencing it, and shows
  the co-editor cannot read the sha. The co-editor's export is refused with that code; the uploader's
  export succeeds and the archive carries `cover.png` with the same bytes. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestExportAnkiPackageRefusesUnreadableTemplateMedia -count=1 -v`:
  ```
  === RUN   TestExportAnkiPackageRefusesUnreadableTemplateMedia
  === RUN   TestExportAnkiPackageRefusesUnreadableTemplateMedia/sqlite
  === RUN   TestExportAnkiPackageRefusesUnreadableTemplateMedia/postgres
  --- PASS: TestExportAnkiPackageRefusesUnreadableTemplateMedia (0.34s)
  --- PASS: TestExportAnkiPackageRefusesUnreadableTemplateMedia/sqlite (0.01s)
  --- PASS: TestExportAnkiPackageRefusesUnreadableTemplateMedia/postgres (0.04s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.348s
  ```
  The same test before the check failed on both drivers with `editor export succeeded, want the
  unreadable template media refused`. `go test ./internal/store/ -run 'TestExportAnkiPackage|TestMedia|TestPackageImport|TestImportPackage|TestAnkiExport' -count=1`
  and `go test ./internal/api/ -run 'Package|Export' -count=1` pass.

- [x] **M19-3 An evicted MCP session answers with the identity of the account that created it** —
  *Blocker.* The first pass reported a stale identity; the rewritten review **confirmed and reproduced it,
  and it is worse than stale identity: it is a cross-account read.** After the owner record is dropped by
  the cap-eviction path, `sessionOwnerMismatch` returns false for the now-unregistered session id
  (`internal/mcp/mcp.go:168-187`); a second account's key presenting that id was then served as the first
  account and received that account's deck list with 200. *Acceptance:* a test evicts the session and shows the next tool call is refused with the
  documented code, and that a call carrying a revoked API key is refused even when the session object
  still exists. Evidence is the raw test output.
  *Closed by `5c8a65d`.* `sessionOwner` (`internal/mcp/mcp.go:153-161`) now tells an unregistered session
  from another owner's, and `ServeHTTP` (`internal/mcp/mcp.go:110-127`) refuses an id with no owner record
  with 404 `session not found` before the request reaches the SDK; 404 is the code the SDK already returns
  for a session it does not have, so the client initializes again and the response reveals nothing. A
  registered session owned by another account is still 403 `session user mismatch`. The revoked-key case
  was already enforced by the auth middleware on every request and is now pinned in the same test.
  `TestMcpEvictedSessionIsRefused` (`internal/mcp/mcp_evicted_session_test.go`) ages the session to the
  oldest slot, fills the owner table past `mcpSessionCap` to drive the real eviction, then shows the other
  account's tool call is refused with no deck in the body and the owner is refused too; it then revokes a
  key whose session is still registered and expects 401. Raw output of
  `go test ./internal/mcp/ -run TestMcpEvictedSessionIsRefused -count=1 -v`:
  ```
  === RUN   TestMcpEvictedSessionIsRefused
  --- PASS: TestMcpEvictedSessionIsRefused (0.05s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/mcp	0.059s
  ```
  The same test before the fix reproduced the report: `evicted session served another account
  (status=200); raw={"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{\"decks\":[{\"id\":\"01a1985c-4164-7e30-ad97-82feb7a6984a\",\"name\":\"deck\",...`. `go test ./internal/mcp/ -count=1` passes.

- [x] **M19-4 Concurrent migration applies one step twice and drops note metadata** — *Blocker.* `Apply`
  reads the schema version once outside the migration transaction and takes no database-level lock
  (`internal/store/migrate.go:747-761`), so two processes that both read version 11 both run `0012`.
  The second pass reads fields the first pass already moved out, nulls them and creates a second note
  type (`migrate_notes.go:302-327,342-348`). *Reproduced* on PostgreSQL with a barrier holding both
  callers past the version read: `first Apply: applied=1; source_url=<set> extra_json=<set>` then
  `second Apply: applied=1; source_url=<nil> extra_json=<nil> note_types=2`. The full `Sync`-level DDL
  interleaving is unverified. *Acceptance:* a database-level lock held across `Up` and the version
  write, with the version re-read inside it; a regression with two connections migrating at once shows
  metadata intact and exactly one note type. A process mutex alone is not a fix.
  *Closed by the `fix(store): serialise migration steps with a database lock` commit on `main`.*
  `Apply` (`internal/store/migrate.go:731`) now opens each due step in a transaction, takes a
  transaction-level advisory lock on PostgreSQL (`lockMigrationStep`, `:807`; key `migrationLockKey`,
  `:799`), re-reads `CurrentVersion` inside the lock and only then runs `Up` and writes the version. A step the
  outer read already put behind the stored version is still skipped, which is safe because the
  version only moves forward. SQLite keeps its single write connection. `TestConcurrentMigrationKeepsNoteMetadata`
  (`internal/store/migrate_test.go`) opens a second PostgreSQL connection to the same schema, seeds a
  note whose field map carries `source_url`/`extra` and a stored version of 1, and runs the next step,
  which moves those fields into columns exactly as `0012` does, on both connections at once. Raw output
  of `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestConcurrentMigrationKeepsNoteMetadata -count=1 -v`:
  ```
  === RUN   TestConcurrentMigrationKeepsNoteMetadata
  --- PASS: TestConcurrentMigrationKeepsNoteMetadata (0.52s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.530s
  ```
  The same test with only `migrate.go` reverted failed with
  `applied = 2, runs = 2 across two migrators; want 1 and 1`,
  `source_url after concurrent migration = <nil>, want "https://example.com/source"`,
  `extra_json after concurrent migration = <nil>, want "{\"licence\":\"example\"}"` and
  `note types after concurrent migration = 2 created (3 total), want 1 created (2 total)`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -count=1` passes in `92.932s`.

- [x] **M19-5 A cancelled import deletes a media file another import committed** — *Blocker.*
  `cleanupMediaWrites` treats "the lookup failed" as "no metadata row"
  (`internal/store/package_import.go:506-519`, esp. `:512-515`; called from `anki_import.go:917-922`).
  A places the file outside its transaction, B reuses that file and commits the metadata row, A then
  cancels; A's lookup runs on the cancelled context, fails, and calls `os.Remove` on a file B depends on.
  Later uploads dedup on the metadata row without checking the file exists (`media_write.go:70-74,141-154`).
  *Reproduced* through the real `prepareAnkiImport` / `importAnkiPlacement` path: `A import err =
  context canceled` / `B row after A = true; file missing = true`, while a healthy-context failure keeps
  the file. *Acceptance:* a lookup error keeps the file and reports; deletion requires proof of
  absence, not a failed query. Cover it with a regression that cancels one import mid-flight while a
  second commits the same sha, and shows the file surviving and a later upload reusing it.
  *Closed by `248f7f5`.* `cleanupMediaWrites` (`internal/store/package_import.go:502-527`) now separates
  the three outcomes: a metadata row keeps the file, a failed lookup keeps the file and logs
  `media cleanup kept a file whose metadata lookup failed` with the sha and path, and only a successful
  lookup that finds no row removes it. Note the entry's own line references predate the fix; the function
  is at `:509`. `TestCancelledImportKeepsMediaCommittedByAnother`
  (`internal/store/media_cleanup_test.go`) prepares import A so its bytes are on disk with no row, lets
  import B commit the row for the same sha, cancels A's context, asserts the cleanup's lookup really fails
  on that context, then asserts the file survives, the warning was logged, and a later
  `SaveBytesTracked` reuses the row and the file. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestCancelledImportKeepsMediaCommittedByAnother -count=1 -v`:
  ```
  === RUN   TestCancelledImportKeepsMediaCommittedByAnother
  === RUN   TestCancelledImportKeepsMediaCommittedByAnother/sqlite
  === RUN   TestCancelledImportKeepsMediaCommittedByAnother/postgres
  --- PASS: TestCancelledImportKeepsMediaCommittedByAnother (0.35s)
      --- PASS: TestCancelledImportKeepsMediaCommittedByAnother/sqlite (0.01s)
      --- PASS: TestCancelledImportKeepsMediaCommittedByAnother/postgres (0.03s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.361s
  ```
  The same test before the fix failed with `cancelled import A removed a file that existed before it:
  /tmp/TestCancelledImportKeepsMediaCommittedByAnothersqlite2182148403/001/b1/b13e90f8....png:
  stat ...: no such file or directory`. The package/Anki/media store tests also pass with
  `go test ./internal/store/ -run 'TestAnkiImport|TestMedia|TestPackage|TestResolvePackageMaxConcurrentImports' -count=1`.

- [x] **M19-6 Deleting a note type can succeed while a note is created against it** — *Blocker.* The
  delete counts referencing notes and then deletes in the same transaction
  (`internal/store/notetype.go:293-340`, count at `:297,305`, delete at `:329`), but a transaction is
  not serialisation on PostgreSQL at READ COMMITTED: another session can still read the type and create
  the note and card, and the delete also succeeds, leaving `notes.note_type_id` dangling. *Reproduced*
  on PostgreSQL with a `BEFORE DELETE` trigger as a barrier: `delete err=<nil>; insert err=<nil>;
  cards=1; notes=1 note_types=0; NoteTypeFor=record not found`. Production SQLite serialises through a
  single connection, so this is a PostgreSQL-path defect. *Acceptance:* a foreign key with `RESTRICT`,
  or a shared lock on the type for note writes and an exclusive one re-checked under the delete;
  a regression must show the two operations cannot both succeed leaving an orphan.
  *Closed by this commit (`fix(store): serialise a note type delete against note writes`).*
  `Delete` (`internal/store/notetype.go:294`) now takes the note type's exclusive row lock before it counts
  referencing notes (`:299`), and every note write takes that row's shared lock in `prepareNoteFields`
  (`internal/store/note.go:353`); `lockNoteTypeRow` (`notetype.go:362`) emits the clause on PostgreSQL only,
  because SQLite holds one write connection and its parser rejects the clause. The two operations are
  therefore ordered: either the delete waits for the write and counts its note, or the write's lock re-read
  finds the row gone and fails with `record not found`. The delete's own doc comment said a note could not
  slip past the check "within the transaction's isolation on either database"; that was the false premise and
  now states the lock.
  `TestConcurrentNoteTypeDeleteAndNoteCreate` (`internal/store/notetype_delete_race_test.go`) is PostgreSQL
  only: a `BEFORE DELETE` trigger parks the delete after its count returned zero, a second connection writes
  a note in that window, and the test asserts the two did not both succeed and that no note survives its
  type. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestConcurrentNoteTypeDeleteAndNoteCreate -count=1 -v`:
  ```
  === RUN   TestConcurrentNoteTypeDeleteAndNoteCreate
  --- PASS: TestConcurrentNoteTypeDeleteAndNoteCreate (1.78s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	1.792s
  ```
  The same test with only the two lock calls removed failed with
  `delete and note create both succeeded: notes=1 note_types=0, want the two ordered`, which is the reported
  orphan. `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestNoteType|TestNote' -count=1`
  passes on both drivers.


- [x] **M19-7 Saving your own profile writes back a stale role or status** — *should-fix.*
  `UserStore.Update` omits only `PasswordHash` and saves the whole row (`internal/store/user.go:65-73`),
  and the profile/locale handlers load a user, change profile fields and save
  (`internal/web/profile.go:176-214,240-250`). A disable or demotion committed between the read and the
  write is overwritten by the stale snapshot. *Reproduced* on both drivers: `status after stale
  whole-row Update = "active", want "disabled"` and `role after stale whole-row Update = "admin", want
  "user"`. The probability of a real concurrent HTTP request winning the window is unverified; the
  overwrite itself is deterministic once the window is hit. *Acceptance:* column-scoped updates for
  profile, locale and reminder writes, with role, status, email verification and quota written only by
  their own methods; extend the existing "a stale snapshot must not clear `PasswordHash`" test to those
  security fields.
  *Closed by the `fix(store): write profile, locale and reminder settings column by column` commit on
  `main`.* `UserStore.Update` is removed, so there is no whole-row save left to write a stale snapshot
  back. `UpdateProfile` (`internal/store/user.go:78`) writes display name, locale, timezone, day cutoff
  and learn-ahead only; `SetLocale` (`:93`) and `SetReminderHour` (`:102`) write their single column.
  The profile handler (`internal/web/profile.go:214`), the locale handler (`:255`), the notification
  preferences handler (`internal/web/mail_prefs_api.go:191`) and `DisableUser`/`EnableUser`
  (`internal/auth/account.go:292,300`, now `SetStatus`) use those setters; role, status, email, email
  verification and quota already had their own setters and are not written anywhere else.
  `TestUserStoreProfileUpdateKeepsSecurityColumns` (`internal/store/user_test.go`) replaces the older
  password-hash test: it reads a snapshot, then writes the password hash, role, status, email
  verification and quota through their setters, then saves the stale profile and asserts the profile
  change landed while all five security columns survived. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestUserStoreProfileUpdateKeepsSecurityColumns -count=1 -v`:
  ```
  === RUN   TestUserStoreProfileUpdateKeepsSecurityColumns
  === RUN   TestUserStoreProfileUpdateKeepsSecurityColumns/postgres
  === RUN   TestUserStoreProfileUpdateKeepsSecurityColumns/sqlite
  --- PASS: TestUserStoreProfileUpdateKeepsSecurityColumns (0.34s)
      --- PASS: TestUserStoreProfileUpdateKeepsSecurityColumns/postgres (0.27s)
      --- PASS: TestUserStoreProfileUpdateKeepsSecurityColumns/sqlite (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.354s
  ```
  The same scenario through the removed whole-row path (`Save` of the stale row, the body of the old
  `Update`, in a temporary test that was not committed) printed
  `role after stale whole-row save = "user", want "admin"`,
  `status after stale whole-row save = "active", want "disabled"`,
  `email_verified_at after stale whole-row save = <nil>` and
  `media_quota_bytes after stale whole-row save = <nil>` on both drivers. The reminder and digest
  test helpers now call `SetReminderHour`. `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -count=1`
  passes in `93.098s`, `go test ./internal/web/ -count=1` in `12.728s`, and
  `go test ./internal/auth/ ./internal/reminder/ ./internal/digest/ -count=1` passes.

- [x] **M19-8 The 0017 ruby migration rewrites already-converted content** — *should-fix.* The step
  applies a context-free string substitution to HTML that 0012 already produced
  (`internal/store/migrate_notes.go:803-805,848-880`): readings double-escape, a literal shortcode
  inside a code span is executed, and a cloze nested in a ruby annotation is cut at the first `}}` and
  left as literal text. *Reproduced* through a full `Sync` on both drivers: `{{ruby:B|a&b}} ->
  <ruby>B<rt>a&amp;amp;b</rt></ruby>` / `` `{{ruby:B|R}}` -> <code><ruby>B<rt>R</rt></ruby></code> `` /
  `{{ruby:{{c1::漢字}}|かんじ}} -> unchanged shortcode`. A database already marked version 17 needs its
  own remedy, not just a corrected step. *Acceptance:* convert by HTML context, skip `code`/`pre`, escape
  the reading once, keep legal nesting; add a regression that runs real old Markdown through 0012 and
  then 0017.
  *Closed by this commit (`fix(store): rewrite ruby shortcodes in their HTML context`).*
  `expandRubyShortcodes` (`internal/store/migrate_notes.go:847`) now walks the field's HTML and rewrites
  text nodes only. It skips a marker inside an element's tag, inside a `code` or `pre` element, and
  inside an HTML comment, using `migrationTagEnd` (`:904`) so a quoted `>` does not end a tag early.
  `rubyMarkerBody` (`:973`) and `splitRubyBody` (`:1009`) count `{{`/`}}` nesting, so the marker ends at
  its own closing braces and its base/reading separator is the first top-level `|`, which lets a cloze
  marker in the base survive. The reading is written as 0012 escaped it, with no second escape, and
  `validRubyBase` (`:1031`) accepts nested cloze markers only, so every other nested shortcode still
  leaves the marker literal as before.
  `TestMigrationConvertsRubyInHtmlContext` (`internal/store/migrate_notes_test.go:1220`) seeds old
  Markdown notes, runs the full `Sync` so 0012 renders them and 0017 rewrites the result, and asserts the
  three reported cases: the reading is escaped once, a code span stays literal, and the cloze keeps its
  nesting. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestMigrationConvertsRubyInHtmlContext -count=1 -v`:
  ```
  === RUN   TestMigrationConvertsRubyInHtmlContext
  === RUN   TestMigrationConvertsRubyInHtmlContext/sqlite
  === RUN   TestMigrationConvertsRubyInHtmlContext/postgres
  --- PASS: TestMigrationConvertsRubyInHtmlContext (0.77s)
      --- PASS: TestMigrationConvertsRubyInHtmlContext/sqlite (0.04s)
      --- PASS: TestMigrationConvertsRubyInHtmlContext/postgres (0.66s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.779s
  ```
  With only `internal/store/migrate_notes.go` reverted, the same run failed with
  `reading is escaped once: field = "<p><ruby>B<rt>a&amp;amp;b</rt></ruby></p>\n", want "<p><ruby>B<rt>a&amp;b</rt></ruby></p>\n"`,
  `code span is skipped: field = "<p><code><ruby>B<rt>R</rt></ruby></code></p>\n", want "<p><code>{{ruby:B|R}}</code></p>\n"`
  and `cloze in the base keeps its nesting: field = "<p>{{ruby:{{c1::漢字}}|かんじ}}</p>\n", want "<p><ruby>{{c1::漢字}}<rt>かんじ</rt></ruby></p>\n"`.
  `go vet ./internal/store/` and `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run
  'TestMigration|TestConvertNoteRuby|TestCheckNotesMigration|TestSync|TestApply' -count=1` pass.
  A database already at schema version 17 keeps what the old step wrote; repairing that content is a
  data migration over user notes, so it is not part of this fix and needs the maintainer's decision.

- [x] **M19-9 A batch note update silently clears `source_url` and `extra_json`** — *should-fix.* The
  bulk import path builds a fresh note without the stored `SourceURL`/`ExtraJSON` and the store writes
  both columns unconditionally (`internal/store/note.go:537-545`; callers
  `internal/api/service.go:473-480,600-608,700-702`), while a single PATCH preserves them. The import
  schema documents top-level `source_url`/`extra`, but `ImportNote` has no such fields, so following the
  schema does not help. *Reproduced* on both drivers: `before {"extra":"{\"k\":\"v\"}","source_url":
  "https://example.com/a"}` then `after {"extra":null,"source_url":null}`. *Acceptance:* the import path
  carries the metadata fields and distinguishes absent from empty, or updates only named columns; a
  regression creates a note, updates it through a batch import keyed by `external_ref`, and asserts the
  metadata survived, plus one that sends the top-level schema keys and asserts they land.
  *Closed by this commit (`fix(api): keep note metadata the import does not name`).*
  `ImportNote` (`internal/api/service.go:496`) now decodes the schema's top-level `source_url` and
  `extra`. The planning loop copies the stored columns into the note being updated and then applies only
  the keys the request named (`:628-645`): a nil `source_url` or an absent `extra` keeps the row's value,
  an explicit value writes it, and an empty `source_url` or a `null` extra clears the column. The store
  still writes both columns on every update, so no column-scoped store API was added; the transport now
  supplies the value the single-note update already copied forward.
  `TestBulkImportKeepsUnnamedNoteMetadata` and `TestBulkImportWritesTopLevelMetadata`
  (`internal/api/import_metadata_test.go`) cover the two halves: the first rewrites a note through the
  import and asserts both columns survive, then sends `source_url: ""` and asserts only that column
  clears; the second creates through the import with both top-level keys and reads them back from the
  row. Raw output of
  `go test ./internal/api/ -run 'TestBulkImportKeepsUnnamedNoteMetadata|TestBulkImportWritesTopLevelMetadata' -count=1 -v`:
  ```
  === RUN   TestBulkImportKeepsUnnamedNoteMetadata
  --- PASS: TestBulkImportKeepsUnnamedNoteMetadata (0.01s)
  === RUN   TestBulkImportWritesTopLevelMetadata
  --- PASS: TestBulkImportWritesTopLevelMetadata (0.02s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/api	0.046s
  ```
  With only the planning loop's metadata block removed, the same tests failed with
  `source_url after import = <nil>, want the stored value kept`,
  `extra_json after import = <nil>, want the stored value kept`,
  `source_url = <nil>, want the top-level value written` and
  `extra_json = <nil>, want the top-level value written`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/api/ -run 'TestImport|TestBulk|TestNote' -count=1`
  passes on both drivers, and `go test ./internal/mcp/ -count=1` passes.


- [x] **M19-10 Exported learning steps round to zero and lose the review history** — *should-fix.*
  `interval_days` is stored in fractional days but exported through `math.Round`
  (`internal/store/anki_export.go:547-560`), so a 10-minute step (600/86400 days) exports `ivl=0`;
  Anki encodes a sub-day interval as negative seconds, and the same conversion corrupts `lastIvl`.
  *Reproduced* by exporting and reading `collection.anki21` on both drivers: `10m learning review
  exported: ivl=0 lastIvl=0 type=0 (want ivl=-600)`. The scheduler does produce fractional days
  (`scheduler.go:322-337`, written at `submit.go:262`) and the import path already converts negative
  seconds back (`anki_import.go:1850-1860`), so this is not dirty data. Re-importing the archive into
  the Anki client is unverified. *Acceptance:* one interval encoding used by both `ivl` and `lastIvl` —
  negative seconds below a day, whole days above — with regressions for learning and relearning steps
  and for import→export, not just whole-day reviews.
  *Closed by this commit (`fix(store): keep sub-day revlog intervals in exported packages`).*
  `writeReviews` (`internal/store/anki_export.go:532`) passes every review interval through
  `ankiRevlogInterval` (`:566`): below one day it writes negative seconds
  (`-round(days*86400)`), one day or more it writes whole days, and the encoded value feeds
  `lastIvl` through the same `previousInterval` map, so both columns use one encoding. The
  reader already converted a negative interval back to days
  (`internal/store/anki_import.go:1849-1852`), so the value round-trips unchanged.
  `TestExportAnkiPackageSubDayIntervals` (`internal/store/anki_export_test.go`) exports a
  10-minute learning grade and a 20-minute relearning grade, imports that package, and exports
  it again; each archive must carry `ivl`/`lastIvl` `-600`/`0` and `-1200`/`-600` with revlog
  types 0 and 2. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestExportAnkiPackageSubDayIntervals -count=1 -v`:
  ```
  === RUN   TestExportAnkiPackageSubDayIntervals
  === RUN   TestExportAnkiPackageSubDayIntervals/sqlite
  === RUN   TestExportAnkiPackageSubDayIntervals/postgres
  --- PASS: TestExportAnkiPackageSubDayIntervals (0.68s)
      --- PASS: TestExportAnkiPackageSubDayIntervals/sqlite (0.01s)
      --- PASS: TestExportAnkiPackageSubDayIntervals/postgres (0.04s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.688s
  ```
  Before the change the same test failed on both drivers with
  `source export: revlog row 0 = {0 0 0}, want {-600 0 0}` and
  `source export: revlog row 1 = {0 0 2}, want {-1200 -600 2}`, and the same pair for
  `import to export`. `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run
  'TestExportAnkiPackage|TestAnkiExport|TestImportPackage|TestPackageImport' -count=1` passes on
  both drivers.

- [x] **M19-11 Clone fails on imported note types and drops custom styling** — *should-fix.* Clone
  reverse-engineers the source type into an engram stock kind and creates the default stock type
  (`internal/store/clone.go:68-93`, esp. `:74-80`). Anki-imported types carry `f0`/`f1`/`t0` identifiers,
  so they do not match a stock shape and the clone fails; a stock-shaped type with custom CSS clones as
  default styling. `SourceURL`/`ExtraJSON`, deck-scoped `media_names` and the card `deck_path` are not
  copied either. *Reproduced* on SQLite: `Clone(imported note type) error = clone note 1: its note type
  is not an engram stock type`, and with a source CSS marker the clone carries the stock `.card` block
  instead. The HTTP 500 mapping and the missing media names' effect on a rendered frame are unverified.
  *Acceptance:* copy the source type's config and content into a new ID instead of rebuilding a stock
  type; regressions clone a real imported type and a custom-CSS type and show the clone still renders
  after the source deck is deleted.
  *Closed by this commit (`fix(store): copy the source note type when cloning a deck`).* `Clone`
  (`internal/store/clone.go:72-90`) resolves each source note through `noteTypeFor` and copies that row
  once per source note type id into the cloned deck (`cloneNoteType`, `:118`): the config is written
  verbatim through `SetConfig`, so field ids, template ids, CSS and `anki_origin` survive, and the
  type's media references are written in the same transaction. The stock-kind rebuild is gone, and its
  two helpers `ensureStockNoteType` and `kindOfNote` (`internal/store/note.go`) had no other caller and
  were removed. `TestCloneCopiesSourceNoteType` (`internal/store/clone_content_test.go`) clones a deck
  whose type carries Anki `f0`/`f1`/`t0` ids and an `anki_origin`, and a stock-shaped type with custom
  CSS, then asserts the clone's type is a new row in the cloned deck with the source CSS, ids and
  origin, renders the cloned note's front, and renders it again after the source deck is deleted. Raw
  output of `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestCloneCopiesSourceNoteType -count=1 -v`:
  ```
  --- PASS: TestCloneCopiesSourceNoteType (0.37s)
      --- PASS: TestCloneCopiesSourceNoteType/sqlite (0.02s)
          --- PASS: TestCloneCopiesSourceNoteType/sqlite/imported_note_type (0.00s)
          --- PASS: TestCloneCopiesSourceNoteType/sqlite/stock_type_with_custom_styling (0.00s)
      --- PASS: TestCloneCopiesSourceNoteType/postgres (0.30s)
          --- PASS: TestCloneCopiesSourceNoteType/postgres/imported_note_type (0.03s)
          --- PASS: TestCloneCopiesSourceNoteType/postgres/stock_type_with_custom_styling (0.02s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.384s
  ```
  With `clone.go` and `note.go` reverted, the same run failed on both drivers with
  `Clone() error = clone note 1: its note type is not an engram stock type, want nil` and
  `cloned CSS = ".card { ... color: black; ... }", want the source CSS ".card { color: rgb(7, 8, 9); } ..." kept`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestClone|TestDeckCopyName|TestCloneKeepsClonedMediaReadable' -count=1` passes on both drivers and `go build ./...` passes.

- [x] **M19-12 A cloze→cloze change reports card removals that do not happen** — *should-fix.* Cloze
  produces an empty template plan, so every card is first listed as stale and soft-deleted, then the
  sync restores the same IDs (`internal/store/note_type_change.go:450-456,334-354,560-568,619-628`).
  The report counts the same cards in both `CardsAdded` and `CardsRemoved`, breaking the documented
  "a card is never counted in both" rule, and demands confirmation for deletions that do not occur.
  *Reproduced* on both drivers: `dry-run: CardsAdded:2 CardsRemoved:2`, then `unconfirmed: note type
  update would remove cards without confirmation`, then `confirmed: live cloze:1 id=1; live cloze:2
  id=2`. *Acceptance:* plan cards from the cloze numbers each note actually keeps, count surviving cards
  as moved/kept, and add a regression that a cloze→cloze change succeeds without confirmation and leaves
  IDs and progress untouched.
  *Closed by this commit (`fix(store): keep cloze cards across a cloze-to-cloze change`).*
  `moveNoteCards` (`internal/store/note_type_change.go:532`) now handles a cloze target before it builds a
  move plan: a cloze card's identity is the number inside its template, and the field content carries that
  number across the change, so every live card is counted as moved and nothing is returned as stale. The card
  sync then only restores or creates cards by that number, so no card is soft-deleted and no progress moves.
  A cloze change therefore needs no `confirm_card_removal`, and `CardsAdded`/`CardsRemoved` no longer count
  the same cards. `TestChangeNoteTypeClozeKeepsCards` (`internal/store/note_type_change_test.go`) creates a
  note with two cloze numbers, seeds progress on both cards, runs a dry run and then the change without
  confirmation, and asserts 2 moved / 0 added / 0 removed, the same card ids, the same live cards and both
  progress rows. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestChangeNoteTypeClozeKeepsCards -count=1 -v`:
  ```
  === RUN   TestChangeNoteTypeClozeKeepsCards
  === RUN   TestChangeNoteTypeClozeKeepsCards/sqlite
  === RUN   TestChangeNoteTypeClozeKeepsCards/postgres
  --- PASS: TestChangeNoteTypeClozeKeepsCards (0.30s)
      --- PASS: TestChangeNoteTypeClozeKeepsCards/sqlite (0.02s)
      --- PASS: TestChangeNoteTypeClozeKeepsCards/postgres (0.23s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.308s
  ```
  With only `internal/store/note_type_change.go` reverted, the same test failed on both drivers with
  `dry-run impact = {NotesMoved:1 CardsMoved:0 CardsAdded:2 CardsRemoved:2 ReviewsOnRemovedCards:2}, want 2
  moved, 0 added, 0 removed` and `ChangeNoteType() error = note type update would remove cards without
  confirmation: 2 card(s) would be removed, want success without confirmation`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestChangeNoteType|TestCleanupEmptyCards|TestNoteType' -count=1`
  passes and `go test ./internal/api/ -run TestNoteType -count=1` passes.

- [x] **M19-13 Editing a note type leaves the search index stale** — *should-fix.* The update rebuilds
  media references and cards but never recomputes `notes.search_text`, which depends on the field set
  and `exclude_from_search` (`internal/store/notetype.go:234-238`, `card.go:186-215`, derivation in
  `note_search.go:47-58`). Excluding a field leaves its old text searchable; re-including one does not
  make it searchable. *Reproduced* on both drivers: `search_text before = "alpha\nbeta"` then
  `search_text after exclude_from_search on A = "alpha\nbeta"` and `List(alpha) total=1`. *Acceptance:*
  recompute affected notes' `search_text` in the same transaction as the config version, with a
  regression that excluding, including and deleting a field each change the search result immediately.
  *Closed by this commit (`fix(store): recompute note search text on a note type edit`).*
  `regenerateNoteTypeCards` (`internal/store/card.go:194`) now computes each note's field values once and,
  in the same transaction as the config write, rewrites `search_text` through `noteSearchText` (`:206`)
  whenever the derived value differs. The loop's scope is unchanged, so it covers the live notes of the
  type — the same notes a card regeneration already walks; a soft-deleted note keeps its stored text until
  it is restored, which this entry does not cover. Excluding a field removes its text from the index,
  re-including it puts the text back, and deleting the field drops the text for good.
  `TestNoteTypeUpdateRewritesSearchText` (`internal/store/notetype_search_test.go`) creates a two-field
  type holding `alpha`/`beta`, then searches after each of the three edits: exclude the front (front 0
  rows, back 1), re-include it (front 1), and delete the back field (back 0, front 1). Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestNoteTypeUpdateRewritesSearchText -count=1 -v`:
  ```
  === RUN   TestNoteTypeUpdateRewritesSearchText
  === RUN   TestNoteTypeUpdateRewritesSearchText/sqlite
  === RUN   TestNoteTypeUpdateRewritesSearchText/postgres
  --- PASS: TestNoteTypeUpdateRewritesSearchText (0.33s)
      --- PASS: TestNoteTypeUpdateRewritesSearchText/sqlite (0.02s)
      --- PASS: TestNoteTypeUpdateRewritesSearchText/postgres (0.26s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.337s
  ```
  Before the change the same test failed on both drivers with
  `search for an excluded field = 1 rows, want 0` and `search for a deleted field = 1 rows, want 0`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestNoteType|TestNoteSearch|TestNoteList|TestNoteWrite|TestBackfillNoteSearchText' -count=1`
  passes on both drivers, and `go test ./internal/api/ -run TestNoteType -count=1` and
  `go test ./internal/web/ -run TestNoteType -count=1` pass.

- [ ] **M19-14 A tag filter matches different notes on SQLite and PostgreSQL** — *should-fix.* Stored
  `tags_json` is folded by SQL `LOWER`, the query string by Go `strings.ToLower`
  (`internal/store/note.go:847-861`); SQLite's built-in `LOWER` only folds ASCII, so a tag stored as
  `Été` matches nothing on SQLite even with its own spelling. The same predicate serves note listing and
  tag-scoped study. *Reproduced*: `sqlite: query "été" -> 0 row(s); query "ÉTÉ" -> 0 row(s)` versus
  `postgres: query "été" -> 1 row(s); query "ÉTÉ" -> 1 row(s)`. *Acceptance:* one Unicode normalisation
  rule on both the stored and the queried side (a derived normalised tag column with a backfill is the
  obvious shape), with a two-driver regression covering accented and non-ASCII case folding.

- [x] **M19-15 Deleting a deck leaves the note-type and media rows behind, so the files are never
  collected** — *should-fix.* The cascade clears notes and cards but not `note_types`, `media_names`
  or `media_note_types` (`internal/store/deck.go:332-355`; account deletion calls the same function at
  `user_admin.go:159-163`). The leftover template references keep the collector convinced the media is
  in use. *Reproduced* on both drivers: `note_types=1; media_names=1; media_note_types=1` and
  `MarkOrphanedMedia marked 0 rows, want 1`, with the uploader's usage still counting the byte. Usage not
  dropping to zero immediately is acceptable during a grace period; never being markable as orphan is
  not. *Acceptance:* in the same transaction clear `media_note_types` by note-type ID, then
  `note_types` and the deck's `media_names`; a regression deletes a deck with template assets, runs the
  mark and the expiry, and asserts no residue and the quota released.
  *Closed by this commit (`fix(store): remove deck note types and media names with the deck`).*
  `deleteDeckTx` (`internal/store/deck.go:332`) now deletes the deck's `media_note_types` rows by note-type id,
  then the deck's `note_types`, then its `media_names`, in the same transaction and after the notes are gone.
  That is the cascade `DeckStore.Delete`, `DeleteMany` and account deletion already share, so all three clean
  up the same scope. `TestDeckDeleteReleasesNoteTypeMedia` (`internal/store/deck_delete_media_test.go`) gives a
  deck a note type whose CSS references `_font.woff2`, registers that name and a 1000-byte upload, deletes the
  deck, and asserts every residue count is zero, the mark selects exactly the released sha, the expiry deletes
  it, and the uploader's usage returns to zero. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestDeckDeleteReleasesNoteTypeMedia -count=1 -v`:
  ```
  === RUN   TestDeckDeleteReleasesNoteTypeMedia
  === RUN   TestDeckDeleteReleasesNoteTypeMedia/sqlite
  === RUN   TestDeckDeleteReleasesNoteTypeMedia/postgres
  --- PASS: TestDeckDeleteReleasesNoteTypeMedia (0.29s)
      --- PASS: TestDeckDeleteReleasesNoteTypeMedia/sqlite (0.01s)
      --- PASS: TestDeckDeleteReleasesNoteTypeMedia/postgres (0.22s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.303s
  ```
  With only `internal/store/deck.go` reverted, the same test failed on both drivers with
  `residue *store.NoteType = 1 rows after deck delete, want 0`, the same line for `*store.MediaName` and
  `*store.MediaNoteType`, and `MarkOrphanedMedia() marked 0 rows, want 1: the template asset is now
  unreferenced`. `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run
  'TestDeck|TestUserDelete|TestMediaGC|TestUserMediaUsage|TestNoteType' -count=1` passes.

- [x] **M19-16 The collector keeps an orphan alive because another deck uses the same file name** —
  *should-fix.* The orphan query joins `media_note_types` to `media_names` on name alone
  (`internal/store/media_gc.go:23-24`) without carrying `note_types.deck_id`; media names are keyed by
  `(deck_id, name)`, not globally. Deck B's `_font.woff2` template reference therefore keeps deck A's
  already-unreferenced file of the same name alive. *Reproduced* by running the real GC SQL on SQLite:
  `GC with cross-deck name collision: marked=0 reclaimed=0`. The PostgreSQL repro of this scenario is
  unverified; the missing join is confirmed by reading the SQL. This is not a cross-account read, it is
  a cross-account effect on collection and disk use. *Acceptance:* join through `note_types` on
  `mn.deck_id = nt.deck_id AND mn.name = mnt.media_name`; a regression with two live decks holding the
  same file name and different hashes, one of them no longer referenced.
  *Closed by this commit (`fix(store): scope the note-type media reference to its own deck`).*
  `orphanCondition` (`internal/store/media_gc.go:22`) now joins `media_note_types` to `note_types` on
  `note_type_id` and to `media_names` on `mn.deck_id = nt.deck_id AND mn.name = mnt.media_name`, so a
  template reference only protects bytes named in its own deck. The same condition serves marking,
  clearing and deleting, so all three are fixed together.
  `TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan` (`internal/store/media_gc_test.go`) gives two decks
  a `_font.woff2` name pointing at different shas, puts the name on deck B's note type, and leaves deck A
  with no reference; it asserts exactly one row is marked, that row is deck A's sha, deletion past the
  grace period removes only it, and deck B's media row survives. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan -count=1 -v`:
  ```
  === RUN   TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan
  === RUN   TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan/postgres
  === RUN   TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan/sqlite
  --- PASS: TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan (0.32s)
      --- PASS: TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan/postgres (0.26s)
      --- PASS: TestMediaGCSameNameInAnotherDeckDoesNotKeepOrphan/sqlite (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.681s
  ```
  With only `internal/store/media_gc.go` reverted, the same run failed on both drivers with
  `MarkOrphanedMedia() marked 0 rows, want 1: another deck's same-name reference kept deck A's file alive`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run
  'TestMediaGC|TestMediaNote|TestMediaName|TestClearOrphaned|TestBackfillNoteTypeMedia|TestExportAnkiPackageRefusesUnreadableTemplateMedia|TestMediaReadable' -count=1`
  passes on both drivers, and `go test ./internal/media/ -count=1` passes.

- [ ] **M19-17 Soft-deleting a note frees its quota but keeps its media forever** — *should-fix.* The
  quota reference set ignores soft-deleted notes, the pending set counts nothing as long as any
  `media_notes` row exists, and the collector keeps the mappings so the media can be restored
  (`internal/store/media_quota.go:38-43`, `media_gc.go:13-24`). Media referenced only by soft-deleted
  notes is therefore neither charged nor collected: upload, reference, soft-delete, repeat, and disk use
  grows while reported usage returns to zero. *Reproduced* on SQLite and confirmed on PostgreSQL:
  `before delete: uploader usage = 1000 bytes` / `after soft delete: uploader usage = 0 bytes` /
  `1 mapping rows, marked=0, reclaimed=0`. *Acceptance:* one policy for physical retention and charging
  — restorable content still needs a budget or a retention cap, or it becomes pending when only
  soft-deleted notes reference it, or restoration is explicitly not guaranteed once collected — plus a
  regression that runs several upload/soft-delete/collect rounds against a budget.

- [x] **M19-18 Two concurrent `into_deck` imports can point a note at another package's media** —
  *should-fix.* `renameAnkiMedia` reads a snapshot of the deck's names, then registers with
  `ON CONFLICT` overwriting the hash (`internal/store/anki_import.go:1982-2046`, `media_names.go:61-80`).
  Two transactions that both see `cover.png` free both keep the name, and the later commit points it at
  its own bytes; the `into_deck` target is resolved by a plain SELECT (`:1926-1931`) with no row lock, and
  the default import slot allows two concurrent imports. *Reproduced* on PostgreSQL through the
  production rename/register calls: `after both commits: media_names rows=1; cover.png -> <B's hash>`
  with A and B holding different bytes. The full note/card import in that interleaving is unverified.
  *Acceptance:* take a per-deck lock before the naming decision, or insert conditionally so a different
  hash cannot overwrite and rename/rewrite the package's references on conflict; a regression imports two
  packages with the same file name and different bytes into one deck concurrently.
  *Closed by this commit (`fix(store): serialise concurrent imports into one deck`).*
  `importAnkiInTx` (`internal/store/anki_import.go:1040`) now calls `lockAnkiTargetDeck` (`:2014`) once
  the target deck is resolved and before `replace_deck` clears notes or the media naming decision
  runs. The no-op `UPDATE decks SET name = name` takes the deck row's lock on PostgreSQL, so a second
  import into the same deck waits for the first to commit and `renameAnkiMedia` then reads the
  committed names instead of a pre-commit snapshot; SQLite's production connection is a single one,
  whose write transaction already serialises the import, so a plain update is enough there.
  `TestAnkiImportSerialisesConcurrentIntoDeckMediaNames`
  (`internal/store/anki_import_deck_lock_test.go`) prepares two packages that both carry
  `placeholder.png` with different bytes, runs the first import in a transaction it leaves open,
  starts the second import, observes through `pg_stat_activity` that the second backend is waiting on
  the deck row lock, then commits. It asserts two `media_names` rows, the first package keeping
  `placeholder.png`, the second renamed, and the first package's imported note still referencing
  `placeholder.png`. Raw output of
  `TEST_PG_DSN=... go test ./internal/store/ -run TestAnkiImportSerialisesConcurrentIntoDeckMediaNames -count=1 -v`:
  ```
  === RUN   TestAnkiImportSerialisesConcurrentIntoDeckMediaNames
  --- PASS: TestAnkiImportSerialisesConcurrentIntoDeckMediaNames (0.31s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.320s
  ```
  With `lockAnkiTargetDeck` returning before its update, the same test failed with `the second import
  did not wait for the target deck's row lock` and
  `media_names rows = 1 ([{DeckID:1 Name:placeholder.png Sha256:52e7287e...}]), want 2: one name per
  package's bytes`, that sha being the second package's: the entry's defect, reproduced through the
  whole store import rather than the rename/register calls alone.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestAnkiImport|TestPackage|TestImportPackage|TestExportAnkiPackage|TestAnkiExport|TestMedia' -count=1`
  passes on both drivers in `14.235s`.

- [x] **M19-19 A media-name clash rewrite corrupts HTML attribute names instead of the reference** —
  *should-fix.* The clash handler replaces the first occurrence of the name inside the whole match
  (`internal/store/media_clash.go:107-113`); the name is an attribute value, so with a short extensionless
  name the first hit can be inside `src`, `href` or `url(`. *Reproduced* by calling the production
  function: `<img src="s"> -> <img s-abc.pngrc="s">`, `<a href="h">x</a> -> <a h-abc.pngref="h">x</a>`,
  `background:url(u) -> background:u-abc.pngrl(u)`. The end-to-end import of such a package is
  unverified. *Acceptance:* replace inside the value capture group, never the first substring of the
  whole match; cover single and double quotes, unquoted `src`, CSS `url()`, `sound:` and short names.
  *Closed by this commit (`fix(store): rewrite only the media reference, not the attribute name`).*
  `rewriteMediaRefs` (`internal/store/media_clash.go:90`) now calls `replaceMediaRefNamesIn`, which takes
  the capture offsets from `FindStringSubmatchIndex` on the match and splices the new name between them,
  so only the value changes; the previous `strings.Replace(match, name, newName, 1)` is gone.
  `TestRewriteMediaRefsRewritesOnlyTheReference` (`internal/store/media_clash_test.go`) covers double- and
  single-quoted `src`, unquoted `src`, `href`, all three `url()` quoting forms, `[sound:]`, a reference
  whose name equals the attribute or function name (`src`, `href`, `url`), a second occurrence in `alt`
  that must stay, and ordinary text. Raw output of
  `go test ./internal/store/ -run 'TestRewriteMediaRefsRewritesOnlyTheReference|TestAddHashSuffixToFileStem' -count=1 -v`:
  ```
  --- PASS: TestAddHashSuffixToFileStem (0.00s)
  --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/double_quoted_src (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/single_quoted_src (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/unquoted_src (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/href (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/css_double_quoted_url (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/css_single_quoted_url (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/css_quoted_url (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/sound (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/src_name_equals_the_attribute (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/href_name_equals_the_attribute (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/css_name_equals_the_function (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/only_the_reference_is_rewritten (0.00s)
      --- PASS: TestRewriteMediaRefsRewritesOnlyTheReference/ordinary_text_is_untouched (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.013s
  ```
  The same test against the previous body failed with `rewriteMediaRefs("<img src=\"s\">") =
  "<img s-abc.pngrc=\"s\">", want "<img src=\"s-abc.png\">"`, plus the same corruption for `href`,
  `url()`, `[sound:]` and the name-equals-attribute cases; `<p>s</p>` and the `alt` occurrence stayed
  intact as expected. `TestAnkiImportKeepsFilesThatExistedBefore` passes, so the import path that calls the
  rewriter still runs.

- [x] **M19-20 A preset write-back lets the optimiser and the settings form overwrite each other** —
  *should-fix.* The settings PATCH changes scheduling fields but carries the stale weights, the
  optimiser changes weights but carries stale scheduling fields, and `Update` writes both groups with no
  compare-and-set (`internal/store/preset.go:227-240`; callers `internal/web/presets_api.go:193-204`,
  `internal/jobs/optimize.go:86-110`); the re-read of `existing` is only used for authorisation.
  *Reproduced* on both drivers with two stale snapshots: `after stale scheduling Update: weights_json=[0.5,
  ...,2.5] review_count=512` and `after optimizer write-back: retention=0.90 steps="1m,10m" max=36500
  fuzz=true`, each undoing the other's committed change. *Acceptance:* separate the scheduling columns
  from the weight columns, or use an explicit field mask (a version check if edit conflicts must be
  surfaced); regressions show a settings save after a reset does not restore weights, and an optimiser
  write from a stale snapshot does not roll settings back.
  *Closed by this commit (`fix(store): write only the preset columns each caller owns`).*
  `PresetStore` now exposes two scoped writers beside the whole-row `Update` (`internal/store/preset.go:214`):
  `UpdateScheduling` (`:221`) writes `name`, the three scheduling fields, `enable_fuzz` and
  `grade_mapping_json`, and `UpdateWeights` (`:228`) writes `weights_json`, `weights_optimized_at` and
  `weights_review_count`; both share one `update` that keeps the ownership check and validation
  (`:235`). The settings PATCH calls `UpdateScheduling` (`internal/web/presets_api.go:204`) and the
  optimiser write-back calls `UpdateWeights` (`internal/jobs/optimize.go:110`), so neither writes the
  snapshot it does not own. `Update` is unchanged as the whole-row writer for a fresh read; no schema
  or version check was needed because the two callers' column groups are disjoint.
  `TestPresetScopedUpdatesDoNotOverwriteEachOther` (`internal/store/preset_test.go`) takes a settings
  snapshot, resets the weights, saves the stale scheduling snapshot and asserts `weights_json` stays
  NULL; it then takes a stale optimiser snapshot, saves a new retention through the settings writer,
  writes the stale weights and asserts the new retention and the new weights both survive. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestPresetScopedUpdatesDoNotOverwriteEachOther -count=1 -v`:
  ```
  === RUN   TestPresetScopedUpdatesDoNotOverwriteEachOther
  === RUN   TestPresetScopedUpdatesDoNotOverwriteEachOther/sqlite
  === RUN   TestPresetScopedUpdatesDoNotOverwriteEachOther/postgres
  --- PASS: TestPresetScopedUpdatesDoNotOverwriteEachOther (0.30s)
      --- PASS: TestPresetScopedUpdatesDoNotOverwriteEachOther/sqlite (0.01s)
      --- PASS: TestPresetScopedUpdatesDoNotOverwriteEachOther/postgres (0.23s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.305s
  ```
  With both scoped methods temporarily routed through the whole-row write, the same test failed on both
  drivers with `weights_json after a settings save = "[0.4,0.6,2.4]", want nil: the reset must survive
  it` and `desired_retention after a stale optimiser write = 0.9, want 0.85: the settings save must
  survive it`. `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestPreset|TestOptimise|TestOptimize' -count=1`
  passes in `3.543s`, `go test ./internal/web/ -run 'TestPreset|TestOptim|TestReview' -count=1` and
  `go test ./internal/jobs/ -run 'TestOptim' -count=1` pass.

- [x] **M19-21 Two concurrent `AddTags` both succeed and one addition is lost** — *should-fix.* Tags are
  read outside the transaction, merged in Go, and written unconditionally inside it
  (`internal/store/note.go:696-727,731-743`); serialising the write transaction cannot save a stale read,
  so production SQLite with a single connection has the same defect. *Reproduced* with 200 notes and 20
  rounds on SQLite (confirmed `MaxOpenConns=1`) and on PostgreSQL: `errors nil; affected A=200 B=200;
  lost tag=true; first row "[\"a\"]"` and `rounds=20 lost=20` on both drivers, with both calls' errors
  checked so this is not a swallowed write. `AddTags`, `RemoveTags` and `SetTags` share the path; the
  reproduction pins two `AddTags`. *Acceptance:* move read, merge and write into one locked transaction,
  or compare-and-set on the old tag value with a retry (moving only the SELECT into a plain PostgreSQL
  transaction is not enough); add a concurrent union/difference regression.
  *Closed by this commit (`fix(store): merge tags inside one locked transaction`).* `applyTags`
  (`internal/store/note.go:698`) now opens the transaction first, reads the rows through
  `loadNotesForTagging` with `SELECT ... FOR UPDATE` on PostgreSQL (SQLite keeps its single write
  connection), and computes the per-row change through `planTagChanges` before writing, so the read cannot
  be stale. The `dryRun` path still reads without a transaction because it writes nothing. On PostgreSQL a
  second transaction waits on the locked rows and re-reads the committed value; on SQLite the single
  connection serialises the whole transaction.
  `TestConcurrentAddTagsKeepsBothAdditions` and `TestConcurrentRemoveTagsKeepsBothRemovals`
  (`internal/store/note_tags_concurrency_test.go`) run 10 rounds of two concurrent operations over 20 notes
  per driver, check both errors, and assert the union kept both tags and the difference removed both. The
  SQLite database is capped at one connection, as `Open` does, so the test exercises the production
  serialisation. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestConcurrentAddTagsKeepsBothAdditions|TestConcurrentRemoveTagsKeepsBothRemovals' -count=1 -v`:
  ```
  --- PASS: TestConcurrentAddTagsKeepsBothAdditions (0.51s)
      --- PASS: TestConcurrentAddTagsKeepsBothAdditions/sqlite (0.03s)
      --- PASS: TestConcurrentAddTagsKeepsBothAdditions/postgres (0.17s)
  --- PASS: TestConcurrentRemoveTagsKeepsBothRemovals (0.46s)
      --- PASS: TestConcurrentRemoveTagsKeepsBothRemovals/sqlite (0.04s)
      --- PASS: TestConcurrentRemoveTagsKeepsBothRemovals/postgres (0.17s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.985s
  ```
  With only the production change reverted, the same run failed on both drivers:
  `round 0 note 1 tags = [a], want both a and b` (sqlite), `round 1 note 1 tags = [b], want both a and b`
  (postgres), `round 1 note 1 tags = [b], want both removals kept` (sqlite) and `round 0 note 1 tags =
  [a], want both removals kept` (postgres). `go test ./internal/store/ -run 'TestWhereNoteTags|TestDeckTag'
  -count=1`, `go test ./internal/api/ -run 'TestBulkNotes|TestReviewTags' -count=1` and
  `go test ./internal/mcp/ -run 'Tag' -count=1` pass.

- [x] **M19-22 A large but legal deck cannot be exported** — *should-fix.* Export expands every note
  and card ID into one `IN` list with no batching (`internal/store/anki_export.go:68-71,122-125,135-138`).
  SQLite's bind cap is reached at 33,000 notes, and one collection entry can hold any number of notes,
  so the zip entry limit is no protection. *Reproduced* with 33,000 real notes: `export anki package:
  list cards: SQL logic error: too many SQL variables (1)`. PostgreSQL's 65,535-parameter ceiling is
  visible in the same queries; the whole oversized export on PostgreSQL is unverified. The call returns
  an error rather than crashing. *Acceptance:* join or subquery the deck inside the database, or load in
  fixed batches preserving order and dedup, at all three sites; a regression exports a deck above the
  SQLite bind cap and one with few notes but many cards.
  *Closed by this commit (`fix(store): keep the deck's id lists on the database side when exporting`).*
  `ExportAnkiPackage` (`internal/store/anki_export.go:55`) no longer builds `noteIDs` or binds `cardIDs`
  into `IN` lists. Cards are selected with `note_id IN (?)` over a `notes` subquery for the deck, and the
  progress and revlog queries (`:126,139`) use `card_id IN (?)` over a `cards` subquery that itself
  selects the deck's notes. Orders are unchanged, the subqueries inherit the soft-delete scope of the
  original `Find` calls, and the id slices that remain are only used for length checks and archive
  mapping.
  `TestExportAnkiPackageAboveSQLiteBindCap` (`internal/store/anki_export_test.go`) is SQLite-only
  because the cap is SQLite's: one deck has 33,000 notes (33,000 note ids and 33,000 card ids), and a
  second has 10 notes with 3,300 cards each (a small note-id list and a 33,000 card-id list). Both
  exports run with `IncludeProgress` and `IncludeReviews`, and the archive must carry at least the
  seeded notes and cards. Raw output of
  `go test ./internal/store/ -run TestExportAnkiPackageAboveSQLiteBindCap -count=1 -v`:
  ```
  === RUN   TestExportAnkiPackageAboveSQLiteBindCap
      anki_export_test.go:759: TEST_PG_DSN not set: skipping the PostgreSQL path on this machine
  === RUN   TestExportAnkiPackageAboveSQLiteBindCap/many_notes
  === RUN   TestExportAnkiPackageAboveSQLiteBindCap/many_cards_from_few_notes
  --- PASS: TestExportAnkiPackageAboveSQLiteBindCap (5.34s)
      --- PASS: TestExportAnkiPackageAboveSQLiteBindCap/many_notes (4.03s)
      --- PASS: TestExportAnkiPackageAboveSQLiteBindCap/many_cards_from_few_notes (1.30s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	5.357s
  ```
  With only `internal/store/anki_export.go` reverted, the same run failed in both subtests, the first
  with `export anki package: list cards: SQL logic error: too many SQL variables (1)` and the second
  with `export anki package: list progress: SQL logic error: too many SQL variables (1)`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestExportAnkiPackage|TestAnkiExport|TestImportPackage|TestPackageImport|TestMedia' -count=1`
  passes on both drivers.

- [ ] **M19-23 The due forecast uses a fixed 24 hours in place of the local day boundary** — *should-fix.*
  `stats.go` builds forecast buckets from `start+24h` while the review-day code uses local `AddDate`
  (`internal/store/stats.go:92-95,111-116`), so the boundary is an hour off on the day before a DST
  change; `<=` also puts a card due exactly at the next cut-off into today. *Reproduced* on both drivers
  with the clock pinned to New York noon: `spring eve: NextStart=2026-03-08T08:00Z; e1=09:00Z` and
  `due=2026-03-08T08:30Z: want tomorrow, got today`; `fall eve: NextStart=2026-11-01T09:00Z; e1=08:00Z`
  and `due=2026-11-01T08:30Z: want today, got tomorrow`. *Acceptance:* derive every boundary from the
  local calendar day, use half-open intervals, and test both sides of a DST change and the exact
  cut-off. The neighbouring `reviewday.Start` behaviour inside the DST cut-off hour needs fixing
  together with this.

- [x] **M19-24 The upgrade preflight cannot inspect an early database and calls unconvertible data
  clean** — *should-fix.* The preflight probes `decks.public_id` for compatibility but still selects
  `notes.public_id` unconditionally (`internal/store/migrate_notes.go:79-90`), so a genuinely early
  database fails with a SQL error; separately, a `fields_json` decode failure is skipped with `continue`,
  so the preflight returns empty `Blocking` although `0012` will fail on that same row. *Reproduced* on
  both drivers: `SQLite old schema: no such column: public_id` / `PG old schema: column "public_id" does
  not exist (SQLSTATE 42703)` and `corrupt: preflight={Blocking:[] Warnings:[]} error=<nil>` followed by
  `conversion_error=... decode fields_json: invalid character 'b'`. The CLI then prints that the
  migration can run. *Acceptance:* probe `notes` the way `decks` is probed and fall back to numeric IDs;
  a decode failure is `Blocking` or a non-zero error; regressions start from a schema without
  `public_id` in either table and from a corrupt field row.
  *Closed by this commit (`fix(store): make the upgrade preflight read an early notes table`).*
  `notesMigrationReport` (`internal/store/migrate_notes.go:60`) now probes `notes.public_id` with
  `HasColumn` and selects it only when present, exactly like `deckPublicIDs`, so an early database scans
  and reports by numeric note and deck ids. The note and deck ids are resolved before the decode, and an
  unreadable `fields_json` now appends a `Blocking` issue with `Field: "fields_json"` and
  `Reason: "fields_json is unreadable: <decode error>"` instead of being skipped; the same check is what
  `0012` enforces, so the CLI no longer calls such a database clean.
  `TestCheckNotesMigrationWithoutNotePublicID` and `TestCheckNotesMigrationReportsUnreadableFields`
  (`internal/store/migrate_notes_test.go`) build the schema without `notes.public_id` (deck public id
  present) and with a corrupt `fields_json` row, and assert a report that names the numeric note id and
  blocks respectively. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestCheckNotesMigrationWithoutNotePublicID|TestCheckNotesMigrationReportsUnreadableFields' -count=1 -v`:
  ```
  === RUN   TestCheckNotesMigrationWithoutNotePublicID
  === RUN   TestCheckNotesMigrationWithoutNotePublicID/sqlite
  === RUN   TestCheckNotesMigrationWithoutNotePublicID/postgres
  --- PASS: TestCheckNotesMigrationWithoutNotePublicID (0.06s)
      --- PASS: TestCheckNotesMigrationWithoutNotePublicID/sqlite (0.00s)
      --- PASS: TestCheckNotesMigrationWithoutNotePublicID/postgres (0.04s)
  === RUN   TestCheckNotesMigrationReportsUnreadableFields
  === RUN   TestCheckNotesMigrationReportsUnreadableFields/sqlite
  === RUN   TestCheckNotesMigrationReportsUnreadableFields/postgres
  --- PASS: TestCheckNotesMigrationReportsUnreadableFields (0.34s)
      --- PASS: TestCheckNotesMigrationReportsUnreadableFields/sqlite (0.02s)
      --- PASS: TestCheckNotesMigrationReportsUnreadableFields/postgres (0.27s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.415s
  ```
  With only `internal/store/migrate_notes.go` reverted, the same run failed on both drivers with
  `CheckNotesMigration() error = check notes migration: scan notes: SQL logic error: no such column: public_id (1), want a report`
  and `CheckNotesMigration() error = check notes migration: scan notes: ERROR: column "public_id" does not exist (SQLSTATE 42703), want a report`,
  plus `report.Blocked() = false, want the corrupt row to block`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestCheckNotesMigration|TestMigration' -count=1`
  passes on both drivers.

- [ ] **M19-25 The unique-constraint test still passes with the business constraints dropped** —
  *should-fix.* The duplicate object is built as `dup := first; dup.ID = 0`, which keeps `PublicID`
  (`internal/store/models_test.go:119-121,131-133`), so the insert can fail on `public_id` alone while the
  test treats any error as proof that the composite business constraint exists. *Reproduced* by dropping
  `idx_notes_deck_external_ref` and `idx_cards_note_template` from the test database under an overlay,
  leaving the inserts and assertions unchanged: the test still reports `--- PASS`, and the errors it
  actually saw were `notes.public_id` / `idx_notes_public_id (SQLSTATE 23505)`. A follow-up probe that
  cleared `PublicID` inserted both business duplicates. The production indexes do exist; this is a
  false-negative test, not a missing constraint. *Acceptance:* clear `ID` and `PublicID` or build a
  fresh object duplicating only the business key, assert the specific constraint error rather than any
  error, and keep the mutation control so deleting the business index makes the test fail.

- [ ] **M19-26 Dead `.edeck` preset and limit code is kept alive by tests that exercise nothing in
  production** — *nit.* `createPresetFromPackage`, `PackagePreset`, `PackageLimits` and
  `DefaultPackageLimits` belong to the removed `.edeck` path; they have no production caller and only
  `preset_fuzz_test` and `package_preset_validation_test` keep them compiling
  (`internal/store/package_import.go:35-45,618-668`, `package.go:81-92`). Real `.apkg` imports use
  `createAnkiPreset` and `PackageImportLimits` (`anki_import.go:1940-1965`). Two sets of defaulting and
  repair logic mean green tests can cover a path nothing runs. This one is static call-graph evidence
  from searching the four symbols. *Acceptance:* delete the dead helpers, types and the tests that only
  serve them, or move the still-valid boundary cases onto `createAnkiPreset`/the import path; do not keep
  a second set of business rules to keep a test alive. Confirm no compatibility promise exists before
  removing an exported symbol.


- [x] **M19-27 A step the preset form accepts silently replaces every scheduling parameter with the
  library defaults** — *Blocker.* `Parameters`/`parametersWithWeights` build `fsrs.Parameters` and return
  them without validating (`internal/schedule/scheduler.go:151-160,172-192`); the only validation is inside
  `fsrs.NewFSRS`, whose failure mode is to swap in `DefaultParam()` — retention 0.9, maximum 36 500,
  steps 1m/10m — with no error and no log (`go-fsrs/v4@v4.0.0/fsrs.go:18-30`). The form accepts the step
  because `validStepSpec` checks the numeric prefix and the unit but never scales by the unit
  (`internal/web/presets.go:49-79`), so `1e308d` passes. *Reproduced* end to end through `Submit` on both
  drivers: preset asking 0.8 / 100 with `learning_steps "1e308d"` yields `effective retention=0.9000
  max=36500 steps=[1 10]`, and the review row is written from those. `CompareFit` (`fit.go:44-63`) builds
  its instances the same way, so an optimiser verdict for such a preset compares two fits that both ignore
  the user's settings. *Acceptance:* validate the assembled parameters and return an error instead of
  letting the library substitute defaults; one step validator for the repository that checks finiteness
  and a bound **after** scaling by the unit, refusing `1e308d` with `preset_steps_invalid`; a table case
  asserting `NewScheduler` errors and a `internal/web` case asserting the PATCH is refused.
  *Closed by this commit (`fix(schedule): refuse scheduling parameters the library would replace`).*
  `parametersWithWeights` (`internal/schedule/scheduler.go:192`) now runs `validateParameters` (`:206`)
  before returning, so `NewScheduler` and `CompareFit` both report an error for retention outside (0, 1], a
  maximum interval outside (0, 36 500] days, a negative or non-finite step, or a non-finite weight, instead
  of `fsrs.NewFSRS` substituting `DefaultParam()`. The check mirrors what the library still rejects after
  its weight clipping, so a weight outside a clip range is still clamped rather than refused.
  The step half of the acceptance was reached by `4a4f573` (M19-28), which applied the finiteness and
  post-scaling bound to all four step validators; this commit does not merge them into one function,
  because they live in three packages and a merge would rewrite unrelated call sites and import behaviour.
  `1e308d` is refused with `preset_steps_invalid`.
  `TestNewSchedulerRefusesParametersTheLibraryWouldReplace` (`internal/schedule/parameter_validation_test.go`)
  is the table of six parameters plus a defaults control. Raw output of
  `go test ./internal/schedule/ -run TestNewSchedulerRefusesParametersTheLibraryWouldReplace -count=1 -v`:
  ```
  === RUN   TestNewSchedulerRefusesParametersTheLibraryWouldReplace
  === RUN   TestNewSchedulerRefusesParametersTheLibraryWouldReplace/retention_zero
  === RUN   TestNewSchedulerRefusesParametersTheLibraryWouldReplace/retention_above_one
  === RUN   TestNewSchedulerRefusesParametersTheLibraryWouldReplace/maximum_interval_zero
  === RUN   TestNewSchedulerRefusesParametersTheLibraryWouldReplace/maximum_interval_above_the_library_ceiling
  === RUN   TestNewSchedulerRefusesParametersTheLibraryWouldReplace/negative_learning_step
  === RUN   TestNewSchedulerRefusesParametersTheLibraryWouldReplace/negative_relearning_step
  --- PASS: TestNewSchedulerRefusesParametersTheLibraryWouldReplace (0.00s)
      --- PASS: TestNewSchedulerRefusesParametersTheLibraryWouldReplace/retention_zero (0.00s)
      --- PASS: TestNewSchedulerRefusesParametersTheLibraryWouldReplace/retention_above_one (0.00s)
      --- PASS: TestNewSchedulerRefusesParametersTheLibraryWouldReplace/maximum_interval_zero (0.00s)
      --- PASS: TestNewSchedulerRefusesParametersTheLibraryWouldReplace/maximum_interval_above_the_library_ceiling (0.00s)
      --- PASS: TestNewSchedulerRefusesParametersTheLibraryWouldReplace/negative_learning_step (0.00s)
      --- PASS: TestNewSchedulerRefusesParametersTheLibraryWouldReplace/negative_relearning_step (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/schedule	0.008s
  ```
  With the `validateParameters` call removed the same table failed on all six cases with
  `NewScheduler() error = nil, want the parameter the library would replace refused`. The form half is
  pinned by `TestPresetWriteRejectsInvalidInput` (`internal/web/presets_api_test.go:191`), whose
  `steps_overflow_the_cap_after_scaling` case sends `learning_steps "1e308d"` and expects
  `preset_steps_invalid`; it and the other preset cases pass:
  `go test ./internal/web/ -run TestPresetWriteRejectsInvalidInput -count=1 -v` ends
  `--- PASS: TestPresetWriteRejectsInvalidInput (0.33s)` / `ok git.nite07.com/nite/engram/internal/web 0.333s`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/schedule/ -count=1` passes in 9.819s, and
  `./internal/store/ -run 'TestPreset|TestRepairPresetSchedulerLimits|TestMigratePreset|TestCreateAnkiPreset'`
  and `./internal/jobs/` pass on both drivers.


- [x] **M19-28 An accepted step overflows `time.Duration` and writes a due date in 1734 with an interval
  of 0 into the review log** — *Blocker.* `stepMinutes` bounds nothing (`internal/schedule/scheduler.go:
  221-247`) and `validStepSpec` accepts `200000d`; 200 000 × 1440 minutes overflows the nanosecond
  duration (`go-fsrs steps.go:24-26`) and `applyStep` adds it, producing `due_at` in the past with
  `state=review` and `scheduled_days=200000` (`scheduler_basic.go:23-32`). `outcomeFrom`
  (`scheduler.go:321-339`) then clamps the negative difference, so `reviews.interval_days` records 0 for a
  grade the scheduler believes scheduled 200 000 days — in the one table AGENTS §2.3.2 says cannot be
  reconstructed. *Reproduced* on both drivers: `step="200000d" ... state=review
  due=1734-06-23T12:12:43Z scheduled_days=200000 review.interval_days=0`, with `validStepSpec("200000d")
  = true`. *Acceptance:* reject any step whose minute value is not finite or exceeds 36 500 days in every
  validator; treat `due.Before(now)` on a non-New outcome as an error rather than clamping the interval;
  regressions for one step just under and one just over the cap, neither writing a review row.
  *Closed by `4a4f573`.* `stepMinutes` (`internal/schedule/scheduler.go:228`) now scales by the unit
  first and refuses a non-finite result or one above 36,500 days (52,560,000 minutes); the same bound
  is applied by the other three step validators: `validStepSpec` (`internal/web/presets.go:51`),
  `validPackageSteps` (`internal/store/package_import.go:679`) and `validAnkiSteps`
  (`internal/store/anki_import.go:1972`). `outcomeFrom` (`internal/schedule/scheduler.go:345`) now
  returns an error when a non-New outcome's due date is before now instead of clamping
  `interval_days` to zero; `Next` and `Preview` propagate it, and `Submit` returns before it writes
  either `card_states` or `reviews`. `TestStepsOutsideSchedulerRangeAreRefused`,
  `TestSubmitRefusesDueDateBeforeNow` and `TestLegitimateLongStepStillWritesReview`
  (`internal/schedule/step_range_test.go`) cover the parser, both drivers through `Submit`, and the
  due-before-now branch with a scheduler built past validation so it really overflows. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/schedule/ -run
  'TestStepsOutsideSchedulerRangeAreRefused|TestSubmitRefusesDueDateBeforeNow|TestLegitimateLongStepStillWritesReview' -count=1 -v`:
  ```
  === RUN   TestStepsOutsideSchedulerRangeAreRefused
  === RUN   TestStepsOutsideSchedulerRangeAreRefused/just_under_the_cap
  === RUN   TestStepsOutsideSchedulerRangeAreRefused/at_the_cap
  === RUN   TestStepsOutsideSchedulerRangeAreRefused/one_day_over_the_cap
  === RUN   TestStepsOutsideSchedulerRangeAreRefused/the_overflow_from_the_report
  === RUN   TestStepsOutsideSchedulerRangeAreRefused/non-finite_after_scaling
  === RUN   TestStepsOutsideSchedulerRangeAreRefused/not_a_number
  --- PASS: TestStepsOutsideSchedulerRangeAreRefused (0.00s)
  --- PASS: TestStepsOutsideSchedulerRangeAreRefused/just_under_the_cap (0.00s)
  --- PASS: TestStepsOutsideSchedulerRangeAreRefused/at_the_cap (0.00s)
  --- PASS: TestStepsOutsideSchedulerRangeAreRefused/one_day_over_the_cap (0.00s)
  --- PASS: TestStepsOutsideSchedulerRangeAreRefused/the_overflow_from_the_report (0.00s)
  --- PASS: TestStepsOutsideSchedulerRangeAreRefused/non-finite_after_scaling (0.00s)
  --- PASS: TestStepsOutsideSchedulerRangeAreRefused/not_a_number (0.00s)
  === RUN   TestSubmitRefusesDueDateBeforeNow
  === RUN   TestSubmitRefusesDueDateBeforeNow/sqlite
  === RUN   TestSubmitRefusesDueDateBeforeNow/postgres
  --- PASS: TestSubmitRefusesDueDateBeforeNow (0.32s)
  --- PASS: TestSubmitRefusesDueDateBeforeNow/sqlite (0.00s)
  --- PASS: TestSubmitRefusesDueDateBeforeNow/postgres (0.01s)
  === RUN   TestLegitimateLongStepStillWritesReview
  === RUN   TestLegitimateLongStepStillWritesReview/sqlite
  === RUN   TestLegitimateLongStepStillWritesReview/postgres
  --- PASS: TestLegitimateLongStepStillWritesReview (0.28s)
  --- PASS: TestLegitimateLongStepStillWritesReview/sqlite (0.00s)
  --- PASS: TestLegitimateLongStepStillWritesReview/postgres (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/schedule	0.610s
  ```
  The same tests against the previous parser failed with `NewScheduler("36501d") error = nil, want the
  step refused`, `NewScheduler("200000d") error = nil`, `NewScheduler("1e308d") error = nil` and
  `Submit() error = nil, want the out-of-range due date refused`; the web and store cases likewise
  failed on their new over-cap rows. One reading note: the acceptance's "neither writing a review row"
  would contradict its own rule ("exceeds 36 500 days" is the rejection threshold), so the regression
  treats 36,499 and 36,500 days as legal and asserts they schedule and write, while the over-cap and
  due-before-now cases write nothing. `go test ./internal/schedule/ -count=1` with `TEST_PG_DSN
  set`, `go test ./internal/web/ -run TestPreset -count=1` and `go test ./internal/store/ -run
  'TestPreset|TestRepairPresetSchedulerLimits|TestCreateAnkiPreset|TestMigratePreset' -count=1`
  pass.

- [ ] **M19-29 Undo restores `scheduled_days` by rounding a float, so the restored state is not what the
  scheduler wrote** — *should-fix.* `rebuildReviewLog` recomputes the field as
  `int(math.Round(prev.IntervalDays))` (`internal/schedule/actions.go:166-197`, esp. `:189-194`), while the
  scheduler truncates: `Floor(delayMinutes/1440)` for a step and `uint64(interval)` for a graduation
  (`scheduler_basic.go:25-27,34-40`). *Reproduced* on both drivers: `step=1.5d before_scheduled=1
  restored_scheduled=2` and `step=200000d before_scheduled=200000 restored_scheduled=0`, with the due date
  restored exactly so nothing looks wrong. The value is exported to Anki as the interval and is the
  `ScheduledDays` handed to the next `Next`. The existing undo test cannot see it: its fixture uses 1m/10m
  steps, so both values are 0 (`actions_test.go:137-139`). *Acceptance:* stop deriving an integer from a
  float — snapshot the post-grade `scheduled_days` in the append-only log (nullable column, backfill
  `floor(interval_days)`) and restore it verbatim; add a regression with a ≥ 1-day step plus a mutation
  control that fails when the field is left unrestored.

- [ ] **M19-30 The retrievability order has no caller, and when reached it applies the daily cap before
  ranking** — *should-fix.* `OrderByRetrievability` is the documented default (`internal/schedule/queue.go:
  33-38`) but the only production call site always passes `OrderByDueAt` (`internal/api/service.go:1177-1185`),
  so that order is unreachable. If it were reached, `Build` sorts the rows the deck query already limited by
  `LIMIT` (`queue.go:229-251,739-747`), so the ordering only permutes the N earliest-due cards and can
  drop the one most likely to be forgotten. *Reproduced* with `reviews_per_day=1` and two due cards
  (R=0.994 due 2 h ago, R=0.846 due 1 h ago): `queue=[{CardID:1 ...}]
  wanted_lowest_R_card=2`. *Acceptance:* either delete the path and the enum value, or rank before capping
  (fetch up to `BatchSize`, compute retrievability, apply each deck's allowance to the ranked list); a
  regression keeps a low-R card that is not the earliest due.

- [x] **M19-31 A `choice_single`/`choice_multi` back face panics on a large numeric field value** —
  *should-fix.* `optionIndex` rejects negatives and fractions but has no upper bound
  (`internal/answer/context.go:261-268`); `1e100` parses, truncates to itself, and `int(1e100)` yields
  `math.MinInt64` on amd64, which passes the `index >= len(options)` guard and indexes the slice
  (`choice_single.go:33-39`, `choice_multi.go:36-59`). *Reproduced*: `PANIC=runtime error: index out of
  range [-9223372036854775808]` for both controls. The value comes from note content and the render runs in
  the request that serves the card face, so the recovery middleware turns it into a 500 per flip rather
  than a crash; the grade path is unaffected because it unmarshals into `int`, which is why a grade-path
  test would miss it. *Acceptance:* bound the index in `optionIndex` (`> math.MaxInt32` is an error) and
  have both `RenderBack` implementations treat an out-of-range stored index as an error; a regression
  asserts an error and no panic.
  *Closed by this commit (`fix(answer): refuse an option index past the int32 range`).*
  `optionIndex` (`internal/answer/context.go:263`) refuses a value above `math.MaxInt32` before the `int`
  conversion, so a float past the int range can no longer turn into a negative index; the doc comment
  states why. `choiceSingleParts` and `choiceMultiParts` already refuse an index at or past the option
  count and are the only path both `RenderBack` implementations take to `options[index]`, so both now
  return that error instead of indexing, and no second bounds check was added. `1e100` is refused at the
  parse step, so `RenderFront` refuses it too.
  `TestChoiceControlsRefuseHugeNumericIndex` (`internal/answer/choice_index_range_test.go`) drives both
  controls with a stored index of `1e100`, calls `RenderBack` first and then `RenderFront`, and requires an
  error and no panic from each, plus an error from the grade path. Raw output of
  `go test ./internal/answer/ -run TestChoiceControlsRefuseHugeNumericIndex -count=1 -v`:
  ```
  === RUN   TestChoiceControlsRefuseHugeNumericIndex
  === RUN   TestChoiceControlsRefuseHugeNumericIndex/choice_single
  === RUN   TestChoiceControlsRefuseHugeNumericIndex/choice_multi
  --- PASS: TestChoiceControlsRefuseHugeNumericIndex (0.00s)
      --- PASS: TestChoiceControlsRefuseHugeNumericIndex/choice_single (0.00s)
      --- PASS: TestChoiceControlsRefuseHugeNumericIndex/choice_multi (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/answer	0.002s
  ```
  Before the change the same test panicked inside `choiceSingleControl.RenderBack`
  (`internal/answer/choice_single.go:38`) with
  `panic: runtime error: index out of range [-9223372036854775808] [recovered, repanicked]`.
  `go test ./internal/answer/ -count=1` passes, and
  `go test ./internal/web/ -run 'TestReview|TestGrade|TestChoice|TestCard' -count=1` and
  `go test ./internal/api/ -run 'TestReview|TestAnswer|TestCard' -count=1` pass.

- [x] **M19-32 `choice_multi` halves the score when the correct-index list repeats an index** —
  *should-fix.* `correct` is built as a set but the denominator is `len(answers)`
  (`internal/answer/choice_multi.go:69-99`, denominator at `:92`), and `choiceMultiParts` validates range
  but not duplicates (`:112-137`). `answers = "0<br>0"` therefore scores `(1-0)/2 = 0.5`, i.e. `Hard` for a
  fully correct selection. *Reproduced*: `score=0.5 rating=2 detail=map[answers:[0 0] hits:1 misses:0
  selected:[0]]`. *Acceptance:* deduplicate `answers` or refuse the duplicate at parse time, so the
  denominator is the number of distinct correct options; a regression selects the duplicated index once and
  expects score 1.
  *Closed by this commit (`fix(answer): score a repeated correct index once`).*
  `choiceMultiParts` (`internal/answer/choice_multi.go:128`) passes the parsed correct indices through
  `distinctIndices` (`:141`), which drops a repeated index and keeps the stored order. The denominator is
  therefore the number of distinct correct options, and `RenderBack` lists each correct option once. The
  range check runs on the distinct list, which is the same set the grade compares against.
  `TestChoiceMultiDuplicateCorrectIndexScoresFullCredit` (`internal/answer/choice_multi_duplicate_test.go`)
  grades a single selection of the duplicated index and asserts score 1, rating `Good`, and the detail
  carrying `answers:[0]`. Raw output of
  `go test ./internal/answer/ -run TestChoiceMultiDuplicateCorrectIndexScoresFullCredit -count=1 -v`:
  ```
  === RUN   TestChoiceMultiDuplicateCorrectIndexScoresFullCredit
  --- PASS: TestChoiceMultiDuplicateCorrectIndexScoresFullCredit (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/answer	0.002s
  ```
  Before the change the same test failed with
  `score = 0.5, want 1 (detail map[answers:[0 0] hits:1 misses:0 rating:2 score:0.5 selected:[0]])`.
  `go test ./internal/answer/ -count=1` passes, and
  `go test ./internal/web/ -run 'TestGrade|TestReview|TestOptionOrder|TestChoice' -count=1` passes.

- [ ] **M19-33 `IsMachineGraded` special-cases a control name in core code** — *nit.*
  `internal/answer/answer.go:136-147` returns false when `spec.Control == "short_answer"`, while the package
  doc and AGENTS §2.3.7 say adding a control means adding one file and registering it with no core change.
  A second self-assessed control (the essay or draw-and-explain kind M10-3 will need) must edit this
  function; if the author forgets, the API demands a 1–4 self-rating for a card that records a written
  answer. Verified by reading it and its callers (`internal/api/service.go:1465-1500`); no test asserts the
  rule for any control but `short_answer`. *Acceptance:* an optional interface implemented by the control
  (`SelfAssessed() bool`, or `MachineGraded() bool`) consulted by `IsMachineGraded`, with no control name
  appearing as a string literal outside its own file.

- [ ] **M19-34 The retention interval constant is dead and the sweep runs every 10 minutes while the
  package documents one hour** — *nit.* `retention.DefaultInterval = time.Hour` is documented as the
  fallback (`internal/retention/retention.go:36-41,72-75`) and passed through as the zero value
  (`:96-102`), after which the worker substitutes its own 10-minute default
  (`internal/worker/worker.go:68-71`), and production never sets `Interval` (`cmd/engram/main.go:199-207`).
  The behaviour is harmless — everything reclaimed is already expired and the queries are indexed — but a
  reader reasoning about load is wrong by 6×, and AGENTS §1 makes a comment that disagrees with the code a
  finding. Verified by tracing the three call sites; no command needed. *Acceptance:* resolve the default
  in `New` so the constant and the comment become true, or delete the constant and document ten minutes.

- [x] **M19-35 The grade-mapping thresholds are not range-checked, so a perfect score can map to `Again`** —
  *nit.* `validate` checks version and rating range but not the threshold domain
  (`internal/answer/grade.go:86-104`), `withDefaults` treats every `<= 0` threshold as unset (`:106-127`),
  and `RatingFor` clamps the score into `[0,1]` (`:131-145`). Observed: `{"full_threshold":2}` makes
  `rating(1)=2`, `{"full_threshold":2,"none_threshold":1.5}` makes `rating(1)=1`, and negative thresholds
  silently become 1/0. Since `reviews.rating` is written from the mapping and cannot be reconstructed, the
  second case is the damaging one; `full_threshold: 0` ("every answer is full credit") is unrepresentable
  because it reads as unset. Reachability, stated so this is not over-read: no transport writes
  `grade_mapping_json` today — the preset request struct has no such field and the `.apkg` import carries
  none — so this is a latent validation gap, not a currently reachable defect. *Acceptance:* validate
  `0 <= none_threshold < full_threshold <= 1` and make "unset" explicit so an intentional 0 survives.
  *Closed by this commit (`fix(answer): refuse a grade-mapping threshold outside its domain`).*
  `GradeMapping.FullThreshold` and `NoneThreshold` are `*float64` (`internal/answer/grade.go:33-34`), so a nil
  threshold means "unset" and an explicit `0` is kept; `withDefaults` (`:122`) substitutes only a nil value, and
  `RatingFor` (`:147`) dereferences the resolved pair. `validate` (`:92`) now refuses a non-finite threshold, a
  `none_threshold` below 0, a `full_threshold` above 1 and a `full_threshold` not greater than `none_threshold`,
  naming both values. A mapping outside that domain therefore no longer parses into a rating, so the append-only
  `reviews.rating` cannot be written from it. No transport writes `grade_mapping_json` today, so this closes the
  latent gap the entry describes rather than a reachable request path.
  `TestGradeMappingThresholdValidation`, `TestGradeMappingIntentionalZeroThreshold`,
  `TestGradeMappingRefusesNonFiniteThreshold` and `TestGradeMappingDefaultsStillApply`
  (`internal/answer/grade_threshold_test.go`) cover the five refused raw mappings, an explicit `0` surviving parse,
  marshal and round trip with the ratings it produces, a NaN and an infinite threshold, and the absent-threshold
  default. Raw output of `go test ./internal/answer/ -run 'TestGradeMapping' -count=1 -v`:
  ```
  === RUN   TestGradeMappingDefaults
  --- PASS: TestGradeMappingDefaults (0.00s)
  === RUN   TestGradeMappingCustom
  --- PASS: TestGradeMappingCustom (0.00s)
  === RUN   TestGradeMappingThresholdValidation
  === RUN   TestGradeMappingThresholdValidation/full_threshold_above_one
  === RUN   TestGradeMappingThresholdValidation/both_thresholds_above_one
  === RUN   TestGradeMappingThresholdValidation/negative_none_threshold
  === RUN   TestGradeMappingThresholdValidation/none_threshold_above_one
  === RUN   TestGradeMappingThresholdValidation/full_threshold_not_above_none_threshold
  --- PASS: TestGradeMappingThresholdValidation (0.00s)
      --- PASS: TestGradeMappingThresholdValidation/full_threshold_above_one (0.00s)
      --- PASS: TestGradeMappingThresholdValidation/both_thresholds_above_one (0.00s)
      --- PASS: TestGradeMappingThresholdValidation/negative_none_threshold (0.00s)
      --- PASS: TestGradeMappingThresholdValidation/none_threshold_above_one (0.00s)
      --- PASS: TestGradeMappingThresholdValidation/full_threshold_not_above_none_threshold (0.00s)
  === RUN   TestGradeMappingIntentionalZeroThreshold
  --- PASS: TestGradeMappingIntentionalZeroThreshold (0.00s)
  === RUN   TestGradeMappingRefusesNonFiniteThreshold
  === RUN   TestGradeMappingRefusesNonFiniteThreshold/NaN
  === RUN   TestGradeMappingRefusesNonFiniteThreshold/positive_infinity
  --- PASS: TestGradeMappingRefusesNonFiniteThreshold (0.00s)
      --- PASS: TestGradeMappingRefusesNonFiniteThreshold/NaN (0.00s)
      --- PASS: TestGradeMappingRefusesNonFiniteThreshold/positive_infinity (0.00s)
  === RUN   TestGradeMappingDefaultsStillApply
  --- PASS: TestGradeMappingDefaultsStillApply (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/answer	0.002s
  ```
  With only the new domain block removed from `validate`, the same run failed on
  all five refusal cases with `ParseGradeMapping(...) error = nil, want the threshold refused` and
  `validate with none_threshold NaN error = nil, want it refused` for both non-finite cases.
  `go vet ./internal/answer/`, `go test ./internal/answer/ -count=1`, `go test ./internal/store/ -run 'TestPreset'
  -count=1` and `go test ./internal/api/ -run 'TestGrade|TestPreset|TestReview' -count=1` pass.

- [ ] **M19-36 Three test defects that will not fail when the behaviour breaks** — *nit.*
  (a) `internal/schedule/actions_test.go:137-139` compares `restored.ScheduledDays` with `afterFirst`'s in a
  fixture whose steps are the default 1m/10m, so both are 0 and the assertion holds even if undo restores
  nothing — the real divergence is M19-29. (b) `internal/schedule/deck_counts_test.go:24` claims `DeckCounts`
  has no `Now` parameter and reads the clock itself, which is false (`queue.go:322,330` and the callers pass
  `Now`); the test then depends on wall-clock proximity, so it fails if the process crosses the 04:00 cut-off
  between fixture setup and the call (`deck_budget_test.go:24,87` has the same shape). (c) No test asserts
  that `NewScheduler` fails for parameters the library will reject; `migrate_preset_limits_test.go` compares
  parameters after migration 0020 has already repaired the row, so it never reaches the silent-reset path of
  M19-27. Verified by reading; (b)'s flake window is milliseconds wide, so it was not reproduced.
  *Acceptance:* a ≥ 1-day step in the undo fixture plus a mutation check, explicit `Now` in the deck-count
  and budget tests with the comment corrected, and a `NewScheduler` error case for an unacceptable step.

- [x] **M19-37 The request log writes live frame-media bearer tokens to disk** — *Blocker.*
  `requestLogger` logs `c.Request.URL.Path` for every request (`internal/web/server.go:539-545`) and is
  mounted globally (`:317`). `/fm/<token>/<name>` puts the token in the path (`internal/web/frame_media.go:34`),
  and `frameMediaServe` accepts that token without any session: it re-checks the token's user and deck role,
  not the caller (`:67-122`). Every card render's media fetch therefore writes a credential that is valid
  for six hours (`:40-42`) to the log, and the frame-media file states the token "is never logged"
  (`:64-66`). Anyone who can read the log stream (container logs, aggregator, backup) can replay a line
  against `/fm/...` and read that deck's media for the rest of the window. The same logger also records
  `PUT /api/v1/decks/import-uploads/<token>` and `GET /api/v1/decks/export-downloads/<token>`
  (`internal/api/server.go:228-229`), but those handlers consume the ticket before `c.Next()` returns, so the
  logged line holds a dead token; that part is hygiene, not a live credential. Verified by reading the
  logger, the route and the verification path; *Unverified:* no log line was captured (the web tests pass a
  discarding logger, and no test covers `requestLogger`).
  *Acceptance:* log a redacted path (or the matched route pattern) for `/fm/` and the two ticket routes and
  add a test that drives `/fm` and asserts the token does not appear in the captured log line.
  *Closed by `8a5de83`.* `requestLogger` (`internal/web/server.go:535`) now passes the path through
  `redactedRequestPath` (`:555`), which replaces the first segment after `/fm/`,
  `/api/v1/decks/import-uploads/` or `/api/v1/decks/export-downloads/` with `<token>`. The frame media
  name and the rest of a normal path stay in the line, so the log still locates the request without
  holding the credential. The frame-media file's "never logged" statement is now true.
  `TestRequestLoggerDoesNotLogCredentials` (`internal/web/request_log_test.go`) mounts the real
  middleware, registers the real `/fm/:token/:name` route and both ticket routes, sends one request to
  each and asserts the token is absent from the captured line and `<token>` is present. Raw output of
  `go test ./internal/web/ -run TestRequestLoggerDoesNotLogCredentials -count=1 -v`:
  ```
  === RUN   TestRequestLoggerDoesNotLogCredentials
  === RUN   TestRequestLoggerDoesNotLogCredentials/frame_media
  === RUN   TestRequestLoggerDoesNotLogCredentials/import_ticket
  === RUN   TestRequestLoggerDoesNotLogCredentials/export_ticket
  --- PASS: TestRequestLoggerDoesNotLogCredentials (0.00s)
      --- PASS: TestRequestLoggerDoesNotLogCredentials/frame_media (0.00s)
      --- PASS: TestRequestLoggerDoesNotLogCredentials/import_ticket (0.00s)
      --- PASS: TestRequestLoggerDoesNotLogCredentials/export_ticket (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/web	0.010s
  ```
  The same test against the previous logger captured the raw path, for example
  `path=/fm/frame-token-secret/_font.woff2`, and failed for all three routes. The test was not taken
  on trust: it drives the middleware, and the failure output above is from the unmodified
  `server.go`. `go test ./internal/web/ -run 'TestRequestLogger|TestFrameMedia|TestHealthz|TestNewRequires' -count=1`
  passes.

- [x] **M19-38 A stolen session can change the account email and then reset the password, so the change
  outlives the session** — *should-fix.* `emailChangeSubmit` accepts a new address from any signed-in
  session without asking for the current password (`internal/web/account.go:271-315`); the confirmation mail
  goes only to the new address (`internal/web/security_mail.go:172-178`), the old address is not notified,
  and no session is revoked. The password-change path deliberately requires the old password
  (`internal/web/profile.go:66-84`), so the email path is a weaker credential change: a session thief moves
  the mailbox to an address they control, confirms it, then uses the normal reset flow
  (`internal/web/account.go:133-169`) to set a password and sign in after the victim's session is gone.
  Verified by reading the three handlers and the reset path; *Unverified:* not reproduced against a running
  instance. *Acceptance:* require the current password (or a fresh OIDC re-authentication) for an email
  change, notify the old address, revoke the other sessions, and add a negative test that a session without
  the password cannot change the email.
  *Closed by this commit (`fix(web): require the password before an email change`).*
  `emailChangeSubmit` (`internal/web/account.go:285`) now verifies the current password through
  `AccountService.VerifyPassword` (`internal/auth/account.go:220`) before issuing the confirmation token:
  an empty password is `password_required`, a wrong one is `invalid_current_password` (the code the
  password-change path already uses), and a pure-OIDC account with no local password is refused with
  `password_not_set` until it sets one from the settings page (which it can do without an old password,
  because the OIDC login is the proof; a fresh OIDC re-authentication was not implemented, so no passwordless
  path is left open). The confirmation handler now loads the user before the update, revokes every session of
  that user after the address changes (`s.sessions.RevokeAllForUser`), and sends the existing
  `credential_changed` notice to the replaced address with the new `mail.notice.credential.email` wording
  (`internal/web/security_mail.go:122`), so the mailbox that loses the account learns about it.
  `TestEmailChangeRequiresPasswordAndRevokesSessions` (`internal/web/email_change_security_test.go`) sends a
  valid new address without a password and with a wrong password, asserts both are refused with no mail and
  no row change, then requests with the right password, confirms the token, and asserts the address changed,
  zero sessions remain live, both pre-change cookies answer 401, and the old address received the notice.
  Raw output of `go test ./internal/web/ -run 'TestAccount|TestEmailChangeRequiresPassword|TestSecurityMailEmailVerifyAndChangeFlow' -count=1 -v`:
  ```
  === RUN   TestAccountRouteCutover
  --- PASS: TestAccountRouteCutover (0.03s)
  === RUN   TestAccountJSON_CSRFRejections
  --- PASS: TestAccountJSON_CSRFRejections (0.03s)
  === RUN   TestAccountForgotResetFlow
  --- PASS: TestAccountForgotResetFlow (0.04s)
  === RUN   TestAccountEmailVerifyFlows
  --- PASS: TestAccountEmailVerifyFlows (0.03s)
  === RUN   TestEmailChangeRequiresPasswordAndRevokesSessions
  --- PASS: TestEmailChangeRequiresPasswordAndRevokesSessions (0.04s)
  === RUN   TestSecurityMailEmailVerifyAndChangeFlow
  --- PASS: TestSecurityMailEmailVerifyAndChangeFlow (0.03s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/web	0.223s
  ```
  With only the three production files reverted, the same new test failed with
  `email change without a password = 200 , want 400 password_required`,
  `email change with a wrong password = 200 , want 401 invalid_current_password`,
  `refused requests queued 2 email_verification mail(s), want none` (both went to `attacker@example.com`),
  and `confirmation not enqueued to the new address`. The frontend form now sends the password
  (`frontend/src/lib/views/EmailSettingsView.svelte`); `npx vitest run src/tests/account.test.ts` reports
  `Test Files 1 passed (1)`, `Tests 12 passed (12)`. The package-wide `go test ./internal/web/ -count=1`
  still fails on the pre-existing session-rotation assertions in `TestPasswordChangeSecurityFlow`,
  `TestInitialPasswordForPasswordlessAccount`, `TestSecurityMailFiveClassATypesDeliveredAtTriggers` and
  `TestSecurityMailClassAHasNoUnsubscribeHeader`; those fail identically on the unmodified `main`.

- [x] **M19-39 A password change keeps the same session ID, so a copied session cookie survives it** —
  *should-fix.* `ChangePasswordKeepingSession` keeps `keepSessionID` and revokes only the other rows
  (`internal/auth/account.go:228-264`, `internal/store/session.go:69-85`), and the web handler passes the
  current session's ID (`internal/web/profile.go:72-84`). Keeping the user signed in is reasonable, but the
  kept row is the same credential: a copy of the cookie taken before the change still authenticates. The
  existing test checks that API keys survive and the request succeeds
  (`internal/web/password_reset_keys_test.go:114-147`); nothing asserts that the session ID changes.
  Verified by reading the revocation query and the call site; *Unverified:* not reproduced with a copied
  cookie. *Acceptance:* when keeping the current login, create a new session row with a new ID and CSRF token,
  revoke the old one and set the new cookie; a regression copies the pre-change cookie and expects 401 while
  the new cookie works.
  *Closed by this commit (`fix(auth): rotate the session when the password changes`).* After the password
  write succeeds, `passwordPatch` (`internal/web/profile.go:104`) calls `Manager.RotateSession`
  (`internal/auth/session.go:167`), which builds a fresh session row and hands out its cookie;
  `SessionStore.Rotate` (`internal/store/session.go:84`) creates the replacement and revokes the old row
  (scoped to the same user) in one transaction, so a failure cannot leave both usable. The previous
  `ChangePasswordKeepingSession` call still keeps this device signed in until the rotation, which then
  revokes that exact session ID; the other devices were already revoked.
  `TestSelfPasswordChangeRotatesSession` (`internal/web/password_reset_keys_test.go`) changes the password
  through `PATCH /api/v1/settings/password`, asserts the response carries a new `engram_session` cookie, that
  the pre-change cookie answers `"authenticated":false`, that the new cookie answers
  `"authenticated":true`, that the old row has `revoked_at`, and that exactly one active row remains with a
  different ID and CSRF token. Raw output of
  `go test ./internal/web/ -run 'TestSelfPasswordChangeRotatesSession|TestSelfPasswordChangeKeepsAPIKeys|TestForgotPasswordResetRevokesAPIKeys' -count=1 -v`:
  ```
  === RUN   TestForgotPasswordResetRevokesAPIKeys
  --- PASS: TestForgotPasswordResetRevokesAPIKeys (0.04s)
  === RUN   TestSelfPasswordChangeKeepsAPIKeys
  --- PASS: TestSelfPasswordChangeKeepsAPIKeys (0.05s)
  === RUN   TestSelfPasswordChangeRotatesSession
  --- PASS: TestSelfPasswordChangeRotatesSession (0.04s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/web	0.151s
  ```
  With only the three production changes reverted, the same test failed with
  `pre-change cookie still authenticates: {"authenticated":true,"csrf_token":"PKyc…","user":{…}}` and
  `password change set cookies [], want a new "engram_session" cookie`. `go test ./internal/auth/ -run
  'TestSession|TestAuthSuite' -count=1` and `go test ./internal/store/ -run 'TestSession' -count=1` pass.

- [x] **M19-40 JSON endpoints have no request-body cap and the server has no read timeout** — *should-fix.*
  `newHTTPServer` sets only `ReadHeaderTimeout` and `IdleTimeout` (`internal/web/server.go:443-459`), and the
  only `http.MaxBytesReader` calls are the media upload (`internal/web/media.go:94`) and the package paths
  (`internal/api/package.go:229,243`). Every JSON handler decodes an unbounded body; `apiLogin` binds at
  `internal/web/auth_api.go:57` **before** it reaches the limiter at `:81`, and register, forgot-password and
  TOTP follow the same shape. An unauthenticated client can allocate the body size in memory on
  `/api/v1/auth/login` (one large JSON string), and with no read timeout it can also hold connections open
  by trickling a body. Verified by reading the server configuration and the handler order; *Unverified:*
  the allocation and the hold were not measured. *Acceptance:* cap JSON bodies with `http.MaxBytesReader`
  before binding (and keep the existing package limit on uploads), give request bodies a deadline that does
  not cut off the documented large uploads, and add a test that an oversized login body answers 413.
  *Closed by this commit (`fix(web): cap and time-bound JSON request bodies`).*
  `jsonBodyLimit` (`internal/web/request_body.go:37`) is mounted globally before the session middleware
  (`internal/web/server.go:321`). For `application/json` and `+json` media types it refuses a declared
  `Content-Length` over 32 MiB before reading, and otherwise reads the body through `http.MaxBytesReader` with the
  same cap and replaces it with the buffered bytes, so no handler ever binds an unbounded body. The same read runs
  under a 30-second per-connection deadline set with `http.NewResponseController`, cleared before the response; an
  over-cap body answers 413 `request_too_large` (registered in `internal/api/errors.go`), and any other read failure,
  including the deadline firing, answers 408 `invalid_request` without handing a partial body to a handler. Multipart
  package and media uploads do not pass through this middleware and keep their own caps; `http.Server.ReadTimeout`
  remains unset.
  `TestJSONBodyLimitRejectsOversizedLogin`, `TestJSONBodyLimitAllowsBodyUnderCap`,
  `TestJSONBodyLimitReadDeadlineClosesTrickledBody`, `TestJSONBodyLimitLeavesMultipartUntouched` and
  `TestJSONBodyLimitOnlyAppliesToJSONMediaTypes` (`internal/web/request_body_test.go`) cover both rejection paths on
  the assembled server, an under-cap control request that still reaches the login handler, a raw TCP connection that
  trickles a JSON body and is answered within the shortened test deadline, a multipart body object that is not
  replaced, and the media-type gate. Raw output of `go test ./internal/web/ -run 'TestJSONBodyLimit' -count=1 -v`:
  ```
  === RUN   TestJSONBodyLimitRejectsOversizedLogin
  === RUN   TestJSONBodyLimitRejectsOversizedLogin/declared_content_length_over_the_cap
  === RUN   TestJSONBodyLimitRejectsOversizedLogin/streamed_body_over_the_cap
  --- PASS: TestJSONBodyLimitRejectsOversizedLogin (0.07s)
      --- PASS: TestJSONBodyLimitRejectsOversizedLogin/declared_content_length_over_the_cap (0.00s)
      --- PASS: TestJSONBodyLimitRejectsOversizedLogin/streamed_body_over_the_cap (0.03s)
  === RUN   TestJSONBodyLimitAllowsBodyUnderCap
  --- PASS: TestJSONBodyLimitAllowsBodyUnderCap (0.02s)
  === RUN   TestJSONBodyLimitReadDeadlineClosesTrickledBody
  --- PASS: TestJSONBodyLimitReadDeadlineClosesTrickledBody (0.25s)
  === RUN   TestJSONBodyLimitLeavesMultipartUntouched
  --- PASS: TestJSONBodyLimitLeavesMultipartUntouched (0.00s)
  === RUN   TestJSONBodyLimitOnlyAppliesToJSONMediaTypes
  --- PASS: TestJSONBodyLimitOnlyAppliesToJSONMediaTypes (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/web	0.360s
  ```
  With the `router.Use(jsonBodyLimit(...))` line removed, the oversized cases answered 403 `csrf_failed` instead of
  413, because the unbounded body reached the route's CSRF middleware. `go vet ./internal/web/ ./internal/api/`,
  `go test ./internal/web/ -run 'TestJSONBodyLimit' -count=1` and `go test ./internal/api/ -count=1` pass.
  `go test ./internal/web/ -count=1` still reports the four failures
  `TestInitialPasswordForPasswordlessAccount`, `TestPasswordChangeSecurityFlow`,
  `TestSecurityMailFiveClassATypesDeliveredAtTriggers` and `TestSecurityMailClassAHasNoUnsubscribeHeader`; the same
  four fail in a detached worktree at `e06f46e`, the parent of this commit, so they are pre-existing and unrelated.

- [x] **M19-41 One successful login clears the per-IP failure budget, so password spraying is not limited
  by IP** — *should-fix.* `Reset` deletes both the account bucket and the `ip:` bucket
  (`internal/auth/ratelimit.go:307-313`), and `apiLogin` calls it on every success
  (`internal/web/auth_api.go:138-140`); the TOTP step does the same (`internal/web/totp_login.go:119-121`).
  The per-account limit (10) still holds, but the per-IP limit (50) is the only control against trying one
  password across many accounts: an attacker with one valid account can spend 49 attempts across 49 accounts
  and then log in once to reset the IP count, indefinitely. Verified by reading the reset semantics;
  *Unverified:* no spraying test exists and none was run. *Acceptance:* on success clear only the account
  bucket, or keep the IP count until its window ends; add a test that a successful login by one account
  leaves another account's accumulated IP failures in place.
  *Closed by this commit (`fix(auth): keep the per-IP failure budget across a successful login`).*
  `LoginLimiter.Reset` (`internal/auth/ratelimit.go:311`) now deletes only the account bucket and returns
  this attempt's in-flight slot in the IP dimension, leaving the IP's recorded failure count and its
  `lastAt` in place until the window ends. `apiLogin` and the TOTP second step call the same `Reset`, so
  both entry points keep the per-IP budget; the successful attempt itself still releases its placeholder,
  so it cannot hold the IP at its limit. The limiter's doc comment was corrected to say the account count
  is cleared on success while the IP count expires with its window.
  `TestLoginLimiterSuccessKeepsOtherAccountsIPFailures` (`internal/auth/ratelimit_test.go`) records three
  failures for one account on an IP, resets after a different account's successful login from that IP,
  and asserts the next attempt still waits; a fourth failure then reaches `MaxIPFailures` and the next
  attempt is refused with `ErrLoginLocked`, while the reset account itself waits nothing on a fresh IP.
  `TestLoginLimiterDelayGrowsAndResetsOnSuccess` now checks the account reset on another IP and the kept
  IP delay on the original one. Raw output of
  `go test ./internal/auth/ -run 'TestLoginLimiterSuccessKeepsOtherAccountsIPFailures|TestLoginLimiterDelayGrowsAndResetsOnSuccess' -count=1 -v`:
  ```
  === RUN   TestLoginLimiterDelayGrowsAndResetsOnSuccess
  --- PASS: TestLoginLimiterDelayGrowsAndResetsOnSuccess (0.00s)
  === RUN   TestLoginLimiterSuccessKeepsOtherAccountsIPFailures
  --- PASS: TestLoginLimiterSuccessKeepsOtherAccountsIPFailures (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/auth	0.010s
  ```
  With `Reset` restored to deleting both buckets, the new test failed with
  `Wait() after another account's success = (0s, <nil>), want > 0: the IP failures must survive`.
  `go test ./internal/auth/ -count=1` passes in `0.596s` and
  `go test ./internal/web/ -run 'TestLogin|TestTOTP|TestAuth|TestRate|TestRegister|TestForgot' -count=1`
  passes.

- [x] **M19-42 The admin settings saves are not atomic, so a rejected later key leaves earlier keys
  written** — *should-fix.* `adminSettingsSave` validates and writes inside one loop
  (`internal/web/admin_settings_api.go:152-198`): a submission with a valid key followed by an invalid one
  (for example `site.name` then `media.max_bytes=abc`) writes the first row and then returns 400. The
  registration save has the same shape with two writes and no transaction
  (`internal/web/admin_registration_api.go:146-155`), and the OIDC/SMTP saves leave earlier keys in place when
  a later `PutSetting` fails. The notification-preference handler shows the intended order: validate every
  submitted key, then write (`internal/web/mail_prefs_api.go:146-166`). Verified by reading the three
  handlers; *Unverified:* no reproduction. *Acceptance:* validate the whole submission before any write and
  apply the keys in one transaction (or one bulk upsert); a test submits one valid and one invalid key and
  asserts every stored value is unchanged.
  *Closed by this commit (`fix(web): save each admin settings submission in one transaction`).*
  `adminSettingsSave` (`internal/web/admin_settings_api.go:140`) now separates the phases: the loop at `:155`
  only validates and collects the accepted keys, the sensitive keys are collected at `:203` without writing,
  and `:222` applies every regular and sensitive write inside one `db.Transaction`. The three other saves are
  wrapped the same way: registration policy plus allowlist (`admin_registration_api.go:148`), the OIDC provider
  fields plus client secret (`admin_oidc.go:194`) and the SMTP fields plus password (`admin_smtp.go:195`). The
  audit row is still written only after the transaction commits, and the OIDC discovery cache is still
  invalidated only then, so a rolled-back save leaves the running configuration untouched.
  `TestAdminSettingsRejectsTheWholeSubmission` (`internal/web/admin_settings_atomic_test.go`) seeds
  `site.name=Before` and submits `site.name=After` together with `media_max_bytes=abc`; the response is 400 and
  the stored `site.name` is still `Before`. `TestAdminSettingsRollsBackAFailingWrite` installs SQLite triggers
  that abort the `media_max_bytes` write, submits `site.name` plus a legal `media_max_bytes`, gets 500 and
  asserts neither key was stored — the valid key is rolled back with the failed one. Raw output of
  `go test ./internal/web/ -run 'TestAdminSettingsRejectsTheWholeSubmission|TestAdminSettingsRollsBackAFailingWrite' -count=1 -v`:
  ```
  === RUN   TestAdminSettingsRejectsTheWholeSubmission
  --- PASS: TestAdminSettingsRejectsTheWholeSubmission (0.04s)
  === RUN   TestAdminSettingsRollsBackAFailingWrite
  --- PASS: TestAdminSettingsRollsBackAFailingWrite (0.03s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/web	0.091s
  ```
  With only the four handlers reverted, the same run failed with
  `site.name after the rejected submission = "After", want the previous "Before": the valid key must not be written`
  and `site.name = "Rolled Back" after the rolled-back submission, want it not stored`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/web/ -run 'TestAdmin|TestOIDC|TestSMTP|TestRegistration' -count=1`
  and the same run without the PostgreSQL DSN pass.

- [x] **M19-43 OIDC first login creates the account and the identity in two writes, so a failed link leaves
  an orphan** — *should-fix.* `Resolve` creates the user at `internal/auth/oauthlink.go:180-192` and inserts
  the identity afterwards (`:189`, `:248-274`) with no transaction. If the insert fails, the account has no
  link; on the next login the email lookup either finds it and denies because the claim is unverified
  (`:78-85`), or — when the IdP sends no email — the lookup is skipped and `CreateOIDCUser` reuses the
  deterministic username (`:291-302`), so every retry fails on the unique constraint. `Bind` already re-reads
  after a duplicate (`:226-234`); `Resolve`'s auto-link path does not. Verified by reading the writes and
  the retry path; *Unverified:* no failure injection and no concurrent run. *Acceptance:* write the user and
  the identity in one transaction (or remove the new user when the link fails), let `Resolve` recover by the
  derived username as well as by email, and add a regression that injects a link failure plus one that runs
  two first logins concurrently and expects both to succeed.
  *Closed by this commit (`fix(auth): create the OIDC account and its identity in one write`).*
  `UserStore.CreateWithIdentity` (`internal/store/user.go:40`) inserts the account and its `identities` row in
  one transaction and fills the link's `UserID` from the new primary key, and `CreateOIDCUserWithIdentity`
  (`internal/auth/account.go:112`) is what `Resolve`'s create branch now calls; a failed link rolls the
  account back with it. The branch's audit row is written after the commit, so a rolled-back account leaves no
  `identity.link` entry. `recoverCreatedUser` (`internal/auth/oauthlink.go:291`) handles the create failing
  because another writer won: it re-reads the link first (a concurrent first login now commits both rows, so
  its link names the account), and otherwise looks the deterministic login name up with
  `FindUserByUsername` (`:313`) and adopts that account only when its email matches case-insensitively;
  the retry then links it. `CreateOIDCUser` stays as the account-only write for a passwordless account not
  reached through an identity. `TestResolveRollsBackTheAccountWhenTheLinkFails` and
  `TestResolveAdoptsAnAccountWhoseLinkFailed` (`internal/auth/oauthlink_transaction_test.go`) install a SQLite
  trigger that aborts the identity insert, assert no user and no identity row remain, then retry after
  dropping it; the second seeds the orphan an earlier version left behind and asserts the retry adopts it.
  `TestResolveConcurrentFirstLoginsBothSucceed` (PostgreSQL-only: two connections, a trigger that delays the
  user insert) runs two `Resolve` calls for one identity and asserts both return the same account with one
  user and one identity row. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/auth/ -run 'TestResolveRollsBackTheAccountWhenTheLinkFails|TestResolveAdoptsAnAccountWhoseLinkFailed|TestResolveConcurrentFirstLoginsBothSucceed' -count=1 -v`:
  ```
  === RUN   TestResolveRollsBackTheAccountWhenTheLinkFails
  --- PASS: TestResolveRollsBackTheAccountWhenTheLinkFails (0.01s)
  === RUN   TestResolveAdoptsAnAccountWhoseLinkFailed
  --- PASS: TestResolveAdoptsAnAccountWhoseLinkFailed (0.01s)
  === RUN   TestResolveConcurrentFirstLoginsBothSucceed
  --- PASS: TestResolveConcurrentFirstLoginsBothSucceed (0.62s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/auth	0.654s
  ```
  With only `internal/auth/account.go`, `internal/auth/oauthlink.go` and `internal/store/user.go` reverted, the
  same run failed with `users after the failed link = 1, want 0: the account must roll back with its identity`,
  `Resolve() decision = "auto_link", want "create_user"`, `Resolve() error = create oidc user: create user:
  create user: constraint failed: UNIQUE constraint failed: users.username (2067), want the existing account
  adopted` and `concurrent Resolve() #1 error = ... duplicate key value violates unique constraint "idx_users_email"
  (SQLSTATE 23505), want nil`. `go test ./internal/auth/ -count=1` and
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestUser|TestIdentity' -count=1` pass.

- [x] **M19-44 Anyone can fill the global OIDC pending table and stop every OIDC login for ten minutes** —
  *should-fix.* `maxPendingAuth` caps the map at 1000 (`internal/auth/oidc.go:62-68`) and `PutPending` refuses
  new starts once it is full (`:292-305`); the only entry point is the unauthenticated
  `GET /auth/oidc/start` (`internal/web/oidc.go:87-141`), which has no rate limit, and entries live ten
  minutes (`:129`). One thousand requests make every login and identity bind fail with "pending table is
  full" until the entries expire, which is exactly what `TestOIDCStartRejectsWhenPendingTableFull` asserts.
  Verified by reading the cap, the eviction rule and the unthrottled route; *Unverified:* no timing was
  measured.
  *Acceptance:* rate-limit the start endpoint per IP with the existing anonymous limiter and/or
  evict the oldest entry instead of refusing new starts; a test fills the table and shows a start from a
  fresh IP still succeeds.
  *Closed by this commit (`fix(auth): evict the oldest pending OIDC login instead of refusing`).*
  `PutPending` (`internal/auth/oidc.go:294`) still sweeps expired entries first, then, if the table is
  still at `maxPendingAuth`, calls `evictOldestPendingLocked` (`:315`) and inserts the new state. The
  evicted entry is the one closest to expiry; every login start carries the same ten-minute lifetime, so
  that is the oldest start. A flood can therefore still make another user's in-flight callback fail as an
  unknown state, but it can no longer stop new logins for ten minutes. The method keeps its `bool`
  result, which is now false only when the cap is not positive; the caller's failure branch is unchanged.
  `TestOIDCPendingEvictsTheOldestInsteadOfRefusing` (`internal/auth/oidc_pending_test.go`) fills the table
  with 1000 unexpired entries of increasing expiry, starts one more, and asserts it is accepted, the size
  stays at the cap, the earliest-expiry entry is gone and the new state is present.
  `TestOIDCStartEvictsOldestWhenPendingTableFull` (`internal/web/oidc_test.go`) pre-seeds 5000 unexpired
  entries through the real client, then drives `GET /auth/oidc/start` and asserts the 302 authorize
  redirect with a state and PKCE parameters. Raw output of
  `go test ./internal/auth/ -run TestOIDCPendingEvictsTheOldestInsteadOfRefusing -count=1 -v`:
  ```
  === RUN   TestOIDCPendingEvictsTheOldestInsteadOfRefusing
  --- PASS: TestOIDCPendingEvictsTheOldestInsteadOfRefusing (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/auth	0.011s
  ```
  and `go test ./internal/web/ -run TestOIDCStartEvictsOldestWhenPendingTableFull -count=1 -v`:
  ```
  === RUN   TestOIDCStartEvictsOldestWhenPendingTableFull
  --- PASS: TestOIDCStartEvictsOldestWhenPendingTableFull (0.15s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/web	0.169s
  ```
  With only `internal/auth/oidc.go` reverted, the same runs failed with
  `PutPending refused a fresh start while the table was full; the oldest entry must be evicted` and
  `GET /auth/oidc/start = 303, want 302 (body <a href="/login">See Other</a>.)`.
  `go vet ./internal/auth/ ./internal/web/`, `go test ./internal/auth/ -run 'TestOIDCPending' -count=1` and
  `go test ./internal/web/ -run 'TestOIDC' -count=1` pass.

- [ ] **M19-45 The batch export buffers every package in memory with no cap on the number of decks** —
  *should-fix.* `deckBatchExportZip` writes each package into a `bytes.Buffer`
  (`internal/web/package_web.go:84-85`), loops over the caller-supplied `req.DeckIDs` (`:89-127`) and sends
  the whole buffer at the end (`:137`). Neither the count nor the accumulated size is bounded, so a user
  with enough decks (or a repeated id list) makes the process hold the complete archive in memory. Verified
  by reading the handler; *Unverified:* no measurement. *Acceptance:* cap the deck count and the total bytes
  (413 when exceeded), stream the archive instead of buffering it, and add a test over the count cap.

- [x] **M19-46 Any status action other than `disable` enables the account** — *should-fix.*
  `adminUserStatus` treats exactly `"disable"` as disable and every other string as enable
  (`internal/web/admin_users_api.go:196-231`, the `else` at `:219`). A typo (`"disabled"`, a trailing space,
  an empty action) silently re-enables a suspended account and returns 204. Verified by reading the branch;
  *Unverified:* no test sends an unknown action. *Acceptance:* accept only `enable` and `disable`, answer 400
  otherwise, and add table cases for `""`, `"disabled"` and `"enable"`.
  *Closed by this commit (`fix(web): refuse an unknown user status action`).*
  `adminUserStatus` (`internal/web/admin_users_api.go:197`) now switches on the exact request value: `disable`
  keeps the gated disable path, `enable` the enable path, and every other value returns 400 `invalid_request`
  before any write. No trimming was added, so a trailing space or a different case is refused rather than
  guessed.
  `TestAdminUserStatusAcceptsOnlyEnableAndDisable` (`internal/web/admin_users_api_test.go`) disables the
  account through the handler, then sends `""`, `"disabled"`, `"enable "` and `"ENABLE"` and asserts each is
  400 and leaves the account disabled, then sends `"enable"` and asserts 204 and an active account. Raw
  output of `go test ./internal/web/ -run TestAdminUserStatusAcceptsOnlyEnableAndDisable -count=1 -v`:
  ```
  === RUN   TestAdminUserStatusAcceptsOnlyEnableAndDisable
  --- PASS: TestAdminUserStatusAcceptsOnlyEnableAndDisable (0.04s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/web	0.059s
  ```
  With only the handler reverted, the same test failed with
  `status action "" = 204, want 400` and `status action "" left status "active", want "disabled"`.
  `go vet ./internal/web/` and
  `go test ./internal/web/ -run 'TestAdminUser' -count=1` pass.

- [ ] **M19-47 A refused user deletion has already mailed the user and signed them out** — *nit.*
  `adminUserDelete` sends the "account deleted" notice and calls `ForceLogout` before the authoritative
  last-admin check inside `WithAdminGate` (`internal/web/admin_users_api.go:355-379`); the pre-check at
  `:356` is lock-free, so a concurrent demotion can make the gate refuse after both side effects ran. The
  target then receives a deletion mail and loses every session while the account remains. Verified by
  reading the order; *Unverified:* the interleaving was not reproduced. *Acceptance:* move the notification
  and the revocation after the gate commit and add a test that a deletion refused with `last_admin` sends no
  mail and revokes no session.

- [ ] **M19-48 Anki drops the `engram` note-type key, so machine grading does not survive a round trip
  through Anki** — *should-fix.* `internal/anki/write.go:414-418` states that "Anki carries an unknown
  top-level model key through import and through both of its exports", and `AGENTS.md` §2.7 turns that into
  the contract: "answer controls ride in one note type key (`engram`) that Anki keeps through import and
  export". It does not hold for the pinned release. Reproduced with `anki==26.9.3`: I added
  `"engram":{"v":1,"answer_specs":{"Card 1":{"control":"text","bindings":{"answer":"Back"}}}}` to the model
  JSON of `internal/notetype/testdata/anki/stock.anki21.apkg`, imported it with
  `Collection.import_anki_package`, and `collection.models.all()` came back without the key;
  `Collection.export_anki_package` then wrote a `collection.anki2` whose model JSON has no `engram` key
  either. engram's own reader still returns the spec from the same package, so the loss happens only when
  Anki sits in the middle, and nothing reports it: `PackageImportReport` has no answer-spec field, so a deck
  that travelled engram → Anki → engram imports with every answer control silently gone.
  `tools/anki-oracle/verify_package.py:14-52` cannot catch this, because `count_answer_specs` counts the
  specs in the *input* package's own model JSON instead of in the collection Anki produced, which makes the
  check circular. *Acceptance:* carry the spec in something Anki preserves and pin it with a golden that
  imports an engram package through Anki and reads the spec back out of Anki's own collection, or state in
  `AGENTS.md` §2.7 and `write.go` that the key survives engram-to-engram traffic only and report the loss
  on import.

- [x] **M19-49 An unclosed `[anki:` marker makes audio-tag parsing quadratic, so one note field can stall a
  review** — *should-fix.* `parseAVTagNode` looks for the closing tag of each candidate with
  `strings.Index(rest, closing)` (`internal/notetype/av.go:300`) and, finding none, reports "not a
  directive"; `parseAVTextNode` then advances one `[` and the next candidate scans the whole remainder again
  (`:310-317`). Measured on a copy of the package:
  `ExtractAVTags(strings.Repeat("[anki:tts lang=en_US]", 40000), "q")` is 840 KB and takes 5.05 s, and
  420 KB takes 1.23 s — four times the work for a doubling; a plain-text control of 840 KB takes 109 µs.
  The input is untrusted: a field of an imported `.apkg` reaches `ExtractAVTags` twice per card view
  (`internal/web/review.go:223,234`, `internal/web/notes.go:224,233`) and the same parser runs over every
  field on save through `notetype.PlainText` (`internal/store/note_search.go:53`), so a 4 MB field costs
  minutes of CPU per request. *Unverified:* no request was timed end to end. *Acceptance:* resolve every
  closing tag in one pass instead of rescanning the remainder per candidate, and add a test that renders a
  field holding thousands of unclosed markers within a time budget.
  *Closed by this commit (`fix(notetype): resolve audio-tag closings in one pass`).*
  `parseAVNodes` (`internal/notetype/av.go:245`) now builds `newAVClosingIndex` (`:265`) once per field: one
  scan collects the offset of every `[/anki:<name>]` into a per-name list, and `avClosingIndex.at` (`:294`)
  binary-searches it, so a candidate's lookup no longer rescans the remainder. The parser carries the
  current absolute `offset` through `parseAVNode` and `parseAVTagNode` so the index and the returned
  remainder agree, and the matched closing is still the first one for that name at or after the opening
  tag's `]`, which keeps the previous grammar. Cost is now one pass plus one `O(log n)` lookup per
  candidate: the reported 840 KB field dropped from ~5 s to ~20 ms.
  `TestExtractAVTagsUnclosedMarkersStayLinear` (`internal/notetype/av_test.go:490`) renders 40,000 unclosed
  `[anki:tts lang=en_US]` markers, asserts the text comes back unchanged with no tag extracted, and holds
  the call to a one-second budget; the `TestExtractAVTags` table gained a case for two directives taking
  their own closings and one showing an unclosed directive does not consume a later directive's body. Raw
  output of `go test ./internal/notetype/ -run TestExtractAVTagsUnclosedMarkersStayLinear -count=1 -v`:
  ```
  === RUN   TestExtractAVTagsUnclosedMarkersStayLinear
  --- PASS: TestExtractAVTagsUnclosedMarkersStayLinear (0.02s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/notetype	0.019s
  ```
  With only `internal/notetype/av.go` reverted, the same run failed with
  `av_test.go:502: ExtractAVTags on 40000 unclosed markers took 4.985885648s, want under 1s` and
  `--- FAIL: TestExtractAVTagsUnclosedMarkersStayLinear (4.99s)`. `go test ./internal/notetype/ -count=1`,
  `go test ./internal/web/ -run 'TestReview|TestNote' -count=1` and
  `go test ./internal/store/ -run 'TestNoteSearch|TestPlainText|TestSearch' -count=1` pass.

- [x] **M19-50 A zero-byte media file is dropped as missing, though Anki keeps it** — *nit.* The package
  importer treats an entry with no bytes as a missing file rather than an empty one
  (`internal/store/anki_import.go:293-303,307-316,1301`), and a direct upload of an empty file fails with
  `ErrMagicMismatch`, whose message is "file has no detectable content" (`internal/media/media.go:215-217`).
  Reproduced the other way round with `anki==26.9.3`: a package whose media index maps `"0"` to a 0-byte
  `empty.txt` imports with the file present in the media directory at 0 bytes, and
  `collection.media.have("empty.txt")` is true. A deck that shows an empty attachment therefore renders in
  Anki and loses the file, reported as missing media, in engram. Verified by reading both paths and by the
  Anki run above. *Acceptance:* keep a named zero-byte entry as a file on import and accept an empty upload,
  with one test that imports such a package and reads the entry back.
  *Closed by this commit (`fix(media): keep a zero-byte file as an empty attachment`).*
  The import no longer reads "no bytes" as "file missing": `ankiMediaNameIndex`, `ankiMissingMedia`,
  `ankiRawMediaSizes`, `placeAnkiMedia`, the media-row loop, `registerAnkiMediaNames` and `renameAnkiMedia`
  (`internal/store/anki_import.go`) now treat a zero-byte entry like any other entry, so its name resolves,
  it is not reported missing, its bytes (none) are placed at the content address, its `media` row is written
  and its deck name is registered. A zero-byte sha contributes no quota bytes, and `ankiUnplayableVideo`
  still skips it because an empty file is not a video. On the upload side `media.Save` no longer refuses
  a zero-byte body with `ErrMagicMismatch`; that error, its `media_magic_mismatch` code and the matching
  422 branch in `internal/web/media.go` are removed, so an empty upload is stored and served at 0 bytes.
  `TestAnkiImportKeepsZeroByteMedia` (`internal/store/anki_import_empty_media_test.go`) imports a fixture
  whose only media entry is a 0-byte `empty.txt` and asserts the report does not list it as missing, the
  deck name resolves to the sha256 of the empty string, the media row is 0 bytes and the file exists at its
  content address with 0 bytes. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run TestAnkiImportKeepsZeroByteMedia -count=1 -v`:
  ```
  --- PASS: TestAnkiImportKeepsZeroByteMedia (0.33s)
      --- PASS: TestAnkiImportKeepsZeroByteMedia/sqlite (0.01s)
      --- PASS: TestAnkiImportKeepsZeroByteMedia/postgres (0.03s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/store	0.339s
  ```
  With only `internal/store/anki_import.go` reverted, the same run failed on both drivers with
  `imported media names = [], want empty.txt`. The upload half is pinned by
  `TestSaveAcceptsEmptyFile` (`internal/media/media_test.go`) and by
  `TestEditorMediaUploadRejectsOversizedAndKeepsEmpty` / `TestMediaUploadStoresEmptyFile`
  (`internal/web/media_editor_test.go`, `internal/web/media_test.go`), which send no bytes and assert 201,
  the empty-string sha256 and a 0-byte read back. Raw output of
  `go test ./internal/web/ -run 'TestEditorMediaUploadRejectsOversizedAndKeepsEmpty|TestMediaUploadStoresEmptyFile' -count=1 -v`:
  ```
  --- PASS: TestEditorMediaUploadRejectsOversizedAndKeepsEmpty (0.02s)
  --- PASS: TestMediaUploadStoresEmptyFile (0.02s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/web	0.089s
  ```
  Before the change those uploads answered 422 `media_magic_mismatch`. `go test ./internal/media/ -count=1`
  passes, and `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run
  'TestAnki|TestMedia|TestPackage|TestExportAnki|TestLocalAnki' -count=1` passes on both drivers
  (`41.315s`).

- [ ] **M19-51 Three public helpers have no caller, and one duplicates the frontend's occlusion grammar** —
  *nit.* `ParseImageOcclusions` and the `ImageOcclusion`, `ImageOcclusionShape` and
  `ImageOcclusionProperty` types (`internal/notetype/cloze.go:407-426,632-668`) are called by nothing
  outside their own tests; the editor they describe parses the same field in TypeScript
  (`frontend/src/lib/image-occlusion/markup.ts`), so one grammar has two implementations. They have already
  diverged, on the live rendering path as well: a property value containing a lone backslash ends the value
  and drops every later attribute of the shape in Go (`cloze.go:456-463`) — measured,
  `{{c1::image-occlusion:rect:left=0.1:fill=#a\b:width=0.3:height=0.4}}` yields only `left` and `fill=#a` —
  while `decodePropertyValue` keeps the character (`markup.ts:106-120`), and Go accepts any shape kind
  (`hexagon` yields a shape with properties) where the TypeScript parser accepts only the four it knows.
  `Config.Clone` (`template.go:129-148`) and `ExtractClozeHints` (`typeanswer.go:80-90`) are likewise 0 %
  covered and have no caller; `Clone` also shares the `OriginalStockKind` and `AnkiOrigin` pointers with its
  source, which `stock.go:428-460` takes care to copy. Verified with
  `go test ./internal/notetype/ -cover` and a module-wide `grep`. *Acceptance:* delete the two unused
  helpers and keep one implementation of the occlusion grammar, agreeing with the frontend on a stray
  backslash and on an unknown shape kind.

- [ ] **M19-52 The type-in golden records rendered card HTML that no test compares** — *nit.*
  `internal/notetype/testdata/anki/typeanswer.golden.json` holds `question_nodes_text` and
  `answer_nodes_text` for four notes, including the `{{cloze:Text}} {{type:cloze:Text}}` and
  `{{type:nc:Back}}` forms whose filter chains `applyFilters` folds (`filters.go:37-44`), but
  `TestRenderGolden` walks only `stock`, `template-syntax`, `template-filters` and `cloze`
  (`template_golden_test.go:62`), and the fixture's other consumer reads `compare_answers` only
  (`typeanswer_test.go:66-96`). `TestGenerateCardsGolden` does render those fronts through
  `GenerateCards`, but it compares generated ordinals, never the text, so a template that rendered nothing
  would still pass: the wiring from `splitReplacement` to the folded filter is unpinned end to end. Verified
  by reading the fixture's three consumers. *Acceptance:* add `typeanswer` to `TestRenderGolden`'s fixture
  list, or drop the two fields from the golden so it no longer claims coverage it lacks.

- [ ] **M19-53 Three archive-hardening branches have no hostile-input test** — *nit.* `indexEntries`
  rejects a symlink entry (`internal/anki/archive.go:99-101`) and a duplicate entry name (`:102-104`), and
  `safeEntryName` rejects a Windows drive-letter name (`:146-148`); none of the three is exercised. The
  reader's tests build a `../evil` entry, a `../evil.png` media name, corrupt meta, corrupt zstd and the
  three size caps, and nothing else (`internal/anki/anki_test.go:236-334`); `isASCIILetter` reports 0 %
  coverage and no test mentions a symlink. This is exactly the input a malicious `.apkg` carries, so a
  refactor that dropped one branch would fail no test. Verified with `go test ./internal/anki/ -cover`
  (77.9 % of statements) and by reading the tests. *Acceptance:* add one archive per branch, each asserting
  `ErrUnsafeEntry` or `ErrBadArchive`.

- [x] **M19-54 Garbage collection aborts a batch after deleting the rows, leaking the rest of its files** —
  *nit.* `CollectGarbage` asks `store.DeleteOrphanedMedia` for a batch of 200 and removes the files only
  afterwards (`internal/media/media.go:304-327`); one `os.Remove` that fails with anything but "not exist"
  (permissions, a read-only mount) returns immediately, and because the whole batch's rows are already
  committed as deleted, the remaining files have no row pointing at them and nothing will ever reclaim
  them, while the next call finds no rows to retry. `absPath` failing has the same shape (`:316-319`). The
  re-upload guard is also a check-then-act window: a row inserted between `bySha256` (`:311-315`) and
  `os.Remove` (`:320`) keeps pointing at the file that is then deleted. Verified by reading the three
  functions. *Unverified:* not reproduced; it needs a filesystem error, and a concurrent upload for the
  window. *Acceptance:* attempt every file of the batch, keep the row of a file that could not be removed so
  the next sweep retries it, and add a test that makes one removal fail and expects the batch's other files
  to be gone and the failed row to survive.
  *Closed by this commit (`fix(media): reclaim the rest of a batch after a failed file removal`).*
  `CollectGarbage` (`internal/media/media.go:301`) now records every failed removal, restores that row and its
  uploader records through `store.RestoreDeletedMedia` (`internal/store/media_gc.go:153`), and returns
  `errors.Join` of the failures only after the whole batch has been attempted; the restored row keeps its
  orphan mark, so the next sweep selects it again. `store.DeleteOrphanedMedia` (`:87`) returns
  `[]DeletedMedia` (`:78`), which carries the uploader rows read before they are deleted, so the restore keeps
  the attribution instead of dropping it. A batch with a failure ends the loop after that batch, because the
  restored rows would otherwise be selected and fail again in the same run.
  `TestCollectGarbageKeepsBatchAfterFailedRemoval` (`internal/media/media_gc_test.go:173`) makes the file of
  the lowest sha a non-empty directory so the first removal of the batch fails, and asserts that the sweep
  reports that sha, reclaims the other two rows and files, keeps the failed row, its uploader row and its
  orphan mark, then reclaims it on the retry once the path is removable. Raw output of
  `go test ./internal/media/ -run TestCollectGarbageKeepsBatchAfterFailedRemoval -count=1 -v`:
  ```
  === RUN   TestCollectGarbageKeepsBatchAfterFailedRemoval
  --- PASS: TestCollectGarbageKeepsBatchAfterFailedRemoval (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/media	0.023s
  ```
  With the old loop restored, the same run failed with
  `sweep removed 0, want 2: the rest of the batch must still be reclaimed`.
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/media/ -count=1` and
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestMediaGC|TestMediaNote|TestMediaName|TestClearOrphaned|TestMediaUploaders|TestUserMediaUsage|TestDeckDeleteReleasesNoteTypeMedia|TestPackage' -count=1`
  pass. The re-upload check between `bySha256` and `os.Remove` is unchanged, so that window remains.

- [ ] **M19-55 The review page's global key handler swallows Space and Enter on its own controls** — *should-fix.*
  `onKeydown` maps Space/Enter to reveal or submit and calls `preventDefault()` for every decision but `ignore`
  (`frontend/src/lib/views/ReviewView.svelte:799-813`; the branch is `frontend/src/lib/review-shortcuts.ts:74-80`),
  while `isTypingTarget` counts only input, textarea and select (`ReviewView.svelte:721-728`) and `isChoiceTarget`
  is a constant `false` (`:731-733`). The listener is on the window (`:879`), so a focused button or link never
  receives its default activation. *Reproduced* in a mounted view under happy-dom: with focus on the header's
  Edit button, a Space keydown left `defaultPrevented: true click fired: false answer area now present: true` —
  the card revealed instead of the button firing; Enter takes the same branch. The affected controls include the
  edit entry, the exit link, the undo button and the answer-scroll checkbox. *Acceptance:* ignore the review keys
  when the event target is a button, link or checkbox (or scope the listener to the card area), and add a client
  test that activates each header control with Space and with Enter.

- [ ] **M19-56 A deck filter does not survive a reload because the router decodes `+` literally** — *should-fix.*
  `parseQuery` splits the query and calls `decodeURIComponent` (`frontend/src/lib/router/index.ts:11-24`, the
  decode at `:21`), which leaves `+` as a plus, while the deck view writes the same address through
  `URLSearchParams` (`frontend/src/lib/views/DeckDetailView.svelte:192-199`), which encodes a space as `+`.
  *Reproduced* by calling the real functions: `matchRoute('/decks/abc?q=two+words').query` is
  `{"q":"two+words"}` and `parseQuery('?q=two+words')` is `{"q":"two+words"}`, so after a reload, a back
  navigation or the editor's return link the filter is read as the literal string `two+words` and the API
  searches for a plus sign. The review page reads its own scope with `URLSearchParams`
  (`ReviewView.svelte:249-266`), so the two readers of one address disagree about a tag that holds a space.
  *Acceptance:* parse the address with one implementation that treats `+` as a space, and add a round-trip test
  for a filter and a tag containing a space, a plus and a percent sign.

- [ ] **M19-57 A malformed percent-encoded path throws during bootstrap and blanks the app** — *should-fix.*
  `matchRoute` calls `decodeURIComponent` on each path segment and each query part with no guard
  (`frontend/src/lib/router/index.ts:21,65`), and the route store is initialised from the current address at
  module load (`:107`), so an address such as `/decks/%E0%A4%A` throws before the app mounts. *Reproduced* by
  importing the real module: `matchRoute('/decks/%E0%A4%A') -> THREW URIError URI malformed`. The server serves
  the SPA shell for any unknown GET page path (`internal/web/not_found.go:26-37`), so such an address reaches
  the client; the same call runs on the user-controlled `?return=` value through `safeReturnPath`
  (`frontend/src/lib/return-to.ts:27-30`), so an editor link with a malformed return path crashes the editor
  too. *Acceptance:* a decode failure yields the 404 route or drops the parameter instead of throwing, with
  tests for a malformed path, a malformed query value and a malformed return value.

- [ ] **M19-58 The create-deck dialog re-requests the preset list forever when that read fails** — *should-fix.*
  The effect reads `createPresets.length` and, on a failed fetch, assigns a new empty array
  (`frontend/src/lib/views/DecksView.svelte:163-180`, the assignment at `:177`); the new array invalidates the
  state the same effect tracks, so the effect runs again at once and starts another request. *Reproduced* by
  mounting the real view against a rejecting `listPresets`: after opening the dialog and six flush rounds the
  spy had recorded `listPresets calls after 6 flush rounds: 21`. In a running app this is a continuous request
  loop for as long as the dialog stays open, and the dialog never reports why the list is empty. *Acceptance:*
  fetch once per dialog opening (a flag or a promise held outside the tracked state), show the failure in the
  dialog, and add a client test asserting exactly one request when the call rejects.

- [ ] **M19-59 An older note-list response overwrites the newer filter and rewrites the address** — *should-fix.*
  `loadData` has no request generation or abort (`frontend/src/lib/views/DeckDetailView.svelte:273-309`): it
  assigns `notes`, `total`, `page` and `appliedFilters` and then calls `replaceAddress(listAddress)`
  (`:297-301`) whichever response lands last, and the debounced filter input (`:345-348`), pagination
  (`:357-367`) and the post-bulk reload (`:417`) can overlap. *Reproduced* by mounting the view with two filter
  requests: after the newer `q=ab` response rendered `NEW`, resolving the older `q=a` request left the input
  showing `ab` while the list showed `OLD-A` and the address became `/decks/d1?q=a`. *Acceptance:* keep one
  in-flight generation per list (bump a counter and drop stale results, or abort the previous request) and add a
  client regression with two out-of-order filter responses that must end on the newest result and address.

- [x] **M19-60 The sidebar shows the previous account's deck names after a 401 session switch** — *should-fix.*
  `refreshSidebarDecks` returns without a request inside its 15-second throttle
  (`frontend/src/lib/components/shell/sidebar.ts:83`), and `resetSidebarDecks` is called only from the explicit
  logout path (`frontend/src/lib/components/shell/UserMenu.svelte:43-44`); `clearSession` on a 401 neither clears
  the cache nor the throttle (`frontend/src/lib/auth/index.ts:71-80`), and an in-flight refresh is not cancelled
  either (`sidebar.ts:79-87`). *Reproduced* against the real module: after account A's refresh the store held
  `{"decks":[{"id":"a1","name":"A private deck","due":7}],"totalDue":7}`, and a non-forced refresh for account B
  logged `B fetch called: 0` and left the store unchanged. After B signs in on the same tab the shell's route
  effect calls that throttled refresh, so A's deck names and due counts stay visible for the rest of the window.
  *Acceptance:* clear the sidebar cache and its throttle whenever the session becomes unauthenticated and on
  login, cancel a refresh belonging to the previous session, and add a test that switches two clients and shows
  only the second client's decks.
  *Closed by this commit (`fix(frontend): keep the sidebar to the current account`).*
  `sidebar.ts:71` now keeps a session generation; `resetSidebarDecks` (`:135`) bumps it, clears the throttle, the
  cached list and the shared in-flight slot, and a refresh that resolves under an older generation is discarded
  instead of written back. `clearSession` (`frontend/src/lib/auth/index.ts:75`) resets the sidebar, and `initAuth`,
  `login`, `completeTOTP` and `logout` reset it on every boundary where the session identity is established or
  lost, so the 15-second throttle cannot carry one account's window into the next. A request already in flight is
  not cancelled at the network level (the API client has no abort hook) but its result is dropped and it no
  longer blocks the new session's first refresh.
  `sidebar-session.test.ts` switches Alice to Bob after `clearSession`, asserts the cache is empty in between,
  that Bob's non-forced refresh still issues a request, and that Alice's late in-flight response leaves Bob's
  list intact; a third case asserts a successful `login` clears the list. Raw output of
  `cd frontend && npx vitest run src/tests/sidebar-session.test.ts src/tests/sidebar-sync.test.ts src/tests/auth.test.ts`:
  ```
   Test Files  3 passed (3)
        Tests  15 passed (15)
  ```
  The same test before the change failed with `expected [ 'A private deck' ] to deeply equal []` (the 401 switch
  kept the cache), `expected [ 'A private deck' ] to deeply equal []` (login kept it) and
  `expected "vi.fn()" to be called 1 times, but got 0 times` (Bob's refresh joined Alice's in-flight request).
  `npx vitest run` passes 588 tests in 58 files, and `npm run check` still reports exactly the 11 pre-existing
  `M19-62` errors in 2 files.

- [ ] **M19-61 Every review render computes four interval previews that no page displays** — *should-fix.*
  The SPA render handler calls `api.PreviewIntervals` for each card render and returns `intervals`
  (`internal/web/review_api.go:313-331`), and that function's doc comment says the review page shows the values
  under the four rating buttons (`internal/api/review_preview.go:25-26`). The frontend declares the field
  (`frontend/src/lib/api/types.ts:943-944`) but no view reads it: a module-wide search for `intervals` finds only
  the type and unrelated copy. Each render therefore adds a card read, a note read, a role check, a scheduler
  build and a card-state read, then four scheduler previews, all discarded. Verified by reading the handler, the
  API function and the search result; *Unverified:* the per-request cost was not measured. *Acceptance:* render
  the four intervals under the rating buttons from that response, or remove the response field and the
  per-render call so neither the API comment nor the type describes a UI that does not exist.

- [x] **M19-62 `npm run check` fails on main, so the CI frontend gate is red** — *should-fix.* Eleven type
  errors in two test files: `frontend/src/tests/import-url.client.ts:345,365,394,417,438,466,519,552,588` build
  preset fixtures with the retired `gate: {threshold, current, eligible}` shape, while `OptimizeGate` now
  requires `reviews`, `min` and `shortfall` (`frontend/src/lib/api/types.ts:1376-1381`); the same file at `:464`
  sets a `Deck.updated_at` that the server never sends; `frontend/src/tests/deck-export.client.ts:44` uses the
  stale block too. *Reproduced:* `npm run check` exits 1 with `svelte-check found 11 errors and 0 warnings in 2
  files`, and `.github/workflows/ci.yml:70` runs that script under `set -euo pipefail`. The 585 tests still pass,
  so nothing else notices the drift. *Acceptance:* `npm run check` exits 0 against fixtures built from the
  current payload shapes.
  *Closed by this commit (`fix(frontend): build test fixtures from the current payload shapes`).*
  The two preset fixtures in `import-url.client.ts` carry the current `OptimizeGate` fields
  (`gate: { reviews: 0, min: 1000, shortfall: 1000, eligible: false }`). Removing the stale `Deck.updated_at`
  exposed the next missing field, so the same literal now carries the required `role: 'owner'`. In
  `deck-export.client.ts` the remaining error was not the retired gate block the entry names: `setup(locale =
  'zh-CN')` inferred `string`, which `setLocale` cannot take, so the parameter is typed
  `SupportedLocale`. Raw output of `npm run check`:
  ```
  svelte-check found 0 errors and 0 warnings
  ```
  Before the change the same command ended `svelte-check found 11 errors and 0 warnings in 2 files`, listing
  the nine `gate` mismatches, `Property 'role' is missing in type ... but required in type 'Deck'` at
  `import-url.client.ts:464` (reported as `'updated_at' does not exist` while that key was present) and
  `Argument of type 'string' is not assignable to parameter of type 'SupportedLocale'` at
  `deck-export.client.ts:44`. The two affected suites pass:
  `npm run test:client -- src/tests/import-url.client.ts src/tests/deck-export.client.ts` ends
  ```
   Test Files  2 passed (2)
        Tests  44 passed (44)
  ```

- [ ] **M19-63 A failed privacy-policy read is displayed as "anyone may share with me"** — *should-fix.*
  `loadSharePolicy` swallows the error and leaves the initial value `sharePolicy = 'anyone'`
  (`frontend/src/lib/views/SettingsView.svelte:45,191-198`); the comment at `:197` argues that a failed read is
  the same as "no special setting", but the value is a stored setting the client never saw, so a user whose
  policy is `nobody` is shown the permissive option as selected and can save that section from a lying form.
  Verified by reading the initializer and the catch; *Unverified:* no request was failed against a running
  instance. *Acceptance:* render an explicit unread state with the controls disabled or retried until the policy
  arrives, and add a test where the read rejects and the permissive value is not shown as selected.

- [ ] **M19-64 Optimiser polling can overlap and write a stale "running" state after the job finished** —
  *should-fix.* `startPolling` uses `setInterval(pollOnce, 2000)`
  (`frontend/src/lib/views/PresetsView.svelte:266-271`) while `pollOnce` awaits the status request (`:282-301`),
  so a response slower than the interval lets a second poll start, and both are applied in arrival order with no
  generation check (`:303-310`). When the newer response sees the job finished, `stopPolling()` and `load()` show
  the result, and an older in-flight response can then apply the same preset back to `running` while polling has
  already stopped. Verified by reading the timer, the await and the missing guard; *Unverified:* no
  slow-response run. *Acceptance:* keep one status request in flight (schedule the next with `setTimeout` after
  each response, or drop older generations) and add a test with two out-of-order status responses that must end
  on the finished state.

- [ ] **M19-65 `$&`, `$'` and `$$` in a parameter corrupt the interpolated message** — *nit.* `formatMessage`
  passes the value as the replacement argument of `String.replace` (`frontend/src/lib/i18n/index.ts:131`), so
  replacement patterns contained in user data are expanded instead of inserted. *Reproduced* with the real
  function: a deck named `A$&B` produced `Paused "A{name}B". ...`, a name of `$'` repeated the rest of the
  sentence, and `A$$B` produced `A$B`. The same class of bug was fixed for field renames in Go (the `M17-58`
  ledger row). *Acceptance:* pass a function as the replacement, and add a unit test with `$&`, `$'`, `` $` ``
  and `$$` in the parameter.

- [ ] **M19-66 English has no singular form, so counts of one read "1 decks" and "1 days"** — *nit.*
  `formatMessage` only substitutes placeholders (`frontend/src/lib/i18n/index.ts:121-136`) and the catalogs hold
  one form per key (`frontend/src/lib/i18n/locales/en.ts:324,383,1199,1568` among others). *Reproduced* with the
  real function: `decks.count` with `count: 1` gives `1 decks`, `stats.streak.days` gives `1 days`,
  `notetype.empty.cleanup.count` gives `1 empty cards` and `deck.tag_review.notes` gives `1 cards`; the deck
  list header and the stats streak print these. *Acceptance:* apply one plural rule per locale through
  `formatMessage` (an `Intl.PluralRules` variant key or an explicit singular key), with a test asserting both
  one and two for the count keys.

- [ ] **M19-67 `Note.external_ref` is typed as non-null while the server sends null** — *nit.* The response type
  declares `external_ref: string` (`frontend/src/lib/api/types.ts:165`), but the server writes `n.ExternalRef`,
  a `*string` (`internal/api/notes.go:106`, `internal/store/models.go:169`), so a note without an external
  reference arrives as `null`. Every current reader treats it as falsy
  (`frontend/src/lib/views/DeckDetailView.svelte:101,821-823`), so nothing breaks today; the declaration hides
  the null branch from the compiler. Verified by reading the two declarations. *Acceptance:* type the field as
  `string | null` or return an empty string, and let the compiler check the readers.

- [ ] **M19-68 `bulkResult` in the deck detail view is written and never read** — *nit.* The state is declared at
  `frontend/src/lib/views/DeckDetailView.svelte:186`, cleared at `:407,922` and assigned at `:410-411` only to be
  passed to `announceBulkResult`, which reads its argument; the template never reads the state. The toast is
  built from the argument, so the state changes nothing and suggests a result banner that does not exist.
  Verified with `grep -n bulkResult`. *Acceptance:* call `announceBulkResult(summarizeBulk(res))` and delete the
  three assignments, or render the state as the result banner its name promises.


- [x] **M19-69 A failed outbox status write re-sends the same batch without end** — *Blocker.* `drain` loops
  "select the due batch → deliver each → stop when the batch is short" (`internal/mail/outbox.go:264-285`) with
  no claim, lease or in-memory attempt counter; `deliver` sends first and records the outcome afterwards
  (`:299-317`), so a row whose `MarkOutboxSent` write failed still matches the next SELECT
  (`internal/store/outbox.go:84-96`). With a full batch the second pass selects the same rows again and the
  only exit is context cancellation. *Reproduced* with two queued mails, `BatchSize: 2` and an injected failure
  on the `sent` update: `2 mails queued, MarkOutboxSent fails: 3551 deliveries in 300ms` — each of the two mails
  re-delivered about 5 900 times a second, every one a real SMTP transaction. The failure that starts it is "reads
  work, writes fail", which is what a full database volume produces, and that is also the state in which a
  restart cannot help. `MarkOutboxRetry` and `MarkOutboxFailed` failing cause the same loop (`:111-135`).
  *Acceptance:* stop the round when a status write fails and let the next tick retry, or claim each row with a
  lease or attempt increment inside the same statement; a regression makes the status write fail and asserts a
  bounded number of sends and that the round returns.
  *Closed by the `fix(mail): stop the outbox round when a status write fails` commit on `main`.*
  `deliver` (`internal/mail/outbox.go:296`) now returns the error from `MarkOutboxSent`,
  `MarkOutboxRetry` and `MarkOutboxFailed` (and from resolving the SMTP config); `drain` (`:280`) logs
  `mail: delivery round stopped after a failed status write` and returns on the first error. The row
  stays as the next SELECT expects it, so the next tick retries it; this trades one bounded duplicate
  delivery for an unbounded loop, which is the trade the acceptance allows. `TestFailedStatusWriteEndsTheRound`
  (`internal/mail/outbox_test.go`) queues two mails, installs a SQLite trigger that aborts the
  `status = 'sent'` UPDATE, drains with `BatchSize: 2`, and asserts the drain returns within two seconds,
  sends at most one batch and leaves both rows pending. Raw output of
  `go test ./internal/mail/ -run TestFailedStatusWriteEndsTheRound -count=1 -v`:
  ```
  === RUN   TestFailedStatusWriteEndsTheRound
  --- PASS: TestFailedStatusWriteEndsTheRound (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/mail	0.022s
  ```
  With only `outbox.go` reverted the same test failed with
  `drain did not return after the status write failed; Send calls = 8842` after the two-second guard.
  `go test ./internal/mail/ -count=1` passes.

- [x] **M19-70 Shutdown abandons a running optimisation and leaves its row unfinished** — *should-fix.*
  `runServe` builds the signal context at `cmd/engram/main.go:187` and hands it to every worker — retention,
  outbox, reminder, digest (`:211-248`) — but the job runner is started earlier, inside `newWebServer`, with
  `context.Background()` (`:383`). The runner has no `Stop`, nothing waits for it, and `srv.Run(ctx)` returns on
  SIGTERM (`internal/web/server.go:473-485`); the process then exits with the job's context still live, so the
  optimiser subprocess is never signalled, is reparented and keeps running until it finishes or dies writing to
  the closed stdout pipe, and `jobs.status` stays `running` until the next start reclaims it as
  `interrupted by restart`. That reclaim goes straight to the store (`internal/jobs/jobs.go:255-268`) and never
  calls the `OnFailure` hook, so no administrator is told. The Runner's doc claims the opposite
  (`jobs.go:233-238`, "when ctx is cancelled … the subprocess currently executing is killed"), and
  `ARCHITECTURE.md` section 7 lists the runner among the workers that stop on shutdown. *Reproduced* for the
  second half, with a runner whose context is cancelled mid-job (what correct wiring would do):
  `/bin/sleep 30` is killed, then `finish job failed failed job_id=1 error="update job 1: context canceled"` and
  `after cancelling the worker ctx: status="running" error=<nil> finished_at=false`, because `execute` writes
  the failure with the already-cancelled context (`jobs.go:411-415`). *Unverified:* that the orphaned adapter
  survives a real process exit; it depends on whether it writes to the dead pipe first. *Acceptance:* pass the
  signal context to `Runner.Start`, add a `Stop` that waits for `execute` to reach a terminal state, and write
  that state with a context that is not cancelled (`context.WithoutCancel`), so shutdown leaves `failed` rather
  than `running`; a regression starts a long job, cancels the process context, and asserts the row is terminal,
  the process group is gone, and the next `Start` has nothing to reclaim.
  *Closed by this commit (`fix(jobs): stop the runner on shutdown and persist a terminal job state`).*
  `runServe` now creates the signal context before assembling the web service
  (`cmd/engram/main.go:184`) and passes it to `newWebServer`, which calls `Runner.Start(ctx)`; `defer srv.StopJobs()`
  runs before the database closes. `Runner.Start` (`internal/jobs/jobs.go:244`) derives the worker context and
  tracks the worker goroutine, and `Runner.Stop` (`:269`) cancels that context and waits for the worker, so
  `runProcess` kills the process group and `execute` finishes before the process exits. `execute` (`:415`) writes
  every terminal state, including the failure that follows a cancelled run, through `context.WithoutCancel(ctx)`,
  so the row ends `failed` with `finished_at` instead of staying `running`. `Stop` is idempotent and safe when
  `Start` was never called.
  `TestStopLeavesRunningJobTerminal` (`internal/jobs/shutdown_test.go`) starts a shell that writes its background
  child's pid and waits, cancels the process context while the job is `running`, calls `Stop`, and asserts the row is
  `failed` with `finished_at`, the child process is gone, and a fresh `RecoverStale` reclaims zero rows.
  `TestStopWithoutStartIsSafe` covers the no-worker case. Raw output of
  `go test ./internal/jobs/ -run 'TestStop' -count=1 -v`:
  ```
  === RUN   TestStopLeavesRunningJobTerminal
      shutdown_test.go:117: job 1 status=failed finished=true error="job canceled: context canceled" child_pid=4025984 gone=true reclaimed=0
  --- PASS: TestStopLeavesRunningJobTerminal (0.02s)
  === RUN   TestStopWithoutStartIsSafe
  --- PASS: TestStopWithoutStartIsSafe (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/jobs	0.027s
  ```
  With only the post-`runProcess` terminal write reverted from `persistCtx` to `ctx`, the same run failed with
  `ERROR finish job failed failed job_id=1 error="update job 1: context canceled"` and
  `job status after shutdown = "running", want "failed" (a cancelled shutdown must not leave it running)`.
  `go test ./internal/jobs/ -count=1` passes in `4.010s`, `go test ./cmd/engram/ -count=1` in `0.328s`, and
  `go vet ./internal/jobs/ ./cmd/engram/ ./internal/web/` passes.

- [x] **M19-71 A job cancelled while it is still queued runs anyway and is reported succeeded** — *should-fix.*
  The worker reads the row at `internal/jobs/jobs.go:371-373` and only then registers the cancel handle that
  `Cancel` looks for at `:376-382`; `Cancel`'s queued branch loads the row itself and writes `failed` directly
  (`:297-309`). `Store.MarkRunning` updates by id with no status predicate (`internal/jobs/store.go:125-130`,
  through `:161-170`), so on the interleaving "worker read `queued` → Cancel wrote `failed` → worker marks
  `running`" the terminal row is flipped back and the subprocess executes; for an optimisation the write-back
  then applies the new weights to the preset and `FinishSucceeded` replaces the cancellation with `succeeded`.
  *Reproduced* deterministically by stalling the worker inside the ByID query after the row was scanned, then
  cancelling: `Cancel err=<nil>; status right after Cancel="failed"` and `final status="succeeded" error=<nil>
  started_at=true subprocess_ran=true`. The existing cancel test covers only a job that is already running
  (`internal/jobs/cancel_test.go:46`), so nothing pins the queued branch. *Acceptance:* make every transition a
  conditional update (`WHERE status = 'queued'`) and treat zero affected rows as "already terminal, do not
  start"; a regression cancels a queued job at the moment the worker has read it and asserts the subprocess
  never runs and the row stays `failed`.
  *Closed by this commit (`fix(jobs): refuse to start a job that left the queue`).*
  `Store.MarkRunning` (`internal/jobs/store.go:132`) and the new `Store.FinishQueuedFailed` (`:151`) update a
  row only while `status = 'queued'` and return `ErrJobNotQueued` (`:127`) when they affect no row.
  `execute` (`internal/jobs/jobs.go:408`) treats that sentinel as "already terminal" and returns without
  starting the subprocess or calling `fail`, and `Cancel` (`:283`) retries once so a job that reached
  `running` is cancelled through its registered handle instead of being reported cancelled while it runs.
  `TestCancelQueuedJobAtPickupNeverRunsCommand` (`internal/jobs/cancel_test.go:115`) stalls the worker's
  `ByID` read with a GORM query callback after the queued row was scanned, cancels while it is held, then
  asserts the command builder never ran, the status is `failed`, the error is `CancelReason`, and
  `started_at` is NULL. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/jobs/ -run TestCancelQueuedJobAtPickupNeverRunsCommand -count=1 -v`:
  ```
  === RUN   TestCancelQueuedJobAtPickupNeverRunsCommand
  2026/11/02 03:23:12 INFO job left the queue before it started job_id=1
  2026/11/02 03:23:12 /home/nite/dev/engram/internal/jobs/cancel_test.go:158
  [warn] removing callback `test:queued_pickup` from /home/nite/dev/engram/internal/jobs/cancel_test.go:158
  --- PASS: TestCancelQueuedJobAtPickupNeverRunsCommand (0.31s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/jobs	0.322s
  ```
  Before the change the same test failed with `the subprocess ran after the job was cancelled while queued`,
  `status after cancelling the queued job = "succeeded", want "failed"`, and
  `cancelled job error = <nil>, want "cancelled by admin"`. `go vet ./internal/jobs/` and
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/jobs/ -count=1` pass.

- [x] **M19-72 A child that leaves the process group keeps the timeout from ending the job** — *should-fix.*
  `runProcess` hands the child a `tailBuffer` for stdout and stderr (`internal/jobs/jobs.go:560-562`), so
  `exec` copies through an `os.Pipe` and `cmd.Wait` does not return until every writer of that pipe is gone
  (`:567-568`, waited on at `:577,581`). `killProcessGroup` (`internal/jobs/process_unix.go:22-30`) only reaches
  the group the child was put in (`:17-19`); a grandchild that calls `setsid` or `setpgid` escapes it, keeps
  the pipe open, and `<-done` blocks for as long as that process lives, so neither the timer nor the context
  bounds the job any more and the single job slot stays occupied. *Reproduced*: with a 200 ms timeout and
  `/bin/sh -c "setsid sleep 6 & wait"` the job reached `failed` after `6.005s` with
  `error "job timed out after 200ms"`. The process-group test (`internal/jobs/jobs_test.go:178-231`) covers only
  a grandchild that stays in the group. *Acceptance:* bound the wait independently of the pipe (`cmd.WaitDelay`,
  or close the parent's read end when the timer or context fires) so that `runProcess` returns within a fixed
  grace period; a regression uses a `setsid` grandchild and asserts the job is terminal well before that
  process would have exited on its own.
  *Closed by this commit (`fix(jobs): bound the wait for output pipes a killed job left open`).*
  `runProcess` (`internal/jobs/jobs.go:591`) sets `cmd.WaitDelay = processWaitDelay` (`:596`); the constant is
  two seconds (`:583`). After the timer or the context has killed the process group, `cmd.Wait` therefore
  closes a pipe an escaped grandchild still holds and returns within that grace period, and the timeout and
  cancellation paths already ignore `Wait`'s result and report `ErrTimedOut` or the cancellation. A
  well-behaved child's buffered output is read before the bound applies, so the captured log tail is unchanged
  for ordinary jobs. `TestOptimiseTimeoutEndsWhenGrandchildEscapesTheGroup`
  (`internal/jobs/jobs_test.go:236`) runs `/bin/sh -c "setsid sleep 30 & echo child=$!; wait"` under a 200 ms
  timeout, asserts the job ends terminal inside six seconds, still reports `job timed out after 200ms`, and
  checks that the escaped grandchild is alive at that moment (so the case really reproduced) before killing
  it in cleanup. Raw output of
  `go test ./internal/jobs/ -run TestOptimiseTimeoutEndsWhenGrandchildEscapesTheGroup -count=1 -v`:
  ```
  === RUN   TestOptimiseTimeoutEndsWhenGrandchildEscapesTheGroup
      jobs_test.go:272: job 1 status=failed elapsed=2.208s error="job timed out after 200ms" escaped_grandchild_pid=3978933 still_alive=true
  --- PASS: TestOptimiseTimeoutEndsWhenGrandchildEscapesTheGroup (2.32s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/jobs	2.331s
  ```
  With only the `cmd.WaitDelay` assignment removed, the same run failed with
  `job 1 did not reach "failed" within 6s (last status "running")`. `go test ./internal/jobs/ -count=1` passes
  in `4.002s` and `/home/nite/.hermes/cache/scratch/pgtest ./internal/jobs/ -count=1` in `4.092s`.

- [x] **M19-73 The preset write-back and the job's terminal write are two writes, so a failing finish either
  leaves the job running or reports failure after the weights were applied** — *should-fix.* `FinishOptimize`
  writes the preset first and the job row second (`internal/jobs/optimize.go:65-74`), and `execute` maps any
  error from it to `fail` (`jobs.go:427-431`); on the generic path a failing `FinishSucceeded` is only logged
  and returns (`:435-439`), although that function's comment says "any failing step must end in failed, so a job
  never stays in running forever". Both outcomes reproduced by failing only the `status=succeeded` update:
  (a) `status="running" finished_at=false failure_hook_calls=0 active=true next_enqueue=a job is already
  running` — `Store.Active` counts the row as in-flight, so every later enqueue is 409 until a restart;
  (b) `FinishOptimize err=...; after execute's fail(): job status="failed" error="finish optimize: update job 1:
  injected final write failure"; preset weights written=true` — the user is told the optimisation failed
  although the new weights are in the preset, and the administrator's failure mail goes out for a job that took
  effect. *Acceptance:* write the preset and the job row in one transaction, or make the write-back idempotent
  and resumable from `result_json`, and retry or fail the terminal write instead of returning; regressions cover
  a failing finish and a failing write-back and assert that nothing is half-applied and no job is non-terminal.
  *Closed by this commit (`fix(jobs): commit the preset write-back with the job's terminal row`).*
  `FinishOptimize` (`internal/jobs/optimize.go:66`) now opens one transaction and runs both writes inside it:
  `applyOptimizedWeights` (`:82`) takes the caller's handle and loads the job and the preset from it, and the new
  `Store.finishSucceeded` (`internal/jobs/store.go:185`, through `updateDB` at `:213`) writes the terminal row on
  the same handle. A failure in either write rolls both back, so `execute`'s existing `fail` path
  (`jobs.go:456-459`) leaves `failed` with the old weights intact instead of `failed` with the new ones applied.
  The generic path (`jobs.go:463-468`) no longer only logs a failed `FinishSucceeded`: it now calls `fail` with the
  failing step in the message, so the row ends terminal and `Store.Active` stops counting it.
  `TestOptimizeFinishIsAtomic` (`internal/jobs/optimize_atomic_test.go`) runs a real `execute` against SQLite
  triggers that abort (a) the `status='succeeded'` update, (b) the preset update and (c) the generic terminal
  update, and asserts in each case that the job ends `failed` with `finished_at` set and `result_json` empty, that
  the preset still carries its old weights, and that `Store.Active` finds nothing. `TestOptimizeFinishFailureNamesTheStep`
  pins the `finish job succeeded:` text the operator sees. Raw output of
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/jobs/ -run 'TestOptimizeFinish' -count=1 -v`:
  ```
  === RUN   TestOptimizeFinishIsAtomic
  === RUN   TestOptimizeFinishIsAtomic/a_failing_terminal_write_rolls_the_preset_back
  === RUN   TestOptimizeFinishIsAtomic/a_failing_write-back_leaves_the_job_terminal
  === RUN   TestOptimizeFinishIsAtomic/a_failed_generic_finish_ends_the_job
  --- PASS: TestOptimizeFinishIsAtomic (0.04s)
  === RUN   TestOptimizeFinishFailureNamesTheStep
  --- PASS: TestOptimizeFinishFailureNamesTheStep (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/jobs	0.061s
  ```
  With only `optimize.go`, `store.go` and `jobs.go` reverted, the same run failed with
  `preset weights = "[0.4,0.6]", want the old "[\"old\"]": a rolled-back finish must not apply them`,
  `job status = "running", want "failed" (the failed write must not leave the job non-terminal)`,
  `Active() = job 1 "running", want none: the row must be terminal` and
  `status/error = "running"/<nil>, want failed with the failing step named`. `/home/nite/.hermes/cache/scratch/pgtest
  ./internal/jobs/ -count=1` and `go test ./internal/web/ -run 'TestPreset|TestOptimize|TestJob' -count=1` pass.

- [x] **M19-74 A stalled SMTP relay blocks the send forever; the configured timeout only covers the dial** —
  *should-fix.* `DefaultTimeout` is documented as "the timeout of a single SMTP interaction (connect +
  handshake + delivery)" (`internal/mail/smtp.go:25-26`) and `SMTPSender.timeout` is passed only to
  `net.Dialer.Timeout` and `tls.DialWithDialer` (`:115-146`). After `smtp.NewClient` nothing sets a deadline on
  the connection, and `net/smtp` takes no context, so `MAIL`, `RCPT`, `DATA` and the wait for the server's reply
  (`:69-85`) are unbounded. One relay that accepts and then stops talking blocks `deliver` →
  `drain` → the worker's only goroutine: no mail is delivered, no round error is logged, and `Lifecycle.Stop`
  waits on that goroutine, so shutdown hangs too. *Reproduced*: a listener that accepts and never sends the 220
  greeting, `SMTPSender{Timeout: 200ms}` and a 1.5 s context deadline — `Send still blocked after 2s with
  Timeout=200ms and a 1.5s context deadline (the context is ignored)`. *Acceptance:* put a deadline on every
  connection, set or refresh it per stage, and close the connection when the context is cancelled, so that one
  `Send` is bounded by the configured timeout; a regression points the sender at a silent listener and asserts
  it returns an error within the timeout and that a subsequent round can still deliver.
  *Closed by this commit (`fix(mail): bound every SMTP stage and close a cancelled connection`).*
  `dial` (`internal/mail/smtp.go:133`) now returns the underlying connection and starts
  `closeConnOnContext` (`:188`), which closes it when `ctx` ends and therefore unblocks any read or write
  `net/smtp` is waiting on. `refreshDeadline` (`:182`) restarts the configured timeout before the greeting,
  `AUTH`, `MAIL`, `RCPT`, `DATA`, the message write, the terminating dot and `QUIT`; `Test` follows the same
  pattern. The implicit-TLS path uses a `tls.Dialer` so the context also bounds that handshake. One `Send` is
  now bounded stage by stage, and shutdown no longer waits on a silent relay.
  `TestSMTPSenderBoundsAStalledRelay` and `TestSMTPSenderStopsOnContextCancellation`
  (`internal/mail/smtp_test.go:103,143`) point the sender at `silentRelay` (`:79`), a listener that accepts
  and never writes: the first asserts `Send` errors within two seconds with `Timeout` 200 ms and that the next
  round delivers to a working relay; the second uses a 30-second timeout and asserts a cancelled context ends
  the handshake within two seconds. Raw output of `go test ./internal/mail/ -run
  'TestSMTPSenderBoundsAStalledRelay|TestSMTPSenderStopsOnContextCancellation' -count=1 -v`:
  ```
  === RUN   TestSMTPSenderBoundsAStalledRelay
  --- PASS: TestSMTPSenderBoundsAStalledRelay (0.20s)
  === RUN   TestSMTPSenderStopsOnContextCancellation
  --- PASS: TestSMTPSenderStopsOnContextCancellation (0.10s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/mail	0.316s
  ```
  With only `internal/mail/smtp.go` reverted, the same run failed with
  `Send() did not return within 2s with Timeout=200ms: the stalled relay held the delivery` and
  `Test() did not return within 2s of the context being cancelled`. `go vet ./internal/mail/` and
  `go test ./internal/mail/ -count=1` pass.

- [ ] **M19-75 The multipart boundary is a constant, so a value containing that line splits the message** —
  *nit.* `buildMessage` hard-codes `const boundary = "engram-mail-boundary"` (`internal/mail/smtp.go:177-193`),
  while variable values are only HTML-escaped (`template.go:231-240`), which leaves CR and LF intact; a value
  therefore appears verbatim in both the HTML body and the derived text part. *Reproduced*: `Render` with the
  deck name `"My deck\r\n--engram-mail-boundary\r\nContent-Type: text/plain\r\n\r\nINJECTED PART\r\n
  --engram-mail-boundary--"` produced `boundary occurrences in the message: 7`,
  `injected part present verbatim: true`, `plain-text part carries the payload: true`. Reachability today is
  narrow, and is stated so that this is not over-read: deck names, deck descriptions and usernames reject C0
  controls (`internal/store/package_import.go:189-197`), but preset names have no text rule
  (`internal/store/preset.go:83-97`, `internal/web/presets_api.go:539-560`) and reach `{{preset}}` in the
  `optimize_done` mail (`internal/mail/vars.go:111`), which is sent to that preset's own owner — so the only
  message a user can corrupt this way is their own. *Acceptance:* choose a random boundary per message and
  assert it does not occur in either body, and give preset names the control-character rule deck names have; a
  regression sends a value containing a literal `--engram-mail-boundary` line and parses the result back into
  its two parts.

- [ ] **M19-76 The outbox's `LookupEnv` seam is accepted and silently ignored** — *nit.* `Deps.LookupEnv` is
  documented as the environment stand-in for tests (`internal/mail/outbox.go:77-78`), but `NewOutbox` builds
  `NewResolver(deps.DB, deps.Secrets)` without it (`:151`), so the field never reaches the resolver and
  `os.LookupEnv` is always used. *Reproduced*: with a stand-in that answers `SMTP_HOST` and `SMTP_FROM`,
  `Configured with the Deps.LookupEnv stand-in = false (want true)` and `resolver lookup is nil: true`. A caller
  that trusts the seam silently tests the real environment instead. *Acceptance:* pass `deps.LookupEnv` into
  `NewResolver`, or delete the field and the two comments that promise it; a test sets it and asserts
  `Configured()` is true.

- [x] **M19-77 The exported review log survives every failed optimisation job** — *nit.* `CommandBuilder`
  creates the job directory and writes the user's whole review history to `review-log.jsonl`
  (`internal/jobs/optimizer.go:149-167`) under a per-process root that carries the pid (`:91-92`), and only
  `Complete` removes that directory, in a deferred call (`:185-191`). Every other terminal path — the adapter
  exiting non-zero, a timeout, an administrator cancel, a `Complete` error — returns before that call, so the
  log stays on disk for the life of the process, and after a crash or restart no later run has the same pid and
  nothing ever removes it. The data is protected by a 0700 directory, so this is retention rather than
  disclosure. Verified by reading the three exit paths; no command was needed. *Acceptance:* remove the job
  directory on every terminal path (a defer in `execute`, or a cleanup call in `fail`) and sweep the per-pid
  roots at startup; a regression runs a failing job and asserts no `review-log.jsonl` remains.
  *Closed by this commit (`fix(jobs): remove the exported review log on every terminal path`).*
  `Command` gained `Cleanup func()` (`internal/jobs/jobs.go:106`), and `Runner.execute` defers it as soon as the
  builder returns a command (`:468`), so an adapter exit code, a timeout, an administrator cancel and a failing
  `Complete` all release the run's files. `Optimizer.CommandBuilder` sets it to `discardWorkDir`
  (`internal/jobs/optimizer.go:184`), a best-effort `os.RemoveAll` that logs a failure without replacing the
  run's outcome (`:192`); the two export-failure returns call the same helper, and `Complete`'s own defer uses it
  instead of a second copy. `NewOptimizer` removes its per-pid work root at construction (`:97`), so a crash or a
  restart with the same process id cannot leave an exported log there. A removal failure is still only logged;
  the file stays until the next startup sweep.
  `TestFailedJobRemovesTheExportedReviewLog`, `TestOptimizerBuilderRemovesTheWorkDirWhenTheExportFails` and
  `TestNewOptimizerClearsAStaleRoot` (`internal/jobs/optimizer_cleanup_test.go`) cover a job whose process exits
  non-zero, a review-log export that fails after the work directory exists, and a stale per-pid root. Raw output
  of `go test ./internal/jobs/ -run
  'TestFailedJobRemovesTheExportedReviewLog|TestOptimizerBuilderRemovesTheWorkDirWhenTheExportFails|TestNewOptimizerClearsAStaleRoot' -count=1 -v`:
  ```
  === RUN   TestFailedJobRemovesTheExportedReviewLog
  --- PASS: TestFailedJobRemovesTheExportedReviewLog (0.02s)
  === RUN   TestOptimizerBuilderRemovesTheWorkDirWhenTheExportFails
  --- PASS: TestOptimizerBuilderRemovesTheWorkDirWhenTheExportFails (0.01s)
  === RUN   TestNewOptimizerClearsAStaleRoot
  --- PASS: TestNewOptimizerClearsAStaleRoot (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/jobs	0.055s
  ```
  (The middle case also prints the SQLite driver's `no such table: reviews` line, which is the injected export
  failure.) With the deferred cleanup, the two builder cleanups and the startup sweep removed, the same run failed
  with `work dir .../job-1 still exists after the failed job, want it removed`,
  `work dir .../job-7 still exists after the export failed, want it removed` and
  `stale root /tmp/engram-optimize-<pid> still exists after NewOptimizer, want it removed`.
  `go vet ./internal/jobs/` and `go test ./internal/jobs/ -count=1` pass (`4.032s`).

- [x] **M19-78 A note's `extra` can be written and preserved but never read back** — *should-fix.* The import
  schema advertises `extra` as "Free-form metadata kept as-is for the caller and round-tripped on export"
  (`schema/note-import.schema.json:71-74`), and the store does keep it: `extractNoteColumns` turns a field-map key
  named `extra` into `notes.extra_json` (`internal/store/note.go:200-226`), the single-note update copies the
  stored value forward (`internal/api/service.go:749-758`), and the update statement writes the column
  (`internal/store/note.go:543`). No read path exposes it. `NotesJSON`, the one shape shared by the note list, `GET /notes/:id`, MCP
  `search_notes` and `get_note`, emits id, deck_id, note_type, fields, tags, created_at, updated_at,
  external_ref, source_url and suspended, and not `extra` (`internal/api/notes.go:75-113`, keys at `:106-107`).
  The JSON/CSV deck export has no extra column either (`ExportRow`, `internal/api/service.go:1079-1091`; header
  at `internal/api/export.go:115-121`), and `ARCHITECTURE.md:246-249` states that the `.apkg` deliberately drops
  it. A caller therefore stores metadata it can never retrieve. *Unverified:* no request was sent; the evidence
  is the complete list of `ExtraJSON` references outside tests, which contains no response mapping.
  *Acceptance:* one read surface (the note list/get shape, MCP `get_note`, or the JSON/CSV export) returns the
  stored value, with a test that writes `fields.extra` and reads the same JSON back; if the column is meant to
  stay server-side, the schema description says so instead of promising a round trip.
  *Closed by this commit (`fix(api): return a note's stored extra metadata`).*
  `NotesJSON` (`internal/api/notes.go:75`) now includes `extra`, decoded by `noteExtra` (`:122`) from
  `notes.extra_json`: a note without metadata, or a row whose text is not valid JSON, yields `null` instead of
  failing the read. Because REST list, REST get, update responses and the MCP `search_notes`/`get_note`/update
  tools all go through this one function, the value is retrievable from every read surface without a second
  mapping. The schema description for `extra` now says it is "returned in every note response (list, single
  note and the MCP read tools)" instead of promising a round trip that the JSON/CSV export and the `.apkg`
  writer do not perform.
  `TestNoteExtraRoundTrip` (`internal/api/notes_test.go`) imports a note with
  `"extra":{"licence":"example","level":2}` and asserts the same object on both `GET /api/v1/notes/{id}` and the
  deck note list. Raw output of `go test ./internal/api/ -run TestNoteExtraRoundTrip -count=1 -v`:
  ```
  === RUN   TestNoteExtraRoundTrip
  --- PASS: TestNoteExtraRoundTrip (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/api	0.029s
  ```
  With only `internal/api/notes.go` reverted, the same run failed with
  `single note response has no extra key: {"created_at":...,"external_ref":null,"fields":{"back":"a","front":"q"},...}`.
  `go vet ./internal/api/`, `go test ./internal/api/ -run 'TestNote|TestBulkImport|TestImport' -count=1` and
  `go test ./internal/mcp/ -count=1` pass.

- [ ] **M19-79 The schema README documents a `kind` request alias the server rejects** — *should-fix.*
  `schema/README.md:55-59` tells a client that `kind` "remains accepted as a transitional alias for `note_type`",
  that the server "resolves it to the deck's stock note type for that kind, and creates that note type when the
  deck has none", and that "when a note carries both, `note_type` wins". `ImportNote` has no such field
  (`internal/api/service.go:470-481`); the first pass validates only `item.NoteType` and refuses an empty one with
  the per-row reason "note_type is required" (`:551-556`), or the whole body with 409 under `on_conflict=fail`
  (`:633`). The schema already rejects that body, and `internal/api/schema_test.go:44-45` pins the rejection with
  the comment "The transitional card kind is gone from the contract", so the README is the only place that still
  says otherwise. *Reproduced:* `uvx check-jsonschema --schemafile schema/note-import.schema.json` on
  `{"notes":[{"kind":"basic","fields":{"front":"q","back":"a"}}]}` reports `'note_type' is a required property`;
  the server-side refusal is read, not run. *Acceptance:* the paragraph is removed, or rewritten to say what is
  true, and no committed document promises a request key the decoder does not have.

- [ ] **M19-80 The documented local start command fails before the server opens the database** — *should-fix.*
  `AGENTS.md:225-226` gives the only `go run` line in the repository: `DB_DRIVER=sqlite DB_DSN=data/engram.db
  AUTO_MIGRATE=1 go run ./cmd/engram serve`. `SESSION_SECRET` and `ENCRYPTION_KEY` are declared `required: true`
  with no default (`internal/config/config.go:56-59`), and `Load` returns an error naming every missing one
  (`:83-100`), so the command exits before it serves. The two documents that should own the setup point at each
  other: `README.md:186` sends the reader to `CONTRIBUTING.md` for "local development setup", and
  `CONTRIBUTING.md:88-90` sends the reader back to the README quick start, which only runs the published Docker
  image. *Reproduced:* `env -u SESSION_SECRET -u ENCRYPTION_KEY DB_DRIVER=sqlite DB_DSN=/tmp/engram.db
  AUTO_MIGRATE=1 go run ./cmd/engram serve` printed `level=ERROR msg=fatal error="missing required environment
  variable(s): ENCRYPTION_KEY, SESSION_SECRET"` and exited 1. *Acceptance:* one committed place shows a complete
  local start (driver, DSN, both secrets, migration, first-admin step) that succeeds on a fresh checkout, and the
  other document links to it.

- [ ] **M19-81 CONTRIBUTING names a Go minimum the module refuses to build with** — *should-fix.*
  `CONTRIBUTING.md:45` states "**Go 1.26 or newer** (the module declares `go 1.26` ...)", while `go.mod:3`
  declares `go 1.27.0` and `README.md:4` advertises "Go 1.27+". Since Go 1.21 the toolchain refuses to build a
  module whose `go` line is newer than the running toolchain, so a contributor who installs the documented
  minimum cannot run any command in this file. *Reproduced:* `head -3 go.mod` prints `go 1.27.0`; no Go 1.26
  toolchain was available to run the refusal, so the failure text is read from the toolchain rule.
  *Acceptance:* the minimum in `CONTRIBUTING.md`, the `go` line in `go.mod` and the README badge name the same
  version, and the paragraph states which one is authoritative.

- [ ] **M19-82 CONTRIBUTING still documents the removed card-type system and names a package that does not
  exist** — *should-fix.* `CONTRIBUTING.md:173-174` restates the registration rule as "**Card types are
  registered, not hardcoded.** Adding a type means adding one file and registering it", and `:197` names
  `internal/cardtype` as a core coverage target. That package was removed with the M17 content-model merge (the
  row for that merge in `ROADMAP.md` section 2 records that `internal/cardtype` is gone), no directory of that
  name exists, and the rule the
  repository now keeps is "Note types are data. Answer controls are registered" (`AGENTS.md:74-75`), with
  coverage targets `internal/schedule`, `internal/notetype` and `internal/answer` (`AGENTS.md:111-113`). A
  contributor following this section looks for a card-type registry and a test package that are not there.
  *Reproduced:* `ls internal` lists 24 packages and none is `cardtype`; the removal is also stated in the ledger.
  *Acceptance:* CONTRIBUTING restates the note-type/answer-control rule and the three coverage targets in the
  words `AGENTS.md` uses.

- [ ] **M19-83 The optimiser README says the service does not run the adapter, which the service does** —
  *should-fix.* `tools/optimizer/README.md:142-144`, under "Current limits (not yet verified)", says "The Go side
  (`internal/jobs`) does not call the adapter at runtime yet; the command injection point is exercised by a Go
  integration test that skips when the binary is absent." The production wiring exists: `cmd/engram/main.go:335`
  passes `optimizer.CommandBuilder()` to the runner, that builder is `internal/jobs/optimizer.go:140`,
  `internal/jobs/jobs.go:553-581` runs the process under a timeout, and `internal/jobs/optimize.go:65-74` writes
  the weights back to the preset. `ARCHITECTURE.md:24-25` and `:315-316` describe the same runtime path, and four
  open entries in this file (M19-70, M19-71, M19-73, M19-77) were reproduced against it. *Unverified:* no command
  was run for this entry; the call chain is read at the lines above. *Acceptance:* the section lists only limits
  that are still true (real-review-data correctness, no exit-4 fixture) and states that the job runner invokes
  the adapter in production.

- [ ] **M19-84 The README publishes a real host name and a demo account's password, and the sanitisation rule
  admits neither** — *should-fix.* `README.md:12` (and `README.zh.md:12`) carries a live demo URL on a real
  domain plus the credentials `test` / `testdemo`; `README.md:5,98,127` carries the Docker Hub repository name and
  `:203` a community link on a real domain. `AGENTS.md:43-50` forbids real domain names, host names and passwords
  in any committed file and names the Go module path as "the one deliberate exception"; `CONTRIBUTING.md:137-140`
  repeats the rule with the same single exception. Nothing automated covers the rule any more
  (`AGENTS.md:52-55`), so review by eye is the only check that can catch this. *Reproduced:* `git grep -n
  "nite07\|linux.do"` lists the README and README.zh.md lines named above, `.github/workflows/release.yml:10`,
  `.goreleaser.yaml:89` and `docker-compose.yaml:20`, and the module path in `go.mod`, the Go imports and
  `CONTRIBUTING.md:299`, which the rule allows. *Acceptance:* either the publication exceptions (release image, live demo, community
  link) are written into `AGENTS.md` §2.2 and `CONTRIBUTING.md`, naming exactly those cases, or the values are
  replaced with placeholders and the demo credentials are no longer committed.

- [ ] **M19-85 CONTRIBUTING states the review log is append-only and omits the one deletion the code performs** —
  *should-fix.* `CONTRIBUTING.md:167-168` restates the invariant as "**`reviews` is append-only.** Every column is
  written from the first commit — it is the only fuel for parameter optimisation and cannot be reconstructed
  later", with no exception. The contract states the same rule with its single exception: undoing a review
  deletes that one row and writes a `review.undo` audit row (`AGENTS.md:64-69`), and the code does exactly that
  (`internal/schedule/actions.go:149` deletes the row). This is the section an external contributor is told to
  follow, and it presents as a violation the one deletion the product is built around. *Unverified:* the undo
  request was not run in this review; the deletion is read at the cited line and stated by the contract.
  *Acceptance:* CONTRIBUTING restates the exception with the rule, in the words `AGENTS.md` uses, so a reviewer of
  a review deletion does not have to guess which document wins.

- [ ] **M19-86 The bulk schema promises it never accepts a body the server rejects, and a whitespace tag breaks
  that** — *nit.* `schema/note-bulk.schema.json:5` says the schema "is therefore the stricter of the two for a
  request carrying repeated or blank entries, and it never accepts a body the server would reject", and
  `schema/README.md:38-40` repeats the claim. `tags.items.minLength: 1` does not reject a whitespace-only tag, but
  the service normalises the list before applying the bound (`internal/api/service.go:864-873`) and
  `NormalizeTags` trims and drops empty entries (`internal/store/note.go:750-759`), so `tags: [" "]` becomes an
  empty list and the request is refused with `invalid_request`. The same gap exists in
  `note-import.schema.json:38-42` for a whitespace-only `note_type`, which the service trims and then treats as
  missing (`service.go:551-556`). *Reproduced:* `uvx check-jsonschema --schemafile schema/note-bulk.schema.json`
  on `{"action":"add_tags","note_ids":["a"],"tags":[" "]}` prints `ok -- validation done`; the server side is
  read, not run. *Acceptance:* the schema expresses the trim (a `pattern` such as `\S`) or the sentence is
  replaced by "the server applies an additional trim the schema cannot express", so neither document claims an
  invariant the pair does not hold.

- [ ] **M19-87 ARCHITECTURE gives `schema/` a deck-package schema that no longer exists** — *nit.*
  `ARCHITECTURE.md:39` describes the directory as "JSON Schemas for note import, bulk notes, and deck packages".
  It contains `note-import.schema.json`, `note-bulk.schema.json`, `README.md` and two examples; the deck-package
  schema left with the `.edeck` format that `M18-5` removed (`AGENTS.md:155-158`). *Reproduced:* `ls schema`
  prints those four entries. *Acceptance:* the tree comment lists the two schemas the directory holds, or the
  removed schema is restored together with the decision that restores it.

- [ ] **M19-88 ARCHITECTURE's dependency table is not what its own regeneration command reports** — *nit.* The
  table's heading says the rows are "Imports between internal packages, as reported by `go list`"
  (`ARCHITECTURE.md:80`) and gives the command at `:100-102`, but the `web` and `api` rows (`:84`, `:86`) omit
  `internal/answer`, which both packages import. *Reproduced:* the documented `go list -f ...` command prints
  `internal/answer` in the import list of both `internal/api` and `internal/web`; neither documented row has it.
  *Acceptance:* regenerating with the command in the file yields a table that equals the file.

- [ ] **M19-89 No test validates the shipped note-import example against its schema** — *nit.* The bulk example
  is validated against its schema by `internal/api/note_bulk_schema_test.go:58-68`, and the import side validates
  only hand-written bodies (`internal/api/schema_test.go:27-58`); no test or CI step reads
  `schema/examples/note-import.example.json`, which is the only complete payload the schema README points an
  external tool at. The example has therefore already drifted from the server: it sends `source_url` at `:13`
  and `extra` at `:32-35` as top-level note keys, which `ImportNote` does not decode (M19-9), while the schema
  accepts them because both properties are declared. *Reproduced:* `grep -rn "note-import.example" --include=*.go
  .` returns nothing, and `uvx check-jsonschema` still reports the file valid. *Acceptance:* one Go test
  compiles both schema files and validates both shipped examples, so a schema change that invalidates an example
  fails the build.

- [x] **M19-100 A failed export is reported as an empty or truncated success** — *should-fix.* `internal/api/export.go:89-94`
  logs the `ExportCards` error and then closes the JSON body with the rows written so far and `"count":<written>`;
  `:106-112` does the same for CSV. The status is 200 either way, so a database failure, a page-scan failure and a
  genuinely empty deck produce the same response, and no field says the export was cut short. *Reproduced:* with the
  SQLite handle closed after the permission check, `streamJSON` answered `200 {"deck_ids":[…],"cards":[],"count":0}`
  for a deck that holds one card. *Acceptance:* a failure before the first row returns the 5xx `internal_error`
  envelope, a failure after it marks the truncation in the body, and a test forces a page read to fail and asserts
  each of the two outcomes.
  *Closed by this commit (`fix(api): report a failed export instead of a short success`).*
  `streamJSON` and `streamCSV` (`internal/api/export.go:74,126`) now write their fixed prefix (JSON) or header (CSV)
  from the first emit callback, not before the export starts, so a page read that fails before any row leaves nothing
  written and the handler answers 500 `internal_error` through `abortError`; a successful export of an empty deck
  still writes the prefix afterwards. A failure after at least one row keeps the 200 body but marks it: JSON closes
  the array and adds `"truncated":true` next to `"count"`, and the CSV file ends with a `#export_truncated` record
  (the field is additive, so a complete export's body is unchanged). The error still goes only to the log.
  `TestExportFailureBeforeFirstRowIsAnError` (`internal/api/export_failure_test.go`) closes the SQLite handle and
  calls each streamer directly, asserting 500, the `internal_error` envelope and no export content;
  `TestExportFailureAfterFirstRowIsMarked` sets `exportPageSize = 1`, closes the handle on the response's first
  write (the page in hand was already read, so its row still lands) and asserts the JSON parses with one card and
  `truncated:true`, and that the CSV keeps header plus row and ends with the marker. Raw output of
  `go test ./internal/api/ -run 'TestExportFailure' -count=1 -v`:
  ```
  --- PASS: TestExportFailureBeforeFirstRowIsAnError (0.02s)
      --- PASS: TestExportFailureBeforeFirstRowIsAnError/json (0.01s)
      --- PASS: TestExportFailureBeforeFirstRowIsAnError/csv (0.01s)
  --- PASS: TestExportFailureAfterFirstRowIsMarked (0.03s)
      --- PASS: TestExportFailureAfterFirstRowIsMarked/json (0.01s)
      --- PASS: TestExportFailureAfterFirstRowIsMarked/csv (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/api	0.056s
  ```
  With only `internal/api/export.go` reverted, the same run failed with
  `status = 200, want 500 (body "{\"deck_ids\":[\"…\"],\"cards\":[],\"count\":0}")` and
  `status = 200, want 500 (body "card_id,note_id,deck_id,template,fields_json,tags_json,external_ref\n")` for the
  first-row cases, `cards = 0, count = 0, want 1/1: the row already read must survive` and
  `truncated = false, want true: the export stopped before the last page`, and
  `lines = 2, want header, one row and the truncation marker`. `go test ./internal/api/ -run 'TestExport' -count=1`
  passes.

- [x] **M19-101 An unrecognised store failure on a write is answered 400 `invalid_request` with the driver's text** —
  *should-fix.* `internal/api/service.go:764-772` (note update), `internal/api/note_types.go:322-335` (note type
  write) and `internal/api/keys.go:67-70` (key creation) map every error that is not one of their named sentinels to
  400 `invalid_request`, and the note and key paths put `err.Error()` into the envelope. A storage outage is
  therefore reported as a client error, and the text that reaches the body is the driver's. The sibling resolver
  `ImportPresetID` (`internal/api/publicid_resolver.go:46-63`) maps the same class to 500 `internal_error`, and
  `internal/api/package_preset_mode_test.go:53-62` pins that behaviour. *Reproduced:* a SQLite `BEFORE UPDATE` trigger
  on `notes` made `PATCH /api/v1/notes/{id}` answer `400 {"code":"invalid_request","message":"… — update note 1:
  constraint failed: probe_internal_marker (1811)"}`. *Acceptance:* every unrecognised store error reaches the caller
  as 500 `internal_error` with the code's static text, and a test that aborts a write inside the database asserts the
  status and a body free of driver text.
  *Closed by this commit (`fix(api): answer an unrecognised store failure as internal_error`).*
  The three sites now hand the store error to the existing fallback instead of building a 400: `UpdateNote`
  returns the wrapped error after its named branches (`internal/api/service.go:818`), `noteTypeWriteError`
  does the same (`internal/api/note_types.go:337`), and `createKey` calls `writeServiceError`
  (`internal/api/keys.go:78`). `asServiceError` maps the result to 500 `internal_error` with an empty message,
  which `formatErrorMessage` renders as the catalog text, and logs the underlying error.
  Two client-error classes that were only distinguishable by text had to become typed, or they would have
  turned into 500s: the store now returns `*store.NotePayloadError` (`internal/store/note.go:238`) for an
  unknown field name or an unsupported value shape, which `UpdateNote` maps to 400 `invalid_request`, and
  `validateAnswerSpecs` returns a `*notetype.ValidationError` with the new `RuleAnswerSpec`
  (`internal/store/notetype.go:97`, `internal/notetype/notetype.go:117`), which `noteTypeWriteError` already
  maps to 400. Key creation keeps `ErrAPIKeyNameRequired`, `ErrAPIKeyUserRequired` and
  `ErrAPIKeyInvalidScope` as 400 and answers everything else with `writeServiceError`.
  `TestStoreFailureIsAnsweredAsInternalError` (`internal/api/internal_error_test.go`) installs a SQLite trigger
  that aborts the write for each of the three paths and asserts 500, `internal_error`, and a body free of the
  marker, `constraint failed` and `SQL`; it then drops the key trigger and shows an ordinary create still
  answers 201. Raw output of `go test ./internal/api/ -run TestStoreFailureIsAnsweredAsInternalError -count=1 -v`:
  ```
  === RUN   TestStoreFailureIsAnsweredAsInternalError
  === RUN   TestStoreFailureIsAnsweredAsInternalError/note_update
  === RUN   TestStoreFailureIsAnsweredAsInternalError/note_type_write
  === RUN   TestStoreFailureIsAnsweredAsInternalError/key_creation
  --- PASS: TestStoreFailureIsAnsweredAsInternalError (0.02s)
      --- PASS: TestStoreFailureIsAnsweredAsInternalError/note_update (0.00s)
      --- PASS: TestStoreFailureIsAnsweredAsInternalError/note_type_write (0.00s)
      --- PASS: TestStoreFailureIsAnsweredAsInternalError/key_creation (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/api	0.034s
  ```
  With the three sites reverted, the same run failed with
  `PATCH note = 400 {"error":{"code":"invalid_request","message":"The request is invalid. — update note 1: constraint failed: probe_internal_marker (1811) (1811)"}}, want 500`,
  which is the reported defect. `go test ./internal/api/ -count=1` passes in `5.354s`, `go test ./internal/mcp/ -count=1`
  in `3.354s`, `go test ./internal/notetype/ -count=1` passes, and
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/api/ -run 'TestNote|TestKey|TestStoreFailure' -count=1`
  and `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestNoteType|TestNote|TestMediaName' -count=1`
  pass on both drivers.

- [x] **M19-102 A failed bulk-import row puts the driver's text into the 200 report** — *should-fix.*
  `internal/api/service.go:709` copies `rowErr.Error()` into `ImportResponse.Errors[].reason`, and `:657` does the
  same for a whole-batch failure; both reach the client in a 200 body, in the field whose purpose is to tell the
  caller what is wrong with that row. *Reproduced:* an aborting `BEFORE INSERT` trigger on `notes` produced
  `errors=[{0 "create note: constraint failed: probe_internal_marker (1811)"}]`. *Acceptance:* a per-row write failure
  reports a stable code or a catalog-backed reason, the underlying error goes only to the log, and a test aborts an
  insert and asserts the report carries no driver text.
  *Closed by this commit (`fix(api): keep a bulk-import row failure out of the response`).*
  `importRowFailureReason` (`internal/api/service.go:773`) returns the controlled detail for a
  `*store.NotePayloadError`, the stable `media_not_readable` code plus its rejected names for a
  `*store.MediaWriteError`, and `internal_error` for every other row failure. `writeImportBatch` (`:755`) logs the
  underlying error with the deck and row index before it records that reason, so the driver text reaches the log and
  never the 200 body. The whole-batch failure in `ImportNotes` (`:698`) likewise records `internal_error` after its
  existing log line instead of `batchErr.Error()`.
  `TestBulkImportRowFailureDoesNotEchoDriverText` (`internal/api/import_error_test.go`) installs an aborting
  `BEFORE INSERT` trigger on `notes` whose message contains `probe_internal_marker`, posts one note through
  `POST /api/v1/decks/:id/notes`, and asserts the 200 report has one row error with reason `internal_error`, no
  `probe_internal_marker` / `constraint failed` / `SQL` text, and no note row left behind. Raw test-runner output of
  `go test ./internal/api/ -run TestBulkImportRowFailureDoesNotEchoDriverText -count=1 -v`:
  ```
  === RUN   TestBulkImportRowFailureDoesNotEchoDriverText
  --- PASS: TestBulkImportRowFailureDoesNotEchoDriverText (0.02s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/api	0.034s
  ```
  With the row-error site reverted to `rowErr.Error()`, the same run failed with
  `row error reason = "create note: constraint failed: probe_internal_marker (1811)", want the stable "internal_error"`
  and the two no-driver-text assertions for `probe_internal_marker` and `constraint failed`.
  `go vet ./internal/api/` and `go test ./internal/api/ -count=1` pass (`4.629s`), and
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/api/ -run 'TestBulkImport|TestRESTBulkImport|TestImport' -count=1`
  passes on both drivers (`1.151s`).

- [x] **M19-103 The `get_due_cards` tool returns one card when `limit` is omitted** — *should-fix.*
  `internal/mcp/tools.go:529` passes `in.Limit` unchanged, so an omitted argument is 0 and `DueCards`
  (`internal/api/service.go:1130-1132`) clamps it to 1, while the REST handler substitutes 50
  (`internal/api/review.go:53`). The same service call reached through two transports returns a different queue
  length for the same request. *Reproduced:* five new cards in one deck gave one card through the tool and five
  through `GET /api/v1/review/due?deck=…` with no `limit`. *Acceptance:* the tool applies the REST default, and a test
  calls both transports without `limit` and asserts the same card set.
  *Closed by this commit (`fix(mcp): apply the REST due-card default to get_due_cards`).*
  `api.DefaultDueLimit` (`internal/api/review.go:36`) is the one value both transports read: the REST handler passes
  it to `queryInt` (`:57`), and `getDueCards` substitutes it when the decoded argument is 0
  (`internal/mcp/tools.go:529-535`), which is what an omitted argument decodes to. `DueCards` still clamps the
  supplied value, so the service contract is unchanged.
  `TestGetDueCardsDefaultLimitMatchesREST` (`internal/mcp/due_limit_test.go`) seeds five new cards in one deck,
  calls `get_due_cards` with only `deck_id`, calls `GET /api/v1/review/due?deck=<id>` with no `limit`, and requires
  five cards from each and the same card-id set. Raw output of
  `go test ./internal/mcp/ -run TestGetDueCardsDefaultLimitMatchesREST -count=1 -v`:
  ```
  === RUN   TestGetDueCardsDefaultLimitMatchesREST
  --- PASS: TestGetDueCardsDefaultLimitMatchesREST (0.05s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/mcp	0.061s
  ```
  With only the default restored to `in.Limit`, the same run failed with
  `get_due_cards without limit returned 1 cards, want 5 (the REST default)`, the reported defect.
  `go vet ./internal/mcp/ ./internal/api/`, `go test ./internal/mcp/ -count=1` (`3.086s`) and
  `go test ./internal/api/ -run 'TestReview|TestDue' -count=1` pass.

- [x] **M19-104 An MCP session keeps the account row it read at the handshake** — *should-fix.*
  `internal/mcp/mcp.go:244-254` answers every tool call with `handshake.User`, which `build` fixed once per session
  (`:78-81`), so role and profile fields are judged from that snapshot while the REST middleware re-reads the row on
  every request; `ImportDeckPackage` derives `allow_others_progress` from that role (`internal/api/package.go:61-64`).
  *Reproduced:* a session opened as an admin, followed by demotion of the account to `user`, still issued an upload
  ticket with `allow_others_progress=true`, while REST issuance by the same account issued `false`. *Acceptance:* each
  tool call uses the user row loaded for its own request, and a test changes the role between handshake and call and
  asserts the two transports take the same decision.
  *Closed by this commit (`fix(mcp): judge every tool call from the current account row`).*
  `requestIdentity` (`internal/mcp/mcp.go:258`) now takes the request context, resolves the key from the header as
  before, and re-reads the account with `API.UserByID` (`internal/api/auth.go:126`) instead of reusing
  `handshake.User`; both the tool-invocation wrapper (`:395`) and the tool-list filter (`:371`) use that identity, and
  a failed reload answers the call with `internal_error` rather than falling back to the stale row. The handshake
  copy remains only as the fallback when the internal identity headers are absent, which no HTTP request can produce.
  `TestMcpSessionUsesTheCurrentAccountRow` (`internal/mcp/session_user_test.go`) opens a session as an administrator,
  demotes the account to `user`, then has MCP and REST each issue an upload ticket with
  `allow_others_progress=true`; both tickets must store `false`. Raw output of
  `go test ./internal/mcp/ -run TestMcpSessionUsesTheCurrentAccountRow -count=1 -v`:
  ```
  === RUN   TestMcpSessionUsesTheCurrentAccountRow
  --- PASS: TestMcpSessionUsesTheCurrentAccountRow (0.04s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/mcp	0.058s
  ```
  Before the change the same test failed with
  `MCP ticket allow_others_progress = true, REST = false: the session kept the handshake role`.
  `go vet ./internal/mcp/ ./internal/api/`, `go test ./internal/mcp/ -count=1`,
  `/home/nite/.hermes/cache/scratch/pgtest ./internal/api/ -count=1` and
  `go test ./internal/web/ -run 'TestMcp|TestMCP' -count=1` pass.

- [x] **M19-105 `GET /notes/{id}` answers 403 although its contract promises 404** — *should-fix.*
  `internal/api/service.go:454-467` states "An unknown id and a read without permission are both 404, never leaking
  'this id exists but you may not see it'", but `GetNote` returns `RequireDeckRole`'s 403 `forbidden` with the detail
  "insufficient role for this deck", so the two cases are distinguishable. *Reproduced:* a stranger reading an
  existing note got `403 {"code":"forbidden",…}` and an unknown id got 404; `internal/api/note_read_test.go:162-168`
  accepts either status while its own comment repeats the no-leak claim. *Acceptance:* an unreadable note answers the
  same 404 `not_found` as an unknown id, and that test asserts the one status instead of a 403-or-404 set.
  *Closed by this commit (`fix(api): answer an unreadable note with the same 404 as an unknown id`).*
  `GetNote` (`internal/api/service.go:471`) now maps a 403 from `RequireDeckRole` to the same
  `404 not_found` / "note not found" it already returns for an unknown id; any other failure is returned unchanged.
  The permission-denied audit row is still written by `RequireDeckRole` before the mapping, so the refusal remains
  auditable without telling the caller which of the two cases it hit. `TestGetNoteAuthorization`
  (`internal/api/note_read_test.go`) now asserts the one 404 and the `not_found` code for the outsider instead of
  accepting 403-or-404. Raw output of `go test ./internal/api/ -run TestGetNote -count=1 -v`:
  ```
  === RUN   TestGetNoteReadsBeyondFirstPage
  --- PASS: TestGetNoteReadsBeyondFirstPage (0.01s)
  === RUN   TestGetNoteAuthorization
  --- PASS: TestGetNoteAuthorization (0.01s)
  === RUN   TestGetNoteAllowsReader
  --- PASS: TestGetNoteAllowsReader (0.01s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/api	0.053s
  ```
  With only `internal/api/service.go` reverted, `TestGetNoteAuthorization` failed with
  `outsider status = 403, want 404 (body {"error":{"code":"forbidden","message":"You do not have access to this
  resource. — insufficient role for this deck"}})`. The MCP `get_note` tool calls the same method, so it now reports
  the read refusal as `not_found` as well.

- [x] **M19-106 A multipart import spools the package before it validates the preset or the target role** —
  *should-fix.* `internal/api/package.go:217-252` takes the slot and the body cap, then `c.FormFile` reads the
  package part, and only afterwards run `ImportPresetID` (`:247`) and `authorizeImportTarget` (`:45`, inside
  `ImportDeckPackage`); the direct-link and ticket entries state and follow the opposite order
  (`internal/api/package_url.go:60-82`, `internal/api/package_upload.go:190-201`), and `internal/api/errors.go:80-83`
  records "refused before its body is read, so nothing is spooled to a temporary file" as the rule for this surface.
  *Unverified:* only read; the spool file was not measured. *Acceptance:* the multipart entry reads the form fields
  and authorizes the target and preset before it consumes the archive part, and a test posts an oversized part with a
  target deck the caller cannot edit and shows the request is refused without spooling.
  *Closed by this commit (`fix(api): authorise an import before reading the archive`).*
  `handleImportPackage` (`internal/api/package.go:214`) now parses the multipart with `c.Request.MultipartReader()`:
  each non-file part is capped at 64 KiB (first value wins, as `PostForm` did), the preset and the target are resolved
  and authorized before the archive part is handed to `ImportDeckPackage`, and only then does the store spool it. The
  handler no longer wraps the body in `http.MaxBytesReader`; the store's spool keeps the `package_max_bytes` cap, and
  the handler maps that one code back to 413 `package_too_large` for this route (the shared `mapPackageError` keeps its
  400 for the other transports). The multipart contract is now fields first, archive last: a file part that arrives
  before any field part answers 400 `invalid_request` instead of silently importing into the `new_deck` defaults, and
  field parts after the archive are ignored. The SPA's `importDeckPackage` (`frontend/src/lib/api/client.ts:341`) appends
  the file last; the Go multipart test builders send their fields first.
  `TestImportPackageAuthorizesBeforeReadingArchive` (`internal/api/package_auth_order_test.go`) posts an oversized
  archive with a target deck the caller cannot edit, asserts 403 `forbidden` and an empty `TMPDIR`, repeats it with an
  unknown target (404 `not_found`), and keeps the 413 `package_too_large` positive control for an authorized target.
  `TestImportPackageRejectsFilePartBeforeFields` pins the 400 for a file-first request. Raw output of
  `go test ./internal/api/ -run TestImportPackageAuthorizesBeforeReadingArchive -count=1 -v`:
  ```
  === RUN   TestImportPackageAuthorizesBeforeReadingArchive
  === RUN   TestImportPackageAuthorizesBeforeReadingArchive/foreign_target_is_refused_before_spooling
  === RUN   TestImportPackageAuthorizesBeforeReadingArchive/unknown_target_is_refused_before_spooling
  === RUN   TestImportPackageAuthorizesBeforeReadingArchive/oversized_archive_for_an_authorized_target_still_hits_the_cap
  --- PASS: TestImportPackageAuthorizesBeforeReadingArchive (0.02s)
      --- PASS: TestImportPackageAuthorizesBeforeReadingArchive/foreign_target_is_refused_before_spooling (0.00s)
      --- PASS: TestImportPackageAuthorizesBeforeReadingArchive/unknown_target_is_refused_before_spooling (0.00s)
      --- PASS: TestImportPackageAuthorizesBeforeReadingArchive/oversized_archive_for_an_authorized_target_still_hits_the_cap (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/api	0.036s
  ```
  With only `internal/api/package.go` reverted, the same run failed with
  `foreign target import = 413, want 403`, `unknown target import = 413, want 404`, and the file-first case imported a
  deck (`file-first import = 200, want 400`). The surrounding suite ends
  `ok  	git.nite07.com/nite/engram/internal/api	0.305s` for
  `go test ./internal/api/ -run 'TestImportPackage|TestImportPreset|TestImportMultipart|TestImportSharedDeck|TestImportSlot' -count=1`.

- [x] **M19-107 A bad `on_conflict` is `package_bad_format` on import and `invalid_request` at ticket issuance** —
  *should-fix.* `internal/store/package_import.go:432-434` wraps the option check in
  `&PackageError{Code: CodePackageBadFormat}`, while `internal/api/package_upload.go:92-97` returns `InvalidRequest`
  for the same value with the same text; a client branches on the code and reads `package_bad_format` as "the package
  itself is corrupt". *Reproduced:* `import_deck` with `on_conflict=bogus` answered `package_bad_format: The package
  format is invalid. — on_conflict must be one of skip, update, fail`, and `create_import_upload` with the same value
  answered `invalid_request: …` with the identical detail. *Acceptance:* every import entry reports a bad request
  option as 400 `invalid_request` and keeps the package codes for archive content, and a table test feeds the invalid
  option to each entry and asserts one code.
  *Closed by this commit (`fix(store): report a bad import option as invalid_request`).*
  `ImportPackage` (`internal/store/package_import.go:437`) now returns a `PackageError` carrying the shared
  `CodeInvalidRequest` (`internal/store/package.go:33`) instead of `CodePackageBadFormat`. The archive's own
  failures keep their package codes, and `mapPackageError` already passes a package error's code through at 400, so
  the multipart, direct-link and MCP entries answer `invalid_request` without a transport change. Ticket issuance
  already validated the same value with `InvalidRequest`; it does not reach this branch.
  `TestImportEntriesReportBadConflictOptionAsInvalidRequest` (`internal/api/package_option_code_test.go:16`) feeds
  `on_conflict=bogus` to the multipart import, the direct-link import and ticket issuance, and asserts each is 400
  `invalid_request`; `TestImportDeckReportsBadConflictOptionAsInvalidRequest`
  (`internal/mcp/package_anki_test.go:65`) sends the same option through `import_deck` and asserts the tool error
  carries `invalid_request`, not `package_bad_format`, and that no deck was written. Raw output of
  `go test ./internal/api/ -run TestImportEntriesReportBadConflictOptionAsInvalidRequest -count=1 -v`:
  ```
  === RUN   TestImportEntriesReportBadConflictOptionAsInvalidRequest
  === RUN   TestImportEntriesReportBadConflictOptionAsInvalidRequest/multipart
  === RUN   TestImportEntriesReportBadConflictOptionAsInvalidRequest/direct_link
  === RUN   TestImportEntriesReportBadConflictOptionAsInvalidRequest/upload_ticket_issuance
  --- PASS: TestImportEntriesReportBadConflictOptionAsInvalidRequest (0.02s)
      --- PASS: TestImportEntriesReportBadConflictOptionAsInvalidRequest/multipart (0.00s)
      --- PASS: TestImportEntriesReportBadConflictOptionAsInvalidRequest/direct_link (0.00s)
      --- PASS: TestImportEntriesReportBadConflictOptionAsInvalidRequest/upload_ticket_issuance (0.00s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/api	0.028s
  ```
  and of `go test ./internal/mcp/ -run TestImportDeckReportsBadConflictOptionAsInvalidRequest -count=1 -v`:
  ```
  === RUN   TestImportDeckReportsBadConflictOptionAsInvalidRequest
  --- PASS: TestImportDeckReportsBadConflictOptionAsInvalidRequest (0.05s)
  PASS
  ok  	git.nite07.com/nite/engram/internal/mcp	0.058s
  ```
  With only `internal/store/package_import.go` reverted, the API run failed with
  `multipart import code = "package_bad_format", want "invalid_request" (body {"error":{"code":"package_bad_format","message":"The package format is invalid. — on_conflict must be one of skip, update, fail"}})`
  and the same line for the direct-link entry, and the MCP run failed with
  `import_deck error text = "package_bad_format: The package format is invalid. — on_conflict must be one of skip, update, fail", want the invalid_request code`.
  `go vet ./internal/api/ ./internal/mcp/ ./internal/store/`,
  `go test ./internal/api/ -run 'TestImport|TestPackage|TestCreateImportUpload|TestAnki|TestExport' -count=1`,
  `go test ./internal/mcp/ -run 'TestImport|TestPackage|TestCreateImportUpload|TestExport|TestAnki' -count=1`
  and `/home/nite/.hermes/cache/scratch/pgtest ./internal/store/ -run 'TestPackage|TestAnkiImport|TestImportPackage' -count=1` pass.

- [ ] **M19-108 The key-revocation handler has no test of its own scoping** — *nit.* `internal/api/keys.go:80-109`
  resolves any key by public id and then revokes it scoped to the caller; the ownership refusal exists only at the
  store layer (`internal/store/apikey_test.go:193`), and no test in `internal/api` or `internal/web` calls
  `DELETE /api/v1/keys/{id}`. *Reproduced:* `grep -rn '"/api/v1/keys' internal/*/*_test.go` lists GET and POST calls
  only. *Acceptance:* an `httptest` case revokes the caller's own key (200, row carries `revoked_at`) and another
  user's key id (404, that key still active), so the handler's scoping is pinned where it is enforced.

- [ ] **M19-109 The two `/mcp` refusals carry no stable code** — *nit.* `internal/mcp/mcp.go:103-116` answers a request
  without a key with `http.Error(..., 401, "api key required")` and a session/user mismatch with
  `403 "session user mismatch"`, both `text/plain` and neither carrying a `code`, while every other refusal in this
  domain uses `{"error":{"code","message"}}` or `ErrorText`; the 401 branch sits behind the middleware that already
  answers in the envelope, and the 403 branch is the one a client actually meets. *Reproduced:* a second account
  presenting another account's registered session id received `403 text/plain; charset=utf-8 "session user
  mismatch\n"`. *Acceptance:* both refusals use the shared envelope with a registered code (`unauthorized` for the
  missing key, `forbidden` for the mismatch) and a test asserts the body's code.

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
| Reproducible oracle checks and precise evidence scope (M18-112) | Source: oracle follow-up review found that the default memory-replay generation cannot promise byte identity. The README distinguishes byte checks for non-memory-replay fixtures from semantic memory-replay comparisons, retaining the stable numeric-only original memory golden in the checksum set. Checks use repository-local temporary files. Environment 0–5 S/D do not distinguish configuration; offset and next_day_at assertions plus New York winter/summer controls supply that evidence. | No — documentation only |
| Precise Anki import approximation warning (M18-110) | Incomplete selected history follows the measured Anki initialization and no longer triggers the not-aligned warning. Only cards without usable carried memory and without selected history count toward the warning, whose text identifies that fallback. | No — log counting and text change only |
| Undo after imported cram grades (M18-108) | Written unselected grades inherit the preceding selected memory; carried final memory stays on the last selected grade and propagates through consecutive cram tails. SQLite and PostgreSQL regression cases import, grade and undo with carried and replay-derived memory. Missing-memory undo still refuses before writes. | No — imported row memory changes; no schema or payload changes |
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
| Measured Anki memory replay and safe imported undo (M18-49, M18-51, M18-79, M18-106, M18-107, M18-109) | Independent Anki 26.9.3 measurements and tag 26.09.3 source establish replay selection and DeltaT: truncate revlog milliseconds to seconds, count 86400-second ages back from the target collection's next rollover, then subtract adjacent ages. The importer uses the importing user's current timezone offset and cutoff (default 04:00), not historical per-row DST offsets or source collection settings. The last learning run is replayed from empty; incomplete history starts from the first eligible interday grade's SM-2 state and replays later ratings. Set due date does not reset history; Forget does; cramming is excluded. Fourteen original golden states, 43 timing controls and six replay-rule controls match within 1e-4 on SQLite and PostgreSQL. The rule controls pin the -86400-second interday boundary and difficulty initialization from Anki FSRS-generated revlog factors 100, 224 and 557; these measurements do not establish invalid-factor behaviour. Selected rows receive memory even with a carried final state; undo refuses before any write if previous memory cannot restore a non-new state. Historical retention remains 0.9, ignore-before is not modelled, and absent history still uses an estimate. Methods and values are in tools/anki-oracle/README.md. | No — no HTTP API, MCP, schema, configuration-key or package-format shape changes; imported scheduling and unsafe-undo behaviour change |
| Import media outside the transaction, bounded imports (M17-61) | An `.apkg` import places its new media files at their content address before the database transaction opens, and removes exactly those files when the transaction rolls back, when the run is a dry run, or when placement fails; a file that existed before is never removed. `package_max_concurrent_imports` (environment variable, then settings row, then default 2) bounds simultaneous imports per process; the multipart, URL, upload-ticket and MCP entries take a slot before reading or downloading and answer 429 `deck_import_busy` at once when none is free. The CLI import takes no slot. | No — one configuration key and one 429 code are added; imported media is now written before the transaction |
| Every image-occlusion shape checked against Anki (M17-62) | The oracle builds six image-occlusion notes through Anki's own API — rect, ellipse, polygon, text, two shapes under one cloze number, and inactive masks (`oi=1`, the hide-all guess-one mode) — and records their fields and rendered faces. The editor's shape model rebuilds each field byte for byte, so parsing and saving an untouched note reproduces Anki's stored value exactly. | No |
| Unmapped templates the target declares keep their cards (M17-63) | A template the mapping leaves out keeps its card when the target config declares the same template id: the card keeps its id, template and `card_states` and counts only in `cards_moved`, so a change between two types that share `forward` and `reverse` no longer asks to confirm a removal that removes nothing. An unmapped template the target does not declare is still removed after confirmation. | No |
| Orphan marks cleared for media an import reuses (M17-64) | Before the import transaction opens, `placeAnkiMedia` clears `orphaned_at` on every media row the package will reuse, so a garbage-collection run during a long import cannot delete the row and its file and fail the transaction. A dry run clears nothing, and an import still removes only the files it created. | No |
| Legacy `.apkg` export (M18-1, with M18-11) | A deck export writes an Anki package the reference release imports: legacy schema 11 in `collection.anki21`, a `meta` entry and the media table; one switch each for scheduling, deck configurations, media and FSRS weights; the weights travel in `fsrsParams6`, answer specs in the `engram` note type key, and a card with a last review time carries `lrt`, in seconds, in `cards.data` (M18-11). An export with `IncludeWeights` on fails when the preset's `weights_json` is `[]`, instead of writing an empty weight list (M18-73). The package is byte-for-byte deterministic. | No — a new export format, with the export switches on the HTTP route, the CLI and the MCP tool |
| The `.edeck` deck package format is removed (M18-5, in parts M18-83 to M18-86) | A deck package is an Anki `.apkg` and nothing else: the import readers, the REST and web exports, the CLI, the MCP tools and the export dialog accept and produce `.apkg`; the old format's reader, writer, JSON document, schema files and MCP export tool are gone. A package carries no `external_ref`, `extra` or `source_url`, because Anki drops `notes.data` on import. | Yes — a file format is removed; a `.edeck` file can no longer be imported or exported |
| `.apkg` import of scheduling data (M18-2, with M18-10 to M18-13, M18-36, M18-37, M18-47, M18-48, M18-50, the reader rules of M18-23 to M18-26, M18-14 to M18-18, M18-56 to M18-62, and M18-65 to M18-71) | An `.apkg` import applies the package's deck configuration to the new deck's preset and, when `include_progress` is on and off by default, the package's card states and review log to the importing account. An imported review row's `interval_days` is the revlog `ivl` (a negative `ivl` converted from seconds to days), its `duration_days` counts fractional days since the previous selected row (NULL for the first selected row and rows outside the selected window; no scheduling or statistics reader consumes it), and its stability and difficulty come from the replay (M18-10). A card's last review time comes from `lrt`, then `lastUsable` (the last graded type 0–3 row after the last reset, including cram rows), then the due date minus `ivl` (M18-11). An interday learning card's due date counts days from collection creation, a filtered-deck card's due date comes from `odue`, and a learning or relearning due value is read as seconds above one billion and as days otherwise in every queue (M18-12, M18-47); such a card is placed in its home deck by `odid` (M18-48). An unusable `s`/`d` counts as missing (M18-36). The measured replay selection, starting state and timing rules are recorded in the memory-replay row above (M18-49, M18-79, M18-106); the first learning row after a reset records state_before 0 (M18-50). Since M18-13 the reader tolerates layouts it used to reject with `package_bad_format`: a REAL value in a scheduling column is rounded, an undecodable deck configuration leaves the preset defaults, and an unreadable revlog no longer rejects a package imported with `include_progress` off (with it on, the import fails, as M18-23 requires). With `include_progress` off, `states_discarded` counts only cards with scheduling state and `progress_discarded` is true only when a state or a review was discarded (M18-14). A re-import with the option on keeps the importer's row when its `last_review_at` is not earlier than the package's and writes only the review rows later than the importer's latest review (M18-15). The new preset stores at most 36500 days and keeps the default steps when the package's steps are negative or not finite (M18-17). The report gains `weights_invalid` (M18-18). With `include_progress` on, a revlog row that cannot be read fails the import with `package_bad_format` (M18-23); a skipped card's review rows count in `reviews_discarded` with a warning (M18-65). When the top deck's configuration is not found and a configuration was skipped, the new preset holds the defaults rather than the single remaining one (M18-24), and one malformed `col.dconf` entry drops only that entry (M18-25). A REAL value in `cards.nid`, `cards.did` or `cards.ord` fails the import; in `revlog.cid`, `revlog.ease` or `revlog.type` it makes that revlog row unreadable; in `cards.type`, `cards.queue`, `cards.odid` or `cards.flags` it leaves that card out and counts in the new `cards_skipped` field (M18-26). Since M18-66 a TEXT value in `cards.type`, `cards.queue`, `cards.odid` or `cards.flags` fails the import instead of leaving the card out, and the error for a refused REAL value names its column. With `include_progress` off, `reviews_discarded` also counts the revlog rows the reader could not read, and the report gains `deck_config_skipped` (M18-68). The Import page shows `cards_skipped` (M18-67). | No — a new option and new report fields; imported cards still start as new when the option is off |
| `.apkg` exports on every transport (M18-3) | The deck export route, the web download, the CLI and the MCP export take `format=apkg`, and `create_export_download` issues a single-use download ticket for the same package. The `export_deck` MCP tool this row was written for is removed by `M18-5`, which leaves `create_export_download` as the only MCP export tool. | No — one new format value and one ticket route |
| Imported FSRS weights are opt-in and validated (M18-9) | An `.apkg` import writes the package's FSRS weights only when `apply_weights` is on, the target is `new_deck` and `fsrs.MigrateWeights` accepts them; the report's `weights_applied` and `weights_discarded` now describe an `.apkg` import as well. | No — a new option and report meaning |
| Preset choice on import (M18-4, with M18-29 to M18-33 and M18-42) | The multipart import, the upload ticket and the URL import take `preset_mode` and `preset_id`; the MCP tools `import_deck` and `create_import_upload` take the same two inputs and the CLI takes `--preset-mode` and `--preset-id`. `new` (the default) builds the preset from the package, `reuse` points the new deck at the caller's preset, and `copy` points it at a copy of that preset. The report carries `preset_mode` and `preset_name` for `new_deck` only and omits them for `into_deck` and `replace_deck` (M18-29). A `preset_id` sent with mode `new` or with no mode is refused for `new_deck` (M18-30) and for every target (M18-42); `reuse` and `copy` are refused with any target other than `new_deck`, and an invalid selection on the URL import is refused before the rate limiter and before the download (M18-31). A store failure while resolving `preset_id` answers 500 `internal` (M18-33). Since M18-82 every preset refusal carries the code `preset_selection_invalid` with status 400, so none of them reports `invalid_request`. | No — `preset_mode` and `preset_id` exist in no release yet, and the default mode keeps the earlier behaviour |
| Preset limits enforced on write (M18-17, with M18-60) | `POST` and `PATCH /api/v1/presets` refuse a `maximum_interval_days` above 36500 with 400 `preset_max_interval_invalid`, the preset page refuses a value above 36500 before sending it (M18-60), and an `.apkg` import stores at most 36500 days and keeps the preset's default steps when the package's step list is negative or not finite. | No breaking shape — a request that used to be accepted is now refused with an explicit code |
| `.edeck` weights validated on import (M18-19; the format is removed by M18-5) | An `.edeck` import with `apply_weights` on writes the package's weights only when `fsrs.MigrateWeights` accepts them, sets `weights_applied` only when they were written, `weights_discarded` when a carried set was not written, and `weights_invalid` when the set was refused. `M18-5` removed that import path; an `.apkg` import applies the same validation and reports the same three fields (M18-9). | No — a new option and report fields |
| Preset repair on upgrade (M18-59, with M18-99) | Versioned migration `0020_repair_preset_scheduler_limits` rewrites stored presets on upgrade: a `maximum_interval_days` outside 1 to 36500 becomes 36500, a `desired_retention` outside (0, 1] becomes 0.9, and a learning or relearning step list the scheduler refuses becomes the default steps. It counts each changed row once, leaves every other column intact, and changes nothing on a second run. The rewrite is needed because go-fsrs replaces every supplied parameter with its defaults for a maximum interval above 36500 or a negative step, and the scheduler refuses a non-finite step, so such a preset is unusable as stored. | No — a versioned migration is added; stored preset values change |
| Import page preset choice (M18-6) | The Import page offers `preset_mode` `new`, `reuse` and `copy`, a preset picker bound to `preset_id`, the `include_progress` checkbox, and the report counters the import returns, among them `cards_skipped`, `reviews_discarded` and `deck_config_skipped`. | No — a page behaviour |
