// Command engram 是服务唯一入口：单二进制 + 子命令（DESIGN.md §10.2）。
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
	case "import":
		return runImport(args[1:])
	case "version":
		fmt.Println(version)
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
	// 历史行回收：会话、一次性令牌、过期邀请、登录指纹都只在读路径上判过期，从不删行，
	// 所以要有一层周期回收（保留期见该包注释）。
	// 刻意放在下面的 mail 分支之外——回收与「SMTP 是否配置」无关，而提醒/周报两个 worker 是
	// 和邮件投递绑在一起的，若把它一并塞进去，未配置 SMTP 的部署就永远不回收。
	retentionWorker, err := retention.New(retention.Deps{
		Sessions:     store.NewSessionStore(db),
		ActionTokens: store.NewActionTokenStore(db),
		Invites:      store.NewInviteStore(db),
		Fingerprints: store.NewLoginFingerprintStore(db),
		Logger:       logger,
	})
	if err != nil {
		return err
	}
	retentionWorker.Start(ctx)
	defer retentionWorker.Stop()
	// 邮件 outbox worker（M1-17）：随 serve 启动、随信号优雅停止；未装配时跳过。
	if mb := srv.Mail(); mb != nil {
		mb.Start(ctx)
		defer mb.Stop()
		// 复习到期提醒 worker（M1-21，C 类）：独立的周期扫描，不复用优化器的单并发 jobs 体系。
		// 文案走语言包，因此这里单独加载一份翻译器；SMTP 未配置时 worker 仍启动但只记日志。
		translator, err := i18n.New()
		if err != nil {
			return err
		}
		// 退订令牌服务（M1-22）：提醒是可选类型，带上免登录的一键退订头。
		tokens, err := auth.NewActionTokenService(store.NewActionTokenStore(db))
		if err != nil {
			return err
		}
		reminderWorker, err := reminder.New(reminder.Deps{
			DB:         db,
			Outbox:     mb,
			Translator: translator,
			Logger:     logger,
			BaseURL:    cfg.Get(config.KeyBaseURL).Value,
			Tokens:     tokens,
		})
		if err != nil {
			return err
		}
		reminderWorker.Start(ctx)
		defer reminderWorker.Stop()
		// 每周学习摘要 worker（M1-23，C 类）：同为周期扫描，但节律是周、候选集是所有可收信
		// 用户（不要求有到期卡），去重台账是独立的 digest_log，故不复用提醒 worker。
		digestWorker, err := digest.New(digest.Deps{
			DB:         db,
			Outbox:     mb,
			Translator: translator,
			Logger:     logger,
			BaseURL:    cfg.Get(config.KeyBaseURL).Value,
			Tokens:     tokens,
		})
		if err != nil {
			return err
		}
		digestWorker.Start(ctx)
		defer digestWorker.Stop()
	}
	return srv.Run(ctx)
}

// newWebServer 按配置装配 web 服务，并把认证依赖注入 web.Deps（M1-14）。
// runServe 与集成测试共用这一条装配路径，避免测试用的依赖与生产漂移。
func newWebServer(cfg *config.Config, db *gorm.DB, logger *slog.Logger) (*web.Server, error) {
	// 可信代理列表（DESIGN.md §4.3、§11）：在启动期解析并校验，非法项直接拒绝启动。
	// 缺省为空 = 不信任任何代理，ClientIP() 回落到 RemoteAddr。
	trustedProxies, err := cfg.TrustedProxies()
	if err != nil {
		return nil, err
	}
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
	// 匿名入口限流（F13，DESIGN.md §4.3）：/register 与 /forgot-password 共用一套
	// 「IP 与目标邮箱各 5 次 / 15 分钟」的固定窗口计数，防匿名轰炸式发信。
	anonLimiter := auth.NewAnonymousLimiter(auth.DefaultAnonRateLimit, auth.DefaultAnonRateWindow, nil)
	// 媒体存储（M2-8）：本地目录 + media 元数据表；目录来自 MEDIA_DIR。
	mediaStore, err := media.New(cfg.Get(config.KeyMediaDir).Value, db)
	if err != nil {
		return nil, err
	}
	// 对外 REST API（M4-2 鉴权 + M4-3 端点）：依赖齐备才挂载 /api/v1。
	// 敏感设置的编解码器（M6-10）：主密钥来自 ENCRYPTION_KEY，格式非法直接拒绝启动。
	secrets, err := store.NewSecretCodec(cfg.Get(config.KeyEncryptionKey).Value)
	if err != nil {
		return nil, err
	}
	// 邮件 outbox（M1-17）：SMTP 配置走 settings 表（口令复用上面的 AES-GCM 编解码器），
	// worker 由 runServe 启动与优雅停止。未配置 SMTP 时 Enqueue 返回 ErrNotConfigured。
	mailOutbox := mail.NewOutbox(mail.Deps{DB: db, Secrets: secrets, Logger: logger})
	// TOTP 二次验证（M1-16）：secret 复用上面的 AES-GCM 编解码器加密存储。
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
		// 卡组包内联媒体（M5-6/M5-7）：字节根目录与媒体存储同一个。
		MediaRoot: mediaStore.Root(),
	})
	if err != nil {
		return nil, err
	}
	// 内置 MCP server（M4-6）复用同一 api 实例：工具直接调用与 REST 相同的 service 方法。
	mcpSrv, err := mcp.New(mcp.Deps{API: apiSrv, Logger: logger})
	if err != nil {
		return nil, err
	}
	// 参数优化作业执行器（M9-1）：单并发 worker + 子进程。Start 在 web 服务启动前调用，
	// worker 随进程存活；启动时会回收上次进程遗留的未完成作业（M9-7/M9-8）。
	//
	// M9-10 生产接线：Command 直接调用 Rust 适配器（先导出复习日志再 exec），Complete 解析
	// 适配器写出的 21 维权重并交给 FinishOptimize 写回 job 与 preset。适配器路径可配置
	// （OPTIMIZER_PATH），缺省按「服务二进制旁 -> 仓库构建产物」解析。
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
		// 邮件 outbox（M1-17）：管理面板读投递状态、SMTP 配置页与测试连接都基于它。
		Mail: mailOutbox,
		// OIDC（M1-11）：身份存储用于绑定列表与解绑；BaseURL 用于拼 redirect_uri。
		Identities: store.NewIdentityStore(db),
		BaseURL:    cfg.Get(config.KeyBaseURL).Value,
		// BOOTSTRAP_ADMIN_EMAIL 预填引导页表单（DESIGN.md §4.1）。
		BootstrapAdminEmail: cfg.Get(config.KeyBootstrapAdminEmail).Value,
		// TRUSTED_PROXIES 决定哪些代理可以改写 ClientIP()（DESIGN.md §4.3、§11）。
		TrustedProxies: trustedProxies,
		API:            apiSrv,
		MCP:            mcpSrv,
		Jobs:           jobRunner,
	})
	if err != nil {
		return nil, err
	}
	// M1-24：作业失败后通知管理员（D 类）。钩子在 Start 之前接好，避免与 worker 竞态。
	jobRunner.SetOnFailure(webSrv.NotifyJobFailed)
	jobRunner.Start(context.Background())
	return webSrv, nil
}

// newAuthStack 构造认证相关的存储与服务：user/session store、账号服务、会话管理器。
//
// 密钥由 internal/config 统一读取（SESSION_SECRET 传会话签名；ENCRYPTION_KEY 是必需项，
// 由 config.Load 校验，M1 尚无消费方；BOOTSTRAP_ADMIN_EMAIL 交给 web.Deps）。
// 必需密钥缺失在 config.Load 阶段就以英文错误拒绝启动，这里不重复校验。
func newAuthStack(cfg *config.Config, db *gorm.DB) (*auth.AccountService, *auth.Manager, *store.UserStore, error) {
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)

	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.DefaultPasswordHasher())
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
