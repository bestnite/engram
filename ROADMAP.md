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
    the same click. A failed load hides the control and keeps the text: an anonymous visitor on
    `/s/:token` cannot read `/media` at all, because media reads require sign-in and anonymous
    share sessions have nowhere to be recorded, which is the limitation images already have.
  - Call it wherever `typeset(...)` already runs: `ReviewView.svelte`, `NoteEditView.svelte`
    (preview only) and `ShareBrowseView.svelte`.
  - New control labels come from both catalogs under a `shortcode.` prefix appended at the end
    of each file, with identical key sets; nothing is hardcoded.
  - Depends on M11-1's DOM contract: do not start before M11-1 lands.

  *Acceptance:* jsdom tests with stubbed `speechSynthesis` and media playback prove that each
  call site enhances the container it typesets, that a second enhancement of the same DOM binds
  nothing twice, that a missing speech API leaves only the visible text, and that a media load
  error removes the control rather than showing a broken player. Evidence is the raw output of
  the frontend test run and `npm --prefix frontend run build`.
- [ ] **M11-3 Link-scoped media reads for share browsing** — an anonymous visitor on
  `/s/:token` cannot load any image or audio today: `mediaServe` requires sign-in
  (`internal/web/media.go:267`), and an anonymous share session has nowhere for a read grant to
  be recorded (`internal/web/publicshare.go:56-59`). Add a read route that treats the share token
  as the credential, so a shared deck's own media load without sign-in and nothing else does.
  Rejecting the cheaper alternative is part of the design: making media public for every deck
  that holds an active link would publish the content-addressed blob, which other users' private
  notes may reference through a dedup hit, would bypass a passphrase, and could not be recalled
  from a browser cache.
  - `GET /s/:token/media/:sha256`, registered only when the share dependencies are wired, like
    `registerShareBrowseRoutes`.
  - Check order: (1) resolve the link and answer 404 when it is revoked, expired or unknown,
    without telling the three apart; (2) validate `sha` with the existing `mediaShaParamRE`;
    (3) when the link carries no passphrase, continue at (5); (4) when it carries one, require the
    unlock cookie below and answer 401 without a valid one; (5) require a `media_notes` row
    pointing at a non-soft-deleted note of this deck, else 404; (6) serve the bytes under
    `mediaServe`'s existing content-type allowlist, `nosniff` and `Content-Disposition` rules.
  - The unlock cookie is stateless: `base64url(payload) + "." + base64url(HMAC-SHA256(secret,
    "share-unlock:" + payload))` with payload
    `{"token_sha":<sha256 of the link token>,"exp":<unix seconds>}`, signed
    through the existing `auth.Manager.SignValue` / `VerifyValue`
    (`internal/auth/session.go:94-126`), so no session, no table and no cleanup job exist. The
    attributes are `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/s/<token>/` and `Max-Age=3600`.
    It is issued only after `shareUnlock` has verified the passphrase. The purpose prefix is what
    keeps a value signed for another use — a session id, the TOTP pending credential — from being
    replayed here.
  - Verification compares `payload.token_sha` with the sha256 of the token resolved from the
    path, so a cookie minted for one link cannot read another link's media. The link is keyed by
    its token (`share_links.token` is the primary key), so the token itself is never copied into
    the cookie; its hash is enough to bind it.
  - `Cache-Control` on this route is `private, max-age=3600`, not `immutable`: a revoked link then
    converges within the hour in a browser cache instead of never.
  - The share content path rewrites, inside HTML that has already been sanitized, exactly the
    `/media/<64 hex characters>` value of `img[src]` and of `span[data-audio-src]` into
    `/s/<token>/media/<sha>`. A tokenizer from `golang.org/x/net/html` (already a direct
    dependency) walks the fragment; no other occurrence is touched, so a path that merely appears
    in card text or a code span stays literal.
  - Recorded so it is not mistaken for an oversight: this route gets no rate limiting, as is true
    of the other share entry points; the only anonymous entry points that have limiting are
    `/register` and `/forgot-password`.
  - Depends on M11-1 for the `data-audio-src` attribute shape. M11-2 is not required: the route
    and the rewrite are testable through the JSON transport.

  *Acceptance:* tests in `internal/web` prove that an anonymous request with an active
  passphrase-less link fetches a referenced medium and returns its exact bytes; that a sha
  referenced only by another deck answers 404, so a token is not a general file proxy; that a
  revoked or expired link answers 404; that a passphrase link answers 401 without the cookie and
  200 with it; that an edited payload, an edited signature, an expired payload, a cookie minted
  for another link, and a session cookie value presented as the unlock cookie each answer 401;
  and that the rendered share HTML carries the rewritten addresses and no bare `/media/<sha>`
  for media the deck references. The existing `internal/web/media_access_test.go` cases pass
  unchanged. Evidence is the raw output of
  `go test ./internal/web/... ./internal/store/... && go vet ./internal/web/...`.

---

## 2. Release ledger

### 2.1 Completed since v0.1.10 (2026-10-20)

**The machine surfaces are compatible with v0.1.10.**

- No REST route was added, removed or renamed, and no MCP tool was removed or renamed. One MCP
  tool was added, `delete_deck`. `get_due_cards` keeps its name and its arguments; only its
  description mentions the new field.
- No database migration was added: `BuiltinMigrations` holds the same eight entries as at the
  tag, in the same order.
- No configuration key and no environment variable was added, removed or renamed.
- The deck-package and note-import schemas keep their shape; the cloze description now documents
  the `\alt` separator.
- The only payload change is additive: the optional `option_order` field on due cards (REST and
  the `get_due_cards` MCP result) and on the `POST /api/v1/review/render` response.

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

Not listed because neither a user nor an integration can observe them: the migration of code
comments to English, the gitignored `.worktrees/` layout, the removal of dead SSR-era test
helpers, the review-day seeding fix inside a test, and the internal rename of
`RenderResult.Extra` to `Options`.

### 2.2 Version number for the next release

- Everything in section 2.1 is non-breaking on the HTTP API, the MCP tools, the database and the
  configuration, so the next release is **v0.1.11**.
- The toolchain bump is breaking only for someone building from source with Go 1.26 or Node 22.
  Counting that as breaking makes the same content **v0.2.0**. Every earlier release, including
  the one that changed media identity and deleted `/media/<numeric id>`, shipped as a `0.1.x`
  patch, so `v0.1.11` follows the project's own practice.

