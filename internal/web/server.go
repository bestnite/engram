// Package web assembles the gin routes, middleware and graceful shutdown.
// Login, CSRF, views and the rest belong to later milestones; at M0 this package only provides a bootable HTTP skeleton and /healthz.
package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/mcp"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
)

// Deps holds the explicitly wired dependencies (AGENTS.md §2.4: explicit wiring at the entry point, no container).
type Deps struct {
	DB     *gorm.DB
	Logger *slog.Logger
	// SchemaVersion returns the applied schema version, for /healthz to report.
	SchemaVersion func(ctx context.Context) (int, error)
	// Assets is the list of embedded static resources; when empty, New loads it from embed.
	Assets *Assets
	// SPA is the embedded SPA static resources and entry list; when empty, New loads it from frontend.FS().
	Shell *Shell
	// Translator is the i18n localizer factory; when empty, New loads it from the embedded language packs.
	Translator *i18n.Translator
	// UserLocale returns the user language setting of the current request (may be an empty string). There is no session at M0; it arrives with M1.
	UserLocale func(c *gin.Context) string
	// Accounts provides creation and authentication of local accounts; it is wired into the auth routes together with Sessions and Users.
	Accounts *auth.AccountService
	// Sessions provides the two middlewares, session resolution and CSRF; the login/logout flow builds on it.
	Sessions *auth.Manager
	// Users decides whether an admin already exists, which decides whether the /setup bootstrap is reachable.
	Users *store.UserStore
	// Invites provides invite creation/listing/revocation/acceptance; the registration flow depends on it when the policy is invite.
	Invites *store.InviteStore
	// Decks provides deck reads and ownership checks, for the card list/edit pages.
	Decks *store.DeckStore
	// Notes provides note queries, updates and bulk operations, for the card list/edit pages.
	Notes *store.NoteStore
	// Cards provides card reads, for the review page to take and render a card.
	Cards *store.CardStore
	// Presets provides scheduling preset reads, for the review page to build the FSRS scheduler.
	Presets *store.PresetStore
	// Grants is the deck grant store; when empty, New builds it from the DB.
	Grants *store.GrantStore
	// Auditor is the single audit exit for all writes; when empty, nothing is audited.
	Auditor *auth.Auditor
	// LoginLimiter provides the escalating delay after failed logins; when empty, login is not rate limited.
	LoginLimiter *auth.LoginLimiter
	// AnonLimiter applies a fixed-window rate limit to anonymous entry points such as /register and /forgot-password,
	// keyed by IP and target email (5 per 15 minutes); when empty, these two entry points are not rate limited.
	AnonLimiter *auth.AnonymousLimiter
	// TOTP is the optional second factor for local accounts; when empty, login only checks the password and the TOTP routes are not exposed.
	TOTP *auth.TOTPService
	// BootstrapAdminEmail is the fallback email for the first admin in containerized deployments, prefilled into the /setup form.
	BootstrapAdminEmail string
	// API is the set of api/v1 handlers; mounted at /api/v1 when non-empty.
	API *api.API
	// MCP is the built-in MCP server; mounts streamable HTTP at /mcp when non-empty.
	MCP *mcp.Server
	// Media is the local media store; when non-empty it mounts uploads and the /media/<sha256> proxy.
	Media *media.Store
	// Secrets is the AES-GCM codec for sensitive settings; when non-empty the admin panel can write
	// encrypted OIDC secrets and the like, and only shows "configured / not configured".
	Secrets *store.SecretCodec
	// Jobs is the single-concurrency executor for background jobs; when non-empty the preset page can trigger parameter optimization
	// and the admin panel can list and cancel jobs; when empty the jobs page only renders an empty list.
	Jobs *jobs.Runner
	// Mail is the mail outbox and background worker; when non-empty the SMTP settings page works and the admin panel can
	// read delivery status; when empty the related features only show "not configured".
	Mail *mail.Outbox
	// Identities provides reading, linking and unlinking of external identities (OIDC); when empty, New builds it from the DB.
	Identities *store.IdentityStore
	// BaseURL is the site's public address (BASE_URL), used to build the OIDC redirect_uri; when empty it is derived from the request.
	BaseURL string
	// TrustedProxies holds the proxy addresses (IP or CIDR) allowed to rewrite ClientIP(), from the TRUSTED_PROXIES startup config.
	// Empty = no proxy is trusted, and ClientIP falls back to RemoteAddr.
	TrustedProxies []string
	// Version is the program version (a tag injected at build time; a dev build is dev + the short commit hash), injected into the SPA entry's
	// <head> for the frontend footer to show. When empty it is not injected and the footer shows no version.
	Version string
}

// Server holds the routes and the listen address.
type Server struct {
	addr   string
	logger *slog.Logger
	router *gin.Engine
	db     *gorm.DB
	// schemaVersion is injected by main, so web does not depend back on store's concrete implementation.
	schemaVersion func(ctx context.Context) (int, error)
	assets        *Assets
	shell         *Shell
	i18n          *i18n.Translator
	// coverageOverride lets tests inject a language pack set with a missing key, to verify the report page renders
	// <100% and names the missing key; empty in production, where the report goes through i18n.Coverage.
	coverageOverride func() []i18n.LocaleCoverage
	userLocale       func(c *gin.Context) string
	accounts         *auth.AccountService
	sessions         *auth.Manager
	users            *store.UserStore
	invites          *store.InviteStore
	decks            *store.DeckStore
	notes            *store.NoteStore
	cards            *store.CardStore
	presets          *store.PresetStore
	grants           *store.GrantStore
	// access is the permission decision shared by Web and REST/MCP (the single implementation is auth.DeckAccess).
	access       *auth.DeckAccess
	auditor      *auth.Auditor
	loginLimiter *auth.LoginLimiter
	// anonLimiter rate limits anonymous requests to /register and /forgot-password (both by IP and by target email).
	anonLimiter *auth.AnonymousLimiter
	// totp is the second-factor service for local accounts; when empty, login has no second step.
	totp           *auth.TOTPService
	bootstrapEmail string
	api            *api.API
	mcp            *mcp.Server
	media          *media.Store
	// secrets lets the admin panel write sensitive settings; when empty, writes are refused and only the status is shown.
	secrets *store.SecretCodec
	// frameMediaKeyMu/frameMediaKey cache the HMAC key of the frame media tokens after the first (lazily created) read,
	// so issuing a token on every card render does not hit the settings table. The mutex serialises first-use creation.
	frameMediaKeyMu sync.Mutex
	frameMediaKey   []byte
	// mediaSizeMu/mediaSize is the sampled cache of media usage for the health page: it avoids a full recursive scan on every request.
	mediaSizeMu sync.Mutex
	mediaSize   *mediaSizeSample
	// jobRunner is the job entry point (enqueue, list, cancel); jobStore lets polling read state by id.
	// They are not merged into a single field: list and cancel only need the Runner, while polling reads the Store.
	jobRunner *jobs.Runner
	jobStore  *jobs.Store
	// mail is the mail outbox; cmd/engram starts and stops its worker.
	mail *mail.Outbox
	// mailer is the single path for composing and sending mail.
	mailer *mail.Mailer
	// tokens issues and consumes one-time action tokens: password reset, email verification, email change confirmation.
	tokens *auth.ActionTokenService
	// fingerprints records login fingerprints, for "new device / new IP login alerts".
	fingerprints *store.LoginFingerprintStore
	// mailTemplates holds admin-defined mail templates; a missing one falls back to the built-in body.
	mailTemplates *store.MailTemplateStore
	// shareInvites holds pending deck sharing invites (L3 consent model); sharePolicy is the "who may share with me" policy.
	shareInvites *store.DeckShareInviteStore
	sharePolicy  *store.SharePolicyStore
	// identities / identityLink are the OIDC linking capabilities: the store serves unlinking and listing, the service runs the three branches of identity linking.
	identities   *store.IdentityStore
	identityLink *auth.IdentityLinkService
	// oidc is the protocol client (discovery document cache + state table); baseURL is used to build redirect_uri.
	oidc    *auth.OIDCClient
	baseURL string
}

// secureCookies reports whether the site is served over https, based on the scheme of BASE_URL.
// In production it is "Caddy terminates TLS → the app only receives plain http", so never look at the request's own TLS state,
// otherwise cookies would lose Secure in production.
func (s *Server) secureCookies() bool {
	return strings.HasPrefix(s.baseURL, "https://")
}

// New constructs the HTTP service. addr is the listen address; the fields in deps must all be filled in.
func New(addr string, deps Deps) (*Server, error) {
	if deps.DB == nil {
		return nil, errors.New("web: Deps.DB is required")
	}
	if deps.SchemaVersion == nil {
		return nil, errors.New("web: Deps.SchemaVersion is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	assets := deps.Assets
	if assets == nil {
		// Embedded resources are constants after package initialization, so a resource missing at build time only makes its URL an empty string.
		var err error
		if assets, err = LoadAssets(); err != nil {
			return nil, err
		}
	}
	// The SPA is the only page rendering layer (the SSR page layer has been deleted): when an embedded resource is missing, startup fails
	// and there is no longer a "degrade back to server-rendered pages" path.
	shell := deps.Shell
	if shell == nil {
		var err error
		if shell, err = LoadShell(); err != nil {
			return nil, err
		}
	}
	if shell == nil {
		return nil, errors.New("web: the embedded SPA is required")
	}
	// Injects the content-hashed URL of the self-hosted MathJax into the SPA entry, for the frontend loader to bring in as a same-origin external script
	// (CSP script-src 'self' already allows it, no inline script needed). When the resource is missing the
	// URL is empty, nothing is injected, and the frontend loader skips loading it.
	shell.SetMathJaxURL(assets.URL("js/mathjax/tex-chtml-full.js"))
	// PWA shell: manifest, theme-color, icons, registration script and theme bootstrap are all injected into the
	// SPA entry <head>. Icons use content-hashed URLs; the theme bootstrap uses the compile-time constant themeBootstrap, the same source as the CSP's
	// script-src hash allowlist, so changing it requires changing the policy in step. The manifest and /pwa.js are stable URLs
	// that must be fetchable before login too (registerPWARoutes).
	shell.SetShell(ShellHTML{
		ManifestURL:       manifestPath,
		ThemeColor:        "#18181b",
		IconURL:           assets.URL("icons/icon.svg"),
		AppleTouchIconURL: assets.URL("icons/apple-touch-icon.png"),
		ScriptURL:         pwaScriptPath,
		ThemeBootstrap:    themeBootstrap,
	})
	// The program version is injected into the SPA entry <head> and read by the frontend footer; when empty it is not injected.
	shell.SetVersion(deps.Version)
	translator := deps.Translator
	if translator == nil {
		var err error
		if translator, err = i18n.New(); err != nil {
			return nil, err
		}
	}

	s := &Server{
		addr:           addr,
		logger:         logger,
		db:             deps.DB,
		schemaVersion:  deps.SchemaVersion,
		assets:         assets,
		shell:          shell,
		i18n:           translator,
		userLocale:     deps.UserLocale,
		accounts:       deps.Accounts,
		sessions:       deps.Sessions,
		users:          deps.Users,
		invites:        deps.Invites,
		decks:          deps.Decks,
		notes:          deps.Notes,
		cards:          deps.Cards,
		presets:        deps.Presets,
		auditor:        deps.Auditor,
		loginLimiter:   deps.LoginLimiter,
		anonLimiter:    deps.AnonLimiter,
		totp:           deps.TOTP,
		bootstrapEmail: deps.BootstrapAdminEmail,
		api:            deps.API,
		mcp:            deps.MCP,
		media:          deps.Media,
		secrets:        deps.Secrets,
		jobRunner:      deps.Jobs,
		jobStore:       jobs.NewStore(deps.DB),
		mail:           deps.Mail,
	}
	// Class A transactional security mail: the delivery helper is only built when the outbox is wired; the token and fingerprint stores are always available.
	tokens, err := auth.NewActionTokenService(store.NewActionTokenStore(deps.DB))
	if err != nil {
		return nil, err
	}
	s.tokens = tokens
	s.fingerprints = store.NewLoginFingerprintStore(deps.DB)
	s.mailTemplates = store.NewMailTemplateStore(deps.DB)
	s.setMailOutbox(deps.Mail, deps.BaseURL)
	s.shareInvites = store.NewDeckShareInviteStore(deps.DB)
	s.sharePolicy = store.NewSharePolicyStore(deps.DB)
	// The grant store can be built from the DB on demand; the decision engine is only wired when the deck store is complete too.
	s.grants = deps.Grants
	if s.grants == nil {
		s.grants = store.NewGrantStore(deps.DB)
	}
	if deps.Decks != nil {
		s.access = auth.NewDeckAccess(deps.Decks, s.grants)
	}

	// OIDC wiring: the protocol client is always present; the linking service needs the account and user stores to be complete.
	s.oidc = auth.NewOIDCClient(nil)
	s.baseURL = strings.TrimRight(strings.TrimSpace(deps.BaseURL), "/")
	s.identities = deps.Identities
	if s.identities == nil {
		s.identities = store.NewIdentityStore(deps.DB)
	}
	if s.accounts != nil && s.users != nil {
		link, err := auth.NewIdentityLinkService(
			auth.NewStoreIdentityLinkStore(s.identities, s.users, s.accounts), s.auditor, logger)
		if err != nil {
			return nil, err
		}
		s.identityLink = link
	}

	// Release mode: gin's own debug logging duplicates our slog middleware, so the former is turned off.
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	// By default gin trusts every proxy (trustedProxies = 0.0.0.0/0, ::/0) and takes X-Forwarded-For /
	// X-Real-IP unconditionally, which would let headers forge the login rate-limit key, the audit IP and the new-device alerts, so we narrow it explicitly to the configured trusted
	// proxy list: an empty list means no trust at all, and ClientIP falls back to RemoteAddr.
	// An illegal entry fails construction here too, never silently ignored.
	if err := router.SetTrustedProxies(deps.TrustedProxies); err != nil {
		return nil, fmt.Errorf("web: set trusted proxies: %w", err)
	}
	// The trusted proxy config is logged at startup, so the first look when investigating "we cannot get the real IP" confirms whether it took effect.
	if len(deps.TrustedProxies) == 0 {
		logger.Info("client ip resolution: no trusted proxies, forwarded headers are ignored")
	} else {
		logger.Info("client ip resolution: trusting forwarded headers from proxies", "trusted_proxies", strings.Join(deps.TrustedProxies, ","))
	}
	router.Use(requestLogger(logger), recovery(logger), forwardedIPWarner(logger, deps.TrustedProxies))
	// Security headers are mounted globally, covering static assets, /media, /api/v1, /mcp and the 404 fallback;
	// they sit before session/locale so that any response aborted early still carries the full set. The CSP is enforcing, not report-only.
	router.Use(securityHeaders())
	// The session middleware comes before the locale middleware: the language from personal settings takes part in the i18n resolution precedence,
	// and it can only be read from an already resolved session user. Session resolution itself does not depend on the language, so the order is safe.
	if s.sessions != nil {
		router.Use(s.sessions.Middleware())
	}
	router.Use(s.localeMiddleware())
	router.GET("/healthz", s.healthz)
	router.GET("/", s.homeRoute)
	router.GET(staticPathPrefix+":hash/*filepath", s.assets.Serve)
	// MathJax loads its CHTML web fonts by a path relative to the script URL; the fonts cannot carry one shared content
	// hash, so the whole MathJax tree is also served at this stable path (configured as MathJax's fontURL).
	router.GET("/static/mathjax/*filepath", s.assets.ServeTree("js/mathjax/"))
	router.GET("/assets/*filepath", s.shell.ServeAsset)
	// The review iframe page: a separate, minimal document owned by the frame runtime, not the SPA shell. Its version segment only
	// busts the iframe cache; see registerCardFrameRoutes.
	s.registerCardFrameRoutes(router)
	// Frame media: the sandboxed frame has no session, so it loads media through HMAC-signed /fm/<token>/<name> URLs.
	s.registerFrameMediaRoutes(router)
	if deps.Sessions != nil && deps.Users != nil {
		// The SPA profile API is only registered when the session dependencies are complete, and writes stay protected by the session CSRF middleware.
		profile := router.Group("/api/v1")
		profile.GET("/profile", s.profileGet)
		profile.PATCH("/profile", deps.Sessions.CSRFMiddleware(), s.profilePatch)
		profile.PATCH("/settings/locale", deps.Sessions.CSRFMiddleware(), s.localePatch)
		// A user's own external identities: reading is a plain authenticated read, binding and unlinking are credential changes.
		profile.GET("/settings/identities", s.identityList)
		profile.POST("/settings/identities/bind", deps.Sessions.CSRFMiddleware(), s.identityBindStart)
		profile.DELETE("/settings/identities/:id", deps.Sessions.CSRFMiddleware(), s.identityUnlink)
		if deps.Accounts != nil {
			profile.PATCH("/settings/password", deps.Sessions.CSRFMiddleware(), s.passwordPatch)
		}
	}
	// PWA shell: the manifest and the service worker are public stable routes that must be fetchable before login too.
	s.registerPWARoutes(router)
	s.registerAuthRoutes(router)
	s.registerDeckRoutes(router)
	s.registerDeckQueueCountRoute(router)
	s.registerDeckSettingsRoutes(router)
	s.registerDeckSettingsAPIRoutes(router)
	// Two-step verification management: the page is /settings/security (app shell), reads and writes go through /api/v1/settings/totp*.
	s.registerTOTPAPIRoutes(router)
	// Email notification preferences: the page is /settings/email (app shell), reads and writes go through /api/v1/settings/notifications.
	s.registerMailPrefsAPIRoutes(router)
	s.registerPackageWebRoutes(router)
	s.registerNoteRoutes(router)
	s.registerSharingRoutes(router)
	s.registerCloneRoutes(router)
	s.registerMediaRoutes(router)
	s.registerReviewRoutes(router)
	s.registerPresetRoutes(router)
	s.registerSettingsRoutes(router)
	// Class A transactional security mail: the pages and trigger points for password reset, email verification and email change confirmation.
	s.registerSecurityMailRoutes(router)
	// One-click unsubscribe (RFC 8058): a login-free one-click unsubscribe endpoint (for optional types only).
	s.registerUnsubscribeRoutes(router)
	s.registerStatsRoutes(router)
	s.registerAdminRoutes(router)
	// Unknown path fallback: every request that matches no route lands on the designed 404 page (not_found.go).
	s.registerNotFoundRoute(router)

	if s.api != nil {
		// The REST API's auth middleware handles the session/bearer dual channel itself, so it only needs to sit after the global session middleware.
		s.api.Register(router)
		// SPA answering only accepts the session cookie, and explicitly validates the session-bound CSRF outside the API group.
		if s.sessions != nil {
			router.POST("/api/v1/review/answer", s.sessions.CSRFMiddleware(), s.reviewAnswer)
			// Answer types go through the grading entry point: the server grades and writes grade_source=typed.
			router.POST("/api/v1/review/grade", s.sessions.CSRFMiddleware(), s.reviewGrade)
			// Bury and card render: write only the caller's own progress / read the card face; still web endpoints protected by session + CSRF,
			// with bury's scheduling logic reusing internal/schedule.
			router.POST("/api/v1/review/bury", s.sessions.CSRFMiddleware(), s.reviewBury)
			router.POST("/api/v1/review/suspend", s.sessions.CSRFMiddleware(), s.reviewSuspend)
			// Undo the grade just submitted: restore card_states and delete the undone reviews row (schedule.Rollback),
			// and the response explicitly carries undone_card_id, from which the frontend returns to the card just undone.
			router.POST("/api/v1/review/undo", s.sessions.CSRFMiddleware(), s.reviewUndo)
			router.POST("/api/v1/review/render", s.sessions.CSRFMiddleware(), s.reviewRender)
		}
	}
	if s.mcp != nil && s.api != nil {
		// MCP reuses the same authentication; once it passes, the identity is injected into the request context and handed to the streamable handler.
		g := router.Group("")
		g.Use(s.api.AuthMiddleware())
		g.POST("/mcp", s.mcpEndpoint)
		g.GET("/mcp", s.mcpEndpoint)
		g.DELETE("/mcp", s.mcpEndpoint)
	}
	s.router = router
	return s, nil
}

// Handler returns the underlying handler, so tests can inject requests directly.
func (s *Server) Handler() http.Handler { return s.router }

// Mail returns the mail outbox; cmd/engram uses it to start and gracefully stop its background worker.
// nil when not wired.
func (s *Server) Mail() *mail.Outbox { return s.mail }

// setMailOutbox attaches the outbox and builds the process's one Mailer around it: the web layer sends through it and
// cmd/engram hands it to the periodic workers (Server.Mailer), so templates, site name, unsubscribe links and
// preference gates are wired exactly once. ob may be nil when mail is not wired.
func (s *Server) setMailOutbox(ob *mail.Outbox, baseURL string) {
	s.mail = ob
	md := mail.MailerDeps{
		DB:        s.db,
		Templates: mail.StoreLookup(s.mailTemplates, s.logger),
		Tokens:    s.tokens,
		BaseURL:   baseURL,
		Locales:   s.i18n.SupportedCodes(),
		Logger:    s.logger,
	}
	// A nil *Outbox stored in the interface would not compare equal to nil, so it is only set when present.
	if ob != nil {
		md.Outbox = ob
	}
	s.mailer = mail.NewMailer(md)
}

// Mailer returns the process's single mail.Mailer, for cmd/engram to hand to the review reminder and weekly digest workers.
func (s *Server) Mailer() *mail.Mailer { return s.mailer }

// Connection-level timeouts. Only request headers and idle connections are limited; there is no overall read/write timeout, because media uploads and deck package imports can have
// very large request bodies and streaming export responses can be very long, and an overall timeout would cut off legitimate slow connections.
const (
	// httpReadHeaderTimeout keeps connections that send headers slowly (slowloris) from holding a connection and a goroutine for a long time.
	httpReadHeaderTimeout = 10 * time.Second
	// httpIdleTimeout reclaims idle keep-alive connections.
	httpIdleTimeout = 2 * time.Minute
)

// newHTTPServer builds an http.Server with connection-level timeouts; Run and the tests share it, so what is tested is the shipped configuration.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}

// Run starts the HTTP service and shuts down gracefully when ctx is cancelled: it stops accepting new requests and waits at most 2 seconds.
func (s *Server) Run(ctx context.Context) error {
	srv := newHTTPServer(s.addr, s.router)
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("http server listening", "addr", s.addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	s.logger.Info("http server stopped")
	return nil
}

// healthResponse is the /healthz body; database and schema_version are the two required fields.
type healthResponse struct {
	Status        string `json:"status"`
	Database      string `json:"database"`
	SchemaVersion int    `json:"schema_version"`
}

func (s *Server) healthz(c *gin.Context) {
	ctx := c.Request.Context()
	status := "ok"
	code := http.StatusOK

	database := "ok"
	if sqlDB, err := s.db.DB(); err != nil {
		database = "error"
		status = "degraded"
		code = http.StatusServiceUnavailable
		s.logger.Error("health check: cannot get sql.DB", "error", err)
	} else if err := sqlDB.PingContext(ctx); err != nil {
		database = "error"
		status = "degraded"
		code = http.StatusServiceUnavailable
		s.logger.Error("health check: database ping failed", "error", err)
	}

	version := -1
	if v, err := s.schemaVersion(ctx); err != nil {
		status = "degraded"
		code = http.StatusServiceUnavailable
		s.logger.Error("health check: cannot read schema version", "error", err)
	} else {
		version = v
	}

	c.JSON(code, healthResponse{Status: status, Database: database, SchemaVersion: version})
}

// mcpEndpoint injects the authenticated user and API key into the request context, then hands it to MCP's
// streamable HTTP handler; scope filtering and call re-checks happen inside the mcp package under that identity.
func (s *Server) mcpEndpoint(c *gin.Context) {
	u, _ := api.CurrentUser(c)
	k, _ := api.CurrentAPIKey(c)
	ctx := mcp.WithIdentity(c.Request.Context(), u, k)
	s.mcp.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
}

// requestLogger logs every request with slog; logs are always in English (AGENTS.md §2.1).
func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}

// recovery catches a handler panic, logs one line in English and returns 500, so a single request cannot take the process down.
func recovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("panic recovered",
					"error", fmt.Sprint(r),
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"stack", string(debug.Stack()),
				)
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
