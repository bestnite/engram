package store

import (
	"time"

	"gorm.io/gorm"
)

// This file is the single definition of which cards a user can study and which of them are due or new. The review queue,
// the deck list counts, deck progress, the home and stats numbers, the reminder and the health page all build their queries
// from these functions, so "due" and "new" cannot mean different things on different pages. Hand-written copies of these
// predicates are how suspended cards and buried new cards once showed up as due on the home page but not in the queue.
//
// Every query built here uses the aliases cs (card_states), cards and notes; WhereNoteTagsAny relies on the notes alias.

// The values stored in card_states.state.
const (
	CardStateNew        = "new"
	CardStateLearning   = "learning"
	CardStateReview     = "review"
	CardStateRelearning = "relearning"
)

// LiveCardStates selects the card_states rows of every user whose card and note are not deleted and which the user has not
// suspended. A suspended card never enters the queue, so no count of studyable cards includes it.
func LiveCardStates(db *gorm.DB) *gorm.DB {
	return db.Table("card_states AS cs").
		Joins("JOIN cards AS cards ON cards.id = cs.card_id AND cards.deleted_at IS NULL").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("cs.suspended_at IS NULL")
}

// UserCardStates narrows LiveCardStates to userID's rows in the given decks. decks is either a []uint64 or a deck-id
// subquery such as the one ActiveIDs and VisibleIDs are built from.
func UserCardStates(db *gorm.DB, userID uint64, decks any) *gorm.DB {
	return whereDeckIn(LiveCardStates(db).Where("cs.user_id = ?", userID), decks)
}

// WhereLearningDue keeps the learning and relearning rows whose due time has come. These cards are never limited by a daily cap.
func WhereLearningDue(q *gorm.DB, now time.Time) *gorm.DB {
	return q.Where("cs.state IN ?", []string{CardStateLearning, CardStateRelearning}).
		Where("cs.due_at IS NOT NULL AND cs.due_at <= ?", now)
}

// WhereReviewDue keeps the review rows whose due time has come; the queue limits them by each deck's reviews_per_day.
func WhereReviewDue(q *gorm.DB, now time.Time) *gorm.DB {
	return q.Where("cs.state = ?", CardStateReview).
		Where("cs.due_at IS NOT NULL AND cs.due_at <= ?", now)
}

// WhereScheduled keeps the rows that have left the new state (learning, review or relearning). A new card is never due:
// a buried new card carries a due_at, but it re-enters the queue as a new card under new_per_day, not as a due one.
func WhereScheduled(q *gorm.DB) *gorm.DB {
	return q.Where("cs.state <> ? AND cs.due_at IS NOT NULL", CardStateNew)
}

// WhereDue keeps the scheduled rows whose due time has come, learning and review alike, with no daily cap applied. It
// answers "is anything due at all"; the number the queue actually serves comes from schedule.QueueBuilder.DueNow.
func WhereDue(q *gorm.DB, now time.Time) *gorm.DB {
	return WhereScheduled(q).Where("cs.due_at <= ?", now)
}

// UserNewCards selects userID's new cards in the given decks: live cards with no state row for the user yet, or whose row
// is still new, and not suspended. It starts from cards with a LEFT JOIN because a card added to a shared deck has no
// state row until the user first touches it. decks takes the same forms as in UserCardStates.
func UserNewCards(db *gorm.DB, userID uint64, decks any) *gorm.DB {
	q := db.Table("cards AS cards").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Joins("LEFT JOIN card_states AS cs ON cs.card_id = cards.id AND cs.user_id = ?", userID).
		Where("cards.deleted_at IS NULL AND cs.suspended_at IS NULL").
		Where("(cs.card_id IS NULL OR cs.state = ?)", CardStateNew)
	return whereDeckIn(q, decks)
}

// WhereNewAvailable drops the new cards buried until a later review day (due_at still in the future), leaving the ones
// the queue may introduce now.
func WhereNewAvailable(q *gorm.DB, now time.Time) *gorm.DB {
	return q.Where("(cs.due_at IS NULL OR cs.due_at <= ?)", now)
}

// whereDeckIn restricts notes.deck_id to decks. GORM renders the two argument forms differently: a slice needs "IN ?"
// (an empty slice becomes IN (NULL) and matches nothing), while a subquery needs "IN (?)" to be parenthesised.
func whereDeckIn(q *gorm.DB, decks any) *gorm.DB {
	if sub, ok := decks.(*gorm.DB); ok {
		return q.Where("notes.deck_id IN (?)", sub)
	}
	return q.Where("notes.deck_id IN ?", decks)
}
