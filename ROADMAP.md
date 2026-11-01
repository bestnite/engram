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

A domain-by-domain review of the whole repository was run with a second model. The `internal/store`
domain completed — 155 files, 23 findings (3 blocker, 19 should-fix, 1 nit), each with a reproduction or
an explicit "unverified" — and its report is kept outside the repository at
`~/.hermes/reports/engram-repo-review-store-20261102.md`. The API and MCP domain was stopped by the
provider's content filter before it could write its report, so M19-1..M19-3 are static analysis that was
read out of the code but never reproduced. Every entry says which it is: "unverified" does not mean
"probably fine", and it does not mean "confirmed broken".

- [ ] **M19-1 A preset identifier may be resolvable across accounts** — the review reported that a
  request can name a preset belonging to another account and have it resolved rather than refused,
  through the code path that turns a preset public id into a row. *Unverified:* the reviewer stopped
  before writing the reproduction. *Acceptance:* a test in `internal/api` (or `internal/store` where the
  lookup lives) sends a write request naming another account's preset public id and shows that it is
  refused with the documented code and that no row is written; if the report turns out to be wrong, the
  test stays as the regression that pins the refusal. Evidence is the raw test output on both drivers.

- [ ] **M19-2 Template media may be exportable by a caller who cannot read the deck** — the review
  reported that a deck export can carry media referenced only by a note type template, and that the
  export path does not apply the same media-readable check the single-card write path applies. *Unverified:*
  no reproduction. *Acceptance:* a test exports a deck containing templated media as a caller with read
  access and as a caller without it, and shows the second is refused (`media_not_readable` or the code
  the rules name) while the first succeeds; the export of an unreadable media name must not appear in
  the archive. Evidence is the raw test output.

- [ ] **M19-3 An evicted MCP session may answer with stale identity** — the review reported that after
  an MCP session is evicted (idle expiry or credential change), a later call on that session is answered
  with the identity bound at creation instead of being re-authenticated or refused. *Unverified:* no
  reproduction. *Acceptance:* a test evicts the session and shows the next tool call is refused with the
  documented code, and that a call carrying a revoked API key is refused even when the session object
  still exists. Evidence is the raw test output.

- [ ] **M19-4 Concurrent migration applies one step twice and drops note metadata** — *Blocker.* `Apply`
  reads the schema version once outside the migration transaction and takes no database-level lock
  (`internal/store/migrate.go:747-761`), so two processes that both read version 11 both run `0012`.
  The second pass reads fields the first pass already moved out, nulls them and creates a second note
  type (`migrate_notes.go:302-327,342-348`). *Reproduced* on PostgreSQL with a barrier holding both
  callers past the version read: `first Apply: applied=1; source_url=<set> extra_json=<set>` then
  `second Apply: applied=1; source_url=<nil> extra_json=<nil> note_types=2`. The full `Sync`-level DDL
  interleaving is unverified. *Acceptance:* a database-level lock held across `Up` and the version
  write, with the version re-read inside it; a regression with two connections migrating at once shows
  metadata intact and exactly one note type. A process mutex alone is not a fix.

- [ ] **M19-5 A cancelled import deletes a media file another import committed** — *Blocker.*
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

- [ ] **M19-6 Deleting a note type can succeed while a note is created against it** — *Blocker.* The
  delete counts referencing notes and then deletes in the same transaction
  (`internal/store/notetype.go:293-340`, count at `:297,305`, delete at `:329`), but a transaction is
  not serialisation on PostgreSQL at READ COMMITTED: another session can still read the type and create
  the note and card, and the delete also succeeds, leaving `notes.note_type_id` dangling. *Reproduced*
  on PostgreSQL with a `BEFORE DELETE` trigger as a barrier: `delete err=<nil>; insert err=<nil>;
  cards=1; notes=1 note_types=0; NoteTypeFor=record not found`. Production SQLite serialises through a
  single connection, so this is a PostgreSQL-path defect. *Acceptance:* a foreign key with `RESTRICT`,
  or a shared lock on the type for note writes and an exclusive one re-checked under the delete;
  a regression must show the two operations cannot both succeed leaving an orphan.

- [ ] **M19-7 Saving your own profile writes back a stale role or status** — *should-fix.*
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

- [ ] **M19-8 The 0017 ruby migration rewrites already-converted content** — *should-fix.* The step
  applies a context-free string substitution to HTML that 0012 already produced
  (`internal/store/migrate_notes.go:803-805,848-880`): readings double-escape, a literal shortcode
  inside a code span is executed, and a cloze nested in a ruby annotation is cut at the first `}}` and
  left as literal text. *Reproduced* through a full `Sync` on both drivers: `{{ruby:B|a&b}} ->
  <ruby>B<rt>a&amp;amp;b</rt></ruby>` / `` `{{ruby:B|R}}` -> <code><ruby>B<rt>R</rt></ruby></code> `` /
  `{{ruby:{{c1::漢字}}|かんじ}} -> unchanged shortcode`. A database already marked version 17 needs its
  own remedy, not just a corrected step. *Acceptance:* convert by HTML context, skip `code`/`pre`, escape
  the reading once, keep legal nesting; add a regression that runs real old Markdown through 0012 and
  then 0017.

- [ ] **M19-9 A batch note update silently clears `source_url` and `extra_json`** — *should-fix.* The
  bulk import path builds a fresh note without the stored `SourceURL`/`ExtraJSON` and the store writes
  both columns unconditionally (`internal/store/note.go:537-545`; callers
  `internal/api/service.go:473-480,600-608,700-702`), while a single PATCH preserves them. The import
  schema documents top-level `source_url`/`extra`, but `ImportNote` has no such fields, so following the
  schema does not help. *Reproduced* on both drivers: `before {"extra":"{\"k\":\"v\"}","source_url":
  "https://example.com/a"}` then `after {"extra":null,"source_url":null}`. *Acceptance:* the import path
  carries the metadata fields and distinguishes absent from empty, or updates only named columns; a
  regression creates a note, updates it through a batch import keyed by `external_ref`, and asserts the
  metadata survived, plus one that sends the top-level schema keys and asserts they land.

- [ ] **M19-10 Exported learning steps round to zero and lose the review history** — *should-fix.*
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

- [ ] **M19-11 Clone fails on imported note types and drops custom styling** — *should-fix.* Clone
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

- [ ] **M19-12 A cloze→cloze change reports card removals that do not happen** — *should-fix.* Cloze
  produces an empty template plan, so every card is first listed as stale and soft-deleted, then the
  sync restores the same IDs (`internal/store/note_type_change.go:450-456,334-354,560-568,619-628`).
  The report counts the same cards in both `CardsAdded` and `CardsRemoved`, breaking the documented
  "a card is never counted in both" rule, and demands confirmation for deletions that do not occur.
  *Reproduced* on both drivers: `dry-run: CardsAdded:2 CardsRemoved:2`, then `unconfirmed: note type
  update would remove cards without confirmation`, then `confirmed: live cloze:1 id=1; live cloze:2
  id=2`. *Acceptance:* plan cards from the cloze numbers each note actually keeps, count surviving cards
  as moved/kept, and add a regression that a cloze→cloze change succeeds without confirmation and leaves
  IDs and progress untouched.

- [ ] **M19-13 Editing a note type leaves the search index stale** — *should-fix.* The update rebuilds
  media references and cards but never recomputes `notes.search_text`, which depends on the field set
  and `exclude_from_search` (`internal/store/notetype.go:234-238`, `card.go:186-215`, derivation in
  `note_search.go:47-58`). Excluding a field leaves its old text searchable; re-including one does not
  make it searchable. *Reproduced* on both drivers: `search_text before = "alpha\nbeta"` then
  `search_text after exclude_from_search on A = "alpha\nbeta"` and `List(alpha) total=1`. *Acceptance:*
  recompute affected notes' `search_text` in the same transaction as the config version, with a
  regression that excluding, including and deleting a field each change the search result immediately.

- [ ] **M19-14 A tag filter matches different notes on SQLite and PostgreSQL** — *should-fix.* Stored
  `tags_json` is folded by SQL `LOWER`, the query string by Go `strings.ToLower`
  (`internal/store/note.go:847-861`); SQLite's built-in `LOWER` only folds ASCII, so a tag stored as
  `Été` matches nothing on SQLite even with its own spelling. The same predicate serves note listing and
  tag-scoped study. *Reproduced*: `sqlite: query "été" -> 0 row(s); query "ÉTÉ" -> 0 row(s)` versus
  `postgres: query "été" -> 1 row(s); query "ÉTÉ" -> 1 row(s)`. *Acceptance:* one Unicode normalisation
  rule on both the stored and the queried side (a derived normalised tag column with a backfill is the
  obvious shape), with a two-driver regression covering accented and non-ASCII case folding.

- [ ] **M19-15 Deleting a deck leaves the note-type and media rows behind, so the files are never
  collected** — *should-fix.* The cascade clears notes and cards but not `note_types`, `media_names`
  or `media_note_types` (`internal/store/deck.go:332-355`; account deletion calls the same function at
  `user_admin.go:159-163`). The leftover template references keep the collector convinced the media is
  in use. *Reproduced* on both drivers: `note_types=1; media_names=1; media_note_types=1` and
  `MarkOrphanedMedia marked 0 rows, want 1`, with the uploader's usage still counting the byte. Usage not
  dropping to zero immediately is acceptable during a grace period; never being markable as orphan is
  not. *Acceptance:* in the same transaction clear `media_note_types` by note-type ID, then
  `note_types` and the deck's `media_names`; a regression deletes a deck with template assets, runs the
  mark and the expiry, and asserts no residue and the quota released.

- [ ] **M19-16 The collector keeps an orphan alive because another deck uses the same file name** —
  *should-fix.* The orphan query joins `media_note_types` to `media_names` on name alone
  (`internal/store/media_gc.go:23-24`) without carrying `note_types.deck_id`; media names are keyed by
  `(deck_id, name)`, not globally. Deck B's `_font.woff2` template reference therefore keeps deck A's
  already-unreferenced file of the same name alive. *Reproduced* by running the real GC SQL on SQLite:
  `GC with cross-deck name collision: marked=0 reclaimed=0`. The PostgreSQL repro of this scenario is
  unverified; the missing join is confirmed by reading the SQL. This is not a cross-account read, it is
  a cross-account effect on collection and disk use. *Acceptance:* join through `note_types` on
  `mn.deck_id = nt.deck_id AND mn.name = mnt.media_name`; a regression with two live decks holding the
  same file name and different hashes, one of them no longer referenced.

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

- [ ] **M19-18 Two concurrent `into_deck` imports can point a note at another package's media** —
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

- [ ] **M19-19 A media-name clash rewrite corrupts HTML attribute names instead of the reference** —
  *should-fix.* The clash handler replaces the first occurrence of the name inside the whole match
  (`internal/store/media_clash.go:107-113`); the name is an attribute value, so with a short extensionless
  name the first hit can be inside `src`, `href` or `url(`. *Reproduced* by calling the production
  function: `<img src="s"> -> <img s-abc.pngrc="s">`, `<a href="h">x</a> -> <a h-abc.pngref="h">x</a>`,
  `background:url(u) -> background:u-abc.pngrl(u)`. The end-to-end import of such a package is
  unverified. *Acceptance:* replace inside the value capture group, never the first substring of the
  whole match; cover single and double quotes, unquoted `src`, CSS `url()`, `sound:` and short names.

- [ ] **M19-20 A preset write-back lets the optimiser and the settings form overwrite each other** —
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

- [ ] **M19-21 Two concurrent `AddTags` both succeed and one addition is lost** — *should-fix.* Tags are
  read outside the transaction, merged in Go, and written unconditionally inside it
  (`internal/store/note.go:696-727,731-743`); serialising the write transaction cannot save a stale read,
  so production SQLite with a single connection has the same defect. *Reproduced* with 200 notes and 20
  rounds on SQLite (confirmed `MaxOpenConns=1`) and on PostgreSQL: `errors nil; affected A=200 B=200;
  lost tag=true; first row "[\"a\"]"` and `rounds=20 lost=20` on both drivers, with both calls' errors
  checked so this is not a swallowed write. `AddTags`, `RemoveTags` and `SetTags` share the path; the
  reproduction pins two `AddTags`. *Acceptance:* move read, merge and write into one locked transaction,
  or compare-and-set on the old tag value with a retry (moving only the SELECT into a plain PostgreSQL
  transaction is not enough); add a concurrent union/difference regression.

- [ ] **M19-22 A large but legal deck cannot be exported** — *should-fix.* Export expands every note
  and card ID into one `IN` list with no batching (`internal/store/anki_export.go:68-71,122-125,135-138`).
  SQLite's bind cap is reached at 33,000 notes, and one collection entry can hold any number of notes,
  so the zip entry limit is no protection. *Reproduced* with 33,000 real notes: `export anki package:
  list cards: SQL logic error: too many SQL variables (1)`. PostgreSQL's 65,535-parameter ceiling is
  visible in the same queries; the whole oversized export on PostgreSQL is unverified. The call returns
  an error rather than crashing. *Acceptance:* join or subquery the deck inside the database, or load in
  fixed batches preserving order and dedup, at all three sites; a regression exports a deck above the
  SQLite bind cap and one with few notes but many cards.

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

- [ ] **M19-24 The upgrade preflight cannot inspect an early database and calls unconvertible data
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
