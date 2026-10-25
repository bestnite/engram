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

