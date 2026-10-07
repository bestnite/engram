package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 SPA 的邮件通知偏好接口：M1-18（DESIGN.md §4.7、§8.1）的 JSON 版本。
//
// 语义与目录定义（internal/mail）完全一致，只是传输形态是 JSON：
//   - 可关闭类型、A 类不可关闭、默认开关全部由 internal/mail 目录推导，这里不复制规则；
//   - 保存时为每个可关闭类型写出一条显式选择，未提交的按关闭处理（与复选框缺席一致）；
//   - reminder_hour 为 null 表示站点默认（NULL），0–23 是显式小时；0 是合法午夜，
//     「未设置」只用 null 表示，绝不用 0 顶替（AGENTS.md §2.3 第 9 条）。
//
// 读取不要求 CSRF；写入一律过会话 CSRF。响应只暴露目录里登记的类型，未知类型不会出现，
// 也不允许前端凭目录外的东西写库。GET /settings/notifications 只发应用壳
// （见 mail_prefs.go），偏好的读写全部走这里。

// spaNotificationType 是一种邮件类型在偏好页上的状态：稳定英文标识 + 由目录推导的结果，不含文案。
// 前端按 type 查自己的语言包键，因此这里绝不返回本地化文本。
type spaNotificationType struct {
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	Locked  bool   `json:"locked"`
}

// spaNotificationGroup 是按大类分组的类型集合，顺序与目录一致（A → B → C → D）。
type spaNotificationGroup struct {
	Class string                `json:"class"`
	Types []spaNotificationType `json:"types"`
}

// spaNotificationPrefsResponse 是 GET 与 PATCH 共用的响应体。
type spaNotificationPrefsResponse struct {
	Groups []spaNotificationGroup `json:"groups"`
	// ReminderHour 为 null 表示用户未设置（用站点默认）；0–23 表示显式选择的小时。
	ReminderHour *int `json:"reminder_hour"`
	// DefaultReminderHour 是站点默认发送小时（reminder.DefaultSendHour），
	// 供前端渲染「站点默认（HH:00）」选项，避免把 19 这类常量复制进前端。
	DefaultReminderHour int    `json:"default_reminder_hour"`
	Timezone            string `json:"timezone"`
}

// spaNotificationPrefsRequest 是保存请求体：显式选择 + 发送小时。
// choices 缺失或为 null 表示所有可关闭类型都关闭（与复选框全部缺席一致）。
type spaNotificationPrefsRequest struct {
	Choices      map[string]bool `json:"choices"`
	ReminderHour *int            `json:"reminder_hour"`
}

// registerSPAMailPrefsRoutes 挂载 SPA 的邮件通知偏好接口；写操作过会话 CSRF。
// 依赖未装配时跳过，保证 M0 阶段与未启用邮件功能的测试仍能构造 Server。
func (s *Server) registerSPAMailPrefsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil {
		return
	}
	router.GET("/api/v1/settings/notifications", s.spaNotificationPrefsGet)
	router.PATCH("/api/v1/settings/notifications", s.sessions.CSRFMiddleware(), s.spaNotificationPrefsPatch)
}

// spaNotificationPrefsGet 返回当前用户的邮件偏好与发送小时，仅接受浏览器会话。
func (s *Server) spaNotificationPrefsGet(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	resp, err := s.spaNotificationPrefsPayload(c, user.ID)
	if err != nil {
		s.logger.Error("build SPA notification preferences failed", "user_id", user.ID, "error", err)
		spaNotificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// spaNotificationPrefsPayload 组装偏好响应：分组与开关一律由 internal/mail 目录推导。
// 发送小时与用户行上的最新值一致，因此重新读一次 users（会话缓存可能是旧快照）。
func (s *Server) spaNotificationPrefsPayload(c *gin.Context, userID uint64) (spaNotificationPrefsResponse, error) {
	ctx := c.Request.Context()
	choices, err := store.NewEmailPrefStore(s.db).Choices(ctx, userID)
	if err != nil {
		return spaNotificationPrefsResponse{}, err
	}
	fresh, err := s.users.ByID(ctx, userID)
	if err != nil {
		return spaNotificationPrefsResponse{}, err
	}
	groups := make([]spaNotificationGroup, 0, len(mail.ClassOrder()))
	for _, class := range mail.ClassOrder() {
		group := spaNotificationGroup{Class: string(class)}
		for _, def := range mail.Catalog() {
			if def.Class != class {
				continue
			}
			group.Types = append(group.Types, spaNotificationType{
				Type:    string(def.Type),
				Enabled: mail.ResolveEnabled(choices, def.Type),
				Locked:  !mail.CanDisable(def.Class),
			})
		}
		groups = append(groups, group)
	}
	return spaNotificationPrefsResponse{
		Groups:              groups,
		ReminderHour:        fresh.ReminderHour,
		DefaultReminderHour: reminder.DefaultSendHour,
		Timezone:            fresh.Timezone,
	}, nil
}

// spaNotificationPrefsPatch 保存用户对可选类型的开关与发送小时。
//
// 规则（DESIGN.md §4.7）与 SSR 一致：先校验提交里出现的每个偏好键再写库，拒绝时不产生半截保存；
// A 类不可关闭，任何指向 A 类的提交都以 400 拒绝；未知类型同样拒绝；
// 可关闭类型按提交里的值写出显式选择（缺席 = 关闭），因此默认关的 C 类也能被打开。
// 发送小时 null = 站点默认，0–23 为显式值，其余拒绝。
func (s *Server) spaNotificationPrefsPatch(c *gin.Context) {
	user, ok := s.spaProfileSessionOnly(c)
	if !ok {
		return
	}
	var req spaNotificationPrefsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spaNotificationPrefsError(c, http.StatusBadRequest, "invalid_request", "The notification preferences request is invalid.")
		return
	}

	// 先校验提交里出现的每个偏好键，再写库：拒绝时不产生半截保存。
	for key := range req.Choices {
		def, found := mail.Lookup(mail.Type(key))
		if !found {
			spaNotificationPrefsError(c, http.StatusBadRequest, "unknown_type", "An unknown email type was submitted.")
			return
		}
		if !mail.CanDisable(def.Class) {
			spaNotificationPrefsError(c, http.StatusBadRequest, "class_locked", "Security and transactional mail cannot be turned off.")
			return
		}
	}
	// 0 是合法值（午夜），因此越界判断只在非 nil 时进行；null 与缺席都表示站点默认。
	if req.ReminderHour != nil && (*req.ReminderHour < 0 || *req.ReminderHour > 23) {
		spaNotificationPrefsError(c, http.StatusBadRequest, "reminder_hour_invalid", "The send hour must be a whole hour from 0 to 23.")
		return
	}

	ctx := c.Request.Context()
	// 复选框缺席表示关闭；为每个可关闭类型都写出显式选择，不依赖存储里的旧值。
	choices := make(map[string]bool, len(mail.Catalog()))
	for _, def := range mail.Catalog() {
		if !mail.CanDisable(def.Class) {
			continue
		}
		choices[string(def.Type)] = req.Choices[string(def.Type)]
	}
	if err := store.NewEmailPrefStore(s.db).SetChoices(ctx, user.ID, choices, time.Now().UTC()); err != nil {
		s.logger.Error("save SPA email preferences failed", "user_id", user.ID, "error", err)
		spaNotificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}

	// 发送小时存在 users 行上，与偏好分开更新；先重读再改，避免覆盖并发写入。
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for SPA email preferences failed", "user_id", user.ID, "error", err)
		spaNotificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	fresh.ReminderHour = req.ReminderHour
	if err := s.users.Update(ctx, fresh); err != nil {
		s.logger.Error("save SPA reminder send hour failed", "user_id", user.ID, "error", err)
		spaNotificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
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

	resp, err := s.spaNotificationPrefsPayload(c, user.ID)
	if err != nil {
		s.logger.Error("build SPA notification preferences after save failed", "user_id", user.ID, "error", err)
		spaNotificationPrefsError(c, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// spaNotificationPrefsError 写出 SPA 邮件偏好接口的错误包壳。
// 与 spaTOTPError 同形：code 稳定且英文，message 为英文兜底文案，前端按 code 映射本地化提示。
func spaNotificationPrefsError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
