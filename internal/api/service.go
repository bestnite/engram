package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// This file is the service layer shared by REST and the built-in MCP: both only do parameter
// validation and wrapping (the real business rules must exist exactly once). REST handlers and MCP tools both call the methods
// here, so the same-named operation produces exactly the same result.

// ServiceError carries a stable error code and an HTTP-semantics status code, shared by the REST envelope and MCP error results.
type ServiceError struct {
	Status  int
	Code    string
	Message string
}

func (e *ServiceError) Error() string { return e.Message }

// newServiceError constructs a ServiceError.
func newServiceError(status int, code, message string) *ServiceError {
	return &ServiceError{Status: status, Code: code, Message: message}
}

// InvalidRequest constructs a ServiceError with 400 invalid_request.
//
// Callers outside REST (e.g. MCP tools checking mutually exclusive parameters) need the same error shape and stable codes as REST,
// but must not invent their own error structure; exporting this one constructor lets them reuse the same error exit.
func InvalidRequest(message string) *ServiceError {
	return newServiceError(http.StatusBadRequest, CodeInvalidRequest, message)
}

// asServiceError normalizes any error into a ServiceError; unrecognized errors become 500 internal.
func asServiceError(err error) *ServiceError {
	var se *ServiceError
	if errors.As(err, &se) {
		return se
	}
	return newServiceError(http.StatusInternalServerError, CodeInternal, err.Error())
}

// ---- Decks ----

// DeckWithRole is a deck plus the caller's explicit relation to it, so list endpoints can mark "mine" and "shared with me".
//
// Role is owner (their own, decided by decks.owner_user_id) or an explicitly granted role of editor/reader.
// It is necessarily one of the two: the list itself contains only owned and granted decks (store.visibleDeckIDsQuery),
// so there is no deck that is "visible but has no role", and the UI needs no branch for that case.
type DeckWithRole struct {
	Deck store.Deck
	Role string
}

// ListDecks returns the decks visible to the user (owned ∪ granted via deck_grants), each carrying
// the caller's explicit relation (see DeckWithRole).
//
// A deck someone else has not granted me never shows up here: decks accept no ownership of the form "entered someone else's list without consent".
// The web deck queue counts read the same DeckStore.ListVisible, and the review queue's whole-library scope (DeckStore.VisibleIDs) matches it
// exactly, and the predicate exists only once, as store.visibleDeckIDsQuery. REST and the built-in MCP
// both call here; switching either one to an owner-only query would hide shared decks from external callers.
func (a *API) ListDecks(ctx context.Context, userID uint64) ([]DeckWithRole, error) {
	decks, err := a.decks.ListVisible(ctx, userID)
	if err != nil {
		a.logger.Error("list decks failed", "user_id", userID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list decks")
	}
	roles, err := a.grants.RolesForUser(ctx, userID)
	if err != nil {
		a.logger.Error("list deck roles failed", "user_id", userID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list decks")
	}
	out := make([]DeckWithRole, 0, len(decks))
	for _, d := range decks {
		role := roles[d.ID]
		if d.OwnerUserID == userID {
			role = store.RoleOwner
		}
		out = append(out, DeckWithRole{Deck: d, Role: role})
	}
	return out, nil
}

// CreateDeckInput is the input for creating a deck.
// APIKeyID is used only in audit entries and is nil on the session channel (web login).
type CreateDeckInput struct {
	Name        string
	Description string
	PresetID    uint64
	APIKeyID    *uint64
}

// CreateDeck creates an empty deck (scope: write); REST and the built-in MCP share this one implementation.
//
// Behavior and error codes match the old REST handler implementation exactly: name is required after trimming, and preset_id of 0 uses
// (or creates) the caller's Default preset; any store rejection maps to invalid_request,
// and a default preset that cannot be ensured maps to internal_error.
func (a *API) CreateDeck(ctx context.Context, u *store.User, in CreateDeckInput) (*store.Deck, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "")
	}
	presetID := in.PresetID
	if presetID == 0 {
		id, err := a.ensureDefaultPreset(ctx, u.ID)
		if err != nil {
			a.logger.Error("ensure default preset failed", "user_id", u.ID, "error", err)
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "")
		}
		presetID = id
	}
	d := store.Deck{
		OwnerUserID: u.ID,
		Name:        name,
		Description: in.Description,
		PresetID:    presetID,
		CreatedAt:   a.now(),
	}
	if err := a.decks.Create(ctx, &d); err != nil {
		// A deck name violating the rules shared with the deck package manifest gets its own code; other store rejections
		// still map to invalid_request, matching existing REST/MCP behavior.
		if errors.Is(err, store.ErrDeckDescriptionInvalid) {
			return nil, newServiceError(http.StatusBadRequest, CodeDeckDescriptionInvalid, "")
		}
		if errors.Is(err, store.ErrDeckNameInvalid) {
			return nil, newServiceError(http.StatusBadRequest, CodeDeckNameInvalid, "")
		}
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "")
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   in.APIKeyID,
		Action:     store.ActionDeckCreate,
		TargetType: "deck",
		TargetID:   store.Ptr(d.ID),
		Detail:     map[string]any{"name": d.Name},
	})
	return &d, nil
}

// UpdateDeckInput is the input for changing a deck's name and description; APIKeyID is used only in audit entries.
// A nil Name / Description means that field is not being changed (PATCH semantics), so omitting the description does not clear it.
type UpdateDeckInput struct {
	Name        *string
	Description *string
	APIKeyID    *uint64
}

// UpdateDeck changes a deck's name and description (scope: write); shared by REST and the built-in MCP.
//
// Only the owner can change it: shared members' study settings go through SetStudySettings and never change the deck itself.
// Only the fields the caller supplied are updated; name and description go through the store's one validation (same source as deck creation and package import), and nothing is written on failure;
// validation failures map to the same dedicated codes as deck creation, and non-owners map to forbidden.
func (a *API) UpdateDeck(ctx context.Context, u *store.User, deckID uint64, in UpdateDeckInput) (*store.Deck, error) {
	name := in.Name
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		name = &trimmed
	}
	if err := a.decks.Update(ctx, u.ID, deckID, name, in.Description); err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return nil, newServiceError(http.StatusNotFound, CodeNotFound, "")
		case errors.Is(err, store.ErrNotOwner):
			return nil, newServiceError(http.StatusForbidden, CodeForbidden, "")
		case errors.Is(err, store.ErrDeckDescriptionInvalid):
			return nil, newServiceError(http.StatusBadRequest, CodeDeckDescriptionInvalid, "")
		case errors.Is(err, store.ErrDeckNameInvalid):
			return nil, newServiceError(http.StatusBadRequest, CodeDeckNameInvalid, "")
		default:
			return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "")
		}
	}
	out, err := a.decks.ByID(ctx, deckID)
	if err != nil {
		a.logger.Error("reload deck after update failed", "deck_id", deckID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   in.APIKeyID,
		Action:     store.ActionDeckUpdate,
		TargetType: "deck",
		TargetID:   store.Ptr(deckID),
		Detail:     map[string]any{"name": out.Name},
	})
	return out, nil
}

// DeleteDeck deletes a deck (scope: write); shared by REST and the built-in MCP. Only the owner can delete.
func (a *API) DeleteDeck(ctx context.Context, u *store.User, deckID uint64, apiKeyID *uint64) error {
	deck, err := a.decks.ByID(ctx, deckID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return newServiceError(http.StatusNotFound, CodeNotFound, "")
		}
		a.logger.Error("load deck for delete failed", "deck_id", deckID, "error", err)
		return newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	if deck.OwnerUserID != u.ID {
		return newServiceError(http.StatusForbidden, CodeForbidden, "")
	}
	if err := a.decks.Delete(ctx, u.ID, deckID); err != nil {
		a.logger.Error("delete deck failed", "deck_id", deckID, "user_id", u.ID, "error", err)
		return newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionDeckDelete,
		TargetType: "deck",
		TargetID:   store.Ptr(deckID),
		Detail:     map[string]any{"deck_name": deck.Name},
	})
	return nil
}

// bulkMaxDecks is the maximum number of decks one bulk action accepts after deduplication.
// Deleting a deck cascades over every card (and every user's progress row) of that deck, so the
// bound is deliberately tighter than the note bulk cap: one request must not be able to wipe an
// unbounded share of the database.
const bulkMaxDecks = 100

// BulkDecksInput is the input of a bulk deck action (shared by REST and the built-in MCP, one model serving both the business and the JSON).
// DeckIDs are the decks' external ids (opaque strings); the service resolves them to primary keys before the numeric logic runs.
type BulkDecksInput struct {
	Action  string   `json:"action"`
	DeckIDs []string `json:"deck_ids"`
	DryRun  bool     `json:"dry_run"`
}

// BulkDecksSkipped is a per-row rejected deck: code is not_found (unknown public id) or forbidden (the caller is not the owner,
// which also covers a deck merely shared with the caller). DeckID echoes the external id as given in the request verbatim.
type BulkDecksSkipped struct {
	DeckID string `json:"deck_id"`
	Code   string `json:"code"`
}

// BulkDecksResponse is the bulk deck action response body. Affected counts the decks actually deleted.
type BulkDecksResponse struct {
	DryRun   bool               `json:"dry_run"`
	Affected int64              `json:"affected"`
	Skipped  []BulkDecksSkipped `json:"skipped"`
}

// BulkDecks performs a bulk deck action; delete is currently the only action.
//
// Request-level validation (unknown action, deck_ids out of range) returns 400 invalid_request and writes not a single row.
// After validation each row is authorized: an unknown public id is recorded as not_found, a deck the caller does not own
// (including one only shared with them) as forbidden, and the remaining decks are deleted in one transaction - one rejected
// row never rolls back the batch. A storage failure rolls the whole batch back and returns internal.
// Auditing writes one deck.delete row per deleted deck, and only when not dry_run; dry_run writes neither rows nor audits.
func (a *API) BulkDecks(ctx context.Context, userID uint64, apiKeyID *uint64, in BulkDecksInput) (BulkDecksResponse, error) {
	action := strings.TrimSpace(in.Action)
	if action != bulkActionDelete {
		return BulkDecksResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			"action must be delete")
	}

	ids := uniquePublicIDs(in.DeckIDs)
	if len(ids) == 0 || len(ids) > bulkMaxDecks {
		return BulkDecksResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("deck_ids must contain between 1 and %d entries", bulkMaxDecks))
	}

	resp := BulkDecksResponse{DryRun: in.DryRun, Skipped: []BulkDecksSkipped{}}
	permitted := make([]uint64, 0, len(ids))
	names := make(map[uint64]string, len(ids))
	for _, publicID := range ids {
		d, err := a.decks.ByPublicID(ctx, publicID)
		if err != nil {
			// Unknown ids are reported row by row rather than failing the batch, matching the note bulk contract.
			resp.Skipped = append(resp.Skipped, BulkDecksSkipped{DeckID: publicID, Code: CodeNotFound})
			continue
		}
		if d.OwnerUserID != userID {
			// A grant of any role is not ownership: deleting a deck is the owner's irreversible operation only.
			resp.Skipped = append(resp.Skipped, BulkDecksSkipped{DeckID: publicID, Code: CodeForbidden})
			continue
		}
		permitted = append(permitted, d.ID)
		names[d.ID] = d.Name
	}

	affected, err := a.decks.DeleteMany(ctx, permitted, in.DryRun)
	if err != nil {
		a.logger.Error("bulk deck delete failed", "user_id", userID, "error", err)
		return BulkDecksResponse{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to delete decks")
	}
	resp.Affected = affected

	if !in.DryRun {
		// One row per deck keeps the same shape as the single-deck delete audit, so history stays aggregatable by target.
		for _, deckID := range permitted {
			a.auditor.Record(ctx, store.AuditEntry{
				UserID:     store.Ptr(userID),
				APIKeyID:   apiKeyID,
				Action:     store.ActionDeckDelete,
				TargetType: "deck",
				TargetID:   store.Ptr(deckID),
				Detail:     map[string]any{"deck_name": names[deckID]},
			})
		}
	}
	return resp, nil
}

// LeaveDeck lets a share recipient leave a deck (scope: write): it deletes their own grant row in deck_grants.
// It is a separate matter from DeleteDeck, independent of it: leaving revokes only your own grant row, touches no deck content, and is reversible
// (being granted again brings it back); deleting a deck is irreversible. So the owner does not take this path - for their own deck, deleting is the only option.
// With no grant row it returns not_found: there is no membership to leave - a deck is either owned or granted,
// with no in-between state of "merely visible".
//
// Leaving does not clean up that user's card_states on the deck: progress is isolated per user and survives a later re-grant.
// An owner revoking someone's grant (internal/web/sharing_api.go) keeps progress too, the same on both sides.
func (a *API) LeaveDeck(ctx context.Context, u *store.User, deckID uint64, apiKeyID *uint64) error {
	deck, err := a.decks.ByID(ctx, deckID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return newServiceError(http.StatusNotFound, CodeNotFound, "")
		}
		a.logger.Error("load deck for leave failed", "deck_id", deckID, "error", err)
		return newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	if deck.OwnerUserID == u.ID {
		return newServiceError(http.StatusBadRequest, CodeInvalidRequest, "")
	}
	role, err := a.grants.Role(ctx, deckID, u.ID)
	if err != nil {
		a.logger.Error("read deck grant for leave failed", "deck_id", deckID, "user_id", u.ID, "error", err)
		return newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	if !store.ValidRole(role) {
		return newServiceError(http.StatusNotFound, CodeNotFound, "")
	}
	if err := a.grants.Revoke(ctx, deckID, u.ID); err != nil {
		a.logger.Error("leave deck failed", "deck_id", deckID, "user_id", u.ID, "error", err)
		return newServiceError(http.StatusInternalServerError, CodeInternal, "")
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionDeckRevoke,
		TargetType: "deck",
		TargetID:   store.Ptr(deckID),
		Detail:     map[string]any{"deck_name": deck.Name, "previous_role": role, "self": true},
	})
	return nil
}

// RequireDeckRole checks that the user holds at least the want role on the deck.
//
// The decision itself is in auth.DeckAccess (one implementation shared by REST and the built-in MCP); here it is translated into a
// ServiceError with a stable code, plus one permission.denied audit entry. No access -> forbidden,
// has a role but not enough -> insufficient_role, deck does not exist -> not_found.
func (a *API) RequireDeckRole(ctx context.Context, userID, deckID uint64, want string) (*store.Deck, error) {
	deck, role, err := a.access.RequireRole(ctx, deckID, userID, want)
	if err == nil {
		return deck, nil
	}
	if errors.Is(err, auth.ErrDeckNotFound) {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "deck not found")
	}
	code := CodeForbidden
	if role != "" {
		code = CodeInsufficientRole
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		Action:     store.ActionPermissionDenied,
		TargetType: "deck",
		TargetID:   store.Ptr(deckID),
		Detail:     map[string]any{"required_role": want, "user_role": role, "code": code},
	})
	return nil, newServiceError(http.StatusForbidden, code, "insufficient role for this deck")
}

// RequireNoteRole fetches the note and its deck and requires at least the want role; a missing note returns 404.
func (a *API) RequireNoteRole(ctx context.Context, userID, noteID uint64, want string) (*store.Note, *store.Deck, error) {
	n, err := a.notes.ByID(ctx, noteID)
	if err != nil {
		return nil, nil, newServiceError(http.StatusNotFound, CodeNotFound, "note not found")
	}
	d, err := a.RequireDeckRole(ctx, userID, n.DeckID, want)
	if err != nil {
		return nil, nil, err
	}
	return n, d, nil
}

// countPlan counts one plan toward the dry_run tallies.
func countPlan(resp *ImportResponse, action string) {
	switch action {
	case "create":
		resp.Created++
	case "update":
		resp.Updated++
	case "skip":
		resp.Skipped++
	}
}

// findNoteByExternalRef looks up a note by (deck_id, external_ref), including soft-deleted rows
// - the unique index covers soft-deleted rows too, so the query must be Unscoped, otherwise a repeated import hits the unique constraint.
func (a *API) findNoteByExternalRef(ctx context.Context, deckID uint64, ref string) (*store.Note, error) {
	var n store.Note
	err := a.db.WithContext(ctx).Unscoped().
		Where("deck_id = ? AND external_ref = ?", deckID, ref).First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// ---- Cards ----

// ListNotes returns the card list under a deck (paging, tag filtering, keyword search).
func (a *API) ListNotes(ctx context.Context, userID, deckID uint64, opts store.NoteListOptions) ([]store.Note, int64, error) {
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader)
	if err != nil {
		return nil, 0, err
	}
	opts = store.NormalizeNoteListOptions(opts)
	opts.DeckID = d.ID
	notes, total, err := a.notes.List(ctx, opts)
	if err != nil {
		a.logger.Error("list notes failed", "deck_id", d.ID, "error", err)
		return nil, 0, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list notes")
	}
	return notes, total, nil
}

// GetNote reads a single note by external id and requires the caller to be at least reader on the note's deck.
//
// The edit page only has a note's external id, and list paging would make "which page it is on" part of correctness;
// with single reads the edit page no longer depends on the list covering the target row. An unknown id and a read without permission are both 404,
// never leaking "this id exists but you may not see it".
func (a *API) GetNote(ctx context.Context, userID uint64, notePublicID string) (*store.Note, error) {
	n, err := a.notes.ByPublicID(ctx, notePublicID)
	if err != nil {
		return nil, newServiceError(http.StatusNotFound, CodeNotFound, "note not found")
	}
	if _, err := a.RequireDeckRole(ctx, userID, n.DeckID, store.RoleReader); err != nil {
		return nil, err
	}
	return n, nil
}

// ImportNote is one note of a bulk import; field names match schema/note-import.schema.json.
//
// NoteType is the note type public id and Fields maps a field name to its HTML value. Kind is the
// transitional alias the current SPA still sends: when NoteType is empty, Kind resolves to the deck's
// stock note type for that kind, creating it when the deck has none.
type ImportNote struct {
	NoteType    string         `json:"note_type"`
	Kind        string         `json:"kind"`
	Fields      map[string]any `json:"fields"`
	ExternalRef string         `json:"external_ref"`
	// NoteID addresses an existing note by external id; mutually exclusive with ExternalRef. An empty string means not addressed by id.
	NoteID string   `json:"note_id"`
	Tags   []string `json:"tags"`
}

// ImportRequest is the bulk create/update body; dry_run and on_conflict are body fields.
type ImportRequest struct {
	DryRun     bool         `json:"dry_run"`
	OnConflict string       `json:"on_conflict"`
	Notes      []ImportNote `json:"notes"`
}

// ImportError is a per-row import error: index is the position in the body's notes array.
type ImportError struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// ImportResponse is the bulk import response body.
type ImportResponse struct {
	DryRun  bool          `json:"dry_run"`
	Created int           `json:"created"`
	Updated int           `json:"updated"`
	Skipped int           `json:"skipped"`
	Errors  []ImportError `json:"errors"`
}

// MaxImportNotes is the cap for one bulk import (≤ 500 per request).
const MaxImportNotes = 500

// ImportBatchSize is the number of rows written per transaction in a bulk import (200 rows/transaction by default).
// Batch commits cut fsync from once per row to once per 200 rows, and also make "a failure is resumable" true: if the process
// exits mid-import, the committed batches stay in the database, and retrying the same request hits idempotently on external_ref without creating duplicate cards.
const ImportBatchSize = 200

// importPlan is the processing plan for one bulk-import row: the first pass decides whether the row is created, updated or skipped.
type importPlan struct {
	index    int
	action   string
	existing *store.Note
	note     store.Note
	fields   map[string]any
}

// ImportNotes bulk-adds/updates cards: idempotent per (deck_id, external_ref).
//
// It uses two passes: the first validates and plans each row's fate (create/update/skip), the second writes to the database.
// The second pass batches by ImportBatchSize, one transaction per batch; inside a batch a failing row is rolled back to its savepoint and
// its reason recorded with the index, while valid rows import as usual - so "a batch containing one invalid row still imports the valid rows and
// reports the failed row's index". dry_run stops after the first pass and only returns counts; with on_conflict=fail
// any conflict or validation error rejects the whole batch.
func (a *API) ImportNotes(ctx context.Context, userID, deckID uint64, apiKeyID *uint64, req ImportRequest) (ImportResponse, error) {
	// The per-note pre-write check uses it to decide "whether references newly introduced by this request are readable by the caller".
	ctx = store.WithActor(ctx, userID)
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleEditor)
	if err != nil {
		return ImportResponse{}, err
	}
	if len(req.Notes) == 0 || len(req.Notes) > MaxImportNotes {
		return ImportResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("notes must contain between 1 and %d items", MaxImportNotes))
	}
	onConflict := strings.TrimSpace(req.OnConflict)
	if onConflict == "" {
		onConflict = "update"
	}
	if onConflict != "skip" && onConflict != "update" && onConflict != "fail" {
		return ImportResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "on_conflict must be one of skip, update, fail")
	}

	plans := make([]importPlan, 0, len(req.Notes))
	resp := ImportResponse{DryRun: req.DryRun, Errors: []ImportError{}}
	hasFailure := false

	for i, item := range req.Notes {
		item.NoteType = strings.TrimSpace(item.NoteType)
		item.Kind = strings.TrimSpace(item.Kind)
		if item.NoteType == "" && item.Kind == "" {
			resp.Errors = append(resp.Errors, ImportError{i, "note_type is required"})
			hasFailure = true
			continue
		}
		if len(item.Fields) == 0 {
			resp.Errors = append(resp.Errors, ImportError{i, "fields are required"})
			hasFailure = true
			continue
		}
		if item.NoteType == "" {
			if _, ok := notetype.EngramStockByKind(item.Kind); !ok {
				resp.Errors = append(resp.Errors, ImportError{i, unknownKindText(item.Kind)})
				hasFailure = true
				continue
			}
		}

		if strings.TrimSpace(item.NoteID) != "" && strings.TrimSpace(item.ExternalRef) != "" {
			// The two addressing modes are mutually exclusive: giving both leaves no way to tell which one locates the row, so the row is invalid.
			resp.Errors = append(resp.Errors, ImportError{i, "note_id and external_ref are mutually exclusive"})
			hasFailure = true
			continue
		}

		ref := strings.TrimSpace(item.ExternalRef)
		byID := strings.TrimSpace(item.NoteID) != ""
		var existing *store.Note
		if byID {
			// Addressed by external id: resolve to a note first, then accept only one that belongs to this deck and is not soft-deleted. A miss and a cross-deck hit share the same
			// "note not found in deck", so it does not leak whether the note exists in another deck.
			found, err := a.notes.ByPublicID(ctx, item.NoteID)
			if err != nil || found.DeckID != d.ID {
				resp.Errors = append(resp.Errors, ImportError{i, "note not found in deck"})
				hasFailure = true
				continue
			}
			existing = found
		} else if ref != "" {
			found, err := a.findNoteByExternalRef(ctx, d.ID, ref)
			if err != nil {
				a.logger.Error("lookup note by external_ref failed", "deck_id", d.ID, "error", err)
				return ImportResponse{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load existing notes")
			}
			existing = found
		}

		var noteTypeID uint64
		if item.NoteType != "" {
			resolved, err := a.noteTypeIDForPublicID(ctx, d.ID, item.NoteType)
			if err != nil {
				resp.Errors = append(resp.Errors, ImportError{i, err.Error()})
				hasFailure = true
				continue
			}
			noteTypeID = resolved
		} else {
			resolved, err := a.noteTypeIDFor(ctx, d.ID, item.Kind)
			if err != nil {
				resp.Errors = append(resp.Errors, ImportError{i, err.Error()})
				hasFailure = true
				continue
			}
			noteTypeID = resolved
		}
		n := store.Note{
			DeckID:     d.ID,
			NoteTypeID: store.Ptr(noteTypeID),
			TagsJSON:   store.TagsJSON(item.Tags),
			CreatedBy:  store.Ptr(userID),
			Source:     store.Ptr("api"),
		}
		if ref != "" {
			n.ExternalRef = store.Ptr(ref)
		}

		action := "create"
		if existing != nil {
			if byID {
				// A note_id row exists by definition, so on_conflict does not apply to it: it always takes the update path,
				// and never triggers a whole-batch rejection under on_conflict=fail.
				action = "update"
			} else {
				switch onConflict {
				case "skip":
					action = "skip"
				case "fail":
					resp.Errors = append(resp.Errors, ImportError{i, "a note with this external_ref already exists"})
					hasFailure = true
					continue
				default:
					action = "update"
				}
			}
		}
		plans = append(plans, importPlan{index: i, action: action, existing: existing, note: n, fields: item.Fields})
	}

	if onConflict == "fail" && hasFailure {
		return ImportResponse{}, newServiceError(http.StatusConflict, CodeConflict, "import aborted: one or more notes are invalid or conflict")
	}

	if req.DryRun {
		for _, p := range plans {
			countPlan(&resp, p.action)
		}
		return resp, nil
	}

	// Second pass: write by batch. One transaction per batch; a failing row rolls back to its savepoint and records its index,
	// while the rest of the batch imports as usual. Batches commit independently, so a failure is resumable.
	for start := 0; start < len(plans); start += ImportBatchSize {
		end := start + ImportBatchSize
		if end > len(plans) {
			end = len(plans)
		}
		batch := plans[start:end]
		batchErr := a.writeImportBatch(ctx, batch, &resp)
		if batchErr != nil {
			// Infrastructure-level failure (savepoint/commit failure): record the index of the batch's first row and continue with the next batch,
			// committed batches are not rolled back; external_ref idempotency on retry prevents duplicate cards.
			a.logger.Error("import batch failed", "deck_id", d.ID, "start_index", batch[0].index, "error", batchErr)
			resp.Errors = append(resp.Errors, ImportError{batch[0].index, batchErr.Error()})
		}
	}

	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     "note.import",
		TargetType: "deck",
		TargetID:   store.Ptr(d.ID),
		Detail: map[string]any{
			"created": resp.Created, "updated": resp.Updated,
			"skipped": resp.Skipped, "errors": len(resp.Errors), "dry_run": req.DryRun,
		},
	})
	return resp, nil
}

// writeImportBatch writes one batch of import plans in a single transaction; a non-nil return means a batch-level failure (already rolled back).
//
// Each row is wrapped in a SAVEPOINT: PostgreSQL aborts the whole transaction after a statement error, so ROLLBACK TO SAVEPOINT is required
// to keep processing the other rows of the batch; SQLite supports the same syntax. Per-row errors go into resp.Errors (with the body index).
func (a *API) writeImportBatch(ctx context.Context, batch []importPlan, resp *ImportResponse) error {
	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, p := range batch {
			if p.action == "skip" {
				resp.Skipped++
				continue
			}
			savepoint := fmt.Sprintf("import_row_%d", p.index)
			if err := tx.SavePoint(savepoint).Error; err != nil {
				return fmt.Errorf("open savepoint for row %d: %w", p.index, err)
			}
			var rowErr error
			switch p.action {
			case "create":
				n := p.note
				_, rowErr = a.notes.CreateInTx(ctx, tx, &n, p.fields)
			default: // update
				if p.existing.DeletedAt.Valid {
					rowErr = a.notes.RestoreInTx(ctx, tx, p.existing.ID)
				}
				if rowErr == nil {
					n := p.note
					n.ID = p.existing.ID
					_, rowErr = a.notes.UpdateInTx(ctx, tx, &n, p.fields)
				}
			}
			if rowErr != nil {
				if rbErr := tx.RollbackTo(savepoint).Error; rbErr != nil {
					return fmt.Errorf("rollback savepoint for row %d: %w", p.index, rbErr)
				}
				resp.Errors = append(resp.Errors, ImportError{p.index, rowErr.Error()})
				continue
			}
			if p.action == "create" {
				resp.Created++
			} else {
				resp.Updated++
			}
		}
		return nil
	})
}

// UpdateNoteInput is the single-note update input; Tags uses a pointer to distinguish "not provided"
// from "clear it". NoteType is the note type public id, and Kind is the transitional alias the current SPA sends.
type UpdateNoteInput struct {
	NoteType string
	Kind     string
	Fields   map[string]any
	Tags     *[]string
}

// UpdateNote updates a card's content; the id and user progress of existing cards stay unchanged (guaranteed by NoteStore.Update).
func (a *API) UpdateNote(ctx context.Context, userID, noteID uint64, apiKeyID *uint64, in UpdateNoteInput) (*store.Note, error) {
	// The pre-write check uses it to decide "whether references newly introduced by this request are readable by the caller".
	ctx = store.WithActor(ctx, userID)
	existing, _, err := a.RequireNoteRole(ctx, userID, noteID, store.RoleEditor)
	if err != nil {
		return nil, err
	}
	if len(in.Fields) == 0 {
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "fields are required")
	}
	noteTypeID := uint64(0)
	if publicID := strings.TrimSpace(in.NoteType); publicID != "" {
		resolved, err := a.noteTypeIDForPublicID(ctx, existing.DeckID, publicID)
		if err != nil {
			return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, err.Error())
		}
		noteTypeID = resolved
	} else {
		// The kind alias names a shape, not one note type. A deck may hold several note types of the same shape
		// (a copy with its own CSS or templates), so the note keeps the note type it already has when the request
		// names no type, or names the shape the note already has. Resolving the alias to "the first type of that
		// shape" would move the note into a different type on an unrelated content edit.
		kind := strings.TrimSpace(in.Kind)
		if kind == "" || kind == a.noteKindOf(ctx, existing) {
			if existing.NoteTypeID != nil {
				noteTypeID = *existing.NoteTypeID
			}
		} else {
			resolved, err := a.noteTypeIDFor(ctx, existing.DeckID, kind)
			if err != nil {
				return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, err.Error())
			}
			noteTypeID = resolved
		}
	}
	if noteTypeID == 0 && existing.NoteTypeID != nil {
		noteTypeID = *existing.NoteTypeID
	}
	if noteTypeID == 0 {
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "note_type is required")
	}
	n := store.Note{ID: existing.ID, DeckID: existing.DeckID, NoteTypeID: store.Ptr(noteTypeID)}
	if in.Tags != nil {
		n.TagsJSON = store.TagsJSON(*in.Tags)
	} else {
		n.TagsJSON = existing.TagsJSON
	}
	if _, err := a.notes.Update(ctx, &n, in.Fields); err != nil {
		var mwe *store.MediaWriteError
		if errors.As(err, &mwe) {
			// Rejected by the pre-write check: referenced media the caller cannot read, an item-level stable code.
			return nil, newServiceError(http.StatusForbidden, CodeMediaNotReadable, strings.Join(mwe.Entries, ", "))
		}
		a.logger.Error("update note failed", "note_id", existing.ID, "error", err)
		return nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, err.Error())
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionNoteUpdate,
		TargetType: "note",
		TargetID:   store.Ptr(existing.ID),
	})
	updated, err := a.notes.ByID(ctx, existing.ID)
	if err != nil {
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to reload note")
	}
	return updated, nil
}

// DeleteNote soft-deletes a card (progress is kept, an accidental delete can be undone); returns the deleted note's id.
func (a *API) DeleteNote(ctx context.Context, userID, noteID uint64, apiKeyID *uint64) (uint64, error) {
	existing, _, err := a.RequireNoteRole(ctx, userID, noteID, store.RoleEditor)
	if err != nil {
		return 0, err
	}
	if err := a.notes.Delete(ctx, existing.ID); err != nil {
		a.logger.Error("delete note failed", "note_id", existing.ID, "error", err)
		return 0, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to delete note")
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		APIKeyID:   apiKeyID,
		Action:     store.ActionNoteDelete,
		TargetType: "note",
		TargetID:   store.Ptr(existing.ID),
	})
	return existing.ID, nil
}

// ---- Bulk card actions ----

// The stable English values of bulk actions (shared by the REST body's action field and the audit detail.action).
const (
	bulkActionDelete     = "delete"
	bulkActionAddTags    = "add_tags"
	bulkActionRemoveTags = "remove_tags"
	bulkActionSetTags    = "set_tags"
)

// bulkMaxTags is the maximum number of tags a single tag action allows (after deduplication).
const bulkMaxTags = 20

// BulkNotesInput is the input of a bulk card action (shared by REST and the built-in MCP, one model serving both the business and the JSON).
// NoteIDs are the notes' external ids (opaque strings); the service resolves them to primary keys before the numeric logic runs.
type BulkNotesInput struct {
	Action  string   `json:"action"`
	NoteIDs []string `json:"note_ids"`
	Tags    []string `json:"tags"`
	DryRun  bool     `json:"dry_run"`
}

// BulkNotesSkipped is a per-row rejected note: code is not_found / insufficient_role.
// NoteID is the external id as given in the request, echoed back verbatim; the auto-increment primary key is never echoed.
type BulkNotesSkipped struct {
	NoteID string `json:"note_id"`
	Code   string `json:"code"`
}

// BulkNotesResponse is the bulk action response body. Affected counts only rows actually changed,
// so submitting the same request a second time returns affected=0.
type BulkNotesResponse struct {
	DryRun   bool               `json:"dry_run"`
	Affected int64              `json:"affected"`
	Skipped  []BulkNotesSkipped `json:"skipped"`
}

// BulkNotes performs delete / add_tags / remove_tags / set_tags on a set of notes.
//
// Request-level validation (unknown action, note_ids out of range, a tag action without tags, delete with tags) returns 400
// invalid_request and writes not a single row. After validation each row is authorized: a missing or soft-deleted id is recorded as not_found,
// an id without permission or with an insufficient role as insufficient_role, and the remaining rows are processed normally - one rejected row does not roll back the batch.
// Auditing writes one row for the whole batch, and only when not dry_run; dry_run only counts and writes neither rows nor audits.
func (a *API) BulkNotes(ctx context.Context, userID uint64, apiKeyID *uint64, in BulkNotesInput) (BulkNotesResponse, error) {
	action := strings.TrimSpace(in.Action)
	switch action {
	case bulkActionDelete, bulkActionAddTags, bulkActionRemoveTags, bulkActionSetTags:
	default:
		return BulkNotesResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			"action must be one of delete, add_tags, remove_tags, set_tags")
	}

	ids := uniquePublicIDs(in.NoteIDs)
	if len(ids) == 0 || len(ids) > MaxImportNotes {
		return BulkNotesResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("note_ids must contain between 1 and %d entries", MaxImportNotes))
	}

	tags := store.NormalizeTags(in.Tags)
	if action == bulkActionDelete {
		if len(in.Tags) > 0 {
			return BulkNotesResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
				"tags are not allowed for action delete")
		}
	} else if len(tags) == 0 || len(tags) > bulkMaxTags {
		return BulkNotesResponse{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("tags must contain between 1 and %d entries", bulkMaxTags))
	}

	resp := BulkNotesResponse{DryRun: in.DryRun, Skipped: []BulkNotesSkipped{}}
	permitted := make([]uint64, 0, len(ids))
	for _, publicID := range ids {
		// The external id is resolved to a note first: unknown, soft-deleted or empty is always not_found.
		// Once resolved it goes through the shared per-row authorization (bulkRowCode), the same convention as other REST writes.
		n, err := a.notes.ByPublicID(ctx, publicID)
		if err != nil {
			resp.Skipped = append(resp.Skipped, BulkNotesSkipped{NoteID: publicID, Code: CodeNotFound})
			continue
		}
		if code := a.bulkRowCode(ctx, userID, n.ID, !in.DryRun); code != "" {
			// A single row failing authorization does not fail the batch: not_found and insufficient_role each add one skipped entry,
			// and the remaining ids are still processed.
			resp.Skipped = append(resp.Skipped, BulkNotesSkipped{NoteID: publicID, Code: code})
			continue
		}
		permitted = append(permitted, n.ID)
	}

	var affected int64
	var err error
	switch action {
	case bulkActionDelete:
		affected, err = a.notes.DeleteMany(ctx, permitted, in.DryRun)
	case bulkActionAddTags:
		affected, err = a.notes.AddTags(ctx, permitted, tags, in.DryRun)
	case bulkActionRemoveTags:
		affected, err = a.notes.RemoveTags(ctx, permitted, tags, in.DryRun)
	case bulkActionSetTags:
		affected, err = a.notes.SetTags(ctx, permitted, tags, in.DryRun)
	}
	if err != nil {
		a.logger.Error("bulk note action failed", "action", action, "error", err)
		return BulkNotesResponse{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to apply bulk note action")
	}
	resp.Affected = affected

	if !in.DryRun {
		// Aligned with the web bulk form: one audit row for the batch, with the action, the requested id list and the number of changed rows in detail.
		a.auditor.Record(ctx, store.AuditEntry{
			UserID:     store.Ptr(userID),
			APIKeyID:   apiKeyID,
			Action:     bulkAuditAction(action),
			TargetType: "notes",
			Detail:     map[string]any{"action": action, "ids": ids, "affected": affected},
		})
	}
	return resp, nil
}

// bulkRowCode decides whether the caller may edit the note: editable returns the empty string, otherwise the stable code for that row's skipped entry
// (the same value set as error codes: not_found / insufficient_role).
//
// With audit true it goes through the shared RequireNoteRole - which writes a permission.denied audit entry when the role is insufficient,
// matching the forensic convention of other REST writes (who tried to change what, when, and was blocked). dry_run passes false and decides each row
// silently: a dry run where 500 ids all lack permission must not shake out 500 audit rows (dry_run writes
// no rows, no audit rows, zero side effects). Both paths must give the same code for the same situation.
func (a *API) bulkRowCode(ctx context.Context, userID, noteID uint64, audit bool) string {
	var err error
	if audit {
		_, _, err = a.RequireNoteRole(ctx, userID, noteID, store.RoleEditor)
	} else {
		var n *store.Note
		if n, err = a.notes.ByID(ctx, noteID); err == nil {
			// The same decision chain as RequireNoteRole, just without writing permission.denied (see above).
			_, _, err = a.access.RequireRole(ctx, n.DeckID, userID, store.RoleEditor)
		}
	}
	if err == nil {
		return ""
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, auth.ErrDeckNotFound) ||
		asServiceError(err).Code == CodeNotFound {
		// Nonexistent, soft-deleted (ByID does not return soft-deleted rows), or its deck is gone: always not_found.
		return CodeNotFound
	}
	return CodeInsufficientRole
}

// bulkAuditAction maps a bulk action to a stable audit action name (constants defined in store/audit.go).
func bulkAuditAction(action string) string {
	switch action {
	case bulkActionDelete:
		return store.ActionNoteDelete
	case bulkActionAddTags:
		return store.ActionNoteTagAdd
	case bulkActionRemoveTags:
		return store.ActionNoteTagRemove
	default:
		return store.ActionNoteTagSet
	}
}

// uniquePublicIDs deduplicates by first occurrence, preserving request order; the bulk action's response and audit both rely on that order.
// Empty strings are not dropped: they are recorded as not_found during per-row resolution, keeping "as many as were written in the request, so many are reported".
func uniquePublicIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// ---- Stats and export ----

// StatsSummary is the response shape shared by /stats/summary and the get_stats tool.
type StatsSummary struct {
	Decks        int   `json:"decks"`
	Due          int64 `json:"due"`
	ReviewsToday int64 `json:"reviews_today"`
	ReviewsTotal int64 `json:"reviews_total"`
	// Retention is the true retention rate: the share of due reviews (state_before=Review) whose grade is not Again,
	// on the same convention as the stats page (store.StatsStore.RetentionByStability).
	Retention float64 `json:"retention"`
	// RetentionTotal / RetentionPassed are the denominator and numerator of Retention. The denominator counts only due reviews,
	// so it is not reviews_total; when it is 0 the client should show "no data yet" rather than 0%.
	RetentionTotal  int64 `json:"retention_total"`
	RetentionPassed int64 `json:"retention_passed"`
	Notes           int64 `json:"notes"`
	Cards           int64 `json:"cards"`
}

// Stats summarizes the current user's due counts / review counts / retention; every number is aggregated from reviews + card_states.
//
// The convention is deliberately asymmetric (2026-10-06 decision): decks/due/notes/cards count only currently visible
// decks (the same store predicate as list_decks, the web list page and the review queue), and are 0 when the deck set is empty;
// due is the review part of the default review queue (due learning/relearning cards + due review cards within each deck's
// reviews_per_day), taken from schedule.QueueBuilder.DueNow: a hand-written count drifted from the queue and counted
// suspended cards and buried new cards that /review never shows, so the home page announced cards the review page did not have;
// like the default queue it leaves out the decks the user paused, and new cards are not part of it;
// reviews_today / reviews_total / retention keep the full history per user_id - reviews are the person's own record,
// and revoking a grant does not reach back. So even with no visible deck at all, reviews must still be aggregated; returning early would
// silently zero the historical numbers. retention shares RetentionByStability with the stats page: likewise not limited to visible decks,
// but counting only due reviews, and excluding reviews of deleted cards (consistent with the stats page).
func (a *API) Stats(ctx context.Context, u *store.User) (StatsSummary, error) {
	now := a.now()

	deckIDs, err := a.decks.VisibleIDs(ctx, u.ID)
	if err != nil {
		a.logger.Error("load visible decks for stats failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}
	resp := StatsSummary{Decks: len(deckIDs)}
	// due follows the default review queue, so the paused decks are left out while the deck, note and card counts keep them.
	due, err := schedule.NewQueueBuilder(a.db, a.decks, nil).DueNow(ctx, u.ID, schedule.QueueOptions{
		Now: now, Timezone: u.Timezone, DayCutoffHour: u.DayCutoffHour,
	})
	if err != nil {
		a.logger.Error("count due cards failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}

	day := reviewday.Day(now, reviewday.Location(u.Timezone), reviewday.ResolveCutoff(u.DayCutoffHour))
	var reviewsToday int64
	if err := a.db.WithContext(ctx).Model(&store.Review{}).
		Where("user_id = ? AND review_day = ?", u.ID, day).Count(&reviewsToday).Error; err != nil {
		a.logger.Error("count reviews today failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}
	var reviewsTotal int64
	if err := a.db.WithContext(ctx).Model(&store.Review{}).
		Where("user_id = ?", u.ID).Count(&reviewsTotal).Error; err != nil {
		a.logger.Error("count reviews failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}
	// Retention reuses the stats page's aggregation, so the home page and the stats page give the same number for the same user.
	retention, err := store.NewStatsStore(a.db).RetentionByStability(ctx, u.ID, 0)
	if err != nil {
		a.logger.Error("count retention failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}

	var notes, cards int64
	if err := a.db.WithContext(ctx).Model(&store.Note{}).
		Where("deck_id IN ?", deckIDs).Count(&notes).Error; err != nil {
		a.logger.Error("count notes failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}
	if err := a.db.WithContext(ctx).Model(&store.Card{}).
		Joins("JOIN notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs).Where("cards.deleted_at IS NULL").
		Count(&cards).Error; err != nil {
		a.logger.Error("count cards failed", "user_id", u.ID, "error", err)
		return StatsSummary{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load statistics")
	}

	resp.Due = due
	resp.ReviewsToday = reviewsToday
	resp.ReviewsTotal = reviewsTotal
	resp.Notes = notes
	resp.Cards = cards
	resp.Retention = retention.Rate
	resp.RetentionTotal = retention.Total
	resp.RetentionPassed = retention.Passed
	return resp, nil
}

// ExportRow is a flattened card-level export row (the granularity external tools use); all three ids are external ids.
type ExportRow struct {
	CardID      string         `json:"card_id"`
	NoteID      string         `json:"note_id"`
	DeckID      string         `json:"deck_id"`
	Kind        string         `json:"kind"`
	Template    string         `json:"template"`
	Fields      map[string]any `json:"fields"`
	Tags        []string       `json:"tags"`
	ExternalRef *string        `json:"external_ref,omitempty"`
	State       *string        `json:"state,omitempty"`
	DueAt       *time.Time     `json:"due_at,omitempty"`
	Reps        *int           `json:"reps,omitempty"`
	Lapses      *int           `json:"lapses,omitempty"`
}

// ---- Review ----

// DueCard is the external shape of a due card (with the note fields as their stored HTML); all three ids are external ids.
type DueCard struct {
	CardID         string            `json:"card_id"`
	NoteID         string            `json:"note_id"`
	DeckID         string            `json:"deck_id"`
	State          string            `json:"state"`
	DueAt          time.Time         `json:"due_at"`
	Retrievability float64           `json:"retrievability"`
	Kind           string            `json:"kind"`
	Fields         map[string]string `json:"fields"`
	Tags           []string          `json:"tags"`
	Template       string            `json:"template,omitempty"`
	Version        int               `json:"version"`
	// AnswerControl is the control name of the card template's answer spec, or null when the template has none.
	// The review page picks the self-rated, essay or machine-graded flow from it and MachineGraded, never from the kind.
	AnswerControl *string `json:"answer_control"`
	// MachineGraded reports whether the server grades this card's answer, by the same rule the submit path applies.
	MachineGraded bool `json:"machine_graded"`
	// FieldOrder lists the note type's field ids in config order, the order the plain-text fallback shows the values in.
	// The fields map is keyed by field id but keeps no order, so the client cannot recover this order on its own.
	FieldOrder []string `json:"field_order"`
	// OptionOrder is the display order of a choice card's options: slot b shows stored option index OptionOrder[b].
	// It is null for card kinds without option controls, and the submitted answer stays a stored option index.
	OptionOrder []int `json:"option_order"`
}

// reviewMaxTags is the maximum number of tags one tag review may select (after deduplication), bounding the number of OR conditions in the SQL.
const reviewMaxTags = 50

// DueCards returns the due cards (with raw field text); an empty deckIDs means all decks; limit is [1,500].
//
// Every deck id requires at least the reader role: any one unreadable or nonexistent fails the whole call (no silent filtering).
// Only when exactly one deck is given is the scheduler built from that deck's preset; multiple decks and the whole library use the default preset.
//
// When tags is non-empty only cards carrying any one of those tags are taken (normalization in ReviewTags).
func (a *API) DueCards(ctx context.Context, u *store.User, deckIDs []uint64, tags []string, limit int) ([]DueCard, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	ids := dedupeDeckIDs(deckIDs)
	tags, err := ReviewTags(ids, tags)
	if err != nil {
		return nil, err
	}
	var deck *store.Deck
	for _, id := range ids {
		d, err := a.RequireDeckRole(ctx, u.ID, id, store.RoleReader)
		if err != nil {
			return nil, err
		}
		if len(ids) == 1 {
			deck = d
		}
	}

	// Building the queue needs a scheduler (it computes retrievability only for review cards); multiple/no decks use the default preset.
	var sched *schedule.Scheduler
	if deck != nil {
		s, err := a.schedulerForDeck(ctx, u.ID, deck)
		if err != nil {
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load deck scheduler")
		}
		sched = s
	} else {
		presetID, err := a.ensureDefaultPreset(ctx, u.ID)
		if err != nil {
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load default preset")
		}
		preset, err := a.presets.ByID(ctx, presetID)
		if err != nil {
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load default preset")
		}
		s, err := schedule.NewScheduler(preset)
		if err != nil {
			return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to build scheduler")
		}
		sched = s
	}

	builder := schedule.NewQueueBuilder(a.db, a.decks, sched)
	opts := schedule.QueueOptions{
		Now:           a.now(),
		Timezone:      u.Timezone,
		DayCutoffHour: u.DayCutoffHour,
		ReviewOrder:   schedule.OrderByDueAt,
		NewOrder:      schedule.NewOrderRandom,
		LearnAhead:    store.ResolveLearnAhead(u.LearnAheadMinutes),
		Tags:          tags,
	}
	// A single deck goes through DeckID (preserving the deck-cap convention); multiple decks go through the DeckIDs set.
	switch len(ids) {
	case 0:
	case 1:
		opts.DeckID = ids[0]
	default:
		opts.DeckIDs = ids
	}
	items, err := builder.Build(ctx, u.ID, opts)
	if err != nil {
		a.logger.Error("build due queue failed", "user_id", u.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to build review queue")
	}
	if len(items) > limit {
		items = items[:limit]
	}

	out := make([]DueCard, 0, len(items))
	if len(items) == 0 {
		return out, nil
	}

	// All assembly-stage loads batch by id set: one set query each for note / card / card_states / deck (chunked at 200
	// ids per chunk), so the query count is independent of queue length. Per-card ByID would make queries per answer grow linearly with the queue,
	// especially visible with SQLite's single connection.
	noteIDs := make([]uint64, 0, len(items))
	cardIDs := make([]uint64, 0, len(items))
	for _, it := range items {
		noteIDs = append(noteIDs, it.NoteID)
		cardIDs = append(cardIDs, it.CardID)
	}
	notesByID, err := a.notes.ByIDs(ctx, noteIDs)
	if err != nil {
		a.logger.Error("load due queue notes failed", "user_id", u.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to build review queue")
	}
	cardsByID, err := a.cards.ByIDs(ctx, cardIDs)
	if err != nil {
		a.logger.Error("load due queue cards failed", "user_id", u.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to build review queue")
	}
	statesByCard, err := store.CardStatesByCardIDs(ctx, a.db, u.ID, cardIDs)
	if err != nil {
		a.logger.Error("load due queue card states failed", "user_id", u.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to build review queue")
	}

	// Deck external ids: only the decks the queue actually uses (notes are already fetched, cards whose note is missing are dropped), again one set query.
	deckIDsToLoad := make([]uint64, 0, len(items))
	for _, it := range items {
		if note, ok := notesByID[it.NoteID]; ok {
			deckIDsToLoad = append(deckIDsToLoad, note.DeckID)
		}
	}
	decksByID, err := a.decks.ByIDs(ctx, deckIDsToLoad)
	if err != nil {
		a.logger.Error("load due queue decks failed", "user_id", u.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to build review queue")
	}

	types := newNoteTypeCache(a.db)
	for _, it := range items {
		note, ok := notesByID[it.NoteID]
		if !ok {
			// The note is not visible (soft-deleted): consistent with dropping the card when a per-card read fails.
			continue
		}
		view := types.load(ctx, note)
		values := store.NoteFieldValues(note.FieldsJSON)
		entry := DueCard{
			NoteID:         note.PublicID,
			State:          it.State.String(),
			DueAt:          it.DueAt,
			Retrievability: it.Retrievability,
			Kind:           view.kind,
			Fields:         values,
			Tags:           store.TagsOrEmpty(note.TagsJSON),
			FieldOrder:     fieldOrder(view.config),
		}
		// Missing card_states / card / deck stay zero-valued, consistent with the result of a failed per-card read.
		if state, ok := statesByCard[it.CardID]; ok {
			entry.Version = state.Version
		}
		if card, ok := cardsByID[it.CardID]; ok {
			entry.CardID = card.PublicID
			entry.Template = card.Template
		}
		if d, ok := decksByID[note.DeckID]; ok {
			entry.DeckID = d.PublicID
		}
		// The template's answer spec decides how the review page answers this card. The same spec drives the option
		// shuffle: only a control that reads options has a display order.
		if spec, _, err := templateAnswer(view.config, entry.Template); err == nil {
			if spec != nil {
				control := spec.Control
				entry.AnswerControl = &control
			}
			entry.MachineGraded = answer.IsMachineGraded(spec)
			if count := optionCount(spec, values); count > 0 {
				entry.OptionOrder = OptionOrder(count, u.ID, it.CardID, entry.Version)
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// ReviewTags normalizes the tag set for reviewing by tag and validates it against the deck scope; deckIDs must already be deduplicated.
//
// With tags the deck scope must be exactly one deck: tags are categories within a deck, and a same-named tag in different decks is not the same
// category, so they cannot be studied together; hence tags with multiple decks or the whole-library scope are always rejected rather than silently ignoring the tags.
// Empty after normalization (trim, drop empties, deduplicate) equals no tags. All transports share this one validation.
func ReviewTags(deckIDs []uint64, tags []string) ([]string, error) {
	tags = store.NormalizeTags(tags)
	if len(tags) == 0 {
		return nil, nil
	}
	if len(tags) > reviewMaxTags {
		return nil, InvalidRequest(fmt.Sprintf("tags must contain at most %d entries", reviewMaxTags))
	}
	if len(deckIDs) != 1 {
		return nil, InvalidRequest("tags require exactly one deck")
	}
	return tags, nil
}

// DeckTags lists the tags within a deck and their note counts; the caller must be at least reader.
func (a *API) DeckTags(ctx context.Context, userID, deckID uint64) ([]store.TagCount, error) {
	d, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader)
	if err != nil {
		return nil, err
	}
	tags, err := a.notes.DeckTagCounts(ctx, d.ID)
	if err != nil {
		a.logger.Error("list deck tags failed", "deck_id", d.ID, "error", err)
		return nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to list deck tags")
	}
	return tags, nil
}

// dedupeDeckIDs drops duplicate deck ids, preserving first-occurrence order; 0 is not a valid deck id and is dropped as well.
func dedupeDeckIDs(ids []uint64) []uint64 {
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

// SubmitReviewInput is the grade submission input. CardID is the card's external id (an opaque string),
// resolved to a primary key by SubmitReview before it enters scheduling.
//
// The grade source is decided by the server and cannot be set by the caller: a template without a grading answer control
// takes Rating; a template whose answer control grades sends Answer for server-side grading, or GiveUp to abandon the
// answer (recorded as Again). The self-assessed short answer control accepts an Answer alongside Rating and records it,
// without letting it affect the grade.
type SubmitReviewInput struct {
	CardID string
	// Rating is the self-grade (1-4), used only for a template without machine grading; for a graded template it must be 0.
	Rating int
	// Answer is the raw answer of a graded template (JSON), decoded by its answer control. A self-assessed template that
	// records a written answer (short answer) accepts it too, archived with the self-grade.
	Answer json.RawMessage
	// GiveUp means a graded template abandons the answer: no grading, a self-assessed log entry is recorded as Again.
	GiveUp          bool
	ExpectedVersion int
	ElapsedMS       *int
}

// SubmitReviewResult is the response shape of a grade submission. CardID is the card's external id;
// the review's auto-increment primary key is not exposed (no client uses it).
type SubmitReviewResult struct {
	CardID    string     `json:"card_id"`
	State     string     `json:"state"`
	DueAt     *time.Time `json:"due_at"`
	Version   int        `json:"version"`
	Stability *float64   `json:"stability"`
	// Grade appears only with server-side grading: the score, rating band and verdict of this answer.
	Grade *GradeResult `json:"grade,omitempty"`
}

// GradeResult is the external result of one server-side grading; Detail is the full detail written to the review log, for server-internal use only.
type GradeResult struct {
	Rating  int            `json:"rating"`
	Score   float64        `json:"score"`
	Verdict string         `json:"verdict"`
	Given   string         `json:"given"`
	Detail  map[string]any `json:"-"`
}

// SubmitReview submits one grade; an optimistic-lock mismatch returns 409 version_conflict.
//
// The self-graded and the answer paths both go through here, so the three entries (REST, MCP and web) obey one set of rules:
// answer card types do not accept self-grades (that would hand grading back to the client), grade_source is written by the server,
// and the client cannot forge a "machine grading" source.
func (a *API) SubmitReview(ctx context.Context, u *store.User, apiKeyID *uint64, in SubmitReviewInput) (SubmitReviewResult, error) {
	if strings.TrimSpace(in.CardID) == "" {
		return SubmitReviewResult{}, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "card_id is required")
	}
	// External id -> primary key: everything after this is numeric logic (scheduling, auditing, card_states queries).
	card, err := a.cards.ByPublicID(ctx, in.CardID)
	if err != nil {
		return SubmitReviewResult{}, newServiceError(http.StatusNotFound, CodeNotFound, "card not found")
	}
	note, err := a.notes.ByID(ctx, card.NoteID)
	if err != nil {
		return SubmitReviewResult{}, newServiceError(http.StatusNotFound, CodeNotFound, "note not found")
	}
	deck, err := a.RequireDeckRole(ctx, u.ID, note.DeckID, store.RoleReader)
	if err != nil {
		return SubmitReviewResult{}, err
	}
	preset, err := a.presetForDeck(ctx, u.ID, deck)
	if err != nil {
		return SubmitReviewResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load deck preset")
	}
	sched, err := schedule.NewScheduler(preset)
	if err != nil {
		return SubmitReviewResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to load deck scheduler")
	}

	rating, source, detailJSON, grade, err := a.resolveRating(ctx, card, note, preset, in)
	if err != nil {
		return SubmitReviewResult{}, err
	}

	var result schedule.SubmitResult
	err = a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inner error
		result, inner = schedule.Submit(ctx, tx, schedule.SubmitInput{
			CardID:          card.ID,
			UserID:          u.ID,
			Rating:          schedule.Rating(rating),
			ExpectedVersion: in.ExpectedVersion,
			ElapsedMS:       in.ElapsedMS,
			GradeSource:     source,
			GradeDetailJSON: detailJSON,
			Scheduler:       sched,
			Now:             a.now(),
			Location:        reviewday.Location(u.Timezone),
			Timezone:        u.Timezone,
			DayCutoffHour:   u.DayCutoffHour,
		})
		return inner
	})
	if err != nil {
		if errors.Is(err, schedule.ErrVersionConflict) {
			return SubmitReviewResult{}, newServiceError(http.StatusConflict, CodeVersionConflict, "card state version conflict")
		}
		a.logger.Error("submit review failed", "card_id", card.ID, "user_id", u.ID, "error", err)
		return SubmitReviewResult{}, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to submit review")
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   apiKeyID,
		Action:     "review.submit",
		TargetType: "card",
		TargetID:   store.Ptr(card.ID),
		Detail:     map[string]any{"rating": rating, "grade_source": source, "review_id": result.ReviewID},
	})
	return SubmitReviewResult{
		CardID:    card.PublicID,
		State:     result.State.State,
		DueAt:     result.State.DueAt,
		Version:   result.State.Version,
		Stability: result.State.Stability,
		Grade:     grade,
	}, nil
}

// resolveRating decides the grade, source and grading detail written by this submission (rules in SubmitReview).
//
// The template's answer spec decides the path: no spec, or the self-assessed short answer control, takes the four-grade
// self-assessment; every other control grades the submitted answer and the preset's mapping turns its score into a rating.
// A machine-graded template refuses a self-grade, so the client cannot hand the grading back to itself.
func (a *API) resolveRating(ctx context.Context, card *store.Card, note *store.Note, preset *store.Preset, in SubmitReviewInput) (int, string, *string, *GradeResult, error) {
	_, cfg, err := a.notes.NoteTypeFor(ctx, note)
	if err != nil {
		return 0, "", nil, nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to read the note type")
	}
	spec, clozeOrdinal, err := templateAnswer(cfg, card.Template)
	if err != nil {
		return 0, "", nil, nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to resolve the card template")
	}
	hasAnswer := len(bytes.TrimSpace(in.Answer)) > 0

	if !answer.IsMachineGraded(spec) {
		if in.GiveUp {
			return 0, "", nil, nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "give_up applies only to templates graded by the server")
		}
		if !schedule.Rating(in.Rating).Valid() {
			return 0, "", nil, nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "rating must be between 1 and 4")
		}
		if !hasAnswer {
			return in.Rating, schedule.GradeSourceSelf, nil, nil, nil
		}
		if spec == nil {
			return 0, "", nil, nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "answer applies only to a template that grades or records it")
		}
		control, ok := answer.Lookup(spec.Control)
		if !ok {
			return 0, "", nil, nil, newServiceError(http.StatusInternalServerError, CodeInternal, "unknown answer control")
		}
		values := store.NoteFieldValues(note.FieldsJSON)
		bindings, err := answer.ResolveBindings(*spec, values)
		if err != nil {
			return 0, "", nil, nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to read the answer bindings")
		}
		result, err := control.Grade(answer.GradeContext{Bindings: bindings, Answer: in.Answer, ClozeOrdinal: clozeOrdinal})
		if err != nil {
			return 0, "", nil, nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "the answer cannot be recorded")
		}
		// A written answer that was blank leaves no detail: the old record stored nothing at all, and an empty
		// self-assessment marker carries no information.
		if len(result.Detail) == 1 && result.Detail["self_assessed"] == true {
			return in.Rating, schedule.GradeSourceSelf, nil, nil, nil
		}
		raw, err := json.Marshal(result.Detail)
		if err != nil {
			return 0, "", nil, nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to encode grade detail")
		}
		detail := string(raw)
		return in.Rating, schedule.GradeSourceSelf, &detail, nil, nil
	}
	if in.Rating != 0 {
		return 0, "", nil, nil, newServiceError(http.StatusBadRequest, CodeGradingRequired, "this template is graded by the server: send answer or give_up instead of rating")
	}
	if in.GiveUp {
		if hasAnswer {
			return 0, "", nil, nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "send either answer or give_up, not both")
		}
		// Abandoning the answer involves no machine grading at all: record Again with source self.
		return int(schedule.Again), schedule.GradeSourceSelf, nil, nil, nil
	}
	control, ok := answer.Lookup(spec.Control)
	if !ok {
		return 0, "", nil, nil, newServiceError(http.StatusInternalServerError, CodeInternal, "unknown answer control")
	}
	values := store.NoteFieldValues(note.FieldsJSON)
	bindings, err := answer.ResolveBindings(*spec, values)
	if err != nil {
		return 0, "", nil, nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to read the answer bindings")
	}
	gc := answer.GradeContext{
		Bindings:     bindings,
		Answer:       in.Answer,
		Mapping:      gradeMappingFor(preset),
		ClozeOrdinal: clozeOrdinal,
	}
	out, err := control.Grade(gc)
	if err != nil {
		// A decode failure and an ungradable answer both write nothing, letting the user answer again.
		return 0, "", nil, nil, newServiceError(http.StatusBadRequest, CodeInvalidRequest, "the answer cannot be graded")
	}
	if out.Rating < int(schedule.Again) || out.Rating > int(schedule.Easy) {
		return 0, "", nil, nil, newServiceError(http.StatusInternalServerError, CodeInternal, "the answer control returned no rating")
	}
	raw, err := json.Marshal(out.Detail)
	if err != nil {
		return 0, "", nil, nil, newServiceError(http.StatusInternalServerError, CodeInternal, "failed to encode grade detail")
	}
	detail := string(raw)
	return out.Rating, schedule.GradeSourceTyped, &detail, &GradeResult{
		Rating: out.Rating, Score: out.Score, Verdict: verdictFor(out.Score), Given: out.Given, Detail: out.Detail,
	}, nil
}

// verdictFor names the outcome of a machine grade in the stable English words the SPA maps to its own copy.
func verdictFor(score float64) string {
	switch {
	case score >= 1:
		return "correct"
	case score <= 0:
		return "incorrect"
	default:
		return "partial"
	}
}
