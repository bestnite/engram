package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/notetype"
)

// Anki package (.apkg) export.
//
// The writer in internal/anki builds the archive; this file maps the deck's rows onto
// that writer's model. Two boundaries decide what a package carries:
//
//   - progress and reviews are the caller's own rows only, so exporting a deck someone
//     else shared never ships their scheduling. The deck configuration is the caller's
//     effective preset (StudySettings), for the same reason.
//   - identifiers and timestamps come from stored data only, never from the clock, so
//     two exports of one deck are byte-identical.

// ExportAnkiPackage writes one deck as a legacy-2 .apkg a schema-11 collection underneath.
//
// The caller is responsible for the deck-level permission check, as with ExportPackage.
// opts.IncludeProgress selects card states, opts.IncludeReviews the review log,
// opts.IncludeMedia media bytes and opts.IncludeDeckConfigs the deck configuration
// (nil means on). Both progress and reviews default to off.
//
// Parameters the legacy dconf cannot express are left out deliberately: daily new and
// review limits (they belong to the deck, not the preset), the FSRS short-term scheduler
// flag, fuzz and any preset field with no legacy counterpart.
func (s *DeckStore) ExportAnkiPackage(ctx context.Context, actorUserID, deckID uint64, opts PackageOptions) ([]byte, error) {
	if deckID == 0 {
		return nil, ErrPackageDeckRequired
	}
	deck, err := s.ByID(ctx, deckID)
	if err != nil {
		return nil, fmt.Errorf("export anki package: load deck: %w", err)
	}

	var types []NoteType
	types, err = NewNoteTypeStore(s.db).ListByDeck(ctx, deckID)
	if err != nil {
		return nil, fmt.Errorf("export anki package: list note types: %w", err)
	}

	var notes []Note
	if err := s.db.WithContext(ctx).Where("deck_id = ?", deckID).Order("id ASC").Find(&notes).Error; err != nil {
		return nil, fmt.Errorf("export anki package: list notes: %w", err)
	}
	noteIDs := make([]uint64, len(notes))
	for i := range notes {
		noteIDs[i] = notes[i].ID
	}
	var cards []Card
	if len(noteIDs) > 0 {
		if err := s.db.WithContext(ctx).Where("note_id IN ?", noteIDs).
			Order("note_id ASC, ordinal ASC, id ASC").Find(&cards).Error; err != nil {
			return nil, fmt.Errorf("export anki package: list cards: %w", err)
		}
	}
	cardIDs := make([]uint64, len(cards))
	for i := range cards {
		cardIDs[i] = cards[i].ID
	}

	cfg := ankiWriteConfig{
		deck:     deck,
		types:    types,
		notes:    notes,
		cards:    cards,
		modelIDs: map[uint64]int64{},
		cfgs:     map[uint64]notetype.Config{},
		noteIDs:  map[uint64]int64{},
		cardIDs:  map[uint64]int64{},
	}
	if err := cfg.assignModelIDs(); err != nil {
		return nil, err
	}
	cfg.assignNoteAndCardIDs()

	collection := anki.WriteCollection{
		NoteTypes: cfg.writeNoteTypes(),
		Decks:     cfg.writeDecks(),
		Notes:     cfg.writeNotes(),
		Cards:     cfg.writeCards(nil),
	}
	collection.CreatedAt, collection.ModifiedMS, collection.SchemaModifiedMS = cfg.times()

	if opts.DeckConfigsEnabled() {
		settings, err := s.StudySettings(ctx, actorUserID, deck)
		if err != nil {
			return nil, fmt.Errorf("export anki package: load study settings: %w", err)
		}
		preset, err := NewPresetStore(s.db).ByID(ctx, settings.PresetID)
		if err != nil {
			return nil, fmt.Errorf("export anki package: load preset: %w", err)
		}
		config, err := ankiDeckConfig(preset, opts.IncludeWeights)
		if err != nil {
			return nil, fmt.Errorf("export anki package: deck config: %w", err)
		}
		collection.DeckConfigs = []anki.WriteDeckConfig{config}
		for i := range collection.Decks {
			collection.Decks[i].ConfigID = config.ID
		}
	}

	if opts.IncludeProgress && len(cardIDs) > 0 {
		var states []CardState
		if err := s.db.WithContext(ctx).Where("user_id = ? AND card_id IN ?", actorUserID, cardIDs).
			Order("card_id ASC").Find(&states).Error; err != nil {
			return nil, fmt.Errorf("export anki package: list progress: %w", err)
		}
		stateByCard := make(map[uint64]CardState, len(states))
		for _, state := range states {
			stateByCard[state.CardID] = state
		}
		collection.Cards = cfg.writeCards(stateByCard)
	}

	if opts.IncludeReviews && len(cardIDs) > 0 {
		var reviews []Review
		if err := s.db.WithContext(ctx).Where("user_id = ? AND card_id IN ?", actorUserID, cardIDs).
			Order("id ASC").Find(&reviews).Error; err != nil {
			return nil, fmt.Errorf("export anki package: list reviews: %w", err)
		}
		collection.Reviews = cfg.writeReviews(reviews)
	}

	var media []anki.WriteMedia
	if opts.IncludeMedia {
		media, err = s.ankiMediaFiles(ctx, deckID, types, notes, opts.MediaRoot)
		if err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	if err := anki.WriteArchive(&buf, &anki.WritePackage{Collection: collection, Media: media}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ankiWriteConfig holds the rows one export maps.
type ankiWriteConfig struct {
	deck  *Deck
	types []NoteType
	notes []Note
	cards []Card

	modelIDs map[uint64]int64
	cfgs     map[uint64]notetype.Config
	noteIDs  map[uint64]int64
	cardIDs  map[uint64]int64
}

// assignModelIDs gives every note type a stable model id. A config imported from Anki
// keeps anki_origin.notetype_id, so a re-import of this deck matches the note type it came
// from. A note type without an origin uses its own row id, which is stable for the rows
// being exported and small enough for Anki's id checks.
func (c *ankiWriteConfig) assignModelIDs() error {
	used := make(map[int64]bool, len(c.types))
	for i := range c.types {
		nt := &c.types[i]
		cfg, err := nt.Config()
		if err != nil {
			return fmt.Errorf("export anki package: note type %d: %w", nt.ID, err)
		}
		c.cfgs[nt.ID] = cfg
		id := int64(nt.ID)
		if cfg.AnkiOrigin != nil && cfg.AnkiOrigin.NoteTypeID > 0 {
			id = cfg.AnkiOrigin.NoteTypeID
		}
		// Two note types may claim one origin after a structural change produced a
		// second row. The later row must still have a distinct model id.
		for used[id] {
			id++
		}
		used[id] = true
		c.modelIDs[nt.ID] = id
	}
	return nil
}

// assignNoteAndCardIDs maps engram's autoincrement ids onto small sequential Anki ids.
// Anki silently replaces ids later than about a day from now, and remaps revlog cids
// when it does, so timestamp-shaped ids are never written.
func (c *ankiWriteConfig) assignNoteAndCardIDs() {
	for i := range c.notes {
		c.noteIDs[c.notes[i].ID] = int64(i) + 1
	}
	for i := range c.cards {
		c.cardIDs[c.cards[i].ID] = int64(i) + 1
	}
}

// times derives the collection timestamps from stored rows. It never reads the clock.
func (c *ankiWriteConfig) times() (created, modifiedMS, schemaModifiedMS int64) {
	created = c.deck.CreatedAt.UTC().Unix()
	latest := c.deck.CreatedAt
	observe := func(t time.Time) {
		if !t.IsZero() && t.After(latest) {
			latest = t
		}
	}
	for i := range c.types {
		observe(c.types[i].UpdatedAt)
	}
	for i := range c.notes {
		observe(c.notes[i].UpdatedAt)
	}
	for i := range c.cards {
		observe(c.cards[i].CreatedAt)
	}
	if latest.IsZero() {
		return created, 0, 0
	}
	modifiedMS = latest.UTC().UnixMilli()
	return created, modifiedMS, modifiedMS
}

// writeNoteTypes maps every note type to the schema-11 model. A note type's answer specs
// are carried in the unknown "engram" key; without that key an exported and re-imported
// deck would lose machine grading.
func (c *ankiWriteConfig) writeNoteTypes() []anki.WriteNoteType {
	out := make([]anki.WriteNoteType, 0, len(c.types))
	for i := range c.types {
		nt := &c.types[i]
		cfg := c.cfgs[nt.ID]
		sortIndex := 0
		for j, field := range cfg.Fields {
			if field.ID == cfg.SortFieldID {
				sortIndex = j
				break
			}
		}
		fields := make([]anki.Field, len(cfg.Fields))
		for j, field := range cfg.Fields {
			fields[j] = anki.Field{
				Ord: j, Name: field.Name, ID: int64(j), Sticky: field.Sticky,
				RTL: field.RTL, Font: field.Font, FontSize: field.FontSize,
				Description: field.Description, PlainText: field.PlainText,
				Collapsed: field.Collapsed, ExcludeFromSearch: field.ExcludeFromSearch,
				Tag: field.Tag, PreventDeletion: field.PreventDeletion,
			}
		}
		templates := make([]anki.Template, len(cfg.Templates))
		for j, template := range cfg.Templates {
			templates[j] = anki.Template{
				Ord: j, Name: template.Name, ID: int64(j), QFormat: template.QFormat,
				AFormat: template.AFormat, BQFormat: template.BQFormat,
				BAFormat: template.BAFormat, BrowserFont: template.BrowserFont,
				BrowserFontSize: template.BrowserFontSize,
			}
		}
		modified := nt.UpdatedAt.UTC().Unix()
		if cfg.AnkiOrigin != nil && cfg.AnkiOrigin.MTime > 0 {
			modified = cfg.AnkiOrigin.MTime
		}
		out = append(out, anki.WriteNoteType{
			ID: c.modelIDs[nt.ID], Name: nt.Name, Kind: ankiKind(cfg.Kind),
			Modified: modified, SortFieldIndex: sortIndex, Fields: fields,
			Templates: templates, CSS: cfg.CSS, LaTeXPre: cfg.LaTeXPre,
			LaTeXPost: cfg.LaTeXPost, LaTeXSVG: cfg.LaTeXSVG,
			OriginalStockKind: ankiStockKindValue(cfg.OriginalStockKind),
			AnswerSpecs:       ankiAnswerSpecs(cfg),
		})
	}
	return out
}

// ankiAnswerSpecs converts a config's answer specs into the exported shape. Binding
// values become field names: a re-import assigns its own field ids, and names are the one
// identifier it preserves.
func ankiAnswerSpecs(cfg notetype.Config) map[string]anki.AnswerSpec {
	var out map[string]anki.AnswerSpec
	fieldNameByID := make(map[string]string, len(cfg.Fields))
	for _, field := range cfg.Fields {
		fieldNameByID[field.ID] = field.Name
	}
	for _, template := range cfg.Templates {
		if template.Answer == nil {
			continue
		}
		bindings := make(map[string]string, len(template.Answer.Bindings))
		for role, id := range template.Answer.Bindings {
			if name, ok := fieldNameByID[id]; ok {
				bindings[role] = name
			} else {
				// A config validated by SetConfig cannot reach this; leaving the role out
				// is safer than exporting a binding that names no field.
				continue
			}
		}
		if out == nil {
			out = make(map[string]anki.AnswerSpec)
		}
		out[template.Name] = anki.AnswerSpec{Control: template.Answer.Control, Bindings: bindings}
	}
	return out
}

func ankiKind(kind notetype.Kind) int {
	if kind == notetype.KindCloze {
		return anki.KindCloze
	}
	return anki.KindNormal
}

// ankiStockKindValue maps the note type model's stock-kind name back to Anki's numeric
// enum. Zero means the type was not created from a stock type.
func ankiStockKindValue(kind *notetype.OriginalStockKind) int {
	if kind == nil {
		return 0
	}
	switch *kind {
	case notetype.StockBasic:
		return 1
	case notetype.StockBasicAndReversed:
		return 2
	case notetype.StockBasicOptionalReversed:
		return 3
	case notetype.StockBasicTyping:
		return 4
	case notetype.StockCloze:
		return 5
	case notetype.StockImageOcclusion:
		return 6
	default:
		return 0
	}
}

// writeDecks maps the flat deck and every distinct card subdeck path onto Anki decks.
// The flat deck is Deck, a card whose deck_path is "Lesson 1::Verbs" becomes
// Deck::Lesson 1::Verbs, so an import that strips the shared prefix gets the path back.
func (c *ankiWriteConfig) writeDecks() []anki.WriteDeck {
	id := int64(1)
	out := []anki.WriteDeck{{
		ID: id, Name: c.deck.Name, Description: c.deck.Description,
		Modified: c.deck.CreatedAt.UTC().Unix(),
	}}
	paths := make(map[string]bool)
	for i := range c.cards {
		if path := ankiDeckPath(c.cards[i].DeckPath); path != "" {
			paths[path] = true
		}
	}
	sorted := make([]string, 0, len(paths))
	for path := range paths {
		sorted = append(sorted, path)
	}
	sort.Strings(sorted)
	for _, path := range sorted {
		id++
		out = append(out, anki.WriteDeck{
			ID: id, Name: c.deck.Name + "::" + path, Modified: c.deck.CreatedAt.UTC().Unix(),
		})
	}
	return out
}

// ankiDeckPath normalises a stored card path and drops the parts Anki would not accept as
// a deck name component.
func ankiDeckPath(path *string) string {
	if path == nil {
		return ""
	}
	parts := strings.Split(*path, "::")
	clean := parts[:0]
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, "::")
}

// deckIDByPath indexes the written decks so a card resolves its deck.
func (c *ankiWriteConfig) deckIDByPath() map[string]int64 {
	out := map[string]int64{"": 1}
	for _, deck := range c.writeDecks() {
		if deck.ID == 1 {
			continue
		}
		out[strings.TrimPrefix(deck.Name, c.deck.Name+"::")] = deck.ID
	}
	return out
}

// writeNotes maps every note to a note row. Field values are emitted in the note type's
// field order and joined with the ASCII unit separator, so the count always matches the
// declared field count. sfld and csum are approximations; Anki recomputes both on import.
func (c *ankiWriteConfig) writeNotes() []anki.WriteNote {
	typeCfg := c.typeConfigs()
	out := make([]anki.WriteNote, 0, len(c.notes))
	for i := range c.notes {
		n := &c.notes[i]
		var modelID int64
		var cfg notetype.Config
		if n.NoteTypeID != nil {
			modelID = c.modelIDs[*n.NoteTypeID]
			cfg = typeCfg[*n.NoteTypeID]
		}
		values, _ := ParseFieldValues(n.FieldsJSON)
		fields := make([]string, len(cfg.Fields))
		sortText := ""
		for j, field := range cfg.Fields {
			fields[j] = values[field.ID]
			if field.ID == cfg.SortFieldID {
				sortText = notetype.PlainText(values[field.ID])
			}
		}
		tags, _ := ParseTags(n.TagsJSON)
		out = append(out, anki.WriteNote{
			ID: c.noteIDs[n.ID], GUID: ankiGUID(n), NoteTypeID: modelID,
			Modified: n.UpdatedAt.UTC().Unix(), Tags: tags, Fields: fields,
			SortField: sortText,
		})
	}
	return out
}

// typeConfigs returns the note type configs decoded while model ids were assigned.
func (c *ankiWriteConfig) typeConfigs() map[uint64]notetype.Config {
	return c.cfgs
}

// ankiGUID uses the guid Anki gave the note when the note came from an Anki import, and
// otherwise derives a stable guid from the note's public id.
func ankiGUID(n *Note) string {
	if n.ExternalRef != nil {
		if guid, ok := strings.CutPrefix(*n.ExternalRef, "anki:"); ok && guid != "" {
			return guid
		}
	}
	return "engram-" + n.PublicID
}

// writeCards maps every card to a card row. Without progress a card is new: type and
// queue zero, due is its new-card position, and every scheduling column is zero. With a
// state the row carries the caller's real scheduling state.
func (c *ankiWriteConfig) writeCards(states map[uint64]CardState) []anki.WriteCard {
	deckIDs := c.deckIDByPath()
	out := make([]anki.WriteCard, 0, len(c.cards))
	for i := range c.cards {
		card := &c.cards[i]
		deckID := deckIDs[ankiDeckPath(card.DeckPath)]
		modified := card.CreatedAt.UTC().Unix()
		row := anki.WriteCard{
			ID: c.cardIDs[card.ID], NoteID: c.noteIDs[card.NoteID], DeckID: deckID,
			Ordinal: card.Ordinal, Modified: modified, Due: int64(i) + 1,
		}
		if state, ok := states[card.ID]; ok {
			row = ankiCardWithState(row, state, c.deck.CreatedAt.UTC().Unix())
		}
		out = append(out, row)
	}
	return out
}

// ankiCardWithState overlays one card_states row onto the new-card defaults.
//
// Anki measures a review due date in days from collection creation and a learning due
// date in seconds; the two columns share one field, so the type decides the meaning.
func ankiCardWithState(row anki.WriteCard, state CardState, createdAt int64) anki.WriteCard {
	row.Stability, row.Difficulty = state.Stability, state.Difficulty
	switch state.State {
	case "new":
		row.Type, row.Queue = 0, 0
	case "learning":
		row.Type, row.Queue = 1, 1
		row.Due = ankiLearningDue(state.DueAt, row.Due)
		row.Left = state.StepIndex
	case "review":
		row.Type, row.Queue = 2, 2
		row.Due = ankiReviewDue(state.DueAt, createdAt)
		row.Ivl = state.ScheduledDays
		row.Factor = 2500
	case "relearning":
		row.Type, row.Queue = 3, 1
		row.Due = ankiLearningDue(state.DueAt, row.Due)
		row.Ivl = state.ScheduledDays
		row.Factor = 2500
		row.Left = state.StepIndex
	}
	row.Reps = state.Reps
	row.Lapses = state.Lapses
	if state.SuspendedAt != nil {
		row.Queue = -1
	}
	if state.LastReviewAt != nil {
		row.Modified = state.LastReviewAt.UTC().Unix()
		row.LastReview = &row.Modified
	}
	return row
}

func ankiLearningDue(due *time.Time, fallback int64) int64 {
	if due == nil {
		return fallback
	}
	return due.UTC().Unix()
}

func ankiReviewDue(due *time.Time, createdAt int64) int64 {
	if due == nil {
		return 0
	}
	days := int64(math.Floor(float64(due.UTC().Unix()-createdAt) / 86400))
	if days < 0 {
		return 0
	}
	return days
}

// writeReviews maps the review log to revlog rows. Only rows whose card is in the same
// archive are written: Anki silently drops a revlog row whose cid does not resolve.
func (c *ankiWriteConfig) writeReviews(reviews []Review) []anki.WriteReview {
	out := make([]anki.WriteReview, 0, len(reviews))
	previousInterval := make(map[uint64]int, len(reviews))
	usedIDs := make(map[int64]bool, len(reviews))
	for i := range reviews {
		rv := &reviews[i]
		cardID, ok := c.cardIDs[rv.CardID]
		if !ok {
			continue
		}
		id := rv.ReviewedAt.UTC().UnixMilli()
		for usedIDs[id] {
			id++
		}
		usedIDs[id] = true
		interval := 0
		if rv.IntervalDays != nil {
			interval = int(math.Round(*rv.IntervalDays))
		}
		elapsed := 0
		if rv.ElapsedMS != nil {
			elapsed = *rv.ElapsedMS
		}
		out = append(out, anki.WriteReview{
			ID: id, CardID: cardID, Ease: rv.Rating, Ivl: interval,
			LastIvl: previousInterval[rv.CardID], Factor: 2500, Time: elapsed,
			Type: ankiReviewType(rv.StateBefore),
		})
		previousInterval[rv.CardID] = interval
	}
	return out
}

// ankiReviewType maps engram's state_before onto Anki's revlog type values: learn,
// review, relearn. A brand-new card was in the learn phase before its first rating.
func ankiReviewType(stateBefore int) int {
	switch stateBefore {
	case 2:
		return 1
	case 3:
		return 2
	default:
		return 0
	}
}

// ankiDeckConfig builds the one legacy dconf entry from the caller's effective preset.
//
// The ID must not be 1: Anki merges a config with id 1 into the target collection's stock
// "Default" and loses its values. Preset ids start at 1, so the exported id is the preset
// id plus one. fsrsWeights and fsrsParams5 are left empty because Anki reads the former as
// the 17-value fsrs_params_4; the 21 FSRS-6 parameters go in fsrsParams6.
func ankiDeckConfig(preset *Preset, includeWeights bool) (anki.WriteDeckConfig, error) {
	id := int64(preset.ID) + 1
	if id <= 1 {
		id = 2
	}
	out := anki.WriteDeckConfig{
		ID:                  id,
		Name:                preset.Name,
		DesiredRetention:    preset.DesiredRetention,
		LearningSteps:       mustAnkiDelays(preset.LearningSteps),
		RelearningSteps:     mustAnkiDelays(preset.RelearningSteps),
		MaximumIntervalDays: preset.MaximumIntervalDays,
	}
	if !includeWeights || preset.WeightsJSON == nil {
		return out, nil
	}
	weights, err := ankiWeights(*preset.WeightsJSON)
	if err != nil {
		return anki.WriteDeckConfig{}, err
	}
	out.FSRSWeights = weights
	return out, nil
}

// ankiWeights accepts exactly the values accepted by the scheduler. An empty string or
// null, after trimming, writes no weights; the caller also omits weights for NULL.
// Invalid weights fail export rather than hiding a preset the scheduler cannot use.
func ankiWeights(raw string) ([]float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	migrated, err := PresetWeights(&raw)
	if err != nil {
		return nil, fmt.Errorf("export weights: %w", err)
	}
	out := make([]float64, len(migrated))
	copy(out, migrated[:])
	return out, nil
}

// mustAnkiDelays converts a preset step string ("1m,10m") into the minutes legacy dconf
// stores. An unparsable step is dropped; the preset write path validates the same syntax,
// so this only guards against a row written before that validation existed.
func mustAnkiDelays(spec string) []float64 {
	steps := []float64{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if value, ok := ankiStepMinutes(part); ok {
			steps = append(steps, value)
		}
	}
	return steps
}

// ankiStepMinutes parses one step the way the scheduler does: a missing unit means
// minutes, and s, m, h and d are accepted. An empty string means no steps.
func ankiStepMinutes(part string) (float64, bool) {
	idx := len(part)
	for idx > 0 {
		c := part[idx-1]
		if (c >= '0' && c <= '9') || c == '.' {
			break
		}
		idx--
	}
	number, unit := part[:idx], strings.ToLower(part[idx:])
	value, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, false
	}
	switch unit {
	case "", "m", "min":
		return value, true
	case "s", "sec":
		return value / 60, true
	case "h", "hr":
		return value * 60, true
	case "d":
		return value * 1440, true
	default:
		return 0, false
	}
}

// ankiMediaFiles collects the media a package carries: every file name the notes and note
// types reference, sorted by name, with its bytes read from the content-addressed store.
// A reference that does not resolve is a missing file, not an empty one, and fails the
// export so the package never silently drops an asset.
func (s *DeckStore) ankiMediaFiles(ctx context.Context, deckID uint64, types []NoteType, notes []Note, mediaRoot string) ([]anki.WriteMedia, error) {
	if mediaRoot == "" {
		return nil, fmt.Errorf("%w: media root is empty", ErrPackageMediaMissing)
	}
	var values []string
	for i := range notes {
		fields, err := ParseFieldValues(notes[i].FieldsJSON)
		if err != nil {
			return nil, fmt.Errorf("export anki package: note %d: %w", notes[i].ID, err)
		}
		for _, value := range fields {
			values = append(values, value)
		}
	}
	for i := range types {
		cfg, err := types[i].Config()
		if err != nil {
			return nil, fmt.Errorf("export anki package: note type %d: %w", types[i].ID, err)
		}
		values = append(values, noteTypeMediaStrings(cfg)...)
	}

	nameSet := make(map[string]bool)
	for _, value := range values {
		for _, name := range mediaNamesInHTML(value) {
			nameSet[name] = true
		}
	}
	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	sort.Strings(names)

	nameStore := NewMediaNameStore(s.db)
	mediaStore := NewMediaStore(s.db)
	out := make([]anki.WriteMedia, 0, len(names))
	for _, name := range names {
		sha := ""
		if match := mediaShaNameRE.FindStringSubmatch(name); match != nil {
			sha = match[1]
		} else {
			row, err := nameStore.ByName(ctx, deckID, name)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// A reference the deck has no media row for is not a file this deck
				// carries; a stale reference does not block the export.
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("export anki package: resolve media name %q: %w", name, err)
			}
			sha = row.Sha256
		}
		meta, err := mediaStore.BySha256(ctx, sha)
		if err != nil {
			return nil, fmt.Errorf("export anki package: media metadata %s: %w", sha, err)
		}
		if meta == nil {
			return nil, fmt.Errorf("%w: sha256=%s", ErrPackageMediaMissing, sha)
		}
		raw, err := os.ReadFile(filepath.Join(mediaRoot, filepath.FromSlash(meta.RelPath)))
		if err != nil {
			return nil, fmt.Errorf("%w: sha256=%s: %v", ErrPackageMediaMissing, sha, err)
		}
		out = append(out, anki.WriteMedia{Name: name, Bytes: raw})
	}
	return out, nil
}
