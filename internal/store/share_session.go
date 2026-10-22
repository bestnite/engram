package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Share session grant (L3): decks a signed-in user opens through a share link join their session-level visible set,
// and media reads (the third branch of mediaReadableByUser) are allowed on that basis.
//
// Why it is recorded on the server-side session rather than in a cookie: a session is server-side state (the sessions table), and logout/password change/disabling all
// revoke it, so the grant must vanish with it; a signed cookie could not be revoked immediately.
//
// Why the key is (session_id, deck_id) and not (user_id, deck_id): a share link's visibility belongs only to "the session that opened
// it"; the same user on another session (another device, another browser) must not inherit it, and the primary key enforces session isolation by construction.
//
// The grant row only mirrors "this session opened this deck" and carries no token itself: a read also requires that the deck still has one
// active share link (see shareSessionDeckIDsQuery), so once the link is revoked or expires, reading stops working at once
// ("can be revoked immediately", "revocation/expiry takes effect immediately").

// ShareSessionGrantMaxTTL is the fallback cap for a share session grant when neither the link nor the session supplies an expiry.
//
// Rationale: no grant should live forever. This cap equals the default session TTL (auth.DefaultSessionTTL = 30 days),
// that is, the longest a single session can live; even if both timestamps are missing (a defensive branch the normal path never reaches),
// a grant cannot outlive the session that created it.
const ShareSessionGrantMaxTTL = 30 * 24 * time.Hour

// ShareSessionStore wraps share_session_decks: registering grants and reclaiming expired ones.
type ShareSessionStore struct {
	db *gorm.DB
}

// NewShareSessionStore builds the share session grant store.
func NewShareSessionStore(db *gorm.DB) *ShareSessionStore { return &ShareSessionStore{db: db} }

// Grant records that sessionID opened deckID through a share link; the expiry is the earlier of the link expiry and the session expiry.
//
// Expired rows are cleaned up along the way before the write: an expired grant can never let a request through again and is pure garbage; the cleanup hangs off this relatively cold
// but always triggered write point, avoiding yet another background job that would "never run".
func (s *ShareSessionStore) Grant(ctx context.Context, sessionID string, deckID uint64, linkExpiresAt *time.Time, sessionExpiresAt time.Time) error {
	if s == nil || s.db == nil || sessionID == "" || deckID == 0 {
		return nil
	}
	now := time.Now().UTC()
	if _, err := s.RemoveExpired(ctx, now); err != nil {
		return err
	}
	expiry := ShareSessionGrantExpiry(linkExpiresAt, sessionExpiresAt, now)
	row := ShareSessionDeck{SessionID: sessionID, DeckID: deckID, ExpiresAt: expiry}
	// Opening the same link repeatedly is normal (a page refresh): upsert on the primary key, no second row, no error.
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}, {Name: "deck_id"}},
		DoUpdates: clause.Assignments(map[string]any{"expires_at": expiry}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("store: grant share session deck: %w", err)
	}
	return nil
}

// RemoveExpired deletes every expired grant row and returns the number deleted.
func (s *ShareSessionStore) RemoveExpired(ctx context.Context, now time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res := s.db.WithContext(ctx).Where("expires_at <= ?", now).Delete(&ShareSessionDeck{})
	if res.Error != nil {
		return 0, fmt.Errorf("store: remove expired share session decks: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// ShareSessionGrantExpiry computes the expiry of one share grant row.
//
// Rule: the earlier of the link expiry and the session expiry -- when either lapses, the grant lapses.
// Both may be empty/zero (on the normal path the session expiry always exists): then the call time plus ShareSessionGrantMaxTTL
// is used as the fallback, so a row that never expires is never written.
func ShareSessionGrantExpiry(linkExpiresAt *time.Time, sessionExpiresAt, now time.Time) time.Time {
	expiry := now.Add(ShareSessionGrantMaxTTL)
	if linkExpiresAt != nil && !linkExpiresAt.IsZero() && linkExpiresAt.Before(expiry) {
		expiry = *linkExpiresAt
	}
	if !sessionExpiresAt.IsZero() && sessionExpiresAt.Before(expiry) {
		expiry = sessionExpiresAt
	}
	return expiry
}

// shareSessionDeckIDsQuery returns the deck-id subquery for "this session holds unexpired access through a share link".
//
// Both conditions are required:
//   - the session has an unexpired grant row on that deck (written by Grant);
//   - the deck still has an active share link (not revoked, not expired).
//
// The second makes "revocation/expiry takes effect immediately" hold: the grant row carries no token and only mirrors "opened this deck";
// if the deck has no live link any more, reading fails at once, even if that session opened it successfully before.
func shareSessionDeckIDsQuery(db *gorm.DB, sessionID string, now time.Time) *gorm.DB {
	active := db.Model(&ShareLink{}).Select("deck_id").
		Where("revoked_at IS NULL").
		Where("(expires_at IS NULL OR expires_at > ?)", now)
	return db.Model(&ShareSessionDeck{}).Select("deck_id").
		Where("session_id = ? AND expires_at > ?", sessionID, now).
		Where("deck_id IN (?)", active)
}

// removeShareGrantsForSessionTx deletes all grant rows of one session (called when a session is revoked, see session.go).
func removeShareGrantsForSessionTx(ctx context.Context, tx *gorm.DB, sessionID string) error {
	if tx == nil || sessionID == "" {
		return nil
	}
	if err := tx.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&ShareSessionDeck{}).Error; err != nil {
		return fmt.Errorf("store: remove share grants for session: %w", err)
	}
	return nil
}

// removeShareGrantsForUserTx deletes the grant rows of all of a user's sessions; when keepSessionID is non-empty that session's rows are kept.
func removeShareGrantsForUserTx(ctx context.Context, tx *gorm.DB, userID uint64, keepSessionID string) error {
	if tx == nil || userID == 0 {
		return nil
	}
	sessions := tx.Model(&Session{}).Select("id").Where("user_id = ?", userID)
	q := tx.WithContext(ctx).Where("session_id IN (?)", sessions)
	if keepSessionID != "" {
		q = q.Where("session_id <> ?", keepSessionID)
	}
	if err := q.Delete(&ShareSessionDeck{}).Error; err != nil {
		return fmt.Errorf("store: remove share grants for user sessions: %w", err)
	}
	return nil
}
