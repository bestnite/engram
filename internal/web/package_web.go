package web

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// Browser paths for deck packages (.edeck):
//   GET  /decks/:id/package  export one deck as a .edeck download (reusing the store-layer export, not rewritten)
//   GET  /import             import page (returns the SPA app shell, rendered by the client router)
// The import itself goes through REST `POST /api/v1/decks/import`, the same service entry the SPA's built-in page uses.

// registerPackageWebRoutes mounts the browser entries for deck packages. Export goes through the api service, so s.api is needed as well.
func (s *Server) registerPackageWebRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.api == nil {
		return
	}
	router.GET("/decks/:id/package", s.deckPackageExport)
	router.POST("/api/v1/decks/export-zip", s.sessions.CSRFMiddleware(), s.deckBatchExportZip)
	// The import page returns the app shell, rendered by the client router; the upload goes through the JSON endpoint (the frontend's importDeckPackage).
	router.GET("/import", s.shell.ServeIndex)
}

// packageExportMediaType is the MIME for .edeck (consistent with the REST export).
const packageExportMediaType = "application/vnd.engram.edeck"

// deckPackageExport exports a deck the current user may read as a .edeck download.
// Permissions follow the same rules as the REST entry (reader is enough to export). The export must go through api.ExportDeckPackage: the media byte root
// is filled in uniformly by it, and calling the store layer directly without the root makes media resolve against the process working directory, so any deck referencing media would 500.
func (s *Server) deckPackageExport(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	deckID, ok := s.deckIDParam(c)
	if !ok {
		return
	}
	deck, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader)
	if !ok {
		return
	}
	pkg, err := s.api.ExportDeckPackage(c.Request.Context(), user.ID, deckID, store.PackageOptions{IncludeMedia: true})
	if err != nil {
		s.logger.Error("deck package export failed", "deck_id", deckID, "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := pkg.WriteZip(&buf); err != nil {
		s.logger.Error("write deck package failed", "deck_id", deckID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Type", packageExportMediaType)
	c.Header("Content-Disposition", api.AttachmentDisposition(api.DeckPackageFilename(deck.Name, deck.PublicID)))
	c.Data(http.StatusOK, packageExportMediaType, buf.Bytes())
}

type batchExportDeckRequest struct {
	DeckIDs         []string `json:"deck_ids"`
	IncludeMedia    *bool    `json:"include_media"`
	IncludeProgress bool     `json:"include_progress"`
	IncludeReviews  bool     `json:"include_reviews"`
	IncludeWeights  bool     `json:"include_weights"`
}

func (s *Server) deckBatchExportZip(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	var req batchExportDeckRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.DeckIDs) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_request", "message": "deck_ids required"}})
		return
	}
	includeMedia := true
	if req.IncludeMedia != nil {
		includeMedia = *req.IncludeMedia
	}

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	ctx := c.Request.Context()
	used := make(map[string]bool, len(req.DeckIDs))
	for _, deckPublicID := range req.DeckIDs {
		deck, ok := s.deckByPublicID(c, deckPublicID)
		if !ok {
			zw.Close()
			return
		}
		if _, ok := s.loadDeckForRole(c, user, deck.ID, store.RoleReader); !ok {
			zw.Close()
			return
		}
		// As with the single export, this goes through the api service, which fills in the media root and clock uniformly.
		pkg, err := s.api.ExportDeckPackage(ctx, user.ID, deck.ID, store.PackageOptions{
			IncludeMedia:    includeMedia,
			IncludeProgress: req.IncludeProgress,
			IncludeReviews:  req.IncludeReviews,
			IncludeWeights:  req.IncludeWeights,
		})
		if err != nil {
			s.logger.Error("export deck package in batch failed", "deck_id", deck.ID, "error", err)
			zw.Close()
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		var pkgBuf bytes.Buffer
		if err := pkg.WriteZip(&pkgBuf); err != nil {
			s.logger.Error("write deck package zip in batch failed", "deck_id", deck.ID, "error", err)
			zw.Close()
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		// Every package inside the zip is named after the deck title too; when two decks share a title a sequence number is appended, so entries do not overwrite each other.
		filename := uniqueEntryName(api.DeckPackageFilename(deck.Name, deck.PublicID), used)
		f, err := zw.Create(filename)
		if err != nil {
			s.logger.Error("create zip entry failed", "filename", filename, "error", err)
			zw.Close()
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		if _, err := f.Write(pkgBuf.Bytes()); err != nil {
			s.logger.Error("write zip entry failed", "filename", filename, "error", err)
			zw.Close()
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}

	if err := zw.Close(); err != nil {
		s.logger.Error("close batch zip failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="engram-decks-export.zip"`)
	c.Data(http.StatusOK, "application/zip", zipBuf.Bytes())
}

// uniqueEntryName registers and returns a unique zip entry name in used: on a collision it appends (2), (3)… before the extension.
func uniqueEntryName(name string, used map[string]bool) string {
	candidate := name
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s (%d)%s", base, i, ext)
	}
	used[candidate] = true
	return candidate
}
