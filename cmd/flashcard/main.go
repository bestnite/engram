// Command flashcard 是服务唯一入口：单二进制 + 子命令（DESIGN.md §10.2）。
// 依赖在这里显式装配（AGENTS.md §2.4）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"gorm.io/gorm"

	"example.com/flashcard/internal/api"
	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/config"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web"
)

// version 由构建时注入：-ldflags "-X main.version=<tag>"；未注入时为 dev。
var version = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		// 默认子命令是 serve，与 DESIGN.md §10.2 一致。
		return runServe(ctx)
	}
	switch args[0] {
	case "serve":
		return runServe(ctx)
	case "schema":
		return runSchema(ctx, args[1:])
	case "export":
		return runExport(args[1:])
	case "optimize":
		return runOptimize(args[1:])
	case "version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q: valid values are serve, schema sync, export, optimize, version", args[0])
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: flashcard <command>")
	fmt.Fprintln(os.Stderr, "  serve           start the HTTP server (default)")
	fmt.Fprintln(os.Stderr, "  schema sync     run AutoMigrate and the registered destructive migrations")
	fmt.Fprintln(os.Stderr, "  export          export the database (implemented in M4)")
	fmt.Fprintln(os.Stderr, "  optimize        run a parameter-optimisation job (implemented in M9)")
	fmt.Fprintln(os.Stderr, "  version         print the version string")
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// loadConfig 读取环境变量；数据库可用时再叠加 settings 表覆盖值（DESIGN.md §8.4 的优先级）。
func loadConfig(ctx context.Context, db *gorm.DB) (*config.Config, error) {
	cfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		return nil, err
	}
	// settings 表可能还不存在（首次启动尚未迁移），此时只用环境变量。
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
	return srv.Run(ctx)
}

// newWebServer 按配置装配 web 服务，并把认证依赖注入 web.Deps（M1-14）。
// runServe 与集成测试共用这一条装配路径，避免测试用的依赖与生产漂移。
func newWebServer(cfg *config.Config, db *gorm.DB, logger *slog.Logger) (*web.Server, error) {
	accounts, sessions, users, err := newAuthStack(cfg, db)
	if err != nil {
		return nil, err
	}
	// 审计（M1-10）与登录限流（M1-9）在此显式装配；两者都是无状态/内存态，单实例直接复用。
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		return nil, err
	}
	limiter := auth.NewLoginLimiter(auth.LimiterConfig{})
	// 对外 REST API（M4-2 鉴权 + M4-3 端点）：依赖齐备才挂载 /api/v1。
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
	})
	if err != nil {
		return nil, err
	}
	return web.New(cfg.Get(config.KeyHTTPAddr).Value, web.Deps{
		DB:     db,
		Logger: logger,
		SchemaVersion: func(ctx context.Context) (int, error) {
			return store.CurrentVersion(ctx, db)
		},
		Accounts:     accounts,
		Sessions:     sessions,
		Users:        users,
		Invites:      store.NewInviteStore(db),
		Decks:        store.NewDeckStore(db),
		Notes:        store.NewNoteStore(db),
		Auditor:      auditor,
		LoginLimiter: limiter,
		// BOOTSTRAP_ADMIN_EMAIL 预填引导页表单（DESIGN.md §4.1）。
		BootstrapAdminEmail: cfg.Get(config.KeyBootstrapAdminEmail).Value,
		API:                 apiSrv,
	})
}

// newAuthStack 构造认证相关的存储与服务：user/session store、账号服务、会话管理器。
//
// 密钥由 internal/config 统一读取（SESSION_SECRET 传会话签名；ENCRYPTION_KEY 是必需项，
// 由 config.Load 校验，M1 尚无消费方；BOOTSTRAP_ADMIN_EMAIL 交给 web.Deps）。
// 必需密钥缺失在 config.Load 阶段就以英文错误拒绝启动，这里不重复校验。
func newAuthStack(cfg *config.Config, db *gorm.DB) (*auth.AccountService, *auth.Manager, *store.UserStore, error) {
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)

	accounts, err := auth.NewAccountService(users, sessions, auth.DefaultPasswordHasher())
	if err != nil {
		return nil, nil, nil, err
	}
	manager, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{
		Secret: []byte(cfg.Get(config.KeySessionSecret).Value),
		// 生产必须 Secure（DESIGN.md §4.3）；本地 http 开发由 BASE_URL 的 scheme 决定，
		// 否则开发态浏览器/curl 不会回传 cookie，登录流程无法联调。
		Secure: strings.HasPrefix(cfg.Get(config.KeyBaseURL).Value, "https://"),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return accounts, manager, users, nil
}

func runSchema(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "sync" {
		return errors.New("usage: flashcard schema sync")
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

func runExport(args []string) error {
	return errors.New("export is not implemented yet (planned for M4)")
}

func runOptimize(args []string) error {
	return errors.New("optimize is not implemented yet (planned for M9)")
}

// parseBool 接受 "1"/"true"/"yes" 之类的常见写法；无法解析时按 false 处理。
func parseBool(raw string) bool {
	if raw == "" {
		return false
	}
	if v, err := strconv.ParseBool(raw); err == nil {
		return v
	}
	return false
}
