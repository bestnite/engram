package store

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

// This file covers the L3 share session grant (share_session.go):
//   - after registering, the third branch of read authorization allows access, and only for the session that registered it (session isolation);
//   - once the grant expires it no longer allows access; RemoveExpired reclaims it;
//   - when a session is revoked (logout, signed out by a password change, disabled) that session's grant rows are deleted with it.

// seedSession inserts one server-side session row for the grant and session-reclamation cases.
func seedSession(t *testing.T, db *gorm.DB, userID uint64, id string, expiresAt time.Time) {
	t.Helper()
	if err := NewSessionStore(db).Create(context.Background(), &Session{
		ID: id, UserID: userID, CSRFToken: "csrf-" + id,
		CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("create session %s: %v", id, err)
	}
}

// shareGrantCount counts the grant rows of a session.
func shareGrantCount(t *testing.T, db *gorm.DB, sessionID string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&ShareSessionDeck{}).Where("session_id = ?", sessionID).Count(&n).Error; err != nil {
		t.Fatalf("count share_session_decks for %s: %v", sessionID, err)
	}
	return n
}

// TestShareSessionGrantAuthorizesMediaForItsSessionOnly covers the third read-side branch and session isolation:
// the share grant allows only the session that registered it; another session of the same user and a call without a session are both denied.
func TestShareSessionGrantAuthorizesMediaForItsSessionOnly(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "share_owner", "share_visitor")
			owner, visitor := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("share-session"))
			saveRefNote(t, db, deckID, Ptr(owner), refFront(sha))

			now := time.Now().UTC()
			seedSession(t, db, visitor, "sess-open", now.Add(time.Hour))
			seedSession(t, db, visitor, "sess-other", now.Add(time.Hour))
			// The deck must still have an active share link for the read to be allowed (revocation/expiry takes effect at once, see share_session.go).
			if _, _, err := NewShareLinkStore(db).Create(ctx, ShareLinkInput{DeckID: deckID, CreatedBy: owner}); err != nil {
				t.Fatalf("create share link: %v", err)
			}
			if err := NewShareSessionStore(db).Grant(ctx, "sess-open", deckID, nil, now.Add(time.Hour)); err != nil {
				t.Fatalf("Grant() error = %v", err)
			}

			if ok, err := MediaAccessibleToUser(ctx, db, visitor, "sess-open", sha); err != nil || !ok {
				t.Fatalf("opening session: readable=%v err=%v, want true", ok, err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, visitor, "sess-other", sha); err != nil || ok {
				t.Fatalf("other session: readable=%v err=%v, want false", ok, err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, visitor, "", sha); err != nil || ok {
				t.Fatalf("no session: readable=%v err=%v, want false", ok, err)
			}
		})
	}
}

// TestShareSessionGrantLapsesWhenExpired covers expiry: once the grant row expires the read becomes false, and RemoveExpired clears it.
func TestShareSessionGrantLapsesWhenExpired(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "expiry_owner", "expiry_visitor")
			owner, visitor := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("share-expiry"))
			saveRefNote(t, db, deckID, Ptr(owner), refFront(sha))
			// The link does not expire, so the "expired" branch can only come from the grant row itself.
			if _, _, err := NewShareLinkStore(db).Create(ctx, ShareLinkInput{DeckID: deckID, CreatedBy: owner}); err != nil {
				t.Fatalf("create share link: %v", err)
			}
			seedSession(t, db, visitor, "sess-expired", time.Now().UTC().Add(time.Hour))

			past := time.Now().UTC().Add(-time.Hour)
			if err := NewShareSessionStore(db).Grant(ctx, "sess-expired", deckID, &past, time.Now().UTC().Add(time.Hour)); err != nil {
				t.Fatalf("Grant() error = %v", err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, visitor, "sess-expired", sha); err != nil || ok {
				t.Fatalf("expired grant: readable=%v err=%v, want false", ok, err)
			}

			removed, err := NewShareSessionStore(db).RemoveExpired(ctx, time.Now().UTC())
			if err != nil {
				t.Fatalf("RemoveExpired() error = %v", err)
			}
			if removed != 1 {
				t.Errorf("RemoveExpired() removed %d rows, want 1", removed)
			}
			if n := shareGrantCount(t, db, "sess-expired"); n != 0 {
				t.Errorf("expired grant row survived RemoveExpired: %d rows", n)
			}
		})
	}
}

// TestShareSessionGrantRemovedWhenSessionRevoked covers the cleanup: logout (Revoke) and the bulk revocations
// (RevokeAllForUserExcept / RevokeAllForUser) must all delete the grant rows of the revoked sessions.
func TestShareSessionGrantRemovedWhenSessionRevoked(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "revoke_owner", "revoke_visitor")
			owner, visitor := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			store := NewShareSessionStore(db)
			now := time.Now().UTC()

			// 1) Logging out a single session: only that session's rows go.
			seedSession(t, db, visitor, "sess-logout", now.Add(time.Hour))
			seedSession(t, db, visitor, "sess-keep", now.Add(time.Hour))
			for _, sid := range []string{"sess-logout", "sess-keep"} {
				if err := store.Grant(ctx, sid, deckID, nil, now.Add(time.Hour)); err != nil {
					t.Fatalf("Grant(%s) error = %v", sid, err)
				}
			}
			if err := NewSessionStore(db).Revoke(ctx, "sess-logout", now); err != nil {
				t.Fatalf("Revoke() error = %v", err)
			}
			if n := shareGrantCount(t, db, "sess-logout"); n != 0 {
				t.Errorf("revoked session still has %d grants, want 0", n)
			}
			if n := shareGrantCount(t, db, "sess-keep"); n != 1 {
				t.Errorf("kept session has %d grants, want 1", n)
			}

			// 2) A password change keeping the current session: the other sessions' grants are deleted, the current session's is kept.
			if err := NewSessionStore(db).RevokeAllForUserExcept(ctx, visitor, "sess-keep", now); err != nil {
				t.Fatalf("RevokeAllForUserExcept() error = %v", err)
			}
			if n := shareGrantCount(t, db, "sess-keep"); n != 1 {
				t.Errorf("kept session lost its grant: %d rows, want 1", n)
			}

			// 3) Revoking everything (disabling/forced sign-out): all grants of that user's sessions are cleared.
			if err := store.Grant(ctx, "sess-keep", deckID, nil, now.Add(time.Hour)); err != nil {
				t.Fatalf("Grant() error = %v", err)
			}
			if err := NewSessionStore(db).RevokeAllForUser(ctx, visitor, now); err != nil {
				t.Fatalf("RevokeAllForUser() error = %v", err)
			}
			if n := shareGrantCount(t, db, "sess-keep"); n != 0 {
				t.Errorf("after RevokeAllForUser: %d grants, want 0", n)
			}
			// The owner's session is unaffected (a different user).
			seedSession(t, db, owner, "sess-owner", now.Add(time.Hour))
			if err := store.Grant(ctx, "sess-owner", deckID, nil, now.Add(time.Hour)); err != nil {
				t.Fatalf("Grant(owner) error = %v", err)
			}
			if err := NewSessionStore(db).RevokeAllForUser(ctx, visitor, now); err != nil {
				t.Fatalf("RevokeAllForUser() second call error = %v", err)
			}
			if n := shareGrantCount(t, db, "sess-owner"); n != 1 {
				t.Errorf("other user's grant was removed: %d rows, want 1", n)
			}
		})
	}
}
