package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Media mapping and readability decisions (settled -10-06).
//
// Two facts:
//   - media_notes: which media a note's fields reference (a derived index, rebuilt by the note write path);
//   - media_uploaders: who supplied these bytes (registered idempotently on upload and import, not revocable).
//
// Both read authorization (read side) and pre-write validation (write side) derive from these two facts, so that 'what counts as readable'
// cannot be implemented twice and drift.

// CodeMediaNotReadable is the stable English code returned when a note write is rejected by pre-write validation (item level, see MediaWriteError).
// It is also registered in the api's English error code table (internal/api's errorMessages, same value as CodeMediaNotReadable).
// By convention the error text is always English and not in the language packs, so neither pack has a matching error.* entry.
const CodeMediaNotReadable = "media_not_readable"

// MediaWriteError means a note write referenced media the writer cannot read, and the whole write was rejected.
//
// Entries lists the rejected references one by one (of the form media/<sha256>), isomorphic to the deck package's PackageError.Entries,
// so callers can both branch programmatically on Code and show the per-item reason to the user.
type MediaWriteError struct {
	Code    string
	Entries []string
}

func (e *MediaWriteError) Error() string {
	if len(e.Entries) == 0 {
		return e.Code
	}
	return e.Code + ": " + strings.Join(e.Entries, ", ")
}

// RecordMediaUploader idempotently registers 'userID supplied these bytes for sha'.
//
// Upload and import (every path that supplies bytes here) call it; registering again neither errors nor creates a duplicate row (primary key conflicts are ignored).
// It cannot be undone after the write: there is no delete path, since deleting would make 'supplied it' untrue.
func RecordMediaUploader(ctx context.Context, db *gorm.DB, mediaSha string, userID uint64) error {
	if db == nil || mediaSha == "" || userID == 0 {
		return nil
	}
	row := MediaUploader{MediaSha: mediaSha, UserID: userID, CreatedAt: time.Now().UTC()}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("store: record media uploader: %w", err)
	}
	return nil
}

// mediaRefsIn returns every media sha256 appearing in the field map (both URL form and in-package form are recognized).
// It reuses scanMediaRefs from media_quota.go, so reference recognition has only one implementation.
func mediaRefsIn(fields map[string]any) map[string]bool {
	refs := map[string]bool{}
	if fields == nil {
		return refs
	}
	scanMediaRefs(fields, refs)
	return refs
}

// mediaReadableByUser decides whether the media for sha may be read by userID (two branches).
//
// The necessary and sufficient conditions for allowing a read:
//  1. media_uploaders has a record pointing at userID (they supplied these bytes; a dedup hit counts, and it is irrevocable);
//  2. the media_notes mapping has a record pointing at 'a non-soft-deleted note inside a deck visible to userID'.
//
// It shares the same decision with the read endpoint MediaAccessibleToUser and with pre-write validation — 'readable' has only this one definition.
func mediaReadableByUser(ctx context.Context, db *gorm.DB, userID uint64, mediaSha string) (bool, error) {
	if db == nil || userID == 0 || mediaSha == "" {
		return false, nil
	}
	var uploads int64
	if err := db.WithContext(ctx).Table("(?) AS m", readableMediaUnionQuery(db, userID)).
		Where("m.sha256 = ?", mediaSha).
		Count(&uploads).Error; err != nil {
		return false, fmt.Errorf("store: count readable media: %w", err)
	}
	if uploads > 0 {
		return true, nil
	}
	return false, nil
}

// checkNewMediaRefs validates before the write that every 'newly introduced reference this time' is readable by the writer (actor).
//
// Rules ('validate at write time'):
//   - only validate shas that appear in newFields but not in oldFields (old references already passed validation, re-checking is pointless);
//   - validation is evaluated against the **state before the write**: callers must call it before writing media/mappings, otherwise the mapping satisfies itself the moment it is created;
//   - a reference whose bytes are not in the DB at all (no media row) passes: it can leak nothing, and it is the normal shape of existing flows such as
//     'export with media disabled, then import / reference media that has since been deleted';
//   - actor 0 means a server-side write (no named writer), and validation is skipped.
//
// Returns *MediaWriteError (item level, stable code) so callers can map it to an HTTP/MCP error.
func checkNewMediaRefs(ctx context.Context, db *gorm.DB, actor uint64, oldFields, newFields map[string]any) error {
	if actor == 0 {
		return nil
	}
	newRefs := mediaRefsIn(newFields)
	if len(newRefs) == 0 {
		return nil
	}
	oldRefs := mediaRefsIn(oldFields)
	var entries []string
	for sha := range newRefs {
		if oldRefs[sha] {
			continue
		}
		// first confirm the bytes really are in the DB; if not, there is no leak, so let it pass.
		var m Media
		err := db.WithContext(ctx).Select("sha256").First(&m, "sha256 = ?", sha).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("store: check media ref %s: %w", sha, err)
		}
		ok, err := mediaReadableByUser(ctx, db, actor, sha)
		if err != nil {
			return err
		}
		if !ok {
			entries = append(entries, "media/"+sha)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	sort.Strings(entries)
	return &MediaWriteError{Code: CodeMediaNotReadable, Entries: entries}
}

// rebuildMediaNotes rebuilds a note's media_notes mapping: it deletes every old row for that note, then inserts by the new fields.
//
// It must **rebuild** rather than append: appending would make 'removing a reference' no longer revoke read access — the old row stays in the table and
// read authorization still hits it. It runs in the same transaction, living and dying with the note write.
func rebuildMediaNotes(ctx context.Context, tx *gorm.DB, noteID uint64, fields map[string]any) error {
	if err := tx.WithContext(ctx).Where("note_id = ?", noteID).Delete(&MediaNote{}).Error; err != nil {
		return fmt.Errorf("store: clear media notes for note %d: %w", noteID, err)
	}
	refs := mediaRefsIn(fields)
	if len(refs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]MediaNote, 0, len(refs))
	for sha := range refs {
		rows = append(rows, MediaNote{MediaSha: sha, NoteID: noteID, CreatedAt: now})
	}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
		return fmt.Errorf("store: insert media notes for note %d: %w", noteID, err)
	}
	return nil
}
