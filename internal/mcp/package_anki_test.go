package mcp

import (
	"encoding/base64"
	"os"
	"strings"
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
	// The stock fixture is a modern Anki package, which carries no engram key; the same count
	// must reach the MCP caller as the REST report.
	if missing, _ := out["answer_specs_missing"].(float64); missing == 0 {
		t.Fatalf("answer_specs_missing = %v, want the modern fixture's note types counted", out["answer_specs_missing"])
	}
}

func TestImportDeckRejectsInvalidPackage(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_invalid_package")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"document", map[string]any{"manifest.json": map[string]any{}}},
		{"invalid base64", "%%%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, isErr, text := callTool(t, cs, "import_deck", map[string]any{"package": tc.value})
			if !isErr {
				t.Fatalf("import_deck accepted invalid package: %s", text)
			}
		})
	}
}

// TestImportDeckReportsBadConflictOptionAsInvalidRequest is the MCP half of M19-107: a malformed
// on_conflict is a request mistake and must carry the same invalid_request code as the REST entry,
// not package_bad_format.
func TestImportDeckReportsBadConflictOptionAsInvalidRequest(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_bad_conflict")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)

	raw, err := os.ReadFile("../notetype/testdata/anki/stock.anki21b.apkg")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	_, isErr, text := callTool(t, cs, "import_deck", map[string]any{
		"package":     base64.StdEncoding.EncodeToString(raw),
		"target":      "new_deck",
		"on_conflict": "bogus",
	})
	if !isErr {
		t.Fatalf("import_deck accepted on_conflict=bogus: %s", text)
	}
	if !strings.Contains(text, "invalid_request") {
		t.Errorf("import_deck error text = %q, want the invalid_request code", text)
	}
	if strings.Contains(text, store.CodePackageBadFormat) {
		t.Errorf("import_deck error text = %q, want no package_bad_format code", text)
	}
	var decks int64
	if err := db.Model(&store.Deck{}).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 0 {
		t.Errorf("rejected import left %d deck(s), want none", decks)
	}
}
