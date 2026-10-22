// Command engram is the service's single entry point: one binary + subcommands.
// Dependencies are assembled explicitly here (AGENTS.md §2.4).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/config"
	"git.nite07.com/nite/engram/internal/digest"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/mcp"
	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/retention"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web"
)

// version is injected at build time: -ldflags "-X main.version=<tag>"; devVersion when nothing was injected.
var version = devVersion

// devVersion is the value used when no build version was injected; the externally displayed version then falls back to the VCS
// revision from the Go build info, so local development and untagged builds still show which commit they correspond to.
const devVersion = "dev"

// displayVersion returns the externally displayed program version. The `engram version` subcommand and the frontend footer share
// this source, so the two never report different versions.
func displayVersion() string {
	info, _ := debug.ReadBuildInfo()
	return formatVersion(version, info)
}

// formatVersion combines the injected version with the Go build info into the displayed version:
//   - a release tag was injected (e.g. v0.1.4) → returned as-is;
//   - nothing injected (empty string or devVersion) → filled in from the VCS revision in the build info, shaped like dev (e223664),
//     plus a -dirty suffix when the worktree had uncommitted changes at build time;
//   - no VCS record in the build info (built outside a git worktree, or with -buildvcs=false) → returns devVersion.
func formatVersion(injected string, info *debug.BuildInfo) string {
	if injected != "" && injected != devVersion {
		return injected
	}
	revision, modified := vcsStamp(info)
	if revision == "" {
		return devVersion
	}
	short := revision
	if len(short) > 7 {
		short = short[:7]
	}
	if modified {
		short += "-dirty"
	}
	return devVersion + " (" + short + ")"
}

// vcsStamp takes the VCS revision and "whether the worktree had uncommitted changes at build time" from the Go build info.
// An empty revision means the build info has no VCS record (the caller falls back to devVersion accordingly).
func vcsStamp(info *debug.BuildInfo) (revision string, modified bool) {
	if info == nil {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return revision, modified
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		// The default subcommand is serve.
		return runServe(ctx)
	}
	switch args[0] {
	case "serve":
		return runServe(ctx)
	case "schema":
		return runSchema(ctx, args[1:])
	case "export":
		return runExport(args[1:])
	case "import":
		return runImport(args[1:])
	case "version":
		fmt.Println(displayVersion())
		return nil
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q: valid values are serve, schema sync, export, import, version", args[0])
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: engram <command>")
	fmt.Fprintln(os.Stderr, "  serve           start the HTTP server (default)")
	fmt.Fprintln(os.Stderr, "  schema sync     run AutoMigrate and the registered destructive migrations")
	fmt.Fprintln(os.Stderr, "  export          export a deck package: --deck N --package out.edeck [--user N]")
	fmt.Fprintln(os.Stderr, "  import          import a deck package: --package in.edeck --user <id|username> [--target ...] [--dry-run]")
	fmt.Fprintln(os.Stderr, "  version         print the version string")
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// loadConfig reads the environment variables; when the database is available, overrides from the settings table are layered on top (precedence).
func loadConfig(ctx context.Context, db *gorm.DB) (*config.Config, error) {
	cfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		return nil, err
	}
	// The settings table may not exist yet (first start, before the migration); then only environment variables are used.
	if db != nil && db.Migrator().HasTable(&store.Setting{}) {
		settings, err := store.LoadSettings(ctx, db)
		if err != nil {
			return nil, err
		}
		cfg, err = config.Load(os.LookupEnv, settings)
		if err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func runServe(ctx context.Context) error {
	logger := newLogger()
	slog.SetDefault(logger)

	envCfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		return err
	}
	db, err := store.Open(envCfg.Get(config.KeyDBDriver).Value, envCfg.Get(config.KeyDBDSN).Value)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(ctx, db)
	if err != nil {
		return err
	}

	if parseBool(cfg.Get(config.KeyAutoMigrate).Value) {
		applied, err := store.Sync(ctx, db, store.BuiltinMigrations)
		if err != nil {
			return err
		}
		logger.Info("schema synchronized", "migrations_applied", applied)
	}

	srv, err := newWebServer(cfg, db, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// History row reclamation: sessions, one-time tokens, expired invites and login fingerprints are judged expired only on the
	// read path and their rows are never deleted; media nobody references is never deleted either. So there has to be a periodic
	// reclamation layer (retention periods are in that package's comment).
	// It is deliberately placed outside the mail branch below — reclamation has nothing to do with "whether SMTP is configured",
	// while the reminder/digest workers are bound to mail delivery, and stuffing it in there would mean deployments without SMTP never reclaim.
	// Media reclamation shares the media directory with the web layer's upload/read; Store itself is stateless, so a separate instance is fine.
	mediaStore, err := media.New(cfg.Get(config.KeyMediaDir).Value, db)
	if err != nil {
		return err
	}
	retentionWorker, err := retention.New(retention.Deps{
		Sessions:     store.NewSessionStore(db),
		ActionTokens: store.NewActionTokenStore(db),
		Invites:      store.NewInviteStore(db),
		ShareInvites: store.NewDeckShareInviteStore(db),
		Fingerprints: store.NewLoginFingerprintStore(db),
		Media:        mediaStore,
		Logger:       logger,
	})
	if err != nil {
		return err
	}
	retentionWorker.Start(ctx)
	defer retentionWorker.Stop()
	// Mail outbox worker: starts with serve and stops gracefully on signal; skipped when not assembled.
	if mb := srv.Mail(); mb != nil {
		mb.Start(ctx)
		defer mb.Stop()
		// Review due reminder worker (C-class): an independent periodic scan that does not reuse the optimizer's single-concurrency jobs system.
		// Its copy goes through the language pack, so a translator is loaded separately here; with SMTP unconfigured the worker still starts but only logs.
		translator, err := i18n.New()
		if err != nil {
			return err
		}
		// Unsubscribe token service: the reminder is an optional type, so it carries a login-free one-click unsubscribe header.
		tokens, err := auth.NewActionTokenService(store.NewActionTokenStore(db))
		if err != nil {
			return err
		}
		// Mail templates: the periodic workers and the web layer share one lookup implementation,
		// otherwise you get hard-to-notice inconsistencies like "mail sent from the web UI has a template, reminder mail does not".
		templates := mail.StoreLookup(store.NewMailTemplateStore(db), logger)
		siteLocale := func() string {
			settings, err := store.LoadSettings(context.Background(), db)
			if err != nil {
				return ""
			}
			raw := strings.TrimSpace(settings["site.default_locale"])
			for _, code := range translator.SupportedCodes() {
				if code == raw {
					return code
				}
			}
			return ""
		}
		reminderWorker, err := reminder.New(reminder.Deps{
			DB:                db,
			Outbox:            mb,
			Translator:        translator,
			Logger:            logger,
			BaseURL:           cfg.Get(config.KeyBaseURL).Value,
			Tokens:            tokens,
			Templates:         templates,
			SiteDefaultLocale: siteLocale,
		})
		if err != nil {
			return err
		}
		reminderWorker.Start(ctx)
		defer reminderWorker.Stop()
		// Weekly study digest worker (C-class): also a periodic scan, but weekly, with every mail-receivable user as the
		// candidate set (due cards are not required) and its own dedup ledger digest_log, so it does not reuse the reminder worker.
		digestWorker, err := digest.New(digest.Deps{
			DB:                db,
			Outbox:            mb,
			Translator:        translator,
			Logger:            logger,
			BaseURL:           cfg.Get(config.KeyBaseURL).Value,
			Tokens:            tokens,
			Templates:         templates,
			SiteDefaultLocale: siteLocale,
		})
		if err != nil {
			return err
		}
		digestWorker.Start(ctx)
		defer digestWorker.Stop()
	}
	return srv.Run(ctx)
}

// newWebServer assembles the web service from the configuration and injects the auth dependencies into web.Deps.
// runServe and the integration tests share this single assembly path, so test dependencies do not drift from production.
func newWebServer(cfg *config.Config, db *gorm.DB, logger *slog.Logger) (*web.Server, error) {
	// Trusted proxy list: parsed and validated at startup, and an invalid entry refuses startup outright.
	// Empty by default = trust no proxy, so ClientIP() falls back to RemoteAddr.
	trustedProxies, err := cfg.TrustedProxies()
	if err != nil {
		return nil, err
	}
	accounts, sessions, users, err := newAuthStack(cfg, db)
	if err != nil {
		return nil, err
	}
	// Audit and login rate limiting are assembled explicitly here; both are stateless/in-memory, so a single instance reuses them directly.
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		return nil, err
	}
	limiter := auth.NewLoginLimiter(auth.LimiterConfig{})
	// Anonymous endpoint rate limiting: /register and /forgot-password share one
	// fixed-window counter of "5 per 15 minutes per IP and per target email", preventing anonymous mail bombing.
	anonLimiter := auth.NewAnonymousLimiter(auth.DefaultAnonRateLimit, auth.DefaultAnonRateWindow, nil)
	// Media storage: local directory + media metadata table; the directory comes from MEDIA_DIR.
	mediaStore, err := media.New(cfg.Get(config.KeyMediaDir).Value, db)
	if err != nil {
		return nil, err
	}
	// External REST API (auth + endpoints): /api/v1 is mounted only when the dependencies are complete.
	// Codec for sensitive settings: the master key comes from ENCRYPTION_KEY, and an invalid format refuses startup outright.
	secrets, err := store.NewSecretCodec(cfg.Get(config.KeyEncryptionKey).Value)
	if err != nil {
		return nil, err
	}
	// Mail outbox: SMTP configuration goes through the settings table (the password reuses the AES-GCM codec above), and the
	// worker is started and gracefully stopped by runServe. With SMTP unconfigured, Enqueue returns ErrNotConfigured.
	mailOutbox := mail.NewOutbox(mail.Deps{DB: db, Secrets: secrets, Logger: logger})
	// TOTP two-factor auth: the secret is stored encrypted with the same AES-GCM codec.
	totpService, err := auth.NewTOTPService(store.NewTOTPStore(db), secrets, "")
	if err != nil {
		return nil, err
	}
	apiSrv, err := api.New(api.Deps{
		DB:      db,
		Logger:  logger,
		Keys:    store.NewAPIKeyStore(db),
		Users:   users,
		Decks:   store.NewDeckStore(db),
		Notes:   store.NewNoteStore(db),
		Presets: store.NewPresetStore(db),
		Cards:   store.NewCardStore(db),
		Auditor: auditor,
		// Inline media inside deck packages: the byte root directory is the same as media storage.
		MediaRoot: mediaStore.Root(),
		// The upload ticket returns an absolute URL so agents can upload directly.
		BaseURL: cfg.Get(config.KeyBaseURL).Value,
	})
	if err != nil {
		return nil, err
	}
	// The built-in MCP server reuses the same api instance: tools call the same service methods as REST.
	mcpSrv, err := mcp.New(mcp.Deps{API: apiSrv, Logger: logger})
	if err != nil {
		return nil, err
	}
	// Parameter optimization job executor: single-concurrency worker + subprocess. Start is called before the web service starts
	// and the worker lives as long as the process; on startup it reclaims unfinished jobs left behind by the previous process.
	//
	// Production wiring: Command calls the Rust adapter directly (export the review log first, then exec), and Complete parses
	// the 21-dimensional weights the adapter writes and hands them to FinishOptimize to write back to the job and preset. The adapter
	// path is configurable (OPTIMIZER_PATH), defaulting to "next to the service binary -> repository build artifact".
	optimizer, err := jobs.NewOptimizer(jobs.OptimizerDeps{
		DB:     db,
		Logger: logger,
		Binary: cfg.Get(config.KeyOptimizerPath).Value,
	})
	if err != nil {
		return nil, err
	}
	logger.Info("optimizer adapter configured", "path", optimizer.Binary())
	jobRunner, err := jobs.New(jobs.Deps{
		DB:       db,
		Logger:   logger,
		Command:  optimizer.CommandBuilder(),
		Complete: optimizer.Complete,
	})
	if err != nil {
		return nil, err
	}
	webSrv, err := web.New(cfg.Get(config.KeyHTTPAddr).Value, web.Deps{
		DB:     db,
		Logger: logger,
		SchemaVersion: func(ctx context.Context) (int, error) {
			return store.CurrentVersion(ctx, db)
		},
		Accounts:     accounts,
		Sessions:     sessions,
		Users:        users,
		UserLocale:   web.SessionUserLocale,
		Invites:      store.NewInviteStore(db),
		Decks:        store.NewDeckStore(db),
		Notes:        store.NewNoteStore(db),
		Cards:        store.NewCardStore(db),
		Presets:      store.NewPresetStore(db),
		Auditor:      auditor,
		LoginLimiter: limiter,
		AnonLimiter:  anonLimiter,
		TOTP:         totpService,
		Media:        mediaStore,
		Secrets:      secrets,
		// Mail outbox: the admin panel reads delivery status from it, and the SMTP settings page and test connection are built on it too.
		Mail: mailOutbox,
		// OIDC: the identity store serves the link list and unlinking; BaseURL builds redirect_uri.
		Identities: store.NewIdentityStore(db),
		BaseURL:    cfg.Get(config.KeyBaseURL).Value,
		// BOOTSTRAP_ADMIN_EMAIL pre-fills the onboarding form.
		BootstrapAdminEmail: cfg.Get(config.KeyBootstrapAdminEmail).Value,
		// TRUSTED_PROXIES decides which proxies may rewrite ClientIP.
		TrustedProxies: trustedProxies,
		// The program version is injected into the SPA entry <head> and displayed by the frontend footer.
		Version: displayVersion(),
		API:     apiSrv,
		MCP:     mcpSrv,
		Jobs:    jobRunner,
	})
	if err != nil {
		return nil, err
	}
	// Notify the administrator after a job failure (D-class). The hook is attached before Start to avoid racing the worker.
	jobRunner.SetOnFailure(webSrv.NotifyJobFailed)
	jobRunner.SetOnSuccess(webSrv.NotifyOptimizeDone)
	jobRunner.Start(context.Background())
	return webSrv, nil
}

// newAuthStack constructs the auth-related stores and services: user/session store, account service, session manager.
//
// Secrets are read centrally by internal/config (SESSION_SECRET supplies session signing; ENCRYPTION_KEY is required and
// validated by config.Load, with no consumer yet in M1; BOOTSTRAP_ADMIN_EMAIL is handed to web.Deps).
// A missing required secret already refuses startup with an English error at config.Load, so it is not re-validated here.
func newAuthStack(cfg *config.Config, db *gorm.DB) (*auth.AccountService, *auth.Manager, *store.UserStore, error) {
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)

	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.DefaultPasswordHasher())
	if err != nil {
		return nil, nil, nil, err
	}
	manager, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{
		Secret: []byte(cfg.Get(config.KeySessionSecret).Value),
		// Must be Secure in production; for local http development it is decided by the BASE_URL scheme,
		// otherwise the dev browser/curl would not send the cookie back and the login flow could not be exercised.
		Secure: strings.HasPrefix(cfg.Get(config.KeyBaseURL).Value, "https://"),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return accounts, manager, users, nil
}

func runSchema(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "sync" {
		return errors.New("usage: engram schema sync")
	}
	logger := newLogger()
	cfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.Get(config.KeyDBDriver).Value, cfg.Get(config.KeyDBDSN).Value)
	if err != nil {
		return err
	}
	applied, err := store.Sync(ctx, db, store.BuiltinMigrations)
	if err != nil {
		return err
	}
	version, err := store.CurrentVersion(ctx, db)
	if err != nil {
		return err
	}
	logger.Info("schema synchronized", "migrations_applied", applied, "schema_version", version)
	return nil
}

// parseBool accepts common spellings like "1"/"true"/"yes"; unparseable values are treated as false.
func parseBool(raw string) bool {
	if raw == "" {
		return false
	}
	if v, err := strconv.ParseBool(raw); err == nil {
		return v
	}
	return false
}
