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

	"example.com/flashcard/internal/api"
	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/i18n"
	"example.com/flashcard/internal/jobs"
	"example.com/flashcard/internal/mcp"
	"example.com/flashcard/internal/media"
	"example.com/flashcard/internal/store"
)

// Deps 是显式装配的依赖（AGENTS.md §2.4：入口显式装配，不引入容器）。
type Deps struct {
	DB     *gorm.DB
	Logger *slog.Logger
	// SchemaVersion 返回已应用的 schema 版本，供 /healthz 报告。
	SchemaVersion func(ctx context.Context) (int, error)
	// Assets 是已嵌入的静态资源清单；为空时由 New 从 embed 加载。
	Assets *Assets
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
	// Auditor 是全部写操作的统一审计出口（M1-10）；为空时不写审计。
	Auditor *auth.Auditor
	// LoginLimiter 提供登录失败的递增延迟（M1-9）；为空时登录不做限流。
	LoginLimiter *auth.LoginLimiter
	// BootstrapAdminEmail 是容器化部署时首个管理员的兜底邮箱，预填到 /setup 表单（DESIGN.md §4.1）。
	BootstrapAdminEmail string
	// API 是 /api/v1 的 handler 集合（M4-3）；非空时挂载到 /api/v1。
	API *api.API
	// MCP 是内置 MCP server（M4-6）；非空时在 /mcp 挂载 streamable HTTP。
	MCP *mcp.Server
	// Media 是本地媒体存储（M2-8）；非空时挂载上传与 /media/:id 代理。
	Media *media.Store
	// Secrets 是敏感设置的 AES-GCM 编解码器（M6-10）；非空时管理面板可写入
	// 加密的 OIDC secret 等，且只显示「已配置/未配置」。
	Secrets *store.SecretCodec
	// Jobs 是后台作业的单并发执行器（M9-1）；非空时预设页可触发参数优化（M9-4），
	// 管理面板也可列出作业并取消（M6-6）；为空时作业页只渲染空列表。
	Jobs *jobs.Runner
	// Identities 提供外部身份（OIDC）的读取、绑定与解绑（M1-11、M6-4）；为空时由 New 从 DB 构造。
	Identities *store.IdentityStore
	// BaseURL 是站点对外地址（BASE_URL），用于拼 OIDC redirect_uri；为空时按请求推导。
	BaseURL string
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
	i18n          *i18n.Translator
	userLocale    func(c *gin.Context) string
	accounts      *auth.AccountService
	sessions      *auth.Manager
	users         *store.UserStore
	invites       *store.InviteStore
	decks         *store.DeckStore
	notes         *store.NoteStore
	cards         *store.CardStore
	presets       *store.PresetStore
	grants        *store.GrantStore
	shareLinks    *store.ShareLinkStore
	// access 是 Web 与 REST/MCP 共用的权限判定（M5-1，单一实现见 auth.DeckAccess）。
	access         *auth.DeckAccess
	auditor        *auth.Auditor
	loginLimiter   *auth.LoginLimiter
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
	// identities / identityLink 是 OIDC 绑定能力（M1-11）：store 供解绑与列表，service 走 §4.5 三分支。
	identities   *store.IdentityStore
	identityLink *auth.IdentityLinkService
	// oidc 是协议客户端（发现文档缓存 + state 表）；baseURL 用于拼 redirect_uri。
	oidc    *auth.OIDCClient
	baseURL string
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
		bootstrapEmail: deps.BootstrapAdminEmail,
		api:            deps.API,
		mcp:            deps.MCP,
		media:          deps.Media,
		secrets:        deps.Secrets,
		jobRunner:      deps.Jobs,
		jobStore:       jobs.NewStore(deps.DB),
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
	router.Use(requestLogger(logger), recovery(logger))
	// 会话中间件先于语言中间件：个人设置里的语言（M1-8）要参与 i18n 解析优先级，
	// 而它只能从已解析的会话用户读取。会话解析本身不依赖语言，先后次序安全。
	if s.sessions != nil {
		router.Use(s.sessions.Middleware())
	}
	router.Use(s.localeMiddleware())
	router.GET("/healthz", s.healthz)
	router.GET("/", s.home)
	router.GET(staticPathPrefix+":hash/*filepath", s.assets.Serve)
	// PWA 外壳（M8-2）：manifest 与 service worker 是公开的稳定路由，登录前也需可取。
	s.registerPWARoutes(router)
	s.registerAuthRoutes(router)
	s.registerDeckRoutes(router)
	s.registerPackageWebRoutes(router)
	s.registerNoteRoutes(router)
	s.registerSharingRoutes(router)
	s.registerShareBrowseRoutes(router)
	s.registerCloneRoutes(router)
	s.registerMediaRoutes(router)
	s.registerReviewRoutes(router)
	s.registerPresetRoutes(router)
	s.registerSettingsRoutes(router)
	s.registerStatsRoutes(router)
	s.registerAdminRoutes(router)

	if s.api != nil {
		// REST API 的鉴权中间件内部自行处理会话/bearer 双通道，挂在全局会话中间件之后即可。
		s.api.Register(router)
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
