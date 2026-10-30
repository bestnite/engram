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

// Read-side serving rule: every /media/<sha256> response is sandboxed, carries the stored type, and is a download
// unless its class is inline (image, audio, video).

// TestMediaServeNeutralisesPollutedMime creates a media row with mime=text/html plus the bytes on disk,
// then asserts the read response forces a download, keeps the sandbox CSP and nosniff, and never loses the bytes.
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
	if cd := get.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q, want attachment", cd)
	}
	if got := get.Header().Get("Content-Security-Policy"); got != "sandbox" {
		t.Fatalf("Content-Security-Policy = %q, want sandbox", got)
	}
	if got := get.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	// The content can still be retrieved (just served as a download); the file was not swallowed.
	if !strings.Contains(get.Body.String(), "<script>") {
		t.Fatalf("polluted media body lost: %q", get.Body.String())
	}
}

// TestMediaServeKeepsImagesInline asserts an image is served inline with the sandbox CSP still set.
func TestMediaServeKeepsImagesInline(t *testing.T) {
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
		t.Fatalf("image Content-Type = %q, want image/png", ct)
	}
	if cd := get.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("image Content-Disposition = %q, want empty (inline)", cd)
	}
	if got := get.Header().Get("Content-Security-Policy"); got != "sandbox" {
		t.Fatalf("Content-Security-Policy = %q, want sandbox", got)
	}
}

// TestMediaServeSandboxesSVGWithScript is the acceptance case for inline SVG: the stored type is kept, the file is
// served inline, and the sandbox CSP is what stops the embedded <script> from running.
func TestMediaServeSandboxesSVGWithScript(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	body := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(document.domain)</script></svg>`)
	up := uploadMedia(t, srv, cookies, csrf, "pic.svg", "image/svg+xml", body)
	if up.Code != http.StatusCreated {
		t.Fatalf("svg upload status = %d, want 201 (body %s)", up.Code, up.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(up.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	get := getWithCookies(t, srv, saved.URL, cookies)
	if get.Code != http.StatusOK {
		t.Fatalf("GET svg status = %d, want 200 (body %s)", get.Code, get.Body.String())
	}
	if ct := get.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("SVG Content-Type = %q, want image/svg+xml", ct)
	}
	if cd := get.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("SVG Content-Disposition = %q, want empty (inline)", cd)
	}
	if got := get.Header().Get("Content-Security-Policy"); got != "sandbox" {
		t.Fatalf("Content-Security-Policy = %q, want sandbox", got)
	}
	if !strings.Contains(get.Body.String(), "<script>") {
		t.Fatalf("SVG body lost: %q", get.Body.String())
	}
}

// TestMediaServeServesScriptAsAttachment is the acceptance case for a JS upload: it is stored as text/javascript
// and served as an attachment, so the browser never executes it on the application origin.
func TestMediaServeServesScriptAsAttachment(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	body := []byte("console.log('hello')")
	up := uploadMedia(t, srv, cookies, csrf, "tool.js", "text/javascript", body)
	if up.Code != http.StatusCreated {
		t.Fatalf("js upload status = %d, want 201 (body %s)", up.Code, up.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(up.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	get := getWithCookies(t, srv, saved.URL, cookies)
	if get.Code != http.StatusOK {
		t.Fatalf("GET js status = %d, want 200 (body %s)", get.Code, get.Body.String())
	}
	if ct := get.Header().Get("Content-Type"); ct != "text/javascript" {
		t.Fatalf("JS Content-Type = %q, want text/javascript", ct)
	}
	if cd := get.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("JS Content-Disposition = %q, want attachment", cd)
	}
	if got := get.Header().Get("Content-Security-Policy"); got != "sandbox" {
		t.Fatalf("Content-Security-Policy = %q, want sandbox", got)
	}
}
