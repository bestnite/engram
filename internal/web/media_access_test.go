package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// This file covers two rules: read authorization for GET /media/<sha256>, and the pre-write media check on note writes.
//
// Read rules (authorization has a single place: a signed-in user with deck access): a signed-in user can read a given
// media if and only if — (1) a media_uploaders record points at them (they supplied these bytes, a dedupe hit counts,
// permanently valid), or (2) a media_notes mapping holds a record pointing at "a non-soft-deleted note inside a deck they can see".
// No permission and not-found both return 404, leaking no existence.
//
// F2c (fixed, 2026-10-06): an editor of a shared deck used to be able to inject references to someone else's media into a note another person had written, so the mapping
// became self-satisfying as soon as it was created and they could read that media (the old TestMediaAccessResidualEditorInjectionStillGrants).
// Now, before a note write, the references newly introduced by that write are validated against **the state before the write** for whether the writer can read them; out-of-scope references are
// refused at write time and the mapping is never created. This file pins that down entry by entry (web editor create/edit) and covers cases for no false positives, being able to read
// one's own uploads after a share is revoked, and a dedupe hit registering every uploader.
//
// Fixtures are always built through the note write methods (the NoteStore write methods), never by inserting notes through GORM — otherwise the mapping would not be created and
// the test would not be exercising the production path.

// uploadAndSha uploads some bytes and decodes the returned media sha256. The bytes determine the sha256, so the caller controls whether it dedupes.
func uploadAndSha(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string, body []byte) string {
	t.Helper()
	rec := uploadMedia(t, srv, cookies, csrf, "pic.png", "image/png", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if !mediaShaOK(saved.Sha256) {
		t.Fatalf("upload returned sha %q, want 64-hex sha256", saved.Sha256)
	}
	return saved.Sha256
}

// uploadToDeckAndSha uploads bytes through the in-deck upload entry (editor required) and decodes the sha256.
func uploadToDeckAndSha(t *testing.T, srv *Server, deckPublicID string, cookies []*http.Cookie, csrf string, body []byte) string {
	t.Helper()
	rec := uploadMediaTo(t, srv, cookies, "/decks/"+deckPublicID+"/media", csrf, "pic.png", "image/png", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("deck upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if !mediaShaOK(saved.Sha256) {
		t.Fatalf("deck upload returned sha %q, want 64-hex sha256", saved.Sha256)
	}
	return saved.Sha256
}

// noteBy creates a note with the given author and fields through the note write methods; author nil means created_by IS NULL.
// Going through the write path is required: the media_notes mapping is created by the write methods, and inserting the note directly would bypass the logic under test.
func noteBy(t *testing.T, db *gorm.DB, deckID uint64, author *uint64, fields map[string]any) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, Kind: "basic", CreatedBy: author}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note, fields); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return note
}

// referenceMediaBy is the media-reference specialisation of noteBy: the fields reference mediaSha exactly, and the author is given by author.
func referenceMediaBy(t *testing.T, db *gorm.DB, deckID uint64, author *uint64, mediaSha string) *store.Note {
	t.Helper()
	return noteBy(t, db, deckID, author, map[string]any{
		"front": fmt.Sprintf("![](/media/%s)", mediaSha),
		"back":  "x",
	})
}

// grantRole has owner grant the given role on a deck to userID.
// The grant table is written directly: the SSR sharing form entry went away with the page layer, and SPA sharing writes go through /api/v1/decks/:id/sharing/grants.
func grantRole(t *testing.T, srv *Server, deckID, userID uint64, role string, ownerCookies []*http.Cookie, ownerCSRF string) {
	t.Helper()
	if err := store.NewGrantStore(srv.db).Grant(context.Background(), deckID, userID, role, nil); err != nil {
		t.Fatalf("grant %s on deck %d to user %d: %v", role, deckID, userID, err)
	}
}

// revokeGrant has owner revoke userID's grant on that deck (also writing the grant table directly).
func revokeGrant(t *testing.T, srv *Server, deckID, userID uint64, ownerCookies []*http.Cookie, ownerCSRF string) {
	t.Helper()
	if err := store.NewGrantStore(srv.db).Revoke(context.Background(), deckID, userID); err != nil {
		t.Fatalf("revoke grant on deck %d from user %d: %v", deckID, userID, err)
	}
}

// mediaNoteRows returns the number of (media_sha, note_id) mapping rows, used to assert "no dirty data in the database when the write is refused".
func mediaNoteRows(t *testing.T, db *gorm.DB, sha string, noteID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&store.MediaNote{}).
		Where("media_sha = ? AND note_id = ?", sha, noteID).Count(&n).Error; err != nil {
		t.Fatalf("count media_notes: %v", err)
	}
	return n
}

// TestMediaAccessFollowsVisibleDeckGrantAndRevoke is the main acceptance:
// media referenced by a note in a private deck is 404 for a user without a grant; 200 once granted reader; 404 again immediately after revocation.
func TestMediaAccessFollowsVisibleDeckGrantAndRevoke(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "private media deck")
	mediaSha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	referenceMediaBy(t, db, deck.ID, &ownerID, mediaSha)
	target := "/media/" + mediaSha

	readerID, readerCookies, _ := createUserAndLogin(t, srv, db, "media-outsider")

	// 1) No grant: the deck is private and ungranted → not allowed even when referenced.
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("ungranted reader GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 2) Granted reader → readable on the next request (the mapping goes through the visible-deck predicate).
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusOK {
		t.Fatalf("granted reader GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 3) Revoked → takes effect immediately (no permission cache without invalidation).
	revokeGrant(t, srv, deck.ID, readerID, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after revoke reader GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestMediaAccessLapsesWhenReferencingNoteSoftDeleted covers "a soft-deleted note no longer grants":
// the visitor is neither the uploader nor a row outside the map; the only source is a reference from a note in a visible deck, and after that note is soft-deleted the read turns 404.
func TestMediaAccessLapsesWhenReferencingNoteSoftDeleted(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	// The note author is the uploader and the visitor (owner) is deliberately not the uploader, ensuring the pass can only come from the "mapping" branch.
	uploaderID, uploaderCookies, uploaderCSRF := createUserAndLogin(t, srv, db, "media-uploader")
	deck := seedDeck(t, db, ownerID, "private media deck")
	mediaSha := uploadAndSha(t, srv, uploaderCookies, uploaderCSRF, pngBody())
	note := referenceMediaBy(t, db, deck.ID, &uploaderID, mediaSha)
	target := "/media/" + mediaSha

	if rec := getWithCookies(t, srv, target, ownerCookies); rec.Code != http.StatusOK {
		t.Fatalf("owner GET referenced media = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if err := db.Delete(note).Error; err != nil {
		t.Fatalf("soft delete note: %v", err)
	}
	if rec := getWithCookies(t, srv, target, ownerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after note soft delete owner GET = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessUploaderPreviewWithoutReference covers rule (1): media the uploader has just uploaded and no card references yet
// is still readable (editor preview) — thanks to media_uploaders.
func TestMediaAccessUploaderPreviewWithoutReference(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	mediaSha := uploadAndSha(t, srv, cookies, csrf, pngBody())
	target := "/media/" + mediaSha
	if rec := getWithCookies(t, srv, target, cookies); rec.Code != http.StatusOK {
		t.Fatalf("uploader GET unreferenced upload = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessExactShaBoundary covers exact matching: two shas exist in the database and the note references only one of them.
// The unreferenced one (uploaded by someone else, invisible to owner) must be 404 — the mapping matches on the full 64-character sha, with no prefix matching.
func TestMediaAccessExactShaBoundary(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	_, otherCookies, otherCSRF := createUserAndLogin(t, srv, db, "media-boundary-other")
	deck := seedDeck(t, db, ownerID, "boundary deck")

	// Someone else uploads two media with different bytes; only the second is referenced by a note in a deck the owner can see.
	unreferenced := uploadAndSha(t, srv, otherCookies, otherCSRF, pngBody())
	referenced := uploadAndSha(t, srv, otherCookies, otherCSRF, append(pngBody(), 'x'))
	referenceMediaBy(t, db, deck.ID, nil, referenced)

	if rec := getWithCookies(t, srv, "/media/"+referenced, ownerCookies); rec.Code != http.StatusOK {
		t.Fatalf("owner GET referenced sha = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// The unreferenced sha exists (the uploader is someone else) and no note in a visible deck references it → 404.
	if rec := getWithCookies(t, srv, "/media/"+unreferenced, ownerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("owner GET unreferenced sha = %d, want 404 (must not match any other sha) (body %s)",
			rec.Code, rec.Body.String())
	}
}

// TestMediaAccessSharedDeckReferenceByOtherAuthorGrants guards against false negatives on "a reference written by someone else":
// A shares the deck with B (reader), and the note referencing A's media is written by A as well → B can still read it.
func TestMediaAccessSharedDeckReferenceByOtherAuthorGrants(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "shared media deck")
	mediaSha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	referenceMediaBy(t, db, deck.ID, &ownerID, mediaSha)
	target := "/media/" + mediaSha

	bID, bCookies, _ := createUserAndLogin(t, srv, db, "media-shared-reader")
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("ungranted reader GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
	grantRole(t, srv, deck.ID, bID, store.RoleReader, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("reader GET media referenced by another author's note = %d, want 200 (body %s)",
			rec.Code, rec.Body.String())
	}
}

// TestMediaAccessOwnUploadWithSelfReferenceGrants covers rule (1): media B uploaded in B's own deck
// is readable by B even when B's own note also references it (via media_uploaders).
func TestMediaAccessOwnUploadWithSelfReferenceGrants(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-selfupload")
	deckB := seedDeck(t, db, bID, "B upload deck")
	mediaSha := uploadAndSha(t, srv, bCookies, bCSRF, pngBody())
	referenceMediaBy(t, db, deckB.ID, &bID, mediaSha)
	target := "/media/" + mediaSha
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("uploader GET own referenced media = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessNullAuthorReferenceGrants pins down NULL handling: a note with created_by IS NULL
// (server import / system-generated) referencing media in a deck the reader can see still lets the reader read it (the mapping ignores the author).
func TestMediaAccessNullAuthorReferenceGrants(t *testing.T) {
	srv, db, _, ownerCookies, ownerCSRF := newNotesServer(t)
	mediaSha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	bID, bCookies, _ := createUserAndLogin(t, srv, db, "media-null-author")
	deckB := seedDeck(t, db, bID, "B null deck")
	referenceMediaBy(t, db, deckB.ID, nil, mediaSha)
	target := "/media/" + mediaSha
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("B GET media referenced by a NULL-author note = %d, want 200 (body %s)",
			rec.Code, rec.Body.String())
	}
}

// The pre-write validation assertion of TestMediaAccessResidualEditorInjectionIsDenied is now covered by internal/api's
// media_access_test.go (REST PATCH/POST is the only write transport); the web side keeps only the read-authorization cases.
// The cases below still cover the read-side behaviour of "the mapping only grants reads inside visible decks".
//
// TestMediaAccessUploaderKeepsAccessAfterGrantRevoked covers "after A revokes the share, B can still read media B uploaded":
// bytes B uploaded while the share was active gain permanent ownership through media_uploaders, and revoking the grant does not affect that.
func TestMediaAccessUploaderKeepsAccessAfterGrantRevoked(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "revoke uploader deck")
	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-revoked-uploader")
	grantRole(t, srv, deck.ID, bID, store.RoleEditor, ownerCookies, ownerCSRF)
	mediaB := uploadToDeckAndSha(t, srv, deck.PublicID, bCookies, bCSRF, pngBody())

	revokeGrant(t, srv, deck.ID, bID, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, "/media/"+mediaB, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("after revoke B GET own upload = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessDedupeHitRegistersEveryUploader covers a dedupe hit: B uploads media byte-identical to bytes C already has,
// and B gets a media_uploaders record too (idempotent, no duplicate rows). This is exactly the premise for "after A revokes the share, B can still read media B uploaded"
// holding under deduplication.
func TestMediaAccessDedupeHitRegistersEveryUploader(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	cID, cCookies, cCSRF := createUserAndLogin(t, srv, db, "media-dedupe-c")
	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-dedupe-b")

	body := pngBody()
	shaC := uploadAndSha(t, srv, cCookies, cCSRF, body)
	shaB := uploadAndSha(t, srv, bCookies, bCSRF, body)
	if shaC != shaB {
		t.Fatalf("dedupe failed: shas %s and %s differ", shaC, shaB)
	}

	var rows int64
	if err := db.Model(&store.MediaUploader{}).Where("media_sha = ?", shaB).Count(&rows).Error; err != nil {
		t.Fatalf("count media_uploaders: %v", err)
	}
	if rows != 2 {
		t.Fatalf("media_uploaders rows for deduped sha = %d, want 2 (both uploaders)", rows)
	}
	for _, uid := range []uint64{bID, cID} {
		var n int64
		if err := db.Model(&store.MediaUploader{}).
			Where("media_sha = ? AND user_id = ?", shaB, uid).Count(&n).Error; err != nil {
			t.Fatalf("count uploader %d: %v", uid, err)
		}
		if n != 1 {
			t.Errorf("uploader %d has %d rows for sha %s, want exactly 1", uid, n, shaB)
		}
	}
	// The uploader can read the bytes they uploaded (dedupe hit) even though someone else uploaded them first.
	if rec := getWithCookies(t, srv, "/media/"+shaB, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("dedupe uploader B GET = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessReferenceRemovalLapsesAccess proves the mapping is "rebuilt" rather than "appended":
// after A removes the media reference from a note and saves again, the old mapping rows must be cleared and the reader immediately loses read access.
func TestMediaAccessReferenceRemovalLapsesAccess(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "reference removal deck")
	mediaSha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	note := referenceMediaBy(t, db, deck.ID, &ownerID, mediaSha)
	target := "/media/" + mediaSha

	readerID, readerCookies, _ := createUserAndLogin(t, srv, db, "media-ref-removed-reader")
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusOK {
		t.Fatalf("reader GET before removal = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// A removes the reference and saves (same write method → mapping rebuilt).
	// The page-layer note write entry has been deleted; this goes straight through the store write method (the only place that rebuilds the mapping).
	if _, err := store.NewNoteStore(db).Update(context.Background(), note, map[string]any{
		"front": "no media any more", "back": "x",
	}); err != nil {
		t.Fatalf("save after removing reference: %v", err)
	}

	if n := mediaNoteRows(t, db, mediaSha, note.ID); n != 0 {
		t.Errorf("stale media_notes row survived reference removal: %d rows", n)
	}
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("reader GET after reference removal = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}
