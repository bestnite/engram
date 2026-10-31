package mcp

import (
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

func TestCreateExportDownload(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_download")
	deck := seedDeck(t, db, u.ID)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead})
	cs := connect(t, ts.URL, key)
	out, isErr, text := callTool(t, cs, "create_export_download", map[string]any{"deck_id": deck.PublicID, "format": "apkg", "include_media": false})
	if isErr {
		t.Fatal(text)
	}
	url, ok := out["url"].(string)
	if !ok || !strings.HasPrefix(url, "/api/v1/decks/export-downloads/") || out["expires_at"] == nil {
		t.Fatalf("ticket: %v", out)
	}
	for i, want := range []int{http.StatusOK, http.StatusNotFound} {
		resp, err := http.Get(ts.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("request %d: %d", i, resp.StatusCode)
		}
	}
}
