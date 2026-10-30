package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
//
// Field values are HTML that refers to media by file name, the way Anki does: an img/video/audio src attribute, a [sound:name]
// tag and a CSS url(name) are the three forms a field or template can carry. A name resolves to content through media_names
// for the note's deck; a name with no row refers to no stored bytes and contributes no reference.

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

// Field media reference patterns. Each one captures the file name a field writes; every other part of the match is skipped.
var (
	// mediaSrcRE matches an HTML src attribute in any of the three valid quoting forms.
	mediaSrcRE = regexp.MustCompile(`(?is)\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	// mediaSoundRE matches Anki's [sound:name] tag.
	mediaSoundRE = regexp.MustCompile(`(?is)\[sound:([^\]]+)\]`)
	// mediaCSSURLRE matches a CSS url(...) reference in any of the three valid quoting forms.
	mediaCSSURLRE = regexp.MustCompile(`(?is)\burl\(\s*(?:"([^"]*)"|'([^']*)'|([^)\s]*))\s*\)`)
	// mediaShaNameRE matches a name that already carries the content sha, the form the migration writes (<sha>.<ext>).
	mediaShaNameRE = regexp.MustCompile(`^([0-9a-f]{64})(?:\.[^/]*)?$`)
)

// mediaNamesInHTML returns every media name a piece of field HTML refers to, in order of appearance and without duplicates.
//
// A reference that is an absolute URL (any scheme, or a root-relative path) is not a deck-scoped file name and is skipped:
// the frame may load an external https image, but that image is not this deck's media.
func mediaNamesInHTML(html string) []string {
	if !strings.Contains(html, "src") && !strings.Contains(html, "[sound:") && !strings.Contains(html, "url(") {
		return nil
	}
	var names []string
	seen := make(map[string]struct{})
	add := func(raw string) {
		name := strings.TrimSpace(raw)
		name = strings.TrimPrefix(name, "./")
		if name == "" || strings.Contains(name, "://") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "data:") {
			return
		}
		name = normaliseMediaName(name)
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, match := range mediaSrcRE.FindAllStringSubmatch(html, -1) {
		add(match[1] + match[2] + match[3])
	}
	for _, match := range mediaSoundRE.FindAllStringSubmatch(html, -1) {
		add(match[1])
	}
	for _, match := range mediaCSSURLRE.FindAllStringSubmatch(html, -1) {
		add(match[1] + match[2] + match[3])
	}
	return names
}

// mediaRefsIn resolves every media reference in the note's field values to a content sha256.
//
// A name that media_names does not know contributes no reference: the lookup is what makes 'the note references these bytes'
// true, so an unknown name can neither leak a medium nor authorize a read.
func mediaRefsIn(ctx context.Context, db *gorm.DB, deckID uint64, values map[string]string) (map[string]bool, error) {
	refs := map[string]bool{}
	var names []string
	for _, html := range values {
		names = append(names, mediaNamesInHTML(html)...)
	}
	if len(names) == 0 {
		return refs, nil
	}
	// A name that already carries the sha (<sha>.<ext>) needs no lookup; every other name is resolved in one query.
	pending := make([]string, 0, len(names))
	for _, name := range names {
		if match := mediaShaNameRE.FindStringSubmatch(name); match != nil {
			refs[match[1]] = true
			continue
		}
		pending = append(pending, name)
	}
	if len(pending) == 0 {
		return refs, nil
	}
	var rows []MediaName
	if err := db.WithContext(ctx).Where("deck_id = ? AND name IN ?", deckID, pending).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("store: resolve media names for deck %d: %w", deckID, err)
	}
	for _, row := range rows {
		refs[row.Sha256] = true
	}
	return refs, nil
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
//   - only validate shas that appear in newValues but not in oldValues (old references already passed validation, re-checking is pointless);
//   - validation is evaluated against the **state before the write**: callers must call it before writing media/mappings, otherwise the mapping satisfies itself the moment it is created;
//   - a reference whose bytes are not in the DB at all (no media row) passes: it can leak nothing, and it is the normal shape of existing flows such as
//     'export with media disabled, then import / reference media that has since been deleted';
//   - actor 0 means a server-side write (no named writer), and validation is skipped.
//
// Returns *MediaWriteError (item level, stable code) so callers can map it to an HTTP/MCP error.
func checkNewMediaRefs(ctx context.Context, db *gorm.DB, actor, deckID uint64, oldValues, newValues map[string]string) error {
	if actor == 0 {
		return nil
	}
	newRefs, err := mediaRefsIn(ctx, db, deckID, newValues)
	if err != nil {
		return err
	}
	if len(newRefs) == 0 {
		return nil
	}
	oldRefs := map[string]bool{}
	if len(oldValues) > 0 {
		if oldRefs, err = mediaRefsIn(ctx, db, deckID, oldValues); err != nil {
			return err
		}
	}
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

// rebuildMediaNotes rebuilds a note's media_notes mapping: it deletes every old row for that note, then inserts the references
// of the new field values.
//
// It must **rebuild** rather than append: appending would make 'removing a reference' no longer revoke read access — the old row stays in the table and
// read authorization still hits it. It runs in the same transaction, living and dying with the note write.
func rebuildMediaNotes(ctx context.Context, tx *gorm.DB, noteID, deckID uint64, values map[string]string) error {
	if err := tx.WithContext(ctx).Where("note_id = ?", noteID).Delete(&MediaNote{}).Error; err != nil {
		return fmt.Errorf("store: clear media notes for note %d: %w", noteID, err)
	}
	refs, err := mediaRefsIn(ctx, tx, deckID, values)
	if err != nil {
		return err
	}
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
