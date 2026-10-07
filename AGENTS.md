# AGENTS.md — Development Guide

Read this file before making any change to this repository. It records the rules that are
not obvious from the code and the definition of "done". The task backlog with stable IDs lives in
`ROADMAP.md`.

---

## 1. Source of truth

- `DESIGN.md` is the single source of truth for behaviour, data model, and scope. It is
  **not part of this repository**: the maintainer keeps it in the local working tree and it is
  not committed (see `.gitignore`). Read it there before making a change; when you start from a
  fresh worktree, the maintainer copies it in.
- If code and `DESIGN.md` disagree, one of them is a bug. Fix the code and report the
  disagreement: `DESIGN.md` is owned by the maintainer, who updates it.
- Anything not decided in `DESIGN.md` (section 13 holds the open questions) must be asked
  or added there, not invented in code.
- **Code comments must be self-contained.** A comment states the rule and its reason in its
  own words. It never cites `DESIGN.md`, any other document that is not committed, or a bare
  section number such as `§4.5`: a public reader has neither the file nor the numbering, so a
  citation there is a dangling pointer that carries no reason. The rule content stays, the
  pointer goes. References to committed files (`AGENTS.md`, `ROADMAP.md`, `schema/*.json`,
  RFCs) remain allowed, because those resolve for every reader.

---

## 2. Non-negotiable rules

Each rule exists because breaking it is expensive to undo. Follow them literally.

### 2.1 Text language

| Kind of text | Language | Notes |
|---|---|---|
| Logs (`slog` messages and fields, job and subprocess output, CLI diagnostics, `panic` text, internal error strings) | **English** | Never localised, never taken from a translation catalog. |
| Code comments (package/function docs and inline explanations) | **Chinese** | Explain *why*, not *what*. |
| User-facing text (UI labels, form validation, API/MCP `message`) | **Translation catalog** (`zh-CN`, `en`) | Never hardcode. |
| Identifiers (log messages, catalog keys, error `code` values) | **English** | Keeps catalogs and logs searchable and avoids mixed-language keys. |

Decision rule: machine and developer facing → English; reader of the source → Chinese;
end user → translation catalog.

### 2.2 Repository sanitisation (open-source repo)

- No real domain names, host names, private IP ranges, personal email addresses, tokens,
  passwords, or descriptions of private infrastructure in **any** committed file —
  including examples, fixtures, comments, and documentation.
- Use placeholders: `example.com`, `localhost`, `CHANGE_ME`.
- The Go module path is the one deliberate exception: it is the project's publication
  address rather than private infrastructure, so it carries the real code-host domain in
  `go.mod`, in imports, and in any documented `go get` command. Every other real domain
  stays out of the repository (see `.gitignore`). Run `git status` before every commit and
  check nothing unintended is staged.
- **This rule has no automated check any more** (the check scripts were removed on
  2026-10-06): review it by eye on every commit and re-read the staged diff before merging.
  A subagent's "the checks pass" is not evidence on its own — the mechanically enforced
  neighbour is the visible-text rule (every user-facing string comes from the catalog, see
  §2.1).
- A test fixture needs a placeholder domain too: emails in fixtures must use `example.com`,
  `example.org`, `example.net`, or `localhost`.

### 2.3 Domain invariants (do not break these)

1. **Content and progress are separate.** `notes`/`cards` hold content only; per-user
   scheduling lives in `card_states`, keyed `(card_id, user_id)`. Sharing a deck must
   never mix two users' progress.
2. **`reviews` is append-only.** Every column of the review log is written from
   the first commit — it is the only fuel for parameter optimisation and cannot be
   reconstructed later.
3. **Ratings and states are integers**: `rating` 1–4 (Again/Hard/Good/Easy),
   `state_before` 0–3 (New/Learning/Review/Relearning). This matches the FSRS ecosystem
   log format and keeps optimiser export trivial.
4. **Both databases must work**: PostgreSQL (default deployment) and SQLite (single-node
   and development). No PG-only types (`jsonb`, `serial`, `array`), JSON stored as `TEXT`,
   `clause.OnConflict` for upserts, `LIMIT/OFFSET` for paging.
5. **AutoMigrate is additive only.** Column type changes, drops, and new non-null
   constraints require an explicit, versioned migration function.
6. **One business layer, two transports.** REST handlers and MCP tools call the same
   service methods. Never duplicate validation or scheduling logic per transport.
7. **Card types are registered, not hardcoded.** Adding a type means adding one file and
   registering it; core code must not change.
8. **Review submission is idempotent.** Enforce the `expected_version` check and return
   `409` on mismatch; write the state update and the review row in one transaction.
9. **A boolean column with a database default must be a pointer in the model.** GORM omits
   a zero-valued field when the column carries a `default` tag, so the database default
   silently overwrites an explicit `false` (measured on `presets.enable_fuzz`). Use `*bool`
   for such columns; never work around it at the call site more than once.

### 2.4 Go style in this repo

- Plain monolith, organised by business package; concrete types over interfaces;
  dependencies wired explicitly in `cmd/engram`.
- **One model serves business, GORM, and JSON/CSV.** No entity/DTO mapping layers.
- Introduce an interface only when there are multiple implementations or a concrete
  replacement need; define the minimal interface **at the consumer**.
- Concrete `Store` types wrap GORM access. Add a `Service` only for genuinely complex
  flows.
- Blocking operations take `context.Context` as the first parameter.
- Errors: wrap with `%w`; API/MCP errors carry a stable English `code` plus a localised
  `message`.
- No new dependency without a reason that survives the selection principle (low complexity,
  prefer mature libraries over invented ones).

### 2.5 Tests and commits

- Table-driven tests; use a real SQLite database (in-memory or temp file) — do not mock
  the database. Core coverage targets: `internal/schedule` and `internal/cardtype`.
- **The default test database is SQLite, locally and in CI.** Neither runs a database
  service: `go test ./...` without `TEST_PG_DSN` makes the PostgreSQL cases gated by
  `internal/pgtest` skip themselves. Set `TEST_PG_DSN` to run them, and do that by hand
  whenever you touch driver-sensitive code (transactions, `clause.OnConflict` upserts,
  concurrency on `card_states`).
- Handler tests use `httptest`. Negative cases are required, not optional: permission
  denials, CSRF failures, expired keys, scope violations.
- Commits are signed (`user.signingkey` is set per repository), use Conventional Commits
  prefixes (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`), and are written in
  **English** — subject and body.
- One logical change per commit; message body explains why, not what.

### 2.6 Writing requirements in this repository

Every written requirement, instruction, or document line must have exactly **one**
possible reading. After writing, re-read each line; if a second reading is possible,
split it into explicit statements. This applies to code comments, task descriptions in
this file, commit messages, and `DESIGN.md`.

---

## 3. Layout

```
engram/
├── AGENTS.md                 # this file: the rules
├── ROADMAP.md                # the task backlog and progress conventions
├── DESIGN.md                 # specification of record (maintainer-local, untracked)
├── README.md / README.zh.md  # English is the primary document; Chinese is parallel
├── LICENSE
├── go.mod                    # module git.nite07.com/nite/engram
├── cmd/engram/main.go     # subcommand entry point
├── internal/
│   ├── config/               # env vars + settings-table overlay and precedence
│   ├── store/                # GORM models and business stores
│   ├── schedule/             # go-fsrs wrapper: state machine, queue, review submit
│   ├── cardtype/             # card type registry and type implementations
│   ├── auth/                 # local accounts, sessions, CSRF, OIDC, API keys, roles
│   ├── media/                # local file storage: dedupe, write, proxy read
│   ├── api/                  # /api/v1 handlers
│   ├── mcp/                  # MCP server: tool definitions that call the services
│   ├── i18n/                 # locales/{zh-CN,en}.yaml plus loader and translator
│   └── web/                  # gin routes, JSON handlers, SPA shell serving, static assets
```

There is no top-level `test/` directory: integration coverage lives beside the code as
`internal/<package>/*_test.go`, using `httptest` for handlers and a real SQLite database (file
or in-memory) for storage.

---

## 4. Build and verify

```bash
# code
go build ./... && go vet ./... && gofmt -l . && go test ./...

# the embedded frontend build output is gitignored, so build it before the Go build
npm --prefix frontend ci
npm --prefix frontend run build

# run locally (SQLite is fine for development)
DB_DRIVER=sqlite DB_DSN=data/engram.db AUTO_MIGRATE=1 go run ./cmd/engram serve
```

Run the frontend build **before** the code checks whenever the change touches `frontend/`:
`go build ./...` embeds `frontend/dist`, which is gitignored, so a stale build is invisible in
`git status`.

### Definition of done

A task is done only when all of the following hold:

1. The acceptance criteria written next to the task in `ROADMAP.md` pass, and the evidence
   is a command output or a captured response — not a claim.
2. `go build ./... && go vet ./... && gofmt -l . && go test ./...` are clean.
3. New behaviour has tests, including the negative cases named in the task.
4. Logs are English, comments are Chinese, user-facing strings come from the catalog.
5. The change is committed with a signed, conventional commit; `git status` is clean.
6. If the change alters behaviour or decisions, report it in your final message so the
   maintainer can update `DESIGN.md`; you cannot commit that file because it is not tracked.
7. No comment, document or commit message cites a file that is not in the repository.
   Every reference resolves for a public reader: `AGENTS.md`, `ROADMAP.md`, `README*`,
   `schema/*.json`, RFCs, or code paths inside the repository.

---

## 5. Dispatching tasks to subagents

- **Subagents must never edit `AGENTS.md` or `ROADMAP.md`** (or any project-context file such as
  `CLAUDE.md` or `.hermes.md`). Writing them requires user approval, which a leaf subagent cannot obtain: the
  attempt is auto-denied or it interrupts the user mid-workflow. The parent records verified
  progress in the gitignored `PROGRESS.local.md` and applies checkbox and counting updates to
  `ROADMAP.md` in batches, with the user present.


- One task per subagent. Pass the task's ID, its full text from `ROADMAP.md`, and the
  relevant `DESIGN.md` sections as context; subagents do not share this conversation.
- Require the subagent to run the verification commands from section 4 and to report the
  raw output, not a summary claim.
- The parent verifies the reported evidence before marking the checkbox; a subagent's
  self-report is not proof.
- A subagent that discovers missing design information must report it instead of
  inventing behaviour; the parent then records the decision in `DESIGN.md`.
- Keep one commit per task so progress can be audited with `git log`.

## 6. Parallel development with git worktrees

A branch alone isolates nothing: `HEAD`, the index, and the working tree are single per
checkout, so two writers in one directory will overwrite each other. Every parallel writer
therefore gets its own worktree. The rules below were verified on git 2.55.

**Layout and naming**

- The main checkout stays on `main` and is used only as the integration point. Do not
  develop in it.
- Worktrees live **outside** the repository directory, for example
  `../engram-wt/<task-id>`, so the main checkout's `git status` stays clean.
- Branch: `feat/<task-id>-<slug>` (for example `feat/m3-2-queue-builder`).
  Directory: `engram-wt/<task-id>`. Both use the task IDs from `ROADMAP.md`.

```bash
git worktree add -b feat/m3-2 ../engram-wt/m3-2 main
# ... work and commit inside ../engram-wt/m3-2 ...
git worktree remove ../engram-wt/m3-2   # or keep the worktree for review
git worktree prune                         # drop stale entries
```

**Working rules**

- One task = one worktree = one commit. The subagent works inside its worktree; the parent
  performs the merge serially (rebase onto `main`, run the section 4 checks, then merge).
  A subagent never merges or pushes.
- Concurrency budget: at most three writers at once, and only when the two tasks touch
  **disjoint file sets**. Parallelism is decided by file overlap, nothing else.
- Single-writer hotspots — one writer at a time, other tasks wait for the next phase:
  `internal/store/models.go`, `internal/config/`, `internal/i18n/locales/`,
  `internal/web/views/`.
- The locale catalogs are the one hotspot that can be shared, under a strict convention: each
  writer **appends its own block at the end of the file**, using a prefix it owns (`stats.`,
  `admin.audit.`), and never reorders or reformats an existing line. Measured over three rounds:
  three lanes appending in parallel merged with no conflict at all, while the same files conflict
  the moment two blocks land at the same position. Every dispatch that touches the catalogs must
  state this convention, and the writer must keep the two catalogs' key sets identical.
- Add a new route to `adminRoutes()` in the same change that registers it. That list is what the
  "non-admin gets 403 on every /admin route" test walks, so a route missing from it is a route
  that is never checked.
- Generated artifacts are per worktree: run `npm --prefix frontend run build` inside each
  worktree. The Go module cache and build cache are shared and safe for concurrent use. Never
  run repository maintenance commands (`git gc`, `git prune`, `git repack`) from a worktree.
- Subagents are session-scoped and are killed when the session ends. Require an early
  commit, even a work-in-progress one, so the work lands in the shared object database
  instead of an orphaned directory.

**Pitfalls (each reproduced on git 2.55)**

1. A branch can be checked out in only one worktree at a time, and `--force` does not
   override this. This is what enforces one task per branch, and it also means two writers
   can never share a branch.
2. Deleting a worktree directory does not remove the worktree. Until `git worktree prune`
   runs, the entry stays listed as `prunable` and the branch still counts as checked out,
   which blocks creating another worktree for that branch.
3. Hooks and scripts exist in a worktree only if they are committed. An untracked
   `.githooks/` does not run and produces no error, so commits can silently lose their
   signature.
4. Repository configuration is shared from the main repository: `user.signingkey` and
   `core.hooksPath` are visible inside every worktree, so signing needs no per-worktree
   setup.

**When to use separate clones instead**

Use `git clone --local` only when a writer needs its own `.git/config`, its own ignore
rules, or the freedom to run `git clean -xdf`. That is not the case in this repository;
worktrees are the default.

**Phase plan — which milestones may run in parallel**

| Phase | Lanes | Constraint |
|---|---|---|
| A | M0 alone | Everything depends on it; run it serially |
| B | M1 (auth) ∥ M2 (store, cardtype, web) | Disjoint packages |
| C | M3 (schedule) ∥ M4 (api, mcp) | Freeze the shared store models; neither lane may change them |
| D | M5 (grants) ∥ M7 (statistics) | Disjoint packages |
| E | M6 (admin views) **or** M8 (mobile, i18n) | Both rewrite `internal/web/views/`; run them in sequence. If they must overlap, split M8 into "gestures and JS" and "view text", and keep the view-text half exclusive with M6 |
| F | M9 last | Needs real review data produced by M3 |
