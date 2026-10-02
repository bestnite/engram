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

	"example.com/engram/internal/auth"
	"example.com/engram/internal/store"
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
	// Grants 是卡组授权存储（M5-1）；为空时由 New 从 DB 构造。
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
	// access 是 Web 与 REST/MCP 共用的权限判定（M5-1，单一实现见 auth.DeckAccess）。
	access  *auth.DeckAccess
	auditor *auth.Auditor
	now     func() time.Time
	authn   *Authenticator
	// mediaRoot 供卡组包内联媒体使用；为空时不落盘媒体。
	mediaRoot string
}

// New 构造 API；M4-2 中间件与 M4-3 handler 的依赖必须齐备。
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
	// 授权存储可按需从 DB 构造，避免每个调用方（含测试）都要显式装配（M5-1）。
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

// Register 把 /api/v1 挂到给定的 router 上；每条路由在其 scope 对应的中间件后执行（DESIGN.md §7.3）。
func (a *API) Register(r gin.IRouter) {
	v1 := r.Group("/api/v1")
	v1.Use(a.authn.Auth())

	v1.GET("/decks", a.authn.RequireScope(store.ScopeRead), a.listDecks)
	v1.POST("/decks", a.authn.RequireScope(store.ScopeWrite), a.createDeck)
	v1.GET("/decks/:id/notes", a.authn.RequireScope(store.ScopeRead), a.listNotes)
	v1.POST("/decks/:id/notes", a.authn.RequireScope(store.ScopeWrite), a.importNotes)
	v1.PATCH("/notes/:id", a.authn.RequireScope(store.ScopeWrite), a.updateNote)
	v1.DELETE("/notes/:id", a.authn.RequireScope(store.ScopeWrite), a.deleteNote)
	v1.GET("/review/due", a.authn.RequireScope(store.ScopeReview), a.dueCards)
	v1.POST("/review", a.authn.RequireScope(store.ScopeReview), a.submitReview)
	v1.GET("/stats/summary", a.authn.RequireScope(store.ScopeRead), a.statsSummary)
	v1.GET("/export", a.authn.RequireScope(store.ScopeRead), a.exportCards)
	v1.GET("/decks/:id/package", a.authn.RequireScope(store.ScopeRead), a.handleExportPackage)
	v1.POST("/decks/import", a.authn.RequireScope(store.ScopeWrite), a.handleImportPackage)
	v1.GET("/keys", a.authn.RequireScope(store.ScopeAdmin), a.listKeys)
	v1.POST("/keys", a.authn.RequireScope(store.ScopeAdmin), a.createKey)
	v1.DELETE("/keys/:id", a.authn.RequireScope(store.ScopeAdmin), a.deleteKey)
}

// audit 写一条审计（带当前 key 的 api_key_id）；写失败记英文日志但不回滚业务。
func (a *API) audit(ctx context.Context, e store.AuditEntry) {
	if a.auditor == nil {
		return
	}
	if err := a.auditor.Record(ctx, e); err != nil {
		a.logger.Error("write audit log failed", "action", e.Action, "error", err)
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

// requireDeckRole 取卡组并校验当前用户在卡组上至少拥有 want 角色（M5-1）。
//
// 判定本体在 auth.DeckAccess（与 Web 共用同一实现）；这里只负责把错误映射成 REST 的
// 稳定 code、写一条 permission.denied 审计并中止请求。无访问权 -> 403 forbidden，
// 有角色但不够 -> 403 insufficient_role，卡组不存在 -> 404 not_found。
func (a *API) requireDeckRole(c *gin.Context, deckID uint64, want string) (*store.Deck, bool) {
	u, _ := CurrentUser(c)
	deck, role, err := a.access.RequireRole(c.Request.Context(), deckID, u.ID, want)
	if err == nil {
		return deck, true
	}
	status, code := http.StatusForbidden, CodeForbidden
	if errors.Is(err, auth.ErrDeckNotFound) {
		status, code = http.StatusNotFound, CodeNotFound
	} else if role != "" {
		code = CodeInsufficientRole
	}
	if !errors.Is(err, auth.ErrDeckNotFound) {
		a.audit(c.Request.Context(), store.AuditEntry{
			UserID:     store.Ptr(u.ID),
			APIKeyID:   CurrentAPIKeyID(c),
			Action:     store.ActionPermissionDenied,
			TargetType: "deck",
			TargetID:   store.Ptr(deckID),
			Detail:     map[string]any{"required_role": want, "user_role": role, "code": code},
		})
	}
	abortError(c, status, code, "")
	return nil, false
}

// requireNoteRole 取 note 及其卡组并要求至少 want 角色；note 不存在时 404，
// 卡组权限不足时沿用 requireDeckRole 的结果（note 不泄露到无权限的卡组之外）。
func (a *API) requireNoteRole(c *gin.Context, noteID uint64, want string) (*store.Note, *store.Deck, bool) {
	n, err := a.notes.ByID(c.Request.Context(), noteID)
	if err != nil {
		abortError(c, http.StatusNotFound, CodeNotFound, "")
		return nil, nil, false
	}
	d, ok := a.requireDeckRole(c, n.DeckID, want)
	if !ok {
		return nil, nil, false
	}
	return n, d, true
}

// ensureDefaultPreset 返回该用户名为 Default 的预设 id；不存在则创建。
// 外部调用方建卡组时通常不关心预设，服务端给出一个可用的默认预设（DESIGN.md §3.5）。
func (a *API) ensureDefaultPreset(ctx context.Context, userID uint64) (uint64, error) {
	list, err := a.presets.ListByOwner(ctx, userID)
	if err != nil {
		return 0, err
	}
	for i := range list {
		if list[i].Name == "Default" {
			return list[i].ID, nil
		}
	}
	p := store.NewPreset(userID, "Default")
	if err := a.presets.Create(ctx, &p); err != nil {
		return 0, err
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
