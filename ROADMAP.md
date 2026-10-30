# ROADMAP.md — Task backlog and release ledger

This file holds the **open** work and the goals completed since the last release: section 1 is
the backlog, section 2 the release ledger. The rules for writing, counting and closing an entry
— the checkbox form, ID retirement, the acceptance evidence, the release cut — live in
`AGENTS.md` section 7; read that first.

---

## 1. Task backlog

### M17 — Anki-compatible deck content

**Goal.** Deck content in engram behaves like Anki release `26.09.3`. The reference sources are the
tag `26.09.3` of the `ankitects/anki` repository on GitHub and the Python package `anki==26.9.3`. "Behaves like
Anki" covers these five points:

1. which cards a note generates, and their numbers;
2. the HTML of each card's front and back after template rendering;
3. how a card face behaves in the browser: HTML, CSS, template JavaScript, MathJax, cloze, image
   occlusion, audio, video, TTS and type-in comparison;
4. after an `.apkg` import, points 1–3 match what Anki shows for the same package;
5. the editor edits all of the above: rich-text fields, templates, styling, cloze and media.

The requirement is the same capability, not the same interface. engram's own interface may differ
from Anki's. engram may also add front-end-only enhancements, such as the audio progress bar and the
seek buttons.

Dispatch every M17 task together with this whole introduction: the tasks rely on it and do not repeat
it.

**Out of scope for M17:**

- new schedulers (FSRS v4/v5 parameters, SM-2);
- queue behaviour: sibling burying, display order, new-card position, leeches, subdeck limits,
  auto-advance and timers;
- importing progress or the review log from an `.apkg`. Imported cards start as new cards;
- deck hierarchy behaviour;
- `.apkg` export;
- Anki's browser, search syntax, sync, statistics and add-ons.

**Fixed decisions.**

1. **One engine.** A note type is data: fields, templates, CSS and Anki's note type options.
   `internal/cardtype` is deleted. The registered unit becomes the *answer control* (M17-13).
2. **Field values are HTML strings.** There is no other field format. The Markdown editor is replaced
   by a rich-text editor. Existing Markdown is converted to HTML once, by migration (M17-14).
3. **Unsanitised HTML is shown only inside the sandboxed frame.** This applies to card faces, deck
   descriptions, mail previews and the rich-text editing area. The server sanitises nothing: card
   faces and mail are both unsanitised, and `bluemonday` is removed. This replaces the allowlist as
   the security boundary.
4. **Deck scope.** A note type belongs to one deck. Media file names are also scoped to one deck.
5. **Subdecks.** A subdeck is stored only as the path in `cards.deck_path`.
6. **External resources in the frame.** The frame may load https images, audio, video and fonts.
   External scripts, external stylesheets and network connections stay blocked.
7. **Audio and TTS use Anki syntax:** `[sound:<name>]` and `[anki:tts …]…[/anki:tts]`. engram extends
   them in ways that Anki tolerates:
   - extra TTS options go inside the `[anki:tts]` tag, because Anki keeps unknown options and ignores
     them;
   - an audio segment, rate or loop goes in the attributes `data-start`, `data-end`, `data-rate` and
     `data-loop="1"` of a wrapping `<span class="engram-sound">`.
8. **No cloze multi-answer rule.** The rule that `|` or `\alt` separates several accepted cloze
   answers is removed. Both are ordinary content.
9. **`.edeck` format 2 only.** Support for format 1 is removed. This is a one-time exception to the
   N-1 rule in `schema/README.md`.

**Data model shared by the tasks.**

- **Table `note_types`.** Columns: `id`, `public_id`, `deck_id` (not null), `name`, `config_json`
  (TEXT), `version` (int, optimistic lock), `created_at`, `updated_at`.
  - `config_json` keys: `kind` (`normal` | `cloze`), `fields`, `templates`, `css`, `sort_field_id`,
    `latex_pre`, `latex_post`, `latex_svg`, `original_stock_kind`, `anki_origin`.
    - `original_stock_kind` is one of `basic`, `basic_and_reversed`, `basic_optional_reversed`,
      `basic_typing`, `cloze`, `image_occlusion`, or null.
    - `anki_origin` is `{notetype_id, mtime}`, or null.
  - A field entry has: `id`, `name`, `sticky`, `rtl`, `font`, `font_size`, `description`,
    `plain_text`, `collapsed`, `exclude_from_search`, `tag`, `prevent_deletion`.
  - A template entry has: `id`, `name`, `qfmt`, `afmt`, `bqfmt`, `bafmt`, `browser_font`,
    `browser_font_size`, `answer`. `answer` is an answer spec `{control, bindings}` or null.
  - Field ids and template ids are stable: renaming or reordering never changes them.
- **Table `notes`.**
  - Gains `note_type_id` (not null), `source_url` and `extra_json`.
  - `fields_json` maps field id to an HTML string.
  - `kind` is dropped.
  - A note imported from Anki has `external_ref = "anki:<guid>"`.
- **Table `cards`.**
  - `template` is the template id for a normal note type, and `cloze:<n>` for a cloze note type.
  - It gains `deck_path`: nullable TEXT holding the Anki subdeck path relative to the imported root
    deck, for example `Lesson 1::Verbs`.
- **Table `media_names`.** Columns `deck_id`, `name` and `sha256`; `(deck_id, name)` is unique. `name`
  is stored NFC-normalised. Fields and templates refer to media by file name, as Anki does.
- **Table `media_note_types`.** Records which media a note type's templates and CSS refer to (for
  example `_`-prefixed JS, CSS and font files). It is used for read access checks and for media garbage
  collection.

**Frame contract shared by the tasks.**

- **Frame page.** `GET /card-frame/<build-version>` serves the frame page with these headers:
  - `Content-Security-Policy: sandbox allow-scripts; default-src 'none'; script-src <origin> 'unsafe-inline' 'unsafe-eval'; style-src <origin> 'unsafe-inline'; img-src <origin> https: data: blob:; media-src <origin> https: blob:; font-src <origin> https: data:; connect-src <origin>/fm/; form-action 'none'; frame-ancestors 'self'`
  - `Referrer-Policy: no-referrer`

  `<origin>` is the configured public origin of the instance.
- **The iframe element.** It carries `sandbox="allow-scripts"` and `allow="autoplay"`, and nothing
  else in `sandbox`. The site-wide `Permissions-Policy` changes `autoplay=()` to `autoplay=(self)`.
- **Frame media.** The frame loads media from `/fm/<token>/<name>`. `<token>` is an HMAC-signed
  `(user_id, deck_id, expiry)`. Each request checks the signature, the expiry and the user's current
  read access to the deck. The frame document sets `<base href="/fm/<token>/">`. Responses carry
  `Access-Control-Allow-Origin: *`.
- **Messages from the parent page to the frame:**
  - `show {html, css, bodyClass, side, mediaBase, autoplay}`
  - `theme {dark}`
  - `replay`
- **Messages from the frame to the parent page:**
  - `ready`
  - `height {px}`
  - `key {key, code, ctrl, alt, shift, meta}`. Not sent while focus is in an input, a textarea or a
    contenteditable element.
  - `answer {typed?, value?}`
  - `link {href}`
- **Message checks.** The parent accepts a message only when `event.source` is that iframe's
  `contentWindow`, and validates every field of the message.

**Rules for every M17 task.**

- **Ported code.** A Go or TypeScript file ported from Anki keeps Anki's header
  (`Copyright: Ankitects Pty Ltd and contributors`, `License: GNU AGPL, version 3 or later`). It also
  names the upstream path it was ported from, at tag `26.09.3`.
- **Golden files.** Golden files generated by M17-5 decide expected behaviour. A test never edits a
  golden file by hand.
- **The P3 integration branch.** M17-12 to M17-20 replaced the content model on
  `feat/m17-p3` and that branch was merged into `main` once, with the full-module check green; it is
  the single breaking release of M17 (HTTP API, MCP, database schema and file format). The
  remaining M17 tasks are additive and land on `main` directly.

**Phase 3 follow-up — defects found in the review of the P3 merge (on `main`)**

These tasks fix what the P3 merge left wrong or unfinished. They land on `main` directly. M17-33,
M17-34, M17-35, M17-41 and M17-44 must land before M17-24, because every imported Anki note type
is a note type whose shape matches no engram stock kind.

- [ ] **M17-32 Keep `notes.note_type_id` NOT NULL** — the constraint that migration 0013 adds must
  survive every later start.
  *Files:* `internal/store/models.go` (single-writer hotspot); `internal/store/migrate.go`;
  `internal/store/migrate_notes.go`; `internal/store/migrate_notes_test.go`; `ARCHITECTURE.md`.
  *Problem:* `Sync` runs AutoMigrate again after the versioned migrations. The model declares
  `NoteTypeID *uint64` without `not null`, so GORM sees a NOT NULL column against a nullable model
  field and alters the column back to nullable. Measured on SQLite: after `Sync` reaches version 14,
  the `notes` DDL reads `note_type_id integer` with no NOT NULL. GORM never tightens a column in the
  other direction, so a database that is already at version 14 keeps the nullable column even after
  the model changes.
  *Work:*
  - The `Note` model declares `note_type_id` with `not null`.
  - The first AutoMigrate pass of `Sync` must not add `note_type_id` as NOT NULL to a `notes` table
    that already holds rows, because SQLite and PostgreSQL both refuse that. While the stored schema
    version is below 13, that pass migrates `notes` from a pre-0013 model shape in which
    `note_type_id` is nullable. From version 13 on, it migrates the real `Note` model.
  - Append a new versioned migration that applies the same change as 0013 (NOT NULL on
    `note_type_id`; drop `kind` when it still exists). It repairs databases that reached version 14
    with the nullable column. Migration 0013 itself stays unchanged.
  - Correct the test comment in `migrate_notes_test.go` that claims a NOT NULL check the test does
    not make.

  *Acceptance:*
  - A table-driven SQLite test runs `Sync` on three databases: a fresh one, one with the pre-M17
    schema and notes, and one at version 14 with a nullable column. After `Sync`, and again after a
    second `Sync`, `note_type_id` is NOT NULL. The test reads the nullability from the database
    catalog (`PRAGMA table_info` on SQLite, `information_schema.columns` on PostgreSQL), not from the
    model.
  - The same test passes with `TEST_PG_DSN` set, run by hand.
  - Inserting a note without a note type fails at the database level.

  *Verify:* `go test ./internal/store/... && go vet ./internal/store/...`.
- [ ] **M17-33 Card faces carry the note type's CSS and body class** — the review page and the note
  preview send the server's per-side face objects to the frame.
  *Files:* `frontend/src/lib/views/ReviewView.svelte` and `frontend/src/lib/views/NoteEditView.svelte`
  (single-writer hotspot); `frontend/src/lib/card-frame.ts`; `frontend/src/lib/api/`.
  *Problem:* the server returns `front` and `back` objects `{html, css, body_class, side,
  media_base}`. The SPA reads only `front_html` and `back_html`, and passes the fixed
  `REVIEW_FRONT_CSS`, `REVIEW_BACK_CSS` or `REVIEW_ANSWER_CSS` constant and `bodyClass="card"` to every
  `CardFrame`. No note type's CSS ever reaches a frame.
  *Work:*
  - Every `CardFrame` in the review page and in the note preview takes `html`, `body_class` and
    `media_base` from the matching `front` or `back` object.
  - The frame's style is the engram face constant followed by the object's `css`, in that order, so
    a note type rule overrides the engram default.

  *Acceptance:*
  - A client test: a note type whose CSS contains `.card { color: rgb(1, 2, 3); }` renders that colour
    in the review frame and in the preview frame.
  - A client test: the frame `<body>` carries the class list that the server returned.

  *Verify:* `npm --prefix frontend run check && npm --prefix frontend test -- --run && npm --prefix frontend run test:client`.
- [ ] **M17-34 The review page works for any note type** — the review page stops reading the
  card-kind catalog.
  *Depends on:* M17-33.
  *Files:* `frontend/src/lib/views/ReviewView.svelte` (single-writer hotspot);
  `frontend/src/lib/review-shortcuts.ts`; `internal/web/review.go`; `internal/web/review_api.go`;
  `internal/api/service.go` (`DueCard`).
  *Problem:* the review page derives `desc` from `descriptionOf($cardTypes, current.kind)` and shows
  the loading skeleton while `desc` is null. A note type whose shape matches no engram stock kind
  gives `kind = ""`, so its cards stay on the skeleton forever. Such note types can already be
  created through `POST /api/v1/decks/:id/note-types`.
  *Work:*
  - The review card response carries `answer_control`, which is the control name of the card's
    template answer spec, or null when the template has none. It also carries `machine_graded`, a
    boolean computed by the same rule the submit path uses.
  - The review page decides between self-rating, the essay flow and machine grading from those two
    fields only. It reads no card-kind description.
  - The plain-text fallback that is shown when the card face fails to load lists the note's field
    values in the order of the note type's fields.

  *Acceptance:*
  - A handler test: a card of a note type created through the API, with a shape that matches no
    stock kind, returns `answer_control: null` and `machine_graded: false`.
  - A client test: that card reaches reveal and rating with no catalog loaded.
  - Client tests: one machine-graded card and one `short_answer` card behave as before.

  *Verify:* `go test ./internal/web/... ./internal/api/...`; `npm --prefix frontend run check && npm --prefix frontend run test:client`.
- [ ] **M17-35 The note editor works on note types** — create, edit and preview a note of any note
  type of the deck.
  *Depends on:* M17-33.
  *Files:* `frontend/src/lib/views/NoteEditView.svelte` (single-writer hotspot);
  `frontend/src/lib/components/NoteFieldsForm.svelte`; `frontend/src/lib/api/`;
  `internal/web/note_preview.go`; the locale catalogs (append a block with the prefix `note_edit.type.`).
  *Problem:* the editor picks a card kind from the `card-types` catalog, builds its fields from that
  kind and saves with `kind`. The preview endpoint renders the stock definition of the posted `kind`,
  not the deck's note type. A note of a note type that matches no stock kind cannot be edited, and a
  customised note type is previewed with stock templates.
  *Work:*
  - The editor lists the deck's note types from `GET /api/v1/decks/:id/note-types`. When a new note is
    created, the selector starts at the first note type in the order the server returns.
  - The fields are the selected note type's fields, in config order. Each field uses its name as the
    label and applies its options (sticky, collapsed, plain text, RTL, font and size).
  - Saving sends `note_type` and `fields` keyed by field name. The editor never sends `kind`.
  - `POST /api/v1/decks/:id/notes/preview` takes `note_type` instead of `kind` and renders the stored note
    type with its CSS.

  *Acceptance:*
  - A client test: a note of a note type that matches no stock kind loads, edits and saves, and the
    save request carries `note_type`.
  - A handler test: the preview of a note type whose template differs from the stock template renders
    the stored template.
  - A handler test: a preview naming a note type of another deck returns 404.
  - The two locale catalogs keep identical key sets.

  *Verify:* `go test ./internal/web/... ./internal/i18n/...`; `npm --prefix frontend run check && npm --prefix frontend run test:client`.
- [ ] **M17-36 Remove the transitional card-kind layer** — delete every payload field and endpoint
  that exists only for the old SPA.
  *Depends on:* M17-34, M17-35.
  *Files:* `internal/api/card_types.go`; `internal/api/card_catalog.go`; `internal/api/notes.go`;
  `internal/api/note_view.go`; `internal/api/service.go`; `internal/api/server.go`;
  `internal/web/review.go`; `internal/store/note.go`; `frontend/src/lib/card-types.ts` and its
  callers; `ARCHITECTURE.md`.
  *Work:*
  - Delete `GET /api/v1/card-types`, the description catalog, and the `kind` field of the note request
    and response, of `DueCard` and of `ExportRow`.
  - Delete `front_html`, `back_html`, `options_html`, `option_order`, `blanks` and the top-level
    `media_base` from the review and preview responses. The `front` and `back` objects remain.
  - Delete `NoteStore.EnsureStockNoteType`, `NoteStore.KindOf` and the API's kind-to-note-type
    resolution. Migration 0012 keeps its own stock lookup.
  - Rewrite the request-path section of `ARCHITECTURE.md` that describes these fields as transitional.

  *Acceptance:*
  - `grep -rn "card-types\|EnsureStockNoteType\|front_html" internal frontend/src` prints nothing.
  - A handler test: a note write that carries `kind` and no `note_type` returns 400 `invalid_request`.
  - The REST and MCP tests pass.

  *Verify:* `go test ./internal/api/... ./internal/web/... ./internal/mcp/... ./internal/store/...`; `npm --prefix frontend run check && npm --prefix frontend test -- --run && npm --prefix frontend run test:client`.
- [ ] **M17-37 A kind alias never moves a note to another note type** — keep a note on its note type
  while the transitional `kind` field still exists.
  *Files:* `internal/api/service.go` (`UpdateNote`); `internal/api/notes_test.go`.
  *Problem:* `UpdateNote` resolves a posted `kind` with `ensureStockNoteType`, which returns the first
  note type of the deck whose field ids and template ids match the stock shape. When a deck holds two
  note types of that shape (one of them a copy with different CSS or templates), saving a note of the
  second one through the SPA moves the note to the first one.
  *Work:* when the request carries no `note_type`, and the posted `kind` equals the stock kind of the
  note's current note type, the note keeps its current note type.
  *Acceptance:* a handler test with two note types of the `basic` shape in one deck: updating a note of
  the second type with `kind: "basic"` keeps `note_type_id` unchanged.
  *Verify:* `go test ./internal/api/... && go vet ./internal/api/...`.
- [ ] **M17-38 Convert the `{{ruby:…}}` shortcode** — existing notes show raw ruby source since
  migration 0012.
  *Files:* `internal/store/migrate.go` (a new versioned migration appended at the end);
  `internal/store/migrate_notes.go`; `internal/store/migrate_notes_test.go`.
  *Problem:* the old renderer expanded `{{ruby:base|reading}}` to `<ruby>base<rt>reading</rt></ruby>`.
  Migration 0012 converts `{{tts:…}}` and `{{audio:…}}` only, and the pre-flight check does not report
  ruby, so every ruby annotation became literal text. A `{{tts:…}}` that wraps a ruby was also left
  literal, because its parameter split stops inside the nested shortcode.
  *Work:* the new migration rewrites every note field, soft-deleted notes included:
  - `{{ruby:B|R}}` becomes `<ruby>B<rt>R</rt></ruby>`, with `R` HTML-escaped.
  - The old rule for a literal ruby still applies, and a ruby that matches it stays unchanged: an
    empty base, an empty reading, a reading containing `=`, `|` or `{{`, or a base containing another
    shortcode.
  - A `{{tts:…}}` whose body contains a ruby is converted with the 0012 TTS rule after the ruby inside
    it is converted. The visible copy keeps the `<ruby>` markup. The text inside
    `[anki:tts …]…[/anki:tts]` is the base `B` alone, because the old speech text left the reading out.
  - `search_text` is recomputed for every changed note.

  *Acceptance:*
  - A table-driven test covers: a plain ruby, a ruby inside a TTS, each literal case, and a note
    without ruby that is left byte-for-byte unchanged.
  - The test runs once with `TEST_PG_DSN`.

  *Verify:* `go test ./internal/store/... && go vet ./internal/store/...`.
- [ ] **M17-39 Delete the dead shortcode UI and the stale sanitising comments** — remove what still
  describes the old renderer.
  *Files:* `frontend/src/lib/components/ShortcodeInsert.svelte`; `frontend/src/lib/shortcode-source.ts`;
  `frontend/src/tests/shortcode-source.test.ts`; `frontend/src/tests/shortcode-insert.client.ts`; the
  locale catalogs; `frontend/src/lib/api/types.ts`; `frontend/src/lib/components/CardFrame.svelte`;
  `frontend/src/lib/views/ReviewView.svelte` (single-writer hotspot); `frontend/src/lib/card-frame.ts`;
  `internal/web/card_frame.go`; `internal/web/review.go`.
  *Work:*
  - Delete the shortcode inserter, its source builder, their tests, and every catalog key that only
    they used. Keep the two catalogs' key sets identical.
  - Fix every comment that says that card-face HTML is sanitised, that names goldmark or bluemonday as
    the card-face renderer, or that names shortcode controls in the frame. The list includes
    `types.ts` ("sanitized by goldmark + bluemonday"), the `html` prop of `CardFrame.svelte`, the
    "server-sanitized" comments in `ReviewView.svelte`, `card-frame.ts` and `card_frame.go`.
  - Fix the comment on `ReviewCardView.Ordinal`: a cloze card's ordinal is its cloze number minus
    one, not zero.

  *Acceptance:* `grep -rn -i "sanitiz\|bluemonday\|shortcode" frontend/src internal --include=*.ts --include=*.svelte --include=*.go`
  prints only lines in `internal/store/migrate_*.go`, `internal/mailstyle/` and their tests.
  *Verify:* `npm --prefix frontend run check && npm --prefix frontend test -- --run && npm --prefix frontend run test:client && go vet ./internal/web/... && go test ./internal/i18n/...`.
- [ ] **M17-40 Updating a note type keeps its cards consistent** — a template change adds and removes
  cards as Anki does, behind an impact check.
  *Files:* `internal/api/note_types.go`; `internal/store/notetype.go`; `internal/store/card.go`;
  `internal/i18n/locales/` (append a block with the prefix `note_type.update.`).
  *Problem:* `UpdateNoteType` rewrites the config and touches no card. An added template generates no
  card for the existing notes. A removed template leaves cards whose template id no longer exists:
  rendering them returns 500, and submitting a rating for them returns 500, so they stay in the queue.
  *Work:*
  - The kind (`normal` or `cloze`) of a note type cannot change. An update that changes it returns
    400 `invalid_request`.
  - In one transaction, an update regenerates the cards of every note of the note type with
    `notetype.GenerateCards`. A card that the new config generates and the note lacks is created.
  - A card whose template id the new config no longer declares is soft-deleted. Its `card_states` and
    `reviews` rows stay.
  - The update body accepts `dry_run`. A dry run writes nothing and returns `cards_added`,
    `cards_removed` and `reviews_on_removed_cards`.
  - An update that would remove at least one card requires `confirm_card_removal: true`. Without it
    the update writes nothing and returns 409 with the code `card_removal_unconfirmed` and the same
    three counts.
  - The update writes one audit row that records the three counts.

  *Acceptance:* handler tests:
  - adding a template creates one card per note whose new front is not empty;
  - a dry run reports the counts and writes nothing;
  - removing a template without the confirmation returns 409;
  - removing a template with the confirmation soft-deletes those cards and keeps their reviews;
  - a kind change returns 400;
  - a reader is refused, and a stale version returns 409.

  *Verify:* `go test ./internal/api/... ./internal/store/... && go vet ./internal/api/... ./internal/store/...`.
- [ ] **M17-41 Validate answer specs on every note type write** — one validation for the API, package
  import and migration.
  *Files:* `internal/store/notetype.go` (`NoteType.SetConfig`); `internal/store/note.go`;
  `internal/store/migrate_notes.go`; `internal/store/package_import.go`; `internal/answer/answer.go`.
  *Problem:* only migration 0012 and `ensureStockNoteType` call `answer.ValidateSpec`. A note type
  created or updated through REST, or imported from an `.edeck`, can name a registered control and
  bind a role to a field that does not exist. Submitting a rating for such a card returns 500 from
  `ResolveBindings`, so the card cannot be rated.
  *Work:*
  - `NoteType.SetConfig` validates every template's answer spec. The control must be registered, every
    required role must be bound, and every bound field id must be declared by the config.
  - Remove the now-duplicate `ValidateSpec` calls from `ensureStockNoteType` and migration 0012.

  *Acceptance:* tests with three cases: an unknown control, a missing required role, and a role bound to
  an undeclared field id.
  - Through REST create and update, each case returns 400, and the message names the template and the
    role.
  - Through `.edeck` import, each case is rejected with `package_bad_format`, and no row is written.

  *Verify:* `go test ./internal/store/... ./internal/api/... ./internal/answer/...`.
- [ ] **M17-42 The editing frame runs no inline script** — a field value cannot execute code in the
  editor.
  *Files:* `internal/web/edit_frame.go`; `internal/web/card_frame.go`; `internal/web/*_test.go`;
  `frontend/e2e/browser-isolation.spec.ts`; `ARCHITECTURE.md` (the editing-frame paragraph says that
  the frame carries the card frame's exact isolation).
  *Problem:* the editing frame uses the card frame's CSP, including `script-src 'unsafe-inline'
  'unsafe-eval'`. The runtime writes the field value with `innerHTML`, so an event handler attribute in
  a shared deck's field (for example `<img src=x onerror=…>`) runs inside the editing frame. That code
  can post `upload` messages, and the parent page uploads the posted files under the editing user's
  session. The editing frame needs neither keyword: its runtime is a same-origin module and it only
  inserts MathJax delimiters, it does not typeset them.
  *Work:* the editing frame sends the card frame's CSP with `script-src <origin>` only. Every other
  directive stays identical to the card frame's.
  *Acceptance:*
  - A handler test asserts the exact editing-frame CSP.
  - A browser-isolation case: an `onerror` handler in a loaded field value does not run.
  - The editor's client tests still pass.

  *Verify:* `go test ./internal/web/...`; `npm --prefix frontend run test:client && npm --prefix frontend run test:e2e`.
- [ ] **M17-43 A note type used only by deleted notes cannot be deleted** — deletion must not orphan a
  restorable note.
  *Files:* `internal/store/notetype.go` (`NoteTypeStore.Delete`); `internal/store/notetype_test.go`.
  *Problem:* the usage count skips soft-deleted notes, but a soft-deleted note can be restored, and
  migration 0012 gives soft-deleted notes a note type for exactly that reason.
  *Work:* the usage count includes soft-deleted notes.
  *Acceptance:* a store test: a note type whose only note is soft-deleted returns `ErrNoteTypeInUse`.
  *Verify:* `go test ./internal/store/... && go vet ./internal/store/...`.
- [ ] **M17-44 `{{Deck}}` and `{{Subdeck}}` follow the card's subdeck path** — the special fields
  render as they do in Anki.
  *Files:* `internal/web/review.go` (`renderCard`); `internal/web/note_preview.go`; the tests beside them.
  *Problem:* `renderCard` sets both `DeckName` and `SubdeckName` to the deck name and ignores
  `cards.deck_path`.
  *Work:*
  - `DeckName` is the deck name when `deck_path` is null. Otherwise it is the deck name, then `::`,
    then `deck_path`.
  - `SubdeckName` is the last `::`-separated component of `DeckName`.

  *Acceptance:* a handler test with `deck_path = "Lesson 1::Verbs"` renders `{{Deck}}` as
  `<deck>::Lesson 1::Verbs` and `{{Subdeck}}` as `Verbs`. A card without `deck_path` renders both as the
  deck name.
  *Verify:* `go test ./internal/web/... && go vet ./internal/web/...`.
- [ ] **M17-45 A note write keeps cards that became empty** — Anki's rule: editing a note adds cards
  and never removes one.
  *Files:* `internal/store/card.go` (`syncCards`); `internal/web/review.go`; `internal/store/*_test.go`;
  `internal/web/*_test.go`; `internal/i18n/locales/` (append a block with the prefix
  `review.empty_card.`).
  *Problem:* `syncCards` soft-deletes every card the note no longer generates, for example the reverse
  card after its field is cleared, or the `c2` card after `{{c2::…}}` is removed. Anki keeps such
  cards as empty cards until the user runs Empty Cards, and M17-28 is written on that premise.
  *Work:*
  - A note write creates the cards that `GenerateCards` requires and restores a soft-deleted card that
    it requires again. It soft-deletes no card.
  - Cards that an earlier version soft-deleted stay soft-deleted until the note requires them again.
  - When `notetype.EmptyExistingCards` reports a card as empty, the review and preview responses
    replace its front HTML with a localised notice. The notice says that the card is empty and that
    the empty-card cleanup removes it.

  *Acceptance:*
  - Store tests: clearing the reverse field of a `basic_optional_reversed` note keeps the reverse card
    and its `card_states`; removing `c2` keeps the `cloze:2` card.
  - A handler test: the empty card returns the notice in both locales.

  *Verify:* `go test ./internal/store/... ./internal/web/... ./internal/i18n/...`.
- [ ] **M17-46 `check-migration` reads a database without public ids** — the pre-flight check must
  not fail on the schema it inspects.
  *Files:* `internal/store/migrate_notes.go` (`deckPublicIDs`); `internal/store/migrate_notes_test.go`.
  *Problem:* the check selects `decks.public_id`. On a database older than migration 0004 that column
  does not exist, and `check-migration` exits with an SQL error instead of a report.
  *Work:* when `decks.public_id` is missing, the report names decks by their numeric id, as it already
  does for a deck without a public id.
  *Acceptance:* a test runs the check on a database whose `decks` table has no `public_id` column and
  gets a report.
  *Verify:* `go test ./internal/store/... && go vet ./internal/store/...`.

**Phase 4 — media (on `main`, after the integration merge)**

- [ ] **M17-22 Media by file name and by any type** — resolve media through `media_names`, accept
  every file type, and serve it safely.
  *Files:* `internal/mediatype/`; `internal/media/`; `internal/store/media_*.go`;
  `internal/web/frame_media.go`; `internal/web/media.go`.
  *Work:*
  - `/fm/<token>/<name>` resolves `name` through `media_names` for the token's deck.
  - The allowlist becomes a classification: image (including SVG and AVIF), audio (including wav,
    flac and opus), video (mp4, webm), font, script, style, other.
  - Upload and import accept every class, within the quota.
  - `/media/<sha>` sends `Content-Security-Policy: sandbox` on every response, and serves everything
    that is not image, audio or video as an attachment.
  - Templates and CSS that reference `_`-prefixed files record them in `media_note_types`. Media
    garbage collection counts these references.
  - On import, a name clash with different content renames the new file as Anki does and rewrites
    its references.

  *Acceptance:* tests cover:
  - detection of each class;
  - an SVG containing `<script>` served from `/media/<sha>` with the sandbox CSP;
  - a JS file served as an attachment;
  - a `_font.woff2` referenced only from CSS that survives garbage collection;
  - the clash rename;
  - a frame media request for a sha that the token user can read in another deck, but that no
    `media_names` row of the token's deck names, returns 404.

  *Verify:* `go test ./internal/mediatype/... ./internal/media/... ./internal/store/... ./internal/web/...`.

**Phase 5 — `.apkg` import**

- [ ] **M17-23 `.apkg` reader** — read all three package formats into an in-memory model.
  *Depends on:* M17-22.
  *Files:* `internal/anki/` (new); `go.mod`, adding `github.com/klauspost/compress/zstd` (reason: the
  standard library has no zstd, and `collection.anki21b` is zstd-compressed).
  *Work:*
  - `collection.anki2` and `collection.anki21`: SQLite through `glebarez/sqlite`. Note types and
    decks come from the JSON columns; the `media` map is JSON.
  - `collection.anki21b`: zstd, then SQLite. Note types, fields and templates are protobuf configs.
    The `meta` and `media` entries are protobuf, decoded with
    `google.golang.org/protobuf/encoding/protowire` by field number, with no code generation.
  - Archive limits match the `.edeck` reader: entry count, total size and path safety.

  *Acceptance:*
  - The three fixture packages from M17-5 decode to the same model.
  - Negative cases: a corrupt zstd stream, a missing collection, and a path-traversal entry.

  *Verify:* `go test ./internal/anki/... && go vet ./internal/anki/...`.
- [ ] **M17-24 `.apkg` import into a deck** — map the decoded package onto the data model.
  *Depends on:* M17-23, M17-33, M17-34, M17-35, M17-41, M17-44.
  *Files:* `internal/anki/import.go`; `internal/api/package*.go`; `internal/mcp/mcp.go`;
  `frontend/src/lib/views/ImportView.svelte`; the locale catalogs (append a block with the prefix
  `import.apkg.`).
  *Work:*
  - The top-level deck becomes one engram deck, and subdecks fill `cards.deck_path`.
  - Note types are imported unchanged, with `answer = null`.
  - Notes get `external_ref = "anki:<guid>"`.
  - Media is registered in `media_names`.
  - Cards are created as they exist in the package, not regenerated.
  - Re-importing into the same deck:
    - updates notes matched by guid, keeping cards and progress;
    - matches note types by `anki_origin.notetype_id`;
    - overwrites the templates and CSS when the field and template structure is equal;
    - otherwise creates a new note type and moves the matched notes to it.
  - The report lists, item by item:
    - missing media;
    - video formats a browser cannot play (anything other than mp4 or webm);
    - references to external scripts or stylesheets;
    - `[latex]` without its image.
  - The Import page, `POST` package import and the MCP `import_deck` accept `.apkg`.

  *Acceptance:*
  - Importing every fixture and comparing the rendered faces with the golden files passes.
  - A re-import test proves that progress is kept.
  - A `dry_run` test writes nothing.
  - The maintainer places an Ultimate Geography `.apkg` in `testdata/local/` and records that the
    card count and every rendered face equal the oracle output. The evidence is the captured command
    output.

  *Verify:* `go test ./internal/anki/... ./internal/api/... ./internal/mcp/...`; `npm --prefix frontend run test:client`.

**Phase 6–9 — remaining capabilities**

- [ ] **M17-25 Audio, video and TTS** — complete playback.
  *Depends on:* M17-17, M17-22.
  *Files:* `frontend/src/cardframe/`; `internal/store/models.go` (the deck column `autoplay`, a `*bool`
  defaulting to true per `AGENTS.md` 2.3 item 9); the deck settings view; the locale catalogs (append
  a block with the prefix `deck.autoplay.`).
  *Work:*
  - The question side autoplays its tags in order. The answer side plays only the tags the answer
    adds, so audio brought in by `{{FrontSide}}` is not replayed. Both follow the deck's `autoplay`
    setting.
  - `r` and `F5` replay.
  - `[sound:]` with mp4 or webm plays in an inline `<video>`.
  - TTS uses `speechSynthesis`:
    - `lang` maps `xx_YY` to `xx-YY`;
    - `voices` matches by name, falling back to the language default;
    - `speed` maps to `rate`;
    - `cloze_blank` is honoured.
  - `span.engram-sound` applies `data-start`, `data-end`, `data-rate` and `data-loop`.
  - The rich-text editor's "insert audio" dialog sets these four attributes.

  *Acceptance:*
  - Client tests: playback order per side, the `FrontSide` exclusion, segment and loop timing on a
    mocked media element, and the voice fallback.
  - The UG check from M17-24 also shows the same audio list per face.

  *Verify:* `npm --prefix frontend run test:client && go test ./internal/store/...`.
- [ ] **M17-26 Type-in in the frame** — Anki's `{{type:F}}` without an answer spec.
  *Depends on:* M17-17.
  *Files:* `frontend/src/cardframe/`; `internal/web/review_api.go`.
  *Work:*
  - The front renders `input#typeans` in the field's font and size, and Enter posts
    `answer {typed}`.
  - The back is rendered with M17-11's comparison.
  - The rating stays self-chosen.

  *Acceptance:*
  - A handler test renders the back for a typed value and matches the golden comparison.
  - A client test covers Enter posting `answer`.

  *Verify:* `go test ./internal/web/...`; `npm --prefix frontend run test:client`.
- [ ] **M17-27 Note type management** — edit a deck's note types.
  *Depends on:* M17-18, M17-35, M17-40.
  *Files:* a new view under `frontend/src/lib/views/` (single-writer hotspot); `frontend/src/lib/router`;
  the locale catalogs (append a block with the prefix `notetype.`).
  *Work:*
  - Create a note type from a stock definition (Anki's six and engram's ten) or by copying one.
  - Fields: add, rename, delete, reorder and options. A rename rewrites the template references, as
    Anki does.
  - Templates: add, rename, delete and reorder. The front, back and styling editors have a live frame
    preview. The editor also covers browser templates and the answer spec (control and field
    bindings).
  - A change that adds or removes cards lists the affected card count and review count before
    saving.

  *Acceptance:*
  - Client tests: a rename rewrites `{{Old}}`, `{{#Old}}` and `{{text:Old}}`.
  - The impact prompt shows the counts that the server returns.
  - Handler tests: a reader cannot save, and a stale version returns `409`.

  *Verify:* `npm --prefix frontend run check && npm --prefix frontend run test:client && go test ./internal/api/...`.
- [ ] **M17-28 Empty-card cleanup and note type change** — Anki's "Empty Cards" and "Change Note
  Type".
  *Depends on:* M17-27, M17-45.
  *Files:* `internal/api/` (service methods and routes); `internal/store/`; the note type view.
  *Work:*
  - Cleanup lists the cards whose front renders empty (M17-9) and deletes them after confirmation,
    writing an audit row.
  - Change note type moves notes to another note type of the same deck with a field mapping and a
    template mapping. Cards follow their mapped template, and an unmapped template's cards are
    deleted after confirmation. Progress follows the card.

  *Acceptance:*
  - Store tests: cleanup removes only empty cards; a mapping change keeps `card_states` for every
    mapped card.
  - Negative cases: a target note type in another deck is rejected; a reader is refused.

  *Verify:* `go test ./internal/api/... ./internal/store/...`.
- [ ] **M17-29 Deck description as HTML or Markdown** — Anki's `markdown_description`.
  *Files:* `internal/store/models.go` (the deck column `description_markdown`, a `*bool` defaulting to
  false); the deck settings and deck detail views.
  *Work:* the description is HTML by default, and Markdown when the flag is set (rendered with
  goldmark). It is shown in the frame.
  *Acceptance:*
  - Tests: an HTML description with a `<script>` runs only inside the frame.
  - Tests: the flag switches the rendering.

  *Verify:* `go test ./internal/store/... ./internal/web/...`; `npm --prefix frontend run test:client`.
- [ ] **M17-30 Image occlusion in review** — port Anki's `ts/routes/image-occlusion/review.ts`.
  *Depends on:* M17-17.
  *Files:* `frontend/src/cardframe/`.
  *Work:*
  - `anki.imageOcclusion.setup()` (and the deprecated `anki.setupImageCloze`) draws rect, ellipse,
    polygon and text shapes on a canvas over the image.
  - Both modes are supported: hide all, guess one; and hide one, guess one (`oi=1`).
  - The toggle-masks button.

  *Acceptance:*
  - The image-occlusion fixtures from M17-5 render through the server and the frame. A client test
    asserts the drawn shape list for each card.

  *Verify:* `npm --prefix frontend run test:client`.
- [ ] **M17-31 Image occlusion editor** — create and edit occlusion notes.
  *Depends on:* M17-27, M17-30.
  *Files:* a new editor component; `frontend/package.json` (dependency `fabric`. Reason: Anki's mask
  editor is built on it, and using the same library keeps the stored shape data identical).
  *Work:*
  - Draw, move and resize rect, ellipse, polygon and text shapes.
  - Group shapes into one cloze number.
  - Choose the occlusion mode.
  - Write the occlusion field in Anki's `{{cN::image-occlusion:…}}` form.

  *Acceptance:*
  - A note created in the editor renders the same as an Anki-created fixture with the same shapes.
  - The stored field round-trips through edit without change.

  *Verify:* `npm --prefix frontend run check && npm --prefix frontend run test:client`.

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

**The M17 content-model merge is a breaking release of the HTTP API, the MCP tools, the database
schema and the `.edeck` file format. Everything else listed here is additive except one removal: the
unused `archived_at` deck field and its column. No configuration key changed.** The v0.2.0 release
notes cover everything before it.

#### Anki-compatible content model (M17 phases 1–3, breaking)

Note types are now data (`internal/notetype`): fields, templates, CSS and Anki's note type options,
with eight registered answer controls in `internal/answer`. `internal/cardtype` and the
bluemonday-sanitised `internal/render` are gone.

- **HTTP API.** A note is written as `{"note_type": "<public id>", "fields": {"<field name>":
  "<HTML>"}}`; a card-face response carries `front`/`back` objects `{html, css, body_class, side,
  media_base}` plus the template ordinal; note types have list/get/create/update/delete endpoints
  under a deck (update is versioned, `409` on a stale version); the `card-types` catalog is
  transitional. New routes: `/card-frame/<version>`, `/edit-frame/<version>`, `/fm/<token>/<name>`.
  New stable codes: `note_type_in_use`, `package_format_obsolete`.
- **MCP.** `list_card_types` is replaced by `list_note_types` (deck-scoped) and `get_note_type`;
  `create_notes` and `update_note` take `note_type`, and field values are HTML.
- **Database schema.** New `note_types`, `media_names` and `media_note_types`; `notes` gains
  `note_type_id` (not null after `0013`), `source_url` and `extra_json`, and loses `kind`;
  `notes.fields_json` is a field-id → HTML map; `cards` gains `deck_path`. Migrations `0012` and
  `0013` convert existing notes; `0014` converts stored custom mail templates from Markdown to HTML.
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
| Deck progress | The deck list, the home deck list and the deck detail header show how many of the deck's cards the user has studied and how many new cards are left, as `Studied N · Left M`. On the home deck list each deck gets a second line of numbers: today's new and review counts as badges, which turn grey at zero, and a progress bar beside the studied and remaining text, which turns to the success colour once no new card is left. The counts are per user, so a shared deck never shows another member's progress, and they ignore the daily limits. A suspended new card is not counted as left; a suspended studied card still counts as studied. | No — two fields added to a session-only response |
| Deck pause | A user pauses a deck for themselves from the deck list (a toggle beside delete or leave) or the deck detail header; the owner and every member, readers included, decide alone. A paused deck keeps a `Paused` badge on the deck list and disappears from the home deck list and the sidebar. It stays out of the default review queue, the home due count, the unfiltered forecast, the weekly digest and the reminder mail, while naming it (`/review?deck=…`, batch review, a stats deck filter) still studies or shows it. Progress is kept and cards keep falling due. Pausing and resuming write the audit actions `deck.pause` and `deck.resume`. | No — additive route, tool, table and field; nothing changes until a user pauses a deck |
| Note search on field text | Note search (`GET /api/v1/decks/:id/notes?q=`, `search_notes` and the card list) matches the text of the fields instead of their JSON encoding. A field key such as `front` no longer matches every note; `<`, `>`, `&`, quotes and text across a line break can be found; case folding of non-ASCII letters is the same on SQLite and PostgreSQL. A query is split on whitespace and every term must occur, in any order. | No — results change, the request and response shapes do not |
| Admin user search wildcards | `_` and `%` in the admin user search match only themselves instead of acting as LIKE wildcards. | No |
| Deck archive removal | The deck archive state is removed: it had no route, tool or page that set it, the sidebar's archived filter could never match, and pausing a deck already covers hiding one. The `archived_at` field leaves every deck response and the column leaves the database through a versioned migration. | Yes — HTTP API and MCP payload (`archived_at` removed) and database schema (column dropped by migration 0010) |
