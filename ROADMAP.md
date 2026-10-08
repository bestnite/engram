# ROADMAP.md — Task backlog and progress

This file holds the **open** work: every open task with a stable ID, grouped by milestone.
A completed task leaves the file in the same commit that completes it, so this is a backlog
rather than a history — `git log` is the history, and the commit that deletes a task carries
its acceptance evidence. The rules for changing this repository live in `AGENTS.md`; read that
first.

A subagent must never edit this file: writing it requires user approval, which a leaf subagent
cannot obtain.

---

## 1. Task backlog

Conventions:

- `- [ ]` is the only checkbox: an open task. There is no `- [x]` line, because completing a
  task deletes it (see section 2.2).
- IDs are unique and never reused. An ID that has been deleted stays retired: reusing it would
  make an older reference silently point at a different task.
- Each task is written to be handed to one subagent without extra context: it names the
  files, the acceptance criteria, and how to verify them.
- A task may be split, but each resulting task keeps a new ID and its own criteria.
- Progress is reported as the number of open tasks (section 2).

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

### Backlog (no milestone yet)

- [ ] **B-10 English `DESIGN.md`** — the specification is currently Chinese only; for a
  public repository either translate it to English and keep the Chinese version as
  `DESIGN.zh.md`, or state explicitly that Chinese is the primary language of the
  specification (blocked on a decision).

---

### Security pass (2026-10-05/06)

One-off repository-wide audit and remediation, run outside the milestone plan and therefore
**not counted** in section 2.1. Recorded here because the working ledger is not committed.

**Method.** `gosec` plus `govulncheck` for the tool layer, then five human audit lanes
(scope/authz, auth, injection, frontend, admin). 43 findings were consolidated into 27 fix
tasks; every fix landed as a signed commit with a fail-first test, a directed green run and a
negative control (behaviour reverted, test must go red, behaviour restored).

**Fixed.**

| Area | What changed |
|---|---|
| Authorization | Package import now enforces the target deck role (`into_deck` = editor, `replace_deck` = owner); review submission is graded per action (rating/undo/bury/next = reader, suspend = owner); statistics, tag breakdown, due forecast and the retro recompute are all scoped to the caller's visible decks; admin-scope API keys are role-bound and a non-admin cannot mint one; MCP is API-key only and every request must be made with a key owned by the handshake user; the full-database and admin export paths were deleted outright; the MCP session map is dropped on `DELETE` and bounded |
| Input limits | REST package import caps the request body with the configured media limit before multipart parsing; base64 archives are length-checked before decoding; deck package manifests bound name/description (200/2000 characters, rune-counted, control characters and invalid UTF-8 rejected) on import *and* on deck create/rename; archives with duplicate entries or a declared sha that disagrees with the bytes are rejected; imported media counts against the importer's quota on all four entry points |
| Resource use | `/register` and `/forgot-password` are rate-limited by IP *and* target email (5 per 15 minutes); the OIDC pending table is capped at 1000 entries with expired-entry eviction; a job whose `MarkRunning` fails is marked failed instead of blocking the queue, and adapter weights reads are bounded |
| Hardening | Formula-rendering XSS in `internal/render`; media MIME sniffing moved to a leaf package with magic-byte checks; pre-session CSRF cookie `Secure` derives from `BASE_URL`; password reset (self-service and admin) revokes every API key of that user; TOTP rejects a replayed time step; admin actions write audit rows; the share page renders the same sanitized HTML as the review page; `govulncheck` runs in CI; an **enforcing** CSP (no `unsafe-eval`; htmx JS-expression attributes replaced by event delegation) plus `nosniff`, `Referrer-Policy`, `X-Frame-Options` and `Permissions-Policy` are set on every response |

**Media model (breaking).** Media is now identified by its content hash: the primary key and
the URL are the sha256, and `/media/<numeric id>` no longer exists. Read authorization is
derived from an explicit `media_notes` mapping plus deck visibility, instead of scanning note
fields; a write that introduces a reference the writer cannot already read is rejected; all
note-writing entry points route through one method that rebuilds the mapping. A second table,
`media_uploaders`, records everyone who ever supplied those bytes, so a deduplicated upload
still grants its uploader read access after a share is revoked. Share links now require a
logged-in account. Existing note references to the old numeric form were **not** migrated.

**Open items.**

- **Closed:** deck descriptions now share the 2000-character bound across creation,
  updates and package import.
- The PostgreSQL branch of the media primary-key migration has never been executed: local and
  CI testing is SQLite only.
- `gosec` is not wired into CI by decision; only `govulncheck` runs there.

---

### After the 0.1.0 release (2026-10-06)

Not milestone tasks and deliberately not checkboxes, so the counting rules in section 2.1
stay intact.

**Hardening completed.** The CSP is now **enforcing** (no `'unsafe-eval'`): the two
`hx-on::after-swap` uses became a delegated `htmx:afterSwap` listener (`static/js/notes.js`),
and a source-level guard test fails if the served shell ever needs something the policy lacks.
Verified in a real browser: the note-editor preview still re-typesets MathJax after an htmx
swap, the review page still renders formulas, and the route set produces zero CSP
violations. The card-body attribute whitelist now has explicit regression tests for
`hx-*`, `data-hx-*`, `on*`, `style`, `javascript:` and `srcdoc`.

**Features added.** A paginated, read-scoped media library: `GET /api/v1/media` (keyset,
`limit` 1–100, default 24) and the editor's "choose from library" picker
(`GET /decks/:id/media/picker`, editor role), which inserts `![](/media/<sha256>)` into the
focused field and previews the originals with CSS sizing plus `loading="lazy"` — no
thumbnails, no re-encoding. Each user can now choose the local hour at which review
reminders and the weekly digest are sent (default 19:00, `users.reminder_hour`, nullable
because midnight is a legitimate value); the old 23:00–07:00 quiet-window heuristic is gone.

**Duplication converged** (from a file-by-file census rather than guesswork). Removed the
dead HTTP-layer deck/note role guards, collapsed duplicated audit/scope/token-error/
pagination/decode helpers into single owners, and unified the timezone, day-cutoff and
review-day-shift helpers. The census also exposed two real defects, both fixed: the default
preset was created under three different names (literal, localised, lower-case) so one user
could accumulate several "defaults", and the store's review-day start disagreed with the
scheduler's about out-of-range cutoffs.

**Behaviour changes worth knowing.** Routing the web import through the shared service
(so all four entry points make the same decisions) changed two visible things: a corrupt
package originally rendered the generic `invalid_request` message REST returned. The
follow-up below restores specific package codes on every transport. Web imports write media through the same
media root as the other entry points. The web page still returns 400 rather than REST's 413
for an oversized body — a pre-existing difference, deliberately left alone. The default
preset is now identified by a stable literal name rather than a localised one, so an account
that already had a localised default preset may end up with one extra row.

**Follow-up fixes completed.**

- Deck creation and updates enforce the shared 2000-character description limit, valid
  UTF-8 and absence of C0 controls. Empty descriptions are allowed. Invalid descriptions
  return `deck_description_invalid`; rejected writes do not persist.
- **Revised:** the day-cutoff form accepts 0–23; zero is midnight. An unset (`NULL`)
  configuration uses 04:00, and clearing the input restores that default. User and
  scheduler inputs distinguish unset from zero; queue quotas, submissions, burying,
  statistics, notifications and optimizer exports use the same resolution rule.
  Migration `0002_nullable_day_cutoff` relaxes the column without rewriting stored values,
  review-day history or existing due times. Existing zero values now mean midnight.
  The migration is exercised on SQLite, including index/trigger preservation; its
  PostgreSQL branch is not locally executed.
- Review reminders and weekly summaries each have an independent 20-hour minimum interval,
  using their existing `sent_at` ledger column. Skipped ticks do not consume a day/week
  entry. This prevents the four-hour day/week-boundary collision without blocking a weekly
  summary after four days of downtime. It is not a calendar-day cap.
- Package errors preserve their specific codes across REST, MCP and web, including
  `package_bad_format`; the existing HTTP status semantics are unchanged.
- `AGENTS.md` now states that integration tests live beside their packages instead of
  listing a nonexistent top-level `test/` directory.
- The migration-period `/spa/*` aliases are gone (2026-10-08). Every page the SSR layer
  owned keeps exactly one address — `/login`, `/register`, `/setup`, `/login/totp`,
  `/review`, `/decks/:id/settings`, `/settings/totp`, `/settings/notifications`,
  `/verify-email`, `/confirm-email-change`, `/unsubscribe` — and the second router table
  that mirrored them under `/spa/...` was removed from both the server and the SPA router.
  A bookmark to an old address now falls through to the SPA's not-found view. `/login/totp`
  never had a server route of its own: it is served by the catch-all shell fallback, and the
  anonymous CSRF cookie comes from `GET /api/v1/auth/session`.

**Unverified.** The PostgreSQL branch of the media primary-key migration has not been
executed locally. Local and CI tests use SQLite; PostgreSQL testing remains out of scope.

## 2. Progress tracking

### 2.1 Counting

- Count open tasks with `grep -c '^- [ ]' ROADMAP.md`, and per milestone with
  `grep -c '^- [ ] \*\*M10-' ROADMAP.md` (replace the prefix).
- Report the open count per milestone, for example `M10 7 open · backlog 1 open`.
- The count only goes down as work lands, and that is the whole picture: a milestone is
  finished when it holds no open task, at which point its section and heading leave this file.

### 2.2 Marking a task done

- Delete the task's entry from this file in the same commit that completes it, and name the ID
  in the commit message so the task text and its evidence stay recoverable with
  `git log --grep=<id>`.
- A task may be deleted only when the acceptance criteria pass with evidence (command output
  or captured response) and `AGENTS.md` section 4's definition of done holds.
- If a task turns out to be wrong or unnecessary, do not delete it as if it were done: delete
  it with a commit message saying what replaced it, and update `DESIGN.md` if the change is
  behavioural.
