// This file implements the externally visible, unpredictable id (public_id).
//
// Why it exists: the ids in routes and JSON used to be auto-increment primary keys, enumerable and guessable. The numeric primary key stays as is
// (it is the foundation of every foreign key and index) but is no longer exposed -- on the model ID is tagged json:\"-\", and the added
// PublicID is tagged json:\"id\", so serializing the model directly yields the right external shape (AGENTS.md §2.4:
// one model serves business logic, GORM and JSON at once, with no DTO mapping layer).
//
// Generation is concentrated in the GORM BeforeCreate hook: every creation path -- pages, REST, MCP, import, clone, test seeding --
// goes through Create, so filling the value in here suffices and no call site has to repeat it. The hook only fills empty values,
// an explicitly set public_id is never overwritten.
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NewPublicID generates an external id: UUIDv7 (RFC 9562), in the 36-character standard text form.
//
// Why v7 and not v4: v7 is built from "a millisecond timestamp + random bits", still unpredictable, but the values increase over time, so
// inserting into a unique index does not scatter the B-tree randomly the way pure-random v4 does.
func NewPublicID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// It only fails when the system random source is unavailable; falling back to v4 is still safer than returning an empty string.
		fallback, ferr := uuid.NewRandom()
		if ferr != nil {
			panic("store: cannot generate a public id: " + ferr.Error())
		}
		return fallback.String()
	}
	return id.String()
}

// assignPublicID is the shared implementation of the models' BeforeCreate hooks: an empty value is generated.
func assignPublicID(p *string) {
	if *p == "" {
		*p = NewPublicID()
	}
}

// The hooks below cover every externally visible entity; tables that are not exposed (sessions, outbox, log ledgers, etc.) do not need this column.
func (m *User) BeforeCreate(*gorm.DB) error     { assignPublicID(&m.PublicID); return nil }
func (m *Identity) BeforeCreate(*gorm.DB) error { assignPublicID(&m.PublicID); return nil }
func (m *Invite) BeforeCreate(*gorm.DB) error   { assignPublicID(&m.PublicID); return nil }
func (m *Preset) BeforeCreate(*gorm.DB) error   { assignPublicID(&m.PublicID); return nil }
func (m *Deck) BeforeCreate(*gorm.DB) error     { assignPublicID(&m.PublicID); return nil }
func (m *Note) BeforeCreate(*gorm.DB) error     { assignPublicID(&m.PublicID); return nil }
func (m *Card) BeforeCreate(*gorm.DB) error     { assignPublicID(&m.PublicID); return nil }
func (m *NoteType) BeforeCreate(*gorm.DB) error { assignPublicID(&m.PublicID); return nil }
func (m *Review) BeforeCreate(*gorm.DB) error   { assignPublicID(&m.PublicID); return nil }
func (m *APIKey) BeforeCreate(*gorm.DB) error   { assignPublicID(&m.PublicID); return nil }
func (m *Job) BeforeCreate(*gorm.DB) error      { assignPublicID(&m.PublicID); return nil }
func (m *AuditLog) BeforeCreate(*gorm.DB) error { assignPublicID(&m.PublicID); return nil }

// firstByPublicID is the shared implementation behind the stores' ByPublicID methods.
// An empty string counts as not found right away: rows from before the migration may have an empty public_id, and an empty string must never match a row.
func firstByPublicID[T any](ctx context.Context, db *gorm.DB, publicID string) (*T, error) {
	if publicID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var out T
	if err := db.WithContext(ctx).First(&out, "public_id = ?", publicID).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

// ByPublicID fetches a deck by external id.
func (s *DeckStore) ByPublicID(ctx context.Context, publicID string) (*Deck, error) {
	return firstByPublicID[Deck](ctx, s.db, publicID)
}

// ByPublicID fetches a note that is not soft-deleted by external id; a deleted note counts as absent (use Restore to bring it back).
func (s *NoteStore) ByPublicID(ctx context.Context, publicID string) (*Note, error) {
	return firstByPublicID[Note](ctx, s.db, publicID)
}

// ByPublicID fetches a preset by external id.
func (s *PresetStore) ByPublicID(ctx context.Context, publicID string) (*Preset, error) {
	return firstByPublicID[Preset](ctx, s.db, publicID)
}

// ByPublicID fetches a user by external id; returns gorm.ErrRecordNotFound when absent.
func (s *UserStore) ByPublicID(ctx context.Context, publicID string) (*User, error) {
	return firstByPublicID[User](ctx, s.db, publicID)
}

// ByPublicID fetches a user-level credential by external id.
func (s *APIKeyStore) ByPublicID(ctx context.Context, publicID string) (*APIKey, error) {
	return firstByPublicID[APIKey](ctx, s.db, publicID)
}

// ByPublicID fetches a visible card by external id; returns gorm.ErrRecordNotFound when its note is soft-deleted.
// Visibility goes through visibleCards, the same definition as CardStore.ByID.
func (s *CardStore) ByPublicID(ctx context.Context, publicID string) (*Card, error) {
	if publicID == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var c Card
	if err := s.visibleCards(ctx).Select("cards.*").Where("cards.public_id = ?", publicID).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ByPublicID fetches an invite by external id; returns ErrInviteNotFound when absent.
func (s *InviteStore) ByPublicID(ctx context.Context, publicID string) (*Invite, error) {
	inv, err := firstByPublicID[Invite](ctx, s.db, publicID)
	if err != nil {
		if IsNotFound(err) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("load invite: %w", err)
	}
	return inv, nil
}

// ByPublicID fetches a binding by external id; returns ErrIdentityNotFound when absent.
func (s *IdentityStore) ByPublicID(ctx context.Context, publicID string) (*Identity, error) {
	ident, err := firstByPublicID[Identity](ctx, s.db, publicID)
	if err != nil {
		if IsNotFound(err) {
			return nil, ErrIdentityNotFound
		}
		return nil, fmt.Errorf("load identity: %w", err)
	}
	return ident, nil
}
