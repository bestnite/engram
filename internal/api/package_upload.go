package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// Uploading a deck package with a one-time ticket:
//   POST /api/v1/decks/import-uploads         issue a ticket (write)
//   PUT  /api/v1/decks/import-uploads/:token  upload and import with the ticket (the ticket is the credential)
//
// Why it exists: every MCP tool argument is emitted by the model token by token, so a large deck passed through import_deck's package
// parameter means making the model spit out the whole package again; and an agent usually cannot get the API key (the key is configured in the MCP client's
// request headers, invisible to the model), so it cannot call the REST upload directly. A ticket splits the two concerns: authentication happens at issuance
// (either MCP or REST can issue), while the bytes are sent directly by the agent with a tool like curl, never through the model, and the key
// never leaves the client.
//
// Tickets live in the action_tokens table: the database holds only a sha256 digest, consumption uses a conditional update to guarantee one-time use, and expired rows are
// cleaned up uniformly by internal/retention. Import options are fixed into the ticket at issuance, and the upload side accepts no options at all,
// so whoever holds the URL can only perform the single import the issuer already authorized.

const (
	// importUploadPurpose is the purpose value of an upload ticket in action_tokens.
	importUploadPurpose = "deck_import_upload"
	// importUploadTTL is the ticket lifetime: enough for an agent to pack and upload a large package, while making a leaked URL useless quickly.
	importUploadTTL = 15 * time.Minute
	// importUploadPathPrefix is the path prefix of the upload endpoint, with the plaintext ticket right after it.
	importUploadPathPrefix = "/api/v1/decks/import-uploads/"
	// importUploadCreateBodyLimit is the cap on the issuance request body: just one short JSON.
	importUploadCreateBodyLimit = 64 << 10
)

// ImportUpload is the issuance result: the agent sends the package bytes to UploadURL using Method.
type ImportUpload struct {
	UploadURL string    `json:"upload_url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expires_at"`
	// MaxBytes is the byte cap on the upload body (the same config item as file import), so callers can check themselves before uploading.
	MaxBytes int64 `json:"max_bytes"`
}

// importUploadTicket is the ticket content written into action_tokens.payload: the import options fixed at issuance,
// plus the API key used for issuance (re-checked for validity at upload time).
type importUploadTicket struct {
	Target              string  `json:"target"`
	DryRun              bool    `json:"dry_run"`
	OnConflict          string  `json:"on_conflict"`
	SkipMissingMedia    bool    `json:"skip_missing_media"`
	AllowOthersProgress bool    `json:"allow_others_progress"`
	ApplyWeights        bool    `json:"apply_weights"`
	APIKeyID            *uint64 `json:"api_key_id,omitempty"`
}

// importUploadRequest is the body of POST /api/v1/decks/import-uploads; its fields map one-to-one onto the file import options.
type importUploadRequest struct {
	Target              string `json:"target"`
	DryRun              bool   `json:"dry_run"`
	OnConflict          string `json:"on_conflict"`
	AllowOthersProgress bool   `json:"allow_others_progress"`
	SkipMissingMedia    bool   `json:"skip_missing_media"`
	ApplyWeights        bool   `json:"apply_weights"`
}

// CreateImportUpload issues a one-time upload ticket; shared by REST and MCP.
//
// Permission on the target deck is decided at issuance: a caller without rights gets no ticket. It is checked again at upload (the import goes through
// ImportDeckPackage), because a role may be revoked between issuance and upload.
func (a *API) CreateImportUpload(ctx context.Context, u *store.User, apiKeyID *uint64, opts store.PackageImportOptions) (*ImportUpload, error) {
	if _, err := a.authorizeImportTarget(ctx, u.ID, opts.Target); err != nil {
		return nil, err
	}
	// The conflict policy is validated at issuance: a wrong option should surface before the agent uploads a large package, not after.
	switch opts.OnConflict {
	case "", "skip", "update", "fail":
	default:
		return nil, InvalidRequest("on_conflict must be one of skip, update, fail")
	}
	payload, err := json.Marshal(importUploadTicket{
		Target:              opts.Target,
		DryRun:              opts.DryRun,
		OnConflict:          opts.OnConflict,
		SkipMissingMedia:    opts.SkipMissingMedia,
		AllowOthersProgress: opts.AllowOthersProgress && u.Role == store.RoleAdmin,
		ApplyWeights:        opts.ApplyWeights,
		APIKeyID:            apiKeyID,
	})
	if err != nil {
		return nil, fmt.Errorf("encode import upload ticket: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate import upload token: %w", err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(buf)
	now := a.now().UTC()
	row := &store.ActionToken{
		Purpose:   importUploadPurpose,
		UserID:    u.ID,
		TokenHash: store.HashActionToken(plaintext),
		Payload:   string(payload),
		CreatedAt: now,
		ExpiresAt: now.Add(importUploadTTL),
	}
	if err := a.tokens.Create(ctx, row); err != nil {
		return nil, err
	}
	return &ImportUpload{
		UploadURL: a.baseURL + importUploadPathPrefix + plaintext,
		Method:    http.MethodPut,
		ExpiresAt: row.ExpiresAt,
		MaxBytes:  media.ResolveMaxBytes(ctx, a.db),
	}, nil
}

// ImportDeckPackageUpload consumes the ticket and imports the deck package in the request body.
//
// The ticket is consumed first, bytes are read after: a request with an invalid ticket reads not a single byte; once presented the ticket is spent, and a failed import
// cannot be replayed - the caller just issues a new one (issuance is cheap, and the uploaded bytes never went through the model anyway).
// The body may be an .edeck or .apkg zip, or the JSON document produced by export_deck, told apart by the first non-whitespace byte.
func (a *API) ImportDeckPackageUpload(ctx context.Context, token string, body io.Reader) (*store.PackageImportReport, error) {
	invalid := newServiceError(http.StatusNotFound, CodeImportUploadInvalid, "")
	now := a.now()
	tok, err := a.tokens.Consume(ctx, importUploadPurpose, token, now)
	if err != nil {
		if errors.Is(err, store.ErrActionTokenNotFound) || errors.Is(err, store.ErrActionTokenUsed) || errors.Is(err, store.ErrActionTokenExpired) {
			return nil, invalid
		}
		return nil, err
	}
	var ticket importUploadTicket
	if err := json.Unmarshal([]byte(tok.Payload), &ticket); err != nil {
		return nil, fmt.Errorf("decode import upload ticket: %w", err)
	}
	// If the account is disabled after issuance, or the key used to issue it is revoked / expired, the ticket dies with it: the permission boundary matches the key itself.
	u, err := a.users.ByID(ctx, tok.UserID)
	if err != nil || u.Status != store.StatusActive {
		return nil, invalid
	}
	if ticket.APIKeyID != nil {
		k, err := a.keys.ByID(ctx, *ticket.APIKeyID)
		if err != nil || k.UserID != u.ID || store.ValidateAPIKey(k, now) != nil {
			return nil, invalid
		}
	}
	opts := store.PackageImportOptions{
		Target:              ticket.Target,
		DryRun:              ticket.DryRun,
		OnConflict:          ticket.OnConflict,
		SkipMissingMedia:    ticket.SkipMissingMedia,
		AllowOthersProgress: ticket.AllowOthersProgress,
		ApplyWeights:        ticket.ApplyWeights,
	}
	// Authorize before reading bytes, the same invariant as direct-link import: a rejected request spends no reading or parsing.
	if _, err := a.authorizeImportTarget(ctx, u.ID, opts.Target); err != nil {
		return nil, err
	}
	limited := &overflowReader{r: body, remaining: media.ResolveMaxBytes(ctx, a.db)}
	r, err := uploadPackageReader(limited)
	if err != nil {
		err = mapPackageError(err)
	} else {
		var report *store.PackageImportReport
		report, err = a.ImportDeckPackage(ctx, u, ticket.APIKeyID, r, opts)
		if err == nil {
			return report, nil
		}
	}
	// Over-limit takes precedence over other errors: truncated bytes make parsing report a format error, which is not the real cause.
	// The code reuses file import's package_too_large, keeping the size cap consistent in both places.
	if limited.exceeded {
		return nil, newServiceError(http.StatusRequestEntityTooLarge, store.CodePackageTooLarge, "")
	}
	return nil, err
}

// uploadPackageReader decides the body shape from the first non-whitespace byte: '{' means a JSON document, handed to the same
// store.PackageReader as MCP uses, restoring it to a zip; anything else is taken as archive bytes (.edeck or .apkg). Agents therefore need not build a zip themselves.
func uploadPackageReader(body io.Reader) (io.Reader, error) {
	br := bufio.NewReader(body)
	head, err := br.Peek(64)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, err
	}
	trimmed := bytes.TrimLeft(head, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return br, nil
	}
	var doc map[string]any
	if err := json.NewDecoder(br).Decode(&doc); err != nil {
		return nil, &store.PackageError{Code: store.CodePackageBadFormat, Message: "package JSON document is malformed"}
	}
	return store.PackageReader(doc)
}

// overflowReader lets at most remaining bytes through; on overflow it records exceeded and returns an error.
// Recording the overflow is what lets a failed import distinguish "the package was truncated for being too large" from "the package itself is malformed".
type overflowReader struct {
	r         io.Reader
	remaining int64
	exceeded  bool
}

func (o *overflowReader) Read(p []byte) (int, error) {
	if o.remaining <= 0 {
		// At the limit: try reading one more byte - getting one means overflow; getting EOF means the package is exactly the limit.
		var one [1]byte
		n, err := o.r.Read(one[:])
		if n > 0 {
			o.exceeded = true
			return 0, errors.New("import upload body exceeds the size limit")
		}
		return 0, err
	}
	if int64(len(p)) > o.remaining {
		p = p[:o.remaining]
	}
	n, err := o.r.Read(p)
	o.remaining -= int64(n)
	return n, err
}

// handleCreateImportUpload is POST /api/v1/decks/import-uploads (application/json).
func (a *API) handleCreateImportUpload(c *gin.Context) {
	u, _ := CurrentUser(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, importUploadCreateBodyLimit)
	var req importUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if isRequestBodyTooLarge(err) {
			abortError(c, http.StatusRequestEntityTooLarge, CodeInvalidRequest, "request body too large")
			return
		}
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "")
		return
	}
	upload, err := a.CreateImportUpload(c.Request.Context(), u, CurrentAPIKeyID(c), store.PackageImportOptions{
		Target:              req.Target,
		DryRun:              req.DryRun,
		OnConflict:          req.OnConflict,
		AllowOthersProgress: req.AllowOthersProgress,
		SkipMissingMedia:    req.SkipMissingMedia,
		ApplyWeights:        req.ApplyWeights,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, upload)
}

// handleImportUpload is PUT /api/v1/decks/import-uploads/:token: the body is the package bytes.
//
// The access log records the path including the ticket; it is written when the request ends, by which time this request has consumed the ticket, so the plaintext
// in the log can no longer be used for any import.
func (a *API) handleImportUpload(c *gin.Context) {
	report, err := a.ImportDeckPackageUpload(c.Request.Context(), c.Param("token"), c.Request.Body)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, report)
}
