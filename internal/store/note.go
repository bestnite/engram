package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/cardtype"
)

var (
	// ErrNoteDeckRequired means the note names no owning deck.
	ErrNoteDeckRequired = errors.New("note deck is required")
	// ErrNoteKindRequired means the note names no kind.
	ErrNoteKindRequired = errors.New("note kind is required")
	// ErrNoteTagRequired means a bulk tag operation was given no tag name.
	ErrNoteTagRequired = errors.New("note tag is required")
)

// List status values: active is the default (excludes soft-deleted rows), deleted sees only soft-deleted rows,
// all sees both. Stable English constants, so bare strings do not scatter across handlers.
const (
	NoteStatusActive  = "active"
	NoteStatusDeleted = "deleted"
	NoteStatusAll     = "all"
)

// DefaultNotePageSize is the per-page row count of the card list; the design only requires pagination to work, it is not user-configurable.
const DefaultNotePageSize = 50

// MaxNotePageSize caps the rows per page: clients may send any per_page, but without a cap one request can drain the whole database
// (rate limiting does not cover the memory of a single request). The cap is enforced in the shared NormalizeNoteListOptions,
// so REST, MCP and store.List agree.
const MaxNotePageSize = 200

// MaxNotePage caps the page number: with a huge page, (page-1)*perPage overflows int into a negative offset,
// handing LIMIT/OFFSET an illegal value; pages beyond the bound are treated as "past the last page" (empty result).
const MaxNotePage = 1_000_000

// NoteListOptions holds the card list query criteria (paging, search, tag and kind filters).
type NoteListOptions struct {
	DeckID uint64
	// Page starts at 1; values below 1 are treated as 1.
	Page int
	// PerPage 0 falls back to DefaultNotePageSize.
	PerPage int
	// Query searches the note's field content (the text of every field, front/back alike), case-insensitively.
	Query string
	// Tag matches a tag in tags_json exactly (quoted boundaries, so a prefix does not match).
	Tag string
	// Kind filters by kind; an empty string means no filter.
	Kind string
	// Status takes the constants above; an empty string equals NoteStatusActive.
	Status string
}

// NormalizeNoteListOptions funnels the paging defaults and bounds into one place: Page below 1 becomes 1, above MaxNotePage
// becomes MaxNotePage, PerPage non-positive becomes DefaultNotePageSize, above MaxNotePageSize becomes MaxNotePageSize.
// All four entry points - REST handler, MCP tool, service and store.List - go through here first, so the same rule is not written
// out in several places (write it more than once and it will drift).
func NormalizeNoteListOptions(opts NoteListOptions) NoteListOptions {
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.Page > MaxNotePage {
		opts.Page = MaxNotePage
	}
	if opts.PerPage <= 0 {
		opts.PerPage = DefaultNotePageSize
	}
	if opts.PerPage > MaxNotePageSize {
		opts.PerPage = MaxNotePageSize
	}
	return opts
}

// NoteStore wraps the notes table and implements the \"generate cards from the kind\" pipeline.
//
// Separating content from progress is the most important design principle of this project: note/card describe content only,
// the user's FSRS progress hangs off the card. So updating content must never rebuild a card (delete the old, insert a new one),
// or the progress is lost with it -- see the invariant notes on syncCards.
type NoteStore struct {
	db       *gorm.DB
	registry *cardtype.Registry
}

// NewNoteStore builds the note store; kinds come from the built-in registry (cardtype.Default).
func NewNoteStore(db *gorm.DB) *NoteStore {
	return &NoteStore{db: db, registry: cardtype.Default}
}

// prepareNoteFields validates the kind's fields, serializes fields into n.FieldsJSON and returns the cards this note should produce.
// Validation completes before the write so illegal content never lands in the database; the returned error is readable English naming the field.
func (s *NoteStore) prepareNoteFields(n *Note, fields map[string]any) ([]cardtype.Card, error) {
	if n.Kind == "" {
		return nil, ErrNoteKindRequired
	}
	if err := s.registry.Validate(n.Kind, fields); err != nil {
		return nil, fmt.Errorf("validate note fields: %w", err)
	}
	wanted, err := s.registry.Cards(cardtype.Note{Kind: n.Kind, Fields: fields})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode note fields: %w", err)
	}
	n.FieldsJSON = string(raw)
	return wanted, nil
}

// actorKey is the context key used by note write validation: the acting user id of this write.
type actorKey struct{}

// WithActor marks the actor of this note write so pre-write validation can decide "is a newly introduced reference readable by me".
//
// Why actor is not an explicit parameter of the write methods: note writes are called by web / REST / MCP / package import / clone / CLI
// in many shapes, so passing actor through context keeps the write method signatures unchanged and lets the "pre-write validation" and "mapping rebuild" pair
// live in a single implementation. **Every external write entry point must call WithActor first** -- otherwise validation is silently skipped (the per-entry point
// privilege-escalation injection cases are exactly what guards this). actor 0 (unmarked) means the server itself is writing and validation is skipped.
func WithActor(ctx context.Context, userID uint64) context.Context {
	return context.WithValue(ctx, actorKey{}, userID)
}

// actorFromContext returns the actor of this write; 0 (the server writing on its own behalf) when unmarked.
func actorFromContext(ctx context.Context) uint64 {
	v, _ := ctx.Value(actorKey{}).(uint64)
	return v
}

// Save is the single entry point for note writes: pre-write validation, the write (create or update) and the mapping rebuild in one transaction.
//
// n.ID == 0 means create, otherwise update. Pre-write validation and the media_notes rebuild both converge here, so
// as long as the web editor / REST / MCP / package import / clone / CLI call it (or its InTx form), the rules do not drift.
func (s *NoteStore) Save(ctx context.Context, n *Note, fields map[string]any) ([]Card, error) {
	var cards []Card
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created, err := s.SaveInTx(ctx, tx, n, fields)
		cards = created
		return err
	})
	if err != nil {
		return nil, err
	}
	return cards, nil
}

// SaveInTx performs the note write (create or update) in the transaction given by the caller, reusing the outer transaction.
// The only difference from Save is who owns the transaction: bulk import puts several notes in one transaction and the caller decides when to commit/roll back.
func (s *NoteStore) SaveInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	if n == nil {
		return nil, errors.New("save note: note is required")
	}
	if n.ID == 0 {
		return s.createInTx(ctx, tx, n, fields)
	}
	return s.updateInTx(ctx, tx, n, fields)
}

// Create makes a new note; equivalent to Save (n.ID is 0). It is kept so "create a note" call sites read explicitly.
func (s *NoteStore) Create(ctx context.Context, n *Note, fields map[string]any) ([]Card, error) {
	return s.Save(ctx, n, fields)
}

// CreateInTx makes a new note in the transaction given by the caller (bulk import commits per batch); equivalent to SaveInTx.
func (s *NoteStore) CreateInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	return s.SaveInTx(ctx, tx, n, fields)
}

// createInTx writes one new note in a transaction: pre-write validation first (references newly introduced by this write must be readable),
// then the note and its cards, and finally the rebuild of its media_notes mapping.
// If any step fails the whole thing rolls back, leaving no half state such as "a note without cards" or "a mapping inconsistent with the fields".
func (s *NoteStore) createInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	if n.DeckID == 0 {
		return nil, ErrNoteDeckRequired
	}
	wanted, err := s.prepareNoteFields(n, fields)
	if err != nil {
		return nil, err
	}
	// Pre-write validation: evaluate against the pre-write state (on create the old reference set is empty).
	if err := checkNewMediaRefs(ctx, tx, actorFromContext(ctx), nil, fields); err != nil {
		return nil, err
	}
	if n.TagsJSON == "" {
		// String defaults are given explicitly on the Go side by the store layer (models.go package comment).
		n.TagsJSON = "[]"
	}
	now := time.Now().UTC()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now

	if err := tx.WithContext(ctx).Create(n).Error; err != nil {
		return nil, fmt.Errorf("create note: %w", err)
	}
	created, err := syncCards(tx, n.ID, wanted, now)
	if err != nil {
		return nil, err
	}
	if err := rebuildMediaNotes(ctx, tx, n.ID, fields); err != nil {
		return nil, err
	}
	return created, nil
}

// ByID loads a note that is not soft-deleted; a deleted note counts as absent (use Restore to bring it back).
func (s *NoteStore) ByID(ctx context.Context, id uint64) (*Note, error) {
	return s.byIDTx(ctx, s.db, id)
}

// byIDTx loads a note that is not soft-deleted on the given transaction/connection; UpdateInTx uses it to read the current content in the same transaction.
func (s *NoteStore) byIDTx(ctx context.Context, tx *gorm.DB, id uint64) (*Note, error) {
	var n Note
	if err := tx.WithContext(ctx).First(&n, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &n, nil
}

// Update changes a note's content and syncs its cards.
//
// The acceptance point: existing cards and their ids stay unchanged after an update. Sync only reuses old cards (matched by template,
// id unchanged) and inserts newly appearing templates, never deleting an existing row (see syncCards).
func (s *NoteStore) Update(ctx context.Context, n *Note, fields map[string]any) ([]Card, error) {
	return s.Save(ctx, n, fields)
}

// UpdateInTx updates the note and its cards in the transaction given by the caller (bulk import commits per batch).
// Same semantics as Update, only reusing the outer transaction; existing card ids and user progress stay unchanged.
func (s *NoteStore) UpdateInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	return s.SaveInTx(ctx, tx, n, fields)
}

// updateInTx updates a note in a transaction: read the old content to compute the "old reference set", run pre-write validation
// against the pre-write state (references newly introduced by this write must be readable), write the note and cards, then **rebuild** its media_notes mapping.
func (s *NoteStore) updateInTx(ctx context.Context, tx *gorm.DB, n *Note, fields map[string]any) ([]Card, error) {
	if n.ID == 0 {
		return nil, errors.New("update note: id is required")
	}
	existing, err := s.byIDTx(ctx, tx, n.ID)
	if err != nil {
		return nil, err
	}
	if n.Kind == "" {
		n.Kind = existing.Kind
	}
	wanted, err := s.prepareNoteFields(n, fields)
	if err != nil {
		return nil, err
	}
	// Pre-write validation: evaluate against the pre-write state -- the old reference set comes from the fields currently in the database, and the write only happens once it passes.
	oldFields, perr := ParseFields(existing.FieldsJSON)
	if perr != nil {
		// Corrupt fields count as an empty set: that neither waves new references through nor blocks editing because of pre-existing dirty data.
		oldFields = nil
	}
	if err := checkNewMediaRefs(ctx, tx, actorFromContext(ctx), oldFields, fields); err != nil {
		return nil, err
	}
	if n.TagsJSON == "" {
		n.TagsJSON = existing.TagsJSON
	}
	now := time.Now().UTC()
	n.UpdatedAt = now

	updates := map[string]any{
		"kind":        n.Kind,
		"fields_json": n.FieldsJSON,
		"tags_json":   n.TagsJSON,
		"updated_at":  now,
	}
	if err := tx.WithContext(ctx).Model(&Note{}).Where("id = ?", n.ID).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("update note %d: %w", n.ID, err)
	}
	synced, err := syncCards(tx, n.ID, wanted, now)
	if err != nil {
		return nil, err
	}
	if err := rebuildMediaNotes(ctx, tx, n.ID, fields); err != nil {
		return nil, err
	}
	return synced, nil
}

// Delete soft-deletes a note: it writes notes.deleted_at only and touches no cards row.
// Progress on the cards must survive, so card invisibility is implemented by filtering in the query layer (CardStore), not by cascading writes to cards.
// Returns gorm.ErrRecordNotFound when the note is already deleted or absent.
func (s *NoteStore) Delete(ctx context.Context, id uint64) error {
	res := s.db.WithContext(ctx).Where("id = ?", id).Delete(&Note{})
	if res.Error != nil {
		return fmt.Errorf("delete note %d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Restore clears notes.deleted_at; the cards rows and ids never changed, so they are visible again afterwards.
// Returns gorm.ErrRecordNotFound when the note does not exist.
func (s *NoteStore) Restore(ctx context.Context, id uint64) error {
	return s.RestoreInTx(ctx, s.db, id)
}

// RestoreInTx restores a soft-deleted note in the transaction given by the caller (bulk import commits per batch:
// when a soft-deleted external_ref is hit, restore it first, otherwise the following Update has no effect).
func (s *NoteStore) RestoreInTx(ctx context.Context, tx *gorm.DB, id uint64) error {
	res := tx.WithContext(ctx).Unscoped().Model(&Note{}).Where("id = ?", id).
		Updates(map[string]any{"deleted_at": nil})
	if res.Error != nil {
		return fmt.Errorf("restore note %d: %w", id, res.Error)
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// List pages through a deck's notes by criteria, returning the rows of this page and the total number of matches.
//
// Query conventions: paging is always LIMIT/OFFSET; fuzzy matching uses LOWER(col) LIKE,
// never PG-only ILIKE or an expression index. Search covers the text of every field in fields_json,
// so front and back both hit; tags use a LIKE with quoted boundaries to match an array element exactly.
func (s *NoteStore) List(ctx context.Context, opts NoteListOptions) ([]Note, int64, error) {
	opts = NormalizeNoteListOptions(opts)
	perPage := opts.PerPage
	page := opts.Page
	offset := (page - 1) * perPage

	// The query is rebuilt on every call, so reusing one *gorm.DB does not carry Count's statement state into Find.
	build := func() *gorm.DB {
		q := s.db.WithContext(ctx).Model(&Note{})
		switch opts.Status {
		case NoteStatusDeleted:
			q = q.Unscoped().Where("notes.deleted_at IS NOT NULL")
		case NoteStatusAll:
			q = q.Unscoped()
		default:
			// By default GORM's soft-delete scope excludes rows whose deleted_at is not null.
		}
		q = q.Where("notes.deck_id = ?", opts.DeckID)
		if v := strings.TrimSpace(opts.Kind); v != "" {
			q = q.Where("notes.kind = ?", v)
		}
		if v := strings.TrimSpace(opts.Query); v != "" {
			// LOWER and LIKE mean the same thing on both databases; wildcards are escaped so the value matches literally.
			q = q.Where("LOWER(notes.fields_json) LIKE ? ESCAPE '\\'",
				"%"+strings.ToLower(escapeLike(v))+"%")
		}
		if v := strings.TrimSpace(opts.Tag); v != "" {
			q = WhereNoteTagsAny(q, []string{v})
		}
		return q
	}

	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count notes for deck %d: %w", opts.DeckID, err)
	}
	var notes []Note
	if err := build().Order("notes.created_at DESC, notes.id DESC").
		Limit(perPage).Offset(offset).Find(&notes).Error; err != nil {
		return nil, 0, fmt.Errorf("list notes for deck %d: %w", opts.DeckID, err)
	}
	return notes, total, nil
}

// DeleteMany soft-deletes a set of notes (writes notes.deleted_at only, touches no cards row).
// Already-deleted or absent ids are not counted in the returned number; an empty ids slice returns (0, nil) right away.
// dryRun only counts "the rows that would be deleted" and performs no write at all.
func (s *NoteStore) DeleteMany(ctx context.Context, ids []uint64, dryRun bool) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if dryRun {
		var n int64
		if err := s.db.WithContext(ctx).Model(&Note{}).Where("id IN ?", ids).Count(&n).Error; err != nil {
			return 0, fmt.Errorf("count notes to delete: %w", err)
		}
		return n, nil
	}
	var affected int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id IN ?", ids).Delete(&Note{})
		if res.Error != nil {
			return fmt.Errorf("delete notes %v: %w", ids, res.Error)
		}
		affected = res.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// tagOp selects how the tag primitive merges; all three primitives converge on the single implementation applyTags.
type tagOp int

const (
	tagOpAdd tagOp = iota
	tagOpRemove
	tagOpSet
)

// AddTags appends tags to a set of notes (union), skipping rows whose result equals the old value (idempotent).
// Returns the number of rows that actually changed; only tags_json and updated_at are written, fields and cards are untouched.
func (s *NoteStore) AddTags(ctx context.Context, ids []uint64, tags []string, dryRun bool) (int64, error) {
	return s.applyTags(ctx, ids, tags, tagOpAdd, dryRun)
}

// RemoveTags drops the given tags from a set of notes (difference, preserving the original order).
// Returns the number of rows that actually changed; only tags_json and updated_at are written, fields and cards are untouched.
func (s *NoteStore) RemoveTags(ctx context.Context, ids []uint64, tags []string, dryRun bool) (int64, error) {
	return s.applyTags(ctx, ids, tags, tagOpRemove, dryRun)
}

// SetTags replaces the tags of a set of notes with the given list as a whole (preserving the list order).
// Returns the number of rows that actually changed; only tags_json and updated_at are written, fields and cards are untouched.
func (s *NoteStore) SetTags(ctx context.Context, ids []uint64, tags []string, dryRun bool) (int64, error) {
	return s.applyTags(ctx, ids, tags, tagOpSet, dryRun)
}

// applyTags is the sole implementation of the three tag primitives. Tags are normalized first (see NormalizeTags),
// then the new value is computed row by row: an unchanged value is skipped (no write, not counted in the return), only a different one writes tags_json + updated_at.
// Every write shares one transaction and a single failing row rolls it all back; under dryRun it only counts and runs no UPDATE.
func (s *NoteStore) applyTags(ctx context.Context, ids []uint64, tags []string, op tagOp, dryRun bool) (int64, error) {
	clean := NormalizeTags(tags)
	if len(clean) == 0 {
		return 0, ErrNoteTagRequired
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var notes []Note
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&notes).Error; err != nil {
		return 0, fmt.Errorf("load notes for tagging: %w", err)
	}
	type tagChange struct {
		id  uint64
		raw string
	}
	changes := make([]tagChange, 0, len(notes))
	for i := range notes {
		old, err := ParseTags(notes[i].TagsJSON)
		if err != nil {
			return 0, fmt.Errorf("note %d: %w", notes[i].ID, err)
		}
		var next []string
		switch op {
		case tagOpAdd:
			next = addTags(old, clean)
		case tagOpRemove:
			next = removeTags(old, clean)
		default:
			next = append([]string{}, clean...)
		}
		if equalTags(old, next) {
			continue
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return 0, fmt.Errorf("encode tags for note %d: %w", notes[i].ID, err)
		}
		changes = append(changes, tagChange{id: notes[i].ID, raw: string(raw)})
	}
	if dryRun {
		return int64(len(changes)), nil
	}
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, ch := range changes {
			if err := tx.Model(&Note{}).Where("id = ?", ch.id).
				Updates(map[string]any{"tags_json": ch.raw, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("update tags for note %d: %w", ch.id, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int64(len(changes)), nil
}

// NormalizeTags normalizes a tag list: trims surrounding whitespace, drops empty strings, de-duplicates by first occurrence and keeps the order.
// The store's three tag primitives and the REST/MCP bulk service share it, so "add/remove/set" and request validation judge the same
// tag identically (one rule, one place to change).
func NormalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || containsString(out, t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// addTags returns the union of old and add: keep old's order, then append unseen tags in add's order.
func addTags(old, add []string) []string {
	out := append([]string{}, old...)
	for _, t := range add {
		if !containsString(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// removeTags returns old with the tags in remove dropped, keeping old's order.
func removeTags(old, remove []string) []string {
	out := make([]string, 0, len(old))
	for _, t := range old {
		if !containsString(remove, t) {
			out = append(out, t)
		}
	}
	return out
}

// equalTags reports whether two tag lists are exactly equal (order-sensitive); nil and an empty list count as equal.
func equalTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ParseTags decodes tags_json; an empty string counts as an empty array and corrupt data returns a readable English error.
func ParseTags(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}, nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, fmt.Errorf("decode tags_json: %w", err)
	}
	return tags, nil
}

// ParseFields decodes fields_json into a field map, used by the list summary and to prefill the edit page.
func ParseFields(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("decode fields_json: %w", err)
	}
	return fields, nil
}

// TagsJSON serializes a tag slice into notes.tags_json; nil becomes "[]", so the column never holds an empty string.
// This is the only serialization entry point, shared by deck package export and REST writes (the output matches what writing it by hand at each site produced).
func TagsJSON(tags []string) string {
	if tags == nil {
		tags = []string{}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// FieldsOrEmpty decodes fields_json; corrupt data or null collapses into an empty object, so a response never carries null.
// The list, due-card and export read paths share it, so the same row yields the same shape at every exit.
func FieldsOrEmpty(raw string) map[string]any {
	fields, err := ParseFields(raw)
	if err != nil || fields == nil {
		return map[string]any{}
	}
	return fields
}

// TagsOrEmpty decodes tags_json; corrupt data or null collapses into an empty array.
func TagsOrEmpty(raw string) []string {
	tags, err := ParseTags(raw)
	if err != nil || tags == nil {
		return []string{}
	}
	return tags
}

// WhereNoteTagsAny restricts to notes carrying any of the tags (union); no condition is added when tags is empty.
// The query must already bring in the notes table under the name notes (query notes directly or JOIN notes AS notes).
//
// tags_json is TEXT (the two-database compatibility convention forbids jsonb/array) and SQL cannot parse JSON across databases, so an array element is matched
// with a LIKE on its JSON encoding that has quoted boundaries: quotes inside an element are always escaped to \", bare quotes only appear at the boundaries,
// so "a" never hits inside "ab". The pattern is generated with the same JSON encoding used to write tags_json (covering quotes,
// backslashes and the <>& escapes); hand-assembling "tag" would make tags containing those characters unmatchable forever.
// Case-insensitive (LOWER on both sides); note list filtering and study-by-tag share this one rule.
func WhereNoteTagsAny(q *gorm.DB, tags []string) *gorm.DB {
	if len(tags) == 0 {
		return q
	}
	conds := make([]string, 0, len(tags))
	args := make([]any, 0, len(tags))
	for _, tag := range tags {
		conds = append(conds, "LOWER(notes.tags_json) LIKE ? ESCAPE '\\'")
		args = append(args, tagLikePattern(tag))
	}
	// Parentheses are explicit: when the OR is combined with outer AND conditions we cannot rely on GORM wrapping it automatically.
	return q.Where("("+strings.Join(conds, " OR ")+")", args...)
}

// tagLikePattern builds the LIKE pattern matching one array element in tags_json (already lowercased, wildcards escaped).
func tagLikePattern(tag string) string {
	raw, err := json.Marshal(tag)
	if err != nil {
		// JSON encoding of a string cannot fail; the fallback assembles it literally, matching the old implementation's behaviour.
		raw = []byte(`"` + tag + `"`)
	}
	return "%" + escapeLike(strings.ToLower(string(raw))) + "%"
}

// TagCount is one tag in a deck together with the number of notes carrying it.
type TagCount struct {
	Tag   string `json:"tag"`
	Notes int    `json:"notes"`
}

// DeckTagCounts aggregates the tags of the non-deleted notes in a deck with their note counts, ordered by tag name ascending.
// Tags are decoded and aggregated in Go: tags_json is TEXT and a JSON array cannot be expanded in SQL across databases.
// Rows with corrupt data count as untagged (TagsOrEmpty), so one dirty row does not drag down the whole list.
func (s *NoteStore) DeckTagCounts(ctx context.Context, deckID uint64) ([]TagCount, error) {
	var raws []string
	if err := s.db.WithContext(ctx).Model(&Note{}).
		Where("deck_id = ?", deckID).
		Pluck("tags_json", &raws).Error; err != nil {
		return nil, fmt.Errorf("load tags for deck %d: %w", deckID, err)
	}
	counts := map[string]int{}
	for _, raw := range raws {
		// Repeated tags within one note are counted once.
		for _, tag := range NormalizeTags(TagsOrEmpty(raw)) {
			counts[tag]++
		}
	}
	out := make([]TagCount, 0, len(counts))
	for tag, n := range counts {
		out = append(out, TagCount{Tag: tag, Notes: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

// escapeLike escapes the wildcards in a LIKE pattern so user input matches literally (together with ESCAPE '\\').
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// containsString reports whether a string slice contains the wanted value.
func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
