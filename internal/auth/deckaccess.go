package auth

import (
	"context"
	"errors"

	"git.nite07.com/nite/engram/internal/store"
)

// The two sentinel errors of the permission check; callers use them to decide the HTTP status and the stable code:
//   - ErrDeckNotFound: the deck does not exist — 404 / not_found
//   - ErrForbidden: no access right, or the role is insufficient — 403 (insufficient_role when there is a role that does not suffice)
var (
	// ErrDeckNotFound means the deck does not exist (or is invisible to the current user).
	ErrDeckNotFound = errors.New("deck not found")
	// ErrForbidden means the user lacks the required permission on this deck.
	ErrForbidden = errors.New("forbidden")
)

// DeckAccess is the deck permission helper shared by Web and REST/MCP (a single implementation).
//
// Effective role resolution order:
//  1. an explicit grant row in deck_grants;
//  2. with no grant row but decks.owner_user_id == userID, the user counts as owner — decks created in M2 have no grant rows,
//     the owner column is the single source of truth, and this fallback keeps existing decks with their original owners after the upgrade.
//  3. everything else is "no access right whatsoever" (empty role).
//
// Every handler that touches decks must decide through here; there is only one check and the two transports do not each implement it.
type DeckAccess struct {
	decks  *store.DeckStore
	grants *store.GrantStore
}

// NewDeckAccess constructs the checker; grants may be nil (with no grant store wired in, only the owner column applies).
func NewDeckAccess(decks *store.DeckStore, grants *store.GrantStore) *DeckAccess {
	return &DeckAccess{decks: decks, grants: grants}
}

// Role returns the user's effective role on the deck; an empty string when there is no access right.
// A missing deck returns ErrDeckNotFound, so callers do not conflate "does not exist" with "no grant".
//
// An effective role has only two sources: decks.owner_user_id (decks created in M2 have no grant rows, the owner column is the single
// source of truth, and this fallback keeps existing decks with their original owners after the upgrade) and explicit grant rows in deck_grants. There is no third
// "implicit read-only access through visibility" — that would equal opening someone else's deck contents to everyone, contradicting the consent model.
func (d *DeckAccess) Role(ctx context.Context, deckID, userID uint64) (string, error) {
	deck, err := d.decks.ByID(ctx, deckID)
	if err != nil {
		return "", ErrDeckNotFound
	}
	if deck.OwnerUserID == userID {
		return store.RoleOwner, nil
	}
	if d.grants == nil {
		return "", nil
	}
	role, err := d.grants.Role(ctx, deckID, userID)
	if err != nil {
		return "", err
	}
	if !store.ValidRole(role) {
		// The role in the grant row is not one of the three legal values (dirty data): treat it as "no grant", preferring a rejection over letting it through.
		return "", nil
	}
	return role, nil
}

// RequireRole requires the user to hold at least the want role on the deck, returning the matched deck and the user's effective role.
//
// An illegal want (not one of the three roles) means "deny everything": a misspelled role name should reject, not pass.
// Both no access right and an insufficient role return ErrForbidden; callers can tell them apart by whether the returned role is empty,
// in order to produce the two stable codes forbidden / insufficient_role.
func (d *DeckAccess) RequireRole(ctx context.Context, deckID, userID uint64, want string) (*store.Deck, string, error) {
	deck, err := d.decks.ByID(ctx, deckID)
	if err != nil {
		return nil, "", ErrDeckNotFound
	}
	role := ""
	if deck.OwnerUserID == userID {
		role = store.RoleOwner
	} else if d.grants != nil {
		granted, gerr := d.grants.Role(ctx, deckID, userID)
		if gerr != nil {
			return nil, "", gerr
		}
		if store.ValidRole(granted) {
			role = granted
		}
	}
	if !store.RoleAllows(role, want) {
		return nil, role, ErrForbidden
	}
	return deck, role, nil
}
