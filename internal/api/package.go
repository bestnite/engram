package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// REST entry points for deck packages:
//   GET  /api/v1/decks/:id/package  export (read)
//   POST /api/v1/decks/import       import (write)
// All the logic is in the store layer; this only shapes parameters, decides permissions and maps errors.

// ExportDeckPackage applies permission checks and server settings for Anki archives.
func (a *API) ExportDeckPackage(ctx context.Context, userID, deckID uint64, opts store.PackageOptions) ([]byte, error) {
	if _, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader); err != nil {
		return nil, err
	}
	opts.MediaRoot, opts.Now = a.mediaRoot, a.now
	data, err := a.decks.ExportDeckPackage(ctx, userID, deckID, opts)
	if err != nil {
		return nil, mapPackageError(err)
	}
	return data, nil
}

// PackageQueryOptions applies the transport defaults to export switches.
func PackageQueryOptions(c *gin.Context) store.PackageOptions {
	configs := c.DefaultQuery("include_deck_configs", "1") != "0"
	engramMeta := c.DefaultQuery("include_engram_meta", "1") != "0"
	return store.PackageOptions{IncludeProgress: c.Query("include_progress") == "1", IncludeReviews: c.Query("include_reviews") == "1", IncludeMedia: c.DefaultQuery("include_media", "1") != "0", IncludeWeights: c.Query("include_weights") == "1", IncludeDeckConfigs: &configs, IncludeEngramMeta: &engramMeta}
}

// ImportDeckPackage parses and imports one deck package; target picks one of three destinations.
// allowOthersProgress only takes effect when the caller is an admin (the progress-import boundary).
func (a *API) ImportDeckPackage(ctx context.Context, u *store.User, apiKeyID *uint64, r io.Reader, opts store.PackageImportOptions) (*store.PackageImportReport, error) {
	target, err := a.authorizeImportTarget(ctx, u.ID, opts.Target)
	if err != nil {
		return nil, err
	}
	opts.Target = target
	opts.MediaRoot = a.mediaRoot
	// Hand the importer's currently effective media quota (their own setting, else the instance-wide one) to the store
	// layer; all four entries (REST/MCP/CLI/Web) go through this method, so they share one resolution and nobody can
	// bypass the quota check.
	opts.MediaQuotaBytes = media.UserQuotaBytes(ctx, a.db, u)
	// The package caps are deployment settings resolved once per import, the same way the media
	// quota is: every transport that reaches this method uses the same values.
	opts.Limits = store.ResolvePackageLimits(ctx, a.db)
	if opts.Now == nil {
		opts.Now = a.now
	}
	if u.Role != store.RoleAdmin {
		// A non-admin can never import someone else's progress (the other half of the admin setting is set by the caller after reading settings).
		opts.AllowOthersProgress = false
	}
	report, err := a.decks.ImportPackage(ctx, u.ID, r, opts)
	if err != nil {
		return nil, mapPackageError(err)
	}
	if len(report.ReservedPrefixFields) > 0 {
		loc := i18n.FromContext(ctx)
		if loc == nil {
			translator, err := i18n.New()
			if err != nil {
				return nil, fmt.Errorf("load translations: %w", err)
			}
			loc = translator.Localizer(language.English)
		}
		report.ReservedPrefixNotice = loc.T("m20.reserved_prefix_import")
	}
	a.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   apiKeyID,
		Action:     "deck.package_import",
		TargetType: "deck",
		TargetID:   store.Ptr(report.DeckID),
		Detail: map[string]any{
			"target": report.Target, "dry_run": report.DryRun,
			"notes_created": report.NotesCreated, "notes_updated": report.NotesUpdated,
			"progress_applied": report.ProgressApplied, "progress_discarded": report.ProgressDiscarded,
			"states_skipped": report.StatesSkipped, "states_discarded": report.StatesDiscarded,
			"reviews_applied": report.ReviewsApplied, "reviews_skipped": report.ReviewsSkipped, "reviews_discarded": report.ReviewsDiscarded,
		},
	})
	return report, nil
}

// resolveImportTarget translates the target string the client sends into the store's shape.
// The client's deck identifier is an external id and the store only knows numeric primary keys; returns the kind, the target deck's numeric primary key
// (0 for new_deck) and the target string in the store's shape.
func (a *API) resolveImportTarget(ctx context.Context, raw string) (kind string, deckID uint64, target string, err error) {
	kind, ref, err := store.ParsePackageTargetRef(raw)
	if err != nil {
		return "", 0, "", mapPackageError(err)
	}
	if kind == store.PackageTargetNewDeck {
		return kind, 0, store.FormatPackageTarget(kind, 0), nil
	}
	d, derr := a.decks.ByPublicID(ctx, ref)
	if derr != nil {
		return "", 0, "", newServiceError(http.StatusNotFound, CodeNotFound, "deck not found")
	}
	return kind, d.ID, store.FormatPackageTarget(kind, d.ID), nil
}

// authorizeImportTarget resolves the import target and decides the caller's role on the target deck, returning a target string the store understands.
// The client's deck identifier is an external id and the store only knows numeric primary keys, hence the resolution here.
// Merging (into_deck) needs editor; replacing (replace_deck) is destructive and is allowed for owner only.
// File import, direct-link import and upload ticket issuance share this function, so the permission rules exist in exactly one place.
func (a *API) authorizeImportTarget(ctx context.Context, userID uint64, raw string) (string, error) {
	kind, deckID, target, err := a.resolveImportTarget(ctx, raw)
	if err != nil {
		return "", err
	}
	if deckID != 0 {
		want := store.RoleEditor
		if kind == "replace_deck" {
			want = store.RoleOwner
		}
		if _, err := a.RequireDeckRole(ctx, userID, deckID, want); err != nil {
			return "", err
		}
	}
	return target, nil
}

// mapPackageError maps a store-layer deck package error to a ServiceError carrying a stable code.
//
// Each package error yields its own stable code (passing store.PackageError.Code through untouched); package_bad_format is no longer
// collapsed into a blanket invalid_request - otherwise the error.package_* i18n keys would
// never be produced, and users could not tell "bad format" / "card type unsupported" / "media type not allowed" apart.
// HTTP statuses keep their existing semantics: quota exceeded 413, referencing unreadable media 403, any other package error 400.
func mapPackageError(err error) error {
	var pe *store.PackageError
	if asPackageError(err, &pe) {
		status := http.StatusBadRequest
		switch pe.Code {
		case store.CodePackageQuotaExceeded:
			// Same HTTP semantics as media_quota_exceeded in the upload chain: quota exceeded is returned as 413.
			status = http.StatusRequestEntityTooLarge
		case store.CodePackageMediaForbidden:
			// Same semantics as media_not_readable for single-card writes: referencing media you cannot read is rejected with 403.
			status = http.StatusForbidden
		}
		code := pe.Code
		if code == "" {
			// Fallback: a package error without a code keeps the existing invalid_request convention.
			code = CodeInvalidRequest
		}
		msg := pe.Message
		if len(pe.Entries) > 0 {
			msg = msg + ": " + strings.Join(pe.Entries, ", ")
		}
		return newServiceError(status, code, msg)
	}
	if err == nil {
		return nil
	}
	// An uncoded error here is a server-side failure; its text may carry a path or a driver
	// diagnostic, so it is logged by writeServiceError and never reaches the response body.
	return newServiceError(http.StatusInternalServerError, CodeInternal, "")
}

// asPackageError is a thin wrapper over errors.As, so the api package does not repeat its import of errors everywhere.
func asPackageError(err error, target **store.PackageError) bool {
	for err != nil {
		if pe, ok := err.(*store.PackageError); ok {
			*target = pe
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// handleExportPackage is GET /api/v1/decks/:id/package.
func (a *API) handleExportPackage(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	publicID, ok := pathPublicID(c, "id")
	if !ok {
		return
	}
	d, err := a.decks.ByPublicID(ctx, publicID)
	if err != nil {
		abortNotFound(c)
		return
	}
	if format := c.DefaultQuery("format", "apkg"); format != "apkg" {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	data, err := a.ExportDeckPackage(ctx, u.ID, d.ID, PackageQueryOptions(c))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Content-Disposition", AttachmentDisposition(DeckPackageFilename(d.Name, publicID)))
	c.Data(http.StatusOK, "application/zip", data)
}

// isRequestBodyTooLarge reports whether a multipart parse failure was triggered by http.MaxBytesReader.
// gin wraps the MaxBytesError inside the parse error, and errors.As can see through the wrapping.
func isRequestBodyTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return true
	}
	return strings.Contains(err.Error(), "http: request body too large")
}

// importMultipartOverhead is the part headers and small form fields allowed above package_max_bytes
// on the multipart import; the archive itself is still capped at exactly package_max_bytes by the store.
const importMultipartOverhead = 1 << 20

// handleImportPackage is POST /api/v1/decks/import (multipart file upload).
//
// The multipart contract is fields first, archive last. The handler reads the field parts, resolves
// the preset and authorizes the target before it consumes the file part; only then does the store
// spool the archive. A refused import therefore reads no archive bytes, and a file part that arrives
// before any field part is rejected instead of silently importing into the new_deck defaults.
// Field parts appearing after the file part are ignored.
//
// The body is bounded at package_max_bytes plus importMultipartOverhead so the field parts cannot
// stream through the parser without limit; the store still caps the spooled archive at exactly
// package_max_bytes (the package cap, not the single-media upload cap) and answers package_too_large.
// Keeping the archive cap in the store means the transport can stop reading an unreadable or
// unauthorized archive early.
func (a *API) handleImportPackage(c *gin.Context) {
	u, _ := CurrentUser(c)
	// Take the import slot before the body is touched: a refused request must read and spool nothing.
	release, err := a.ImportSlot(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	defer release()

	// Bound the whole body: the store caps the archive it spools, but the field parts and the
	// multipart framing are read here, so the package cap plus a fixed overhead keeps an unbounded
	// body from streaming through the parser. The store's own cap still decides the archive's 413;
	// this outer bound only refuses a body larger than the package could ever be.
	limit := store.ResolvePackageLimits(c.Request.Context(), a.db).MaxPackageBytes
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit+importMultipartOverhead)

	reader, err := c.Request.MultipartReader()
	if err != nil {
		if isRequestBodyTooLarge(err) {
			abortError(c, http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, "")
			return
		}
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	// Collect the first value of each field part, matching Request.PostForm's first-value-wins rule.
	// A field is capped so a caller cannot make the handler buffer an unbounded text part.
	const maxFieldBytes = 64 << 10
	fields := make(map[string]string)
	var archive io.Reader
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if isRequestBodyTooLarge(err) {
				abortError(c, http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, "")
				return
			}
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
			return
		}
		if part.FormName() != "file" {
			if _, seen := fields[part.FormName()]; !seen {
				value, err := io.ReadAll(io.LimitReader(part, maxFieldBytes+1))
				part.Close()
				if err != nil {
					if isRequestBodyTooLarge(err) {
						abortError(c, http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, "")
						return
					}
					abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
					return
				}
				if len(value) > maxFieldBytes {
					abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
					return
				}
				fields[part.FormName()] = string(value)
			} else {
				part.Close()
			}
			continue
		}
		if len(fields) == 0 {
			// A file-first request carries no target or preset yet; falling back to the new_deck
			// defaults would import the archive the caller may have meant to merge elsewhere.
			part.Close()
			abortError(c, http.StatusBadRequest, CodeInvalidRequest, "form fields must precede the file part")
			return
		}
		archive = part
		break
	}
	if archive == nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}

	dryRun := fields["dry_run"] == "1" || fields["dry_run"] == "true"
	presetID, err := a.ImportPresetID(c.Request.Context(), u.ID, fields["preset_id"])
	if err != nil {
		writeServiceError(c, err)
		return
	}
	// ImportDeckPackage authorizes the target before the store reads the archive part, so an
	// unauthorized import returns its 404/403 without spooling the package.
	report, err := a.ImportDeckPackage(c.Request.Context(), u, CurrentAPIKeyID(c), archive, store.PackageImportOptions{
		PresetMode: fields["preset_mode"], PresetID: presetID,
		Target:     fields["target"],
		DryRun:     dryRun,
		OnConflict: fields["on_conflict"],
		// Allowing import of someone else's progress is an admin setting; only an explicit admin opt-in counts here.
		AllowOthersProgress: fields["allow_others_progress"] == "1" && u.Role == store.RoleAdmin,
		IncludeProgress:     fields["include_progress"] == "1",
		SkipMissingMedia:    fields["skip_missing_media"] == "1",
		ApplyWeights:        fields["apply_weights"] == "1",
	})
	if err != nil {
		// The body cap moved from the transport's MaxBytesReader to the store's spool. package_too_large
		// keeps its 413 for this route even though mapPackageError's default status is 400.
		if se := asServiceError(err); se.Code == store.CodePackageTooLarge {
			abortError(c, http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, se.Message)
			return
		}
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

// importSlotGate bounds how many package imports run at the same time in one server process.
//
// Each import may spool a package body of up to package_max_bytes to a temporary file before it
// parses anything, so a few simultaneous imports can fill the temporary disk. The gate never
// waits: a request that finds no free slot is refused at once, because waiting would hold the
// connection while a multi-gigabyte import finishes.
type importSlotGate struct {
	mu      sync.Mutex
	running int
}

// acquire takes one slot when fewer than limit imports hold one, and reports whether it did.
// A limit below 1 counts as 1: the setting may be edited at runtime and the gate must stay a gate.
func (g *importSlotGate) acquire(limit int) bool {
	if limit < 1 {
		limit = 1
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running >= limit {
		return false
	}
	g.running++
	return true
}

// release returns one slot to the gate.
func (g *importSlotGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running > 0 {
		g.running--
	}
}

// ImportSlot takes one package-import slot and returns the function that releases it.
//
// Every transport that can start an import calls this before it reads the request body or starts
// a download; the returned function must be deferred so a panic also releases the slot. When no
// slot is free the error is 429 deck_import_busy and names the configured limit.
func (a *API) ImportSlot(ctx context.Context) (func(), error) {
	limit := store.ResolvePackageMaxConcurrentImports(ctx, a.db)
	if !a.importSlots.acquire(limit) {
		return nil, newServiceError(http.StatusTooManyRequests, CodeDeckImportBusy,
			fmt.Sprintf("the configured limit is %d", limit))
	}
	var once sync.Once
	return func() { once.Do(a.importSlots.release) }, nil
}

// formatTargetID is used inline by strconv, no separate function is needed.
