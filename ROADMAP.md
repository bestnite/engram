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

**Phase 6–9 — remaining capabilities**

- [ ] **M17-25 Audio, video and TTS** — complete playback.
  *Depends on:* M17-17.
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
  *Depends on:* M17-18.
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
| Editor on note types (M17-35, M17-40) | The note editor lists the deck's note types, builds the form from the selected type's fields in config order (field options included) and saves `{note_type, fields}` without a kind; the preview renders the stored type's own templates and CSS and answers 404 for a type of another deck. A note type update regenerates the cards of every live note of the type in one transaction: an added template creates the missing cards, a removed template soft-deletes their cards while `card_states` and `reviews` stay. The edit body takes `dry_run` and `confirm_card_removal`; a removal without confirmation is 409 `card_removal_unconfirmed` with the three counts, a kind change is 400, and a successful update writes one audit row with the counts. | Yes — the note preview request takes `note_type` instead of `kind`; the note type update response gains four fields and a new 409 code |
| Deck progress | The deck list, the home deck list and the deck detail header show how many of the deck's cards the user has studied and how many new cards are left, as `Studied N · Left M`. On the home deck list each deck gets a second line of numbers: today's new and review counts as badges, which turn grey at zero, and a progress bar beside the studied and remaining text, which turns to the success colour once no new card is left. The counts are per user, so a shared deck never shows another member's progress, and they ignore the daily limits. A suspended new card is not counted as left; a suspended studied card still counts as studied. | No — two fields added to a session-only response |
| Deck pause | A user pauses a deck for themselves from the deck list (a toggle beside delete or leave) or the deck detail header; the owner and every member, readers included, decide alone. A paused deck keeps a `Paused` badge on the deck list and disappears from the home deck list and the sidebar. It stays out of the default review queue, the home due count, the unfiltered forecast, the weekly digest and the reminder mail, while naming it (`/review?deck=…`, batch review, a stats deck filter) still studies or shows it. Progress is kept and cards keep falling due. Pausing and resuming write the audit actions `deck.pause` and `deck.resume`. | No — additive route, tool, table and field; nothing changes until a user pauses a deck |
| Note search on field text | Note search (`GET /api/v1/decks/:id/notes?q=`, `search_notes` and the card list) matches the text of the fields instead of their JSON encoding. A field key such as `front` no longer matches every note; `<`, `>`, `&`, quotes and text across a line break can be found; case folding of non-ASCII letters is the same on SQLite and PostgreSQL. A query is split on whitespace and every term must occur, in any order. | No — results change, the request and response shapes do not |
| Admin user search wildcards | `_` and `%` in the admin user search match only themselves instead of acting as LIKE wildcards. | No |
| Deck archive removal | The deck archive state is removed: it had no route, tool or page that set it, the sidebar's archived filter could never match, and pausing a deck already covers hiding one. The `archived_at` field leaves every deck response and the column leaves the database through a versioned migration. | Yes — HTTP API and MCP payload (`archived_at` removed) and database schema (column dropped by migration 0010) |
