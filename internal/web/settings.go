package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// registerSettingsRoutes 挂载个人设置页（M1-8，DESIGN.md §4.1、§8.1）。
//
// 这是登录用户自己的页面，路由不挂在 /admin/* 下：任何已登录用户都能改自己的
// 显示名、界面语言、时区、复习日切点与密码。写操作一律过 CSRF（DESIGN.md §4.3）。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerSettingsRoutes(router *gin.Engine) {
	if s.sessions == nil || s.users == nil || s.accounts == nil {
		return
	}
	router.GET("/settings", s.settingsPage)
	router.POST("/settings/profile", s.sessions.CSRFMiddleware(), s.settingsProfileSubmit)
	// 页头语言切换（M1-8 语言切换落库）：只改 users.locale 一个字段，因此单独一个端点。
	// 它仍然是写操作，一律过 CSRF（DESIGN.md §4.3）。
	router.POST(languageRoute, s.sessions.CSRFMiddleware(), s.settingsLocaleSubmit)
	router.POST("/settings/password", s.sessions.CSRFMiddleware(), s.settingsPasswordSubmit)
	// 用户级 API Key 管理（M4-10，DESIGN.md §8.1）：路由与 handler 在 keys.go。
	// 创建与撤销都是写操作，一律过 CSRF（DESIGN.md §4.3）。
	router.GET("/settings/keys", s.keysPage)
	router.POST("/settings/keys", s.sessions.CSRFMiddleware(), s.keysCreate)
	router.POST("/settings/keys/:id/revoke", s.sessions.CSRFMiddleware(), s.keysRevoke)
	// 邮件类型偏好页（M1-18）：路由与 handler 在 mail_prefs.go，仍属个人设置体系。
	s.registerMailPrefsRoutes(router)
}

// settingsPage 渲染个人设置页；匿名访问被重定向到登录页。
func (s *Server) settingsPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	renderHTMLStatus(c, http.StatusOK, views.SettingsPage(s.settingsData(c, loc, user, "", "")))
}

// settingsProfileSubmit 保存显示名 / 语言 / 时区 / 复习日切点。
//
// 校验分三层：显示名非空、语言必须是受支持的语言码、时区必须能被 time.LoadLocation
// 解析、切点必须在 0–23。非法值一律拒绝并回填已本地化的提示，绝不静默落库。
// 成功后用 303 回到 GET /settings：这样响应页面（以及后续页面）的语言就是新设置的语言。
func (s *Server) settingsProfileSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	display := strings.TrimSpace(c.PostForm("display_name"))
	locale := strings.TrimSpace(c.PostForm("locale"))
	timezone := strings.TrimSpace(c.PostForm("timezone"))
	rawCutoff := strings.TrimSpace(c.PostForm("day_cutoff_hour"))

	// 每次写都从库里重读用户：会话里缓存的副本可能已过期，直接改它会丢别人的并发写。
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for settings failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	// 回填用一份候选副本，保证错误时页面显示的是用户刚提交的值而不是旧值。
	candidate := *fresh
	candidate.DisplayName = display
	candidate.Locale = locale
	candidate.Timezone = timezone

	if display == "" {
		s.renderSettingsError(c, loc, &candidate, loc.T("settings.error.display_name_required"))
		return
	}
	if !s.supportedLocale(locale) {
		s.renderSettingsError(c, loc, &candidate, loc.T("settings.error.locale_invalid"))
		return
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		s.renderSettingsError(c, loc, &candidate, loc.T("settings.error.timezone_invalid"))
		return
	}
	// 留空明确使用默认值；0 是午夜，二者不得混用。范围外的提交拒绝且不落库。
	var cutoff *int
	if rawCutoff != "" {
		hour, err := strconv.Atoi(rawCutoff)
		if err != nil || hour < 0 || hour > 23 {
			s.renderSettingsError(c, loc, &candidate, loc.T("settings.error.cutoff_invalid"))
			return
		}
		cutoff = store.Ptr(hour)
	}

	fresh.DisplayName = display
	fresh.Locale = locale
	fresh.Timezone = timezone
	fresh.DayCutoffHour = cutoff
	if err := s.users.Update(ctx, fresh); err != nil {
		s.logger.Error("update user settings failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserProfileUpdate,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
		Detail: map[string]any{
			"locale":          locale,
			"timezone":        timezone,
			"day_cutoff_hour": cutoff,
		},
	})
	c.Redirect(http.StatusSeeOther, "/settings")
}

// settingsLocaleSubmit 保存页头语言切换（M1-8）。
//
// 页头的语言下拉对已登录用户提交到这里。切换语言必须落进 users.locale：只靠 ?lang
// 覆盖本次请求的话，下一次不带该参数的请求（点任一导航链接或刷新）就会回退到旧值，
// 这正是本端点要修的缺陷（DESIGN.md §8.3）。因此这里不做整套资料校验，只校验语言码，
// 校验通过后用 303 回到提交时携带的站内地址。
func (s *Server) settingsLocaleSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	code := strings.TrimSpace(c.PostForm("lang"))
	next := safeNextPath(c.PostForm("next"))
	// 提交里带 oob 标记说明这一页还有别的语言控件（个人设置页），响应要一并刷新它。
	includeLocaleControl := c.PostForm("oob") == views.SettingsLocaleControlID
	htmx := c.GetHeader("HX-Request") == "true"

	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for locale change failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// 非法语言码一律拒绝并回填本地化提示，绝不静默落库；与 /settings/profile 同一条文案。
	if !s.supportedLocale(code) {
		if htmx {
			// htmx 的响应只会换进页头下拉，塞不下设置页的错误提示；直接 400 让 htmx
			// 不做任何替换（htmx 默认不交换 4xx），页面保持原样。下拉只提交受支持的语言码，
			// 所以这条只有伪造请求能走到。
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		s.renderSettingsError(c, loc, fresh, loc.T("settings.error.locale_invalid"))
		return
	}
	if fresh.Locale != code {
		fresh.Locale = code
		if err := s.users.Update(ctx, fresh); err != nil {
			s.logger.Error("update user locale failed", "user_id", user.ID, "error", err)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		s.audit(ctx, store.AuditEntry{
			UserID:     store.Ptr(user.ID),
			Action:     store.ActionUserProfileUpdate,
			TargetType: "user",
			TargetID:   store.Ptr(user.ID),
			Detail: map[string]any{
				"locale": code,
				"via":    "header",
			},
		})
	}
	if htmx {
		s.renderLocaleSwitchFragment(c, code, next, includeLocaleControl)
		return
	}
	c.Redirect(http.StatusSeeOther, next)
}

// renderLocaleSwitchFragment 就地刷新页头语言下拉（htmx 路径）：主交换是新语言下的下拉组件，
// 来自设置页的请求再带外（out-of-band）交换设置页的语言控件，两处因此立即一致。
//
// 必须用新语言重建本地化器：请求进来时的本地化器按切换前的用户设置解析，直接复用会渲染出旧语言。
func (s *Server) renderLocaleSwitchFragment(c *gin.Context, code, next string, includeLocaleControl bool) {
	loc := s.i18n.Localizer(s.i18n.Pick(code, "", ""))
	layout := s.pageLayout(c, loc, "settings.title")
	if layout.LangForm != nil {
		// 当前请求的 URL 是 POST 目标（/settings/locale），不是用户所在的页面；
		// 回跳地址与 oob 标记必须沿用提交时带回的值，否则第二次切换会丢状态。
		layout.LangForm.Next = next
		layout.LangForm.OOBLocaleControl = includeLocaleControl
	}
	data := views.LanguageSwitchData{
		Layout:               layout,
		IncludeLocaleControl: includeLocaleControl,
		LocaleLabel:          loc.T("settings.profile.locale_label"),
		LocaleOptions:        s.localeOptions(loc, code),
	}
	renderHTMLStatus(c, http.StatusOK, views.LanguageSwitchResponse(data))
}

// localeOptions 构造语言下拉的选项；selected 决定哪一项处于选中态。
func (s *Server) localeOptions(loc *i18n.Localizer, selected string) []views.SettingOption {
	codes := s.i18n.SupportedCodes()
	out := make([]views.SettingOption, 0, len(codes))
	for _, code := range codes {
		out = append(out, views.SettingOption{
			Value:    code,
			Label:    loc.T("language." + code),
			Selected: code == selected,
		})
	}
	return out
}

// safeNextPath 只接受站内绝对路径，其余一律回落到首页，避免开放重定向。
// 拒掉的形态：不以单个 "/" 开头、以 "//" 开头（浏览器按协议相对 URL 跳到外站）、
// 含反斜杠（部分浏览器把它归一化成 "/"）、以及 url.Parse 后仍带主机或 scheme 的值。
func safeNextPath(raw string) string {
	next := strings.TrimSpace(raw)
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return "/"
	}
	if u, err := url.Parse(next); err != nil || u.IsAbs() || u.Host != "" {
		return "/"
	}
	return next
}

// settingsPasswordSubmit 改密码：校验旧密码、argon2id 重哈希，并作废本人其它会话。
//
// 当前会话通过 ChangePasswordKeepingSession 保留，用户改完密码不会被踢回登录页；
// 其它设备上的会话立即失效（DESIGN.md §11）。旧密码错误与策略失败分别回填提示。
func (s *Server) settingsPasswordSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	oldPassword := c.PostForm("old_password")
	newPassword := c.PostForm("new_password")
	fresh, err := s.users.ByID(ctx, user.ID)
	if err != nil {
		s.logger.Error("load user for password change failed", "user_id", user.ID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if fresh.PasswordHash == nil {
		s.renderSettingsError(c, loc, fresh, loc.T("settings.error.password_unavailable"))
		return
	}
	if oldPassword == "" {
		s.renderSettingsError(c, loc, fresh, loc.T("settings.error.old_password_required"))
		return
	}
	if newPassword == "" {
		s.renderSettingsError(c, loc, fresh, loc.T("settings.error.new_password_required"))
		return
	}

	sess, _ := auth.CurrentSession(c)
	keep := ""
	if sess != nil {
		keep = sess.ID
	}
	err = s.accounts.ChangePasswordKeepingSession(ctx, user.ID, keep, oldPassword, newPassword)
	if err != nil {
		s.renderSettingsError(c, loc, fresh, loc.T(passwordChangeErrorKey(err)))
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(user.ID),
		Action:     store.ActionUserPasswordChange,
		TargetType: "user",
		TargetID:   store.Ptr(user.ID),
	})
	// M1-19：凭据变更通知（密码）；发信失败不影响改密成功。
	s.notifyCredentialChanged(ctx, fresh, "password")
	c.Redirect(http.StatusSeeOther, "/settings")
}

// passwordChangeErrorKey 把改密错误映射到语言包 key；未知错误按「旧密码错误」处理，
// 因为那是最常见的原因，且绝不回显内部错误文本。
func passwordChangeErrorKey(err error) string {
	switch {
	case errors.Is(err, auth.ErrPasswordUnchanged):
		return "settings.error.password_unchanged"
	case errors.Is(err, auth.ErrInvalidCredentials):
		return "settings.error.old_password_wrong"
	default:
		// 策略失败（过短/过长/常见弱密码）复用注册流程的既有提示。
		return "settings.error.password_rejected"
	}
}

// renderSettingsError 以 400 重渲染设置页并带一条本地化提示。
func (s *Server) renderSettingsError(c *gin.Context, loc *i18n.Localizer, user *store.User, msg string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusBadRequest)
	if err := views.SettingsPage(s.settingsData(c, loc, user, msg, "")).Render(c.Request.Context(), c.Writer); err != nil {
		s.logger.Error("render template failed", "error", err, "path", c.Request.URL.Path)
	}
}

// settingsData 组装设置页的渲染数据；文案全部取自语言包（模板零硬编码）。
// user 是用于回填表单的用户快照：正常进入时来自会话，校验失败时是用户刚提交的候选值。
func (s *Server) settingsData(c *gin.Context, loc *i18n.Localizer, user *store.User, errMsg, savedMsg string) views.SettingsData {
	csrf := sessionCSRF(c)
	// 设置页上除页头下拉外还有一个语言控件：页头切换要带外刷新它，故此处标记。
	layout := s.pageLayout(c, loc, "settings.title")
	if layout.LangForm != nil {
		layout.LangForm.OOBLocaleControl = true
	}
	zones := make([]string, 0, len(commonTimezones))
	for _, z := range commonTimezones {
		zones = append(zones, z)
	}
	return views.SettingsData{
		Layout:              layout,
		Heading:             loc.T("settings.heading"),
		Intro:               loc.T("settings.intro"),
		ErrorMessage:        errMsg,
		SavedMessage:        savedMsg,
		ProfileHeading:      loc.T("settings.profile.heading"),
		DisplayNameLabel:    loc.T("settings.profile.display_name_label"),
		DisplayNameValue:    user.DisplayName,
		LocaleLabel:         loc.T("settings.profile.locale_label"),
		LocaleOptions:       s.localeOptions(loc, user.Locale),
		TimezoneLabel:       loc.T("settings.profile.timezone_label"),
		TimezoneValue:       user.Timezone,
		TimezoneHint:        loc.T("settings.profile.timezone_hint"),
		TimezoneOptions:     zones,
		CutoffLabel:         loc.T("settings.profile.cutoff_label"),
		CutoffValue:         strconv.Itoa(store.ResolveCutoff(user.DayCutoffHour)),
		CutoffHint:          loc.T("settings.profile.cutoff_hint"),
		ProfileSubmit:       loc.T("settings.profile.submit"),
		PasswordHeading:     loc.T("settings.password.heading"),
		OldPasswordLabel:    loc.T("settings.password.old_label"),
		NewPasswordLabel:    loc.T("settings.password.new_label"),
		PasswordSubmit:      loc.T("settings.password.submit"),
		PasswordNote:        loc.T("settings.password.note"),
		PasswordAvailable:   user.PasswordHash != nil,
		PasswordUnavailable: loc.T("settings.password.oidc_only"),
		TOTPHeading:         loc.T("totp.settings.heading"),
		TOTPHint:            loc.T("totp.settings.intro"),
		TOTPLinkLabel:       loc.T("totp.settings.link"),
		TOTPHref:            "/settings/totp",
		// M1-18：邮件类型偏好入口，指向独立的 /settings/notifications 页。
		MailPrefsHeading:   loc.T("settings.mail_prefs.heading"),
		MailPrefsHint:      loc.T("settings.mail_prefs.hint"),
		MailPrefsLinkLabel: loc.T("settings.mail_prefs.link"),
		MailPrefsHref:      "/settings/notifications",
		// M1-19：邮箱地址变更入口，指向 /settings/email。仅当安全邮件栈可用
		// （SMTP 已配置且 outbox 已装配）时才给出入口：邮件未配置时该页只会渲染
		// mail.not_configured，隐藏入口比把用户送去死路更好。
		EmailAvailable: s.securityMailReady(),
		EmailHeading:   loc.T("settings.email.heading"),
		EmailHint:      loc.T("settings.email.hint"),
		EmailLinkLabel: loc.T("settings.email.link"),
		EmailHref:      "/settings/email",
		// M4-10：用户级 API Key 管理入口，指向独立的 /settings/keys 页。
		KeysHeading:   loc.T("settings.keys.heading"),
		KeysHint:      loc.T("settings.keys.hint"),
		KeysLinkLabel: loc.T("settings.keys.link"),
		KeysHref:      "/settings/keys",
		CSRF:          csrf,
	}
}

// commonTimezones 是时区输入框的常用候选（datalist）。它不是权威列表：
// 用户仍可手输任意 IANA 名，服务端一律用 time.LoadLocation 校验，因此不需要引入第三方时区数据包。
var commonTimezones = []string{
	"UTC",
	"Asia/Shanghai",
	"Asia/Hong_Kong",
	"Asia/Taipei",
	"Asia/Tokyo",
	"Asia/Seoul",
	"Asia/Singapore",
	"Europe/London",
	"Europe/Paris",
	"Europe/Berlin",
	"America/New_York",
	"America/Chicago",
	"America/Los_Angeles",
	"Australia/Sydney",
}

// supportedLocale 报告 code 是否是当前受支持的语言码。
func (s *Server) supportedLocale(code string) bool {
	for _, c := range s.i18n.SupportedCodes() {
		if c == code {
			return true
		}
	}
	return false
}
