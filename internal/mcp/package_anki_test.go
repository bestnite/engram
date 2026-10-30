package mcp

import (
	"encoding/base64"
	"os"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestImportDeckAcceptsAnki proves import_deck accepts a base64-encoded .apkg through the same
// service method as REST; the tool shapes arguments only, it does not parse archives itself.
func TestImportDeckAcceptsAnki(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_apkg")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	raw, err := os.ReadFile("../notetype/testdata/anki/stock.anki21b.apkg")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	out, isErr, text := callTool(t, cs, "import_deck", map[string]any{
		"package":     base64.StdEncoding.EncodeToString(raw),
		"target":      "new_deck",
		"dry_run":     true,
		"on_conflict": "update",
	})
	if isErr {
		t.Fatalf("import_deck(.apkg) error: %s", text)
	}
	if created, _ := out["notes_created"].(float64); created != 8 {
		t.Fatalf("import_deck(.apkg) report = %v, want eight notes", out)
	}
	if out["match_rule"] != "anki_guid" {
		t.Fatalf("match_rule = %v, want anki_guid", out["match_rule"])
	}
}
