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

These tasks fix what the P3 merge left wrong or unfinished. They land on `main` directly.

- [ ] **M17-36 Remove the transitional card-kind layer** — delete every payload field and endpoint
  that exists only for the old SPA.
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

**Phase 9 follow-up — defects found in the review of M17-28, M17-31, M17-39 and M17-52 to M17-59 (on `main`)**

These tasks land on `main` directly.

- [ ] **M17-60 Changing the note type generates cards and allows template swaps** — match Anki's
  "Change Note Type" card rules.
  *Files:* `internal/store/note_type_change.go` (`ChangeNoteType`, `moveNoteCards`);
  `internal/api/note_type_change.go`; `frontend/src/lib/views/NoteTypesView.svelte` (single-writer
  hotspot); `frontend/src/lib/api/types.ts`; the tests beside them.
  *Problem:*
  - `ChangeNoteType` moves cards and generates none ("Cards are moved, not regenerated"). Anki
    `26.09.3` (`rslib/src/notetype/notetypechange.rs`) updates each changed note with
    `update_note_inner_generating_cards`, so a target template that no source card maps to still gets
    a card when its front is not empty. Changing a note from a basic type to a basic-and-reversed type
    therefore yields no reverse card in engram.
  - `moveNoteCards` refuses a move when another live card of the same note already holds the target
    template id. Two note types created from the same stock definition share template ids
    (`forward`, `reverse`), so a swap mapping `forward → reverse`, `reverse → forward` between them is
    refused. Anki remaps each card from its own original ordinal, so a swap succeeds.

  *Work:*
  - After a note's cards are moved, the change creates every card that `notetype.GenerateCards`
    produces for the note under the target config and that the note lacks. It soft-deletes no card
    beyond the unmapped ones it already removes.
  - A mapping that permutes template ids among a note's live cards succeeds. The moves are written so
    that the `(note_id, template)` unique index never holds two rows with the same template after any
    single statement.
  - A soft-deleted card that holds a target template id still makes the change fail with
    `ErrNoteTypeMappingInvalid` naming the note, as today.
  - The impact gains `cards_added`. A dry run reports it and writes nothing. The note type view shows it
    in the confirmation.

  *Acceptance:* store tests:
  - changing a note from a basic type to a basic-and-reversed type, mapping only the forward template,
    keeps the forward card's id and `card_states`, and creates the reverse card;
  - a swap mapping between two types with the template ids `forward` and `reverse` succeeds, and each
    card keeps its id and `card_states` under its new template;
  - a dry run reports `cards_added` and writes nothing.

  *Verify:* `go test ./internal/store/... ./internal/api/...`; `npm --prefix frontend run check && npm --prefix frontend run test:client`.
- [ ] **M17-61 Write imported media outside the database transaction, and limit concurrent imports** —
  a large import must not hold the database write lock or fill the disk.
  *Files:* `internal/store/anki_import.go` (`importAnkiPackage`, `importAnkiInTx`);
  `internal/store/package_import.go`; `internal/api/package.go`, `package_upload.go` and
  `package_url.go`; `internal/api/errors.go` (the code and its default message);
  `frontend/src/lib/views/ImportView.svelte` and the frontend locale catalogs (append a block with the
  prefix `import.busy.`), so the Import page explains the refusal; `ARCHITECTURE.md`; the tests beside
  them.
  *Problem:*
  - `importAnkiInTx` streams every new media file into the content-addressed store inside the one
    database transaction of the import. Since M17-53 an `.apkg` may decompress to 4 GiB, so the
    transaction can last minutes. On SQLite the write lock is held for that whole time, and every other
    user's review submission waits or fails.
  - Nothing bounds how many imports run at once. Each import may first spool a body of up to
    `package_max_bytes` (2 GiB) to a temporary file, so a few simultaneous imports can fill the
    temporary disk.

  *Work:*
  - The `.apkg` import writes every new media file to the content-addressed store after the hash pass
    and before the database transaction opens. The transaction writes only rows. When the transaction
    fails or is a dry run, the files this import created are removed through the existing
    `writtenMedia` cleanup. A file that existed before the import is never removed.
  - A dry run writes no media file.
  - A new setting `package_max_concurrent_imports` / `PACKAGE_MAX_CONCURRENT_IMPORTS`, resolved as
    environment variable, then settings table, then the default `2`, the way the other `package_*`
    settings are resolved. It caps the number of package imports that run at the same time in one
    server process.
  - The multipart handler takes an import slot before it parses the multipart body. The URL import
    takes a slot before it downloads. The upload-ticket import takes a slot before it reads the body.
    Each slot is released when its import ends, on every path.
  - When no slot is free, the request is refused at once with 429 and the new code `deck_import_busy`,
    and nothing is read or spooled. The MCP import tool reports the same code.
  - The CLI `import` subcommand takes no slot.

  *Acceptance:*
  - A store test: while the media of a large generated package is written, another connection can
    commit a write to the same SQLite database.
  - A store test: a failed import and a dry run leave no media file that the import created, and keep
    every file that existed before.
  - A handler test with the setting at `1`: a second import that starts while the first is running gets
    429 `deck_import_busy`; after the first ends, a new import succeeds.
  - The existing import tests and goldens pass.

  *Verify:* `go test ./internal/store/... ./internal/api/... ./internal/mcp/...`.
- [ ] **M17-62 Compare every image occlusion shape with Anki** — the editor's output must match Anki
  for all shape kinds, not only rectangles.
  *Files:* `tools/anki-oracle/generate.py`; `internal/notetype/testdata/anki/` (a new fixture and its
  golden); `frontend/src/tests/image-occlusion-editor.client.ts`;
  `frontend/src/tests/image-occlusion-markup.test.ts`.
  *Problem:* M17-31's acceptance asks that a note created in the editor renders the same as an
  Anki-created fixture with the same shapes. The only image occlusion note in the fixtures
  (`stock.golden.json`) holds one rectangle, so ellipses, polygons, text, several shapes under one cloze
  number, and the hide-one mode were never compared with Anki's output.
  *Work:*
  - The oracle generates a fixture of image occlusion notes made with Anki `26.09.3`: one note per shape
    kind (rect, ellipse, polygon, text); one note with two shapes under one cloze number; and one note in
    the hide-one, guess-one mode (`oi=1`). The golden records each field and each rendered face.
  - The tests rebuild every fixture note with the editor's shape model, and compare the field it writes
    and the faces it renders with the golden.

  *Acceptance:* the fixture and its golden are committed, and every comparison passes. A test that
  edits nothing and saves gets each golden field back byte for byte.
  *Verify:* `npm --prefix frontend test -- --run && npm --prefix frontend run test:client`.

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
  under a deck (update is versioned, `409` on a stale version); the `card-types` catalog is
  transitional. New routes: `/card-frame/<version>`, `/edit-frame/<version>`, `/fm/<token>/<name>`.
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
| Anki `.apkg` import (M17-23, M17-24) | `internal/anki` reads all three Anki package layouts (legacy `collection.anki2`, `collection.anki21`, and zstd-compressed `collection.anki21b` with protobuf note type/deck configuration) under the deck package's entry, per-file and total caps. The import page, `POST` package import, upload tickets and the MCP `import_deck` accept `.apkg`: the top-level package deck becomes one engram deck, subdecks fill `cards.deck_path`, note types arrive unchanged with no answer spec, notes carry `external_ref = "anki:<guid>"`, media is registered by name, and the package's cards are created as they exist. Re-importing into the same deck matches notes by guid and note types by `anki_origin.notetype_id`, keeping cards and progress; an equal field/template structure is overwritten in place, a changed one creates a new note type and moves the notes. The report lists missing media, unplayable video, external scripts/stylesheets and missing LaTeX images. | No — a new input format is accepted and the import report gains four lists |
| Anki import deck choice (M17-47) | An `.apkg` is named after its real deck tree, not Anki's usually empty `Default` deck: only decks that hold package cards take part, the top name is the longest `::` component prefix they share (or the largest branch when they share none), cards in it get no subdeck path, and the package description is adopted only when the deck rules accept it. | No |
| Note field HTML in responses and note type card removal (M17-49, M17-50) | Every note response (REST get/list/search, MCP note tools, due-card fields) returns each field's stored HTML string unchanged instead of reshaping stock-kind fields into arrays, booleans and numbers, so opening and saving a choice note no longer deletes the formatting of its options. A note type update soft-deletes only the cards of templates the new config no longer declares; a declared template whose front is now empty keeps its cards, and a cloze update removes none. | Yes — note response field values are HTML strings for every note type where some stock kinds used to return arrays, booleans or numbers |
| Type-in in the frame (M17-26) | A template with `{{type:F}}` and no answer spec draws `input#typeans` in the frame, in the field's font and size, and posts the typed value on Enter or input; revealing renders the back through Anki's comparison (good/bad/missed) while the rating stays one of the four self-ratings. The render request takes an optional `answer` and the response reports `type_in`. | No — two optional fields on the review render request/response |
| Empty cards survive a note edit (M17-45) | A note write creates the cards the note requires and restores a previously removed one, but soft-deletes nothing: clearing the reverse field or removing a cloze marker keeps the card and its progress. When an existing card's front renders empty, the review response shows a localised notice saying the card is empty and that the Empty Cards cleanup removes it. | No |
| Structural re-import removes stale cards (M17-51) | A re-import that moves a note to a new note type soft-deletes that note's cards whose template the new type no longer declares, keeping their `card_states` and `reviews`; the import report gains `cards_removed`, and a dry run reports the count. | No — one report field is added |
| Image occlusion in review (M17-30) | Ported Anki's image-occlusion review runtime into the card frame: `anki.imageOcclusion.setup()` (and `anki.setupImageCloze`) draws rectangle, ellipse, polygon and text masks on a canvas over the image, supports hide-all-guess-one and hide-one-guess-one (`oi=1`), and wires the toggle-masks button and `M`. | No |
| Deck description as Markdown (M17-29) | A deck's description is HTML by default and Markdown (goldmark, with TeX protection) when the new owner-only flag is set; the server renders it and the deck page shows it inside the sandboxed frame, never in the parent page. The deck response gains `description_markdown` and `description_html`, the settings page has the toggle, and a clone keeps the format. | No — a deck column and two response fields are added |
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
| Deck description as plain text in the lists (M17-57) | Every deck response gains `description_text`: `description_html` reduced to one line of plain text. The deck list and the home deck list print it, so an imported HTML description no longer shows raw tags, while the deck detail page still renders the description as HTML inside the sandboxed frame. | No — additive response field |
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
