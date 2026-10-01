# Contributing

Thanks for wanting to help. This document covers **how to build, test, and submit changes**
and restates the rules that external contributors are expected to follow. It is deliberately
short on the product itself: for what the service is and how to run it locally, read
[`README.md`](README.md) first — this file does not repeat the quick start.

Two documents are the project's authority, and both must be read before you change anything:

- [`DESIGN.md`](DESIGN.md) — the specification of record for behaviour, data model, and scope.
- [`DESIGN.md`](DESIGN.md) §2.3 — the database compatibility rules, referenced throughout below.

`AGENTS.md` is the maintainers' development guide (non-negotiable rules, definition of done,
task backlog). You are welcome to read it. **Do not edit it** (see
[Do not edit AGENTS.md](#do-not-edit-agentsmd)) — that applies to maintainers' automation
subagents as much as to external contributors.

## Contents

- [Before you start](#before-you-start)
- [Development environment](#development-environment)
- [Build, test, and generate](#build-test-and-generate)
- [Repository checks](#repository-checks)
- [Non-negotiable rules](#non-negotiable-rules)
- [Tests](#tests)
- [Commits](#commits)
- [Branch and pull request workflow](#branch-and-pull-request-workflow)
- [Parallel development with git worktrees](#parallel-development-with-git-worktrees)
- [Do not edit AGENTS.md](#do-not-edit-agentsmd)
- [Placeholders: project name and licence](#placeholders-project-name-and-licence)

## Before you start

1. Read [`README.md`](README.md) and its quick start. Every command there runs as written.
2. Read [`DESIGN.md`](DESIGN.md) for the area you want to change. If code and `DESIGN.md`
   disagree, one of them is a bug: fix the code, or change `DESIGN.md` in the same commit —
   never leave them inconsistent.
3. Anything not decided in `DESIGN.md` (its §13 lists the open questions) must be raised
   rather than invented in code. Open an issue or ask on the pull request.

## Development environment

- **Go 1.26 or newer** (the module declares `go 1.26`; the development machines run 1.27).
- A C toolchain is not required for development: SQLite works through a pure-Go driver.
- `templ` and the standalone `tailwindcss` CLI are needed for the generated code and CSS
  bundle. Their versions are pinned in the `Containerfile` and the CI workflow; bump them
  together with `go.mod`.
- The Tailwind standalone CLI is a **glibc** binary. It cannot run inside a musl (Alpine)
  image, so any container builder stage must use a glibc base image
  (`golang:1.26-bookworm`), not Alpine.
- PostgreSQL is the default deployment database; SQLite is fine for local development.
  Both must keep working (see [Both databases must work](#both-databases-must-work)).

## Build, test, and generate

Run the full check suite from the repository root before every commit:

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./...
```

`gofmt -l .` prints files that need formatting; it must print nothing.

Generated code and assets are **gitignored**, so a fresh checkout does not build until you
generate them. Both steps are wired into `go generate`:

```bash
go generate ./...
```

That runs `templ generate` (which emits `*_templ.go`) and the Tailwind build (which emits
`internal/web/static/css/tailwind.css`). If you prefer to run them explicitly, the two
equivalent commands are:

```bash
templ generate
tailwindcss -i ./internal/web/static/css/input.css \
            -o ./internal/web/static/css/tailwind.css --minify
```

Run the code checks **and** the generation steps before committing anything that touches
templates or styles: both outputs are gitignored, so a stale build is invisible in
`git status`.

To start the service locally with SQLite, use the quick start in [`README.md`](README.md).
It covers the data directory, the required environment variables, and the first-admin
setup; do not duplicate that setup here.

## Repository checks

Two scripts enforce the rules in the next section. They scan **tracked files only** (they use
`git grep`), so they must be run **after `git add`**. Running them on a dirty working tree
that has not been staged gives a **false green**: the files they cannot see are exactly the
ones about to be committed.

```bash
git add <your files>
bash scripts/checks/no-private-data.sh
bash scripts/checks/no-template-literals.sh
```

Both scripts must exit `0`. Re-run them on the target branch before merging. A "the checks
pass" claim is not evidence on its own — paste the command output in the pull request.

- `scripts/checks/no-private-data.sh` — fails on real domain names, host names, private or
  link-local IP addresses, non-placeholder email addresses, credentials, and key material.
- `scripts/checks/no-template-literals.sh` — fails on user-facing text hardcoded in a
  `*.templ` template instead of coming from the translation catalog.

If a needed string trips the sanitisation scan, change the placeholder — **do not weaken the
scan** and do not add a real host to `scripts/checks/allowed-hosts.txt`. Adding a legitimate
public dependency host is an explicit registration, not a way to silence a finding.

## Non-negotiable rules

These are restated from the maintainers' guide because breaking a rule is expensive to undo.
Follow them literally.

### Text language

| Kind of text | Language |
|---|---|
| Logs (`slog` messages and fields, job and subprocess output, CLI diagnostics, `panic` text, internal error strings) | **English** |
| Code comments (package/function docs and inline explanations) | **Chinese** |
| User-facing text (UI labels, form validation, API/MCP `message`) | **Translation catalog** (`zh-CN`, `en`) |
| Identifiers (log messages, catalog keys, error `code` values) | **English** |

Decision rule: machine and developer facing → English; reader of the source → Chinese; end
user → translation catalog. Never hardcode a user-facing string, and never take a log message
from a translation catalog. Comments explain *why*, not *what*. A missing key in one catalog
must fail the test build (catalog parity is asserted).

### Sanitisation

This is an open-source repository. **No committed file** — code, examples, fixtures, comments,
scripts, or documentation — may contain a real domain name, host name, private IP range,
personal email address, token, password, or description of private infrastructure.

- Use placeholders: `example.com`, `localhost`, `CHANGE_ME`.
- Test fixtures need placeholder domains too: emails in fixtures must use `example.com`,
  `example.org`, `example.net`, or `localhost`. A fixture that trips the scan is fixed by
  changing the fixture, never by adding a host to the allow-list.
- Local data, credentials, generated files, and build output stay out of git (see
  `.gitignore`). Run `git status` before every commit and check that nothing unintended is
  staged.

### Both databases must work

PostgreSQL (default deployment) and SQLite (single-node and development) are both first-class.
Follow the compatibility rules in `DESIGN.md` §2.3:

- No PostgreSQL-only column types (`jsonb`, `serial`, `array`).
- Store JSON as `TEXT`.
- Use `clause.OnConflict` for upserts and `LIMIT`/`OFFSET` for paging.
- `AutoMigrate` is **additive only**. Column type changes, drops, and new non-null constraints
  require an explicit, versioned migration function — never an implicit change.

### Domain invariants (do not break these)

1. **Content and progress are separate.** `notes`/`cards` hold content only; per-user
   scheduling lives in `card_states`, keyed `(card_id, user_id)`. Sharing a deck must never
   mix two users' progress.
2. **`reviews` is append-only.** Every column listed in `DESIGN.md` §2.2 is written from the
   first commit — it is the only fuel for parameter optimisation and cannot be reconstructed
   later.
3. **Ratings and states are integers**: `rating` 1–4 (Again/Hard/Good/Easy), `state_before`
   0–3 (New/Learning/Review/Relearning). This matches the FSRS ecosystem log format.
4. **One business layer, two transports.** REST handlers and MCP tools call the same service
   methods. Never duplicate validation or scheduling logic per transport.
5. **Card types are registered, not hardcoded.** Adding a type means adding one file and
   registering it; core code must not change.
6. **Review submission is idempotent.** Enforce the `expected_version` check and return `409`
   on mismatch; write the state update and the review row in one transaction.

`AGENTS.md` §2.3 lists the remaining invariants in full (including the pointer-boolean rule
for columns with a database default). Read them before touching the store layer.

### Go style

The repository is a plain monolith, organised by business package. Prefer concrete types over
interfaces; introduce an interface only when there are multiple implementations or a concrete
replacement need, and define the minimal interface **at the consumer**. One model serves
business, GORM, and JSON/CSV — no entity/DTO mapping layers. Blocking operations take
`context.Context` as the first parameter. Wrap errors with `%w`; API/MCP errors carry a stable
English `code` plus a localised `message`. Do not add a dependency without a reason that
survives the selection principle in `DESIGN.md` §10.1 (low complexity, prefer mature
libraries over invented ones).

## Tests

- Use **table-driven** tests.
- Use a **real SQLite database** (in-memory or a temp file). **Do not mock the database.**
- Handler tests use `httptest`.
- Core coverage targets are `internal/schedule` and `internal/cardtype`.
- **Negative cases are required, not optional.** Every new behaviour needs its failure path
  asserted: permission denials, CSRF failures, expired keys, scope violations, rejected
  input, and conflict paths. A test that only proves the happy path is incomplete.
- If a change alters behaviour or a decision, update `DESIGN.md` in the same commit.

A task is complete only when its acceptance criteria pass with evidence (a command output or a
captured response — not a claim) and the build/vet/format/test suite is clean.

## Commits

- **Conventional Commits**, English subject and body:
  `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`.
- **Signed commits.** `user.signingkey` and `commit.gpgsign` are set per repository, so
  signing needs no per-worktree setup. If you commit from a worktree, the same configuration
  applies.
- **One logical change per commit.** Do not bundle unrelated edits.
- The body explains **why**, not what. Keep the subject line in the imperative mood.

Example:

```
feat(schedule): honour deck-level daily caps in the queue builder

The caps were passed through QueueOptions with hard-coded defaults, so a deck
whose caps differed from the defaults was silently ignored.
```

## Branch and pull request workflow

External contributors use a fork and a pull request; project maintainers use worktrees
directly (see the next section). Both follow the same branch naming and commit rules.

1. Branch from the latest `main`:
   `feat/<task-id>-<slug>` (for example `feat/m3-2-queue-builder`), or `fix/<slug>` and
   `docs/<slug>` where there is no task ID.
2. Make your change, adding tests including the negative cases.
3. Regenerate templates and CSS if you touched them (`go generate ./...`).
4. Stage your files, then run **both** repository checks and the full check suite:
   `git add …`, `bash scripts/checks/no-private-data.sh`,
   `bash scripts/checks/no-template-literals.sh`,
   `go build ./... && go vet ./... && gofmt -l . && go test ./...`.
5. Commit with a signed, conventional message — one logical change per commit.
6. Push and open a pull request against `main`. In the description, state what changed, why,
   and paste the raw output of the checks and tests. Do not paraphrase the result.
7. Address review feedback with additional commits. A maintainer rebases onto `main`, re-runs
   the checks, and merges serially. **Contributors do not merge or push to `main` themselves.**

Keep the pull request focused. A change that touches many unrelated packages is the hardest
kind to review and the easiest to get wrong.

## Parallel development with git worktrees

If you are working alongside other writers (for example, when several agents or lanes run at
once), a branch alone isolates nothing: `HEAD`, the index, and the working tree are single per
checkout, so two writers in one directory overwrite each other. **One task = one worktree =
one commit.**

- The main checkout stays on `main` and is used only as the integration point. Do not develop
  in it.
- Worktrees live **outside** the repository directory, for example
  `../flashcard-wt/<task-id>`, so the main checkout's `git status` stays clean.
- Branch `feat/<task-id>-<slug>`, directory `flashcard-wt/<task-id>`.

```bash
git worktree add -b feat/m3-2 ../flashcard-wt/m3-2 main
# ... work and commit inside ../flashcard-wt/m3-2 ...
git worktree remove ../flashcard-wt/m3-2   # or keep the worktree for review
git worktree prune                         # drop stale entries
```

Working rules:

- A subagent never merges or pushes. The parent performs the merge serially (rebase onto
  `main`, run the checks, then merge).
- Generated artifacts are per worktree: run `templ generate` and the Tailwind build inside
  each worktree. The Go module cache and build cache are shared and safe for concurrent use.
- Never run repository maintenance commands (`git gc`, `git prune`, `git repack`) from a
  worktree.
- Commit early, even a work-in-progress commit, so the work lands in the shared object
  database instead of an orphaned directory.
- Single-writer hotspots — one writer at a time: `internal/store/models.go`,
  `internal/config/`, `internal/i18n/locales/`, `internal/web/views/`.
- Repository configuration is shared from the main repository: `user.signingkey` and
  `core.hooksPath` are visible inside every worktree.

## Do not edit AGENTS.md

**Subagents and external contributors must never edit `AGENTS.md`** (or any project-context
file such as `CLAUDE.md` or `.hermes.md`). Writing it requires the user's approval, which a
leaf subagent cannot obtain; the attempt is auto-denied or it interrupts the user mid-workflow.
This is the easiest trap to fall into when a task description says "update the guide": report
the needed change to the parent instead.

Maintainers record verified progress in the gitignored `PROGRESS.local.md` and apply checkbox
and counting updates to `AGENTS.md` in batches, with the user present.

## Placeholders: project name and licence

The **project name, module path, and licence are still placeholders** and have not been
decided:

- Project name and module path: `flashcard` / `example.com/flashcard` (backlog B-1).
- Licence: not chosen yet (MIT / Apache-2.0 / AGPL-3.0); `LICENSE` currently holds a
  placeholder (backlog B-2, `DESIGN.md` §13 #2).

**Do not pick one in a contribution.** A change that replaces the placeholder name, module
path, or licence will be rejected until the decision is made; open an issue to discuss it
instead.
