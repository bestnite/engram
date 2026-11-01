package mcp

import (
	"bytes"
	"context"
	"git.nite07.com/nite/engram/internal/anki"
	"io"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// Acceptance for import_deck's direct-link source (url): mutually exclusive with package, and url goes through the same service method
// as REST POST /decks/import-url (the downloader is replaced with a controlled implementation, so the real network is never touched).

// stubPackageFetcher is a controlled direct-link downloader: it returns preset bytes and records calls.
type stubPackageFetcher struct {
	body  []byte
	calls int
	urls  []string
}

func (f *stubPackageFetcher) Fetch(_ context.Context, rawURL string, _ int64) (io.ReadCloser, error) {
	f.calls++
	f.urls = append(f.urls, rawURL)
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func mcpMinimalPackageZip(t *testing.T) []byte {
	t.Helper()
	pkg := anki.WritePackage{Collection: anki.WriteCollection{
		CreatedAt:   1700000000,
		Decks:       []anki.WriteDeck{{ID: 2, Name: "imported", ConfigID: 2}},
		DeckConfigs: []anki.WriteDeckConfig{{ID: 2, Name: "Imported", DesiredRetention: 0.9, LearningSteps: []float64{1}, RelearningSteps: []float64{10}, MaximumIntervalDays: 100}},
		NoteTypes:   []anki.WriteNoteType{{ID: 2, Name: "Basic", Fields: []anki.Field{{Name: "Front"}, {Ord: 1, Name: "Back"}}, Templates: []anki.Template{{Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{FrontSide}}<hr>{{Back}}"}}}},
		Notes:       []anki.WriteNote{{ID: 2, GUID: "fixture-guid", NoteTypeID: 2, Fields: []string{"q", "a"}}},
		Cards:       []anki.WriteCard{{ID: 2, NoteID: 2, DeckID: 2}},
	}}
	var buf bytes.Buffer
	if err := anki.WriteArchive(&buf, &pkg); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImportDeckURLMutualExclusion(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "url_excl")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	_, isErr, text := callTool(t, cs, "import_deck", map[string]any{
		"package": map[string]any{"manifest.json": map[string]any{}},
		"url":     "https://example.com/d.apkg",
	})
	if !isErr || !strings.Contains(text, "mutually exclusive") {
		t.Fatalf("import_deck(package+url) = (isErr=%v, %q), want a mutual-exclusion error", isErr, text)
	}

	_, isErr, text = callTool(t, cs, "import_deck", map[string]any{"target": "new_deck"})
	if !isErr || !strings.Contains(text, "required") {
		t.Fatalf("import_deck(no source) = (isErr=%v, %q), want a missing-source error", isErr, text)
	}
}

func TestImportDeckURLDownloadsAndImports(t *testing.T) {
	f := &stubPackageFetcher{body: mcpMinimalPackageZip(t)}
	_, db, keys, _, ts := newEnvWithPackageFetcher(t, f)
	u := seedUser(t, db, "url_import")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	out, isErr, text := callTool(t, cs, "import_deck", map[string]any{
		"url":     "https://example.com/deck.apkg",
		"target":  "new_deck",
		"dry_run": true,
	})
	if isErr {
		t.Fatalf("import_deck(url) error: %s", text)
	}
	if out["dry_run"] != true || out["notes_created"] != float64(1) {
		t.Errorf("import_deck(url) report = %v, want dry_run=true notes_created=1", out)
	}
	if f.calls != 1 || len(f.urls) != 1 || f.urls[0] != "https://example.com/deck.apkg" {
		t.Errorf("fetcher calls/urls = %d/%v, want one call to the given url", f.calls, f.urls)
	}
}

// TestImportURLRateLimitSharedAcrossRESTAndMCP proves REST and MCP direct-link imports share the same per-user
// rate limit pool: with one user alternating between the two transports, the 11th call is blocked whichever transport it takes, and no further download happens.
// Each call to newEnvWithPackageFetcher is a brand-new fixture (its own SQLite database + its own rate limiter).
func TestImportURLRateLimitSharedAcrossRESTAndMCP(t *testing.T) {
	f := &stubPackageFetcher{body: mcpMinimalPackageZip(t)}
	_, db, keys, _, ts := newEnvWithPackageFetcher(t, f)
	u := seedUser(t, db, "url_pool")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	const restCalls, mcpCalls = 6, 4
	restBody := `{"url":"https://example.com/deck.apkg","target":"new_deck","dry_run":true}`

	for i := 0; i < restCalls; i++ {
		if code, _ := rest(t, ts.URL, http.MethodPost, "/api/v1/decks/import-url", key, restBody); code != http.StatusOK {
			t.Fatalf("REST call %d = %d, want 200", i+1, code)
		}
	}
	for i := 0; i < mcpCalls; i++ {
		if _, isErr, text := callTool(t, cs, "import_deck", map[string]any{
			"url": "https://example.com/deck.apkg", "target": "new_deck", "dry_run": true,
		}); isErr {
			t.Fatalf("MCP call %d failed: %s", i+1, text)
		}
	}
	if f.calls != restCalls+mcpCalls {
		t.Fatalf("fetcher calls = %d, want %d after the allowed REST+MCP mix", f.calls, restCalls+mcpCalls)
	}

	// The 11th call: both MCP and REST must be blocked by the same pool, and neither triggers a download.
	if _, isErr, text := callTool(t, cs, "import_deck", map[string]any{
		"url": "https://example.com/deck.apkg", "target": "new_deck", "dry_run": true,
	}); !isErr || !strings.Contains(text, api.CodeRateLimited) {
		t.Fatalf("11th call over MCP = (isErr=%v, %q), want a rate_limited error", isErr, text)
	}
	if code, _ := rest(t, ts.URL, http.MethodPost, "/api/v1/decks/import-url", key, restBody); code != http.StatusTooManyRequests {
		t.Fatalf("11th call over REST = %d, want 429", code)
	}
	if f.calls != restCalls+mcpCalls {
		t.Fatalf("fetcher calls = %d, want %d: a rate-limited request must not download", f.calls, restCalls+mcpCalls)
	}
}
