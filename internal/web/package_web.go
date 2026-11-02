package web

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// Browser paths for deck packages:
//   GET  /decks/:id/package  export one deck as a .apkg download (reusing the store-layer export, not rewritten)
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

// deckPackageExport exports an Anki package for a deck the current user may read.
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
	if format := c.DefaultQuery("format", "apkg"); format != "apkg" {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	data, err := s.api.ExportDeckPackage(c.Request.Context(), user.ID, deckID, api.PackageQueryOptions(c))
	if err != nil {
		s.logger.Error("deck package export failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Disposition", api.AttachmentDisposition(api.DeckPackageFilename(deck.Name, deck.PublicID)))
	c.Data(http.StatusOK, "application/zip", data)
}

type batchExportDeckRequest struct {
	DeckIDs           []string `json:"deck_ids"`
	IncludeMedia      *bool    `json:"include_media"`
	IncludeEngramMeta *bool    `json:"include_engram_meta"`
	IncludeProgress   bool     `json:"include_progress"`
	IncludeReviews    bool     `json:"include_reviews"`
	IncludeWeights    bool     `json:"include_weights"`
}

// A batch export is a convenience for a handful of decks, not a bulk transfer. Past these limits the
// request is refused before the archive is built, so one caller cannot make the process hold or spool an
// unbounded archive. maxBatchExportBytes is a variable so a test can lower it; production never changes it.
const maxBatchExportDecks = 100

var maxBatchExportBytes int64 = 512 << 20

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
	if len(req.DeckIDs) > maxBatchExportDecks {
		abortBatchExportTooLarge(c, fmt.Sprintf("at most %d decks can be exported at once", maxBatchExportDecks))
		return
	}

	// Resolve every deck and the caller's role before the first response byte, so a refused deck can
	// still answer 403/404 instead of a truncated archive from an already-started response.
	decks := make([]*store.Deck, 0, len(req.DeckIDs))
	for _, deckPublicID := range req.DeckIDs {
		deck, ok := s.deckByPublicID(c, deckPublicID)
		if !ok {
			return
		}
		if _, ok := s.loadDeckForRole(c, user, deck.ID, store.RoleReader); !ok {
			return
		}
		decks = append(decks, deck)
	}

	// The archive is written to a temporary file instead of a bytes.Buffer: that keeps a large batch out
	// of process memory and makes the total size known before the response starts, so an oversized
	// archive answers 413 rather than streaming a download the client cannot tell is truncated.
	tmp, err := os.CreateTemp("", "engram-deck-export-*.zip")
	if err != nil {
		s.logger.Error("create batch export temp file failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	ctx := c.Request.Context()
	counted := &countingWriter{w: tmp}
	zw := zip.NewWriter(counted)
	used := make(map[string]bool, len(decks))
	for _, deck := range decks {
		// As with the single export, this goes through the api service, which fills in the media root and clock uniformly.
		pkg, err := s.api.ExportDeckPackage(ctx, user.ID, deck.ID, store.PackageOptions{
			IncludeMedia:      includeMedia,
			IncludeEngramMeta: req.IncludeEngramMeta,
			IncludeProgress:   req.IncludeProgress,
			IncludeReviews:    req.IncludeReviews,
			IncludeWeights:    req.IncludeWeights,
		})
		if err != nil {
			s.logger.Error("export deck package in batch failed", "deck_id", deck.ID, "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		if counted.n+int64(len(pkg)) > maxBatchExportBytes {
			abortBatchExportTooLarge(c, "the archive is too large; export the decks one at a time")
			return
		}
		// Every package inside the zip is named after the deck title too; when two decks share a title a sequence number is appended, so entries do not overwrite each other.
		filename := uniqueEntryName(api.DeckPackageFilename(deck.Name, deck.PublicID), used)
		f, err := zw.Create(filename)
		if err != nil {
			s.logger.Error("create zip entry failed", "filename", filename, "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		if _, err := f.Write(pkg); err != nil {
			s.logger.Error("write zip entry failed", "filename", filename, "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
	}

	if err := zw.Close(); err != nil {
		s.logger.Error("close batch zip failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// The zip headers and the central directory add bytes beyond the packages themselves.
	if counted.n > maxBatchExportBytes {
		abortBatchExportTooLarge(c, "the archive is too large; export the decks one at a time")
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		s.logger.Error("rewind batch export failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.DataFromReader(http.StatusOK, counted.n, "application/zip", tmp, map[string]string{
		"Content-Disposition": `attachment; filename="engram-decks-export.zip"`,
	})
}

// abortBatchExportTooLarge answers a batch export past its deck-count or size cap; nothing has been
// written to the response yet, so the client gets a 413 instead of a partial archive.
func abortBatchExportTooLarge(c *gin.Context, detail string) {
	c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{
		"code":    api.CodeRequestTooLarge,
		"message": api.ErrorMessage(c.Request.Context(), api.CodeRequestTooLarge) + " " + detail,
	}})
}

// countingWriter counts the bytes written through it while passing them on, so the archive's exact size
// is known before it is sent.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
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
