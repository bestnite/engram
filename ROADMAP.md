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

---

## 2. Release ledger

### 2.1 Completed since v0.1.10 (2026-10-20)

**The release is not machine-compatible with v0.1.10: it removes seven REST routes and drops
two tables. The shortcode elements and the render attributes it adds are additive.**

- Seven REST routes were removed and none was added or renamed: `GET /s/:token`,
  `GET /api/v1/share/:token`, `POST /api/v1/share/:token/unlock`,
  `POST /api/v1/share/:token/join`, `POST /api/v1/decks/:id/sharing/links`,
  `DELETE /api/v1/decks/:id/sharing/links` and
  `DELETE /api/v1/decks/:id/sharing/links/revoke/:digest`. No MCP tool was removed or renamed.
  One MCP tool was added, `delete_deck`; `get_due_cards` keeps its name and its arguments, and
  only its description mentions the new field.
- Two migrations were added: `0008_drop_share_link_tables` drops the two share-link tables, and
  `0009_unique_default_preset_names` renames duplicate default presets. Both are listed below.
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
| Card content shortcodes | Card text accepts `{{tts:…}}`, `{{audio:…}}` and `{{ruby:…}}`; the server expands each into an element carrying named `data-*` attributes and leaves the body visible, and the review page and editor preview attach speech and audio controls to those attributes. An unknown name, a malformed parameter or a body containing `{{` stays literal; code spans and fenced blocks stay literal; mail bodies render plain text. | No — content without a shortcode renders exactly as before |
| Shortcode controls | Audio has a seekable progress track over the played segment; `tts` accepts the bare `hide` flag, which hides the body only while a speech control exists and restores it when it cannot be read aloud; both controls fit inside their line box with a 40×40 pointer target. | No — the added `data-tts-hide` attribute is additive; existing shortcodes and content are unchanged |
| Shortcode reference and insertion tools | `/help/shortcodes` documents the grammar, parameters and fallback rules in both languages; each editor text field carries its own image and shortcode tools, with the media library and composer in dialogs. | No |
| Note editor layout | Insertion tools sit on the field they write into; below `lg` the live preview is an on-demand drawer; the page-level field selector, upload area and standalone composer block are gone. | No — screen layout only |
| Share links removed | The anonymous share-link transport is gone: seven REST routes, the `share_links` and `share_session_decks` tables (migration `0008_drop_share_link_tables`), and the share-session branch of the media read rule. `deck_grants`, `share_invites`, the sharing page and the grant endpoints remain. | Yes — the seven routes disappear and the two tables are dropped; clients and stored links that used them stop working |

Not listed because neither a user nor an integration can observe them: the migration of code
comments to English, the gitignored `.worktrees/` layout, the removal of dead SSR-era test
helpers, the review-day seeding fix inside a test, and the internal rename of
`RenderResult.Extra` to `Options`.

### 2.2 Version number for the next release

- Section 2.1 removes seven REST routes and drops two tables. Migration
  `0008_drop_share_link_tables` drops `share_links` and `share_session_decks`; migration
  `0009_unique_default_preset_names` renames duplicate `Default` presets and changes no table,
  column or index. The shortcode renderer adds `ruby`/`rt`/`rp` elements and named `data-*`
  attributes to card HTML, which is additive for existing content. No MCP tool, configuration
  key or file format changes. One request is validated more strictly: `POST /api/v1/keys`
  rejects an `expires_at` that is not in the future with `400 invalid_request`, where it
  previously returned a key that was already unusable.
- The next release is **v0.1.11**. The project has shipped breaking changes in `0.1.x` patches
  before — the release that changed media identity and deleted `/media/<numeric id>` was a
  patch — so the share-link removal does not by itself force a minor bump. Counting it as one
  would make the same content **v0.2.0**.
- The toolchain bump is breaking only for someone building from source with Go 1.26 or Node 22.
  Counting that as breaking also points at **v0.2.0**; `v0.1.11` follows the project's own
  practice for the content above.

