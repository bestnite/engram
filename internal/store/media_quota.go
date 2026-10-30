package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// The accounting rule for the per-user media quota.
//
// 'Used media' = the sum of the bytes of the union of the two sets below, deduped by sha256:
//  1. media referenced by notes this user created and that are not soft-deleted (via the media_notes mapping, maintained by the note write path);
//  2. media this user uploaded but that **no note currently references** (pending uploads).
//
// Why count by 'reference': media is content-addressed by sha256, so the same bytes are stored only once instance-wide. Counting by uploader would attribute a shared
// blob only to its first uploader, and would make 'deleting your own reference' unable to free quota. Counting by reference gives: deleting or replacing a
// reference frees it (soft-deleting the note frees it too); one blob referenced several times by the same user counts once; when several users reference the same blob
// it counts once for each (logical attribution — physically there is still one copy on disk, so the quota is not disk usage).
//
// Why set 2 is counted too: counting only by reference would make a file uploaded and never inserted into any card free forever, so an account could
// repeatedly upload 'unreferenced files' and fill the disk. Pending uploads are charged to the uploader until a note references them (which moves them into
// set 1, charged to the referencer) or until media reclamation deletes them.
type MediaUsage struct {
	// Bytes is the deduped number of used media bytes.
	Bytes int64
	// Sha256 is the set of media this user is charged for (sha256 → true), used at upload time to decide whether a new file is already charged.
	Sha256 map[string]bool
}

// UserMediaUsage computes the user's used media (rule in the MediaUsage comment). Returns empty usage when db is nil.
// Both sets are subqueries over indexes, so the cost is independent of the user's total note count.
func UserMediaUsage(ctx context.Context, db *gorm.DB, userID uint64) (MediaUsage, error) {
	usage := MediaUsage{Sha256: map[string]bool{}}
	if db == nil || userID == 0 {
		return usage, nil
	}
	referenced := db.Table("media_notes AS mn").Select("mn.media_sha").
		Joins("JOIN notes AS n ON n.id = mn.note_id AND n.deleted_at IS NULL").
		Where("n.created_by = ?", userID)
	pending := db.Table("media_uploaders AS mu").Select("mu.media_sha").
		Where("mu.user_id = ?", userID).
		Where("NOT EXISTS (SELECT 1 FROM media_notes AS ref WHERE ref.media_sha = mu.media_sha)")
	var rows []Media
	if err := db.WithContext(ctx).Model(&Media{}).Select("sha256", "bytes").
		Where("sha256 IN (?) OR sha256 IN (?)", referenced, pending).
		Find(&rows).Error; err != nil {
		return usage, fmt.Errorf("store: compute media usage for user %d: %w", userID, err)
	}
	for _, m := range rows {
		usage.Sha256[m.Sha256] = true
		usage.Bytes += m.Bytes
	}
	return usage, nil
}
