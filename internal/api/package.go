package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// REST entry points for deck packages (.edeck):
//   GET  /api/v1/decks/:id/package  export (read)
//   POST /api/v1/decks/import       import (write)
// All the logic is in the store layer; this only shapes parameters, decides permissions and maps errors.

// ExportDeckPackage exports a deck package and returns the in-memory form; shared by REST, MCP and the CLI.
// Callers only set the Include* switches; MediaRoot and the clock are decided by the server and any value passed in is overwritten.
func (a *API) ExportDeckPackage(ctx context.Context, userID, deckID uint64, opts store.PackageOptions) (*store.DeckPackage, error) {
	if _, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader); err != nil {
		return nil, err
	}
	opts.MediaRoot = a.mediaRoot
	opts.Now = a.now
	pkg, err := a.decks.ExportPackage(ctx, userID, deckID, opts)
	if err != nil {
		return nil, mapPackageError(err)
	}
	return pkg, nil
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
	// Hand the importer's currently effective media quota to the store layer; all four entries (REST/MCP/CLI/Web) go through this method,
	// so they share one resolution and nobody can bypass the quota check.
	opts.MediaQuotaBytes = media.ResolveUserQuotaBytes(ctx, a.db)
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
	return newServiceError(http.StatusInternalServerError, CodeInternal, err.Error())
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
	pkg, err := a.ExportDeckPackage(ctx, u.ID, d.ID, store.PackageOptions{
		IncludeProgress: c.Query("include_progress") == "1",
		IncludeReviews:  c.Query("include_reviews") == "1",
		IncludeWeights:  c.Query("include_weights") == "1",
		// include_media defaults to on; passing 0 explicitly turns it off.
		IncludeMedia: c.DefaultQuery("include_media", "1") != "0",
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var buf bytes.Buffer
	if err := pkg.WriteZip(&buf); err != nil {
		a.logger.Error("write deck package failed", "deck_id", d.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "")
		return
	}
	// The download name uses the deck title so users recognize the deck in their download folder; when the title sanitizes to nothing it falls back to the external id.
	c.Header("Content-Type", "application/vnd.engram.edeck")
	c.Header("Content-Disposition", AttachmentDisposition(DeckPackageFilename(d.Name, publicID)))
	c.Data(http.StatusOK, "application/vnd.engram.edeck", buf.Bytes())
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

// handleImportPackage is POST /api/v1/decks/import (multipart file upload).
func (a *API) handleImportPackage(c *gin.Context) {
	u, _ := CurrentUser(c)
	// Cap the request body at the admin-configured upload limit before parsing multipart: gin spills parts beyond MaxMultipartMemory
	// to a temp file, so setting no limit first would invite memory/disk amplification.
	// Over the limit is a stable 413, reusing the deck package's existing package_too_large code.
	limit := media.ResolveMaxBytes(c.Request.Context(), a.db)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	fh, err := c.FormFile("file")
	if err != nil {
		if isRequestBodyTooLarge(err) {
			abortError(c, http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, "")
			return
		}
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	f, err := fh.Open()
	if err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	defer f.Close()

	dryRun := c.PostForm("dry_run") == "1" || c.PostForm("dry_run") == "true"
	report, err := a.ImportDeckPackage(c.Request.Context(), u, CurrentAPIKeyID(c), f, store.PackageImportOptions{
		Target:     c.PostForm("target"),
		DryRun:     dryRun,
		OnConflict: c.PostForm("on_conflict"),
		// Allowing import of someone else's progress is an admin setting; only an explicit admin opt-in counts here.
		AllowOthersProgress: c.PostForm("allow_others_progress") == "1" && u.Role == store.RoleAdmin,
		SkipMissingMedia:    c.PostForm("skip_missing_media") == "1",
		ApplyWeights:        c.PostForm("apply_weights") == "1",
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

// formatTargetID is used inline by strconv, no separate function is needed.
