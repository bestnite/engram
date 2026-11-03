package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// CardStore wraps GORM access to the cards table.
//
// Visibility rule: when a note is soft-deleted, its cards become invisible as well — that filter is implemented in the query
// layer as a join with notes, not by rewriting cards rows at delete time. That way progress data (attached to the card) is never lost because content was deleted.
type CardStore struct {
	db *gorm.DB
}

// NewCardStore constructs the card store.
func NewCardStore(db *gorm.DB) *CardStore { return &CardStore{db: db} }

// visibleCards is the single query starting point for \"card visible\": GORM adds cards.deleted_at IS NULL automatically,
// and the join condition further requires the owning note not to be soft-deleted.
func (s *CardStore) visibleCards(ctx context.Context) *gorm.DB {
	return s.db.WithContext(ctx).Model(&Card{}).
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL")
}

// ByID fetches one visible card; returns gorm.ErrRecordNotFound when the owning note is soft-deleted.
func (s *CardStore) ByID(ctx context.Context, id uint64) (*Card, error) {
	var c Card
	if err := s.visibleCards(ctx).Select("cards.*").Where("cards.id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ByNote lists a note's visible cards, ordered by ordinal, then id ascending; returns an empty slice when the note is soft-deleted.
// The returned card templates and ordinals line up with notetype's card generation.
func (s *CardStore) ByNote(ctx context.Context, noteID uint64) ([]Card, error) {
	var cards []Card
	if err := s.visibleCards(ctx).Select("cards.*").
		Where("cards.note_id = ?", noteID).
		Order("cards.ordinal ASC, cards.id ASC").Find(&cards).Error; err != nil {
		return nil, fmt.Errorf("list cards by note: %w", err)
	}
	return cards, nil
}

// Card list sort modes. They only choose the browse order; paging is always LIMIT/OFFSET.
const (
	// CardListSortCreated orders by card row id, so the order is stable and matches creation order.
	CardListSortCreated = "created"
	// CardListSortNoteType groups a deck's cards by their note type name, then by creation order.
	CardListSortNoteType = "note_type"
)

// Paging bounds of a deck card list. The defaults and caps are applied by NormalizeCardListOptions, which
// every caller runs first, so the JSON handler and the store never disagree about what a page is.
const (
	DefaultCardPageSize = 20
	MaxCardPageSize     = 100
	// MaxCardPage caps the page number: a huge page would make (page-1)*perPage overflow int into a
	// negative LIMIT/OFFSET, and pages past the bound are treated as past the last page (empty result).
	MaxCardPage = 1_000_000
)

// CardListOptions holds the browse criteria of a deck card list.
type CardListOptions struct {
	// Page starts at 1; values below 1 are treated as 1.
	Page int
	// PerPage 0 falls back to DefaultCardPageSize.
	PerPage int
	// Sort takes the CardListSort constants; any other value falls back to CardListSortCreated.
	Sort string
}

// NormalizeCardListOptions applies the paging defaults and bounds in one place.
func NormalizeCardListOptions(opts CardListOptions) CardListOptions {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.Page > MaxCardPage {
		opts.Page = MaxCardPage
	}
	if opts.PerPage <= 0 {
		opts.PerPage = DefaultCardPageSize
	}
	if opts.PerPage > MaxCardPageSize {
		opts.PerPage = MaxCardPageSize
	}
	if opts.Sort != CardListSortNoteType {
		opts.Sort = CardListSortCreated
	}
	return opts
}

// ListByDeck pages through one deck's visible cards and returns the page plus the total number of
// matches. Visibility follows the store's card rule: a card of a soft-deleted note is excluded, and
// so is a card whose own row is soft-deleted. Suspension is a per-user study state, not a visibility
// rule, so suspended cards are listed like any other content.
//
// The order is stable across pages, which is what makes next/previous paging safe: by card id, or by
// note type name and then card id. The note type join is only added for that sort, so the other order
// never pays for it.
func (s *CardStore) ListByDeck(ctx context.Context, deckID uint64, opts CardListOptions) ([]Card, int64, error) {
	opts = NormalizeCardListOptions(opts)
	build := func() *gorm.DB {
		return s.visibleCards(ctx).Where("notes.deck_id = ?", deckID)
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count cards of deck %d: %w", deckID, err)
	}
	query := build()
	switch opts.Sort {
	case CardListSortNoteType:
		query = query.Joins("JOIN note_types ON note_types.id = notes.note_type_id").
			Order("note_types.name ASC, cards.id ASC")
	default:
		query = query.Order("cards.id ASC")
	}
	var cards []Card
	offset := (opts.Page - 1) * opts.PerPage
	if err := query.Select("cards.*").Limit(opts.PerPage).Offset(offset).Find(&cards).Error; err != nil {
		return nil, 0, fmt.Errorf("list cards of deck %d: %w", deckID, err)
	}
	return cards, total, nil
}

// cardSyncChange records which cards one syncCards call made visible or invisible.
//
// Added lists cards that did not exist as visible cards before the sync: a newly inserted row and a
// soft-deleted row restored because its template is generated again. Removed lists cards the sync
// soft-deleted. An ordinal-only alignment changes neither list, because the card stayed visible.
type cardSyncChange struct {
	Added   []uint64
	Removed []uint64
}

// syncCards aligns a note's card set with the wanted set produced by notetype.GenerateCards.
//
// Invariants (AGENTS.md §2.3 item 1, content and progress kept separate):
//   - an existing card and its id are always kept: progress hangs off the card, deleting the row loses progress with it;
//   - an existing template reuses the original row (a soft-deleted one is restored here, ordinal aligned), the id stays unchanged;
//   - only newly appearing templates are inserted;
//   - a card the note no longer generates is kept in place, never soft-deleted: editing a note adds cards and never removes
//     one. A card whose front became empty, such as the reverse card after its field is cleared or the cloze:2 card after
//     {{c2::}} is removed, stays in the table and is dealt with only by the explicit empty-card cleanup. A card that an
//     earlier version soft-deleted also stays soft-deleted until the note generates it again; the reuse branch above then
//     restores the same row, bringing its progress back.
//
// The stored template value is the card's identity inside a note, not the ordinal: a normal note type's ordinals shift when a
// template in the middle is removed, while cards.template keeps naming the same card. The (note_id, template) unique index is
// the database backstop for that rule.
//
// If a duplicate template shows up within one sync, that is an error.
func syncCards(tx *gorm.DB, noteID uint64, wanted []notetype.GeneratedCard, now time.Time) ([]Card, error) {
	cards, _, err := syncCardsWithChange(tx, noteID, wanted, now)
	return cards, err
}

// syncCardsWithChange is syncCards plus the list of cards it made visible or invisible, so a caller
// that reports the impact of a bulk card change does not have to diff the table afterwards.
func syncCardsWithChange(tx *gorm.DB, noteID uint64, wanted []notetype.GeneratedCard, now time.Time) ([]Card, cardSyncChange, error) {
	return syncCardsWithChangeKeeping(tx, noteID, wanted, nil, now)
}

// syncCardsWithChangeKeeping is syncCardsWithChange with an extra keep rule: a stored card whose
// template the wanted set omits is soft-deleted only when keep reports false for that template. A nil
// keep keeps every stored card, which is what the note write path wants: a note write creates and
// restores cards but never removes one.
//
// The note type update passes a keep rule, because it must not decide from the wanted set alone: a
// template stays declared while it produces no card for a particular note, so removing that card
// there would discard the progress of a template the user never deleted.
func syncCardsWithChangeKeeping(tx *gorm.DB, noteID uint64, wanted []notetype.GeneratedCard, keep func(template string) bool, now time.Time) ([]Card, cardSyncChange, error) {
	// soft-deleted rows must be included: the unique index does not exclude them, so only reusing by template avoids hitting the unique constraint.
	var existing []Card
	if err := tx.Unscoped().Where("note_id = ?", noteID).Find(&existing).Error; err != nil {
		return nil, cardSyncChange{}, fmt.Errorf("load cards for note %d: %w", noteID, err)
	}
	return syncCardsWithChangeKeepingExisting(tx, noteID, wanted, keep, existing, now)
}

// syncCardsWithChangeKeepingExisting is syncCardsWithChangeKeeping over an already loaded card
// set, so a note type update can read the cards of every note in bounded batches instead of one
// SELECT per note. The caller must load every row of noteID, soft-deleted rows included.
func syncCardsWithChangeKeepingExisting(tx *gorm.DB, noteID uint64, wanted []notetype.GeneratedCard, keep func(template string) bool, existing []Card, now time.Time) ([]Card, cardSyncChange, error) {
	var change cardSyncChange
	byTemplate := make(map[string]*Card, len(existing))
	for i := range existing {
		byTemplate[existing[i].Template] = &existing[i]
	}

	out := make([]Card, 0, len(wanted))
	seen := make(map[string]bool, len(wanted))
	for _, want := range wanted {
		if want.Template == "" {
			return nil, cardSyncChange{}, fmt.Errorf("note %d produced a card with an empty template", noteID)
		}
		if seen[want.Template] {
			return nil, cardSyncChange{}, fmt.Errorf("note %d produced duplicate card template %q", noteID, want.Template)
		}
		seen[want.Template] = true

		if row, ok := byTemplate[want.Template]; ok {
			// reuse the original row: write to the DB only when a soft-deleted row must be restored or the ordinal aligned, otherwise write nothing and keep the id.
			if row.DeletedAt.Valid || row.Ordinal != want.Ordinal {
				restored := row.DeletedAt.Valid
				updates := map[string]any{"ordinal": want.Ordinal, "deleted_at": nil}
				if err := tx.Unscoped().Model(&Card{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
					return nil, cardSyncChange{}, fmt.Errorf("update card %d: %w", row.ID, err)
				}
				row.Ordinal = want.Ordinal
				row.DeletedAt = gorm.DeletedAt{}
				if restored {
					change.Added = append(change.Added, row.ID)
				}
			}
			out = append(out, *row)
			continue
		}

		card := Card{NoteID: noteID, Template: want.Template, Ordinal: want.Ordinal, CreatedAt: now}
		if err := tx.Create(&card).Error; err != nil {
			return nil, cardSyncChange{}, fmt.Errorf("create card %q for note %d: %w", want.Template, noteID, err)
		}
		change.Added = append(change.Added, card.ID)
		out = append(out, card)
	}

	// wrap-up: soft-delete cards that are still alive but no longer in wanted, except the ones the keep
	// rule protects. A nil keep protects every stored card: the note write must never remove one.
	// Written per primary key, over the same set loaded above.
	var stale []uint64
	for i := range existing {
		if existing[i].DeletedAt.Valid || seen[existing[i].Template] {
			continue
		}
		if keep == nil || keep(existing[i].Template) {
			continue
		}
		stale = append(stale, existing[i].ID)
	}
	if len(stale) > 0 {
		if err := tx.Model(&Card{}).Where("id IN ?", stale).Update("deleted_at", now).Error; err != nil {
			return nil, cardSyncChange{}, fmt.Errorf("soft-delete stale cards of note %d: %w", noteID, err)
		}
		change.Removed = append(change.Removed, stale...)
	}
	return out, change, nil
}

// regenerateNoteTypeCards applies the per-note derived data that plan marks stale to the given
// notes of a note type, and reports the card impact.
//
// Removal follows the config's declaration, not its card output: a normal note type soft-deletes
// exactly the cards whose template id the new config no longer declares, and a cloze note type
// soft-deletes no card. The distinction matters because a declared template can produce no card for
// a note whose fields leave its front empty; such a card is kept, together with its progress, because
// the user has not removed the template. Creation and restoration do follow the card output: every
// card notetype.GenerateCards produces for the note is inserted when it is missing and restored when
// its row is soft-deleted.
//
// The plan decides which derived table is rebuilt. A save that changes neither the search inputs,
// the metadata field ids nor the card inputs passes a plan that marks nothing, and this function
// writes nothing. When the card set must be resynced, every note's stored cards are read in bounded
// batches up front, because the per-note sync would otherwise run one SELECT per note.
//
// A note that is soft-deleted is never passed in: its cards are already invisible, and touching them
// would count changes the caller cannot see.
//
// A removed card's card_states and reviews rows stay. ReviewsOnRemovedCards counts the review log
// rows attached to the cards this call removed, because the caller must warn the user before
// silently taking reviewed cards out of circulation.
func regenerateNoteTypeCards(ctx context.Context, tx *gorm.DB, notes []Note, typeName string, cfg notetype.Config, plan noteTypeDerivedPlan, now time.Time) (NoteTypeCardImpact, error) {
	if !plan.loadsNotes() {
		return NoteTypeCardImpact{}, nil
	}
	var cardsByNote map[uint64][]Card
	if plan.Cards {
		noteIDs := make([]uint64, len(notes))
		for i := range notes {
			noteIDs[i] = notes[i].ID
		}
		var err error
		cardsByNote, err = loadCardsForNotes(tx, noteIDs)
		if err != nil {
			return NoteTypeCardImpact{}, err
		}
	}
	keep := noteTypeCardKeepRule(cfg)
	var impact NoteTypeCardImpact
	var removedIDs []uint64
	for i := range notes {
		note := &notes[i]
		values := NoteFieldValues(note.FieldsJSON)
		// search_text is derived from the config's field set and its search flags, so the same
		// transaction that stores the new config must recompute it; otherwise a field keeps its
		// old text searchable, or stays unsearchable after it is re-included.
		if plan.SearchText {
			if text := noteSearchText(cfg, values); text != note.SearchText {
				if err := tx.Model(&Note{}).Where("id = ?", note.ID).UpdateColumn("search_text", text).Error; err != nil {
					return NoteTypeCardImpact{}, fmt.Errorf("update note %d search text: %w", note.ID, err)
				}
			}
		}
		// A field rename can turn content into metadata or vice versa, so the
		// derived authorization mapping must follow the new field names too.
		if plan.MediaNotes {
			if err := rebuildMediaNotes(ctx, tx, note.ID, note.DeckID, mediaFieldValues(cfg, values)); err != nil {
				return NoteTypeCardImpact{}, err
			}
		}
		if !plan.Cards {
			continue
		}
		wanted, err := notetype.GenerateCards(cfg, notetype.RenderData{
			Fields:   values,
			Tags:     TagsOrEmpty(note.TagsJSON),
			TypeName: typeName,
		})
		if err != nil {
			return NoteTypeCardImpact{}, fmt.Errorf("note %d: generate cards: %w", note.ID, err)
		}
		_, change, err := syncCardsWithChangeKeepingExisting(tx, note.ID, wanted, keep, cardsByNote[note.ID], now)
		if err != nil {
			return NoteTypeCardImpact{}, err
		}
		impact.CardsAdded += len(change.Added)
		impact.CardsRemoved += len(change.Removed)
		removedIDs = append(removedIDs, change.Removed...)
	}
	if err := countReviewsOnCards(tx, removedIDs, &impact.ReviewsOnRemovedCards); err != nil {
		return NoteTypeCardImpact{}, err
	}
	return impact, nil
}

// loadCardsForNotes loads every stored card, soft-deleted rows included, of the given notes and
// groups them by note id. The ids are read in bounded batches because SQLite rejects one statement
// with more bound parameters than its compiled limit, and a note type can back more notes than
// that limit.
func loadCardsForNotes(tx *gorm.DB, noteIDs []uint64) (map[uint64][]Card, error) {
	out := make(map[uint64][]Card, len(noteIDs))
	const batch = 500
	for start := 0; start < len(noteIDs); start += batch {
		end := start + batch
		if end > len(noteIDs) {
			end = len(noteIDs)
		}
		var cards []Card
		if err := tx.Unscoped().Where("note_id IN ?", noteIDs[start:end]).Find(&cards).Error; err != nil {
			return nil, fmt.Errorf("load cards of notes: %w", err)
		}
		for i := range cards {
			out[cards[i].NoteID] = append(out[cards[i].NoteID], cards[i])
		}
	}
	return out, nil
}

// noteTypeCardKeepRule returns the rule a note type update uses to protect stored cards from removal
// when the new config generates no card for the note that holds them.
//
// A normal note type declares its card templates by id, so a card is protected when its template id
// is still declared: the template may produce no card for this particular note, but the user has not
// removed it. A cloze note type declares no card template ids, because its cards are named after the
// cloze numbers found in the note's fields; every card of a cloze note is therefore protected and an
// update removes none.
func noteTypeCardKeepRule(cfg notetype.Config) func(string) bool {
	if cfg.Kind == notetype.KindCloze {
		return func(string) bool { return true }
	}
	declared := make(map[string]struct{}, len(cfg.Templates))
	for _, tmpl := range cfg.Templates {
		declared[tmpl.ID] = struct{}{}
	}
	return func(template string) bool {
		_, ok := declared[template]
		return ok
	}
}

// countReviewsOnCards sums the review rows of the given cards into *out.
//
// The ids are queried in bounded batches: SQLite rejects one statement with more bound parameters
// than its compiled limit, and a note type can back more cards than that limit.
func countReviewsOnCards(tx *gorm.DB, cardIDs []uint64, out *int) error {
	const batch = 500
	for start := 0; start < len(cardIDs); start += batch {
		end := start + batch
		if end > len(cardIDs) {
			end = len(cardIDs)
		}
		var n int64
		if err := tx.Model(&Review{}).Where("card_id IN ?", cardIDs[start:end]).Count(&n).Error; err != nil {
			return fmt.Errorf("count reviews of removed cards: %w", err)
		}
		*out += int(n)
	}
	return nil
}

// SuspendedNoteIDs returns the notes in noteIDs for which userID has suspended at least one undeleted card (suspension applies to the caller only).
func SuspendedNoteIDs(ctx context.Context, db *gorm.DB, userID uint64, noteIDs []uint64) (map[uint64]bool, error) {
	out := map[uint64]bool{}
	if len(noteIDs) == 0 {
		return out, nil
	}
	var ids []uint64
	if err := db.WithContext(ctx).Table("card_states AS cs").
		Joins("JOIN cards AS c ON c.id = cs.card_id AND c.deleted_at IS NULL").
		Where("cs.user_id = ? AND cs.suspended_at IS NOT NULL AND c.note_id IN ?", userID, noteIDs).
		Distinct("c.note_id").Pluck("c.note_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("store: list suspended notes: %w", err)
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
