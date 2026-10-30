package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.nite07.com/nite/engram/internal/mediatype"
)

// mediaDeckName is the one rule that turns a content sha and a mime into a deck-scoped file name:
// "<sha256>.<ext>", with mediatype.Ext falling back to "bin" for an unknown or missing mime.
//
// Both the note write path and the note-type migration use it, so a name written by one is the
// name the other would choose for the same bytes.
func mediaDeckName(sha, mime string) string {
	return sha + "." + mediatype.Ext(mime)
}

// normaliseMediaName is the single normalisation rule for a deck-scoped media file name.
//
// Unicode equivalence: the composed and the decomposed spelling of the same name must resolve to one row, so every writer
// and every lookup passes the name through NFC before it reaches the database. Keeping the rule in one function means a
// writer cannot store a spelling a reader would not find.
func normaliseMediaName(name string) string {
	return norm.NFC.String(name)
}

// MediaNameStore wraps GORM access to the media_names table.
type MediaNameStore struct {
	db *gorm.DB
}

// NewMediaNameStore builds the media name store.
func NewMediaNameStore(db *gorm.DB) *MediaNameStore { return &MediaNameStore{db: db} }

// Upsert registers that deckID refers to the media content sha256 under name.
//
// A repeated (deck_id, name) reassigns the row to the new sha256 instead of inserting a second row: the schema's primary key
// is the unique index, and clause.OnConflict expresses the upsert identically on SQLite and PostgreSQL.
func (s *MediaNameStore) Upsert(ctx context.Context, deckID uint64, name, sha256 string) error {
	name = normaliseMediaName(name)
	if deckID == 0 {
		return errors.New("register media name: deck is required")
	}
	if name == "" {
		return errors.New("register media name: name is required")
	}
	if sha256 == "" {
		return errors.New("register media name: sha256 is required")
	}
	row := MediaName{DeckID: deckID, Name: name, Sha256: sha256}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "deck_id"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"sha256"}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("register media name %q for deck %d: %w", name, deckID, err)
	}
	return nil
}

// ByName resolves a file name within a deck; the lookup name is NFC-normalised exactly like a stored one.
func (s *MediaNameStore) ByName(ctx context.Context, deckID uint64, name string) (*MediaName, error) {
	var row MediaName
	if err := s.db.WithContext(ctx).Where("deck_id = ? AND name = ?", deckID, normaliseMediaName(name)).
		First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// ListByDeck lists a deck's media names ordered by name.
func (s *MediaNameStore) ListByDeck(ctx context.Context, deckID uint64) ([]MediaName, error) {
	var rows []MediaName
	if err := s.db.WithContext(ctx).Where("deck_id = ?", deckID).
		Order("name ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list media names for deck %d: %w", deckID, err)
	}
	return rows, nil
}

// MediaNoteTypeStore wraps GORM access to the media_note_types table.
type MediaNoteTypeStore struct {
	db *gorm.DB
}

// NewMediaNoteTypeStore builds the note type media reference store.
func NewMediaNoteTypeStore(db *gorm.DB) *MediaNoteTypeStore { return &MediaNoteTypeStore{db: db} }

// Replace sets the media names a note type's templates and CSS refer to, replacing the previous set as a whole.
//
// The reference set is derived data: a caller recomputes it from the templates instead of diffing, so replace is the only
// write. Names are NFC-normalised, de-duplicated and sorted, which makes the stored order and the returned order the same.
// An empty set is legal and clears the rows.
func (s *MediaNoteTypeStore) Replace(ctx context.Context, noteTypeID uint64, names []string) error {
	if noteTypeID == 0 {
		return errors.New("replace note type media: note type is required")
	}
	clean := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		n := normaliseMediaName(name)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		clean = append(clean, n)
	}
	sort.Strings(clean)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("note_type_id = ?", noteTypeID).Delete(&MediaNoteType{}).Error; err != nil {
			return fmt.Errorf("clear media of note type %d: %w", noteTypeID, err)
		}
		if len(clean) == 0 {
			return nil
		}
		rows := make([]MediaNoteType, 0, len(clean))
		for _, name := range clean {
			rows = append(rows, MediaNoteType{NoteTypeID: noteTypeID, MediaName: name})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("write media of note type %d: %w", noteTypeID, err)
		}
		return nil
	})
}

// List returns a note type's media names ordered by name.
func (s *MediaNoteTypeStore) List(ctx context.Context, noteTypeID uint64) ([]string, error) {
	var names []string
	if err := s.db.WithContext(ctx).Model(&MediaNoteType{}).
		Where("note_type_id = ?", noteTypeID).
		Order("media_name ASC").Pluck("media_name", &names).Error; err != nil {
		return nil, fmt.Errorf("list media of note type %d: %w", noteTypeID, err)
	}
	return names, nil
}
