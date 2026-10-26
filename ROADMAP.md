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

### M11 — Card content shortcodes

Card content is Markdown plus TeX rendered server-side by `internal/render`. Markdown cannot
express inline audio, speech or ruby annotation, and the content allowlist deliberately forbids
`style`, `data-*` and every script-capable element, so raw HTML is not an option either. This
milestone adds one fixed set of single-line shortcodes, expanded at render time, plus the browser
side that turns them into controls. Adding further shortcodes means adding a row to that table,
not widening the allowlist.

The agreed grammar, which every shortcode in this milestone follows:

- one line, no nesting: `{{name:body|key=value|...}}`, and the body ends at the first `}}`;
- a shortcode never fails a render: an unknown name, a malformed parameter, or a body that itself
  contains `{{` is emitted as its source text, and the review flow never learns about it;
- the expanded element carries data in named attributes and no behaviour: the server emits no
  `<audio>`, no `<button>` and no event attribute, so the allowlist grows by exactly the
  attributes a shortcode needs;
- the visible body always stays in the output, so a client without JavaScript, without a speech
  engine, or on a page whose media it may not read shows text instead of nothing;
- the mail renderer turns every shortcode into plain text, because a mail body shares the same
  pipeline and has no controls.

Rejected, with the reason recorded so it is not re-litigated:

- `style`, `size`, `color`: the allowlist forbids the `style` attribute on purpose, and one
  shortcode emitting it would make every other constraint worthless;
- conditionals, loops, variables, template expressions: a requirement that needs an expression is
  a card type, and card types are registered;
- cross-note references such as `{{card:<id>}}`: rendering takes one string and stays a pure
  function of it, or the export, mail and share paths inherit a database read;
- `video`: the media allowlist holds no video format;
- server-side reading generation for ruby: it needs a dictionary and disambiguation, which is
  another system rather than a shortcode;
- `kbd`, `mark`, `ipa`: inline code and emphasis already express them.

Deferred, not part of this milestone: `{{record:<seconds>}}`, which records and plays back the
learner's own voice through `MediaRecorder`, and `{{timer:<seconds>}}`, a countdown for a spoken
answer. Both are front-end only and need no server change; they wait until the first three
shortcodes have been used on a real deck.

- [ ] **M11-1 Shortcode layer in `internal/render` with `tts`, `audio` and `ruby`** — add a
  goldmark inline parser and one fixed shortcode table in `internal/render` (a new file beside
  `render.go`, sharing the existing pipeline). Syntax is single-line, non-nesting,
  `{{name:body|key=value|...}}`.
  - `{{tts:こんにちは|lang=ja-JP|rate=0.9}}` → `<span class="engram-tts" data-tts-text="…"
    data-tts-lang="…" data-tts-rate="…">こんにちは</span>`, the body staying as visible text.
  - `{{audio:/media/<sha256>|start=12.5|end=15|rate=0.75|loop}}` → `<span class="engram-audio"
    data-audio-src="…" …>…</span>`, the same visible-text rule.
  - `{{ruby:漢字|かんじ}}` → `<ruby>漢字<rt>かんじ</rt></ruby>`.
  - An unknown name, a malformed parameter or a body containing `{{` emits the source text
    unchanged: a shortcode never makes rendering fail.
  - The server emits no `<audio>`, no `<button>` and no event attribute; the controls come from
    M11-2.
  - Allowlist changes are exactly: the elements `ruby`, `rt`, `rp`, and the named `data-tts-*` /
    `data-audio-*` attributes on `span`, each with a value regex. A general `data-*`, `style`, or
    any other attribute stays forbidden.
  - `RenderMailMarkdown` renders each shortcode as plain text: `tts` and `audio` as their body,
    `ruby` as `漢字(かんじ)`.
  - Structural change: `ARCHITECTURE.md` gains the shortcode layer and its contract in the same
    commit.

  *Acceptance:* table-driven tests in `internal/render` prove each expansion, that a shortcode
  inside a code span or a fenced block stays literal, that an unknown or malformed shortcode
  returns its source verbatim, that a body carrying `"` or `<` cannot escape its attribute and
  cannot introduce an attribute outside the allowlist, and that the mail renderer produces plain
  text for all three. The existing `TestRenderMarkdown*` cases still pass unchanged. Evidence is
  the raw output of `go test ./internal/render/... && go vet ./internal/render/...`.
- [ ] **M11-2 Review, preview and share pages turn shortcodes into controls** — add one frontend
  enhancement entry point in the pattern of `frontend/src/lib/mathjax/index.ts`: it takes the
  HTML container already sanitized by the server and binds `[data-tts-*]` and `[data-audio-*]`
  through event delegation, is idempotent when the same container is enhanced twice, and never
  reads raw card text.
  - `tts` speaks `data-tts-text` with `data-tts-lang` and `data-tts-rate` through the Web Speech
    API, always from a click (no autoplay). Without `speechSynthesis`, or with no voice for that
    language, the control is absent and the text stays visible.
  - `audio` plays `data-audio-src` between `start` and `end`, honours `rate` and `loop`, seeks in
    the same click. A failed load hides the control and keeps the text: a session that may not
    read the bytes gets 404 from `/media` (`internal/web/media.go:267`), which is the limitation
    images already have.
  - Call it wherever `typeset(...)` already runs: `ReviewView.svelte`, `NoteEditView.svelte`
    (preview only) and `ShareBrowseView.svelte`. If M12-1 has already landed, `ShareBrowseView`
    no longer exists and that third call site simply does not apply.
  - New control labels come from both catalogs under a `shortcode.` prefix appended at the end
    of each file, with identical key sets; nothing is hardcoded.
  - Depends on M11-1's DOM contract: do not start before M11-1 lands.

  *Acceptance:* jsdom tests with stubbed `speechSynthesis` and media playback prove that each
  call site enhances the container it typesets, that a second enhancement of the same DOM binds
  nothing twice, that a missing speech API leaves only the visible text, and that a media load
  error removes the control rather than showing a broken player. Evidence is the raw output of
  the frontend test run and `npm --prefix frontend run build`.

- [ ] **M11-3 Seekable progress control for the audio shortcode** — the audio control in
  `frontend/src/lib/shortcodes/index.ts` is a play/stop button only, so a learner cannot see the
  position or jump inside the played segment. Add one progress control beside the button for
  every `span.engram-audio` at every call site (`ReviewView.svelte` and the preview in
  `NoteEditView.svelte`), without changing the server contract: `data-audio-src`,
  `data-audio-start`, `data-audio-end`, `data-audio-rate` and `data-audio-loop` stay exactly as
  `internal/render/shortcode.go` emits them.
  - The track spans the played segment. With `start` and `end`, the filled ratio is
    `(currentTime - start) / (end - start)`, clamped to 0–1. Without `end`, it spans
    `start … duration`; without both, `0 … duration` once metadata has loaded.
  - Click, drag and keyboard seek inside the segment: Left/Right move by 5% of its length, and
    Home/End go to its edges. Seeking while playing continues playback from the new position.
  - `timeupdate` moves the fill, `ended` resets it to the segment start, and `loop` restarts at
    `start`. Enhancing the same container twice adds no second track or listener.
  - A media load error removes the button and the track together and restores the visible path,
    as the button-only failure path does today.
  - The track is a native `<input type="range">` or an element with `role="slider"`. Its label
    and value text come from both catalogs under a `shortcode.` prefix appended at the end of
    each file, with identical key sets. Nothing is hardcoded and the module still reads only the
    server's data attributes.

  *Acceptance:* happy-dom tests with stubbed media playback prove the ratio with and without
  `start`/`end`, that a click and each keyboard key set `currentTime` inside the segment, that
  `ended` and `loop` reset or restart at the right position, that a second enhancement adds no
  second track or listener, and that a load error removes the whole control. Evidence is the raw
  output of the frontend test run and `npm --prefix frontend run build`.

  *Order:* land this task before M11-4 and M11-5. All three modify
  `frontend/src/lib/shortcodes/index.ts`, and only one may be edited at a time.
- [ ] **M11-4 Optional `hide` flag for `tts` to suppress the body text** — `{{tts:…}}` always
  shows the body. Add one bare flag to the `tts` entry in the fixed table of
  `internal/render/shortcode.go`:
  - `{{tts:こんにちは|lang=ja-JP|hide}}` emits the existing span plus `data-tts-hide="1"`. The
    allowlist in `internal/render/render.go` allows exactly that attribute on `span` with the
    value `1`; no general `data-*` is opened.
  - `hide` is a bare flag like `loop`. `hide=1`, a `hide` flag on `audio` or `ruby`, and any
    other placement stay literal source text.
  - The body remains in the HTML, so the no-JavaScript and no-speech paths still show it. The
    client hides it only after it has created a working speech control; without
    `speechSynthesis`, or with no voice for the language, the body stays visible. Hiding is
    presentational: keep the text in the DOM for assistive technology (a visually hidden
    wrapper satisfies this) and restore it when the control is absent or removed.
  - `RenderMailMarkdown` is unchanged: a mail renders the body as plain text, because a mail
    has no speech control and hiding there would lose content.
  - The insertion panel (`ShortcodeInsert.svelte`, `shortcode-source.ts`) gains a hide-body
    checkbox for `tts` that appends `|hide`; the builder and validator accept the flag for
    `tts` only and keep rejecting it elsewhere.
  - The help page (`ShortcodesHelpView.svelte` and both catalogs) documents the flag in the
    `tts` parameter table and shows it in the example. The page stays self-contained and
    carries no design rationale.

  *Acceptance:* table-driven `internal/render` tests prove the expansion carries
  `data-tts-hide="1"`, that `hide=1` and `hide` on `audio`/`ruby` stay literal, and that the
  mail renderer still emits the body. Frontend tests prove the body is hidden only after a
  working control exists, remains visible without `speechSynthesis` or a matching voice, is
  restored when the control is removed, and that the panel emits `{{tts:…|hide}}` only from the
  checkbox. Evidence is the raw output of
  `go test ./internal/render/... && go vet ./internal/render/...`, the frontend test run and
  `npm --prefix frontend run build`.

  *Order:* after M11-3, because both modify `frontend/src/lib/shortcodes/index.ts`.
- [ ] **M11-5 Keep the shortcode controls inside their line box** — the `tts` and `audio`
  controls draw a 28px circle (`size-7` and a negative block margin in
  `frontend/src/lib/shortcodes/index.ts`) inside card text whose line box is smaller, so on a
  multi-line card the button paints over the line above or below. Make the control fit its line
  while keeping a usable pointer target:
  - Size the visible circle from the current font size and align it on the text centre line.
    Remove the negative block margin. The line box may become taller on a line that contains a
    control; the rule is that the control never paints outside its own line box and never
    overlaps the previous or next line's text.
  - The hit area may exceed the visible circle only within the same line box; it must not
    intercept a click in an adjacent line.
  - Apply the same metrics to `tts`, `audio` and the audio progress track from M11-3, in the
    review page and the editor preview.
  - Autoplay, idempotence, fallback and one-at-a-time behaviour are unchanged. Catalog text
    changes only if the accessible names need to mention the track; any new key is appended at
    the end of both files with identical key sets.

  *Acceptance:* a browser measurement at 16px card text and at 1.25× text size on a multi-line
  paragraph shows that every control rectangle stays inside its line box, that no control
  rectangle intersects the text rectangle of the previous or next line, and that a click one
  line above and below the control does not reach the control. Keyboard focus stays visible and
  the pointer target is at least 40×40px. Evidence is the captured geometry, screenshots at both
  sizes, the frontend test run and `npm --prefix frontend run build`.

  *Order:* last of the three; depends on M11-3's progress markup and M11-4's `tts` changes.

### M12 — Share link removal

The owner has confirmed that no instance uses this feature, and every job it does is served by a
better path: `POST /decks/:id/export` writes a self-contained `.edeck` that can inline media
(`IncludeMedia`, `internal/store/package.go:97`), and sharing inside one instance already runs on
`deck_grants` plus `share_invites`. A share link cannot serve a language deck at all, because an
anonymous visitor can never read `/media`. Removing it also deletes the third branch of the media
read rule, which is where the real simplification lies: three allow conditions become two, and a
table and a grant concept disappear with them. Invites, grants and the receiving policy are a
different feature and stay.

- [ ] **M12-1 Remove share links end to end** — delete the anonymous read-only link transport, its
  management endpoints, its two tables and the media authorization branch it fed. What stays:
  `deck_grants`, `share_invites`, the accepting policy (`/api/v1/settings/share-policy`), the
  sharing page at `/decks/:id/sharing`, the grant endpoints, and the `deck_share_invites`
  retention sweep.
  - Routes removed: `GET /s/:token`; `GET /api/v1/share/:token`;
    `POST /api/v1/share/:token/unlock`; `POST /api/v1/share/:token/join`;
    `POST /api/v1/decks/:id/sharing/links`; `DELETE /api/v1/decks/:id/sharing/links`; and
    `DELETE /api/v1/decks/:id/sharing/links/revoke/:digest`.
  - Files deleted whole: `internal/web/publicshare.go`, `internal/web/share_api.go`,
    `internal/store/sharelink.go`, `internal/store/share_session.go`, and the tests that exist
    only for them. Removed in place: the link handlers and link fields of
    `internal/web/sharing_api.go`'s response, the link routes and the `ShareLinks` dependency and
    field in `internal/web/server.go` and `internal/web/sharing.go`, the `ShareLink` and
    `ShareSessionDeck` models and their `AllModels()` entries, and the frontend
    `ShareBrowseView.svelte`, its client-router entry, the `getShare` / `unlockShare` /
    `joinSharedDeck` client methods, the `ShareResponse` / `JoinShareResponse` types, and every
    language-pack key used only by them.
  - Media read rule: drop the share-session branch in `internal/store/media_mapping.go`
    (`mediaReadableByUser`, `shareSessionDeckIDsQuery`) and the `sessionID` parameter that exists
    only for it in `MediaAccessibleToUser` (`internal/store/media_access.go`) and in its caller
    `internal/web/media.go`. Update the doc comments that enumerate the three conditions down to
    two; a stale comment describing a branch that no longer exists is a defect.
  - Migration: append `0008_drop_share_link_tables` to `BuiltinMigrations` in
    `internal/store/migrate.go`, dropping `share_links` and `share_session_decks` on both drivers.
    `AutoMigrate` is additive only, so a drop must be an explicit versioned migration.
  - Audit rows: keep the `share_link_*` action constants and their labels. Historical audit rows
    still reference them, and deleting the constants would render those rows unreadable.
  - `ARCHITECTURE.md` loses the link transport, the two tables and the third read condition in the
    same commit.

  *Acceptance:* `grep -rn` over the repository finds no remaining reference to `share_links`,
    `share_session_decks`, `ShareLink`, `ShareSessionDeck`, `resolveShareLink`, `shareUnlock`,
    `shareJoin`, `recordShareGrant`, or `share-browse`, except the retained audit constants and
    their labels. A test on a fresh database shows neither table exists, and the same test on a
    database that already holds rows for both tables shows the migration drops them and leaves
    `deck_grants`, `deck_share_invites` and the rest untouched. The media authorization tests that
    covered the removed branch are deleted rather than weakened, and a positive case proves that a
    deck member still reads the deck's media while a stranger still cannot. Evidence is the raw
    output of `go test ./internal/web/... ./internal/store/... ./internal/auth/... &&
    go vet ./internal/web/... ./internal/store/...`, the frontend test run,
    `npm --prefix frontend run build`, and the grep above. If `TEST_PG_DSN` was unset, say so
    explicitly instead of implying the PostgreSQL path of the migration was exercised.

  Removal is breaking: seven REST routes disappear and two tables are dropped. The release that
  carries it must say so in the ledger, and the commit message must name the removed routes.

### M14 — Note editor input layout

- [ ] **M14-1 Put insertion tools on each field and collapse the preview on narrow screens** — the
  editor keeps one page-level insertion block (field selector, upload, media library, shortcode
  panel) below the card fields, so on a phone the author scrolls between `front`/`back` and the
  tools for every insertion. Move the tools to the field they write into, and make the preview
  an on-demand drawer below the desktop breakpoint.
  - Every field whose control is `text` or `textarea` in `NoteFieldsForm.svelte` gets a compact
    toolbar in its label row with two icon actions: image (opens a dialog with a dropzone and
    the media library filtered to `image/`) and shortcode (opens the existing composer in a
    dialog). Both write into that field at its caret. A `lines` field stores an array and gets
    no toolbar: an inline insertion cannot address one element of it.
  - Remove the page-level field selector (`media.field`), the page-level `Dropzone`, the
    page-level `MediaPicker`, and the standalone `ShortcodeInsert` block from
    `NoteEditView.svelte`. The dialog reuses `Dialog.svelte` (focus moves in, Esc closes, focus
    returns to the trigger), `MediaPicker` with its mime filter, and `ShortcodeInsert`; no
    second field selector is introduced, because the toolbar's field is the target.
  - The image dialog accepts image files only. The composer's audio tab gains an upload action
    that reuses `apiClient.uploadDeckMedia` and fills the path field, so audio uploads keep
    working after the page-level upload disappears; an audio upload never produces the image
    form.
  - At `lg` and wider the two columns and the sticky preview stay as they are. Below `lg` the
    preview becomes a drawer opened by a "Preview" button in the page header, closed by
    default, closed by Esc or its close control, live while open, and opened without losing the
    focused field, its caret, or unsaved text.
  - All new labels come from both catalogs, appended at the end of each file with identical key
    sets. Delete catalog keys that no longer have a reference (`media.field` and any upload or
    heading key used only by the removed section) after confirming no other view reads them.
  - No server, API or shortcode-contract change; the rendered HTML the editor previews is
    unchanged.

  *Acceptance:* at 390px, with the first text field focused, its image and shortcode buttons are
  visible in that field's label row without scrolling, and the page has no page-level insertion
  section. Selecting a library image inserts `![](<url>)` at that field's caret; choosing an
  audio file from the composer's audio tab inserts `{{audio:<url>}}`; the composer still builds
  all three forms. A `lines` field shows no toolbar. The preview drawer opens from its button,
  reflects edits made while open, closes by Esc and by its close control, and returns focus to
  its trigger. At 1280px the two-column layout and the sticky preview are unchanged. Evidence
  is the raw frontend test run, `npm --prefix frontend run build`, and screenshots with the
  measured positions at 390, 900 and 1280px.

  *Order:* last; after M11-3, M11-4 and M11-5, whose control markup and composer props this task
  consumes.

---

## 2. Release ledger

### 2.1 Completed since v0.1.10 (2026-10-20)

**The machine surfaces are compatible with v0.1.10, with one tightened request check listed below.**

- No REST route was added, removed or renamed, and no MCP tool was removed or renamed. One MCP
  tool was added, `delete_deck`. `get_due_cards` keeps its name and its arguments; only its
  description mentions the new field.
- No database migration was added up to the point below: `BuiltinMigrations` held the same eight
  entries as at the tag, in the same order; the ninth is the preset-name cleanup listed below.
- No configuration key and no environment variable was added, removed or renamed.
- The deck-package and note-import schemas keep their shape; the cloze description now documents
  the `\alt` separator.
- The only payload change is additive: the optional `option_order` field on due cards (REST and
  the `get_due_cards` MCP result) and on the `POST /api/v1/review/render` response.
- One request is validated more strictly, with no shape change: `POST /api/v1/keys` rejects an
  `expires_at` that is not strictly after the current time, where it previously created a key
  that was already unusable. Omitting the field, or sending a future time, behaves as before.

| Goal | What shipped | Breaking change |
|---|---|---|
| Choice options in a display order | Each (user, card, state version) gets a derived display order, so the correct answer cannot be memorised by position; a submitted review produces a new one. `option_order[b]` is the stored index shown in slot `b`, and the submitted `answer` is still a stored index. | No — additive field; a client that ignores it sees the stored order and grades correctly |
| Formulas in the answer controls | The render response carries one sanitized HTML fragment per option; the controls inject it and typeset it, so TeX delimiters no longer appear literally. | No |
| `\alt` inside a formula | A cloze deletion whose whole content is inside a formula separates its accepted answers with `\alt`; `\|` keeps its meaning, and validation rejects `\alt` where it cannot split. | No — `\alt` is not a MathJax macro, so no existing content changes meaning |
| Rating buttons lose the interval hint | The four buttons show only their names, and the help popover no longer promises a time under each one. | No — display only; the render response still carries `intervals` |
| Login page links to registration | The SPA login page shows a link to `/register` when the anonymous policy probe reports `open`, and nothing while probing or for `invite` / `closed`. | No |
| Option controls for screen readers | Each checkbox and radio is named from the element that shows the option, through `aria-labelledby`, so a typeset formula is announced as its rendered text and a formula-only option is not nameless. | No |
| Review completion page | The completion text waits for the another-batch quota request, so the text and the offer form appear together; the form copy and the hint were shortened in the same pass. | No |
| Notification switches save again | Saving preferences sends only closable mail types; the locked class-A entries the form keeps for rendering no longer make every request fail with `class_locked`. | No |
| Class-D admin notifications | They carry RFC 8058 one-click unsubscribe headers, so an admin can opt out without logging in. | No |
| Toast close button theme | The close button follows the page theme, including a live switch, instead of sonner's light palette in dark mode. | No |
| Toolchain | Go 1.27 and Node 26 / npm 12 in `go.mod`, the Dockerfile, CI and the release workflow; `engines` requires node >= 26 and npm >= 12. | For from-source builds only — images and binaries are unaffected |
| Deleting a deck over MCP | `delete_deck` removes a deck together with its notes, cards and progress. Only the deck's owner may call it, and the deletion is irreversible. It calls the same service method as `DELETE /api/v1/decks/:id` and returns the same response body. | No — an added tool; a client that does not call it sees no change |
| Default mail templates | The built-in body of every mail type is designed Markdown rendered through the same pipeline as an admin template: a title, a primary action, a note and a footer, a derived plain-text part, and a shell that survives clients without rounded corners or `<style>`. Saving the pre-filled default unchanged sends exactly that mail, and the copy of all thirteen types was rewritten in both languages. | No — an added HTML part; the template variables and the stored templates are unchanged |
| API key expiry | The personal key page sets an expiry when creating a key: never, 7 / 30 / 90 days, one year (365 days), or a custom date meaning the end of that local day. The list shows each key's expiry date and puts already-expired keys in their own group with an `expired` badge, ranked below `revoked`; an expired key keeps the revoke action, so it can still be removed by its owner. | No — the field already existed and was already accepted on create |
| Expiry on create must be in the future | `POST /api/v1/keys` now rejects an `expires_at` that is not strictly after the current time with `400 invalid_request` and creates nothing, instead of returning a key that is dead on arrival. A request that omits the field, or sends a future time, behaves exactly as before. | Tightened — a client that sent a past `expires_at` previously succeeded and now fails, though the key it received was unusable |
| Imported presets stop duplicating the default preset | Importing a package into a new deck derives the preset name from the deck when the package carries no name or the reserved `Default`, and makes that name unique among the importer's presets, so an import can no longer add a preset named `Default`. Migration `0009_unique_default_preset_names` renames the duplicate `Default` rows earlier imports left behind, keeping the oldest row as the account's own default and leaving every `decks.preset_id` and `deck_member_settings.preset_id` reference in place. | No — preset names only. A deck that references a renamed preset keeps it, while a deck created afterwards starts from the account's own default preset again |

Not listed because neither a user nor an integration can observe them: the migration of code
comments to English, the gitignored `.worktrees/` layout, the removal of dead SSR-era test
helpers, the review-day seeding fix inside a test, and the internal rename of
`RenderResult.Extra` to `Options`.

### 2.2 Version number for the next release

- Section 2.1 adds, removes and renames no REST route, MCP tool, database schema item or
  configuration key. It adds one data-only migration, `0009_unique_default_preset_names`, which
  renames rows in `presets` and changes no table, column or index. One behaviour change is
  visible to a client: `POST /api/v1/keys` now
  rejects an `expires_at` that is not in the future with `400 invalid_request`, where it
  previously returned a key that was already unusable. The next release is **v0.1.11**.
- The toolchain bump is breaking only for someone building from source with Go 1.26 or Node 22.
  Counting that as breaking makes the same content **v0.2.0**. Every earlier release, including
  the one that changed media identity and deleted `/media/<numeric id>`, shipped as a `0.1.x`
  patch, so `v0.1.11` follows the project's own practice.

