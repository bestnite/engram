package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/mediatype"
	"git.nite07.com/nite/engram/internal/store"
)

// Media-related system setting keys and their environment-variable override names.
// Environment variable > settings table > built-in default, the same precedence as internal/config; the settings
// table is read directly here so an admin change takes effect immediately, with no restart.
const (
	settingKeyMediaAllowedMimes = "media_allowed_mimes"
	// settingKeyMediaUserQuotaBytes is the per-user total media quota (bytes); 0/unconfigured = unlimited.
	// Together with envMediaUserQuotaBytes it points at the same parsing in internal/media (the upload and import chains share it).
	settingKeyMediaUserQuotaBytes = media.SettingKeyMediaUserQuotaBytes
	envMediaAllowedMimes          = "MEDIA_ALLOWED_MIMES"
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
	}
}

// deckMediaUpload is the upload entry the editor uses: first confirm the current user has at least the editor role on the deck,
// then go through the same storage and validation logic as /media.
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
	s.storeUpload(c, user)
}

// allowedMimes resolves the effective mime allow-list: environment variable > settings table > default allow-list.
func (s *Server) allowedMimes(ctx context.Context) []string {
	if raw := strings.TrimSpace(os.Getenv(envMediaAllowedMimes)); raw != "" {
		return splitMimeList(raw)
	}
	if s.db != nil {
		if settings, err := store.LoadSettings(ctx, s.db); err == nil {
			if raw, ok := settings[settingKeyMediaAllowedMimes]; ok {
				if list := splitMimeList(raw); len(list) > 0 {
					return list
				}
			}
		}
	}
	return media.DefaultAllowedMimes()
}

// userMediaQuota resolves the effective per-user total media quota (bytes): environment variable > settings table > 0.
// 0 (including unconfigured) means unlimited — being off by default is deliberate: no picking a number for the admin that nobody agreed to.
// The parsing shares internal/media.ResolveUserQuotaBytes with the deck package import chain, so the rules cannot drift apart.
func (s *Server) userMediaQuota(ctx context.Context) int64 {
	return media.ResolveUserQuotaBytes(ctx, s.db)
}

// splitMimeList splits a comma-separated mime string into a lowercase list with whitespace and empty items removed.
func splitMimeList(raw string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// multipartOverheadBytes is the allowance for the upload body beyond the single-file limit (boundaries, part headers, other small fields).
const multipartOverheadBytes = 64 << 10

// mediaUpload is the generic upload entry: any signed-in user may upload (the behaviour before authorization landed, unchanged).
func (s *Server) mediaUpload(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	s.storeUpload(c, user)
}

// storeUpload reads the file field from the multipart, writes it to disk and returns metadata JSON; errors map to stable codes.
// Both the /media and /decks/:id/media entries share this function, so the validation logic exists only once.
func (s *Server) storeUpload(c *gin.Context, user *store.User) {
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

	declared := ""
	if header != nil {
		declared = header.Header.Get("Content-Type")
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
	if quota := s.userMediaQuota(ctx); quota > 0 {
		if !s.checkMediaQuota(c, user, raw, quota) {
			return
		}
	}
	saved, err := s.media.Save(ctx, bytes.NewReader(raw), media.SaveOptions{
		Limit:        fileLimit,
		AllowedMimes: s.allowedMimes(ctx),
		DeclaredMime: declared,
		CreatedBy:    store.Ptr(user.ID),
	})
	if err != nil {
		status, code, msg := mediaErrorResponse(err)
		s.logger.Info("media upload rejected", "user_id", user.ID, "code", code, "error", err)
		writeMediaError(c, status, code, msg)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionMediaUpload,
		TargetType: "media",
		Detail:     map[string]any{"sha256": saved.Sha256, "mime": saved.Mime, "bytes": saved.Bytes},
	})
	// The external identifier is the sha256: responses no longer carry an auto-increment id, and the url is built straight from the hash.
	c.JSON(http.StatusCreated, gin.H{
		"sha256": saved.Sha256,
		"mime":   saved.Mime,
		"bytes":  saved.Bytes,
		"url":    "/media/" + saved.Sha256,
	})
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
		case media.CodeMimeNotAllowed:
			return http.StatusUnsupportedMediaType, me.Code, me.Msg
		case media.CodeMagicMismatch:
			return http.StatusUnprocessableEntity, me.Code, me.Msg
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

	etag := `"` + m.Sha256 + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	// The read side tolerates historical polluted rows that may exist in the database: only types the byte check allows are inlined as declared,
	// everything else (e.g. text/html or image/svg+xml written by an old import chain) is downgraded to a download, avoiding same-origin stored XSS.
	contentType := m.Mime
	if !mediatype.Allowed(m.Mime, mediatype.DefaultAllowedMimes()) {
		contentType = "application/octet-stream"
		c.Header("Content-Disposition", "attachment")
	}
	// Defence in depth: an explicit Content-Type is not overridden by http.ServeContent, but nosniff still stops old browsers/proxies from
	// executing the response body as a script — before this fix the header was not set here at all.
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", contentType)
	// An ETag hit returns 304 without re-reading the file body.
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	http.ServeContent(c.Writer, c.Request, "", m.CreatedAt, f)
}
