package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

var (
	// ErrNoteTypeDeckMismatch means the two note types of a change do not both belong to the deck the operation
	// runs on. Moving a note into a type of another deck would move content across decks, so it is refused.
	ErrNoteTypeDeckMismatch = errors.New("note types belong to different decks")
	// ErrNoteTypeKindMismatch means the source and target note types are of different kinds. A normal and a cloze
	// type generate their cards by different rules, so a card cannot carry its progress into the other kind.
	ErrNoteTypeKindMismatch = errors.New("note types have different kinds")
	// ErrNoteTypeMappingInvalid means a field or template mapping names an id the note type does not declare.
	// The error names the offending id, so the caller can show it.
	ErrNoteTypeMappingInvalid = errors.New("note type mapping is invalid")
)

// EmptyCard is one stored card that its note no longer generates.
//
// The card id, note id and note type id are the public ids the API speaks, so a caller that lists empty cards can
// also report them without a second lookup. Template and Ordinal are what the card currently holds.
type EmptyCard struct {
	CardID           uint64
	CardPublicID     string
	NoteID           uint64
	NotePublicID     string
	NoteTypeID       uint64
	NoteTypePublicID string
	Template         string
	Ordinal          int
}

// EmptyCardCleanupOptions carries the two safety switches of an empty-card cleanup.
//
// DryRun reports the counts and writes nothing. Confirm allows the cleanup to soft-delete the cards it found;
// without it a cleanup that found at least one card returns ErrCardRemovalUnconfirmed and writes nothing.
type EmptyCardCleanupOptions struct {
	DryRun  bool
	Confirm bool
}

// EmptyCardCleanupImpact is the card effect of one empty-card cleanup.
//
// CardsRemoved counts the cards the cleanup soft-deleted. ReviewsOnRemovedCards counts the review rows attached to
// them; the rows stay, because the review log is never destroyed by a content change.
type EmptyCardCleanupImpact struct {
	CardsRemoved          int
	ReviewsOnRemovedCards int
}

// NoteTypeChangeOptions carries the mappings and the safety switches of a change-note-type operation.
//
// FieldMapping maps a source field id to a target field id. TemplateMapping maps a source template id to a target
// template id. A target field with no source mapping receives the empty string; a source template with no mapping
// has its cards soft-deleted, which requires Confirm.
type NoteTypeChangeOptions struct {
	FieldMapping       map[string]string
	TemplateMapping    map[string]string
	DryRun             bool
	ConfirmCardRemoval bool
}

// NoteTypeChangeImpact is what one change-note-type operation did.
//
// NotesMoved is the number of notes whose note type changed. CardsMoved counts live cards whose template was
// changed to the mapped target template; their card ids, and therefore their progress, stay the same.
// CardsRemoved counts live cards of unmapped source templates that the operation soft-deleted;
// ReviewsOnRemovedCards counts the review rows attached to those cards, which stay in place.
type NoteTypeChangeImpact struct {
	NotesMoved            int
	CardsMoved            int
	CardsRemoved          int
	ReviewsOnRemovedCards int
}

// ListEmptyCards returns the cards of one deck whose front renders empty under their note's note type.
//
// It is a read-only report: nothing is deleted. The emptiness rule is notetype.EmptyExistingCards, the same rule
// the review page uses, so the list and the review page's empty-card notice cannot disagree.
func (s *NoteStore) ListEmptyCards(ctx context.Context, deckID uint64) ([]EmptyCard, error) {
	return listEmptyCardsTx(ctx, s.db, deckID)
}

// CleanupEmptyCards soft-deletes the cards of one deck whose front renders empty.
//
// The list is recomputed inside the transaction that deletes it, so the count in the confirmation response is
// exactly what the confirmed request removes. A soft-delete is used rather than a hard delete: card_states and
// reviews follow the card id and are never destroyed by a content cleanup, and the row leaves the queue because
// every card query filters cards.deleted_at.
//
// A dry run and an unconfirmed cleanup both roll back, so they write nothing. The returned impact belongs to both.
func (s *NoteStore) CleanupEmptyCards(ctx context.Context, deckID uint64, opts EmptyCardCleanupOptions) (EmptyCardCleanupImpact, error) {
	var impact EmptyCardCleanupImpact
	// rollbackCleanup ends the transaction without committing; it is a sentinel, not a failure.
	rollbackCleanup := errors.New("rollback empty-card cleanup")
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		empty, err := listEmptyCardsTx(ctx, tx, deckID)
		if err != nil {
			return err
		}
		impact.CardsRemoved = len(empty)
		ids := make([]uint64, 0, len(empty))
		for _, card := range empty {
			ids = append(ids, card.CardID)
		}
		if err := countReviewsOnCards(tx, ids, &impact.ReviewsOnRemovedCards); err != nil {
			return err
		}
		if opts.DryRun {
			return rollbackCleanup
		}
		if len(ids) > 0 && !opts.Confirm {
			return rollbackCleanup
		}
		now := time.Now().UTC()
		// Soft-delete in bounded batches: SQLite rejects one statement with more bound parameters than its limit.
		const batch = 500
		for start := 0; start < len(ids); start += batch {
			end := start + batch
			if end > len(ids) {
				end = len(ids)
			}
			if err := tx.Model(&Card{}).Where("id IN ?", ids[start:end]).Update("deleted_at", now).Error; err != nil {
				return fmt.Errorf("soft-delete empty cards: %w", err)
			}
		}
		return nil
	})
	if errors.Is(err, rollbackCleanup) {
		if !opts.DryRun && impact.CardsRemoved > 0 && !opts.Confirm {
			return impact, fmt.Errorf("%w: %d card(s) would be removed", ErrCardRemovalUnconfirmed, impact.CardsRemoved)
		}
		return impact, nil
	}
	if err != nil {
		return EmptyCardCleanupImpact{}, err
	}
	return impact, nil
}

// listEmptyCardsTx is the single empty-card listing, shared by the read report and the cleanup transaction.
func listEmptyCardsTx(ctx context.Context, tx *gorm.DB, deckID uint64) ([]EmptyCard, error) {
	var notes []Note
	if err := tx.WithContext(ctx).Where("deck_id = ?", deckID).Order("id ASC").Find(&notes).Error; err != nil {
		return nil, fmt.Errorf("list notes of deck %d: %w", deckID, err)
	}
	if len(notes) == 0 {
		return nil, nil
	}
	// One query loads the live cards of the whole deck; the join is expressed as a subquery on notes so the
	// statement stays inside the bound-parameter limit of SQLite for a large deck.
	var cards []Card
	noteIDs := tx.WithContext(ctx).Model(&Note{}).Select("id").Where("deck_id = ?", deckID)
	if err := tx.WithContext(ctx).Where("note_id IN (?)", noteIDs).
		Order("ordinal ASC, id ASC").Find(&cards).Error; err != nil {
		return nil, fmt.Errorf("list cards of deck %d: %w", deckID, err)
	}
	byNote := make(map[uint64][]Card, len(notes))
	for _, card := range cards {
		byNote[card.NoteID] = append(byNote[card.NoteID], card)
	}

	typeConfigs, typeRows, err := loadNoteTypesForNotes(ctx, tx, notes)
	if err != nil {
		return nil, err
	}

	out := make([]EmptyCard, 0)
	for i := range notes {
		note := &notes[i]
		if note.NoteTypeID == nil {
			continue
		}
		cfg, ok := typeConfigs[*note.NoteTypeID]
		if !ok {
			// A note whose type row is gone cannot be evaluated; skipping it keeps the rest of the deck usable.
			continue
		}
		stored := byNote[note.ID]
		existing := make([]notetype.ExistingCard, len(stored))
		byTemplate := make(map[string]Card, len(stored))
		for j, card := range stored {
			existing[j] = notetype.ExistingCard{Template: card.Template, Ordinal: card.Ordinal}
			byTemplate[card.Template] = card
		}
		data := notetype.RenderData{
			Fields:   NoteFieldValues(note.FieldsJSON),
			Tags:     TagsOrEmpty(note.TagsJSON),
			TypeName: typeRows[*note.NoteTypeID].Name,
		}
		empty, err := notetype.EmptyExistingCards(cfg, data, existing)
		if err != nil {
			return nil, fmt.Errorf("note %d: find empty cards: %w", note.ID, err)
		}
		for _, entry := range empty {
			card, ok := byTemplate[entry.Template]
			if !ok {
				continue
			}
			out = append(out, EmptyCard{
				CardID:           card.ID,
				CardPublicID:     card.PublicID,
				NoteID:           note.ID,
				NotePublicID:     note.PublicID,
				NoteTypeID:       *note.NoteTypeID,
				NoteTypePublicID: typeRows[*note.NoteTypeID].PublicID,
				Template:         card.Template,
				Ordinal:          card.Ordinal,
			})
		}
	}
	return out, nil
}

// loadNoteTypesForNotes loads the note types the given notes reference, keyed by id, together with their rows.
func loadNoteTypesForNotes(ctx context.Context, tx *gorm.DB, notes []Note) (map[uint64]notetype.Config, map[uint64]NoteType, error) {
	ids := make([]uint64, 0, len(notes))
	seen := make(map[uint64]bool, len(notes))
	for _, note := range notes {
		if note.NoteTypeID == nil || seen[*note.NoteTypeID] {
			continue
		}
		seen[*note.NoteTypeID] = true
		ids = append(ids, *note.NoteTypeID)
	}
	configs := make(map[uint64]notetype.Config, len(ids))
	rows := make(map[uint64]NoteType, len(ids))
	if len(ids) == 0 {
		return configs, rows, nil
	}
	var types []NoteType
	if err := tx.WithContext(ctx).Where("id IN ?", ids).Find(&types).Error; err != nil {
		return nil, nil, fmt.Errorf("load note types of a deck: %w", err)
	}
	for i := range types {
		cfg, err := types[i].Config()
		if err != nil {
			return nil, nil, err
		}
		configs[types[i].ID] = cfg
		rows[types[i].ID] = types[i]
	}
	return configs, rows, nil
}

// ChangeNoteType moves every note of one note type to another note type of the same deck.
//
// Both note types must belong to the deck the operation runs on (ErrNoteTypeDeckMismatch) and must be of the same
// kind (ErrNoteTypeKindMismatch). A mapping id that the source or target config does not declare is rejected with
// ErrNoteTypeMappingInvalid.
//
// Notes keep their card rows: a live card whose source template is mapped keeps its id and its progress, and only its
// template and ordinal move to the target template. A live card whose template is unmapped is soft-deleted, which
// needs ConfirmCardRemoval; its card_states and reviews stay. Content moves by field id: a target field with no source
// mapping receives the empty string, search_text is recomputed from the target config, and the media mapping is rebuilt
// because an unmapped field can drop references. Tags, source, external_ref and deck stay unchanged.
//
// Cards are moved, not regenerated: a target template with no source card produces no card for an existing note.
//
// A dry run and an unconfirmed removal both roll back, so they write nothing; the returned impact belongs to both.
func (s *NoteStore) ChangeNoteType(ctx context.Context, deckID, sourceID, targetID uint64, opts NoteTypeChangeOptions) (NoteTypeChangeImpact, error) {
	var impact NoteTypeChangeImpact
	rollbackChange := errors.New("rollback note type change")
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		source, target, srcCfg, tgtCfg, err := loadChangeTypes(ctx, tx, deckID, sourceID, targetID)
		if err != nil {
			return err
		}
		if source.ID == target.ID {
			return fmt.Errorf("%w: source and target note type are the same", ErrNoteTypeMappingInvalid)
		}
		if srcCfg.Kind != tgtCfg.Kind {
			return fmt.Errorf("%w: %q cannot become %q", ErrNoteTypeKindMismatch, string(srcCfg.Kind), string(tgtCfg.Kind))
		}
		fieldPlan, err := buildFieldPlan(srcCfg, tgtCfg, opts.FieldMapping)
		if err != nil {
			return err
		}
		templatePlan, err := buildTemplatePlan(srcCfg, tgtCfg, opts.TemplateMapping)
		if err != nil {
			return err
		}

		var notes []Note
		if err := tx.WithContext(ctx).Where("deck_id = ? AND note_type_id = ?", deckID, source.ID).
			Order("id ASC").Find(&notes).Error; err != nil {
			return fmt.Errorf("load notes of note type %d: %w", source.ID, err)
		}
		now := time.Now().UTC()
		var removedIDs []uint64
		for i := range notes {
			note := &notes[i]
			values := remapNoteFields(tgtCfg, fieldPlan, NoteFieldValues(note.FieldsJSON))
			raw, err := json.Marshal(values)
			if err != nil {
				return fmt.Errorf("encode fields of note %d: %w", note.ID, err)
			}
			if err := tx.WithContext(ctx).Model(&Note{}).Where("id = ?", note.ID).Updates(map[string]any{
				"note_type_id": target.ID,
				"fields_json":  string(raw),
				"search_text":  noteSearchText(tgtCfg, values),
				"updated_at":   now,
			}).Error; err != nil {
				return fmt.Errorf("move note %d: %w", note.ID, err)
			}
			if err := rebuildMediaNotes(ctx, tx, note.ID, deckID, values); err != nil {
				return err
			}

			moved, stale, err := moveNoteCards(tx, note.ID, templatePlan, now)
			if err != nil {
				return err
			}
			impact.CardsMoved += moved
			impact.CardsRemoved += len(stale)
			removedIDs = append(removedIDs, stale...)
		}
		impact.NotesMoved = len(notes)
		if err := countReviewsOnCards(tx, removedIDs, &impact.ReviewsOnRemovedCards); err != nil {
			return err
		}
		if opts.DryRun {
			return rollbackChange
		}
		if impact.CardsRemoved > 0 && !opts.ConfirmCardRemoval {
			return rollbackChange
		}
		return nil
	})
	if errors.Is(err, rollbackChange) {
		if !opts.DryRun && impact.CardsRemoved > 0 && !opts.ConfirmCardRemoval {
			return impact, fmt.Errorf("%w: %d card(s) would be removed", ErrCardRemovalUnconfirmed, impact.CardsRemoved)
		}
		return impact, nil
	}
	if err != nil {
		return NoteTypeChangeImpact{}, err
	}
	return impact, nil
}

// loadChangeTypes loads both note types of a change and validates their deck and config.
func loadChangeTypes(ctx context.Context, tx *gorm.DB, deckID, sourceID, targetID uint64) (*NoteType, *NoteType, notetype.Config, notetype.Config, error) {
	var source, target NoteType
	if err := tx.WithContext(ctx).First(&source, "id = ?", sourceID).Error; err != nil {
		return nil, nil, notetype.Config{}, notetype.Config{}, fmt.Errorf("load source note type %d: %w", sourceID, err)
	}
	if err := tx.WithContext(ctx).First(&target, "id = ?", targetID).Error; err != nil {
		return nil, nil, notetype.Config{}, notetype.Config{}, fmt.Errorf("load target note type %d: %w", targetID, err)
	}
	if source.DeckID != deckID || target.DeckID != deckID {
		return nil, nil, notetype.Config{}, notetype.Config{}, fmt.Errorf("%w: deck %d", ErrNoteTypeDeckMismatch, deckID)
	}
	srcCfg, err := source.Config()
	if err != nil {
		return nil, nil, notetype.Config{}, notetype.Config{}, err
	}
	tgtCfg, err := target.Config()
	if err != nil {
		return nil, nil, notetype.Config{}, notetype.Config{}, err
	}
	return &source, &target, srcCfg, tgtCfg, nil
}

// buildFieldPlan validates a field mapping and turns it into "target field id -> source field id".
//
// Both sides are matched by field id, the key a note's fields_json uses. An unknown id or two source fields
// feeding one target field is rejected, because the second value would silently overwrite the first.
func buildFieldPlan(srcCfg, tgtCfg notetype.Config, mapping map[string]string) (map[string]string, error) {
	sourceIDs := make(map[string]bool, len(srcCfg.Fields))
	for _, field := range srcCfg.Fields {
		sourceIDs[field.ID] = true
	}
	targetIDs := make(map[string]bool, len(tgtCfg.Fields))
	for _, field := range tgtCfg.Fields {
		targetIDs[field.ID] = true
	}
	plan := make(map[string]string, len(mapping))
	usedTargets := make(map[string]bool, len(mapping))
	for sourceID, targetID := range mapping {
		if !sourceIDs[sourceID] {
			return nil, fmt.Errorf("%w: source note type has no field %q", ErrNoteTypeMappingInvalid, sourceID)
		}
		if !targetIDs[targetID] {
			return nil, fmt.Errorf("%w: target note type has no field %q", ErrNoteTypeMappingInvalid, targetID)
		}
		if usedTargets[targetID] {
			return nil, fmt.Errorf("%w: two fields map to %q", ErrNoteTypeMappingInvalid, targetID)
		}
		usedTargets[targetID] = true
		plan[targetID] = sourceID
	}
	return plan, nil
}

// templateMove is one mapped target template: its id and the ordinal the card row must carry after the move.
type templateMove struct {
	targetID string
	ordinal  int
}

// buildTemplatePlan validates a template mapping and turns it into "source template id -> target template move".
//
// A cloze note type has no template mapping: its cards are named by cloze number, not by template id, and the
// numbers stay with the field content. A non-empty mapping for a cloze type is rejected.
func buildTemplatePlan(srcCfg, tgtCfg notetype.Config, mapping map[string]string) (map[string]templateMove, error) {
	if srcCfg.Kind == notetype.KindCloze {
		if len(mapping) > 0 {
			return nil, fmt.Errorf("%w: a cloze note type has no template mapping", ErrNoteTypeMappingInvalid)
		}
		return map[string]templateMove{}, nil
	}
	sourceIDs := make(map[string]bool, len(srcCfg.Templates))
	for _, template := range srcCfg.Templates {
		sourceIDs[template.ID] = true
	}
	targetByID := make(map[string]templateMove, len(tgtCfg.Templates))
	for ordinal, template := range tgtCfg.Templates {
		targetByID[template.ID] = templateMove{targetID: template.ID, ordinal: ordinal}
	}
	plan := make(map[string]templateMove, len(mapping))
	usedTargets := make(map[string]bool, len(mapping))
	for sourceID, targetID := range mapping {
		if !sourceIDs[sourceID] {
			return nil, fmt.Errorf("%w: source note type has no template %q", ErrNoteTypeMappingInvalid, sourceID)
		}
		move, ok := targetByID[targetID]
		if !ok {
			return nil, fmt.Errorf("%w: target note type has no template %q", ErrNoteTypeMappingInvalid, targetID)
		}
		if usedTargets[targetID] {
			return nil, fmt.Errorf("%w: two templates map to %q", ErrNoteTypeMappingInvalid, targetID)
		}
		usedTargets[targetID] = true
		plan[sourceID] = move
	}
	return plan, nil
}

// remapNoteFields builds the target note's field map: a target field gets its mapped source value, everything else
// is the empty string.
func remapNoteFields(tgtCfg notetype.Config, plan map[string]string, values map[string]string) map[string]string {
	out := make(map[string]string, len(tgtCfg.Fields))
	for _, field := range tgtCfg.Fields {
		sourceID, ok := plan[field.ID]
		if !ok {
			out[field.ID] = ""
			continue
		}
		out[field.ID] = values[sourceID]
	}
	return out
}

// moveNoteCards moves one note's live cards to their mapped target templates and soft-deletes the rest.
//
// It returns the number of moved cards and the ids of the cards it soft-deleted, so the caller can count the review
// rows those cards kept. A card whose target template is already held by another row of the same note is refused:
// both databases enforce the (note_id, template) unique index over soft-deleted rows too, so the move could not be
// written.
func moveNoteCards(tx *gorm.DB, noteID uint64, plan map[string]templateMove, now time.Time) (moved int, stale []uint64, err error) {
	// Unscoped: a soft-deleted row still occupies the (note_id, template) unique key.
	var cards []Card
	if err := tx.Unscoped().Where("note_id = ?", noteID).Find(&cards).Error; err != nil {
		return 0, nil, fmt.Errorf("load cards of note %d: %w", noteID, err)
	}
	holder := make(map[string]uint64, len(cards))
	for _, card := range cards {
		holder[card.Template] = card.ID
	}
	for i := range cards {
		card := &cards[i]
		if card.DeletedAt.Valid {
			continue
		}
		move, ok := plan[card.Template]
		if !ok {
			stale = append(stale, card.ID)
			continue
		}
		if card.Template == move.targetID {
			// Identity mapping: the template id already matches, only the ordinal can have moved.
			if card.Ordinal != move.ordinal {
				if err := tx.Model(&Card{}).Where("id = ?", card.ID).Update("ordinal", move.ordinal).Error; err != nil {
					return 0, nil, fmt.Errorf("align card %d ordinal: %w", card.ID, err)
				}
			}
			moved++
			continue
		}
		if other, taken := holder[move.targetID]; taken && other != card.ID {
			return 0, nil, fmt.Errorf("%w: template %q is already stored on note %d", ErrNoteTypeMappingInvalid, move.targetID, noteID)
		}
		if err := tx.Model(&Card{}).Where("id = ?", card.ID).Updates(map[string]any{
			"template": move.targetID,
			"ordinal":  move.ordinal,
		}).Error; err != nil {
			return 0, nil, fmt.Errorf("move card %d: %w", card.ID, err)
		}
		delete(holder, card.Template)
		holder[move.targetID] = card.ID
		moved++
	}
	const batch = 500
	for start := 0; start < len(stale); start += batch {
		end := start + batch
		if end > len(stale) {
			end = len(stale)
		}
		if err := tx.Model(&Card{}).Where("id IN ?", stale[start:end]).Update("deleted_at", now).Error; err != nil {
			return 0, nil, fmt.Errorf("soft-delete unmapped cards of note %d: %w", noteID, err)
		}
	}
	return moved, stale, nil
}
