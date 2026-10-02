package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 本文件是 M1-22：RFC 8058 一键退订。
//
// 设计要点（DESIGN.md §4.7）：
//   - 退订入口只加在**可选类型**（CanDisable 为真的 B/C/D）的邮件上；A 类一律不带
//     （头由 internal/mail 的 UnsubscribeHeaders 统一构造，A 类传进去也只得到 nil）。
//   - 令牌化且**无需登录**：退订不该先让人登进来。令牌是一次性的、有有效期，
//     库里只存摘要（store.HashActionToken），明文只出现在邮件链接里，绝不进日志。
//   - 令牌的 Payload 指名**那一个**可选类型；端点只读令牌里的类型，不接受调用方传入类型，
//     因此一枚令牌只能关掉它指名的类型，也无法被改指到别的类型或别的用户。
//
// CSRF 例外（有理由的例外，不是绕开保护）：
//   - POST /unsubscribe 故意不挂 sessions.CSRFMiddleware。RFC 8058 的 One-Click POST 由
//     邮件客户端直接发出，不带会话 cookie，也没有可绑定 token 的会话；若要求 CSRF，
//     一键退订在真实邮件客户端里必然失败，功能等于没做。
//   - 这里的「凭据」是 URL 里的高熵一次性令牌本身：只有收到该邮件的人才有它，
//     它只能关掉一个可选类型，且用后即废。相比强行套 CSRF 而让功能不可用，
//     令牌即能力（capability）是 RFC 8058 认可的正确边界。
//   - 其余带会话的写路径不受影响，照旧走 CSRFMiddleware（见 mail_prefs.go 等）。

// registerUnsubscribeRoutes 挂载 M1-22 的免登录一键退订路由。
// 令牌服务未装配时跳过（M0 阶段仍可构造 Server）。
func (s *Server) registerUnsubscribeRoutes(router *gin.Engine) {
	if s.tokens == nil {
		return
	}
	// GET 渲染确认页（人点邮件里的链接时用），POST 执行退订（RFC 8058 One-Click）。
	// 两者都不挂 CSRFMiddleware —— 理由见文件头注释。
	router.GET("/unsubscribe", s.unsubscribePage)
	router.POST("/unsubscribe", s.unsubscribeSubmit)
}

// unsubscribeSubmit 消费一次性退订令牌，并只关掉令牌指名的那个可选类型。
//
// 顺序刻意如此：先消费令牌（一次性），再用令牌里的 Payload 定位类型与用户——
// 调用方无法提供类型，因此不存在「拿 A 类型的令牌关 B 类型」的可能。
func (s *Server) unsubscribeSubmit(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		token = strings.TrimSpace(c.PostForm("token"))
	}
	tok, err := s.tokens.Consume(ctx, store.ActionTokenUnsubscribe, token)
	if err != nil {
		s.renderUnsubscribeResult(c, loc, http.StatusBadRequest, "", loc.T(unsubscribeTokenErrorKey(err)))
		return
	}
	typ := mail.Type(strings.TrimSpace(tok.Payload))
	if !mail.CanUnsubscribe(typ) {
		// 令牌载荷由服务端签发，出现不可退订的类型说明数据异常；令牌已被消费，
		// 用户重新从邮件点一次即可。绝不退订 A 类（CanUnsubscribe 对 A 类恒为 false）。
		s.renderUnsubscribeResult(c, loc, http.StatusBadRequest, "", loc.T("mail.unsub.error_invalid"))
		return
	}

	prefs := store.NewEmailPrefStore(s.db)
	choices, err := prefs.Choices(ctx, tok.UserID)
	if err != nil {
		s.logger.Error("unsubscribe: load email preferences failed", "user_id", tok.UserID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// 只关掉这一个类型：在既有显式选择上写一条 false，其余类型原样保留。
	choices[string(typ)] = false
	if err := prefs.SetChoices(ctx, tok.UserID, choices, time.Now().UTC()); err != nil {
		s.logger.Error("unsubscribe: save email preferences failed", "user_id", tok.UserID, "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserEmailUnsubscribe,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
		Detail:     map[string]any{"type": string(typ)},
	})

	// 结果页语言跟随令牌归属用户，而不是邮件客户端的 Accept-Language。
	respLoc := s.unsubscribeUserLocalizer(ctx, tok.UserID, loc)
	def, _ := mail.Lookup(typ)
	s.renderUnsubscribeResult(c, respLoc, http.StatusOK,
		respLoc.Tf("mail.unsub.done", map[string]any{"type": respLoc.T(def.LabelKey)}), "")
}

// unsubscribePage 渲染一键退订的确认页（GET）。
//
// 刻意**不消费**令牌：链接预取器、安全扫描器会对 URL 发 GET，若 GET 即退订，
// 用户会莫名其妙被退订。真正的退订只由 POST（RFC 8058 One-Click 或本页按钮）完成。
func (s *Server) unsubscribePage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		s.renderUnsubscribeResult(c, loc, http.StatusBadRequest, "", loc.T("mail.unsub.error_invalid"))
		return
	}
	data := views.SecurityFormData{
		Layout:      s.authLayout(c, loc, "mail.unsub.title"),
		Heading:     loc.T("mail.unsub.heading"),
		Intro:       loc.T("mail.unsub.intro"),
		ShowForm:    true,
		Action:      "/unsubscribe?token=" + url.QueryEscape(token),
		SubmitLabel: loc.T("mail.unsub.submit"),
		AltLabel:    loc.T("mail.unsub.back"),
		AltHref:     "/settings/notifications",
	}
	s.renderSecurityForm(c, loc, http.StatusOK, data)
}

// renderUnsubscribeResult 渲染退订流程的结果页（无表单）：notice 为成功提示，errMsg 为失败提示。
func (s *Server) renderUnsubscribeResult(c *gin.Context, loc *i18n.Localizer, status int, notice, errMsg string) {
	data := views.SecurityFormData{
		Layout:   s.authLayout(c, loc, "mail.unsub.title"),
		Heading:  loc.T("mail.unsub.heading"),
		AltLabel: loc.T("mail.unsub.back"),
		AltHref:  "/settings/notifications",
	}
	if status == http.StatusOK {
		data.Notice = notice
	} else {
		data.ErrorMessage = errMsg
	}
	s.renderSecurityForm(c, loc, status, data)
}

// unsubscribeUserLocalizer 取令牌归属用户的界面语言；用户不存在时回落请求语言。
func (s *Server) unsubscribeUserLocalizer(ctx context.Context, userID uint64, fallback *i18n.Localizer) *i18n.Localizer {
	if s.users == nil {
		return fallback
	}
	u, err := s.users.ByID(ctx, userID)
	if err != nil || u == nil {
		return fallback
	}
	return s.userLocalizer(u)
}

// optionalUnsubscribeHeaders 为可选类型签发一枚指名该类型的免登录退订令牌，并返回 RFC 8058 头。
//
// A 类（不可关闭）或签发失败时返回 nil —— 宁可这封邮件少一个退订入口，也绝不给安全邮件
// 加退订头，或让发信因退订令牌失败而失败（DESIGN.md §4.7：发信失败不得让触发操作失败）。
func (s *Server) optionalUnsubscribeHeaders(c *gin.Context, userID uint64, typ mail.Type) map[string]string {
	if s.tokens == nil || !mail.CanUnsubscribe(typ) {
		return nil
	}
	token, err := s.tokens.Issue(c.Request.Context(), userID, store.ActionTokenUnsubscribe, string(typ), auth.UnsubscribeTTL)
	if err != nil {
		s.logger.Error("unsubscribe: issue token failed", "user_id", userID, "type", string(typ), "error", err)
		return nil
	}
	link := s.inviteAbsoluteURL(c, "/unsubscribe?token="+url.QueryEscape(token))
	return mail.UnsubscribeHeaders(typ, link)
}

// unsubscribeTokenErrorKey 把令牌消费错误映射到稳定语言包 key。
func unsubscribeTokenErrorKey(err error) string {
	switch {
	case errors.Is(err, store.ErrActionTokenExpired):
		return "mail.unsub.error_expired"
	case errors.Is(err, store.ErrActionTokenUsed):
		return "mail.unsub.error_used"
	default:
		return "mail.unsub.error_invalid"
	}
}
