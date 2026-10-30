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
- **The P3 integration branch.**
  - M17-12 to M17-20 replace the content model and cannot land on `main` one by one. Each of these
    tasks branches from, and merges back into, the integration branch `feat/m17-p3`.
  - The parent merges `feat/m17-p3` into `main` once, after all of them are done and the full-module
    check passes.
  - That merge is the single breaking release of M17: HTTP API, MCP, database schema and file
    format.

**Phase 1 — sandboxed frame (on `main`; the content model does not change yet)**

- [ ] **M17-3 Show every card face in the frame** — a `CardFrame` component, used wherever a card face
  is shown today.
  *Depends on:* M17-1, M17-2.
  *Files:* `frontend/src/lib/components/CardFrame.svelte` (new); `frontend/src/lib/views/ReviewView.svelte`;
  `frontend/src/lib/views/NoteEditView.svelte`; any other view that injects a card face. The views
  directory is a single-writer hotspot.
  *Work:*
  - Card faces leave the parent DOM. Choice options stay as sanitised `{@html}` in the parent until
    M17-17.
  - The existing shortcode enhancement (`frontend/src/lib/shortcodes`) moves into the frame runtime.
  - The parent:
    - forwards the review shortcuts it receives as `key` messages;
    - resizes the iframe from `height`;
    - sends `theme` whenever the theme changes;
    - opens `link` targets in a new tab with `noopener`.

  *Acceptance:*
  - A Vitest case scans `frontend/src` for `{@html`. The case keeps an allowlist that names each
    remaining use and its reason, and it fails on any use not in the list.
  - Review, the note preview and the deck page look and behave as before: formulas, audio, TTS and
    keyboard shortcuts. The evidence is a captured screenshot set from the `run` skill.

  *Verify:* `npm --prefix frontend run check && npm --prefix frontend test -- --run && npm --prefix frontend run test:client`.
  *Evidence pending:* the screenshot set from the `run` skill is captured by M17-4's browser
  harness; the component tests and the `{@html}` allowlist test landed in `6865a54`.
- [ ] **M17-4 Browser isolation tests** — prove the sandbox in a real browser.
  *Depends on:* M17-3.
  *Files:* `frontend/e2e/` (new); `frontend/package.json` (dev dependency `@playwright/test`. Reason:
  the sandbox is the only security boundary for unsanitised HTML, and only a real browser can prove
  it); `.github/workflows/ci.yml` (a job that installs Chromium and runs the suite).
  *Acceptance:* a test card whose template script tries each of these must fail at every one:
  - reading `document.cookie` returns an empty string;
  - `fetch('/api/v1/decks')` is rejected by CSP;
  - `top.location = …` is blocked;
  - submitting a `<form>` is blocked;
  - `window.open` returns `null`;
  - reading `parent.document` throws.

  The same card can load an https image, and the test observes that the image loaded.
  *Verify:* `npm --prefix frontend run test:e2e` against a server started by the test with SQLite.

**Phase 2 — `internal/notetype`, the Anki engine (on `main`; not wired into any request path)**

- [ ] **M17-9 Card generation** — port `rslib/src/notetype/cardgen.rs`.
  *Depends on:* M17-7, M17-8.
  *Files:* `internal/notetype/cardgen.go` and its tests.
  *Work:*
  - A normal note type generates the card of a template when that template's front renders non-empty
    with the note's non-empty fields.
  - A cloze note type generates one card per cloze number found in any of the note's fields, as
    Anki does; `{{cloze:X}}` decides what a card displays, not which cards exist.
  - The function also reports the existing cards whose front is now empty. It never deletes them;
    that is M17-28.

  *Acceptance:* for every fixture note, the generated card set equals the golden card list.
  *Verify:* `go test ./internal/notetype/...`.
- [ ] **M17-10 Audio, TTS and LaTeX markup; `PlainText`** — port the relevant parts of
  `rslib/src/card_rendering/` and `rslib/src/latex.rs`.
  *Depends on:* M17-7.
  *Files:* `internal/notetype/av.go`, `latex.go` and `plaintext.go`, with their tests.
  *Work:*
  - Parse `[sound:<name>]` and `[anki:tts …]…[/anki:tts]`. Unknown TTS options are kept.
  - Replace each tag with Anki's replay-button markup, and return the ordered tag list per side.
  - Replace `[latex]`, `[$]` and `[$$]` with `<img class=latex src="latex-<sha1>.<png|svg>">`, using
    Anki's file-name rule.
  - `PlainText(html)` strips HTML and audio/TTS tags as Anki's `html_to_text_line` does.

  *Acceptance:*
  - Tag lists and rendered HTML equal the golden output.
  - `PlainText` has table-driven cases for entities, `<br>`, block elements and tags.

  *Verify:* `go test ./internal/notetype/...`.
- [ ] **M17-11 Type-in comparison** — port `rslib/src/typeanswer.rs`.
  *Depends on:* M17-10.
  *Files:* `internal/notetype/typeanswer.go` and its tests.
  *Work:* the `compare_answer` output (`code#typeans` with `typeGood`, `typeBad` and `typeMissed`),
  the combining and non-combining (`nc`) variants, and the expected-text extraction for
  `type:cloze`.
  *Acceptance:* every type-in fixture input equals the golden output.
  *Verify:* `go test ./internal/notetype/...`.

**Phase 3 — the switch (integration branch `feat/m17-p3`; one breaking release)**

- [ ] **M17-12 Schema for note types and media names** — add the tables and columns of the shared
  data model, with their stores.
  *Files:* `internal/store/models.go` (single-writer hotspot); `internal/store/notetype.go` and
  `internal/store/media_names.go` (new).
  *Work:*
  - AutoMigrate adds `note_types`, `media_names`, `media_note_types`, `notes.note_type_id`
    (nullable for now), `notes.source_url`, `notes.extra_json` and `cards.deck_path`.
  - `NoteTypeStore` provides list by deck, get, create, update with `version` (a mismatch returns
    `ErrVersionConflict`) and delete. Delete is refused while notes use the note type.
  - Both databases are supported: JSON stays TEXT, upserts use `clause.OnConflict`.

  *Acceptance:*
  - Table-driven store tests on SQLite cover the version conflict, the refused delete, and the
    `(deck_id, name)` uniqueness of `media_names`.
  - The same tests pass with `TEST_PG_DSN` set, run by hand.
  - Update `ARCHITECTURE.md` (data model).

  *Verify:* `go test ./internal/store/... && go vet ./internal/store/...`.
- [ ] **M17-13 Answer controls** — the registry `internal/answer` and the eight controls that replace
  engram's graded kinds.
  *Depends on:* M17-10, M17-11, M17-12.
  *Files:* `internal/answer/` (new).
  *Work:* each control renders its input markup at the template's `{{type:…}}` placeholder. On the
  front it renders the input; on the back it renders the result breakdown. Each control also parses
  the answer JSON and grades it with the existing scoring rules. The answer JSON shapes and
  `grade_detail_json` are those of today's kinds. The score maps to a rating through the preset's
  grade mapping.

  | Control | Required roles | Optional roles |
  |---|---|---|
  | `text` | `answer` | `accept`, `ignore_case`, `ignore_whitespace`, `regex` |
  | `numeric` | `value` | `tolerance_absolute`, `tolerance_relative`, `unit` |
  | `choice_single` | `options`, `answer` | — |
  | `choice_multi` | `options`, `answers` | — |
  | `true_false` | `answer` | — |
  | `list` | `items` | `ordered` |
  | `cloze_blanks` | `text` | — |
  | `short_answer` | — | `reference` |

  Bound field values are HTML and are parsed in one of four ways:
  - **text:** `PlainText`, trimmed;
  - **lines:** `PlainText` split at line breaks, where `<br>` and block boundaries count as line
    breaks; each line trimmed; empty lines dropped;
  - **bool:** the lowercased text equals `true` or `false`;
  - **number:** the text parsed as a decimal.

  `answer` (choice_single) and each line of `answers` (choice_multi) are zero-based option indices.
  `cloze_blanks` has no `|` or `\alt` rule. `short_answer` is self-graded.
  A template with an answer spec must contain exactly one `{{type:…}}` on its front; note type
  validation enforces this.
  *Acceptance:*
  - Every grading case of today's `internal/cardtype` tests, rewritten as HTML field values, yields
    the same rating and detail. The only exception is the cloze cases that use `|` or `\alt`; their
    expected values follow the removed rule.
  - Negative cases cover: a missing required role, an unparseable bool, an unparseable number, an
    option index out of range, and two `{{type:…}}` placeholders.

  *Verify:* `go test ./internal/answer/... ./internal/notetype/... && go vet ./internal/answer/...`.
- [ ] **M17-14 Data migration** — convert every existing note to the new model, and add a read-only
  pre-flight check.
  *Depends on:* M17-12, M17-13.
  *Files:* `internal/notetype/stock_engram.go` (new); `internal/store/migrate.go` (a new versioned
  migration); `cmd/engram` (a new subcommand `check-migration`).
  *Work:*
  - For each deck and each `kind` it uses, the migration creates one note type from the engram stock
    definitions. Field ids and names keep today's field names.

    | Kind | Templates | Answer control |
    |---|---|---|
    | `basic` | template id `forward` | — |
    | `basic_both` | template ids `forward`, `reverse` | — |
    | `cloze` | `kind=cloze`; front `{{cloze:text}}{{type:cloze:text}}` | `cloze_blanks` |
    | `typed` | front `{{prompt}}{{type:answer}}` | `text` |
    | `numeric` | front `{{prompt}}{{type:value}}` | `numeric` |
    | `choice_single` | front `{{question}}{{type:options}}` | `choice_single` |
    | `choice_multi` | front `{{question}}{{type:options}}` | `choice_multi` |
    | `true_false` | front `{{statement}}{{type:answer}}` | `true_false` |
    | `list` | front `{{prompt}}{{type:items}}` | `list` |
    | `short_answer` | front `{{prompt}}{{type:reference}}` | `short_answer` |

  - Every field value is converted, in this order:
    1. Markdown to HTML, with today's goldmark configuration: GFM, single newline to `<br>`, formula
       protection.
    2. `{{tts:X|lang=L|rate=R}}` becomes `X[anki:tts lang=L' speed=R]X[/anki:tts]`, where `L'` is `L`
       with `-` replaced by `_`. With the `hide` flag, the leading visible `X` is omitted.
    3. `{{audio:/media/S|start=a|end=b|rate=r|loop}}` becomes
       `<span class="engram-sound" data-start="a" data-end="b" data-rate="r" data-loop="1">[sound:S.ext]</span>`.
       Without parameters it becomes plain `[sound:S.ext]`.
    4. Every `/media/<sha>` becomes `<sha>.<ext>` and is registered in `media_names` for the deck.
    5. A string array becomes its items, each converted by step 1 as inline HTML, joined with `<br>`.
       A boolean becomes `true` or `false`. A number becomes its decimal string.
  - `source_url` and `extra` move to their columns.
  - `cards.template` values are unchanged. `card_states` and `reviews` are not touched.
  - The rewrite from M17-2 is deleted.
  - The pre-flight check lists:
    - every note with an array item containing a newline; the migration refuses to run while any
      exists;
    - every cloze note containing `|` or `\alt`; this is a warning only.
  - A second versioned migration makes `notes.note_type_id` not null and drops `notes.kind`.
  - Custom mail templates are converted by M17-19, not here.

  *Acceptance:*
  - A test seeds one note of every kind, including audio, TTS, formulas and media. It runs the
    migration and asserts the converted field values, the created note types and the unchanged cards
    and progress.
  - A second test compares, for every seeded card, the old `PlainText` with the new one. The only
    allowed difference is the display of a cloze `|`.
  - `check-migration` prints the offending notes and exits non-zero when one blocks the migration.
  - Run once with `TEST_PG_DSN`.

  *Verify:* `go test ./internal/store/... ./internal/notetype/... ./cmd/engram/...`.
- [ ] **M17-15 Server render and review path on the new engine** — switch every server path from
  `internal/cardtype` to `internal/notetype` and `internal/answer`, then delete the old packages.
  *Depends on:* M17-14.
  *Files:* `internal/web/review.go`, `review_api.go`, `notes.go` and `note_preview.go`;
  `internal/api/service.go`, `notes.go`, `review.go` and `card_types.go`; `internal/store/note.go`
  (search text from `PlainText` of the fields, skipping `exclude_from_search` fields);
  `internal/store/media_mapping.go` (references found in HTML: `src`, `[sound:]`, CSS `url()`);
  `go.mod` (drop `bluemonday`); deletion of `internal/cardtype` and `internal/render`.
  *Work:*
  - The card view returns `{html, css, body_class, side, media_base}` per side.
  - Writing a note regenerates its cards with M17-9.
  - Submitting an answer grades through M17-13.
  - The back side is rendered with the answer result.
  - goldmark remains only in the migration and in deck descriptions (M17-29).

  *Acceptance:*
  - The existing review, undo, bury, suspend and grading tests pass after being rewritten against
    note types.
  - Negative cases: an answer for a self-graded template is refused; a stale `expected_version`
    returns `409`.
  - `go list ./...` shows neither deleted package, and `grep -r bluemonday --include=*.go .` prints
    nothing.
  - Update `ARCHITECTURE.md` (packages, request path).

  *Verify:* `go test ./internal/web/... ./internal/api/... ./internal/store/... && go vet ./internal/...`.
- [ ] **M17-16 REST, MCP and schemas for note types** — the new payload shape and the note type
  endpoints.
  *Depends on:* M17-15.
  *Files:* `internal/api/` (routes and service methods); `internal/mcp/mcp.go`;
  `schema/note-import.schema.json`, `schema/note-bulk.schema.json` and `schema/README.md`;
  `schema/examples/`.
  *Work:*
  - A note is sent and received as `{"note_type": "<public id>", "fields": {"<field name>": "<HTML>"}}`.
    The server maps field names to field ids.
  - Note type list, get, create, update and delete under a deck. Update checks `version`, and a
    mismatch returns `409`.
  - The MCP tool `list_card_types` becomes `list_note_types`, scoped to a deck. Add `get_note_type`.
    `create_notes` and `update_note` take `note_type`. Tool descriptions state that field values are
    HTML.
  - `note-import.schema.json` describes `fields` as a map from field name to string.

  *Acceptance:*
  - `httptest` cases for each endpoint, including: a reader may not edit a note type; an unknown
    field name is rejected and names the field; a version conflict returns `409`.
  - The MCP tool list test is updated.
  - `uvx check-jsonschema` validates the updated examples.

  *Verify:* `go test ./internal/api/... ./internal/mcp/...` and the `check-jsonschema` commands in
  `schema/README.md`.
- [ ] **M17-17 Anki reviewer runtime in the frame** — the Anki-compatible review environment and the
  answer controls' front end.
  *Depends on:* M17-15.
  *Files:* `frontend/src/cardframe/`; `frontend/src/lib/views/ReviewView.svelte` (single-writer
  hotspot); the self-hosted MathJax assets.
  *Work:*
  - Body classes `card cardN`, plus `nightMode night_mode` in the dark theme.
  - Root classes computed from the user agent exactly as Anki's `ts/reviewer/browser_selector.ts`
    does, plus `isMac`, `isWin` or `isLin`.
  - Globals `$` and `jQuery` (dependency `jquery`. Reason: Anki's reviewer exposes them and shared
    templates call them), `getTypedAnswer()`, and `pycmd()` / `bridgeCommand()`. The two functions
    accept `ans`, `play:q:N` and `play:a:N` and ignore everything else.
  - MathJax `tex-chtml-full` with Anki's configuration: packages `noerrors` and `mathtools`, no
    `textmacros`, `\[ \]` as display math.
  - The front ends of the eight answer controls, which post `answer {value}`.
  - Minimal playback: the replay buttons and in-order playback, with engram's audio controls (play,
    pause, seek bar, ±5 s, time readout) attached to every `[sound:]`.
  - The choice options leave the parent DOM.

  *Acceptance:*
  - Vitest cases for the body and root classes for six user agents.
  - Vitest cases for each control's posted value.
  - The `{@html}` allowlist from M17-3 loses the choice-option entry.
  - A screenshot set from the `run` skill: one card of every migrated kind, front and back.

  *Verify:* `npm --prefix frontend run check && npm --prefix frontend test -- --run && npm --prefix frontend run test:client`.
- [ ] **M17-18 Rich-text field editor** — replace the Markdown editor with a sandboxed rich-text
  editor.
  *Depends on:* M17-16.
  *Files:* `frontend/src/lib/components/RichTextField.svelte` and `frontend/src/editframe/` (new);
  `NoteFieldsForm.svelte`; `NoteEditView.svelte` (single-writer hotspot); deletion of
  `MarkdownField.svelte` and `markdown-format.ts`; the locale catalogs (append a block with the prefix
  `editor.`).
  *Work:*
  - The editing area is a sandboxed iframe with the same isolation as the card frame. The toolbar is
    in the parent and sends commands by message.
  - Commands:
    - bold, italic, underline, superscript, subscript, text colour, highlight and clear formatting;
    - ordered and unordered lists, alignment and indent;
    - insert media by upload, paste or drop;
    - insert MathJax inline or block, and LaTeX;
    - cloze with a new number (`Ctrl+Shift+C`) or the same number (`Ctrl+Alt+Shift+C`);
    - a toggle to the HTML source view.
  - Pasting strips formatting unless `Shift` is held. This matches Anki's default; confirm the
    default against Anki `26.09.3` first.
  - The field options apply: sticky, collapsed, plain text, RTL, font and size; the description is
    the placeholder.

  *Acceptance:*
  - Client tests: each command produces the expected HTML; sticky keeps the value after "add";
    cloze numbering continues from the highest existing number; the HTML view round-trips unchanged
    markup.
  - The two locale catalogs keep identical key sets.

  *Verify:* `npm --prefix frontend run check && npm --prefix frontend test -- --run && npm --prefix frontend run test:client && go test ./internal/i18n/...`.
- [ ] **M17-19 HTML mail templates without sanitising** — administrators write mail in arbitrary HTML
  and CSS.
  *Depends on:* M17-18.
  *Files:* `internal/mail/template.go`, `style.go` and `defaults.go`; `internal/store/migrate.go` (a
  versioned migration for stored custom templates); `frontend/src/lib/views/admin/AdminMailTemplatesView.svelte`.
  *Work:* rendering becomes four steps, in this order:
  1. placeholders become salted tokens;
  2. tokens become the HTML-escaped variable values;
  3. the plain-text part is derived with `PlainText`;
  4. the fixed shell is applied (site name, footer, unsubscribe row).

  Nothing is sanitised. The automatic inline styling step is removed. The built-in templates and
  every stored custom template are converted from Markdown to HTML, with today's inline styles
  written into the HTML, so delivered mail looks the same. The admin page edits with the rich-text
  editor and previews in the frame.
  *Acceptance:*
  - The existing variable-escaping, missing-variable, leftover-placeholder and subject cases still
    pass.
  - A new case: a template containing `<style>` and a `<table>` is delivered unchanged.
  - A golden comparison of each built-in mail before and after the change shows identical HTML
    bodies.

  *Verify:* `go test ./internal/mail/... ./internal/store/...`; `npm --prefix frontend run test:client`.
- [ ] **M17-20 Deck package format 2** — export and import `.edeck` format 2 only, and regenerate the
  bundled decks.
  *Depends on:* M17-16.
  *Files:* `internal/store/package.go`, `package_import.go` and `package_document.go`;
  `schema/deck-package.schema.json`; `schema/README.md`; `schema/examples/deck-package.example.json`;
  `decks/`.
  *Work:*
  - The manifest `format_version` is `2`.
  - New entry `note_types.json`.
  - `notes.json` carries the note type reference, fields by field id (HTML strings), `source_url` and
    `extra`.
  - `cards.json` carries `template` and `deck_path`.
  - `media.json` gains `names` per sha.
  - A package whose `format_version` is not `2` is rejected. The error tells the user to import it
    with an older engram release and export it again.
  - Format 1 code is deleted.
  - The version policy in `schema/README.md` records the one-time exception.
  - The bundled `decks/*.edeck` are regenerated once, and nothing of this procedure is committed
    except the new files:
    1. import them with the last release before M17 into a temporary SQLite database;
    2. run the M17 migrations on it;
    3. export format 2.

  *Acceptance:*
  - A round-trip test (export, import, compare notes, cards, note types and media names).
  - A rejection test for format 1.
  - Archive-safety tests keep passing.
  - Every regenerated deck imports.

  *Verify:* `go test ./internal/store/... ./cmd/engram/...`; `uvx check-jsonschema` on the example.
- [ ] **M17-21 Conventions for the new model** — *parent task, with the user present; a subagent never
  edits these files.*
  *Depends on:* M17-15.
  *Files:* `AGENTS.md`; `README.md`; `README.zh.md`.
  *Work:*
  - `AGENTS.md` 2.3 item 7 becomes: "Note types are data. Answer controls are registered: adding one
    means adding one file and registering it."
  - A new invariant: "Unsanitised HTML is shown only inside the sandboxed frame."
  - Section 2.5's coverage targets become `internal/notetype` and `internal/answer`.
  - The READMEs describe HTML fields and the Anki audio/TTS syntax.

  *Acceptance:* `grep -n cardtype AGENTS.md README.md README.zh.md` prints nothing.
  *Verify:* the grep above.

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
  - the clash rename.

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
  *Depends on:* M17-23.
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
  *Depends on:* M17-18.
  *Files:* a new view under `frontend/src/lib/views/` (single-writer hotspot); `frontend/src/lib/router`;
  the locale catalogs (append a block with the prefix `notetype.`).
  *Work:*
  - Create a note type from a stock definition (Anki's six and engram's eight) or by copying one.
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
  *Depends on:* M17-27.
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
- One nullable column was added by AutoMigrate, `notes.search_text`, and the versioned migration
  `0011_backfill_note_search_text` fills it for existing notes. It is not part of any payload.
- With no deck named, `GET /api/v1/review/due` and `get_due_cards` leave out the decks the caller
  paused, and so do `due` in the stats summary, the unfiltered due forecast, the weekly digest and the
  reminder mail. Until a user pauses a deck, every result is the same as before.

| Goal | What shipped | Breaking change |
|---|---|---|
| Deck progress | The deck list, the home deck list and the deck detail header show how many of the deck's cards the user has studied and how many new cards are left, as `Studied N · Left M`. On the home deck list each deck gets a second line of numbers: today's new and review counts as badges, which turn grey at zero, and a progress bar beside the studied and remaining text, which turns to the success colour once no new card is left. The counts are per user, so a shared deck never shows another member's progress, and they ignore the daily limits. A suspended new card is not counted as left; a suspended studied card still counts as studied. | No — two fields added to a session-only response |
| Deck pause | A user pauses a deck for themselves from the deck list (a toggle beside delete or leave) or the deck detail header; the owner and every member, readers included, decide alone. A paused deck keeps a `Paused` badge on the deck list and disappears from the home deck list and the sidebar. It stays out of the default review queue, the home due count, the unfiltered forecast, the weekly digest and the reminder mail, while naming it (`/review?deck=…`, batch review, a stats deck filter) still studies or shows it. Progress is kept and cards keep falling due. Pausing and resuming write the audit actions `deck.pause` and `deck.resume`. | No — additive route, tool, table and field; nothing changes until a user pauses a deck |
| Note search on field text | Note search (`GET /api/v1/decks/:id/notes?q=`, `search_notes` and the card list) matches the text of the fields instead of their JSON encoding. A field key such as `front` no longer matches every note; `<`, `>`, `&`, quotes and text across a line break can be found; case folding of non-ASCII letters is the same on SQLite and PostgreSQL. A query is split on whitespace and every term must occur, in any order. | No — results change, the request and response shapes do not |
| Admin user search wildcards | `_` and `%` in the admin user search match only themselves instead of acting as LIKE wildcards. | No |
| Deck archive removal | The deck archive state is removed: it had no route, tool or page that set it, the sidebar's archived filter could never match, and pausing a deck already covers hiding one. The `archived_at` field leaves every deck response and the column leaves the database through a versioned migration. | Yes — HTTP API and MCP payload (`archived_at` removed) and database schema (column dropped by migration 0010) |
