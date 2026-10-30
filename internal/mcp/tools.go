package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// This file defines the input types and handlers of all MCP tools; each handler only shapes arguments,
// then calls exactly the same internal/api service method as the REST endpoint of the same name.
//
// Every input argument that points at an auto-increment primary key is a public id (a UUIDv7 string): the handler first resolves it
// into the numeric primary key via api's ByPublicID, then enters the service layer; numeric primary keys are neither accepted nor returned.

// ---- input arguments ----

// listDecksIn takes no arguments.
type listDecksIn struct{}

// createDeckIn is the input of create_deck; when preset_id is empty the caller's Default preset is used (or created).
type createDeckIn struct {
	Name        string `json:"name" jsonschema:"the deck name (required)"`
	Description string `json:"description,omitempty" jsonschema:"optional deck description"`
	PresetID    string `json:"preset_id,omitempty" jsonschema:"scheduling preset public id; empty uses the caller's Default preset"`
}

// updateDeckIn is the input of update_deck; both fields may be omitted, and omitting one keeps the original value (PATCH semantics).
type updateDeckIn struct {
	DeckID      string  `json:"deck_id" jsonschema:"the public id of the deck to update"`
	Name        *string `json:"name,omitempty" jsonschema:"new deck name; omit to keep the current name"`
	Description *string `json:"description,omitempty" jsonschema:"new deck description; omit to keep it, an empty string clears it"`
}

// deleteDeckIn is the input of delete_deck.
type deleteDeckIn struct {
	DeckID string `json:"deck_id" jsonschema:"the public id of the deck to delete"`
}

// bulkDecksIn is the input of bulk_decks and has the same shape as the REST POST /decks/bulk request body
// (MCP only shapes arguments; semantics and validation live in the service layer).
type bulkDecksIn struct {
	Action  string   `json:"action" jsonschema:"bulk action: delete"`
	DeckIDs []string `json:"deck_ids" jsonschema:"public ids of the decks to act on, deduplicated to 1..100 entries"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"count without writing or auditing"`
}

// searchNotesIn is the input of search_notes; deck_id is required.
type searchNotesIn struct {
	DeckID  string `json:"deck_id" jsonschema:"the public id of the deck to search in"`
	Query   string `json:"q,omitempty" jsonschema:"keywords matched case-insensitively against the text of the note fields; every whitespace-separated term must occur"`
	Tag     string `json:"tag,omitempty" jsonschema:"exact tag filter"`
	Kind    string `json:"kind,omitempty" jsonschema:"card type filter (e.g. basic, cloze)"`
	Status  string `json:"status,omitempty" jsonschema:"note status filter"`
	Page    int    `json:"page,omitempty" jsonschema:"page number, 1-based"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"items per page"`
}

// getNoteIn is the input of get_note.
type getNoteIn struct {
	NoteID string `json:"note_id" jsonschema:"the public id of the note to read"`
}

// getStatsIn takes no arguments.
type getStatsIn struct{}

// listNoteTypesIn is the input of list_note_types: the deck whose note types to list.
type listNoteTypesIn struct {
	DeckID string `json:"deck_id" jsonschema:"the public id of the deck whose note types to list"`
}

// getNoteTypeIn is the input of get_note_type.
type getNoteTypeIn struct {
	DeckID     string `json:"deck_id" jsonschema:"the public id of the deck the note type belongs to"`
	NoteTypeID string `json:"note_type_id" jsonschema:"the public id of the note type to read"`
}

// exportDeckIn is the input of export_deck; it exports a **deck package**.
type exportDeckIn struct {
	DeckID          string `json:"deck_id" jsonschema:"the public id of the deck to export as a package"`
	IncludeProgress bool   `json:"include_progress,omitempty" jsonschema:"include the caller's own review progress"`
	// IncludeMedia defaults to true (media is inlined by default).
	IncludeMedia   *bool `json:"include_media,omitempty" jsonschema:"inline media bytes; default true"`
	IncludeReviews bool  `json:"include_reviews,omitempty" jsonschema:"include review logs (requires include_progress)"`
	// IncludeWeights defaults to false: FSRS weights are the exporter's personal memory curve and are not carried along with deck content by default.
	IncludeWeights bool `json:"include_weights,omitempty" jsonschema:"include the caller's FSRS weights in preset.json; default false"`
}

// importNoteIn is the input for a single note; field names match schema/note-import.schema.json.
type importNoteIn struct {
	NoteType string `json:"note_type,omitempty" jsonschema:"public id of the note type; list_note_types returns the deck's note types and get_note_type their field names"`
	// Kind is the transitional alias for note_type: the current SPA still sends a card kind, and the server
	// resolves it to the deck's stock note type for that kind.
	Kind        string         `json:"kind,omitempty" jsonschema:"transitional alias for note_type: a stock card kind such as basic or cloze"`
	Fields      map[string]any `json:"fields" jsonschema:"field values keyed by field name; every value is an HTML string (the server maps names to field ids)"`
	ExternalRef string         `json:"external_ref,omitempty" jsonschema:"caller-defined idempotency key, unique per deck"`
	// NoteID addresses an existing note by public id and rewrites it in place; mutually exclusive with external_ref.
	NoteID string   `json:"note_id,omitempty" jsonschema:"address an existing note by public id to rewrite in place; mutually exclusive with external_ref"`
	Tags   []string `json:"tags,omitempty" jsonschema:"note tags"`
}

// bulkNotesIn is the input of create_notes (the bulk card creation path).
type bulkNotesIn struct {
	DeckID     string         `json:"deck_id" jsonschema:"public id of the target deck"`
	Notes      []importNoteIn `json:"notes" jsonschema:"notes to create or update (1..500)"`
	DryRun     bool           `json:"dry_run,omitempty" jsonschema:"validate and count without writing"`
	OnConflict string         `json:"on_conflict,omitempty" jsonschema:"conflict policy: skip, update (default) or fail"`
}

// bulkActionIn is the input of bulk_notes and has the same shape as the REST POST /notes/bulk request body
// (MCP only shapes arguments; semantics and validation live in the service layer).
type bulkActionIn struct {
	Action  string   `json:"action" jsonschema:"bulk action: delete, add_tags, remove_tags or set_tags"`
	NoteIDs []string `json:"note_ids" jsonschema:"public ids of the notes to act on, deduplicated to 1..500 entries"`
	Tags    []string `json:"tags,omitempty" jsonschema:"tags for the tag actions, 1..20 entries; not allowed for delete"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"count without writing or auditing"`
}

// importDeckIn is the input of import_deck: it accepts a deck package, with the source being one of two alternatives.
type importDeckIn struct {
	// Package is the package body itself: the JSON document export_deck outputs, a base64-encoded .edeck zip, or a base64-encoded .apkg archive. Mutually exclusive with URL.
	Package any `json:"package,omitempty" jsonschema:"the deck package: export_deck's JSON document, or a base64-encoded .edeck/.apkg archive; mutually exclusive with url"`
	// URL is a public HTTPS direct link (.edeck/.zip/.apkg); it is downloaded and validated by the server, and is mutually exclusive with Package.
	URL string `json:"url,omitempty" jsonschema:"public HTTPS direct link to a .edeck/.zip/.apkg package; mutually exclusive with package"`
	// Target takes new_deck (default), into_deck:<public id>, or replace_deck:<public id>.
	Target     string `json:"target,omitempty" jsonschema:"import target: new_deck (default), into_deck:<public id> or replace_deck:<public id>"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"validate and count without writing"`
	OnConflict string `json:"on_conflict,omitempty" jsonschema:"conflict policy: skip, update (default) or fail"`
	// SkipMissingMedia only counts missing media and continues; default false (missing media means failure).
	SkipMissingMedia bool `json:"skip_missing_media,omitempty" jsonschema:"skip missing media instead of failing"`
	// AllowOthersProgress can only be set by an administrator: allows importing other people's progress inside the package.
	AllowOthersProgress bool `json:"allow_others_progress,omitempty" jsonschema:"admin only: import progress that belongs to another user"`
	// ApplyWeights defaults to false: only under the new_deck target are the package's weights written into the newly created preset.
	ApplyWeights bool `json:"apply_weights,omitempty" jsonschema:"use the package's FSRS weights for the new deck's preset (new_deck only); default false"`
}

// createImportUploadIn is the input of create_import_upload: the import options are fixed into the ticket when it is issued.
type createImportUploadIn struct {
	// Target takes new_deck (default), into_deck:<public id>, or replace_deck:<public id>.
	Target     string `json:"target,omitempty" jsonschema:"import target: new_deck (default), into_deck:<public id> or replace_deck:<public id>"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"validate and count without writing"`
	OnConflict string `json:"on_conflict,omitempty" jsonschema:"conflict policy: skip, update (default) or fail"`
	// SkipMissingMedia only counts missing media and continues; default false (missing media means failure).
	SkipMissingMedia bool `json:"skip_missing_media,omitempty" jsonschema:"skip missing media instead of failing"`
	// AllowOthersProgress can only be set by an administrator: allows importing other people's progress inside the package.
	AllowOthersProgress bool `json:"allow_others_progress,omitempty" jsonschema:"admin only: import progress that belongs to another user"`
	// ApplyWeights defaults to false: only under the new_deck target are the package's weights written into the newly created preset.
	ApplyWeights bool `json:"apply_weights,omitempty" jsonschema:"use the package's FSRS weights for the new deck's preset (new_deck only); default false"`
}

// updateNoteIn is the input of update_note.
type updateNoteIn struct {
	NoteID string `json:"note_id" jsonschema:"the public id of the note to update"`
	// NoteType is the note type public id; Kind is the transitional alias and defaults to the existing type.
	NoteType string         `json:"note_type,omitempty" jsonschema:"public id of the note type; defaults to the note's existing type"`
	Kind     string         `json:"kind,omitempty" jsonschema:"transitional alias for note_type: a stock card kind such as basic or cloze"`
	Fields   map[string]any `json:"fields" jsonschema:"new field values keyed by field name; every value is an HTML string"`
	Tags     []string       `json:"tags,omitempty" jsonschema:"replacement tags"`
}

// deleteNoteIn is the input of delete_note.
type deleteNoteIn struct {
	NoteID string `json:"note_id" jsonschema:"the public id of the note to soft-delete"`
}

// getDueCardsIn is the input of get_due_cards.
// deck_id and deck_ids are mutually exclusive: giving both returns an argument error; omitting both means all decks.
type getDueCardsIn struct {
	DeckID  string   `json:"deck_id,omitempty" jsonschema:"public id of a single deck to scope the queue; empty means all decks; mutually exclusive with deck_ids"`
	DeckIDs []string `json:"deck_ids,omitempty" jsonschema:"public ids of the decks to scope the queue; absent or empty means all decks; mutually exclusive with deck_id"`
	Tags    []string `json:"tags,omitempty" jsonschema:"only cards whose note carries any of these tags; requires exactly one deck (deck_id, or deck_ids with one entry)"`
	Limit   int      `json:"limit,omitempty" jsonschema:"maximum cards to return (1..500)"`
}

// listDeckTagsIn is the input of list_deck_tags.
type listDeckTagsIn struct {
	DeckID string `json:"deck_id" jsonschema:"public id of the deck whose tags to list"`
}

// submitReviewIn is the input of submit_review. The rules match REST (see api.SubmitReviewInput):
// self-graded card types give rating, answer-based card types give answer or give_up; grade_source accepts only self.
type submitReviewIn struct {
	CardID          string `json:"card_id" jsonschema:"the public id of the card being reviewed"`
	Rating          int    `json:"rating,omitempty" jsonschema:"self-assessed rating 1=again, 2=hard, 3=good, 4=easy; omit for card types graded by the server (typed, numeric, choice_single, choice_multi, true_false)"`
	Answer          any    `json:"answer,omitempty" jsonschema:"the answer for a card type graded by the server: a string for typed, a string or number for numeric, an option index for choice_single, an array of option indices for choice_multi, a boolean for true_false, an array of strings for cloze (one per blank of the card's cloze number, in order of appearance), an array of strings for list (the recalled items; in item order when the note is ordered); for short_answer an optional string the learner wrote, sent together with the self rating and stored with the review"`
	GiveUp          bool   `json:"give_up,omitempty" jsonschema:"give up on a card type graded by the server; records Again without grading"`
	ExpectedVersion int    `json:"expected_version,omitempty" jsonschema:"card state version the caller read"`
	ElapsedMS       *int   `json:"elapsed_ms,omitempty" jsonschema:"time spent on the card in milliseconds"`
	GradeSource     string `json:"grade_source,omitempty" jsonschema:"must be omitted or self; the server decides where a grade came from"`
}

// addExtraNewCardsIn is the input of add_extra_new_cards; when count is omitted or 0 the daily new-card limit is used.
type addExtraNewCardsIn struct {
	DeckID string `json:"deck_id" jsonschema:"public id of the deck to extend today"`
	Count  int    `json:"count,omitempty" jsonschema:"number of extra new cards to allow today (1..1000); omit to use the caller's daily new card limit for the deck"`
}

// buryCardIn is the input of bury_card.
type buryCardIn struct {
	CardID string `json:"card_id" jsonschema:"public id of the card to postpone to the next review day"`
}

// setDeckPausedIn is the input of set_deck_paused.
type setDeckPausedIn struct {
	DeckID string `json:"deck_id" jsonschema:"public id of the deck to pause or resume"`
	Paused bool   `json:"paused" jsonschema:"true pauses the deck for the caller, false resumes it"`
}

// ---- handlers ----

func (s *Server) listDecks(ctx context.Context, id Identity, _ listDecksIn) (any, error) {
	decks, err := s.api.ListDecks(ctx, id.User.ID)
	if err != nil {
		return nil, err
	}
	out := make([]api.DeckResponse, 0, len(decks))
	for _, d := range decks {
		out = append(out, s.api.ToDeckResponse(ctx, id.User.ID, d.Deck, d.Role))
	}
	return map[string]any{"decks": out}, nil
}

// createDeck creates an empty deck: it goes through the same service method as REST `POST /decks`.
// It returns the same api.DeckResponse as REST, so the response shape of the two transports cannot drift.
func (s *Server) createDeck(ctx context.Context, id Identity, in createDeckIn) (any, error) {
	// preset_id is a public id: an empty string is left to the service to resolve the default preset; a non-empty one is resolved to a primary key first and then passed as a number.
	var presetID uint64
	if pid := strings.TrimSpace(in.PresetID); pid != "" {
		p, err := s.api.PresetByPublicID(ctx, pid)
		if err != nil {
			return nil, err
		}
		presetID = p.ID
	}
	d, err := s.api.CreateDeck(ctx, id.User, api.CreateDeckInput{
		Name:        in.Name,
		Description: in.Description,
		PresetID:    presetID,
		APIKeyID:    id.apiKeyID(),
	})
	if err != nil {
		return nil, err
	}
	return s.api.ToDeckResponse(ctx, id.User.ID, *d, store.RoleOwner), nil
}

// updateDeck changes a deck's name and description; it goes through the same service method as REST `PATCH /decks/:id` (owner only).
func (s *Server) updateDeck(ctx context.Context, id Identity, in updateDeckIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	updated, err := s.api.UpdateDeck(ctx, id.User, d.ID, api.UpdateDeckInput{
		Name:        in.Name,
		Description: in.Description,
		APIKeyID:    id.apiKeyID(),
	})
	if err != nil {
		return nil, err
	}
	return s.api.ToDeckResponse(ctx, id.User.ID, *updated, store.RoleOwner), nil
}

// deleteDeck removes a deck and all of its content; it goes through the same service method as REST `DELETE /decks/:id` (owner only),
// and returns the same body so the two transports cannot drift. The call is irreversible: the deck rows are hard-deleted.
func (s *Server) deleteDeck(ctx context.Context, id Identity, in deleteDeckIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	if err := s.api.DeleteDeck(ctx, id.User, d.ID, id.apiKeyID()); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// bulkDecks performs a bulk action on a set of decks: it goes through the same service method as REST `POST /decks/bulk`.
// deck_ids are decks' public ids; resolution, the per-row owner check and auditing happen in the service layer,
// so the two transports cannot drift. Deletion is irreversible.
func (s *Server) bulkDecks(ctx context.Context, id Identity, in bulkDecksIn) (any, error) {
	return s.api.BulkDecks(ctx, id.User.ID, id.apiKeyID(), api.BulkDecksInput{
		Action:  in.Action,
		DeckIDs: in.DeckIDs,
		DryRun:  in.DryRun,
	})
}

func (s *Server) searchNotes(ctx context.Context, id Identity, in searchNotesIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	opts := store.NormalizeNoteListOptions(store.NoteListOptions{
		Page:    in.Page,
		PerPage: in.PerPage,
		Query:   in.Query,
		Tag:     in.Tag,
		Kind:    in.Kind,
		Status:  in.Status,
	})
	notes, total, err := s.api.ListNotes(ctx, id.User.ID, d.ID, opts)
	if err != nil {
		return nil, err
	}
	out := s.api.NotesJSON(ctx, id.User.ID, notes)
	return map[string]any{"notes": out, "total": total, "page": opts.Page, "per_page": opts.PerPage}, nil
}

// listDeckTags lists the tags in a deck with their note counts; it goes through the same service method as REST `GET /decks/:id/tags`.
func (s *Server) listDeckTags(ctx context.Context, id Identity, in listDeckTagsIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	tags, err := s.api.DeckTags(ctx, id.User.ID, d.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tags": tags}, nil
}

// getNote reads a single note by public id; it goes through the same service method as REST `GET /notes/:id`.
func (s *Server) getNote(ctx context.Context, id Identity, in getNoteIn) (any, error) {
	n, err := s.api.GetNote(ctx, id.User.ID, in.NoteID)
	if err != nil {
		return nil, err
	}
	return s.api.NotesJSON(ctx, id.User.ID, []store.Note{*n})[0], nil
}

// listNoteTypes lists a deck's note types: it goes through the same service method as REST `GET /decks/:id/note-types`.
// The config carries the field ids and names a note payload must use, and the templates the note type generates cards from.
func (s *Server) listNoteTypes(ctx context.Context, id Identity, in listNoteTypesIn) (api.NoteTypeListResponse, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return api.NoteTypeListResponse{}, err
	}
	types, err := s.api.ListNoteTypes(ctx, id.User.ID, d.ID)
	if err != nil {
		return api.NoteTypeListResponse{}, err
	}
	return api.NoteTypeListResponse{NoteTypes: types}, nil
}

// getNoteType reads one note type of a deck: it goes through the same service method as REST `GET /decks/:id/note-types/:type_id`.
func (s *Server) getNoteType(ctx context.Context, id Identity, in getNoteTypeIn) (api.NoteTypeJSON, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return api.NoteTypeJSON{}, err
	}
	return s.api.GetNoteType(ctx, id.User.ID, d.ID, in.NoteTypeID)
}

func (s *Server) getStats(ctx context.Context, id Identity, _ getStatsIn) (any, error) {
	return s.api.Stats(ctx, id.User)
}

// exportDeck exports a deck package: it goes through the same service method as REST `GET /decks/:id/package`.
// It returns the logical content of the package in JSON document form, which can be validated directly against schema/deck-package.schema.json.
func (s *Server) exportDeck(ctx context.Context, id Identity, in exportDeckIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	includeMedia := true
	if in.IncludeMedia != nil {
		includeMedia = *in.IncludeMedia
	}
	pkg, err := s.api.ExportDeckPackage(ctx, id.User.ID, d.ID, store.PackageOptions{
		IncludeProgress: in.IncludeProgress,
		IncludeMedia:    includeMedia,
		IncludeReviews:  in.IncludeReviews,
		IncludeWeights:  in.IncludeWeights,
	})
	if err != nil {
		return nil, err
	}
	return pkg.Document(), nil
}

func (s *Server) createNotes(ctx context.Context, id Identity, in bulkNotesIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	return s.api.ImportNotes(ctx, id.User.ID, d.ID, id.apiKeyID(), toImportRequest(in))
}

// bulkNotes performs a bulk action on a set of notes: it goes through the same service method as REST `POST /notes/bulk`.
// note_ids are notes' public ids; resolution and per-row permission checks happen in the service layer.
func (s *Server) bulkNotes(ctx context.Context, id Identity, in bulkActionIn) (any, error) {
	return s.api.BulkNotes(ctx, id.User.ID, id.apiKeyID(), api.BulkNotesInput{
		Action:  in.Action,
		NoteIDs: in.NoteIDs,
		Tags:    in.Tags,
		DryRun:  in.DryRun,
	})
}

// importDeck imports a deck package: it goes through the same service method as REST `POST /decks/import` (or `POST /decks/import-url`)
// The package's permission checks, progress attribution and auditing are all done in the service layer;
// into_deck:<public id> / replace_deck:<public id> in target are resolved by the service itself.
// package and url are mutually exclusive: exactly one of them, and omitting both reports an argument error.
func (s *Server) importDeck(ctx context.Context, id Identity, in importDeckIn) (any, error) {
	opts := store.PackageImportOptions{
		Target:              in.Target,
		DryRun:              in.DryRun,
		OnConflict:          in.OnConflict,
		SkipMissingMedia:    in.SkipMissingMedia,
		AllowOthersProgress: in.AllowOthersProgress,
		ApplyWeights:        in.ApplyWeights,
	}
	url := strings.TrimSpace(in.URL)
	if url != "" {
		if in.Package != nil {
			return nil, api.InvalidRequest("package and url are mutually exclusive")
		}
		return s.api.ImportDeckPackageURL(ctx, id.User, id.apiKeyID(), url, opts)
	}
	if in.Package == nil {
		return nil, api.InvalidRequest("package or url is required")
	}
	r, err := store.PackageReader(in.Package)
	if err != nil {
		return nil, err
	}
	return s.api.ImportDeckPackage(ctx, id.User, id.apiKeyID(), r, opts)
}

// createImportUpload issues a one-time upload ticket: it goes through the same service method as REST `POST /decks/import-uploads`.
// The package bytes are then PUT by the agent straight to the returned URL, never passing through model output.
func (s *Server) createImportUpload(ctx context.Context, id Identity, in createImportUploadIn) (*api.ImportUpload, error) {
	return s.api.CreateImportUpload(ctx, id.User, id.apiKeyID(), store.PackageImportOptions{
		Target:              in.Target,
		DryRun:              in.DryRun,
		OnConflict:          in.OnConflict,
		SkipMissingMedia:    in.SkipMissingMedia,
		AllowOthersProgress: in.AllowOthersProgress,
		ApplyWeights:        in.ApplyWeights,
	})
}

func (s *Server) updateNote(ctx context.Context, id Identity, in updateNoteIn) (any, error) {
	n, err := s.api.NoteByPublicID(ctx, in.NoteID)
	if err != nil {
		return nil, err
	}
	var tags *[]string
	if in.Tags != nil {
		t := in.Tags
		tags = &t
	}
	updated, err := s.api.UpdateNote(ctx, id.User.ID, n.ID, id.apiKeyID(), api.UpdateNoteInput{
		NoteType: in.NoteType,
		Kind:     in.Kind,
		Fields:   in.Fields,
		Tags:     tags,
	})
	if err != nil {
		return nil, err
	}
	return s.api.NotesJSON(ctx, id.User.ID, []store.Note{*updated})[0], nil
}

func (s *Server) deleteNote(ctx context.Context, id Identity, in deleteNoteIn) (any, error) {
	n, err := s.api.NoteByPublicID(ctx, in.NoteID)
	if err != nil {
		return nil, err
	}
	if _, err := s.api.DeleteNote(ctx, id.User.ID, n.ID, id.apiKeyID()); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": n.PublicID}, nil
}

func (s *Server) getDueCards(ctx context.Context, id Identity, in getDueCardsIn) (any, error) {
	// deck_id and deck_ids are mutually exclusive: giving both is a caller argument error, returned through the same MCP error exit
	// (the client sees isErr and a stable invalid_request code). An empty string counts as not provided.
	deckID := strings.TrimSpace(in.DeckID)
	if deckID != "" && len(in.DeckIDs) > 0 {
		return nil, api.InvalidRequest("deck_id and deck_ids are mutually exclusive")
	}
	// Resolve each public id into a primary key: any unknown one fails the whole call (no silent filtering), matching REST.
	// Empty entries are skipped outright (consistent with REST skipping empty deck query parameters).
	var resolved []uint64
	switch {
	case deckID != "":
		d, err := s.api.DeckByPublicID(ctx, deckID)
		if err != nil {
			return nil, err
		}
		resolved = []uint64{d.ID}
	default:
		for _, raw := range in.DeckIDs {
			pid := strings.TrimSpace(raw)
			if pid == "" {
				continue
			}
			d, err := s.api.DeckByPublicID(ctx, pid)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, d.ID)
		}
	}
	cards, err := s.api.DueCards(ctx, id.User, resolved, in.Tags, in.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"cards": cards}, nil
}

func (s *Server) submitReview(ctx context.Context, id Identity, in submitReviewIn) (any, error) {
	if in.GradeSource != "" && in.GradeSource != "self" {
		return nil, api.InvalidRequest("grade_source is decided by the server")
	}
	var answer json.RawMessage
	if in.Answer != nil {
		raw, err := json.Marshal(in.Answer)
		if err != nil {
			return nil, api.InvalidRequest("answer is not valid JSON")
		}
		answer = raw
	}
	return s.api.SubmitReview(ctx, id.User, id.apiKeyID(), api.SubmitReviewInput{
		CardID:          in.CardID,
		Rating:          in.Rating,
		Answer:          answer,
		GiveUp:          in.GiveUp,
		ExpectedVersion: in.ExpectedVersion,
		ElapsedMS:       in.ElapsedMS,
	})
}

func (s *Server) addExtraNewCards(ctx context.Context, id Identity, in addExtraNewCardsIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	return s.api.AddExtraNew(ctx, id.User, id.apiKeyID(), d.ID, in.Count)
}

// setDeckPaused pauses or resumes a deck for the caller: the same service method as REST `PUT /decks/:id/paused`.
func (s *Server) buryCard(ctx context.Context, id Identity, in buryCardIn) (any, error) {
	return s.api.BuryCard(ctx, id.User, in.CardID)
}

func (s *Server) setDeckPaused(ctx context.Context, id Identity, in setDeckPausedIn) (any, error) {
	d, err := s.api.DeckByPublicID(ctx, in.DeckID)
	if err != nil {
		return nil, err
	}
	return s.api.SetDeckPaused(ctx, id.User, id.apiKeyID(), d.ID, in.Paused)
}

// toImportRequest turns the MCP input arguments into the service layer's ImportRequest; shared by both tools.
func toImportRequest(in bulkNotesIn) api.ImportRequest {
	notes := make([]api.ImportNote, 0, len(in.Notes))
	for _, n := range in.Notes {
		notes = append(notes, api.ImportNote{
			NoteType:    n.NoteType,
			Kind:        n.Kind,
			Fields:      n.Fields,
			ExternalRef: n.ExternalRef,
			NoteID:      n.NoteID,
			Tags:        n.Tags,
		})
	}
	return api.ImportRequest{DryRun: in.DryRun, OnConflict: in.OnConflict, Notes: notes}
}
