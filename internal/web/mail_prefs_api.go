package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 SPA 的邮件通知偏好接口：M1-18的 JSON 版本。
//
// 语义与目录定义（internal/mail）完全一致，只是传输形态是 JSON：
//   - 可关闭类型、A 类不可关闭、默认开关全部由 internal/mail 目录推导，这里不复制规则；
//   - 保存时为每个可关闭类型写出一条显式选择，未提交的按关闭处理（与复选框缺席一致）；
//   - D 管理员通知只发给管理员：非管理员的响应不含该分组，提交该类的偏好以 403 拒绝，
//     也不为非管理员写下该类的显式选择（否则日后被提升为管理员的人会带着陈旧的「关闭」上任）；
//   - reminder_hour 为 null 表示站点默认（NULL），0–23 是显式小时；0 是合法午夜，
//     「未设置」只用 null 表示，绝不用 0 顶替（AGENTS.md §2.3 第 9 条）。
//
// 读取不要求 CSRF；写入一律过会话 CSRF。响应只暴露目录里登记的类型，未知类型不会出现，
// 也不允许前端凭目录外的东西写库。GET /settings/notifications 只发应用壳
// （见 mail_prefs.go），偏好的读写全部走这里。

// notificationType 是一种邮件类型在偏好页上的状态：稳定英文标识 + 由目录推导的结果，不含文案。
// 前端按 type 查自己的语言包键，因此这里绝不返回本地化文本。
type notificationType struct {
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	Locked  bool   `json:"locked"`
}

// notificationGroup 是按大类分组的类型集合，顺序与目录一致（A → B → C → D）。
type notificationGroup struct {
	Class string             `json:"class"`
	Types []notificationType `json:"types"`
}

// notificationPrefsResponse 是 GET 与 PATCH 共用的响应体。
type notificationPrefsResponse struct {
	Groups []notificationGroup `json:"groups"`
	// ReminderHour 为 null 表示用户未设置（用站点默认）；0–23 表示显式选择的小时。
	ReminderHour *int `json:"reminder_hour"`
	// DefaultReminderHour 是站点默认发送小时（reminder.DefaultSendHour），
	// 供前端渲染「站点默认（HH:00）」选项，避免把 19 这类常量复制进前端。
	DefaultReminderHour int    `json:"default_reminder_hour"`
	Timezone            string `json:"timezone"`
}

// notificationPrefsRequest 是保存请求体：显式选择 + 发送小时。
// choices 缺失或为 null 表示所有可关闭类型都关闭（与复选框全部缺席一致）。
type notificationPrefsRequest struct {
	Choices      map[string]bool `json:"choices"`
	ReminderHour *int            `json:"reminder_hour"`
}

// registerMailPrefsAPIRoutes 挂载 SPA 的邮件通知偏好接口；写操作过会话 CSRF。
// 依赖未装配时跳过，保证 M0 阶段与未启用邮件功能的测试仍能构造 Server。
func (s *Server) registerMailPrefsAPIRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil {
		return
	}
	router.GET("/api/v1/settings/notifications", s.notificationPrefsGet)
	router.PATCH("/api/v1/settings/notifications", s.sessions.CSRFMiddleware(), s.notificationPrefsPatch)
}

// notificationPrefsGet 返回当前用户的邮件偏好与发送小时，仅接受浏览器会话。
func (s *Server) notificationPrefsGet(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	resp, err := s.notificationPrefsPayload(c, user.ID)
	if err != nil {
		s.logger.Error("build SPA notification preferences failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// notificationPrefsPayload 组装偏好响应：分组与开关一律由 internal/mail 目录推导。
// 发送小时与用户行上的最新值一致，因此重新读一次 users（会话缓存可能是旧快照）。
// 角色也取自这一次重读：D 类只发给管理员，非管理员的响应里连该分组都不出现。
func (s *Server) notificationPrefsPayload(c *gin.Context, userID uint64) (notificationPrefsResponse, error) {
	ctx := c.Request.Context()
	choices, err := store.NewEmailPrefStore(s.db).Choices(ctx, userID)
	if err != nil {
		return notificationPrefsResponse{}, err
	}
	fresh, err := s.users.ByID(ctx, userID)
	if err != nil {
		return notificationPrefsResponse{}, err
	}
	admin := fresh.Role == store.RoleAdmin
	groups := make([]notificationGroup, 0, len(mail.ClassOrder()))
	for _, class := range mail.ClassOrder() {
		if mail.AdminOnly(class) && !admin {
			continue
		}
		group := notificationGroup{Class: string(class)}
		for _, def := range mail.Catalog() {
			if def.Class != class {
				continue
			}
			group.Types = append(group.Types, notificationType{
				Type:    string(def.Type),
				Enabled: mail.ResolveEnabled(choices, def.Type),
				Locked:  !mail.CanDisable(def.Class),
			})
		}
		groups = append(groups, group)
	}
	return notificationPrefsResponse{
		Groups:              groups,
		ReminderHour:        fresh.ReminderHour,
		DefaultReminderHour: reminder.DefaultSendHour,
		Timezone:            fresh.Timezone,
	}, nil
}

// notificationPrefsPatch 保存用户对可选类型的开关与发送小时。
//
// 规则与 SSR 一致：先校验提交里出现的每个偏好键再写库，拒绝时不产生半截保存；
// A 类不可关闭，任何指向 A 类的提交都以 400 拒绝；D 类只对管理员开放，非管理员提交它以
// 403 拒绝（而不是静默忽略——忽略会让伪造请求以为配置成功，也让「页面不该展示它」这条
// 规则没有服务端落地）；未知类型同样拒绝；
// 可关闭类型按提交里的值写出显式选择（缺席 = 关闭），因此默认关的 C 类也能被打开。
// 发送小时 null = 站点默认，0–23 为显式值，其余拒绝。
func (s *Server) notificationPrefsPatch(c *gin.Context) {
	user, ok := s.profileSessionOnly(c)
	if !ok {
		return
	}
	// 角色每请求现查（会话中间件从库里重读 users 行），因此降级或提升即时生效。
	admin := user.Role == store.RoleAdmin
	var req notificationPrefsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		notificationPrefsError(c, http.StatusBadRequest, "invalid_request", "The notification preferences request is invalid.")
		return
	}

	// 先校验提交里出现的每个偏好键，再写库：拒绝时不产生半截保存。
	for key := range req.Choices {
		def, found := mail.Lookup(mail.Type(key))
		if !found {
			notificationPrefsError(c, http.StatusBadRequest, "unknown_type", "An unknown email type was submitted.")
			return
		}
		if !mail.CanDisable(def.Class) {
			notificationPrefsError(c, http.StatusBadRequest, "class_locked", "Security and transactional mail cannot be turned off.")
			return
		}
		if mail.AdminOnly(def.Class) && !admin {
			notificationPrefsError(c, http.StatusForbidden, "forbidden_type", "Administrator notices can only be configured by administrators.")
			return
		}
	}
	// 0 是合法值（午夜），因此越界判断只在非 nil 时进行；null 与缺席都表示站点默认。
	if req.ReminderHour != nil && (*req.ReminderHour < 0 || *req.ReminderHour > 23) {
		notificationPrefsError(c, http.StatusBadRequest, "reminder_hour_invalid", "The send hour must be a whole hour from 0 to 23.")
		return
	}

	ctx := c.Request.Context()
	// 复选框缺席表示关闭；为每个可关闭类型都写出显式选择，不依赖存储里的旧值。
	// D 类只对管理员有意义，非管理员不为它写出选择：留空即回落到目录默认（开），
	// 这样日后被提升为管理员的人不会带着一个陈旧的「关闭」上任。
	choices := make(map[string]bool, len(mail.Catalog()))
	for _, def := range mail.Catalog() {
		if !mail.CanDisable(def.Class) {
			continue
		}
		if mail.AdminOnly(def.Class) && !admin {
			continue
		}
		choices[string(def.Type)] = req.Choices[string(def.Type)]
	}
	if err := store.NewEmailPrefStore(s.db).SetChoices(ctx, user.ID, choices, time.Now().UTC()); err != nil {
		s.logger.Error("save SPA email preferences failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}

	// 发送小时存在 users 行上，与偏好分开更新；先重读再改，避免覆盖并发写入。
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for SPA email preferences failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	fresh.ReminderHour = req.ReminderHour
	if err := s.users.Update(ctx, fresh); err != nil {
		s.logger.Error("save SPA reminder send hour failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	// 审计动作与 Detail 形态与 SSR 保存一致（internal/web/mail_prefs.go）。
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserEmailPrefsUpdate,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"choices": choices, "reminder_hour": req.ReminderHour},
	})

	resp, err := s.notificationPrefsPayload(c, user.ID)
	if err != nil {
		s.logger.Error("build SPA notification preferences after save failed", "user_id", user.ID, "error", err)
		notificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// notificationPrefsError 写出 SPA 邮件偏好接口的错误包壳。
// 与 totpError 同形：code 稳定且英文，message 为英文兜底文案，前端按 code 映射本地化提示。
func notificationPrefsError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
