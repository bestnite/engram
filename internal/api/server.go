package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/urlfetch"
)

// Deps holds the API's explicit dependencies (AGENTS.md §2.4: explicit wiring at the entry point).
type Deps struct {
	DB      *gorm.DB
	Logger  *slog.Logger
	Keys    *store.APIKeyStore
	Users   *store.UserStore
	Decks   *store.DeckStore
	Notes   *store.NoteStore
	Presets *store.PresetStore
	Cards   *store.CardStore
	// Grants is the deck grant store; when empty, New builds it from DB.
	Grants  *store.GrantStore
	Auditor *auth.Auditor
	// Now is an injectable clock; when zero, the system UTC time is used.
	Now func() time.Time
	// MediaRoot is the local root directory for media bytes, used when exporting/importing decks with inline media (may be empty).
	MediaRoot string
	// PackageFetcher downloads public HTTPS direct links of deck packages; when empty the production implementation in internal/urlfetch is used.
	// It is explicitly injectable so tests can verify endpoint behavior with a controlled implementation instead of depending on the public internet.
	PackageFetcher PackageFetcher
	// BaseURL is the instance's public root address (config item base_url), used to build absolute upload ticket URLs;
	// when empty, tickets return only a site-relative path.
	BaseURL string
	// ReadLimit / WriteLimit / RateWindow are passed straight through to the auth middleware's per-key rate limiting.
	ReadLimit  int
	WriteLimit int
	RateWindow time.Duration
}

// API holds every handler and middleware of /api/v1.
type API struct {
	db        *gorm.DB
	logger    *slog.Logger
	keys      *store.APIKeyStore
	users     *store.UserStore
	decks     *store.DeckStore
	notes     *store.NoteStore
	noteTypes *store.NoteTypeStore
	presets   *store.PresetStore
	cards     *store.CardStore
	grants    *store.GrantStore
	// access is the permission check shared by Web and REST/MCP (the single implementation lives in auth.DeckAccess).
	access  *auth.DeckAccess
	auditor *auth.Auditor
	now     func() time.Time
	authn   *Authenticator
	// mediaRoot is used for inline media in deck packages; when empty no media is written to disk.
	mediaRoot string
	// fetcher downloads public HTTPS direct links of deck packages (internal/urlfetch).
	fetcher PackageFetcher
	// importURL is the per-user direct-link import limiter: this entry triggers outbound requests and needs its own limit.
	importURL *RateLimiter
	// tokens stores upload tickets (action_tokens table, digests only, one-time consumption).
	tokens *store.ActionTokenStore
	// baseURL is the instance root address without a trailing slash; see Deps.BaseURL.
	baseURL string
	// exportPageSize is the page size of export paging: at once the memory bound of one page and the time bound of "holding one database connection".
	// New sets exportDefaultPageSize; tests may lower it (e.g. to 2) to force multiple pages and verify the connection is idle between them.
	exportPageSize int
}

// New builds the API; the dependencies of the middleware and handlers must be complete.
func New(deps Deps) (*API, error) {
	if deps.DB == nil {
		return nil, errors.New("api: Deps.DB is required")
	}
	missing := []string{}
	if deps.Keys == nil {
		missing = append(missing, "Keys")
	}
	if deps.Users == nil {
		missing = append(missing, "Users")
	}
	if deps.Decks == nil {
		missing = append(missing, "Decks")
	}
	if deps.Notes == nil {
		missing = append(missing, "Notes")
	}
	if deps.Presets == nil {
		missing = append(missing, "Presets")
	}
	if deps.Cards == nil {
		missing = append(missing, "Cards")
	}
	if len(missing) > 0 {
		return nil, errors.New("api: missing required dependencies: " + strings.Join(missing, ", "))
	}
	// The grant store can be built from DB on demand, so not every caller (tests included) has to wire it explicitly.
	grants := deps.Grants
	if grants == nil {
		grants = store.NewGrantStore(deps.DB)
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	authn, err := NewAuthenticator(AuthConfig{
		Keys:       deps.Keys,
		Users:      deps.Users,
		Auditor:    deps.Auditor,
		Logger:     logger,
		Now:        now,
		ReadLimit:  deps.ReadLimit,
		WriteLimit: deps.WriteLimit,
		RateWindow: deps.RateWindow,
	})
	if err != nil {
		return nil, err
	}
	// Direct-link download defaults to the production implementation: system resolver/dialer/root CAs with built-in SSRF protection.
	fetcher := deps.PackageFetcher
	if fetcher == nil {
		fetcher = urlfetch.New(urlfetch.DefaultLimits())
	}
	return &API{
		db:        deps.DB,
		logger:    logger,
		keys:      deps.Keys,
		users:     deps.Users,
		decks:     deps.Decks,
		notes:     deps.Notes,
		noteTypes: store.NewNoteTypeStore(deps.DB),
		presets:   deps.Presets,
		cards:     deps.Cards,
		grants:    grants,
		access:    auth.NewDeckAccess(deps.Decks, grants),
		auditor:   deps.Auditor,
		now:       now,
		authn:     authn,
		mediaRoot: deps.MediaRoot,
		fetcher:   fetcher,
		importURL: NewRateLimiter(importURLRateLimit, importURLRateWindow, now),
		tokens:    store.NewActionTokenStore(deps.DB),
		baseURL:   strings.TrimRight(strings.TrimSpace(deps.BaseURL), "/"),
		// Default page size for export pagination; see the note on API.exportPageSize.
		exportPageSize: exportDefaultPageSize,
	}, nil
}

// Register mounts /api/v1 on the given router; each route runs behind the middleware matching its scope.
func (a *API) Register(r gin.IRouter) {
	v1 := r.Group("/api/v1")
	v1.Use(a.authn.Auth())

	v1.GET("/decks", a.authn.RequireScope(store.ScopeRead), a.listDecks)
	v1.GET("/media", a.authn.RequireScope(store.ScopeRead), a.listMedia)
	v1.POST("/decks", a.authn.RequireScope(store.ScopeWrite), a.createDeck)
	// Bulk deck action (currently delete) over a set of decks; owner-only per row, dry_run counts without writing.
	v1.POST("/decks/bulk", a.authn.RequireScope(store.ScopeWrite), a.bulkDeleteDecks)
	v1.PATCH("/decks/:id", a.authn.RequireScope(store.ScopeWrite), a.updateDeck)
	v1.DELETE("/decks/:id", a.authn.RequireScope(store.ScopeWrite), a.deleteDeck)
	// Leave share: revoke your own grant row on this deck; a separate matter from deleting the deck, hence its own path.
	v1.DELETE("/decks/:id/membership", a.authn.RequireScope(store.ScopeWrite), a.leaveDeck)
	v1.GET("/decks/:id/notes", a.authn.RequireScope(store.ScopeRead), a.listNotes)
	v1.GET("/decks/:id/tags", a.authn.RequireScope(store.ScopeRead), a.listDeckTags)
	// Note type metadata: the stock definitions a type can be created from, and the answer controls
	// with their roles. Both are static, so they need the read scope but no role on any deck.
	v1.GET("/note-type-stocks", a.authn.RequireScope(store.ScopeRead), a.noteTypeStocks)
	v1.GET("/answer-controls", a.authn.RequireScope(store.ScopeRead), a.answerControls)
	// Note type CRUD under a deck. Reading needs the read scope; every write needs an editor role on the deck.
	v1.GET("/decks/:id/note-types", a.authn.RequireScope(store.ScopeRead), a.listNoteTypes)
	v1.GET("/decks/:id/note-types/:type_id", a.authn.RequireScope(store.ScopeRead), a.getNoteType)
	v1.POST("/decks/:id/note-types", a.authn.RequireScope(store.ScopeWrite), a.createNoteType)
	v1.PATCH("/decks/:id/note-types/:type_id", a.authn.RequireScope(store.ScopeWrite), a.updateNoteType)
	v1.DELETE("/decks/:id/note-types/:type_id", a.authn.RequireScope(store.ScopeWrite), a.deleteNoteType)
	// Change a note type's notes in place, and the explicit empty-card cleanup. Both change content a user cannot
	// reconstruct, so both are editor-only and both take a dry_run plus a confirmation flag.
	v1.POST("/decks/:id/note-types/:type_id/change", a.authn.RequireScope(store.ScopeWrite), a.changeNoteType)
	v1.GET("/decks/:id/empty-cards", a.authn.RequireScope(store.ScopeRead), a.listEmptyCards)
	v1.POST("/decks/:id/empty-cards", a.authn.RequireScope(store.ScopeWrite), a.cleanupEmptyCards)
	// "One more batch": read today's new-card allowance, add to the caller's allowance for today; touches only personal progress and sits under review like reviewing.
	v1.GET("/decks/:id/extra-new", a.authn.RequireScope(store.ScopeReview), a.getExtraNew)
	v1.POST("/decks/:id/extra-new", a.authn.RequireScope(store.ScopeReview), a.postExtraNew)
	// Pause or resume a deck for the caller only; a study choice like extra-new, so it needs the review scope.
	v1.PUT("/decks/:id/paused", a.authn.RequireScope(store.ScopeReview), a.putDeckPaused)
	v1.POST("/decks/:id/notes", a.authn.RequireScope(store.ScopeWrite), a.importNotes)
	v1.PATCH("/notes/:id", a.authn.RequireScope(store.ScopeWrite), a.updateNote)
	v1.GET("/notes/:id", a.authn.RequireScope(store.ScopeRead), a.getNote)
	v1.DELETE("/notes/:id", a.authn.RequireScope(store.ScopeWrite), a.deleteNote)
	// gin builds a tree per method; the POST side has no /notes/:id, so /notes/bulk does not conflict with it.
	v1.POST("/notes/bulk", a.authn.RequireScope(store.ScopeWrite), a.bulkNotes)
	v1.GET("/review/due", a.authn.RequireScope(store.ScopeReview), a.dueCards)
	v1.POST("/review", a.authn.RequireScope(store.ScopeReview), a.submitReview)
	v1.POST("/cards/:id/bury", a.authn.RequireScope(store.ScopeReview), a.postCardBury)
	v1.PUT("/cards/:id/suspension", a.authn.RequireScope(store.ScopeReview), a.putCardSuspension)
	v1.DELETE("/cards/:id/suspension", a.authn.RequireScope(store.ScopeReview), a.deleteCardSuspension)
	v1.PUT("/notes/:id/suspension", a.authn.RequireScope(store.ScopeReview), a.putNoteSuspension)
	v1.DELETE("/notes/:id/suspension", a.authn.RequireScope(store.ScopeReview), a.deleteNoteSuspension)
	v1.GET("/stats/summary", a.authn.RequireScope(store.ScopeRead), a.statsSummary)
	// Card-type self-description: static metadata (no user state), for the frontend to render edit forms and review controls.
	v1.GET("/export", a.authn.RequireScope(store.ScopeRead), a.exportCards)
	v1.GET("/decks/:id/package", a.authn.RequireScope(store.ScopeRead), a.handleExportPackage)
	v1.POST("/decks/import", a.authn.RequireScope(store.ScopeWrite), a.handleImportPackage)
	// Import a deck package from a public HTTPS direct link: same service, same scope and CSRF rules as file import.
	v1.POST("/decks/import-url", a.authn.RequireScope(store.ScopeWrite), a.handleImportPackageURL)
	// Issue a one-time upload ticket: issuance needs write scope, the same auth stack as the other import entries.
	v1.POST("/decks/import-uploads", a.authn.RequireScope(store.ScopeWrite), a.handleCreateImportUpload)
	// Upload the package with a ticket: it does not sit behind the v1 group's auth middleware, the ticket in the path is itself the credential (auth happened at issuance).
	// The request does not depend on cookies, so it has no CSRF surface either.
	r.PUT(importUploadPathPrefix+":token", a.handleImportUpload)
	v1.GET("/keys", a.authn.RequireScope(store.ScopeKeys), a.listKeys)
	v1.POST("/keys", a.authn.RequireScope(store.ScopeKeys), a.createKey)
	v1.DELETE("/keys/:id", a.authn.RequireScope(store.ScopeKeys), a.deleteKey)
}

// pathPublicID reads the external id parameter from the path (an opaque UUID string); an empty string writes 404 and returns false immediately.
// Entity resolution is the caller's job via the matching store's ByPublicID; the routing layer only hands the external id over.
func pathPublicID(c *gin.Context, name string) (string, bool) {
	raw := strings.TrimSpace(c.Param(name))
	if raw == "" {
		abortNotFound(c)
		return "", false
	}
	return raw, true
}

// abortNotFound writes 404 not_found; the single exit for an unknown, empty or malformed external id.
func abortNotFound(c *gin.Context) {
	abortError(c, http.StatusNotFound, CodeNotFound, "")
}

// queryInt reads an integer query parameter; returns def when missing or invalid.
func queryInt(c *gin.Context, name string, def int) int {
	raw := c.Query(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

// ensureDefaultPreset returns the id of the user's default preset: when absent, store.EnsureDefaultPreset creates it.
// External callers creating a deck usually do not care about presets, so the server supplies a usable default.
// The preset name is a stable literal (store.DefaultPresetName), and the web entry shares the same backfill logic.
func (a *API) ensureDefaultPreset(ctx context.Context, userID uint64) (uint64, error) {
	list, err := store.EnsureDefaultPreset(ctx, a.db, userID)
	if err != nil {
		return 0, err
	}
	p := store.DefaultPreset(list)
	if p == nil {
		return 0, store.ErrNoDefaultPreset
	}
	return p.ID, nil
}
