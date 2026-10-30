package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/notetype"
)

var (
	// ErrNoteTypeInUse means a note type still backs at least one live note: deleting it would orphan that note's content.
	ErrNoteTypeInUse = errors.New("note type is in use by one or more notes")
	// ErrNoteTypePurgeUnconfirmed means the note type is only used by soft-deleted notes, and the caller did not confirm
	// hard-deleting them. The store returns it together with the computed impact, so the caller can report the counts
	// before asking again with confirmation.
	ErrNoteTypePurgeUnconfirmed = errors.New("note type purge is not confirmed")
	// ErrVersionConflict means the version the caller read no longer matches the stored row: another writer committed
	// in between. Callers should map it to HTTP 409. It is the one optimistic-lock sentinel of the repository;
	// internal/schedule aliases it for card_states instead of defining a second value.
	ErrVersionConflict = errors.New("version conflict")
	// ErrCardRemovalUnconfirmed means an update would soft-delete at least one card and the caller did not confirm it.
	// The store returns it together with the computed impact, so the caller can report the counts before asking again.
	ErrCardRemovalUnconfirmed = errors.New("note type update would remove cards without confirmation")
)

// NoteTypeUpdateOptions carries the optimistic lock and the two safety switches of a note type update.
type NoteTypeUpdateOptions struct {
	// ExpectedVersion is the version the caller read. A stale value is rejected with ErrVersionConflict.
	ExpectedVersion int
	// DryRun computes the card impact and rolls the transaction back, so nothing is written.
	DryRun bool
	// ConfirmCardRemoval allows the update to soft-delete cards whose template the new config no longer declares.
	// Without it an update that would remove at least one card returns ErrCardRemovalUnconfirmed and writes nothing.
	ConfirmCardRemoval bool
}

// NoteTypeCardImpact is the card effect of one note type update.
//
// CardsAdded counts cards that became visible: newly inserted rows and previously soft-deleted rows
// restored because their template is generated again. CardsRemoved counts cards this update
// soft-deleted. ReviewsOnRemovedCards counts the review rows attached to those removed cards; the
// rows themselves are kept, because progress and the review log are never destroyed by a content change.
type NoteTypeCardImpact struct {
	CardsAdded            int
	CardsRemoved          int
	ReviewsOnRemovedCards int
}

// SetConfig validates and stores a note type config in ConfigJSON.
//
// Both the name and the config are validated here, before any write, so an illegal note type never reaches the table.
// The structural rules name the offending field or template (notetype.ValidationError.Detail); the answer-spec rule
// names the template and the role it rejected. Every note type write path (API create/update, package import, stock
// type creation and migration) funnels through this method, so the answer-spec rule has one implementation point.
func (nt *NoteType) SetConfig(name string, cfg notetype.Config) error {
	if err := notetype.ValidateName(name); err != nil {
		return fmt.Errorf("validate note type name: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate note type config: %w", err)
	}
	if err := validateAnswerSpecs(cfg); err != nil {
		return fmt.Errorf("validate note type config: %w", err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode note type config: %w", err)
	}
	nt.Name = name
	nt.ConfigJSON = string(raw)
	return nil
}

// validateAnswerSpecs runs the answer-control rules over every template of a config.
//
// The template name is added here rather than inside internal/answer, because the spec alone does not know which
// template it belongs to and a rejection must let the caller find the offending template.
func validateAnswerSpecs(cfg notetype.Config) error {
	fieldIDs := make([]string, 0, len(cfg.Fields))
	for _, field := range cfg.Fields {
		fieldIDs = append(fieldIDs, field.ID)
	}
	for _, template := range cfg.Templates {
		if template.Answer == nil {
			continue
		}
		if err := answer.ValidateSpec(*template.Answer, fieldIDs); err != nil {
			return fmt.Errorf("template %q: %w", template.Name, err)
		}
	}
	return nil
}

// Config decodes ConfigJSON into the in-memory note type shape.
func (nt *NoteType) Config() (notetype.Config, error) {
	var cfg notetype.Config
	if err := json.Unmarshal([]byte(nt.ConfigJSON), &cfg); err != nil {
		return notetype.Config{}, fmt.Errorf("decode note type %d config: %w", nt.ID, err)
	}
	return cfg, nil
}

// NoteTypeStore wraps GORM access to the note_types table.
type NoteTypeStore struct {
	db *gorm.DB
}

// NewNoteTypeStore builds the note type store.
func NewNoteTypeStore(db *gorm.DB) *NoteTypeStore { return &NoteTypeStore{db: db} }

// Create writes a new note type; the public id is generated by the model hook.
// The name and config are validated first (see SetConfig), so a rejected note type writes no row.
func (s *NoteTypeStore) Create(ctx context.Context, nt *NoteType, cfg notetype.Config) error {
	if nt == nil {
		return errors.New("create note type: note type is required")
	}
	if nt.DeckID == 0 {
		return errors.New("create note type: deck is required")
	}
	if err := nt.SetConfig(nt.Name, cfg); err != nil {
		return err
	}
	now := time.Now().UTC()
	if nt.CreatedAt.IsZero() {
		nt.CreatedAt = now
	}
	nt.UpdatedAt = now
	nt.Version = 0
	// The row and the derived media reference set are written together: garbage collection resolves `_`-prefixed
	// note-type assets through media_note_types, so a type that exists without its references would let them be reclaimed.
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(nt).Error; err != nil {
			return fmt.Errorf("create note type: %w", err)
		}
		if err := NewMediaNoteTypeStore(tx).Replace(ctx, nt.ID, noteTypeMediaNames(cfg)); err != nil {
			return fmt.Errorf("create note type media: %w", err)
		}
		return nil
	})
}

// ByID fetches a note type by primary key.
func (s *NoteTypeStore) ByID(ctx context.Context, id uint64) (*NoteType, error) {
	var nt NoteType
	if err := s.db.WithContext(ctx).First(&nt, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &nt, nil
}

// ByPublicID fetches a note type by external id; an empty id is not found.
func (s *NoteTypeStore) ByPublicID(ctx context.Context, publicID string) (*NoteType, error) {
	return firstByPublicID[NoteType](ctx, s.db, publicID)
}

// ByIDs loads the note types with the given primary keys, keyed by id. Missing ids are simply absent from the result.
func (s *NoteTypeStore) ByIDs(ctx context.Context, ids []uint64) (map[uint64]NoteType, error) {
	out := make(map[uint64]NoteType, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var types []NoteType
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&types).Error; err != nil {
		return nil, fmt.Errorf("load note types by id: %w", err)
	}
	for _, nt := range types {
		out[nt.ID] = nt
	}
	return out, nil
}

// ListByDeck lists one deck's note types ordered by name, then id (a deterministic order for two identically named types).
func (s *NoteTypeStore) ListByDeck(ctx context.Context, deckID uint64) ([]NoteType, error) {
	var types []NoteType
	if err := s.db.WithContext(ctx).Where("deck_id = ?", deckID).
		Order("name ASC, id ASC").Find(&types).Error; err != nil {
		return nil, fmt.Errorf("list note types for deck %d: %w", deckID, err)
	}
	return types, nil
}

// Update replaces the name and config only when the stored version equals opts.ExpectedVersion, and increments the version.
//
// The compare-and-swap is one UPDATE ... WHERE id = ? AND version = ?, so two concurrent writers cannot both win on either database.
// A zero-row result is disambiguated afterwards: the type is gone (gorm.ErrRecordNotFound) or the version moved (ErrVersionConflict).
//
// The version bump, the media reference set and the card regeneration of every note of the type commit
// in one transaction, so a note type never holds a card set that its stored config does not generate.
//
// Both safety outcomes finish by rolling the transaction back, which is what makes "writes nothing"
// literal: a dry run reports the impact and returns nil, and an unconfirmed card removal reports the
// impact with ErrCardRemovalUnconfirmed. The compare-and-swap still runs first in both cases, so a
// stale version is rejected even when nothing is written.
func (s *NoteTypeStore) Update(ctx context.Context, nt *NoteType, cfg notetype.Config, opts NoteTypeUpdateOptions) (NoteTypeCardImpact, error) {
	if nt == nil || nt.ID == 0 {
		return NoteTypeCardImpact{}, errors.New("update note type: id is required")
	}
	if err := nt.SetConfig(nt.Name, cfg); err != nil {
		return NoteTypeCardImpact{}, err
	}
	now := time.Now().UTC()
	var impact NoteTypeCardImpact
	// rollbackUpdate ends the transaction without committing. It is a sentinel rather than a real
	// failure: the caller converts it into the dry-run result or the confirmation error.
	rollbackUpdate := errors.New("rollback note type update")
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&NoteType{}).
			Where("id = ? AND version = ?", nt.ID, opts.ExpectedVersion).
			Updates(map[string]any{
				"name":        nt.Name,
				"config_json": nt.ConfigJSON,
				"version":     opts.ExpectedVersion + 1,
				"updated_at":  now,
			})
		if res.Error != nil {
			return fmt.Errorf("update note type %d: %w", nt.ID, res.Error)
		}
		if res.RowsAffected == 0 {
			var count int64
			if err := tx.Model(&NoteType{}).Where("id = ?", nt.ID).Count(&count).Error; err != nil {
				return fmt.Errorf("check note type %d after failed update: %w", nt.ID, err)
			}
			if count == 0 {
				return gorm.ErrRecordNotFound
			}
			return fmt.Errorf("%w: note type %d is not at version %d", ErrVersionConflict, nt.ID, opts.ExpectedVersion)
		}
		// The new config replaces the reference set in the same transaction: a rejected version writes neither.
		if err := NewMediaNoteTypeStore(tx).Replace(ctx, nt.ID, noteTypeMediaNames(cfg)); err != nil {
			return fmt.Errorf("update note type media %d: %w", nt.ID, err)
		}
		regenerated, err := regenerateNoteTypeCards(tx, nt.ID, nt.Name, cfg, now)
		if err != nil {
			return err
		}
		impact = regenerated
		if opts.DryRun {
			return rollbackUpdate
		}
		if impact.CardsRemoved > 0 && !opts.ConfirmCardRemoval {
			return rollbackUpdate
		}
		return nil
	})
	if errors.Is(err, rollbackUpdate) {
		if !opts.DryRun && impact.CardsRemoved > 0 && !opts.ConfirmCardRemoval {
			return impact, fmt.Errorf("%w: %d card(s) would be removed", ErrCardRemovalUnconfirmed, impact.CardsRemoved)
		}
		return impact, nil
	}
	if err != nil {
		return NoteTypeCardImpact{}, err
	}
	nt.Version = opts.ExpectedVersion + 1
	nt.UpdatedAt = now
	return impact, nil
}

// NoteTypeDeleteOptions carries the safety switch of a note type delete.
type NoteTypeDeleteOptions struct {
	// ConfirmPurge allows hard-deleting the notes of the type when every note that references it is soft-deleted.
	// Without it such a delete reports the impact with ErrNoteTypePurgeUnconfirmed and writes nothing.
	ConfirmPurge bool
}

// NoteTypePurgeImpact is what a note type delete removes besides the type itself.
//
// DeletedNotes counts the soft-deleted notes the delete hard-removed, Cards counts their card rows (soft-deleted
// cards included), and Reviews counts the review rows kept behind, which the delete never removes.
type NoteTypePurgeImpact struct {
	DeletedNotes int
	Cards        int
	Reviews      int
}

// Delete removes a note type together with the notes that only exist as soft-deleted rows.
//
// A live note still referencing the type refuses the delete with ErrNoteTypeInUse: deleting then would orphan
// content the user still studies. Soft-deleted notes do not block it, because nothing restores them except a
// re-import of the same guid; they are purged only when opts.ConfirmPurge is set, and an unconfirmed delete
// returns ErrNoteTypePurgeUnconfirmed with the impact and writes nothing.
//
// The reference counts and every write share one transaction, so a note inserted mid-delete cannot slip past the
// check within the transaction's isolation on either database. A confirmed purge hard-deletes the notes, their
// cards, card_states and media_notes rows through the shared cascade; the reviews rows stay, as they do when a
// deck is deleted.
func (s *NoteTypeStore) Delete(ctx context.Context, id uint64, opts NoteTypeDeleteOptions) (NoteTypePurgeImpact, error) {
	var impact NoteTypePurgeImpact
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var live int64
		if err := tx.Model(&Note{}).Where("note_type_id = ?", id).Count(&live).Error; err != nil {
			return fmt.Errorf("check note type %d usage: %w", id, err)
		}
		if live > 0 {
			return fmt.Errorf("%w: %d note(s) use note type %d", ErrNoteTypeInUse, live, id)
		}
		// The default scope hides soft-deleted notes, so this count is exactly the set the purge would remove.
		var deletedNotes int64
		if err := tx.Unscoped().Model(&Note{}).Where("note_type_id = ?", id).Count(&deletedNotes).Error; err != nil {
			return fmt.Errorf("check soft-deleted notes of note type %d: %w", id, err)
		}
		if deletedNotes > 0 {
			var cards, reviews int64
			// Unscoped: cards a template change soft-deleted are rows the purge removes all the same.
			if err := tx.Unscoped().Model(&Card{}).
				Where("note_id IN (SELECT id FROM notes WHERE note_type_id = ?)", id).
				Count(&cards).Error; err != nil {
				return fmt.Errorf("count cards of note type %d: %w", id, err)
			}
			if err := tx.Model(&Review{}).
				Where("card_id IN (SELECT id FROM cards WHERE note_id IN (SELECT id FROM notes WHERE note_type_id = ?))", id).
				Count(&reviews).Error; err != nil {
				return fmt.Errorf("count reviews of note type %d: %w", id, err)
			}
			impact = NoteTypePurgeImpact{DeletedNotes: int(deletedNotes), Cards: int(cards), Reviews: int(reviews)}
			if !opts.ConfirmPurge {
				return fmt.Errorf("%w: %d soft-deleted note(s) of note type %d", ErrNoteTypePurgeUnconfirmed, deletedNotes, id)
			}
			if err := deleteNotesTx(tx, "SELECT id FROM notes WHERE note_type_id = ?", id); err != nil {
				return err
			}
		}
		res := tx.Delete(&NoteType{}, "id = ?", id)
		if res.Error != nil {
			return fmt.Errorf("delete note type %d: %w", id, res.Error)
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		// The media reference rows die with the type; otherwise garbage collection could never reclaim the assets.
		if err := tx.Where("note_type_id = ?", id).Delete(&MediaNoteType{}).Error; err != nil {
			return fmt.Errorf("delete media of note type %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		return impact, err
	}
	return impact, nil
}
