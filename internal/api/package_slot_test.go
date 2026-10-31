package api

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestImportSlotGateLimitOne covers the gate itself at the limit a request meets first: one holder,
// a second acquire refused, and a released slot reusable.
func TestImportSlotGateLimitOne(t *testing.T) {
	var gate importSlotGate
	if !gate.acquire(1) {
		t.Fatal("first acquire failed at limit 1")
	}
	if gate.acquire(1) {
		t.Fatal("second acquire succeeded while the only slot was taken")
	}
	gate.release()
	if !gate.acquire(1) {
		t.Fatal("acquire after release failed")
	}
	gate.release()
	gate.release() // an extra release must not let the running count go negative
	if !gate.acquire(1) {
		t.Fatal("acquire after an extra release failed")
	}
}

// TestImportPackageRefusesWhenEverySlotIsTaken is the handler acceptance at
// package_max_concurrent_imports=1: an import that starts while the only slot is held is refused
// at once with 429 deck_import_busy and spools nothing; once the slot is free a new import succeeds.
func TestImportPackageRefusesWhenEverySlotIsTaken(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "import_busy", store.RoleUser)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	t.Setenv(store.EnvPackageMaxConcurrentImports, "1")
	t.Setenv(store.EnvPackageMaxBytes, "")

	// TMPDIR points at an empty directory: a refused request must leave no multipart temp file.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	// The running import another request meets; the gate is the same one every entry uses.
	release, err := env.api.ImportSlot(context.Background())
	if err != nil {
		t.Fatalf("ImportSlot: %v", err)
	}

	body, ct := multipartBody(t, "deck.edeck", minimalPackageZip(t))
	rec := postImport(t, env, k.Plaintext, body, ct)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("import while the only slot is taken = %d, want 429 (body %s)", rec.Code, rec.Body.String())
	}
	code, msg := decodePackageErrorCode(t, rec.Body.Bytes())
	if code != CodeDeckImportBusy {
		t.Errorf("refused import code = %q, want %q", code, CodeDeckImportBusy)
	}
	// The message names the configured limit, so an operator can find the setting that refused them.
	if !strings.Contains(msg, "limit is 1") {
		t.Errorf("refused import message = %q, want it to name the limit", msg)
	}
	if entries, err := os.ReadDir(tmp); err == nil {
		for _, e := range entries {
			t.Errorf("refused import left a temporary file behind: %s", e.Name())
		}
	}

	release()
	// A rejected import still returns its slot: an invalid package answers 400, not 429, and the
	// next valid import succeeds.
	badBody, badCT := multipartBody(t, "bad.edeck", []byte("this is not a zip"))
	if rec := postImport(t, env, k.Plaintext, badBody, badCT); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid package after release = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	goodBody, goodCT := multipartBody(t, "deck.edeck", minimalPackageZip(t))
	if rec := postImport(t, env, k.Plaintext, goodBody, goodCT); rec.Code != http.StatusOK {
		t.Fatalf("import after the first ended = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}
