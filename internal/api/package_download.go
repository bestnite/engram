package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"git.nite07.com/nite/engram/internal/store"
	"github.com/gin-gonic/gin"
)

const exportDownloadPurpose = "deck_export_download"
const exportDownloadPath = "/api/v1/decks/export-downloads/"

type ExportDownload struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type exportDownloadTicket struct {
	UserID             uint64 `json:"user_id"`
	DeckID             uint64 `json:"deck_id"`
	Format             string `json:"format"`
	IncludeProgress    bool   `json:"include_progress"`
	IncludeReviews     bool   `json:"include_reviews"`
	IncludeDeckConfigs *bool  `json:"include_deck_configs"`
	IncludeMedia       bool   `json:"include_media"`
	IncludeWeights     bool   `json:"include_weights"`
}

func (a *API) CreateExportDownload(ctx context.Context, userID, deckID uint64, format string, opts store.PackageOptions) (*ExportDownload, error) {
	if format == "" {
		format = "edeck"
	}
	if format != "edeck" && format != "apkg" {
		return nil, InvalidRequest("invalid package format")
	}
	if _, err := a.RequireDeckRole(ctx, userID, deckID, store.RoleReader); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(exportDownloadTicket{userID, deckID, format, opts.IncludeProgress, opts.IncludeReviews, opts.IncludeDeckConfigs, opts.IncludeMedia, opts.IncludeWeights})
	if err != nil {
		return nil, fmt.Errorf("encode export ticket: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate export ticket: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	now := a.now().UTC()
	row := &store.ActionToken{Purpose: exportDownloadPurpose, UserID: userID, TokenHash: store.HashActionToken(token), Payload: string(payload), CreatedAt: now, ExpiresAt: now.Add(importUploadTTL)}
	if err := a.tokens.Create(ctx, row); err != nil {
		return nil, err
	}
	return &ExportDownload{URL: a.baseURL + exportDownloadPath + token, ExpiresAt: row.ExpiresAt}, nil
}

func (a *API) handleExportDownload(c *gin.Context) {
	ctx := c.Request.Context()
	tok, err := a.tokens.Consume(ctx, exportDownloadPurpose, c.Param("token"), a.now())
	if err != nil {
		if errors.Is(err, store.ErrActionTokenNotFound) || errors.Is(err, store.ErrActionTokenUsed) || errors.Is(err, store.ErrActionTokenExpired) {
			abortNotFound(c)
			return
		}
		writeServiceError(c, err)
		return
	}
	var ticket exportDownloadTicket
	if err := json.Unmarshal([]byte(tok.Payload), &ticket); err != nil || ticket.UserID != tok.UserID {
		abortNotFound(c)
		return
	}
	u, err := a.users.ByID(ctx, ticket.UserID)
	if err != nil || u.Status != store.StatusActive {
		abortNotFound(c)
		return
	}
	d, err := a.decks.ByID(ctx, ticket.DeckID)
	if err != nil {
		abortNotFound(c)
		return
	}
	var data []byte
	contentType := "application/vnd.engram.edeck"
	name := DeckPackageFilename(d.Name, d.PublicID)
	opts := store.PackageOptions{IncludeProgress: ticket.IncludeProgress, IncludeReviews: ticket.IncludeReviews, IncludeDeckConfigs: ticket.IncludeDeckConfigs, IncludeMedia: ticket.IncludeMedia, IncludeWeights: ticket.IncludeWeights}
	switch ticket.Format {
	case "apkg":
		data, err = a.ExportAnkiPackage(ctx, ticket.UserID, d.ID, opts)
		contentType = "application/zip"
		name = strings.TrimSuffix(name, ".edeck") + ".apkg"
	case "edeck":
		var pkg *store.DeckPackage
		pkg, err = a.ExportDeckPackage(ctx, ticket.UserID, d.ID, opts)
		if err == nil {
			var buf bytes.Buffer
			err = pkg.WriteZip(&buf)
			data = buf.Bytes()
		}
	default:
		abortNotFound(c)
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Content-Disposition", AttachmentDisposition(name))
	c.Data(http.StatusOK, contentType, data)
}
