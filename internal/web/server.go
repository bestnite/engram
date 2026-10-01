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
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"example.com/flashcard/internal/i18n"
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
		addr:          addr,
		logger:        logger,
		db:            deps.DB,
		schemaVersion: deps.SchemaVersion,
		assets:        assets,
		i18n:          translator,
		userLocale:    deps.UserLocale,
	}

	// 发布模式：gin 自带的调试日志与我们的 slog 中间件重复，关掉前者。
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(requestLogger(logger), recovery(logger), s.localeMiddleware())
	router.GET("/healthz", s.healthz)
	router.GET("/", s.home)
	router.GET(staticPathPrefix+":hash/*filepath", s.assets.Serve)
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
