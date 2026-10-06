# SPA migration gap audit (refreshed on 2026-10-06)

> Implementation evidence is based on current `main`, including SPA profile persistence, CSRF-protected review flow, review route cutover, and MCP cookie rejection. SPA note editing, API-key UI and server-side Markdown preview are still separate in-progress branches and are not counted as merged functionality.

## Current merged SPA inventory

- Routes with real SPA views: `/`, `/decks`, `/decks/:id`, `/decks/:id/notes`, `/spa/review`, `/stats`, `/settings`, `/settings/keys`, `/login`.
- Deck listing and create use `GET/POST /api/v1/decks`; profile and locale persist through session-only `GET/PATCH /api/v1/profile` and `PATCH /api/v1/settings/locale`.
- API key self-management is merged at `/settings/keys`, backed by existing REST key routes. Plaintext is only shown once in component memory.
- SPA review is still partial and deliberately uses `/spa/review`; legacy SSR `/review` remains active. The SPA supports self-assessed cards only, plain text content, no graded cards, edit/bury, Markdown or MathJax.
- The note editor is not yet merged. Current deck detail remains mostly read-only.

## Remaining migration areas

| Area | Current state | Removal decision |
|---|---|---|
| Onboarding, registration, invites and OIDC | SSR only; no SPA views or equivalent JSON flows | Keep SSR |
| Deck settings and daily caps | SSR only; no REST cap endpoint or SPA view | Keep SSR |
| Note create, edit, delete, bulk, restore, preview | REST supports some mutations; UI/preview/restore parity incomplete | Keep SSR |
| Media upload/library/picker | SSR upload/picker; REST supports listing only; no SPA upload | Keep SSR |
| Sharing, visibility, links and clone | SSR only; no REST equivalent | Keep SSR |
| Package import/export | REST exists; no SPA flow | Keep SSR |
| Review | `/spa/review` self-assessment prototype; `/review` SSR has broader functionality | Keep SSR |
| Statistics | SPA summary only; detailed distributions/forecasts remain SSR | Keep SSR |
| Password, email, notifications, TOTP | SSR flows remain; profile/locale only migrated | Keep SSR |
| Presets and optimizer jobs | SSR only; no SPA REST endpoints | Keep SSR |
| Admin users, settings, audit, jobs, health | SSR only; no SPA admin API | Keep SSR |
| PWA service worker | SPA JS/CSS precache update is merged; HTML, API and review data remain network-only | Keep active shared PWA routes |
| MathJax/CSP | CSP is enforcing and self-hosted MathJax remains available to SSR; SPA has no MathJax integration | Keep MathJax and CSP support |

## Build and verification status

- Vite production assets are embedded by Go; Docker/CI/GoReleaser build chains are configured.
- Recent local validation includes frontend check/test/build, targeted Go web/API tests, and full Go gate after the review/MCP fixes. Full Docker image and GoReleaser snapshot/publish acceptance remains outstanding.
- No legacy SSR/templ code is proven dead. Explicit server routes and active forms still handle substantial product functionality; do not remove them until each replacement workflow is implemented and verified.

## Cutover gates

1. Finish missing SPA pages and actions with session/CSRF protection, server authorization, and bilingual UI.
2. Complete safe server-rendered Markdown/TeX contract and SPA MathJax loading under current CSP.
3. Run browser E2E for authentication, notes, review, media, sharing/import, stats/settings/admin, and deep-link refresh.
4. Only then remove explicit SSR routes, templates, htmx scripts and unused assets; rebuild and rerun all tests.
5. Verify Docker build, GoReleaser snapshot archive contents, and release workflow artifacts.



**Scope:** source audit of `main` at `77f1ce0` (the checked-out `feat/spa-audit-refresh` worktree is based on that commit). This records current code behavior, not intended behavior. `DESIGN.md` §8 describes a pure Svelte SPA replacing templ/htmx; server authorization remains authoritative. This report is an audit, not a claim of complete cutover.

## Executive findings

- The SPA routes are `/`, `/decks`, `/decks/:id`, `/decks/:id/notes`, `/spa/review`, `/stats`, `/settings`, and `/login`. Current views cover home/deck listing and detail, a self-assessment review flow, summary stats, basic profile settings, and login. The profile API exists now: session-cookie `GET/PATCH /api/v1/profile` and `PATCH /api/v1/settings/locale` are registered by the web server, not `internal/api.API.Register`.
- Review migration is partial and deliberately separate from legacy `/review`: SPA review is at `/spa/review`; legacy SSR remains at `/review`. SPA review supports only self-assessment cards. Graded cards are omitted from the SPA queue display; card text is plain text, with no rendered Markdown or MathJax. SPA review also lacks edit and bury actions. Browser E2E has not been run.
- Explicit Go routes continue to win over the SPA fallback. The router still registers SSR pages and form actions for notes, review, settings, sharing, presets, import/export, admin, OIDC, and other features. This is a parallel shell/prototype, not a completed route cutover; no legacy handler/view is safe to remove on this evidence.
- REST provides many business endpoints, including notes writes, review due/submission, import/export/package, stats, keys, and media listing. There are still no corresponding SPA-facing endpoints/UI for several SSR capabilities, including deck caps, note preview, media upload, sharing/cloning, preset/job management, and admin CRUD/settings/audit/health. The review SPA's session-cookie/CSRF endpoint is a web route outside the REST API registration.
- Authentication remains transport-specific. SPA calls use same-origin session cookies and in-memory CSRF tokens; business REST routes use bearer API keys/scopes. Profile updates, SPA review submission, and SPA auth writes use explicit session CSRF protection. This is not a UI security boundary; the server validates access.
- CSP is enforced globally. It allows the theme bootstrap hash and inline styles, and excludes `unsafe-eval`. MathJax remains embedded for legacy pages; the SPA has no MathJax integration.
- The service worker still caches only legacy static resources (CSS, htmx/review JS, MathJax, icons, manifest). It does not cache Vite `/assets/*` or SPA HTML. Its fetch handler only considers `/static/` and the manifest; API, review, and HTML responses are network-only.

## Route-by-route inventory

“REST” means concrete registrations in `internal/api/server.go`, plus SPA-specific profile/review routes registered from `internal/web/server.go`. “State” describes current SPA coverage, not whether the server-side feature exists.

| Feature / URL | SSR route / active handler | SPA route / current view | Existing API and current SPA coverage | Legacy safe to remove? |
|---|---|---|---|---|
| Home `/` | Explicit Go `GET /` renders home | `/` → `HomeView` | Uses decks and summary stats; missing per-deck queue counts/streak details from the target | No; exact Go route remains active |
| Login/logout `/login`, `/logout` | Explicit SSR login/logout handlers | `/login` → `LoginView`; SPA navigation invokes logout | Session/auth JSON endpoints; SPA session-cookie + CSRF flow implemented | No; explicit legacy routes and flows remain |
| Setup, registration, invites, OIDC | SSR auth routes remain registered | No corresponding SPA views/routes | No SPA API coverage for setup/register/invite/OIDC flows | No; onboarding and protocol flows are not migrated |
| Deck list `/decks` | Explicit SSR deck listing and actions | `/decks` → `DecksView` | `GET /api/v1/decks` used; create exists via REST but is not implemented in this view | No; explicit SSR route remains and SPA lacks parity |
| Deck detail/note list `/decks/:id`, `/decks/:id/notes` | `/decks/:id` can reach SPA fallback; `/decks/:id/notes` is explicit SSR | Both map to `DeckDetailView` | Deck and notes list APIs are used. View is read-only; no rendered Markdown/MathJax, editor, or bulk actions | No; note-list path is an explicit Go route |
| Deck CRUD, caps, settings | SSR deck and deck-settings handlers | No SPA settings/CRUD view | Deck creation API exists; no deck update/delete/cap endpoints | No; server-side settings/actions are not covered |
| Note create/update/delete/bulk | SSR note handlers and forms | No SPA editor/actions | REST batch create/update, patch, soft-delete, and bulk actions exist; SPA does not call them | No |
| Note preview/render `/decks/:id/preview` | SSR preview endpoints and fragments | None | No preview REST endpoint; SPA displays field values as text | No |
| Restore/permanent delete | No dedicated SSR route identified for restore/permanent delete | None | REST delete is soft-delete; restore code occurs within package import, not a standalone route | No; no SPA parity and no standalone restore endpoint |
| Media upload/library/picker | SSR upload, picker, and proxy routes | No SPA upload/library view | REST media listing exists; direct upload is SSR-only | No |
| Sharing, visibility, links, clone | SSR sharing/browse/clone handlers | None | No REST sharing/grant/link/clone routes | No |
| Package import/export `/import`, `/export`, package | SSR package pages/forms | None | REST package import/export and card export exist | No |
| Review `/spa/review` and `/review` | Explicit legacy `GET /review`; legacy answer/action POST routes remain | `/spa/review` → `ReviewView` | Due-card fetch plus session-CSRF `POST /api/v1/review/answer`. SPA only displays self-assessment cards; graded cards are omitted. Plain text only; no Markdown/MathJax, edit, or bury action. Legacy SSR supports graded answers and additional actions | No; `/review` stays the legacy SSR route and has broader coverage |
| Stats `/stats` | Explicit SSR stats page | `/stats` → `StatsView` | Summary endpoint and aggregate metrics only; detailed forecasts, distributions, tags, and trends are not full SPA parity | No; explicit SSR route remains |
| Profile/locale `/settings` | SSR profile/password and related settings routes | `/settings` → `SettingsView` | `GET/PATCH /api/v1/profile` and `PATCH /api/v1/settings/locale`, registered by web server. SPA supports basic display name, locale, timezone, and cutoff profile fields; password, email, notification, TOTP, and key management remain absent | No; SSR paths remain active and settings coverage is partial |
| API keys `/settings/keys`, `/admin/api-keys` | SSR personal and admin key pages | None | REST key endpoints exist; not consumed by a SPA view | No |
| Presets/optimization | SSR preset and job handlers | None | No corresponding REST endpoints in `internal/api/server.go` | No |
| Admin `/admin/*` | SSR admin route table and pages remain active | None | No admin REST namespace/routes | No |
| Health `/healthz` | Standalone JSON handler | Not a migration target | Public health endpoint | Not applicable |
| SPA shell/assets and fallback | `/assets/*` serves embedded Vite assets; fallback serves SPA index on unmatched page GETs | Svelte app routes listed above | Explicit Go routes take precedence over fallback | No; the fallback is only a partial page route mechanism |
| Service worker/PWA | `/sw.js`, `/manifest.webmanifest`, `/pwa.js`, favicon routes | SPA asset integration is not a replacement for SW coverage | Precache list remains legacy `/static/` CSS/JS/MathJax/icons/manifest; no Vite assets or HTML | No; cache manifest and fetch policy remain legacy-only |

## Auth, CSRF, and rendering evidence

- `frontend/src/lib/api/client.ts` uses `credentials: 'same-origin'`, removes `Authorization`, stores CSRF in memory, and adds `X-CSRF-Token` on unsafe requests. The client therefore uses session auth, not bearer REST credentials.
- `internal/web/server.go` registers the profile/locale routes under `/api/v1` when sessions and users are configured. Profile PATCH and locale PATCH use session CSRF middleware. `POST /api/v1/review/answer` is separately registered with session CSRF middleware when API and sessions are available.
- `spaReviewAnswer` rejects graded cards and submits self-assessment using the shared API service. `ReviewView.svelte` displays text values directly and provides reveal/rating controls only; it contains no MathJax or Markdown rendering integration and no edit/bury actions.
- `internal/web/review.go` registers legacy GET `/review` and legacy answer/action POST routes. The source comment explicitly says to keep the old page while SPA review is incomplete and use `/spa/review` temporarily.
- `internal/web/pwa.go` lists `css/tailwind.css`, `js/htmx.min.js`, `js/review.js`, MathJax and icons in `pwaShellAssets`; its service worker caches only `/static/` and the manifest. It does not include `/assets/` or cache HTML.
- The global CSP is enforced in `internal/web/security_headers.go`; its `script-src` excludes `unsafe-eval`. Legacy templates and JS still link/use self-hosted MathJax; the SPA source does not.

## Build, tests, and evidence limits

The earlier audit recorded source/config inspection and explicitly did **not** run generated frontend or broad Go tests. For this refresh, the following commands were actually run:

```text
cd frontend && npm ci && npm run check && npm test && npm run build
npm ci: success; 0 vulnerabilities (npm printed an esbuild install-script approval warning)
svelte-check: 0 errors, 0 warnings
Vitest: 11 files passed; 93 tests passed
Vite build: success; emitted index HTML, CSS, and JS bundles

go test ./internal/web -run 'Test(SPA|SPAFallbackDeepLinks|SPAProfile|SPASelf|.*Review)'
initial attempt: blocked because frontend/dist did not exist

go generate ./...
success; Tailwind and templ generation completed

go test ./internal/web -run 'Test(SPA|SPAFallbackDeepLinks|SPAProfile|SPASelf|.*Review)'
ok  git.nite07.com/nite/engram/internal/web  13.900s
```

The Go test attempt required `go generate ./...` because generated assets/views were absent in the worktree. That generation changed only ignored outputs; no generated output is part of this report's intended change. No full Go suite, browser E2E, or release build was run. Frontend tests and the selected Go tests verify specific client/server contracts; they do not establish browser behavior or complete cutover parity.

## Focused implementation update: SPA deck creation

The SPA now submits deck creation through the existing `POST /api/v1/decks` endpoint. The API client obtains a session CSRF token when it has none, then uses its same-origin cookie and `X-CSRF-Token` request behavior. The form defaults visibility to private and sends `preset_id: 0` to use the service default; successful creation inserts the returned deck into the list. Stable server validation codes for invalid deck name, invalid description, and invalid request receive localized UI messages. English and Simplified Chinese catalog keys were appended in parity.

Focused API tests cover session token bootstrap, the create payload/response, and preservation of the server validation code. Verification: `npm run check` passed with 0 errors/warnings; `npm test -- --run` passed (11 files, 95 tests); `npm run build` passed; `git diff --check` passed. No Go backend files changed.

## Audit limitations

This is a source audit, not browser E2E or full acceptance. The dynamic `frontend/dist` bundle was not independently audited. Route existence alone does not establish business semantics; authorization and payload conclusions are based on handler/middleware source and corresponding tests.

## Cutover decision and remaining gates

Legacy cleanup is blocked until SPA parity exists and is verified route by route. Remaining work includes onboarding/OIDC; deck settings and caps; note creation, deletion, bulk operations and restore; media upload/library; sharing and clone; package import/export; graded review, Markdown/MathJax, edit and bury; detailed stats; full account security settings; presets/jobs; and all admin surfaces. Update the service-worker cache policy for Vite assets while keeping HTML, API and review data uncached. Add browser E2E for authentication, routing, review, rendered card content, and CSP behavior. Only after those gates pass should active route references, templates, HTMX scripts, MathJax assets, and legacy cache entries be considered for removal.
