package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/reminder"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 本文件是 M1-18 的 Web 层：邮件偏好页（/settings/notifications）的读取与保存。
//
// 页面与发信方共用 internal/mail 的目录：这里不复制「哪类能关、默认开还是关」，
// 只调用 mail.Catalog / CanDisable / ResolveEnabled；保存的值也只是用户显式选择，
// 有效开关一律由目录推导。这样「页面能关、后端照发」不可能发生。
//
// 路由注册挂在 registerSettingsRoutes（个人设置体系）里，与 /settings/totp 同属登录用户自己的页面。

// mailPrefInputPrefix 是可选类型复选框的名字前缀，形如 mail_pref.review_reminder。
// A 类（不可关闭）从不渲染输入，因此提交里出现该前缀且指向 A 类只可能是伪造请求。
const mailPrefInputPrefix = "mail_pref."

// reminderHourInputName 是「发送时间」下拉的 name（复习提醒与周报共用同一小时）。
const reminderHourInputName = "reminder_hour"

// registerMailPrefsRoutes 挂载邮件偏好页路由（M1-18）。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerMailPrefsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil {
		return
	}
	router.GET("/settings/notifications", s.mailPrefsRoute)
	router.POST("/settings/notifications", s.sessions.CSRFMiddleware(), s.mailPrefsSubmit)
}

// mailPrefsPage 渲染邮件偏好页；匿名访问被重定向到登录页。
func (s *Server) mailPrefsPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	saved := ""
	if c.Query("saved") != "" {
		saved = loc.T("mail.prefs.saved")
	}
	s.renderMailPrefs(c, loc, user, http.StatusOK, "", saved)
}

// mailPrefsSubmit 保存用户对可选类型的开关。
//
// 规则（DESIGN.md §4.7）：A 类不可关闭——任何指向不可关闭类型的提交都以 400 拒绝并回填原因，
// 而不是静默忽略；可关闭类型按复选框是否出现写出显式选择（缺席 = 关闭），
// 因此默认关的 C 类也能被用户打开。保存后 303 回本页，页面立即反映新状态。
func (s *Server) mailPrefsSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	// 先校验提交里出现的每个偏好键，再写库：拒绝时不产生半截保存。
	for key := range c.Request.PostForm {
		if !strings.HasPrefix(key, mailPrefInputPrefix) {
			continue
		}
		t := mail.Type(strings.TrimPrefix(key, mailPrefInputPrefix))
		def, found := mail.Lookup(t)
		if !found {
			s.renderMailPrefs(c, loc, user, http.StatusBadRequest,
				loc.Tf("mail.prefs.error.unknown_type", map[string]any{"name": string(t)}), "")
			return
		}
		if !mail.CanDisable(def.Class) {
			s.renderMailPrefs(c, loc, user, http.StatusBadRequest,
				loc.Tf("mail.prefs.error.class_locked", map[string]any{"name": loc.T(def.LabelKey)}), "")
			return
		}
	}

	// 发送小时同样先校验后写：空串 = 未设置（用站点默认），否则必须是 0–23 的整数。
	// 0 是合法值（午夜），因此「未设置」只用空串表示，绝不用 0 顶替（AGENTS.md §2.3 第 9 条）。
	reminderHour, ok := parseReminderHour(c.PostForm(reminderHourInputName))
	if !ok {
		s.renderMailPrefs(c, loc, user, http.StatusBadRequest, loc.T("mail.prefs.error.reminder_hour_invalid"), "")
		return
	}

	// 复选框缺席表示关闭；为每个可关闭类型都写出显式选择，不依赖存储里的旧值。
	choices := make(map[string]bool, len(mail.Catalog()))
	for _, def := range mail.Catalog() {
		if !mail.CanDisable(def.Class) {
			continue
		}
		choices[string(def.Type)] = c.PostForm(mailPrefInputPrefix+string(def.Type)) != ""
	}
	if err := store.NewEmailPrefStore(s.db).SetChoices(ctx, user.ID, choices, time.Now().UTC()); err != nil {
		s.logger.Error("save email preferences failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// 发送小时存在 users 行上，与偏好分开更新；先重读再改，避免覆盖并发写入。
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for email preferences failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	fresh.ReminderHour = reminderHour
	if err := s.users.Update(ctx, fresh); err != nil {
		s.logger.Error("save reminder send hour failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserEmailPrefsUpdate,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail:     map[string]any{"choices": choices, "reminder_hour": reminderHour},
	})
	c.Redirect(http.StatusSeeOther, "/settings/notifications?saved=1")
}

// parseReminderHour 解析发送时间下拉的值：空串表示未设置，返回 nil 指针（用站点默认）；
// 0–23 的十进制整数返回其指针；其余返回 ok=false。
//
// 用可空指针而不是整数：0 是合法值（午夜），「未设置」与「午夜」必须能区分开
// （见 store.User.ReminderHour 的注释）。
func parseReminderHour(raw string) (hour *int, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 23 {
		return nil, false
	}
	return store.Ptr(n), true
}

// renderMailPrefs 组装并写出邮件偏好页；status 用于把校验失败渲染成 4xx。
func (s *Server) renderMailPrefs(c *gin.Context, loc *i18n.Localizer, user *store.User, status int, errMsg, savedMsg string) {
	data, err := s.mailPrefsData(c, loc, user, errMsg, savedMsg)
	if err != nil {
		s.logger.Error("build email preferences data failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	renderHTMLStatus(c, status, views.MailPrefsPage(data))
}

// mailPrefsData 组装偏好页数据；文案全部取自语言包，开关一律由目录推导。
func (s *Server) mailPrefsData(c *gin.Context, loc *i18n.Localizer, user *store.User, errMsg, savedMsg string) (views.MailPrefsData, error) {
	choices, err := store.NewEmailPrefStore(s.db).Choices(c.Request.Context(), user.ID)
	if err != nil {
		return views.MailPrefsData{}, err
	}
	// 发送小时存在 users 行上；会话缓存里可能是旧快照，重读一次保证下拉反映最新值。
	fresh, err := s.users.ByID(c.Request.Context(), user.ID)
	if err != nil {
		return views.MailPrefsData{}, err
	}
	classes := mail.ClassOrder()
	groups := make([]views.MailPrefGroup, 0, len(classes))
	for _, class := range classes {
		group := views.MailPrefGroup{
			Heading: loc.T("mail.prefs.class." + string(class) + ".heading"),
		}
		for _, def := range mail.Catalog() {
			if def.Class != class {
				continue
			}
			item := views.MailPrefType{
				Label:     loc.T(def.LabelKey),
				InputName: mailPrefInputPrefix + string(def.Type),
				Enabled:   mail.ResolveEnabled(choices, def.Type),
			}
			if !mail.CanDisable(def.Class) {
				item.Locked = true
				item.LockedReason = loc.T("mail.prefs.locked")
			}
			group.Types = append(group.Types, item)
		}
		groups = append(groups, group)
	}
	return views.MailPrefsData{
		Layout:       s.pageLayout(c, loc, "mail.prefs.title"),
		Heading:      loc.T("mail.prefs.heading"),
		Intro:        loc.T("mail.prefs.intro"),
		ErrorMessage: errMsg,
		SavedMessage: savedMsg,
		SubmitLabel:  loc.T("mail.prefs.submit"),
		Groups:       groups,
		CSRF:         sessionCSRF(c),

		ReminderTimeHeading:    loc.T("mail.prefs.reminder_time.heading"),
		ReminderTimeLabel:      loc.T("mail.prefs.reminder_time.label"),
		ReminderTimeHint:       loc.Tf("mail.prefs.reminder_time.hint", map[string]any{"tz": fresh.Timezone}),
		ReminderTimeSelectName: reminderHourInputName,
		ReminderTimeOptions:    reminderHourOptions(loc, fresh.ReminderHour),
	}, nil
}

// reminderHourOptions 构造发送时间下拉的选项：第一项是「用站点默认」（value="", 对应 NULL），
// 其后是 0–23 每个整点，当前值所在的项被选中。
//
// 选项标签里的整点由代码格式化成 "HH:00"，不含需要本地化的词；只有「用站点默认」那一项
// 走语言包，并把全局默认小时（reminder.DefaultSendHour）作为占位符传进去。
func reminderHourOptions(loc *i18n.Localizer, selected *int) []views.SelectOption {
	options := make([]views.SelectOption, 0, 25)
	options = append(options, views.SelectOption{
		Value:    "",
		Label:    loc.Tf("mail.prefs.reminder_time.default", map[string]any{"hour": reminder.DefaultSendHour}),
		Selected: selected == nil,
	})
	for hour := 0; hour <= 23; hour++ {
		options = append(options, views.SelectOption{
			Value:    strconv.Itoa(hour),
			Label:    hourLabel(hour),
			Selected: selected != nil && *selected == hour,
		})
	}
	return options
}

// hourLabel 把 0–23 的整点格式化成 "HH:00"；纯数字，不含需要本地化的词。
func hourLabel(hour int) string {
	return strconv.Itoa(hour) + ":00"
}

// mailPrefsRoute 提供 GET /settings/notifications：SPA 已加载时返回应用壳（DESIGN.md §8.1、§8.5），
// 由客户端路由渲染邮件通知偏好页；读取与写入走 /api/v1/settings/notifications（同一份服务逻辑）。
// 授权判定与迁移前一致：未登录一律重定向登录页，页面迁移不新增写路径。
// SPA 缺失（降级构建）时回退 SSR 偏好页 mailPrefsPage。
func (s *Server) mailPrefsRoute(c *gin.Context) {
	if _, ok := s.requireUser(c); !ok {
		return
	}
	if s.spa != nil {
		s.spa.ServeIndex(c)
		return
	}
	s.mailPrefsPage(c)
}
