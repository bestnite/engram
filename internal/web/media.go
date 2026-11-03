package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/mediatype"
	"git.nite07.com/nite/engram/internal/store"
)

// Media-related system setting keys and their environment-variable override names.
// Environment variable > settings table > built-in default, the same precedence as internal/config; the settings
// table is read directly here so an admin change takes effect immediately, with no restart.
const (
	// settingKeyMediaUserQuotaBytes is the per-user total media quota (bytes); 0/unconfigured = unlimited.
	// Together with envMediaUserQuotaBytes it points at the same parsing in internal/media (the upload and import chains share it).
	settingKeyMediaUserQuotaBytes = media.SettingKeyMediaUserQuotaBytes
	envMediaUserQuotaBytes        = media.EnvMediaUserQuotaBytes
)

// registerMediaRoutes mounts media upload and reading. Skipped when the dependency is not wired up.
func (s *Server) registerMediaRoutes(router *gin.Engine) {
	if s.sessions == nil || s.media == nil {
		return
	}
	// Upload is a write, so it goes through the CSRF middleware; reading requires sign-in plus the deck-access predicate in mediaServe
	// (a media_uploaders record, a note in a deck you can see, or an L3 share session).
	router.POST("/media", s.sessions.CSRFMiddleware(), s.mediaUpload)
	router.GET("/media/:sha", s.mediaServe)
	// The in-deck upload entry the editor uses. Writing requires the editor role on the deck, so a reader cannot push media
	// into someone else's deck; the permission check shares auth.DeckAccess with the other write paths (a single implementation).
	if s.access != nil {
		router.POST("/decks/:id/media", s.sessions.CSRFMiddleware(), s.deckMediaUpload)
		// The media picker's insert entry, mounted under /api/v1 because it answers the SPA's JSON call rather than a form:
		// it registers a name for a medium that is already stored, which is a deck write and needs the same editor role
		// and CSRF protection as an upload.
		router.POST("/api/v1/decks/:id/media/names", s.sessions.CSRFMiddleware(), s.deckMediaNameRegister)
	}
}

// codeMediaNotFound is the stable code for naming a sha256 that has no media row.
//
// The editor maps a refusal to localized copy from the stable code; the English text below is what a caller that reads the
// API message directly is shown.
const codeMediaNotFound = "media_not_found"

// mediaNotFoundMessage is the stable English text for codeMediaNotFound.
const mediaNotFoundMessage = "The media file was not found."

// deckMediaNameRegister is the media picker's insert entry: it registers the deck-scoped name of an already stored medium
// and returns that name, without copying the bytes again.
//
// The name is the same content-addressed "<sha256>.<ext>" the upload path registers, so a field inserted from the picker
// saves, renders and resolves exactly like one uploaded into the deck. Registering a name is a write to the deck, so it
// requires the editor role, the same check as the upload entry above.
//
// A caller who cannot read the medium is refused with media_not_readable before any row is written: the note write path
// would reject the same reference later, and refusing here keeps the two decisions identical instead of creating a name
// the caller could never save.
//
// The request is idempotent: the canonical name is looked up by (deck, name) and written once, so a repeated request
// returns the same name. An imported alias for the same bytes is untouched — the canonical name is an extra media_names
// row, never a rename or a repoint of the existing one.
func (s *Server) deckMediaNameRegister(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || user.Status != store.StatusActive {
		writeMediaError(c, http.StatusUnauthorized, api.CodeUnauthorized, api.ErrorMessage(c.Request.Context(), api.CodeUnauthorized))
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	ctx := c.Request.Context()
	// The body is read after the permission check so a caller without the editor role learns nothing about which shapes
	// are accepted.
	var req struct {
		Sha256 string `json:"sha256"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !mediaShaParamRE.MatchString(req.Sha256) {
		writeMediaError(c, http.StatusBadRequest, api.CodeInvalidRequest, api.ErrorMessage(ctx, api.CodeInvalidRequest))
		return
	}
	// The stored mime decides the canonical extension; the name is derived from it, never taken from the request.
	m, err := store.NewMediaStore(s.db).BySha256(ctx, req.Sha256)
	if err != nil {
		s.logger.Error("media name registration lookup failed", "user_id", user.ID, "deck_id", deckID, "media_sha", req.Sha256, "error", err)
		writeMediaError(c, http.StatusInternalServerError, api.CodeInternal, api.ErrorMessage(ctx, api.CodeInternal))
		return
	}
	if m == nil {
		writeMediaError(c, http.StatusNotFound, codeMediaNotFound, mediaNotFoundMessage)
		return
	}
	readable, err := store.MediaAccessibleToUser(ctx, s.db, user.ID, req.Sha256)
	if err != nil {
		s.logger.Error("media name registration access check failed", "user_id", user.ID, "deck_id", deckID, "media_sha", req.Sha256, "error", err)
		writeMediaError(c, http.StatusInternalServerError, api.CodeInternal, api.ErrorMessage(ctx, api.CodeInternal))
		return
	}
	if !readable {
		s.logger.Info("media name registration refused", "user_id", user.ID, "deck_id", deckID, "media_sha", req.Sha256, "code", store.CodeMediaNotReadable)
		writeMediaError(c, http.StatusForbidden, store.CodeMediaNotReadable, api.ErrorMessage(ctx, store.CodeMediaNotReadable))
		return
	}
	name, err := store.RegisterUploadedMediaName(ctx, s.db, deckID, req.Sha256, m.Mime)
	if err != nil {
		s.logger.Error("media name registration failed", "user_id", user.ID, "deck_id", deckID, "media_sha", req.Sha256, "error", err)
		writeMediaError(c, http.StatusInternalServerError, api.CodeInternal, api.ErrorMessage(ctx, api.CodeInternal))
		return
	}
	c.JSON(http.StatusOK, gin.H{"name": name})
}

// deckMediaUpload is the upload entry the editor uses: first confirm the current user has at least the editor role on the deck,
// then go through the same storage and validation logic as /media. The response also names the file inside the deck.
func (s *Server) deckMediaUpload(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleEditor); !ok {
		return
	}
	s.storeUpload(c, user, deckID)
}

// userMediaQuota resolves the media quota (bytes) of one user: the administrator's setting for that user, otherwise
// environment variable > settings table > 0. 0 (including unconfigured) means unlimited — being off by default is
// deliberate: no picking a number for the admin that nobody agreed to. The rule is internal/media.UserQuotaBytes, shared
// with the deck package import chain, so the two cannot drift apart.
func (s *Server) userMediaQuota(ctx context.Context, user *store.User) int64 {
	return media.UserQuotaBytes(ctx, s.db, user)
}

// multipartOverheadBytes is the allowance for the upload body beyond the single-file limit (boundaries, part headers, other small fields).
const multipartOverheadBytes = 64 << 10

// mediaUpload is the generic upload entry: any signed-in user may upload (the behaviour before authorization landed, unchanged).
func (s *Server) mediaUpload(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	s.storeUpload(c, user, 0)
}

// storeUpload reads the file field from the multipart, writes it to disk and returns metadata JSON; errors map to stable codes.
// Both the /media and /decks/:id/media entries share this function, so the validation logic exists only once.
//
// A non-zero deckID also registers the file's deck-scoped name and returns it as "name": the editor inserts that name, so
// the sandboxed editing frame can show the file through the frame media route before the note is saved.
func (s *Server) storeUpload(c *gin.Context, user *store.User, deckID uint64) {
	ctx := c.Request.Context()
	fileLimit := media.ResolveMaxBytes(ctx, s.db)
	// Cap the whole request body before parsing the multipart: multipart parsing spills parts over the in-memory threshold to a temp file,
	// so without a cap an arbitrarily large body would be written to disk first and only then reach the single-file size check below.
	// The cap = single-file limit + allowance for multipart boundaries and headers.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, fileLimit+multipartOverheadBytes)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status, code, msg := mediaErrorResponse(media.ErrTooLarge)
			s.logger.Info("media upload rejected", "user_id", user.ID, "code", code, "reason", "request body too large")
			writeMediaError(c, status, code, msg)
			return
		}
		writeMediaError(c, http.StatusBadRequest, "media_missing_file", "no file part named file")
		return
	}
	defer file.Close()

	name := ""
	if header != nil {
		name = header.Filename
	}
	// Validate before writing to disk: read the file into memory (bounded by the single-file limit, reading one byte extra to detect overflow),
	// with every rejection check run after that; only on pass is it handed to the storage layer to write a temp file + rename, avoiding "half the file
	// written before the overflow is noticed". Memory use is bounded by the single-file limit and does not grow with anything but upload concurrency.
	raw, err := io.ReadAll(io.LimitReader(file, fileLimit+1))
	if err != nil {
		s.logger.Info("media upload read failed", "user_id", user.ID, "error", err)
		writeMediaError(c, http.StatusBadRequest, "media_read_failed", "could not read the uploaded file")
		return
	}
	if int64(len(raw)) > fileLimit {
		status, code, msg := mediaErrorResponse(media.ErrTooLarge)
		s.logger.Info("media upload rejected", "user_id", user.ID, "code", code)
		writeMediaError(c, status, code, msg)
		return
	}
	// Per-user total quota: checked only when a positive limit is configured; 0/unconfigured = unlimited.
	if quota := s.userMediaQuota(ctx, user); quota > 0 {
		if !s.checkMediaQuota(c, user, raw, quota) {
			return
		}
	}
	saved, err := s.media.Save(ctx, bytes.NewReader(raw), media.SaveOptions{
		Limit:     fileLimit,
		Name:      name,
		CreatedBy: store.Ptr(user.ID),
	})
	if err != nil {
		status, code, msg := mediaErrorResponse(err)
		s.logger.Info("media upload rejected", "user_id", user.ID, "code", code, "error", err)
		writeMediaError(c, status, code, msg)
		return
	}
	s.auditor.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionMediaUpload,
		TargetType: "media",
		Detail:     map[string]any{"sha256": saved.Sha256, "mime": saved.Mime, "bytes": saved.Bytes},
	})
	// The external identifier is the sha256: responses no longer carry an auto-increment id, and the url is built straight from the hash.
	body := gin.H{
		"sha256": saved.Sha256,
		"mime":   saved.Mime,
		"bytes":  saved.Bytes,
		"url":    "/media/" + saved.Sha256,
	}
	if deckID != 0 {
		name, err := store.RegisterUploadedMediaName(ctx, s.db, deckID, saved.Sha256, saved.Mime)
		if err != nil {
			s.logger.Error("media upload name registration failed", "user_id", user.ID, "deck_id", deckID, "error", err)
			writeMediaError(c, http.StatusInternalServerError, "media_internal_error", "could not store the file")
			return
		}
		body["name"] = name
	}
	c.JSON(http.StatusCreated, body)
}

// checkMediaQuota checks the per-user total quota before writing to disk: used + this addition > limit means refusal,
// with the stable code media_quota_exceeded and localized copy naming the limit/usage; returns false.
//
// Used = media referenced by that user's notes ∪ that user's uploads nobody references yet, deduplicated and summed (see internal/store.UserMediaUsage).
// If this file has the same sha256 as a blob already charged to that user, the addition is 0 (deduplication is not charged twice).
func (s *Server) checkMediaQuota(c *gin.Context, user *store.User, raw []byte, quota int64) bool {
	ctx := c.Request.Context()
	usage, err := store.UserMediaUsage(ctx, s.db, user.ID)
	if err != nil {
		s.logger.Error("media quota usage lookup failed", "user_id", user.ID, "error", err)
		writeMediaError(c, http.StatusInternalServerError, "media_internal_error", "could not store the file")
		return false
	}
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])
	extra := int64(len(raw))
	if usage.Sha256[sha] {
		extra = 0
	}
	if usage.Bytes+extra <= quota {
		return true
	}
	s.logger.Info("media upload rejected",
		"user_id", user.ID, "code", media.CodeQuotaExceeded, "used", usage.Bytes, "quota", quota)
	writeMediaError(c, http.StatusRequestEntityTooLarge, media.CodeQuotaExceeded,
		s.mediaQuotaMessage(c, usage.Bytes, quota))
	// Notify the admin (class D) when the quota is hit. A failed send never affects this upload's error response.
	s.NotifyMediaAlert(ctx, user, usage.Bytes, quota)
	return false
}

// mediaQuotaMessage assembles the localized copy naming the limit and usage (user-visible copy lives in the language packs).
// Falls back to English when the localizer is missing — never hide the numbers.
func (s *Server) mediaQuotaMessage(c *gin.Context, used, quota int64) string {
	loc, ok := s.localizer(c)
	if !ok || loc == nil {
		return "media quota exceeded"
	}
	return loc.Tf("media.quota.exceeded", map[string]any{
		"used":  humanBytes(used),
		"limit": humanBytes(quota),
	})
}

// mediaErrorResponse maps a storage-layer error to an HTTP status code and a stable code.
func mediaErrorResponse(err error) (status int, code, msg string) {
	var me *media.Error
	if errors.As(err, &me) {
		switch me.Code {
		case media.CodeTooLarge:
			return http.StatusRequestEntityTooLarge, me.Code, me.Msg
		}
		return http.StatusBadRequest, me.Code, me.Msg
	}
	return http.StatusInternalServerError, "media_internal_error", "could not store the file"
}

// writeMediaError emits the uniform error envelope.
func writeMediaError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// mediaShaParamRE is the path parameter shape of GET /media/:sha: exactly 64 lowercase hex characters.
// A malformed parameter 404s straight away, without touching the database first — no arbitrary string gets to probe the media table.
var mediaShaParamRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// mediaServe proxies media reads: with a sha256 ETag and immutable caching.
//
// Authorization: besides sign-in, "deck access" is required — one of two: (1) a media_uploaders record pointing at me
// (I supplied these bytes, including a deduplication hit); (2) a media_notes mapping to a non-soft-deleted note inside a deck I can see.
// The predicate is shared with list/queue/stats (store.MediaAccessibleToUser → mediaReadableByUser),
// and no permission and not-found both return 404.
func (s *Server) mediaServe(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	sha := c.Param("sha")
	// Shape validation precedes any query: shorter than 64 characters, a non-hex character, or a legacy numeric id all 404.
	if !mediaShaParamRE.MatchString(sha) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	// No permission caching: revoking a grant must take effect on the next request, caching would turn 'revoke' into 'wait for expiry'.
	allowed, err := store.MediaAccessibleToUser(c.Request.Context(), s.db, user.ID, sha)
	if err != nil {
		s.logger.Error("media access check failed", "media_sha", sha, "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !allowed {
		// The same 404 as "media does not exist": do not leak to someone without permission whether a given sha exists.
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	m, f, err := s.media.Open(c.Request.Context(), sha)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		s.logger.Error("open media failed", "media_sha", sha, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// The headers are written before the ETag revalidation, so a 304 carries the same security policy as a 200.
	writeMediaHeaders(c, m)
	etag := `"` + m.Sha256 + `"`
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	// An ETag hit returns 304 without re-reading the file body.
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	http.ServeContent(c.Writer, c.Request, "", m.CreatedAt, f)
}

// writeMediaHeaders sets the one serving rule shared by /media/<sha> and /fm/<token>/<name>: every response is sandboxed,
// carries the type it was classified as, and is a download unless its class is inline (image, audio, video).
// A sandboxed CSP keeps even an inline SVG containing <script> from executing on the application's own origin, and
// nosniff stops a proxy or old browser from reinterpreting the body as another type.
func writeMediaHeaders(c *gin.Context, m *store.Media) {
	class := mediatype.ClassOfMime(m.Mime)
	c.Header("Content-Security-Policy", "sandbox")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", m.Mime)
	if !class.IsInline() {
		c.Header("Content-Disposition", "attachment")
	}
	c.Header("ETag", `"`+m.Sha256+`"`)
}
