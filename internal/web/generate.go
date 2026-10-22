// This file carries the go:generate directives (the build step in AGENTS.md §4).
//
// With the SPA migration done the server no longer renders pages: the SSR templates (templ) and the Tailwind stylesheets they used have been deleted,
// so there is no generation step here any more — the frontend artifacts are produced by `npm --prefix frontend run build` and go:embed-ed into the binary.
package web
