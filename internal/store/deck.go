package store

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// Documented defaults for deck-level daily caps: 20 new cards, 200 reviews, 0 means unlimited.
// Kept in sync with the column defaults of the decks table; changing them requires updating the default tags in models.go.
const (
	DefaultNewPerDay     = 20
	DefaultReviewsPerDay = 200
)

// Length bounds for deck name and description (decided by the user on 2026-10-06).
//
// Counted in Unicode characters (runes), not bytes: one Han character or emoji counts as one character. Counting bytes would
// cut a Chinese deck name down to 66 characters, while 200 emoji would leave only 50 — character count would directly
// conflict with the user's notion of 'how many characters'.
//
// This is the single source of truth: deck package manifest import validation (package_import.go) and web/API creation
// and rename share this constant set, the numbers are written only here; package_import.go keeps the old name as an alias.
const (
	maxDeckNameChars        = 200
	maxDeckDescriptionChars = 2000
)

var (
	// ErrNotOwner means the caller is not the resource owner and therefore may not modify it.
	// Reusing the same sentinel lets upper layers translate it consistently into 403 via errors.Is.
	ErrNotOwner = errors.New("actor is not the owner")
	// ErrDeckNameRequired means the deck name is empty.
	ErrDeckNameRequired = errors.New("deck name is required")
	// ErrDeckNameInvalid means the deck name does not meet the rules shared with the deck package manifest: more than
	// maxDeckNameChars characters (counted by rune), containing C0 control characters, or not valid UTF-8.
	// An empty name is signalled separately by ErrDeckNameRequired and is not folded into this sentinel.
	ErrDeckNameInvalid = errors.New("deck name is invalid")
	// ErrDeckDescriptionInvalid separates description rejection from deck name rejection, so the transport layer can point at the field.
	ErrDeckDescriptionInvalid = errors.New("deck description is invalid")
	// ErrDeckPresetRequired means the deck has no scheduling preset set.
	ErrDeckPresetRequired = errors.New("deck preset is required")
	// ErrInvalidDeckCap means a daily cap is negative; 0 is a legal value (unlimited).
	ErrInvalidDeckCap = errors.New("deck daily cap must not be negative")
	// ErrDeckPresetInvalid means the target preset does not belong to this deck's owner. Attaching someone else's preset would
	// expose their scheduling parameters to the caller (deck readable ⇒ preset readable).
	ErrDeckPresetInvalid = errors.New("deck preset must belong to the deck owner")
	// ErrDeckUpdateEmpty means a deck update supplied neither name nor description: no writable field at all (under PATCH semantics
	// both fields may be omitted, but at least one must be given).
	ErrDeckUpdateEmpty = errors.New("deck update has no fields")
)

// DeckCaps is the deck-level daily caps; NewPerDay and ReviewsPerDay both 0 means unlimited.
type DeckCaps struct {
	NewPerDay     int
	ReviewsPerDay int
}

// validDeckCaps validates that the caps are non-negative.
func validDeckCaps(c DeckCaps) error {
	if c.NewPerDay < 0 || c.ReviewsPerDay < 0 {
		return fmt.Errorf("%w: new_per_day=%d reviews_per_day=%d", ErrInvalidDeckCap, c.NewPerDay, c.ReviewsPerDay)
	}
	return nil
}

// requireDeckOwner checks whether actor is the deck owner and returns ErrNotOwner if not.
//
// It guards the owner-only operations (rename, cap changes, delete). Shared access is decided elsewhere: deck_grants rows feed the
// visibility queries and auth.DeckAccess, so an editor granted on a deck still cannot rename or delete it here.
func requireDeckOwner(d *Deck, actorUserID uint64) error {
	if d.OwnerUserID != actorUserID {
		return fmt.Errorf("%w: deck %d is owned by user %d", ErrNotOwner, d.ID, d.OwnerUserID)
	}
	return nil
}

// DeckStore wraps GORM access to the decks table and concentrates every permission check at one entry point.
type DeckStore struct {
	db *gorm.DB
}

// NewDeckStore constructs the deck store.
func NewDeckStore(db *gorm.DB) *DeckStore { return &DeckStore{db: db} }

// validateDeckName validates a deck name with rules exactly matching the deck package manifest: non-empty, valid UTF-8,
// free of C0 control characters (U+0000–U+001F), at most maxDeckNameChars long (counted by rune).
//
// An empty name keeps the existing semantics and returns ErrDeckNameRequired instead of ErrDeckNameInvalid — callers must
// give different messages for 'no name filled in' and 'name is invalid'.
func validateDeckName(name string) error {
	if name == "" {
		return ErrDeckNameRequired
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("%w: invalid UTF-8", ErrDeckNameInvalid)
	}
	if r, ok := firstControlRune(name); ok {
		return fmt.Errorf("%w: contains a control character U+%04X", ErrDeckNameInvalid, r)
	}
	if n := utf8.RuneCountInString(name); n > maxDeckNameChars {
		return fmt.Errorf("%w: %d characters, limit is %d", ErrDeckNameInvalid, n, maxDeckNameChars)
	}
	return nil
}

// DeckCopyName returns the deck name 'source name + copy suffix': when the whole string exceeds maxDeckNameChars
// characters, the source name is truncated by rune so the result still satisfies the shared name validation. The suffix is supplied by the caller per language (localization lives only in the transport layer),
// so only length is handled here and the suffix content is irrelevant — otherwise cloning would produce an over-long copy name for a legitimate deck near the limit.
func DeckCopyName(sourceName, suffix string) string {
	if utf8.RuneCountInString(sourceName)+utf8.RuneCountInString(suffix) <= maxDeckNameChars {
		return sourceName + suffix
	}
	keep := maxDeckNameChars - utf8.RuneCountInString(suffix)
	if keep < 0 {
		keep = 0
	}
	runes := []rune(sourceName)
	if keep > len(runes) {
		keep = len(runes)
	}
	return string(runes[:keep]) + suffix
}

// validateDeckForWrite validates the writable fields; on create it additionally requires owner and preset to be set.
func validateDeckForWrite(d *Deck, create bool) error {
	if err := validateDeckName(d.Name); err != nil {
		return err
	}
	// Shares the same text rules as package import, so creation cannot produce a description that the import side rejects. An empty description is legal.
	if entries := textFieldErrors("deck.description", d.Description, maxDeckDescriptionChars); len(entries) > 0 {
		return fmt.Errorf("%w: %s", ErrDeckDescriptionInvalid, entries[0])
	}
	if create {
		if d.OwnerUserID == 0 {
			return errors.New("deck owner is required")
		}
		if d.PresetID == 0 {
			return ErrDeckPresetRequired
		}
	}
	return nil
}

// Create writes a new deck; ownership is given explicitly by d.OwnerUserID.
func (s *DeckStore) Create(ctx context.Context, d *Deck) error {
	if err := validateDeckForWrite(d, true); err != nil {
		return err
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	if err := s.db.WithContext(ctx).Create(d).Error; err != nil {
		return fmt.Errorf("create deck: %w", err)
	}
	return nil
}

// ByID fetches a deck by primary key; archiving does not affect reads, callers decide how to display it by ArchivedAt.
func (s *DeckStore) ByID(ctx context.Context, id uint64) (*Deck, error) {
	var d Deck
	if err := s.db.WithContext(ctx).First(&d, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &d, nil
}

// ListByOwner lists the decks owned by a user (archived included), newest first.
func (s *DeckStore) ListByOwner(ctx context.Context, ownerUserID uint64) ([]Deck, error) {
	var decks []Deck
	if err := s.db.WithContext(ctx).Where("owner_user_id = ?", ownerUserID).
		Order("created_at DESC, id DESC").Find(&decks).Error; err != nil {
		return nil, fmt.Errorf("list decks by owner: %w", err)
	}
	return decks, nil
}

// Update changes a deck's name and description; only the owner may. nil name / description means 'do not change this field',
// so a request with only a name does not wipe the description (PATCH semantics).
//
// Validation applies to the 'merged final text' (existing value + fields supplied this time), with rules shared by creation and deck package import.
// **preset_id is not written**: the scheduling preset is a study setting changed only via SetPreset / SetStudySettings; if the rename path
// wrote it back, it would overwrite a preset change made in the meantime with the value read earlier in this call (the owner's setting lives in decks.preset_id).
// Ownership, creation time and archive state are likewise not writable fields, so a whole-row overwrite cannot also wipe them.
func (s *DeckStore) Update(ctx context.Context, actorUserID, deckID uint64, name, description *string) error {
	if name == nil && description == nil {
		return ErrDeckUpdateEmpty
	}
	existing, err := s.ByID(ctx, deckID)
	if err != nil {
		return err
	}
	if err := requireDeckOwner(existing, actorUserID); err != nil {
		return err
	}
	next := *existing
	if name != nil {
		next.Name = *name
	}
	if description != nil {
		next.Description = *description
	}
	if err := validateDeckForWrite(&next, false); err != nil {
		return err
	}
	updates := map[string]any{}
	if name != nil {
		updates["name"] = next.Name
	}
	if description != nil {
		updates["description"] = next.Description
	}
	if err := s.db.WithContext(ctx).Model(&Deck{}).Where("id = ?", deckID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update deck %d: %w", deckID, err)
	}
	return nil
}

// Archive stamps the archive time; archiving again returns nil directly (idempotent).
func (s *DeckStore) Archive(ctx context.Context, actorUserID, deckID uint64, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return s.mutateOwned(ctx, actorUserID, deckID, map[string]any{"archived_at": at})
}

// Restore clears the archive time; idempotent and returns nil when not archived.
func (s *DeckStore) Restore(ctx context.Context, actorUserID, deckID uint64) error {
	return s.mutateOwned(ctx, actorUserID, deckID, map[string]any{"archived_at": nil})
}

// Caps reads the deck's daily caps; returns the underlying error when the deck does not exist.
func (s *DeckStore) Caps(ctx context.Context, deckID uint64) (DeckCaps, error) {
	var d Deck
	if err := s.db.WithContext(ctx).Select("id", "new_per_day", "reviews_per_day").
		First(&d, "id = ?", deckID).Error; err != nil {
		return DeckCaps{}, err
	}
	return DeckCaps{NewPerDay: d.NewPerDay, ReviewsPerDay: d.ReviewsPerDay}, nil
}

// SetCaps writes the deck-level daily caps; only the owner may.
// It updates via a map rather than Save on the whole model: 0 is a legal value here (unlimited), and a map writes 0 explicitly,
// unaffected by GORM's zero-value omission for columns with defaults.
func (s *DeckStore) SetCaps(ctx context.Context, actorUserID, deckID uint64, caps DeckCaps) error {
	if err := validDeckCaps(caps); err != nil {
		return err
	}
	return s.mutateOwned(ctx, actorUserID, deckID, map[string]any{
		"new_per_day":     caps.NewPerDay,
		"reviews_per_day": caps.ReviewsPerDay,
	})
}

// SetPreset changes a deck's scheduling preset; only the owner may,
// and the target preset must belong to the same user — otherwise someone could hang another's preset on their own deck
// and expose that preset's parameters through 'the deck is readable'.
func (s *DeckStore) SetPreset(ctx context.Context, actorUserID, deckID, presetID uint64) error {
	var count int64
	if err := s.db.WithContext(ctx).Model(&Preset{}).
		Where("id = ? AND owner_user_id = ?", presetID, actorUserID).Count(&count).Error; err != nil {
		return fmt.Errorf("check preset ownership: %w", err)
	}
	if count == 0 {
		return ErrDeckPresetInvalid
	}
	return s.mutateOwned(ctx, actorUserID, deckID, map[string]any{"preset_id": presetID})
}

// mutateOwned is the single-column / multi-column update channel with the owner check; every mutating method lands through it.
func (s *DeckStore) mutateOwned(ctx context.Context, actorUserID, deckID uint64, updates map[string]any) error {
	existing, err := s.ByID(ctx, deckID)
	if err != nil {
		return err
	}
	if err := requireDeckOwner(existing, actorUserID); err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(&Deck{}).Where("id = ?", deckID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update deck %d: %w", deckID, err)
	}
	return nil
}

// Delete deletes the deck and its related content; only the owner may.
func (s *DeckStore) Delete(ctx context.Context, actorUserID, deckID uint64) error {
	existing, err := s.ByID(ctx, deckID)
	if err != nil {
		return err
	}
	if err := requireDeckOwner(existing, actorUserID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteDeckTx(tx, deckID)
	})
}

// deleteDeckTx hard-deletes a deck and all its related rows inside the given transaction (no permission check).
// Deck deletion and user deletion (removing every deck under a user) share this one cascade, so both clean up the same scope.
func deleteDeckTx(tx *gorm.DB, deckID uint64) error {
	// explicitly cascade-clean the related tables so both databases behave exactly the same regardless of their foreign-key PRAGMA settings (AGENTS.md §2.3 item 4)
	if err := tx.Exec("DELETE FROM media_notes WHERE note_id IN (SELECT id FROM notes WHERE deck_id = ?)", deckID).Error; err != nil {
		return fmt.Errorf("delete media_notes: %w", err)
	}
	if err := tx.Exec("DELETE FROM card_states WHERE card_id IN (SELECT id FROM cards WHERE note_id IN (SELECT id FROM notes WHERE deck_id = ?))", deckID).Error; err != nil {
		return fmt.Errorf("delete card_states: %w", err)
	}
	if err := tx.Exec("DELETE FROM cards WHERE note_id IN (SELECT id FROM notes WHERE deck_id = ?)", deckID).Error; err != nil {
		return fmt.Errorf("delete cards: %w", err)
	}
	if err := tx.Exec("DELETE FROM notes WHERE deck_id = ?", deckID).Error; err != nil {
		return fmt.Errorf("delete notes: %w", err)
	}
	if err := tx.Exec("DELETE FROM deck_grants WHERE deck_id = ?", deckID).Error; err != nil {
		return fmt.Errorf("delete deck_grants: %w", err)
	}
	if err := tx.Exec("DELETE FROM deck_member_settings WHERE deck_id = ?", deckID).Error; err != nil {
		return fmt.Errorf("delete deck_member_settings: %w", err)
	}
	if err := tx.Exec("DELETE FROM deck_daily_extras WHERE deck_id = ?", deckID).Error; err != nil {
		return fmt.Errorf("delete deck_daily_extras: %w", err)
	}
	if err := tx.Exec("DELETE FROM share_links WHERE deck_id = ?", deckID).Error; err != nil {
		return fmt.Errorf("delete share_links: %w", err)
	}
	// pending invites must be cleaned too: with the deck gone, opening that invite only 404s.
	if err := tx.Exec("DELETE FROM deck_share_invites WHERE deck_id = ?", deckID).Error; err != nil {
		return fmt.Errorf("delete deck_share_invites: %w", err)
	}
	if err := tx.Exec("DELETE FROM share_session_decks WHERE deck_id = ?", deckID).Error; err != nil {
		return fmt.Errorf("delete share_session_decks: %w", err)
	}
	if err := tx.Exec("DELETE FROM decks WHERE id = ?", deckID).Error; err != nil {
		return fmt.Errorf("delete deck: %w", err)
	}
	return nil
}
