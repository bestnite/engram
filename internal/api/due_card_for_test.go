package api

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDueCardForMatchesDueCards guards the single-card view against drifting from the queue: the fallback a review
// action response uses for a card outside the queue must assemble exactly the entry the queue returns for that card.
func TestDueCardForMatchesDueCards(t *testing.T) {
	env := newTestEnv(t, 0, 0)
	user := seedUser(t, env.db, "due-card-for", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	dueCard := seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-time.Hour), "review front", 3)
	newCard := seedNewCard(t, env.db, deck.ID, "new front")

	queue, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, nil, 500)
	if err != nil {
		t.Fatalf("DueCards() error = %v", err)
	}
	byID := make(map[string]DueCard, len(queue))
	for _, entry := range queue {
		byID[entry.CardID] = entry
	}

	for _, card := range []store.Card{dueCard, newCard} {
		fromQueue, ok := byID[card.PublicID]
		if !ok {
			t.Fatalf("card %s is missing from the queue", card.PublicID)
		}
		single, err := env.api.DueCardFor(context.Background(), user, card.PublicID)
		if err != nil {
			t.Fatalf("DueCardFor(%s) error = %v", card.PublicID, err)
		}
		if !reflect.DeepEqual(fromQueue, single) {
			t.Errorf("DueCardFor(%s) = %+v, want the queue's entry %+v", card.PublicID, single, fromQueue)
		}
	}
}

// TestDueCardForRejections covers the negative cases: an unknown card is not found, and a user who cannot read the
// card's deck is refused by the same reader rule the queue applies.
func TestDueCardForRejections(t *testing.T) {
	env := newTestEnv(t, 0, 0)
	owner := seedUser(t, env.db, "due-card-owner", store.RoleUser)
	stranger := seedUser(t, env.db, "due-card-stranger", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	card := seedNewCard(t, env.db, deck.ID, "front")

	_, err := env.api.DueCardFor(context.Background(), owner, "not-a-card")
	if status, code := serviceCode(t, err); status != http.StatusNotFound || code != CodeNotFound {
		t.Errorf("DueCardFor(unknown card) = %d %s, want 404 %s", status, code, CodeNotFound)
	}
	_, err = env.api.DueCardFor(context.Background(), stranger, card.PublicID)
	if status, _ := serviceCode(t, err); status != http.StatusForbidden && status != http.StatusNotFound {
		t.Errorf("DueCardFor(foreign card) = %d, want 403 or 404", status)
	}
}

// TestDueCardForNeverStudiedCard pins the new-card fallback: a card with no state row is reported as new with the
// current time as its due time, the same values the queue builder fills in for a new card.
func TestDueCardForNeverStudiedCard(t *testing.T) {
	env := newTestEnv(t, 0, 0)
	user := seedUser(t, env.db, "due-card-new", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	card := seedNewCard(t, env.db, deck.ID, "never studied")

	entry, err := env.api.DueCardFor(context.Background(), user, card.PublicID)
	if err != nil {
		t.Fatalf("DueCardFor() error = %v", err)
	}
	if entry.State != store.CardStateNew {
		t.Errorf("state = %q, want %q", entry.State, store.CardStateNew)
	}
	if !entry.DueAt.Equal(env.now.UTC()) {
		t.Errorf("due_at = %v, want the current time %v", entry.DueAt, env.now.UTC())
	}
	if entry.Version != 0 || entry.Retrievability != 0 {
		t.Errorf("version/retrievability = %d/%v, want 0/0", entry.Version, entry.Retrievability)
	}
	if entry.CardID != card.PublicID || entry.DeckID != deck.PublicID {
		t.Errorf("ids = card %q deck %q, want %q and %q", entry.CardID, entry.DeckID, card.PublicID, deck.PublicID)
	}
}

// TestDueCardForOutsideTheQueue covers the reason the method exists: a card in a state that keeps it out of today's
// queue is still described, and the queue itself is unchanged by the read.
func TestDueCardForOutsideTheQueue(t *testing.T) {
	env := newTestEnv(t, 0, 0)
	user := seedUser(t, env.db, "due-card-outside", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	card := seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-time.Hour), "outside", 1)
	future := env.now.Add(72 * time.Hour)
	if err := env.db.Model(&store.CardState{}).
		Where("card_id = ? AND user_id = ?", card.ID, user.ID).
		Update("due_at", future).Error; err != nil {
		t.Fatalf("move due date into the future: %v", err)
	}

	queue, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, nil, 500)
	if err != nil {
		t.Fatalf("DueCards() error = %v", err)
	}
	if len(queue) != 0 {
		t.Fatalf("queue = %+v, want no cards (the card is due later)", queue)
	}
	entry, err := env.api.DueCardFor(context.Background(), user, card.PublicID)
	if err != nil {
		t.Fatalf("DueCardFor() error = %v", err)
	}
	if entry.State != store.CardStateReview {
		t.Errorf("state = %q, want %q", entry.State, store.CardStateReview)
	}
	if !entry.DueAt.Equal(future.UTC()) {
		t.Errorf("due_at = %v, want %v", entry.DueAt, future.UTC())
	}
	if entry.Version != 1 {
		t.Errorf("version = %d, want 1", entry.Version)
	}
}
