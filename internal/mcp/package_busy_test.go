package mcp

import (
	"context"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// TestImportDeckReportsBusyWhenEverySlotIsTaken asserts import_deck refuses with the same stable
// code and message as the REST entries when package_max_concurrent_imports imports already run.
func TestImportDeckReportsBusyWhenEverySlotIsTaken(t *testing.T) {
	a, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "busy_import")
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	cs := connect(t, ts.URL, key)
	t.Setenv(store.EnvPackageMaxConcurrentImports, "1")

	release, err := a.ImportSlot(context.Background())
	if err != nil {
		t.Fatalf("ImportSlot: %v", err)
	}
	defer release()

	_, isErr, text := callTool(t, cs, "import_deck", map[string]any{
		"package": map[string]any{"manifest.json": map[string]any{}},
		"target":  "new_deck",
		"dry_run": true,
	})
	if !isErr {
		t.Fatalf("import_deck with every slot taken = (isErr=%v, %q), want an error", isErr, text)
	}
	if !strings.Contains(text, api.CodeDeckImportBusy) {
		t.Fatalf("import_deck refusal = %q, want code %q", text, api.CodeDeckImportBusy)
	}
}
