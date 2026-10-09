package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
)

// Deps 是 API 的显式依赖（AGENTS.md §2.4：入口显式装配）。
type Deps struct {
	DB      *gorm.DB
	Logger  *slog.Logger
	Keys    *store.APIKeyStore
	Users   *store.UserStore
	Decks   *store.DeckStore
	Notes   *store.NoteStore
	Presets *store.PresetStore
	Cards   *store.CardStore
	// Grants 是卡组授权存储；为空时由 New 从 DB 构造。
	Grants  *store.GrantStore
	Auditor *auth.Auditor
	// Now 可注入时钟；为零时用系统 UTC 时间。
	Now func() time.Time
	// MediaRoot 是媒体字节的本地根目录；卡组包导出/导入内联媒体时使用（可为空）。
	MediaRoot string
	// ReadLimit / WriteLimit / RateWindow 透传给鉴权中间件的按 key 限流。
	ReadLimit  int
	WriteLimit int
	RateWindow time.Duration
}

// API 持有 /api/v1 的全部 handler 与中间件。
type API struct {
	db      *gorm.DB
	logger  *slog.Logger
	keys    *store.APIKeyStore
	users   *store.UserStore
	decks   *store.DeckStore
	notes   *store.NoteStore
	presets *store.PresetStore
	cards   *store.CardStore
	grants  *store.GrantStore
	// access 是 Web 与 REST/MCP 共用的权限判定（单一实现见 auth.DeckAccess）。
	access  *auth.DeckAccess
	auditor *auth.Auditor
	now     func() time.Time
	authn   *Authenticator
	// mediaRoot 供卡组包内联媒体使用；为空时不落盘媒体。
	mediaRoot string
}

// New 构造 API；中间件与 handler 的依赖必须齐备。
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
		return nil, errors.New("api: missing required dependencies: " + joinStrings(missing))
	}
	// 授权存储可按需从 DB 构造，避免每个调用方（含测试）都要显式装配。
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
	return &API{
		db:        deps.DB,
		logger:    logger,
		keys:      deps.Keys,
		users:     deps.Users,
		decks:     deps.Decks,
		notes:     deps.Notes,
		presets:   deps.Presets,
		cards:     deps.Cards,
		grants:    grants,
		access:    auth.NewDeckAccess(deps.Decks, grants),
		auditor:   deps.Auditor,
		now:       now,
		authn:     authn,
		mediaRoot: deps.MediaRoot,
	}, nil
}

// Register 把 /api/v1 挂到给定的 router 上；每条路由在其 scope 对应的中间件后执行。
func (a *API) Register(r gin.IRouter) {
	v1 := r.Group("/api/v1")
	v1.Use(a.authn.Auth())

	v1.GET("/decks", a.authn.RequireScope(store.ScopeRead), a.listDecks)
	v1.GET("/media", a.authn.RequireScope(store.ScopeRead), a.listMedia)
	v1.POST("/decks", a.authn.RequireScope(store.ScopeWrite), a.createDeck)
	v1.DELETE("/decks/:id", a.authn.RequireScope(store.ScopeWrite), a.deleteDeck)
	// 退出共享：撤掉自己在该卡组的一行授权；与删除卡组是两件事，故独立路径。
	v1.DELETE("/decks/:id/membership", a.authn.RequireScope(store.ScopeWrite), a.leaveDeck)
	v1.GET("/decks/:id/notes", a.authn.RequireScope(store.ScopeRead), a.listNotes)
	v1.POST("/decks/:id/notes", a.authn.RequireScope(store.ScopeWrite), a.importNotes)
	v1.PATCH("/notes/:id", a.authn.RequireScope(store.ScopeWrite), a.updateNote)
	v1.DELETE("/notes/:id", a.authn.RequireScope(store.ScopeWrite), a.deleteNote)
	// gin 按 method 建树；POST 侧没有 /notes/:id，故 /notes/bulk 与它不冲突。
	v1.POST("/notes/bulk", a.authn.RequireScope(store.ScopeWrite), a.bulkNotes)
	v1.GET("/review/due", a.authn.RequireScope(store.ScopeReview), a.dueCards)
	v1.POST("/review", a.authn.RequireScope(store.ScopeReview), a.submitReview)
	v1.GET("/stats/summary", a.authn.RequireScope(store.ScopeRead), a.statsSummary)
	v1.GET("/export", a.authn.RequireScope(store.ScopeRead), a.exportCards)
	v1.GET("/decks/:id/package", a.authn.RequireScope(store.ScopeRead), a.handleExportPackage)
	v1.POST("/decks/import", a.authn.RequireScope(store.ScopeWrite), a.handleImportPackage)
	v1.GET("/keys", a.authn.RequireScope(store.ScopeKeys), a.listKeys)
	v1.POST("/keys", a.authn.RequireScope(store.ScopeKeys), a.createKey)
	v1.DELETE("/keys/:id", a.authn.RequireScope(store.ScopeKeys), a.deleteKey)
}

// recordAudit 写一条审计；写失败只记英文日志，不回滚业务（AGENTS.md §2.1：日志恒英文）。
// *API 与 *Authenticator 两个接收者共用这一实现，避免同一守卫各写一遍。
func recordAudit(ctx context.Context, auditor *auth.Auditor, logger *slog.Logger, e store.AuditEntry) {
	if auditor == nil {
		return
	}
	if err := auditor.Record(ctx, e); err != nil {
		logger.Error("write audit log failed", "action", e.Action, "error", err)
	}
}

// pathID 解析路径里的无符号整型参数；解析失败直接写 404 并返回 false。
func pathID(c *gin.Context, name string) (uint64, bool) {
	raw := c.Param(name)
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		abortError(c, http.StatusNotFound, CodeNotFound, "")
		return 0, false
	}
	return id, true
}

// queryInt 读取整型查询参数；缺失或非法时返回 def。
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

// ensureDefaultPreset 返回该用户默认预设的 id：不存在则由 store.EnsureDefaultPreset 补齐。
// 外部调用方建卡组时通常不关心预设，服务端给出一个可用的默认预设。
// 预设名是稳定字面量（store.DefaultPresetName），与 web 入口共用同一补齐逻辑。
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

// joinStrings 用逗号拼接依赖名，构造可读的英文装配错误。
func joinStrings(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
