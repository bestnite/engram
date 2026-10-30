# ARCHITECTURE.md — How engram is built

This file is a map of the current code: the processes, the packages, and how a request or a
review travels through them. It changes whenever the structure changes. The rules for changing
the repository live in `AGENTS.md`; the task backlog and the release ledger live in `ROADMAP.md`.

The code decides. If this file and the code disagree, the code is right and this file is stale:
fix this file in the same change that notices the disagreement.

---

## 1. Shape of the system

- **One Go binary.** `cmd/engram` is the only entry point. Its subcommands are `serve`,
  `schema`, `check-migration`, `export`, `import`, and `version`. Every dependency is constructed and wired by hand
  in `cmd/engram/main.go`; there is no dependency-injection framework.
- **One single-page frontend.** `frontend/` is a Svelte 5 application built by Vite into
  `frontend/dist`. `frontend/embed.go` embeds that directory into the binary with `go:embed`, and
  `internal/web` serves it. The build output is gitignored, so it must be built before the Go
  build (see `AGENTS.md` section 4).
- **Two databases, one model set.** GORM talks to PostgreSQL (the default deployment) or SQLite
  (single node and development). `AutoMigrate` covers additive changes; anything else is an
  explicit versioned migration in `internal/store/migrate.go`.
- **One subprocess.** FSRS parameter optimisation runs the Rust adapter in `tools/optimizer` as a
  child process, started by `internal/jobs`. The optimiser algorithm never enters the Go code.

## 2. Repository layout

```
engram/
├── AGENTS.md                 # rules for changing the repository
├── ARCHITECTURE.md           # this file: the current structure
├── ROADMAP.md                # task backlog and release ledger
├── CONTRIBUTING.md           # guide for external contributors
├── README.md / README.zh.md  # English is the primary document; Chinese is parallel
├── cmd/engram/               # the binary: subcommands and explicit wiring
├── internal/                 # all Go packages (section 3)
├── frontend/                 # Svelte 5 SPA, its tests, and embed.go
├── schema/                   # JSON Schemas for note import, bulk notes, and deck packages
├── decks/                    # bundled deck library
├── tools/optimizer/          # Rust FSRS optimiser adapter
├── tools/anki-oracle/        # Python oracle that generates the Anki reference fixtures
├── Dockerfile, Dockerfile.release, docker-compose.yaml
└── go.mod                    # Go module definition
```

There is no top-level `test/` directory. Integration tests live beside the code as
`internal/<package>/*_test.go`; frontend tests live in `frontend/src/tests/`.

## 3. Go packages

| Package | Responsibility |
|---|---|
| `internal/config` | Startup configuration. Precedence is environment variable, then the `settings` table, then the built-in default; every accessor can report which source won. |
| `internal/store` | GORM models (`AllModels` in `models.go`) and the concrete stores that wrap database access, including deck package import and export. The import entry point sniffs the archive: an `.apkg` is decoded by `internal/anki` and mapped by `anki_import.go` (deck, note types, notes, cards and media), while an `.edeck` uses the package format reader. `MediaStore.Put` is the single content-addressed media write (`<sha256[:2]>/<sha256>.<ext>`, temp file + rename, dedup-hit rules) used by both upload and import. `NoteStore` is the one note write path: it resolves the note type, converts the payload field map to field-id keyed HTML, derives `search_text` from the searchable fields' plain text, regenerates the note's cards, and rebuilds the media mapping. `migrate_markdown.go` holds the temporary goldmark step the note type migration needs. |
| `internal/answer` | The answer-control registry and one file per control: the input markup a card face shows at its type placeholder, the result breakdown on the back, the four field-value parses, and the grading rules with their `grade_detail_json` shape. A control declares the roles its answer spec binds and registers itself, so the registry needs no change when one is added. It also owns the score-to-rating mapping a preset stores. |
| `internal/anki` | Leaf reader for Anki's three `.apkg` layouts: legacy `collection.anki2`/`collection.anki21` (schema 11, note types and decks as JSON) and modern `collection.anki21b` (zstd collection and media table, schema 18, protobuf configurations decoded by field number). It enforces the deck package reader's entry, per-file and total caps, rejects unsafe entry names, and returns one in-memory model of note types, decks, notes, cards and media. It imports no internal package, so the store can map its model without a cycle. |
| `internal/notetype` | The note type data model, validation and the definitions of Anki's stock note types and engram's ten stock note types, plus the template engine that renders one side of a card (template syntax and special fields, the built-in filters, the cloze and image-occlusion markup), card generation (which cards a note requires and which stored cards are now empty), the audio/TTS, LaTeX and plain-text transforms, and type-in answer comparison. |
| `internal/reviewday` | Leaf package with the single definition of a user's review day: timezone resolution (unknown names fall back to UTC), the day cutoff hour, the review-day boundaries, and the `YYYY-MM-DD` day string written to `reviews.review_day`. Grading, burying, budgets, statistics, reminders and digests all read it. |
| `internal/schedule` | The go-fsrs wrapper: card state machine, queue builder, daily budgets, review submission, and undo. |
| `internal/auth` | Local accounts, password hashing, sessions, CSRF, TOTP, OIDC, API keys, roles, and deck access checks. |
| `internal/api` | `/api/v1` REST handlers, their authentication and rate-limit middleware, and the service layer (`service.go` and the methods on `API`) that REST and MCP share. |
| `internal/mcp` | The built-in MCP server at `/mcp` (streamable HTTP). It accepts API keys only, and every tool calls an `internal/api` service method. |
| `internal/web` | The gin router: security headers, session and locale middleware, the session-and-CSRF JSON endpoints the SPA uses, the admin API, media proxying, the SPA shell, and the not-found fallback. It mounts `internal/api` and `internal/mcp`. |
| `internal/markdown` | Leaf package holding the Markdown-to-HTML renderer for text that is stored as Markdown source, currently the deck description. It uses goldmark with the GFM extensions, hard line wraps and raw HTML passed through, and protects whole TeX formulas so they survive parsing for the frame's MathJax. The output is unsanitised: the sandboxed frame is the security boundary. |
| `internal/media` | Local media storage around `store.MediaStore.Put`: streaming and validating uploads, serving files, and reclaiming orphans. |
| `internal/mediatype` | Leaf package with the media classification (image, audio, video, font, script, style, other) and the mime-to-extension table shared by `internal/media` and `internal/store`. |
| `internal/texmath` | The single rule for locating TeX math, shared by `internal/mailstyle`, `internal/markdown` and the store's migration Markdown step. |
| `internal/i18n` | Server-side translation catalogs (`internal/i18n/locales/*.yaml`), request language resolution, and the localiser placed in the request context. |
| `internal/mail` | Mail templates (built-in HTML defaults and admin overrides share one renderer), the fixed inline-styled HTML shell, the outbox, SMTP transport, and the delivery worker with backoff retry. `Mailer` is the single send path: it checks SMTP and the recipient's preference, resolves the site name and template, adds the footer and the unsubscribe link, and enqueues. The web layer builds the process's one `Mailer` and the reminder and digest workers receive it from `cmd/engram`. Rendering is four steps: placeholders become salted tokens, tokens become HTML-escaped values, the plain-text part comes from `notetype.PlainText`, and the fixed shell is applied. Nothing is sanitised and no Markdown is parsed at send time: administrators write HTML/CSS, and the built-in bodies carry the inline styles. |
| `internal/mailstyle` | Leaf package holding the inline style map of a mail body and the one Markdown-to-HTML conversion used by the store's template migration. The built-in bodies and the migration read the same styles, so a converted custom template keeps the delivered look. |
| `internal/reminder` | Worker that emails users who have due cards. |
| `internal/digest` | Worker that sends the weekly digest email. |
| `internal/retention` | Worker that deletes expired sessions, used or expired action tokens, expired invites, and old login fingerprints. |
| `internal/worker` | The shared start, stop, and tick loop used by the periodic workers. |
| `internal/jobs` | Single-concurrency background jobs persisted in the `jobs` table; runs the optimiser subprocess with timeout and progress capture. |
| `internal/urlfetch` | Guarded HTTPS download used to import a deck package from a URL. |
| `internal/pgtest` | Test helper that gives each test its own PostgreSQL schema when `TEST_PG_DSN` is set. |

### Dependency direction

Imports between internal packages, as reported by `go list`:

```
cmd/engram → web, api, mcp, auth, config, digest, i18n, jobs, mail, media, reminder, retention, store
web        → api, mcp, auth, config, i18n, jobs, mail, media, mediatype, notetype, reminder, reviewday, schedule, store
mcp        → api, store
api        → auth, markdown, media, notetype, reviewday, schedule, store, urlfetch
digest     → i18n, mail, reminder, reviewday, schedule, store, worker
reminder   → i18n, mail, reviewday, schedule, store, worker
jobs       → schedule, store
mail       → config, mailstyle, notetype, store, worker
schedule   → reviewday, store
auth       → store
media      → mediatype, store
store      → anki, answer, mailstyle, mediatype, notetype, reviewday, texmath
markdown   → texmath
answer     → notetype
retention  → worker
anki, config, i18n, mediatype, notetype, reviewday, texmath, urlfetch, worker, pgtest → (no internal imports)
```

Regenerate it with
`go list -f '{{.ImportPath}}: {{join .Imports " "}}' ./internal/... ./cmd/...` when packages
change.

## 4. Request paths

- **Browser (SPA).** The browser authenticates with a session cookie. Writes carry a CSRF token
  bound to the session. Pages are client-side routes served by the SPA shell; data comes from JSON
  endpoints registered by `internal/web` and from `/api/v1`.
- **Card frame.** `GET /card-frame/<build-version>` serves the review iframe document
  (`internal/web/card_frame.go`). The version segment only busts the iframe cache and is otherwise
  ignored. The route replaces the site headers with the frame's own sandbox CSP, sends
  `Referrer-Policy: no-referrer`, and drops `X-Frame-Options`; the parent page embeds the document
  in a sandboxed iframe and communicates with it only through `postMessage`.
- **Editing frame.** `GET /edit-frame/<build-version>` serves the note editor's rich-text iframe
  document (`internal/web/edit_frame.go`). It carries the card frame's exact isolation and headers,
  because a field value is untrusted note content parsed by a sandboxed document; the parent page
  sends the field HTML and the toolbar commands with `postMessage` and receives `change`, `height`
  and upload requests.
- **Frame media.** `GET /fm/<token>/<name>` (`internal/web/frame_media.go`) serves media to the
  sandboxed frame, which has no session cookie. The token is an HMAC over `(user_id, deck_id,
  expiry)`; its key lives in the encrypted settings channel under `frame_media_key` and is
  created on first use. Each request checks the signature, the expiry, the token user's current
  status, the user's current read access to the token's deck and a `media_names` row for that deck
  and name; every failure is the same bare 404. Tokens live six hours. A field stores media by
  deck-scoped file name (`<sha256>.<ext>`), which is what the frame resolves against `media_base`
  (`/fm/<token>/`), the document's `<base href>`. Every stored medium is served with
  `Content-Security-Policy: sandbox` and as an attachment unless it is image, audio or video. A
  card-face response to a request without the `X-Engram-Frame: 1` header rewrites those names into
  the absolute `/media/<sha256>` URLs the SPA needs, because the SPA has no base element. The
  self-hosted MathJax script is referenced by its content-hashed
  `/static/v/<hash>/js/mathjax/tex-chtml-full.js` URL, and its CHTML web fonts are served from the
  stable `/static/mathjax/` path because a font request cannot carry the script's hash; that tree is
  public and sends `Access-Control-Allow-Origin: *`, because the sandboxed frame has an opaque
  origin.
- **Card view.** A card-face response carries one `{html, css, body_class, side, media_base, av_tags, autoplay}`
  object per side (`front` and `back`), plus the card's template `ordinal`. The HTML is the note
  type's template rendered against the note's field values. The back side substitutes the answer
  control's breakdown for the `[[type:...]]` marker once a graded answer result exists. A
  frame-output request (`X-Engram-Frame: 1`) substitutes the control's `RenderFront` input markup
  for the same marker on the front, so the frame draws the control and posts its value; every
  other request strips the marker. The frame-output HTML replaces every `[sound:]` and
  `[anki:tts]` tag with an `[anki:play:<side>:<n>]` marker and carries the tag list in `av_tags`;
  the answer face is rendered with the already-extracted question in place of `{{FrontSide}}`, so
  its tag list separates the question's tags from the tags the answer adds. `autoplay` is the
  deck's setting, read by the frame to start a face's own tags. Until the SPA reads the two
  objects, `front_html`/`back_html` repeat their HTML and `options_html`, `option_order` and
  `blanks` remain in the payload, though the review page no longer renders controls from them. A due card also carries `answer_control`
  (the template's answer control name, null when the template has none), `machine_graded` (the rule
  the submit path applies) and `field_order` (the note type's field ids in config order). The
  review page chooses the self-rated, essay or machine-graded flow from the first two, so a note
  type that matches no stock kind answers like any other, and its plain-text fallback lists the
  field values in `field_order` when a face fails to load. Those flat fields, and the card-kind
  vocabulary the note response still carries (`kind` as a transitional alias, the description
  catalog in `internal/api/card_catalog.go`), stay while the SPA note form still reads that
  catalog; removing them requires the form to read note types instead of card kinds. A note write
  already uses the permanent payload: `note_type` plus `fields` keyed by field name with HTML
  values.
- **Note write and review.** `NoteStore.Save` resolves the note type (deck and note type id),
  rejects a field name the config does not declare and names it, converts the payload values to
  HTML keyed by field id, normalises `/media/<sha256>` and `[sound:<sha256>]` into deck media
  names `<sha256>.<ext>` and registers them in `media_names`, recomputes `search_text` from the
  plain text of every field not marked `exclude_from_search`, regenerates the cards with
  `notetype.GenerateCards` (existing cards keep their rows and progress; a card the note no longer
  generates is soft-deleted), and rebuilds the `media_notes` mapping from the `src`, `href`,
  `[sound:]` and CSS `url()` references of the field HTML.
  Submitting a review resolves the card's template answer spec: a grading control decodes the
  submitted answer, grades it, and maps its score through the preset's mapping; a template with no
  spec, or the self-assessed short answer control, takes the four-grade self-assessment. A
  self-graded template refuses a machine answer, and a stale `expected_version` is `409`.
- **REST clients.** `/api/v1` (`internal/api`) accepts either the session channel or a bearer API
  key. An API key carries scopes, and each route declares the scope it requires. A note is sent and
  received as `{"note_type": "<public id>", "fields": {"<field name>": "<HTML>"}}`; the server
  resolves the note type and maps field names to field ids. Note types are managed per deck at
  `GET|POST /decks/:id/note-types` and `GET|PATCH|DELETE /decks/:id/note-types/:type_id`; an update
  names the version it read and a stale version is `409`, and a delete is refused while a note still
  uses the type. A reader may list and read note types but only an editor may change them. A create
  may send `stock_id` instead of a config; `GET /note-type-stocks` lists the source-prefixed ids of
  Anki's six and engram's ten stock definitions, and the server builds the config from the chosen
  definition. `GET /answer-controls` lists the registered controls with their role names and parses,
  so a client builds an answer spec without a second hard-coded list. `kind` remains a transitional
  alias for `note_type` on a note write while the SPA editor still sends the card kind.
- **MCP clients.** `/mcp` (`internal/mcp`) accepts API keys only and re-resolves the identity on
  every request. `list_note_types` and `get_note_type` expose a deck's note types; `create_notes`
  and `update_note` take `note_type` and send field values as an HTML map.
- **One business layer.** A REST handler and the MCP tool for the same operation call the same
  `internal/api` service method, so validation and results cannot drift between transports.
  Service errors (`ServiceError`) carry an HTTP status, a stable English `code`, and a message.

## 5. Data model

- **Content and progress are separate.** `notes` and `cards` hold content. Per-user scheduling
  state lives in `card_states`, keyed `(card_id, user_id)`. A shared deck therefore never mixes
  two users' progress.
- **Review log.** `reviews` records every rating with the full before-and-after state. It is the
  input to parameter optimisation. Undoing a review deletes that one row and writes a
  `review.undo` audit row.
- **Decks and sharing.** A deck has an owner. Grants give other users a role on the deck. Each
  member chooses their own preset and study settings for a shared deck.
- **Deck pause.** A user pauses a deck for themselves in `deck_pauses`, keyed `(deck_id, user_id)`;
  the owner and every member decide alone, and no `decks` column is involved. Visibility and
  the default study scope are two predicates in `internal/store`: `visibleDeckIDsQuery` (owned or
  granted) decides what a user can see, and `activeDeckIDsQuery` (visible minus paused) decides what
  is studied when no deck is named — the whole-library queue, the "due now" number, and the unfiltered
  due forecast.
- **Presets.** A preset holds the FSRS weights, desired retention, learning and relearning
  steps, and fuzz switch that the scheduler for a deck uses.
- **Note types.** `note_types` holds a deck-scoped note type: `name`, `config_json` and a `version`
  optimistic lock. `config_json` is TEXT holding `internal/notetype.Config` (kind, fields,
  templates, CSS, LaTeX and Anki origin); the store marshals and unmarshals it, so both databases
  see the same string. The name and config are validated before a write, and an update names the
  version it read and increments it, so a stale writer gets `ErrVersionConflict` instead of
  overwriting a newer edit. A note type still used by a note that is not soft-deleted cannot be
  deleted (`ErrNoteTypeInUse`).
- **Notes and note types.** `notes` carries `note_type_id`, `fields_json`, `source_url` and
  `extra_json`, and no kind column: every read and write path works through the note type.
  `fields_json` maps a field id to that field's HTML value, and `search_text` is derived from the
  plain text of the fields not marked `exclude_from_search`. Migration 0012 creates one engram
  stock note type per (deck, kind) and rewrites every note's field values from Markdown to HTML,
  moving `source_url` and `extra` to their columns; migration 0013 makes `note_type_id` NOT NULL
  and drops `notes.kind`, and migration 0016 repeats the NOT NULL change for a database whose
  column AutoMigrate had loosened again. The model declares `note_type_id` NOT NULL; the first
  additive pass of `Sync` migrates a database below 0013 through a nullable copy of the notes
  shape, and the write path rejects a note that names no type.
  `cmd/engram check-migration` runs the same read-only pre-flight check migration 0012
  enforces and reports blocking and warning notes without writing. `source_url` and `extra_json`
  are nullable TEXT.
- **Field values are HTML.** A field value is an HTML string; nothing else is stored. The note
  write path already takes that shape: `fields` maps a field name to an HTML string. The
  transitional `kind` alias still accepts the old typed shapes on a write (a string array for
  options, a boolean, a number), but every note response returns each field's stored HTML string
  unchanged, keyed by field id, so an editor round-trips content without interpreting it. The deck
  package format 2 writes the permanent shape only: `note_types.json` plus notes whose `fields`
  map a field id to HTML.
- **Cards and subdecks.** `cards.deck_path` is a nullable TEXT path relative to the imported root
  deck (for example `Lesson 1::Verbs`); a card without it sits directly in the note's deck. A
  subdeck is stored only as this path and never as a deck row.
- **Deck-scoped media names.** `media_names` maps a deck-scoped file name to the content-addressed
  `sha256`, so a field or template can refer to media by name the way Anki does. `(deck_id, name)`
  is the primary key, which puts the uniqueness in the schema, and names are stored
  NFC-normalised. An upsert (`clause.OnConflict`) repoints a name to new content instead of adding
  a row. A field or template refers to media in four forms: an `src` attribute, an `href`
  attribute, a `[sound:name]` tag, and a CSS `url(name)`. A name that already carries the sha
  (`<sha256>.<ext>`, the form the migration writes) needs no lookup; any other name resolves
  through `media_names` for the note's deck.
- **Note type media references.** `media_note_types` records which `_`-prefixed media names a note
  type's templates and CSS refer to, keyed by `(note_type_id, media_name)`. The set is derived
  data, replaced as a whole, and exists for media garbage collection; the read side no longer
  consults it. It is written on create, update and package import, cleared when the type is
  deleted, and backfilled by migration 0015.
- **Audit.** Security-relevant and settings-changing actions append rows to the audit log; the
  action names are constants in `internal/store/audit.go`.

## 6. Scheduling

- `internal/schedule` builds a go-fsrs scheduler per deck from the member's effective preset.
- **Card predicates.** `internal/store/card_scope.go` is the only definition of which cards a user can
  study (live and not suspended), which are due (learning or review state with `due_at` reached) and
  which are new (no state row, or state new; a buried new card is still new, never due). The queue,
  the deck list counts, deck progress, the due forecast, the health page and the reminder prefilter
  all build on it.
- **Due now.** `QueueBuilder.DueNow` sums the review half of `DeckCounts` over the active decks. It is
  the only "due" number shown to a user outside a deck: the home summary (REST and MCP), the review
  reminder mail and the weekly digest. The stats page's per-deck table shows `DeckCounts` too, so every
  page agrees with what `/review` serves.
- **Queue.** The queue builder selects due reviews and new cards for one or more decks and a tag
  filter, for the user's current review day. With no deck named it covers the user's active decks
  (`DeckStore.ActiveIDs`), so a paused deck only enters the queue when it is named.
- **Daily budgets.** `deckBudget` in `internal/schedule/queue.go` is the only implementation of
  the per-deck daily new and review limits. The review queue, the deck list counts, and the deck
  settings page all call it, so the three numbers always agree.
- **Daily limits are goals, not caps.** After the daily new-card goal is met, a user can add
  another batch of new cards to one deck for the current review day. The extra amount is stored
  per deck, per user, per review day in `deck_daily_extras`, and `deckBudget` adds it to the new
  card limit. It expires with the review day. Review days themselves come from `internal/reviewday`. REST (`/api/v1/decks/:id/extra-new`) and MCP
  (`add_extra_new_cards`) share one service method.
- **Deck progress.** `DeckProgress` in `internal/schedule/deck_progress.go` counts, per deck and
  per user, the cards studied and the new cards left, independent of the daily budgets. The new
  cards left are `store.UserNewCards`, the queue's own definition. A suspended new card is not counted as
  left; a suspended studied card still counts as studied. The SPA reads both numbers from the
  session-only `/api/v1/decks/queue-counts`, next to the daily budget counts.
- **Interval preview.** The review screen shows the next interval for each rating. The preview
  uses a scheduler with fuzz disabled, because go-fsrs seeds its fuzz with the current
  millisecond and a fuzzed preview could never match the submitted result. With fuzz enabled on
  the preset, the real interval can therefore differ slightly from the preview.
- **Submission.** Submitting a review checks `expected_version`, returns `409` on mismatch, and
  writes the state update and the review row in one transaction.

## 7. Background work

`cmd/engram serve` starts these alongside the HTTP server and stops them on shutdown:

- the retention worker (`internal/retention`), always;
- the mail delivery worker (`internal/mail`) and the reminder and digest workers, when the mail
  outbox is wired; the mail outbox and the reminder, digest, and retention workers share
  `internal/worker`;
- the jobs runner (`internal/jobs`), which runs at most one optimisation job at a time and
  answers `409` when another job is already unfinished.

## 8. Frontend

- `frontend/src/lib/views/` holds one view per page, `frontend/src/lib/components/` the shared
  components, and `frontend/src/lib/api/` the typed client for the JSON endpoints.
- Vite has three entries: `frontend/index.html` (the SPA shell), `frontend/card-frame.html`
  (the card frame document) and `frontend/edit-frame.html` (the rich-text editing frame).
  `frontend/src/cardframe/` is the review frame runtime: it holds no Svelte component
  and reads no cookie; it writes the content sent by the parent page into `#qa`, re-executes the content
  scripts, typesets the face, attaches the shortcode controls to it, and posts height, keys, answers
  and links back to the parent. Its language is the `<html lang>` the server resolved for the request.
  It also supplies the Anki reviewer environment a shared template expects: the body classes
  `card cardN` and `nightMode night_mode`, the browser and platform classes ported from Anki's
  `browser_selector.ts`, the `$`, `jQuery`, `getTypedAnswer`, `pycmd` and `bridgeCommand` globals, the
  answer controls' posted values, and engram's audio controls on every play marker. The frame
  turns each marker into the same audio, video or speech source the shortcodes produce, autoplays
  only the face's own tags when the deck allows it, and replays the face on `r` or `F5`.
- `frontend/src/editframe/` is the editing frame runtime. It parses the field HTML only inside the
  sandboxed document, applies the toolbar's range commands (`commands.ts`), handles paste and drop,
  switches between the rich surface, the plain-text textarea and the HTML source textarea, and posts
  `change`, `height` and upload requests back to the parent.
- `frontend/src/lib/components/CardFrame.svelte` is the only way a view embeds a card face: it owns
  the sandboxed iframe, sends `show` (with the card ordinal, side and media base) and `theme`, and
  forwards the frame's key presses and `answer {value}` messages to the view.
  `frontend/src/lib/components/RichTextField.svelte` is the only way a note field is edited: it owns
  the editing iframe and the toolbar, sends `load` and `command`, and writes the frame's `change`
  back into the form. Card-face and field HTML therefore never enter the parent DOM.
  `frontend/src/lib/components/MailPreviewFrame.svelte` is the admin mail-template preview: it puts
  the complete server-rendered mail document into a sandboxed iframe with no script permission, so
  administrator-written HTML and CSS are only ever rendered inside a sandbox. The SPA's remaining
  HTML sinks are the per-item grading breakdown and the option labels `RadioGroup` renders, and
  `frontend/src/tests/html-sink-allowlist.test.ts` lists each of them with its reason.
- `frontend/src/lib/format.ts` is the only place numbers, percentages, byte counts, dates and raw field
  values become text; components pass the current locale.
- UI text comes from `frontend/src/lib/i18n/locales/{zh-CN,en}.ts`. Text produced by the
  server (emails, server-side messages) comes from `internal/i18n/locales/{zh-CN,en}.yaml`.
- Tests run in two vitest suites: an SSR suite and a happy-dom suite for `*.client.ts` files.
  `svelte-check` type-checks the components.
