package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Media library listing (paged) — the read side reuses the same decision, exactly the same rule as media reads.
//
// Why the listing needs a cap: it serves the 'pick an image to insert into a card' scenario, and pulling the whole library into one page is both slow and uses memory
// as a buffer. The cap is hard-coded here so REST, the web picker and future callers share one rule.
const (
	// DefaultMediaPageSize is the default page size when no limit is given.
	DefaultMediaPageSize = 24
	// MaxMediaPageSize is the maximum rows per page; anything above it is clamped to it (the REST layer returns 400 for over-limit values).
	MaxMediaPageSize = 100
)

// ErrInvalidMediaCursor means the cursor string cannot be decoded. The REST layer returns 400 on it rather than silently starting over
// — silently restarting turns 'paging' into 'seeing the first page over and over', which callers cannot notice.
var ErrInvalidMediaCursor = errors.New("store: invalid media cursor")

// mediaBaseColumns are the columns both union branches select; the order must match for UNION.
const mediaBaseColumns = "media.sha256, media.rel_path, media.mime, media.bytes, media.width, media.height, media.created_by, media.created_at"

// readableMediaUnionQuery returns the union subquery of 'media readable by a user' (two sources UNION-deduped):
//  1. media_uploaders has a record pointing at userID — they supplied these bytes (a dedup hit counts, irrevocable);
//  2. media_notes JOIN notes (not soft-deleted) and the note's deck falls inside the visibleDeckIDsQuery range.
//
// It is the single source of 'the readable media set', shared by the read decision (the first two branches of mediaReadableByUser) and the listing,
// so the SQL is not written twice and cannot drift. The third branch (reading through a share session) is open only to the /media proxy path;
// the picker and the REST listing are both used by the logged-in user themselves and do not include it.
//
// UNION (rather than UNION ALL) ensures the same bytes hit on both paths appear only once.
func readableMediaUnionQuery(db *gorm.DB, userID uint64) *gorm.DB {
	uploaded := db.Model(&Media{}).Select(mediaBaseColumns).
		Joins("JOIN media_uploaders u ON u.media_sha = media.sha256").
		Where("u.user_id = ?", userID)
	referenced := db.Model(&Media{}).Select(mediaBaseColumns).
		Joins("JOIN media_notes mn ON mn.media_sha = media.sha256").
		Joins("JOIN notes n ON n.id = mn.note_id AND n.deleted_at IS NULL").
		Where("n.deck_id IN (?)", visibleDeckIDsQuery(db, userID))
	return db.Raw("? UNION ?", uploaded, referenced)
}

// ListReadableMedia returns media readable by userID by keyset, ordered media.created_at DESC,
// media.sha256 DESC; it returns this page's rows and the next page cursor (an empty string means the end).
//
// The sort key includes the primary key sha256, so the total order is unique: repeating a request with the same cursor yields the same page, with no duplicates and no gaps.
// Invariant: limit is clamped to [1, MaxMediaPageSize] (illegal values are handled as 400 by the caller; this is only a safety net).
func ListReadableMedia(ctx context.Context, db *gorm.DB, userID uint64, limit int, cursor string) ([]Media, string, error) {
	if db == nil || userID == 0 {
		return nil, "", nil
	}
	if limit < 1 {
		limit = DefaultMediaPageSize
	}
	if limit > MaxMediaPageSize {
		limit = MaxMediaPageSize
	}
	q := db.WithContext(ctx).Table("(?) AS m", readableMediaUnionQuery(db, userID))
	if cursor != "" {
		at, sha, err := decodeMediaCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		// strictly less than the previous page's last item: compare created_at first, then sha256 for the same instant.
		q = q.Where("m.created_at < ? OR (m.created_at = ? AND m.sha256 < ?)", at, at, sha)
	}
	// fetch one extra row to tell whether there is a next page, and drop it from the result.
	var rows []Media
	if err := q.Order("m.created_at DESC, m.sha256 DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", fmt.Errorf("store: list readable media: %w", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = encodeMediaCursor(rows[len(rows)-1])
	}
	return rows, next, nil
}

// encodeMediaCursor encodes a row's sort key into an opaque cursor: base64url(created_at_unix_nano:sha256).
func encodeMediaCursor(m Media) string {
	raw := strconv.FormatInt(m.CreatedAt.UTC().UnixNano(), 10) + ":" + m.Sha256
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeMediaCursor parses a cursor; any badly shaped input returns ErrInvalidMediaCursor.
func decodeMediaCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", ErrInvalidMediaCursor
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return time.Time{}, "", ErrInvalidMediaCursor
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", ErrInvalidMediaCursor
	}
	if !isMediaSha(parts[1]) {
		return time.Time{}, "", ErrInvalidMediaCursor
	}
	return time.Unix(0, nanos).UTC(), parts[1], nil
}

// isMediaSha validates 64 lowercase hex digits, matching the shape required by the web layer's /media/:sha.
func isMediaSha(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
