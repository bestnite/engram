package schedule

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.nite07.com/nite/engram/internal/store"
)

// Documented defaults for queue construction. The day-cutoff default takes store as the single source of truth,
// because the normalisation rule (0/out of range → default) must be shared by store/schedule/web/reminder/digest.
const (
	DefaultDayCutoffHour = store.DefaultDayCutoffHour
	DefaultNewPerDay     = 20
	DefaultReviewsPerDay = 200
	DefaultReviewBatch   = 200
	DefaultQueueTimezone = "UTC"
)

// deckUnlimited is the sentinel for "unlimited today for this field in this deck". A deck column of 0 means unlimited, not "fall back to the default",
// so "unlimited" has to be distinguishable from "0 cards left".
const deckUnlimited = -1

// ReviewOrder decides how due review cards are sorted.
type ReviewOrder int

const (
	// OrderByRetrievability sorts by ascending retention probability (default, the most likely to be forgotten comes first).
	OrderByRetrievability ReviewOrder = iota
	// OrderByDueAt sorts by ascending due time (classic order).
	OrderByDueAt
)

// NewOrder decides the presentation order of new cards.
type NewOrder int

const (
	// NewOrderRandom shuffles randomly, avoiding always studying only the first few (default).
	NewOrderRandom NewOrder = iota
	// NewOrderCreated follows creation order, convenient for tests and for an "introduce in order" preference.
	NewOrderCreated
)

// QueueKind identifies the origin, hence the priority, of a queue item.
type QueueKind int

const (
	// QueueLearning is a due card in the learning/relearning phase, highest priority.
	QueueLearning QueueKind = iota
	// QueueReview is a due review card.
	QueueReview
	// QueueNew is a new card.
	QueueNew
)

// QueueOptions are the inputs for one queue construction. Zero values are filled in by withDefaults to the documented defaults.
//
// Deck scope (three modes): DeckIDs non-empty (after dedup) → take all decks in the set;
// DeckID non-zero and DeckIDs empty → take only that deck; both empty → the deck set **visible** to that user
// (store.DeckStore.VisibleIDs, the same set as the deck list page, excluding other people's decks).
//
// Daily caps are always **computed per deck** (new_per_day/reviews_per_day are deck-level columns):
// each deck in scope computes its remaining allowance from its own columns and today's usage; multiple decks / whole library = the sum of the per-deck allowances.
// This package no longer has any "global total" path.
//
// Override precedence (only for tests and explicit entry points; production entry points do not pass these):
//  1. NewPerDayOverride / ReviewsPerDayOverride non-nil — applies uniformly to every deck in scope,
//     0 means unlimited (the pointer distinguishes "unset" from "explicit 0");
//  2. NewPerDay / ReviewsPerDay positive — likewise applies uniformly to every deck in scope (retained legacy entry);
//  3. otherwise read each deck's new_per_day / reviews_per_day, where a column value of 0 means unlimited;
//  4. only when the deck row does not exist (column unreadable) fall back to DefaultNewPerDay.
type QueueOptions struct {
	// When DeckID is 0, cards are taken across all decks visible to the user; this field is ignored when DeckIDs is non-empty.
	DeckID uint64
	// DeckIDs is the set of decks to review; when non-empty (after dedup) it takes precedence over DeckID, an empty slice means the whole-library mode.
	//
	// Input contract: **the caller must check permission for every id individually first** (at least the reader role on each id); any unreadable id
	// fails the whole request. This builder does not check permissions and does not filter out unauthorised ids: every id here is treated as a readable deck
	// and expanded directly to fetch cards.
	//
	// Why permission checking is not moved into the builder: once it sinks into the builder, the only expressible behaviour is "silently drop that deck, return the remaining cards",
	// which would degrade "unauthorised deck = the whole request fails" into partial success, contradicting the established rule
	// (that rule is fulfilled per id by the REST API.DueCards and the web loadDeckForRole).
	DeckIDs []uint64
	// Now is the observation instant; the zero value means use the current time. Due decisions and the review day are both based on it.
	Now time.Time
	// Location is the user's timezone; when nil it is loaded from the Timezone name (falling back to UTC on failure).
	Location *time.Location
	// Timezone is an IANA timezone name, used only when Location is nil.
	Timezone string
	// DayCutoffHour is the review day cutoff (local hour); nil defaults to 4, an explicit 0 is midnight.
	DayCutoffHour *int
	// NewPerDay is a caller override for the daily new-card cap; when <= 0 the deck value is read instead.
	NewPerDay int
	// ReviewsPerDay is a caller override for the daily review cap; when <= 0 the deck value is read instead (a deck value of 0 means unlimited).
	ReviewsPerDay int
	// NewPerDayOverride / ReviewsPerDayOverride are explicit overrides; when non-nil they take precedence over the deck settings.
	// Pointers are used because an integer zero cannot distinguish "unset" from "explicit 0 (unlimited)".
	NewPerDayOverride     *int
	ReviewsPerDayOverride *int
	// ReviewOrder — see ReviewOrder.
	ReviewOrder ReviewOrder
	// NewOrder — see NewOrder.
	NewOrder NewOrder
	// BatchSize is the batch size for fetching review cards at a time (retrievability has to be sorted in memory).
	BatchSize int
	// LearnAhead is the learn-ahead window: cards in the learning / relearning phase due within (Now, Now+LearnAhead] are appended to the end of the queue,
	// and they only come up after all other cards are done (the queue is consumed in order and rebuilt after each grade). 0 means no look-ahead.
	LearnAhead time.Duration
	// When Tags is non-empty, only cards whose note carries any of those tags are taken (union; matching rules in store.WhereNoteTagsAny);
	// an empty slice means no tag filtering. Tags only narrow the fetched range: daily allowances are still computed per deck and are shared with the untagged queue
	// (new cards introduced today under some tag also consume that deck's new_per_day); the scheduling rules are unchanged.
	// This builder does not limit the number of decks in scope; "tags require exactly one deck" is validated by the caller's business layer.
	Tags []string
	// Rand is an optional random source; it only affects NewOrderRandom. When nil, a fixed seed is derived from (user, review day),
	// so repeated builds within the same day yield the same permutation (see queueShuffleSeed).
	Rand *rand.Rand
}

// withDefaults fills zero-value fields in with the documented defaults. An integer zero cannot distinguish "unset" from "explicit 0",
// so the daily caps are not among the defaults — they must be read from each deck first (0 in the deck column expresses "unlimited").
func (o QueueOptions) withDefaults() QueueOptions {
	if o.Timezone == "" {
		o.Timezone = DefaultQueueTimezone
	}

	if o.BatchSize == 0 {
		o.BatchSize = DefaultReviewBatch
	}
	return o
}

// QueueItem is one card in the queue; Retrievability is only meaningful for review cards, it is 0 otherwise.
type QueueItem struct {
	CardID         uint64
	NoteID         uint64
	DeckID         uint64
	State          State
	DueAt          time.Time
	Retrievability float64
	Kind           QueueKind
}

// DeckQueueCounts is the queue composition for a single deck: New is the number of new cards that can still be introduced today,
// Review is the number of due cards available today (due learning/relearning cards + review cards limited by reviews_per_day).
// The two added together equal the number of cards actually available after entering that deck, which is exactly what those two numbers mean on the list page.
type DeckQueueCounts struct{ New, Review int }

// QueueBuilder constructs the review queue from card_states / reviews.
type QueueBuilder struct {
	db        *gorm.DB
	decks     *store.DeckStore
	scheduler *Scheduler
}

// NewQueueBuilder constructs a queue builder.
// decks is required: the whole-library mode relies on it to resolve "the deck set visible to the user". An optional setter is deliberately not provided —
// forgetting to inject it once silently degraded to "no deck filtering", which is exactly why other people's private decks leaked into the queue.
func NewQueueBuilder(db *gorm.DB, decks *store.DeckStore, s *Scheduler) *QueueBuilder {
	return &QueueBuilder{db: db, decks: decks, scheduler: s}
}

// validate checks the three dependencies required to build a queue; a missing one reports an English error (no silent degradation).
func (b *QueueBuilder) validate(userID uint64) error {
	if userID == 0 {
		return errors.New("schedule: build queue: user id is required")
	}
	if b.decks == nil {
		return errors.New("schedule: build queue: deck store is required")
	}
	if b.scheduler == nil {
		return errors.New("schedule: build queue: scheduler is required")
	}
	return nil
}

// stateRow carries the intermediate query result: the cs columns + the locating columns of the owning card/note.
type stateRow struct {
	CardID       uint64     `gorm:"column:card_id"`
	NoteID       uint64     `gorm:"column:note_id"`
	DeckID       uint64     `gorm:"column:deck_id"`
	State        string     `gorm:"column:state"`
	DueAt        *time.Time `gorm:"column:due_at"`
	Stability    *float64   `gorm:"column:stability"`
	Difficulty   *float64   `gorm:"column:difficulty"`
	LastReviewAt *time.Time `gorm:"column:last_review_at"`
}

// toCardState turns the intermediate result into a CardState usable by the scheduler.
func (r stateRow) toCardState() *store.CardState {
	return &store.CardState{
		CardID:       r.CardID,
		State:        r.State,
		DueAt:        r.DueAt,
		Stability:    r.Stability,
		Difficulty:   r.Difficulty,
		LastReviewAt: r.LastReviewAt,
	}
}

// Build constructs the queue in the priority order "learning cards → due review cards → new cards".
// Learning cards consume no allowance; review cards and new cards are each limited by their deck's reviews_per_day / new_per_day.
func (b *QueueBuilder) Build(ctx context.Context, userID uint64, opts QueueOptions) ([]QueueItem, error) {
	if err := b.validate(userID); err != nil {
		return nil, err
	}
	opts = opts.withDefaults()
	now := opts.now()

	// The random source is fixed before fetching cards: the same source first decides "which new cards to draw from each deck" (the ordering key in SQL),
	// then decides the merged presentation order; both steps are determined by (user, review day) and reproducible.
	var rng *rand.Rand
	var order *newCardOrder
	if opts.NewOrder == NewOrderRandom {
		rng = opts.Rand
		if rng == nil {
			loc, err := opts.location()
			if err != nil {
				return nil, err
			}
			rng = rand.New(rand.NewSource(queueShuffleSeed(userID, ReviewDay(now, loc, store.ResolveCutoff(opts.DayCutoffHour)))))
		}
		order = randomNewCardOrder(rng)
	}

	col, err := b.collect(ctx, userID, opts, order)
	if err != nil {
		return nil, err
	}

	// Review cards: fetched deck by deck, then merged, globally sorted the requested way, then truncated to BatchSize (the upper bound for in-memory sorting).
	reviews := col.mergeReviews()
	if opts.ReviewOrder == OrderByRetrievability {
		for i := range reviews {
			r, err := b.scheduler.Retrievability(reviews[i].row.toCardState(), now)
			if err != nil {
				return nil, fmt.Errorf("schedule: compute retrievability for card %d: %w", reviews[i].item.CardID, err)
			}
			reviews[i].item.Retrievability = r
		}
	}
	sort.SliceStable(reviews, func(i, j int) bool {
		if opts.ReviewOrder == OrderByRetrievability && reviews[i].item.Retrievability != reviews[j].item.Retrievability {
			return reviews[i].item.Retrievability < reviews[j].item.Retrievability
		}
		if !reviews[i].item.DueAt.Equal(reviews[j].item.DueAt) {
			return reviews[i].item.DueAt.Before(reviews[j].item.DueAt)
		}
		return reviews[i].item.CardID < reviews[j].item.CardID
	})
	if len(reviews) > opts.BatchSize {
		reviews = reviews[:opts.BatchSize]
	}

	fresh := col.mergeFresh()
	if rng != nil && len(fresh) > 1 {
		// With multiple decks the new cards of each deck are concatenated in deck order, and they are shuffled once more here so that decks interleave too.
		rng.Shuffle(len(fresh), func(i, j int) { fresh[i], fresh[j] = fresh[j], fresh[i] })
	}

	out := make([]QueueItem, 0, len(col.learning)+len(reviews)+len(fresh))
	out = append(out, col.learning...)
	for i := range reviews {
		out = append(out, reviews[i].item)
	}
	out = append(out, fresh...)

	// Learn-ahead: learning steps are often only 1 minute, 10 minutes, and after pressing Again on the last card the queue empties and the session
	// ends, while that card is not due for a few minutes. So learning cards that fall due within the window are placed last: they do not intrude while other cards exist,
	// and once those run out you simply keep studying.
	if opts.LearnAhead > 0 && len(col.order) > 0 {
		ahead, err := b.learningAhead(ctx, userID, now, now.Add(opts.LearnAhead), col.order, opts.Tags)
		if err != nil {
			return nil, err
		}
		out = append(out, ahead...)
	}
	return out, nil
}

// learningAhead fetches learning / relearning cards due within (from, until], in ascending due time (used only for learn-ahead).
func (b *QueueBuilder) learningAhead(ctx context.Context, userID uint64, from, until time.Time, deckIDs []uint64, tags []string) ([]QueueItem, error) {
	var rows []stateRow
	err := b.baseStateQuery(ctx, userID, deckIDs, tags).
		Where("cs.state IN ?", []string{StateLearning.String(), StateRelearning.String()}).
		Where("cs.due_at > ? AND cs.due_at <= ?", from, until).
		Order("cs.due_at ASC, cs.card_id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("schedule: load learn-ahead cards: %w", err)
	}
	return rowsToItems(rows, QueueLearning, from)
}

// queueShuffleSeed derives the shuffle seed for new cards from (user, review day).
//
// Why not seed from "the build instant": that value differs on every request, so within the same day a page refresh or fetching the next batch of new cards would
// switch to a different permutation — the order the learner sees would be unstable, and reported problems could not be reproduced. With (user, review day),
// the order is fixed within a day (the order of a given batch is reproducible) and rotates automatically the next day, still serving the original intent of
// "avoid always studying only the first few"; and different users on the same day do not share a permutation.
//
// The review day must use this package's single ReviewDay definition (including the user's timezone and cutoff), otherwise the rotation point would drift a day away from quotas, streaks
// and reminders — from every feature that counts by review day.
func queueShuffleSeed(userID uint64, day string) int64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], userID)
	h.Write(buf[:])
	h.Write([]byte(day))
	return int64(h.Sum64())
}

// DeckCounts returns the composition of what is studyable today per deck: New = the number of new cards that can still be introduced today, Review = due learning/relearning
// cards + due review cards limited by reviews_per_day. The total number of cards available after entering that deck is the sum of these two
// (Build returns cards in batches of at most BatchSize and fetches the next batch when drained, so this is **not** truncated by the batch size).
//
// It shares the same set of filter conditions with Build (stateFrom / learningDueWhere / reviewDueWhere / newCardsFrom)
// and the same allowance formula (deckBudget), only replacing row fetching with a COUNT grouped by deck: the list page does not need the cards themselves,
// and an unlimited deck will not pull every row into memory either.
//
// opts only reads Now, Location/Timezone and DayCutoffHour: "today's usage" is counted by the user's own review day,
// and must match the semantics written into reviews.review_day at grading time. An empty deckIDs resolves to the deck set visible to that user.
func (b *QueueBuilder) DeckCounts(ctx context.Context, userID uint64, deckIDs []uint64, opts QueueOptions) (map[uint64]DeckQueueCounts, error) {
	if userID == 0 {
		return nil, errors.New("schedule: deck counts: user id is required")
	}
	if b.decks == nil {
		return nil, errors.New("schedule: deck counts: deck store is required")
	}
	opts = QueueOptions{
		DeckIDs: deckIDs, Now: opts.Now, Location: opts.Location, Timezone: opts.Timezone,
		DayCutoffHour: opts.DayCutoffHour,
	}.withDefaults()
	now := opts.now()
	scope, err := b.resolveScope(ctx, userID, opts)
	if err != nil {
		return nil, err
	}
	out := make(map[uint64]DeckQueueCounts, len(scope))
	if len(scope) == 0 {
		return out, nil
	}
	ids := make([]uint64, 0, len(scope))
	for _, d := range scope {
		ids = append(ids, d.id)
	}

	learning, err := countByDeck(learningDueWhere(b.stateFrom(ctx, userID, ids, nil), now))
	if err != nil {
		return nil, fmt.Errorf("schedule: count learning cards: %w", err)
	}
	reviews, err := countByDeck(reviewDueWhere(b.stateFrom(ctx, userID, ids, nil), now))
	if err != nil {
		return nil, fmt.Errorf("schedule: count due reviews: %w", err)
	}
	fresh, err := countByDeck(b.newCardsFrom(ctx, userID, ids, nil, now))
	if err != nil {
		return nil, fmt.Errorf("schedule: count new cards: %w", err)
	}
	for _, d := range scope {
		out[d.id] = DeckQueueCounts{
			New:    capCount(fresh[d.id], d.newLeft),
			Review: learning[d.id] + capCount(reviews[d.id], d.reviewLeft),
		}
	}
	return out, nil
}

// capCount truncates the available count to the remaining allowance; no truncation when the allowance is deckUnlimited.
func capCount(n, left int) int {
	if left != deckUnlimited && n > left {
		return left
	}
	return n
}

// countByDeck counts a query that already has its filters applied, grouped by notes.deck_id.
func countByDeck(q *gorm.DB) (map[uint64]int, error) {
	var rows []struct {
		DeckID uint64 `gorm:"column:deck_id"`
		N      int    `gorm:"column:n"`
	}
	if err := q.Select("notes.deck_id AS deck_id, COUNT(*) AS n").Group("notes.deck_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint64]int, len(rows))
	for _, r := range rows {
		out[r.DeckID] = r.N
	}
	return out, nil
}

// deckScope is one scope entry resolved for a deck in this build: its remaining allowance for today.
type deckScope struct {
	id uint64
	// newLeft / reviewLeft are how many cards can still be taken today; deckUnlimited means unlimited.
	newLeft    int
	reviewLeft int
}

// dueReview is one fetched due review card: the queue item plus the state row needed to compute retrievability on demand.
type dueReview struct {
	item QueueItem
	row  stateRow
}

// collectedQueue is the intermediate result of the "fetch cards" phase; Build and DeckCounts share the same path.
type collectedQueue struct {
	// learning holds due learning/relearning cards, one query across the whole scope, ascending due_at (not truncated by allowances).
	learning []QueueItem
	// reviews / fresh are grouped by deck, each already truncated to that deck's remaining allowance.
	reviews map[uint64][]dueReview
	fresh   map[uint64][]QueueItem
	// order is the deck order within the scope, keeping the merged result deterministic.
	order []uint64
}

// mergeReviews concatenates the review cards of each deck into one slice in scope order; the global sorting is done by the caller.
func (c *collectedQueue) mergeReviews() []dueReview {
	total := 0
	for _, id := range c.order {
		total += len(c.reviews[id])
	}
	out := make([]dueReview, 0, total)
	for _, id := range c.order {
		out = append(out, c.reviews[id]...)
	}
	return out
}

// mergeFresh concatenates the new cards of each deck into one slice in scope order; the shuffling is done by the caller.
func (c *collectedQueue) mergeFresh() []QueueItem {
	total := 0
	for _, id := range c.order {
		total += len(c.fresh[id])
	}
	out := make([]QueueItem, 0, total)
	for _, id := range c.order {
		out = append(out, c.fresh[id]...)
	}
	return out
}

// collect is the card-fetch path shared by Build and DeckCounts: resolve the scope and each deck's allowance, then fetch deck by deck
// the learning cards (one query), the due review cards (per deck, limited by that deck's remaining allowance), and the new cards (per deck, limited by the remaining allowance).
// When order is nil, new cards are fetched in creation order; otherwise by the pseudo-random sort key given by order.
func (b *QueueBuilder) collect(ctx context.Context, userID uint64, opts QueueOptions, order *newCardOrder) (*collectedQueue, error) {
	opts = opts.withDefaults()
	now := opts.now()

	scope, err := b.resolveScope(ctx, userID, opts)
	if err != nil {
		return nil, err
	}
	out := &collectedQueue{reviews: map[uint64][]dueReview{}, fresh: map[uint64][]QueueItem{}}
	if len(scope) == 0 {
		// An empty set = no decks to take from (e.g. the user has no visible decks): the queue is empty and no error is reported.
		return out, nil
	}

	deckIDs := make([]uint64, 0, len(scope))
	for _, d := range scope {
		deckIDs = append(deckIDs, d.id)
		out.order = append(out.order, d.id)
	}

	// Due learning/relearning cards: one query fetches them all, not truncated by allowances (learning cards do not consume the review cap).
	learning, err := b.learningDue(ctx, userID, now, deckIDs, opts.Tags)
	if err != nil {
		return nil, err
	}
	out.learning = learning

	// Due review cards: fetched deck by deck up to each deck's remaining allowance; when unlimited each deck fetches BatchSize, and the caller truncates after merging.
	for _, d := range scope {
		if d.reviewLeft == 0 {
			continue
		}
		limit := d.reviewLeft
		if limit == deckUnlimited {
			limit = opts.BatchSize
		}
		rows, err := b.reviewDueDeck(ctx, userID, now, d.id, opts.Tags, limit)
		if err != nil {
			return nil, err
		}
		out.reviews[d.id] = rows
	}

	// New cards: fetched deck by deck up to each deck's remaining allowance; no LIMIT is set when unlimited.
	for _, d := range scope {
		if d.newLeft == 0 {
			continue
		}
		items, err := b.newCardsDeck(ctx, userID, d.id, opts.Tags, d.newLeft, now, order)
		if err != nil {
			return nil, err
		}
		out.fresh[d.id] = items
	}
	return out, nil
}

// resolveScope resolves the three deck modes into a list of "deck → remaining allowance today".
// Scope: DeckIDs non-empty (deduped) → that set; otherwise DeckID != 0 → a single deck; both empty → the decks visible to that user.
// It then loads each deck's caps and today's usage and computes the remaining allowance per deck with deckBudget (the rule is written in one place only).
func (b *QueueBuilder) resolveScope(ctx context.Context, userID uint64, opts QueueOptions) ([]deckScope, error) {
	ids, err := b.scopeDeckIDs(ctx, userID, opts)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	caps, err := b.loadDeckCaps(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	now := opts.now()
	loc, err := opts.location()
	if err != nil {
		return nil, err
	}
	day := ReviewDay(now, loc, store.ResolveCutoff(opts.DayCutoffHour))
	usage, err := b.countUsage(ctx, userID, day, ids)
	if err != nil {
		return nil, err
	}

	out := make([]deckScope, 0, len(ids))
	for _, id := range ids {
		c, ok := caps[id]
		if !ok {
			// Only an unreadable deck column (deck row missing) falls back to the default; a deck column of 0 means unlimited, not falling back to the default.
			c = store.DeckCaps{NewPerDay: DefaultNewPerDay}
		}
		// Explicit overrides apply uniformly to every deck in scope; the legacy entry's positive fields mean the same (see the QueueOptions doc).
		if opts.NewPerDayOverride != nil {
			c.NewPerDay = *opts.NewPerDayOverride
		} else if opts.NewPerDay > 0 {
			c.NewPerDay = opts.NewPerDay
		}
		if opts.ReviewsPerDayOverride != nil {
			c.ReviewsPerDay = *opts.ReviewsPerDayOverride
		} else if opts.ReviewsPerDay > 0 {
			c.ReviewsPerDay = opts.ReviewsPerDay
		}
		newLeft, reviewLeft := deckBudget(c, usage[id])
		out = append(out, deckScope{id: id, newLeft: newLeft, reviewLeft: reviewLeft})
	}
	return out, nil
}

// scopeDeckIDs normalises the three deck modes into one deduplicated id set:
// DeckIDs wins when non-empty (deduped), otherwise a non-zero DeckID returns a single element, and when both are empty the user's
// default study scope is returned: the visible decks minus the ones the user paused. A paused deck named in DeckID or DeckIDs is
// studied as usual, because naming it is an explicit choice.
func (b *QueueBuilder) scopeDeckIDs(ctx context.Context, userID uint64, opts QueueOptions) ([]uint64, error) {
	if len(opts.DeckIDs) > 0 {
		return dedupeIDs(opts.DeckIDs), nil
	}
	if opts.DeckID != 0 {
		return []uint64{opts.DeckID}, nil
	}
	ids, err := b.decks.ActiveIDs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("schedule: resolve active decks: %w", err)
	}
	return ids, nil
}

// deckBudget computes a deck's remaining new-card and review allowance for today; this is the single implementation of "how the daily allowance is computed",
// Build, DeckCounts and DeckBudgets all go through it, so the formula cannot drift across several places.
// An entry in caps <= 0 means that entry is unlimited and deckUnlimited is returned; otherwise max(cap-used, 0) is returned.
// The new-card cap is the daily cap plus the allowance added today by "another round": the daily cap is the study goal, the addition only raises today's amount.
// An unlimited deck needs no addition; the added amount has no effect on it.
func deckBudget(caps store.DeckCaps, u deckUsage) (newLeft, reviewLeft int) {
	newCap := caps.NewPerDay
	if newCap > 0 {
		newCap += u.extraNew
	}
	return budgetLeft(newCap, u.introduced), budgetLeft(caps.ReviewsPerDay, u.reviewed)
}

// budgetLeft returns the remaining allowance after capping; cap <= 0 is unlimited.
func budgetLeft(cap, used int) int {
	if cap <= 0 {
		return deckUnlimited
	}
	if left := cap - used; left > 0 {
		return left
	}
	return 0
}

// dedupeIDs removes duplicate deck ids and keeps first-occurrence order (the set the caller passes may be unordered).
func dedupeIDs(ids []uint64) []uint64 {
	out := make([]uint64, 0, len(ids))
	seen := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// location resolves the user's timezone; when unspecified it is loaded from Timezone, falling back to UTC on failure (so review is never blocked).
func (o QueueOptions) location() (*time.Location, error) {
	if o.Location != nil {
		return o.Location, nil
	}
	loc, err := time.LoadLocation(o.Timezone)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// now returns the observation instant (UTC).
func (o QueueOptions) now() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now.UTC()
}

// deckUsage is a deck's usage today: introduced is the number of new cards introduced today (state_before = 0),
// reviewed is today's review volume (state_before <> 0); neither uses a counter table, both are aggregated from reviews;
// extraNew is the new-card allowance added today by "another round" (store.DeckDailyExtra).
type deckUsage struct{ introduced, reviewed, extraNew int }

// loadDeckCaps reads userID's daily caps on each deck: the owner reads the deck columns, other members read their own member settings
// (the rule lives only in store.StudySettingsFor).
func (b *QueueBuilder) loadDeckCaps(ctx context.Context, userID uint64, deckIDs []uint64) (map[uint64]store.DeckCaps, error) {
	var decks []store.Deck
	if err := b.db.WithContext(ctx).Select("id", "owner_user_id", "preset_id", "new_per_day", "reviews_per_day").
		Where("id IN ?", deckIDs).Find(&decks).Error; err != nil {
		return nil, fmt.Errorf("schedule: load deck caps: %w", err)
	}
	settings, err := store.StudySettingsFor(ctx, b.db, userID, decks)
	if err != nil {
		return nil, fmt.Errorf("schedule: load deck caps: %w", err)
	}
	out := make(map[uint64]store.DeckCaps, len(settings))
	for id, st := range settings {
		out[id] = st.Caps
	}
	return out, nil
}

// countUsage aggregates each deck's usage today from reviews by (user_id, review_day), fetched in one grouped query,
// then merges in the added new-card allowance for the same review day. New cards count state_before = 0, reviews count state_before <> 0;
// allowances are deck-level, so the grouping must be by deck (new_per_day/reviews_per_day are defined on the deck).
func (b *QueueBuilder) countUsage(ctx context.Context, userID uint64, day string, deckIDs []uint64) (map[uint64]deckUsage, error) {
	var rows []struct {
		DeckID     uint64 `gorm:"column:deck_id"`
		Introduced int    `gorm:"column:introduced"`
		Reviewed   int    `gorm:"column:reviewed"`
	}
	if err := b.db.WithContext(ctx).Table("reviews AS r").
		Select("n.deck_id AS deck_id, "+
			"SUM(CASE WHEN r.state_before = ? THEN 1 ELSE 0 END) AS introduced, "+
			"SUM(CASE WHEN r.state_before <> ? THEN 1 ELSE 0 END) AS reviewed",
			int(StateNew), int(StateNew)).
		Joins("JOIN cards AS c ON c.id = r.card_id").
		Joins("JOIN notes AS n ON n.id = c.note_id").
		Where("r.user_id = ? AND r.review_day = ? AND n.deck_id IN ?", userID, day, deckIDs).
		Group("n.deck_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("schedule: count deck usage: %w", err)
	}
	out := make(map[uint64]deckUsage, len(rows))
	for _, r := range rows {
		out[r.DeckID] = deckUsage{introduced: r.Introduced, reviewed: r.Reviewed}
	}
	extras, err := store.DailyExtraNew(ctx, b.db, userID, day, deckIDs)
	if err != nil {
		return nil, fmt.Errorf("schedule: count deck usage: %w", err)
	}
	for id, n := range extras {
		u := out[id]
		u.extraNew = n
		out[id] = u
	}
	return out, nil
}

// stateFrom is the FROM and filters shared by learning and review cards: only cards that are neither soft-deleted nor suspended,
// joined to notes that are not soft-deleted (consistent with CardStore's visibility rules). It carries no SELECT;
// row fetching (baseStateQuery) and counting (countByDeck) each add their own.
// deckIDs is the already-resolved scope set and is always non-empty (an empty set returns early in the caller); empty tags mean no tag filtering.
func (b *QueueBuilder) stateFrom(ctx context.Context, userID uint64, deckIDs []uint64, tags []string) *gorm.DB {
	q := b.db.WithContext(ctx).Table("card_states AS cs").
		Joins("JOIN cards AS cards ON cards.id = cs.card_id AND cards.deleted_at IS NULL").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("cs.user_id = ?", userID).
		Where("cs.suspended_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs)
	return store.WhereNoteTagsAny(q, tags)
}

// baseStateQuery adds the columns needed for row fetching on top of stateFrom.
func (b *QueueBuilder) baseStateQuery(ctx context.Context, userID uint64, deckIDs []uint64, tags []string) *gorm.DB {
	return b.stateFrom(ctx, userID, deckIDs, tags).
		Select("cs.card_id AS card_id, cards.note_id AS note_id, notes.deck_id AS deck_id, " +
			"cs.state AS state, cs.due_at AS due_at, cs.stability AS stability, " +
			"cs.difficulty AS difficulty, cs.last_review_at AS last_review_at")
}

// learningDueWhere restricts to due cards in the learning/relearning phase.
func learningDueWhere(q *gorm.DB, now time.Time) *gorm.DB {
	return q.Where("cs.state IN ?", []string{StateLearning.String(), StateRelearning.String()}).
		Where("cs.due_at IS NOT NULL AND cs.due_at <= ?", now)
}

// reviewDueWhere restricts to due cards in the review phase.
func reviewDueWhere(q *gorm.DB, now time.Time) *gorm.DB {
	return q.Where("cs.state = ?", StateReview.String()).
		Where("cs.due_at IS NOT NULL AND cs.due_at <= ?", now)
}

// learningDue fetches due cards in the learning/relearning phase, in ascending due time.
func (b *QueueBuilder) learningDue(ctx context.Context, userID uint64, now time.Time, deckIDs []uint64, tags []string) ([]QueueItem, error) {
	var rows []stateRow
	err := learningDueWhere(b.baseStateQuery(ctx, userID, deckIDs, tags), now).
		Order("cs.due_at ASC, cs.card_id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("schedule: load learning cards: %w", err)
	}
	return rowsToItems(rows, QueueLearning, now)
}

// reviewDueDeck fetches a single deck's due review cards, ascending by due_at, at most limit rows.
// Fetching deck by deck is how each deck's own reviews_per_day lands in the SQL LIMIT; the global sort after merging is up to the caller.
func (b *QueueBuilder) reviewDueDeck(ctx context.Context, userID uint64, now time.Time, deckID uint64, tags []string, limit int) ([]dueReview, error) {
	var rows []stateRow
	err := reviewDueWhere(b.baseStateQuery(ctx, userID, []uint64{deckID}, tags), now).
		Order("cs.due_at ASC, cs.card_id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("schedule: load due reviews: %w", err)
	}
	out := make([]dueReview, 0, len(rows))
	for _, row := range rows {
		item, err := rowToItem(row, QueueReview, now)
		if err != nil {
			return nil, err
		}
		out = append(out, dueReview{item: item, row: row})
	}
	return out, nil
}

// newCardOrder holds the sort-key parameters for pseudo-random new-card drawing (see sortKeySQL).
//
// Why sort in SQL rather than shuffle after fetching: the daily cap has to land in the LIMIT, and if you LIMIT by creation order first and then shuffle in memory,
// the cards drawn will always be the earliest created ones, so "random" would only change the presentation order that day. The parameters vary with the random source of
// (user, review day), so the set drawn differs on each review day.
type newCardOrder struct {
	Mul1 int64
	Add  int64
	Mul2 int64
}

const newCardOrderModulus = int64(1) << 31

// randomNewCardOrder draws a set of sort-key parameters from the random source; both multipliers are forced odd.
func randomNewCardOrder(rng *rand.Rand) *newCardOrder {
	return &newCardOrder{
		Mul1: rng.Int63n(newCardOrderModulus/2)*2 + 1,
		Add:  rng.Int63n(newCardOrderModulus),
		Mul2: rng.Int63n(newCardOrderModulus/2)*2 + 1,
	}
}

// sortKeySQL returns a sort-key expression computed from cards.id, shaped like a small integer hash:
//
//	h1  = ((id mod 2^31) × Mul1 + Add) mod 2^31
//	h2  = h1 xor (h1 / 2^16)          // xor is written as (a|b) − (a&b); both databases only have | and &
//	key = (h2 × Mul2) mod 2^31
//
// With only multiply-add-modulo, consecutive small ids form an arithmetic sequence and the ordering is still essentially creation order; the xor in the middle breaks the linearity.
// All three steps are bijective on [0, 2^31) (odd multipliers), so no ties occur; every product is below 2^62,
// so neither database overflows. The expression contains only integers generated by this function, with no external input at all.
func (o *newCardOrder) sortKeySQL() string {
	m := newCardOrderModulus
	h1 := fmt.Sprintf("(((cards.id %% %d) * %d + %d) %% %d)", m, o.Mul1, o.Add, m)
	hi := fmt.Sprintf("(%s / 65536)", h1)
	h2 := fmt.Sprintf("((%s | %s) - (%s & %s))", h1, hi, h1, hi)
	return fmt.Sprintf("((%s * %d) %% %d)", h2, o.Mul2, m)
}

// newCardsDeck fetches a single deck's new cards: creation order when order is nil, otherwise the pseudo-random sort key. A new card is defined as
// "state is new", including cards with no card_states row yet (LEFT JOIN), so rows just added to a shared deck for which the user has produced
// no state yet can still appear in the queue. A buried new card (due_at pushed into the future) does not count as today's new card, hence the extra requirement that
// due_at is not in the future. No LIMIT is set when limit is deckUnlimited.
func (b *QueueBuilder) newCardsDeck(ctx context.Context, userID uint64, deckID uint64, tags []string, limit int, now time.Time, order *newCardOrder) ([]QueueItem, error) {
	q := b.newCardsFrom(ctx, userID, []uint64{deckID}, tags, now).
		Select("cards.id AS card_id, cards.note_id AS note_id, notes.deck_id AS deck_id, " +
			"COALESCE(cs.state, 'new') AS state, cs.due_at AS due_at, cs.stability AS stability, " +
			"cs.difficulty AS difficulty, cs.last_review_at AS last_review_at")
	if order != nil {
		// It must be wrapped in clause.OrderBy: GORM's Order silently ignores a bare clause.Expr — no error, no sorting.
		q = q.Order(clause.OrderBy{Expression: clause.Expr{SQL: order.sortKeySQL() + ", cards.id ASC"}})
	} else {
		q = q.Order("cards.created_at ASC, cards.id ASC")
	}
	if limit != deckUnlimited {
		q = q.Limit(limit)
	}
	var rows []stateRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("schedule: load new cards: %w", err)
	}
	return rowsToItems(rows, QueueNew, now)
}

// newCardsFrom is the FROM and filters for new cards (shared by row fetching and counting): cards whose state is new, including those with no
// card_states row; buried new cards (due_at in the future) are excluded. Empty tags mean no tag filtering.
func (b *QueueBuilder) newCardsFrom(ctx context.Context, userID uint64, deckIDs []uint64, tags []string, now time.Time) *gorm.DB {
	q := b.db.WithContext(ctx).Table("cards AS cards").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Joins("LEFT JOIN card_states AS cs ON cs.card_id = cards.id AND cs.user_id = ?", userID).
		Where("cards.deleted_at IS NULL AND cs.suspended_at IS NULL").
		Where("(cs.card_id IS NULL OR cs.state = ?)", StateNew.String()).
		Where("(cs.due_at IS NULL OR cs.due_at <= ?)", now).
		Where("notes.deck_id IN ?", deckIDs)
	return store.WhereNoteTagsAny(q, tags)
}

// rowToItem turns one query row into a queue item; now fills in the missing due time of new cards.
func rowToItem(row stateRow, kind QueueKind, now time.Time) (QueueItem, error) {
	state, err := ParseState(row.State)
	if err != nil {
		return QueueItem{}, err
	}
	due := derefTime(row.DueAt)
	if due.IsZero() {
		due = now
	}
	return QueueItem{
		CardID: row.CardID,
		NoteID: row.NoteID,
		DeckID: row.DeckID,
		State:  state,
		DueAt:  due.UTC(),
		Kind:   kind,
	}, nil
}

// rowsToItems turns query results into queue items; now fills in the missing due time of new cards.
func rowsToItems(rows []stateRow, kind QueueKind, now time.Time) ([]QueueItem, error) {
	items := make([]QueueItem, 0, len(rows))
	for _, row := range rows {
		it, err := rowToItem(row, kind, now)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// ReviewDay computes the review day (YYYY-MM-DD) from the user's local time and the cutoff: take the date after subtracting day_cutoff_hour from local time,
// so early-morning instants before the cutoff count as the previous day.
//
// The resolved cutoff only falls back to the default 4 when out of range, and midnight 0 is preserved — the same semantics as store.ReviewDayString:
// writes of the review day (this package's submit/bury) and reads (stats/streak/reminder/digest) must land on the same day, otherwise the same
// user would see a different "today" on the two sides. Nullable user settings are resolved via ResolveCutoff first; the computation function takes only a valid hour.
func ReviewDay(now time.Time, loc *time.Location, cutoffHour int) string {
	if loc == nil {
		loc = time.UTC
	}
	cutoffHour = store.NormalizedCutoff(cutoffHour)
	return now.In(loc).Add(-time.Duration(cutoffHour) * time.Hour).Format("2006-01-02")
}
