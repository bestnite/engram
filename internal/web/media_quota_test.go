package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
	"gorm.io/gorm"
)

// This file covers: the per-user total media quota, on top of the single-file limit.
// Three acceptance lines: over-limit refusal (stable code + localized copy naming the limit), quota released after references are deleted,
// unlimited by default (unconfigured means no self-invented number).

// quotaPNG builds a PNG with a fixed total byte count (8-byte signature + padding), for precise usage control.
func quotaPNG(total int) []byte {
	if total < 8 {
		total = 8
	}
	b := make([]byte, total)
	copy(b, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	return b
}

// mediaErr decodes the code and localized message of the uniform error envelope.
type mediaErrResp struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// TestMediaQuotaDefaultUnlimited acceptance: with no quota configured, uploads are not limited in total (off by default).
func TestMediaQuotaDefaultUnlimited(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	// Clear the environment variable explicitly so the "unconfigured" path is taken; the settings table is not written.
	t.Setenv(envMediaUserQuotaBytes, "")
	rec := uploadMedia(t, srv, cookies, csrf, "big.png", "image/png", quotaPNG(6000))
	if rec.Code != http.StatusCreated {
		t.Fatalf("default upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	t.Logf("default (no env, no setting) accepts a 6000 B upload: status 201")
}

// TestMediaQuotaExceededRejectedLocalized acceptance: when over the limit, refuse with a stable code and localized copy naming the limit/usage;
// both languages are given — the default (zh-CN) and ?lang=en.
func TestMediaQuotaExceededRejectedLocalized(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	t.Setenv(envMediaUserQuotaBytes, "")
	ctx := context.Background()
	if err := store.PutSetting(ctx, db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting() error = %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")

	// First upload 1500 bytes and have owner's note reference it → used = 1500.
	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500))
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d, want 201", first.Code)
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, saved.Sha256)

	// Upload another 1000 bytes: 1500+1000 > 2048 → refused.
	rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var e mediaErrResp
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if e.Error.Code != media.CodeQuotaExceeded {
		t.Fatalf("error code = %q, want %q", e.Error.Code, media.CodeQuotaExceeded)
	}
	// The copy names the limit and the used amount (human-readable form).
	if !strings.Contains(e.Error.Message, humanBytes(2048)) || !strings.Contains(e.Error.Message, humanBytes(1500)) {
		t.Fatalf("zh message %q must name limit %s and used %s", e.Error.Message, humanBytes(2048), humanBytes(1500))
	}

	// ?lang=en: the English copy names the numbers as well.
	recEn := uploadMediaTo(t, srv, cookies, "/media?lang=en", csrf, "m2.png", "image/png", quotaPNG(1000))
	if recEn.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("en over-quota status = %d, want 413", recEn.Code)
	}
	var eEn mediaErrResp
	if err := json.Unmarshal(recEn.Body.Bytes(), &eEn); err != nil {
		t.Fatalf("decode en error body: %v", err)
	}
	if eEn.Error.Code != media.CodeQuotaExceeded {
		t.Fatalf("en error code = %q, want %q", eEn.Error.Code, media.CodeQuotaExceeded)
	}
	if !strings.Contains(eEn.Error.Message, humanBytes(2048)) || !strings.Contains(eEn.Error.Message, "quota") {
		t.Fatalf("en message %q must be English and name the limit %s", eEn.Error.Message, humanBytes(2048))
	}
	t.Logf("over-quota rejected: code=%s zh=%q en=%q", e.Error.Code, e.Error.Message, eEn.Error.Message)
}

// TestMediaQuotaFreesAfterDeleteReference acceptance: after the reference is deleted (note soft-deleted) the quota is released again.
func TestMediaQuotaFreesAfterDeleteReference(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	t.Setenv(envMediaUserQuotaBytes, "")
	ctx := context.Background()
	if err := store.PutSetting(ctx, db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting() error = %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")

	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500))
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	note := referenceMedia(t, db, deck.ID, ownerID, saved.Sha256)

	if rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota status = %d, want 413", rec.Code)
	}

	// Delete the note referencing it → usage back to zero → the same file should be accepted this time.
	if err := db.Delete(note).Error; err != nil {
		t.Fatalf("soft delete note: %v", err)
	}
	if usage, err := store.UserMediaUsage(ctx, db, ownerID); err != nil {
		t.Fatalf("UserMediaUsage() error = %v", err)
	} else if usage.Bytes != 0 {
		t.Fatalf("usage after delete = %d, want 0", usage.Bytes)
	}
	if rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000)); rec.Code != http.StatusCreated {
		t.Fatalf("post-delete upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	t.Logf("quota released after deleting the reference note: usage 1500 -> 0, upload of 1000 B accepted (201)")
}

// TestMediaQuotaDedupeDoesNotChargeTwice acceptance rule: when the same blob is already charged to that user,
// uploading the same bytes again does not add up (deduplication is not charged twice).
func TestMediaQuotaDedupeDoesNotChargeTwice(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	t.Setenv(envMediaUserQuotaBytes, "")
	ctx := context.Background()
	if err := store.PutSetting(ctx, db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting() error = %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")
	body := quotaPNG(1500)

	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d, want 201", first.Code)
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, saved.Sha256)

	// 1500 is already charged; the same sha again would exceed the limit if counted as 1500+1500=3000, but after deduplication the addition is 0.
	again := uploadMedia(t, srv, cookies, csrf, "m1-copy.png", "image/png", body)
	if again.Code != http.StatusCreated {
		t.Fatalf("dedupe re-upload status = %d, want 201 (body %s)", again.Code, again.Body.String())
	}
}

// referenceMedia has owner create a note referencing the given media sha256 (simulating the editor inserting /media/<sha256> into a field).
// The fixture is built through the note write methods: the media_notes mapping is created by the write path, so the test goes through the production path (L2).
func referenceMedia(t *testing.T, db *gorm.DB, deckID, ownerID uint64, mediaSha string) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, Kind: "basic", CreatedBy: &ownerID}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note,
		map[string]any{"front": "![](/media/" + mediaSha + ")", "back": "x"}); err != nil {
		t.Fatalf("create referencing note: %v", err)
	}
	return note
}

// TestMediaQuotaCountsUnreferencedUploads is the negative case: a file uploaded and never inserted into any note still counts against the quota,
// otherwise an account could keep uploading "unreferenced files" to fill the disk. 1500 B pending upload + 1000 B > 2048 → refused.
func TestMediaQuotaCountsUnreferencedUploads(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	t.Setenv(envMediaUserQuotaBytes, "")
	if err := store.PutSetting(context.Background(), db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting() error = %v", err)
	}
	if rec := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500)); rec.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d, want 201", rec.Code)
	}
	rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("second unreferenced upload status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var e mediaErrResp
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if e.Error.Code != media.CodeQuotaExceeded {
		t.Errorf("code = %q, want %q", e.Error.Code, media.CodeQuotaExceeded)
	}
}

// TestMediaUploadRejectsOversizedBody asserts that a request body over the overall limit is refused with media_too_large (the body is
// length-capped before multipart parsing, so an oversized body never gets written to the temp disk first).
func TestMediaUploadRejectsOversizedBody(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	t.Setenv(media.EnvMediaMaxBytes, "4096")
	rec := uploadMedia(t, srv, cookies, csrf, "huge.png", "image/png", quotaPNG(4096+multipartOverheadBytes+1))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var e mediaErrResp
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if e.Error.Code != media.CodeTooLarge {
		t.Errorf("code = %q, want %q", e.Error.Code, media.CodeTooLarge)
	}
}
