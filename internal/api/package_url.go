package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/urlfetch"
)

// Importing a deck package from a public HTTPS direct link (POST /api/v1/decks/import-url).
//
// The only difference from file import is "where the package bytes come from": once downloaded they all go to the same ImportDeckPackage,
// so package parsing, the zip cap, media quota, progress ownership and auditing exist in one implementation only. The download itself is done by the leaf package
// internal/urlfetch (address validation, public-address pinning, byte cap); this file only shapes parameters and maps errors.

// PackageFetcher downloads a deck package direct link; see internal/urlfetch for the implementation, tests inject a controlled one.
type PackageFetcher interface {
	Fetch(ctx context.Context, rawURL string, maxBytes int64) (io.ReadCloser, error)
}

const (
	// importURLBodyLimit is the cap on the import-url body: it holds one short JSON, a few KB is plenty.
	// Limit the body before parsing so malformed/oversized JSON is not read wholly into memory.
	importURLBodyLimit = 64 << 10
	// importURLRateLimit is the number of direct-link imports allowed per user within the window.
	// It is stricter than the per-key write limit: this entry makes the server issue an outbound request, costlier than an ordinary write.
	importURLRateLimit  = 10
	importURLRateWindow = time.Minute
)

// importURLRequest is the body of POST /api/v1/decks/import-url.
// Its fields map one-to-one onto the file import options, only the package bytes are replaced by a URL.
type importURLRequest struct {
	URL                 string `json:"url"`
	Target              string `json:"target"`
	DryRun              bool   `json:"dry_run"`
	OnConflict          string `json:"on_conflict"`
	AllowOthersProgress bool   `json:"allow_others_progress"`
	SkipMissingMedia    bool   `json:"skip_missing_media"`
	ApplyWeights        bool   `json:"apply_weights"`
}

// ImportDeckPackageURL downloads a deck package from a public HTTPS direct link and imports it.
//
// Order matters: resolve the target deck and authorize first, and only start the network download once the request is known not to be rejected. Downloading before authorizing
// would let unauthorized users turn this entry into an outbound pivot for the server. After the download it reuses ImportDeckPackage, which resolves the target
// once more and authorizes once more - redundant but idempotent, in exchange for the invariant "authorization precedes fetching bytes".
func (a *API) ImportDeckPackageURL(ctx context.Context, u *store.User, apiKeyID *uint64, rawURL string, opts store.PackageImportOptions) (*store.PackageImportReport, error) {
	// Target deck permission: the same check as file import, and before the download.
	if _, err := a.authorizeImportTarget(ctx, u.ID, opts.Target); err != nil {
		return nil, err
	}
	// Per-user rate limiting: REST and MCP both go through this method and share one per-user counting pool. Placed after authorization and
	// before the download, so "an unauthorized request spends no quota" and "a request with exhausted quota never starts a download" both hold.
	if !a.importURL.Allow(urlImportLimitKey(u.ID)) {
		return nil, newServiceError(http.StatusTooManyRequests, CodeRateLimited, "")
	}
	// Download: address shape, public-address pinning and byte cap all live in the fetcher; the cap comes from the same source as file import.
	limit := media.ResolveMaxBytes(ctx, a.db)
	body, err := a.fetcher.Fetch(ctx, rawURL, limit)
	if err != nil {
		return nil, mapFetchError(err)
	}
	defer body.Close()
	return a.ImportDeckPackage(ctx, u, apiKeyID, body, opts)
}

// mapFetchError maps a download error to a ServiceError carrying a stable code.
//
// Each failure yields its own code (invalid URL / non-public address / download failed / not a package),
// while over-limit reuses file import's package_too_large (413), keeping the "size cap" consistent in both places.
func mapFetchError(err error) error {
	var fe *urlfetch.Error
	if errors.As(err, &fe) {
		switch fe.Kind {
		case urlfetch.KindInvalidURL:
			return newServiceError(http.StatusBadRequest, CodeImportURLInvalid, "")
		case urlfetch.KindBlockedHost:
			return newServiceError(http.StatusBadRequest, CodeImportURLBlocked, "")
		case urlfetch.KindNotPackage:
			return newServiceError(http.StatusBadRequest, CodeImportURLNotPackage, "")
		case urlfetch.KindTooLarge:
			return newServiceError(http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, "")
		case urlfetch.KindFetchFailed:
			return newServiceError(http.StatusBadGateway, CodeImportURLFetchFailed, "")
		}
	}
	return newServiceError(http.StatusBadGateway, CodeImportURLFetchFailed, "")
}

// handleImportPackageURL is POST /api/v1/decks/import-url (application/json).
// scope and CSRF are handled by the Auth/RequireScope middleware on the route, exactly as for file import.
func (a *API) handleImportPackageURL(c *gin.Context) {
	u, _ := CurrentUser(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, importURLBodyLimit)
	var req importURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if isRequestBodyTooLarge(err) {
			abortError(c, http.StatusRequestEntityTooLarge, CodeInvalidRequest, "request body too large")
			return
		}
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	report, err := a.ImportDeckPackageURL(c.Request.Context(), u, CurrentAPIKeyID(c), req.URL, store.PackageImportOptions{
		Target:     req.Target,
		DryRun:     req.DryRun,
		OnConflict: req.OnConflict,
		// Allowing import of someone else's progress is an admin setting; only an explicit admin opt-in counts here.
		AllowOthersProgress: req.AllowOthersProgress && u.Role == store.RoleAdmin,
		SkipMissingMedia:    req.SkipMissingMedia,
		ApplyWeights:        req.ApplyWeights,
	})
	if err != nil {
		// Rate limiting fires in the service layer (REST and MCP share the same counting pool); all this does is put the 429's
		// Retry-After header back on the response, telling the client to retry after the window length.
		if se := asServiceError(err); se.Code == CodeRateLimited {
			c.Header("Retry-After", strconv.Itoa(int(importURLRateWindow.Seconds())))
		}
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}

// urlImportLimitKey is the bucket key for direct-link import rate limiting: normalized per user so REST and MCP land on the same key.
func urlImportLimitKey(userID uint64) string {
	return "user:" + strconv.FormatUint(userID, 10)
}
