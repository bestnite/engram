package schedule

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
)

// DeckBudget is a deck's allowance situation today, for the settings page to show "used / left".
//
// The two *Unlimited fields express "this field is unlimited today": when true the matching PerDay / Left value is meaningless
// (a stored 0 means unlimited, an integer of the same shape as "0 cards left", so a boolean is required to tell them apart).
// When false, Left = max(PerDay-Used, 0), where Used is today's usage.
type DeckBudget struct {
	// NewPerDay / ReviewsPerDay are the daily caps on the deck columns; writing 0 means unlimited.
	NewPerDay     int
	ReviewsPerDay int
	// NewExtra is the new-card allowance added today by "another round"; NewLeft already includes it, NewPerDay does not.
	NewExtra int
	// NewUsed / ReviewUsed are today's usage (new = introduced today, review = reviewed today).
	NewUsed    int
	ReviewUsed int
	// NewLeft / ReviewLeft are the remaining allowance today; meaningful only when the matching Unlimited is false.
	NewLeft    int
	ReviewLeft int
	// NewUnlimited / ReviewUnlimited being true means that field is unlimited today (PerDay is 0).
	NewUnlimited    bool
	ReviewUnlimited bool
}

// DeckBudgets returns the allowance situation of each deck today.
//
// It shares the same allowance formula (deckBudget) and the same review-day semantics as Build / DeckCounts, so the "used / left"
// on the settings page and the number of cards the queue can actually produce come from the same source; the web layer rewrites no SQL and recomputes no formula. opts only reads
// Now, Location/Timezone and DayCutoffHour: "today's usage" must be counted by the user's own review day, matching the semantics
// written into reviews.review_day at grading time.
//
// deckIDs is the set of decks to query; an empty set returns an empty map (no error). When a deck column is unreadable (deck row missing)
// the resolveScope fallback applies: NewPerDay uses DefaultNewPerDay, ReviewsPerDay stays 0 (unlimited).
func (b *QueueBuilder) DeckBudgets(ctx context.Context, userID uint64, deckIDs []uint64, opts QueueOptions) (map[uint64]DeckBudget, error) {
	if userID == 0 {
		return nil, errors.New("schedule: deck budgets: user id is required")
	}
	if b.decks == nil {
		return nil, errors.New("schedule: deck budgets: deck store is required")
	}
	ids := dedupeIDs(deckIDs)
	out := make(map[uint64]DeckBudget, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	opts = QueueOptions{
		DeckIDs: ids, Now: opts.Now, Location: opts.Location, Timezone: opts.Timezone,
		DayCutoffHour: opts.DayCutoffHour,
	}.withDefaults()

	caps, err := b.loadDeckCaps(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	loc := opts.location()
	day := reviewday.Day(opts.now(), loc, reviewday.ResolveCutoff(opts.DayCutoffHour))
	usage, err := b.countUsage(ctx, userID, day, ids)
	if err != nil {
		return nil, err
	}

	for _, id := range ids {
		c, ok := caps[id]
		if !ok {
			// Only an unreadable deck column (deck row missing) falls back to the default; keep the rule in one place with resolveScope.
			c = store.DeckCaps{NewPerDay: DefaultNewPerDay}
		}
		u := usage[id]
		newLeft, reviewLeft := deckBudget(c, u)
		out[id] = DeckBudget{
			NewPerDay:       c.NewPerDay,
			ReviewsPerDay:   c.ReviewsPerDay,
			NewExtra:        u.extraNew,
			NewUsed:         u.introduced,
			ReviewUsed:      u.reviewed,
			NewLeft:         nonNegative(newLeft),
			ReviewLeft:      nonNegative(reviewLeft),
			NewUnlimited:    newLeft == deckUnlimited,
			ReviewUnlimited: reviewLeft == deckUnlimited,
		}
	}
	return out, nil
}

// nonNegative folds the deckUnlimited sentinel into 0, so -1 does not leak to the caller; whether it is unlimited is expressed by the boolean field.
func nonNegative(left int) int {
	if left < 0 {
		return 0
	}
	return left
}

// NewCardsAvailable returns the total number of new cards in the deck that userID has not studied yet and that can be introduced right now, ignoring daily allowances.
//
// "Another round" uses this to decide whether there are new cards to add and how many by default: the allowance only decides how many are released today, while this number is
// how many are left in the deck in total. The filters are shared with the queue's new-card fetch via newCardsFrom (excluding suspended and buried), and when
// tags is non-empty only cards carrying any of those tags are counted, the same semantics as the tagged review queue.
func (b *QueueBuilder) NewCardsAvailable(ctx context.Context, userID, deckID uint64, tags []string, now time.Time) (int, error) {
	if userID == 0 || deckID == 0 {
		return 0, errors.New("schedule: new cards available: user id and deck id are required")
	}
	counts, err := countByDeck(b.newCardsFrom(ctx, userID, []uint64{deckID}, tags, now.UTC()))
	if err != nil {
		return 0, fmt.Errorf("schedule: count available new cards: %w", err)
	}
	return counts[deckID], nil
}
