package store

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// The user-store extension that user management in the admin panel needs.
//
// It lives in a separate file rather than being stuffed into user.go / models.go: those two are single-writer hotspots for other milestones
// and should not be touched while work is running in parallel. Only new methods are added here; no existing behaviour changes.

// AdminListUsersPageSize is the per-page row count of the admin panel's user list; it doubles as the cap for one render,
// so the whole user table is never drawn in one go ("pagination or a cap").
const AdminListUsersPageSize = 50

// ListForAdmin pages through users by search term. An empty query means no filter; it matches username, email or display name.
// Searches always compare LOWER(...): SQLite's LIKE is case-insensitive for ASCII by default while PostgreSQL's is not,
// and only lowering both sides makes the two databases behave the same (the two-database convention).
// The query is lowered in Go and its LIKE wildcards are escaped, so "_" or "%" in a search matches only that character.
func (s *UserStore) ListForAdmin(ctx context.Context, query string, page, size int) ([]User, int64, error) {
	if size <= 0 || size > AdminListUsersPageSize {
		size = AdminListUsersPageSize
	}
	if page < 1 {
		page = 1
	}
	q := s.db.WithContext(ctx).Model(&User{})
	if query != "" {
		like := "%" + escapeLike(strings.ToLower(query)) + "%"
		q = q.Where("LOWER(username) LIKE ? ESCAPE '\\' OR LOWER(email) LIKE ? ESCAPE '\\' OR LOWER(display_name) LIKE ? ESCAPE '\\'",
			like, like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count users for admin: %w", err)
	}
	var users []User
	if err := q.Order("id ASC").Limit(size).Offset((page - 1) * size).Find(&users).Error; err != nil {
		return nil, 0, fmt.Errorf("list users for admin: %w", err)
	}
	return users, total, nil
}

// ListAdmins returns every admin account that can still sign in (role = admin and status = active), ordered by id ascending.
//
// The (category D) admin notification resolves its recipients from this: the rule is to use the login email only, with no separate recipient
// mailbox, so the recipients are simply these accounts' Email. Only active admins are taken: a disabled account cannot receive mail and should not
// be treated as a delivery target.
func (s *UserStore) ListAdmins(ctx context.Context) ([]User, error) {
	var users []User
	if err := s.db.WithContext(ctx).
		Where("role = ? AND status = ?", RoleAdmin, StatusActive).
		Order("id ASC").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("list admins: %w", err)
	}
	return users, nil
}

// SetRole changes only the role column, so a whole-row overwrite cannot lose a concurrent write.
func (s *UserStore) SetRole(ctx context.Context, id uint64, role string) error {
	if err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Update("role", role).Error; err != nil {
		return fmt.Errorf("set user role: %w", err)
	}
	return nil
}

// UserUsage is one user's usage counts (decks, cards, reviews), shown in the admin panel list.
type UserUsage struct {
	Decks   int64
	Cards   int64
	Reviews int64
}

// UsageCounts counts the decks a user owns, the live cards in those decks, and their review count.
func (s *UserStore) UsageCounts(ctx context.Context, userID uint64) (UserUsage, error) {
	var u UserUsage
	db := s.db.WithContext(ctx)
	if err := db.Model(&Deck{}).Where("owner_user_id = ?", userID).Count(&u.Decks).Error; err != nil {
		return u, fmt.Errorf("count decks for user: %w", err)
	}
	// Only non-soft-deleted notes / cards count, matching the visible set of the review page.
	if err := db.Raw(`SELECT COUNT(*) FROM cards c
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		JOIN decks d ON d.id = n.deck_id
		WHERE d.owner_user_id = ? AND c.deleted_at IS NULL`, userID).Scan(&u.Cards).Error; err != nil {
		return u, fmt.Errorf("count cards for user: %w", err)
	}
	if err := db.Model(&Review{}).Where("user_id = ?", userID).Count(&u.Reviews).Error; err != nil {
		return u, fmt.Errorf("count reviews for user: %w", err)
	}
	return u, nil
}

// DeleteUser deletes a user and the data they own outright.
//
// The users table is referenced by a great many tables (sessions, identities, keys, progress, reviews, grants, decks, presets, ...),
// so a single DELETE would be stopped by a foreign key on Postgres and would leave orphan rows on SQLite; hence the explicit cleanup
// in dependency order inside one transaction. Audit rows are deliberately kept: the deletion itself must leave a trace (who deleted whom).
func (s *UserStore) DeleteUser(ctx context.Context, userID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return DeleteUserTx(ctx, tx, userID)
	})
}

// userCleanup is one statement run when deleting a user; args is the number of placeholders in the statement, all bound to the deleted user's id.
type userCleanup struct {
	stmt string
	args int
}

// userCleanups lists every row to handle when deleting a user, in three groups:
//   - rows that belong only to this user: delete;
//   - nullable columns in someone else's content recording "created / modified by this user": null them out, leaving the content to its owner;
//   - media: media rows are **not** deleted. Media is de-duplicated by content, the same bytes may be referenced by someone else's note, and deleting the row would break their
//     image and turn the file into an orphan without metadata. Only the user's "uploader" records are deleted; unreferenced media is handled by the reclamation job.
var userCleanups = []userCleanup{
	{"DELETE FROM sessions WHERE user_id = ?", 1},
	{"DELETE FROM identities WHERE user_id = ?", 1},
	{"DELETE FROM api_keys WHERE user_id = ?", 1},
	{"DELETE FROM card_states WHERE user_id = ?", 1},
	{"DELETE FROM reviews WHERE user_id = ?", 1},
	{"DELETE FROM deck_grants WHERE user_id = ?", 1},
	{"DELETE FROM deck_member_settings WHERE user_id = ?", 1},
	{"DELETE FROM deck_daily_extras WHERE user_id = ?", 1},
	{"DELETE FROM deck_share_invites WHERE user_id = ? OR invited_by = ?", 2},
	{"DELETE FROM share_allow WHERE from_user_id = ? OR to_user_id = ?", 2},
	{"DELETE FROM user_totp WHERE user_id = ?", 1},
	{"DELETE FROM totp_recovery_codes WHERE user_id = ?", 1},
	{"DELETE FROM email_prefs WHERE user_id = ?", 1},
	{"DELETE FROM reminder_log WHERE user_id = ?", 1},
	{"DELETE FROM digest_log WHERE user_id = ?", 1},
	{"DELETE FROM action_tokens WHERE user_id = ?", 1},
	{"DELETE FROM login_fingerprints WHERE user_id = ?", 1},
	{"DELETE FROM media_uploaders WHERE user_id = ?", 1},
	{"UPDATE deck_grants SET created_by = NULL WHERE created_by = ?", 1},
	{"UPDATE notes SET created_by = NULL WHERE created_by = ?", 1},
	{"UPDATE invites SET created_by = NULL WHERE created_by = ?", 1},
	{"UPDATE invites SET used_by = NULL WHERE used_by = ?", 1},
	{"UPDATE media SET created_by = NULL WHERE created_by = ?", 1},
	{"UPDATE settings SET updated_by = NULL WHERE updated_by = ?", 1},
	{"UPDATE mail_templates SET updated_by = NULL WHERE updated_by = ?", 1},
}

// DeleteUserTx runs DeleteUser's cleanup inside the transaction given by the caller (the admin gate is decided first, in the same transaction).
//
// The decks owned by the user go through the same cascade as DeckStore.Delete (deleteDeckTx): other people's grants on those decks,
// progress, pending invites and media mappings are all cleared with them, rather than deleting only cards and notes and leaving dangling rows.
func DeleteUserTx(ctx context.Context, tx *gorm.DB, userID uint64) error {
	tx = tx.WithContext(ctx)
	var deckIDs []uint64
	if err := tx.Model(&Deck{}).Where("owner_user_id = ?", userID).Pluck("id", &deckIDs).Error; err != nil {
		return fmt.Errorf("delete user data: list owned decks: %w", err)
	}
	for _, id := range deckIDs {
		if err := deleteDeckTx(tx, id); err != nil {
			return fmt.Errorf("delete user data: deck %d: %w", id, err)
		}
	}
	for _, c := range userCleanups {
		args := make([]any, c.args)
		for i := range args {
			args[i] = userID
		}
		if err := tx.Exec(c.stmt, args...).Error; err != nil {
			return fmt.Errorf("delete user data: %w", err)
		}
	}
	// Presets are deleted after decks (decks reference presets), and the user row itself last.
	for _, stmt := range []string{"DELETE FROM presets WHERE owner_user_id = ?", "DELETE FROM users WHERE id = ?"} {
		if err := tx.Exec(stmt, userID).Error; err != nil {
			return fmt.Errorf("delete user data: %w", err)
		}
	}
	return nil
}
