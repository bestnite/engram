// Package web 组装 gin 路由、中间件与优雅关闭。
// 登录、CSRF、视图等属于后续里程碑；本包在 M0 只提供可启动的 HTTP 骨架与 /healthz。
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

// Deps 是显式装配的依赖（AGENTS.md §2.4：入口显式装配，不引入容器）。
type Deps struct {
	DB     *gorm.DB
	Logger *slog.Logger
	// SchemaVersion 返回已应用的 schema 版本，供 /healthz 报告。
	SchemaVersion func(ctx context.Context) (int, error)
	// Assets 是已嵌入的静态资源清单；为空时由 New 从 embed 加载。
	Assets *Assets
	// SPA 是已嵌入的 SPA 静态资源及入口清单；为空时由 New 从 frontend.FS() 加载。
	SPA *SPA
	// Translator 是 i18n 本地化器工厂；为空时由 New 从嵌入语言包加载。
	Translator *i18n.Translator
	// UserLocale 返回当前请求的用户语言设置（可为空串）。M0 尚无会话，M1 接入后提供。
	UserLocale func(c *gin.Context) string
	// Accounts 提供本地账号的创建与认证（M1-4）；与 Sessions、Users 一起接入认证路由。
	Accounts *auth.AccountService
	// Sessions 提供会话解析与 CSRF 两个中间件（M1-2、M1-3）；登录/登出流程基于它。
	Sessions *auth.Manager
	// Users 用于判断是否已存在管理员，决定 /setup 引导是否可达（M1-5）。
	Users *store.UserStore
	// Invites 提供邀请的创建/列出/撤销/接受（M1-7）；策略为 invite 时注册流程依赖它。
	Invites *store.InviteStore
	// Decks 提供卡组的读取与归属判断，供卡片列表/编辑页使用（M2-7）。
	Decks *store.DeckStore
	// Notes 提供 note 的查询、更新与批量操作，供卡片列表/编辑页使用（M2-7）。
	Notes *store.NoteStore
	// Cards 提供 card 的读取，供复习页取卡与渲染（M3-5）。
	Cards *store.CardStore
	// Presets 提供调度预设的读取，供复习页构造 FSRS 调度器（M3-5）。
	Presets *store.PresetStore
	// Grants 是卡组授权存储（M5-1）；为空时由 New 从 DB 构造。
	Grants *store.GrantStore
	// ShareLinks 是分享链接存储（M5-3）；为空时由 New 从 DB 构造。
	ShareLinks *store.ShareLinkStore
	// ShareSessions 是分享链接打开的卡组的会话级授权（L3）；为空时由 New 从 DB 构造。
	ShareSessions *store.ShareSessionStore
	// Auditor 是全部写操作的统一审计出口（M1-10）；为空时不写审计。
	Auditor *auth.Auditor
	// LoginLimiter 提供登录失败的递增延迟（M1-9）；为空时登录不做限流。
	LoginLimiter *auth.LoginLimiter
	// AnonLimiter 对 /register 与 /forgot-password 这类匿名入口按 IP 与目标邮箱做固定
	// 窗口限流（5 次 / 15 分钟）；为空时这两个入口不做限流。
	AnonLimiter *auth.AnonymousLimiter
	// TOTP 是本地账号的可选二次因素（M1-16）；为空时登录只校验密码，不暴露 TOTP 相关路由。
	TOTP *auth.TOTPService
	// BootstrapAdminEmail 是容器化部署时首个管理员的兜底邮箱，预填到 /setup 表单（DESIGN.md §4.1）。
	BootstrapAdminEmail string
	// API 是 /api/v1 的 handler 集合（M4-3）；非空时挂载到 /api/v1。
	API *api.API
	// MCP 是内置 MCP server（M4-6）；非空时在 /mcp 挂载 streamable HTTP。
	MCP *mcp.Server
	// Media 是本地媒体存储（M2-8）；非空时挂载上传与 /media/<sha256> 代理。
	Media *media.Store
	// Secrets 是敏感设置的 AES-GCM 编解码器（M6-10）；非空时管理面板可写入
	// 加密的 OIDC secret 等，且只显示「已配置/未配置」。
	Secrets *store.SecretCodec
	// Jobs 是后台作业的单并发执行器（M9-1）；非空时预设页可触发参数优化（M9-4），
	// 管理面板也可列出作业并取消（M6-6）；为空时作业页只渲染空列表。
	Jobs *jobs.Runner
	// Mail 是邮件 outbox 与后台 worker（M1-17）；非空时 SMTP 配置页可用，管理面板可
	// 读到投递状态；为空时相关能力只显示"未配置"。
	Mail *mail.Outbox
	// Identities 提供外部身份（OIDC）的读取、绑定与解绑（M1-11、M6-4）；为空时由 New 从 DB 构造。
	Identities *store.IdentityStore
	// BaseURL 是站点对外地址（BASE_URL），用于拼 OIDC redirect_uri；为空时按请求推导。
	BaseURL string
	// TrustedProxies 是允许改写 ClientIP() 的代理地址（IP 或 CIDR），来自启动配置 TRUSTED_PROXIES。
	// 为空 = 不信任任何代理，ClientIP() 回落到 RemoteAddr（DESIGN.md §4.3、§11）。
	TrustedProxies []string
}

// Server 持有路由与监听地址。
type Server struct {
	addr   string
	logger *slog.Logger
	router *gin.Engine
	db     *gorm.DB
	// schemaVersion 由 main 注入，避免 web 反向依赖 store 的具体实现。
	schemaVersion func(ctx context.Context) (int, error)
	assets        *Assets
	spa           *SPA
	i18n          *i18n.Translator
	// coverageOverride 供测试注入一份「缺 key」的语言包集合，验证 M8-4 报告页会渲染
	// <100% 并点名缺失的 key；生产为空，报告走 i18n.Coverage。
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
	shareLinks       *store.ShareLinkStore
	// shareSessions 记录「本会话通过分享链接打开过哪些卡组」（L3）；媒体读取据此放行。
	shareSessions *store.ShareSessionStore
	// access 是 Web 与 REST/MCP 共用的权限判定（M5-1，单一实现见 auth.DeckAccess）。
	access       *auth.DeckAccess
	auditor      *auth.Auditor
	loginLimiter *auth.LoginLimiter
	// anonLimiter 限流 /register 与 /forgot-password 的匿名请求（IP + 目标邮箱双维度）。
	anonLimiter *auth.AnonymousLimiter
	// totp 是本地账号的二次因素服务（M1-16）；为空时登录不含第二步。
	totp           *auth.TOTPService
	bootstrapEmail string
	api            *api.API
	mcp            *mcp.Server
	media          *media.Store
	// secrets 供管理面板写入敏感设置（M6-10）；为空时拒绝写入，只显示状态。
	secrets *store.SecretCodec
	// mediaSizeMu/mediaSize 是健康页媒体占用的采样缓存（M6-8）：避免每次请求都全量递归扫描。
	mediaSizeMu sync.Mutex
	mediaSize   *mediaSizeSample
	// jobRunner 是作业入口（M9-4 入队、M6-6 列表与取消）；jobStore 供轮询按 id 读状态。
	// 不合并成一个字段：列表与取消只需 Runner，而轮询读的是 Store。
	jobRunner *jobs.Runner
	jobStore  *jobs.Store
	// mail 是邮件 outbox（M1-17）；由 cmd/engram 启动/停止其 worker。
	mail *mail.Outbox
	// securityMail 是 A 类事务安全邮件的入站投递助手（M1-19）。
	securityMail *mail.SecurityNotifier
	// tokens 签发/消费一次性动作令牌（M1-19）：密码重置、邮箱验证、改邮箱确认。
	tokens *auth.ActionTokenService
	// fingerprints 记录登录指纹，用于「新设备 / 新 IP 登录提醒」（M1-19）。
	fingerprints *store.LoginFingerprintStore
	// identities / identityLink 是 OIDC 绑定能力（M1-11）：store 供解绑与列表，service 走 §4.5 三分支。
	identities   *store.IdentityStore
	identityLink *auth.IdentityLinkService
	// oidc 是协议客户端（发现文档缓存 + state 表）；baseURL 用于拼 redirect_uri。
	oidc    *auth.OIDCClient
	baseURL string
}

// secureCookies 报告站点是否经 https 提供，依据 BASE_URL 的 scheme（DESIGN.md §4.3、§4.4）。
// 生产是「Caddy 终止 TLS → 应用只收到明文 http」，所以绝不能看请求自身的 TLS 状态，
// 否则线上 cookie 会丢掉 Secure。
func (s *Server) secureCookies() bool {
	return strings.HasPrefix(s.baseURL, "https://")
}

// New 构造 HTTP 服务。addr 是监听地址，deps 里的字段必须齐备。
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
		// 嵌入资源在包初始化后即为常量，构建期缺失的资源只会让对应 URL 为空串。
		var err error
		if assets, err = LoadAssets(); err != nil {
			return nil, err
		}
	}
	spa := deps.SPA
	if spa == nil {
		var err error
		if spa, err = LoadSPA(); err != nil {
			return nil, err
		}
	}
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
		spa:            spa,
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
	// A 类事务安全邮件（M1-19）：outbox 装配时才构造投递助手；令牌与指纹存储总是可用。
	tokens, err := auth.NewActionTokenService(store.NewActionTokenStore(deps.DB))
	if err != nil {
		return nil, err
	}
	s.tokens = tokens
	s.fingerprints = store.NewLoginFingerprintStore(deps.DB)
	if deps.Mail != nil {
		s.securityMail = mail.NewSecurityNotifier(deps.Mail, store.NewEmailPrefStore(deps.DB), logger)
	}
	// 授权存储可按需从 DB 构造；只有卡组存储也齐备时才装配判定器（M5-1）。
	s.grants = deps.Grants
	if s.grants == nil {
		s.grants = store.NewGrantStore(deps.DB)
	}
	s.shareLinks = deps.ShareLinks
	if s.shareLinks == nil {
		s.shareLinks = store.NewShareLinkStore(deps.DB)
	}
	// L3：分享链接打开的卡组计入会话可见集合，媒体读取据此放行。同样允许从 DB 构造。
	s.shareSessions = deps.ShareSessions
	if s.shareSessions == nil {
		s.shareSessions = store.NewShareSessionStore(deps.DB)
	}
	if deps.Decks != nil {
		s.access = auth.NewDeckAccess(deps.Decks, s.grants)
	}

	// OIDC 装配（M1-11）：协议客户端常驻；绑定服务需要账号与用户存储齐备。
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

	// 发布模式：gin 自带的调试日志与我们的 slog 中间件重复，关掉前者。
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	// gin 默认信任所有代理（trustedProxies = 0.0.0.0/0、::/0），会无条件采信 X-Forwarded-For /
	// X-Real-IP。那会让登录限流键、审计 IP 与新设备提醒被请求头伪造，因此显式收敛到配置的可信
	// 代理列表：空列表即完全不信任，ClientIP() 回落到 RemoteAddr（DESIGN.md §4.3、§11）。
	// 非法项在此也会让构造失败，绝不静默忽略。
	if err := router.SetTrustedProxies(deps.TrustedProxies); err != nil {
		return nil, fmt.Errorf("web: set trusted proxies: %w", err)
	}
	router.Use(requestLogger(logger), recovery(logger))
	// 安全响应头（F26）挂在全局，覆盖静态资源、/media、/api/v1、/mcp 与 404 回退；
	// 放在 session/locale 之前，保证任何提前中止的响应也带齐这组头。CSP 只上报不阻断。
	router.Use(securityHeaders())
	// 会话中间件先于语言中间件：个人设置里的语言（M1-8）要参与 i18n 解析优先级，
	// 而它只能从已解析的会话用户读取。会话解析本身不依赖语言，先后次序安全。
	if s.sessions != nil {
		router.Use(s.sessions.Middleware())
	}
	router.Use(s.localeMiddleware())
	router.GET("/healthz", s.healthz)
	router.GET("/", s.home)
	router.GET(staticPathPrefix+":hash/*filepath", s.assets.Serve)
	if s.spa != nil {
		router.GET("/assets/*filepath", s.spa.ServeAsset)
	}
	if deps.Sessions != nil && deps.Users != nil {
		// SPA 个人资料 API 仅在会话依赖齐备时注册，写请求继续由会话 CSRF 中间件保护。
		profile := router.Group("/api/v1")
		profile.GET("/profile", s.spaProfileGet)
		profile.PATCH("/profile", deps.Sessions.CSRFMiddleware(), s.spaProfilePatch)
		profile.PATCH("/settings/locale", deps.Sessions.CSRFMiddleware(), s.spaLocalePatch)
		if deps.Accounts != nil {
			profile.PATCH("/settings/password", deps.Sessions.CSRFMiddleware(), s.spaPasswordPatch)
		}
	}
	// PWA 外壳（M8-2）：manifest 与 service worker 是公开的稳定路由，登录前也需可取。
	s.registerPWARoutes(router)
	s.registerAuthRoutes(router)
	s.registerDeckRoutes(router)
	s.registerSPADeckQueueCountRoute(router)
	s.registerDeckSettingsRoutes(router)
	s.registerPackageWebRoutes(router)
	s.registerNoteRoutes(router)
	s.registerSharingRoutes(router)
	s.registerShareBrowseRoutes(router)
	s.registerCloneRoutes(router)
	s.registerMediaRoutes(router)
	s.registerReviewRoutes(router)
	s.registerPresetRoutes(router)
	s.registerSettingsRoutes(router)
	// M1-19 A 类事务安全邮件：密码重置、邮箱验证与改邮箱确认的页面与触发点。
	s.registerSecurityMailRoutes(router)
	// M1-22 一键退订（RFC 8058）：免登录的一键退订端点（可选类型专用）。
	s.registerUnsubscribeRoutes(router)
	s.registerStatsRoutes(router)
	s.registerAdminRoutes(router)
	// 未知路径回退：所有未命中路由的请求落到设计过的 404 页面（not_found.go）。
	s.registerNotFoundRoute(router)

	if s.api != nil {
		// REST API 的鉴权中间件内部自行处理会话/bearer 双通道，挂在全局会话中间件之后即可。
		s.api.Register(router)
		// SPA 答题只接受会话 cookie，并在 API 组之外显式校验会话绑定的 CSRF。
		if s.sessions != nil {
			router.POST("/api/v1/review/answer", s.sessions.CSRFMiddleware(), s.spaReviewAnswer)
			// 作答类题型走判分入口：服务端判分并写 grade_source=typed（DESIGN.md §6.2、§8.2）。
			router.POST("/api/v1/review/grade", s.sessions.CSRFMiddleware(), s.spaReviewGrade)
		}
	}
	if s.mcp != nil && s.api != nil {
		// MCP 复用同一套鉴权（DESIGN.md §7.4）；鉴权后把身份注入请求上下文再交给 streamable handler。
		g := router.Group("")
		g.Use(s.api.AuthMiddleware())
		g.POST("/mcp", s.mcpEndpoint)
		g.GET("/mcp", s.mcpEndpoint)
		g.DELETE("/mcp", s.mcpEndpoint)
	}
	s.router = router
	return s, nil
}

// Handler 返回底层 handler，便于测试直接注入请求。
func (s *Server) Handler() http.Handler { return s.router }

// Mail 返回邮件 outbox（M1-17）；cmd/engram 借此启动与优雅停止其后台 worker。
// 未装配时为 nil。
func (s *Server) Mail() *mail.Outbox { return s.mail }

// Run 启动 HTTP 服务并在 ctx 取消时优雅关闭：停止接收新请求，最多等 2 秒。
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{Addr: s.addr, Handler: s.router}
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

// healthResponse 是 /healthz 的响应体；database 与 schema_version 是必有的两个字段。
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

// mcpEndpoint 把已鉴权的用户与 API key 注入请求上下文，再交给 MCP 的
// streamable HTTP handler；scope 过滤与调用复查在 mcp 包内按该身份完成。
func (s *Server) mcpEndpoint(c *gin.Context) {
	u, _ := api.CurrentUser(c)
	k, _ := api.CurrentAPIKey(c)
	ctx := mcp.WithIdentity(c.Request.Context(), u, k)
	s.mcp.ServeHTTP(c.Writer, c.Request.WithContext(ctx))
}

// requestLogger 用 slog 记录每个请求；日志一律英文（AGENTS.md §2.1）。
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

// recovery 捕获 handler panic，记一条英文日志并返回 500，避免进程被单个请求带崩。
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
