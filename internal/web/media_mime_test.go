package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// Read-side fallback: the database may already hold polluted rows written by an old import chain (mime=text/html, image/svg+xml …).
// mediaServe must never echo such a mime as Content-Type; it must downgrade to a download/octet-stream, with nosniff.

// TestMediaServeNeutralisesPollutedMime creates a media row with mime=text/html plus the bytes on disk,
// then asserts the read response's Content-Type is not an executable type, that it forces a download, and that it carries X-Content-Type-Options: nosniff.
func TestMediaServeNeutralisesPollutedMime(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	root := srv.media.Root()

	body := []byte("<html><body><script>alert(document.domain)</script></body></html>")
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	rel := filepath.Join(sha[:2], sha+".bin")
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir shard: %v", err)
	}
	if err := os.WriteFile(abs, body, 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}
	// The owner supplied these bytes: register media_uploaders (read authorization rests on ownership, no longer on media.created_by).
	// This case only cares about the mime fallback; read authorization is let through by that.
	row := store.Media{Sha256: sha, RelPath: rel, Mime: "text/html", Bytes: int64(len(body)), CreatedBy: store.Ptr(ownerID), CreatedAt: time.Now().UTC()}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed polluted media row: %v", err)
	}
	if err := store.RecordMediaUploader(context.Background(), db, sha, ownerID); err != nil {
		t.Fatalf("record media uploader: %v", err)
	}

	get := getWithCookies(t, srv, "/media/"+row.Sha256, cookies)
	if get.Code != http.StatusOK {
		t.Fatalf("GET polluted media status = %d, want 200 (body %s)", get.Code, get.Body.String())
	}
	if ct := get.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream (must not echo text/html)", ct)
	}
	if cd := get.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q, want attachment", cd)
	}
	if got := get.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	// The content can still be retrieved (just served as a download); the file was not swallowed.
	if !strings.Contains(get.Body.String(), "<script>") {
		t.Fatalf("polluted media body lost: %q", get.Body.String())
	}
}

// TestMediaServeKeepsAllowedMediaInline asserts allowed images are still served inline as declared, i.e. the new validation does not trip up normal reads.
func TestMediaServeKeepsAllowedMediaInline(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	up := uploadMedia(t, srv, cookies, csrf, "ok.png", "image/png", pngBody())
	if up.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", up.Code, up.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(up.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	get := getWithCookies(t, srv, saved.URL, cookies)
	if get.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", saved.URL, get.Code)
	}
	if ct := get.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("legal image Content-Type = %q, want image/png", ct)
	}
	if cd := get.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("legal image Content-Disposition = %q, want empty (inline)", cd)
	}
	if got := get.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}
