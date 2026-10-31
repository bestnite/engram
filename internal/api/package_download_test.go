package api

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

func TestExportDownloadSingleUseExpiryAndOwner(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "download_owner", store.RoleUser)
	other := seedUser(t, env.db, "download_other", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	ctx := context.Background()
	if _, err := env.api.CreateExportDownload(ctx, other.ID, deck.ID, "apkg", store.PackageOptions{}); err == nil {
		t.Fatal("unauthorized issuance succeeded")
	}
	get := func(url string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		env.router().ServeHTTP(r, httptest.NewRequest(http.MethodGet, url, nil))
		return r
	}
	ticket, err := env.api.CreateExportDownload(ctx, owner.ID, deck.ID, "apkg", store.PackageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r := get(ticket.URL); r.Code != http.StatusOK {
		t.Fatalf("first download: %d %s", r.Code, r.Body.String())
	}
	if r := get(ticket.URL); r.Code != http.StatusNotFound {
		t.Fatalf("replay: %d", r.Code)
	}
	ticket, err = env.api.CreateExportDownload(ctx, owner.ID, deck.ID, "apkg", store.PackageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env.api.now = func() time.Time { return env.now.Add(16 * time.Minute) }
	if r := get(ticket.URL); r.Code != http.StatusNotFound {
		t.Fatalf("expired: %d", r.Code)
	}
	env.api.now = func() time.Time { return env.now }
	ticket, err = env.api.CreateExportDownload(ctx, owner.ID, deck.ID, "apkg", store.PackageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.db.Model(&store.Deck{}).Where("id = ?", deck.ID).Update("owner_user_id", other.ID).Error; err != nil {
		t.Fatal(err)
	}
	if r := get(ticket.URL); r.Code == http.StatusOK {
		t.Fatal("download bypassed revoked access")
	}
}

func TestExportAnkiCapturedCurl(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "curl_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
	server := httptest.NewServer(env.router())
	defer server.Close()
	file := filepath.Join(t.TempDir(), "deck.apkg")
	cmd := exec.Command("curl", "-fsS", "-H", "Authorization: Bearer "+key.Plaintext, "-o", file, server.URL+"/api/v1/decks/"+deck.PublicID+"/package?format=apkg&include_media=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("curl: %v: %s", err, output)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := exec.Command("unzip", "-l", file).CombinedOutput()
	if err != nil {
		t.Fatalf("unzip: %v: %s", err, listing)
	}
	if !strings.Contains(string(listing), "collection.anki21") || !strings.Contains(string(listing), "meta") {
		t.Fatalf("listing: %s", listing)
	}
	t.Logf("curl archive size: %d bytes\nunzip -l:\n%s", info.Size(), listing)
}

func TestExportAnkiRouteAndScope(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "export_owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	read := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
	write := seedKey(t, env.keys, owner.ID, []string{store.ScopeWrite}, nil)
	get := func(query, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/decks/"+deck.PublicID+"/package?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		r := httptest.NewRecorder()
		env.router().ServeHTTP(r, req)
		return r
	}
	r := get("format=apkg&include_media=0", read.Plaintext)
	if r.Code != http.StatusOK {
		t.Fatalf("export: %d %s", r.Code, r.Body.String())
	}
	if r.Header().Get("Content-Type") != "application/zip" || !strings.Contains(r.Header().Get("Content-Disposition"), ".apkg") {
		t.Fatalf("headers: %v", r.Header())
	}
	z, err := zip.NewReader(bytes.NewReader(r.Body.Bytes()), int64(r.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range z.File {
		names[f.Name] = true
	}
	if !names["collection.anki21"] || !names["meta"] {
		t.Fatalf("zip entries: %v", names)
	}
	if _, err := env.api.ImportDeckPackage(context.Background(), owner, nil, bytes.NewReader(r.Body.Bytes()), store.PackageImportOptions{Target: "new_deck", DryRun: true}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if r := get("format=unknown", read.Plaintext); r.Code != http.StatusBadRequest {
		t.Fatalf("format: %d", r.Code)
	}
	if r := get("format=apkg", write.Plaintext); r.Code != http.StatusForbidden {
		t.Fatalf("scope: %d", r.Code)
	}
}
