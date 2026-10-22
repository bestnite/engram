// Package frontend carries the embedding of the frontend Svelte SPA production build artifacts.
package frontend

import (
	"embed"
	"fmt"
	"io/fs"
)

// DistFS embeds the Vite production build artifacts (index.html and assets/).
// `npm run build` must be run in the frontend/ directory beforehand; if the artifacts are not built, the Go build fails outright.
//
//go:embed all:dist
var DistFS embed.FS

// FS returns a filesystem rooted at dist and verifies that the required artifact (index.html) exists and is non-empty.
func FS() (fs.FS, error) {
	return SubFS(DistFS)
}

// SubFS extracts the dist subdirectory from the given embed.FS and verifies index.html (handy for negative unit-test cases).
func SubFS(efs embed.FS) (fs.FS, error) {
	sub, err := fs.Sub(efs, "dist")
	if err != nil {
		return nil, fmt.Errorf("frontend: open dist sub fs: %w", err)
	}
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, fmt.Errorf("frontend: dist/index.html is missing: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("frontend: dist/index.html is empty; run 'npm run build' first")
	}
	return sub, nil
}
