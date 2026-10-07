package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
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
// 移除 SSR 页面层后，退订由三条传输承担，三者共用同一份令牌语义与同一份实现
// （consumeUnsubscribe）；这里是「加传输」，安全模型不变：
//   - POST /unsubscribe：RFC 8058 One-Click 的机器端点（邮件客户端直接 POST），
//     只回状态码与一个最小 JSON 体，绝不渲染模板；
//   - GET /unsubscribe（及迁移期别名 /spa/unsubscribe）：返回 SPA 应用壳并下发会话前
//     双提交 cookie（收件人可能未登录），页面由客户端渲染；
//   - /api/v1/unsubscribe：SPA 的同源 JSON 传输。GET 读取令牌指名的类型（不消费），
//     POST 消费令牌、落偏好并写审计。
//
// CSRF 例外（有理由的例外，不是绕开保护）：
//   - POST /unsubscribe 故意不挂 sessions.CSRFMiddleware。RFC 8058 的 One-Click POST 由
//     邮件客户端直接发出，不带会话 cookie，也没有可绑定 token 的会话；若要求 CSRF，
//     一键退订在真实邮件客户端里必然失败，功能等于没做。
//   - 这里的「凭据」是 URL 里的高熵一次性令牌本身：只有收到该邮件的人才有它，
//     它只能关掉一个可选类型，且用后即废。相比强行套 CSRF 而让功能不可用，
//     令牌即能力（capability）是 RFC 8058 认可的正确边界。
//   - SPA 的 POST /api/v1/unsubscribe 是会话前写请求，照 /api/v1/auth/verify-email 的约定
//     挂 auth.DoubleSubmitMiddleware（双提交 cookie 由 GET /unsubscribe 下发）。
//   - 其余带会话的写路径不受影响，照旧走 CSRFMiddleware（见 mail_prefs.go 等）。

// unsubscribeTypeInvalidCode 是令牌载荷不是可退订类型时的稳定英文 code。
// 正常流程绝不会签发这种令牌；出现即数据异常，前端按 code 映射提示。
const unsubscribeTypeInvalidCode = "unsubscribe_type_invalid"

// errUnsubscribableType 表示令牌载荷不是可退订的可选类型（数据异常）。
// 与 store 的令牌哨兵错误分开，便于调用方把它映射成专属 code。
var errUnsubscribableType = errors.New("unsubscribe token names an unsubscribable mail type")

// registerUnsubscribeRoutes 挂载 M1-22 的免登录一键退订路由。
// 令牌服务未装配时跳过（M0 阶段仍可构造 Server）。
func (s *Server) registerUnsubscribeRoutes(router *gin.Engine) {
	if s.tokens == nil {
		return
	}
	// GET 返回应用壳（人点邮件里的链接时用），POST 执行退订（RFC 8058 One-Click）。
	// 两者都不挂 CSRFMiddleware —— 理由见文件头注释。
	router.GET("/unsubscribe", s.unsubscribeShell)
	router.POST("/unsubscribe", s.unsubscribeSubmit)
	// 迁移期别名：与 /verify-email、/spa/verify-email 同构，共用同一处理器。
	router.GET("/spa/unsubscribe", s.unsubscribeShell)
	// SPA 的同源 JSON 传输：读不消费令牌，确认消费令牌；确认走会话前双提交 CSRF。
	router.GET("/api/v1/unsubscribe", s.spaUnsubscribeRead)
	router.POST("/api/v1/unsubscribe", auth.DoubleSubmitMiddleware(), s.spaUnsubscribeConfirm)
}

// unsubscribeShell 提供 GET /unsubscribe 与 /spa/unsubscribe：先下发会话前双提交 cookie
// （收件人可能未登录），再返回 SPA 应用壳；退订协议由客户端走 /api/v1/unsubscribe。
//
// 刻意**不消费**令牌：链接预取器、安全扫描器会对 URL 发 GET，若 GET 即退订，
// 用户会莫名其妙被退订。真正的退订只由 POST 完成。
func (s *Server) unsubscribeShell(c *gin.Context) {
	auth.EnsureDoubleSubmitToken(c, s.secureCookies())
	s.spa.ServeIndex(c)
}

// unsubscribeTokenFromRequest 从查询串或表单体里取出明文令牌（机器端点的入参形态）。
func unsubscribeTokenFromRequest(c *gin.Context) string {
	if token := strings.TrimSpace(c.Query("token")); token != "" {
		return token
	}
	return strings.TrimSpace(c.PostForm("token"))
}

// apiUnsubscribeRequest 是 POST /api/v1/unsubscribe 的请求体（SPA 的确认请求）。
type apiUnsubscribeRequest struct {
	Token string `json:"token"`
}

// spaUnsubscribeRead 是 SPA 的读端点（GET /api/v1/unsubscribe?token=…）：
// 只读取令牌指名的可选类型，不消费令牌、不写任何偏好。令牌无效/已用/过期时返回稳定 code，
// 让客户端在用户点确认之前就能提示「链接已失效」，而不是让他白点一次。
func (s *Server) spaUnsubscribeRead(c *gin.Context) {
	tok, err := s.tokens.Peek(c.Request.Context(), store.ActionTokenUnsubscribe, strings.TrimSpace(c.Query("token")))
	if err != nil {
		s.writeUnsubscribeError(c, err)
		return
	}
	typ := mail.Type(strings.TrimSpace(tok.Payload))
	if !mail.CanUnsubscribe(typ) {
		s.writeUnsubscribeError(c, errUnsubscribableType)
		return
	}
	c.JSON(http.StatusOK, gin.H{"type": string(typ)})
}

// spaUnsubscribeConfirm 是 SPA 的确认端点（POST /api/v1/unsubscribe，会话前双提交 CSRF）：
// 消费一次性退订令牌，只关掉令牌指名的那个可选类型。
func (s *Server) spaUnsubscribeConfirm(c *gin.Context) {
	var req apiUnsubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiAuthError(c, http.StatusBadRequest, api.CodeInvalidRequest, "The request is invalid.")
		return
	}
	typ, err := s.consumeUnsubscribe(c.Request.Context(), strings.TrimSpace(req.Token))
	if err != nil {
		s.writeUnsubscribeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"type": string(typ)})
}

// unsubscribeSubmit 是 RFC 8058 One-Click 的机器端点（POST /unsubscribe）。
//
// 邮件客户端直接 POST（可能不带 cookie），只关心 2xx；因此这里只回状态码与一个最小 JSON 体，
// 绝不渲染模板。令牌语义与 SPA 的 JSON 确认完全一致——两者共用 consumeUnsubscribe。
func (s *Server) unsubscribeSubmit(c *gin.Context) {
	typ, err := s.consumeUnsubscribe(c.Request.Context(), unsubscribeTokenFromRequest(c))
	if err != nil {
		s.writeUnsubscribeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"unsubscribed": true, "type": string(typ)})
}

// consumeUnsubscribe 消费退订令牌并把令牌指名的那个可选类型关掉，返回该类型。
//
// 顺序刻意如此：先消费令牌（一次性），再用令牌里的 Payload 定位类型与用户——
// 调用方无法提供类型，因此不存在「拿 A 类型的令牌关 B 类型」的可能。
// 三条传输共用它，保证只关这一个类型、只写一次偏好、只落一条审计。
//
// 返回的错误分三类：store 的令牌哨兵（NotFound/Used/Expired）、errUnsubscribableType
// （载荷异常）以及其它（偏好读写失败，已被 %w 包装且不含令牌明文）。
func (s *Server) consumeUnsubscribe(ctx context.Context, token string) (mail.Type, error) {
	tok, err := s.tokens.Consume(ctx, store.ActionTokenUnsubscribe, token)
	if err != nil {
		return "", err
	}
	typ := mail.Type(strings.TrimSpace(tok.Payload))
	if !mail.CanUnsubscribe(typ) {
		// 令牌载荷由服务端签发，出现不可退订的类型说明数据异常；令牌已被消费，
		// 用户重新从邮件点一次即可。绝不退订 A 类（CanUnsubscribe 对 A 类恒为 false）。
		return "", errUnsubscribableType
	}
	prefs := store.NewEmailPrefStore(s.db)
	choices, err := prefs.Choices(ctx, tok.UserID)
	if err != nil {
		return "", fmt.Errorf("load email preferences: %w", err)
	}
	// 只关掉这一个类型：在既有显式选择上写一条 false，其余类型原样保留。
	choices[string(typ)] = false
	if err := prefs.SetChoices(ctx, tok.UserID, choices, time.Now().UTC()); err != nil {
		return "", fmt.Errorf("save email preferences: %w", err)
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(tok.UserID),
		Action:     store.ActionUserEmailUnsubscribe,
		TargetType: "user",
		TargetID:   store.Ptr(tok.UserID),
		Detail:     map[string]any{"type": string(typ)},
	})
	return typ, nil
}

// writeUnsubscribeError 把消费/读取退订令牌的错误映射到稳定英文 code 与状态码。
// 令牌类错误与载荷异常都是 400（用户可用重新点一次邮件解决），其余按 500 记英文日志。
// 绝不把令牌明文写进日志。
func (s *Server) writeUnsubscribeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errUnsubscribableType):
		apiAuthError(c, http.StatusBadRequest, unsubscribeTypeInvalidCode,
			"The unsubscribe link names a mail type that cannot be unsubscribed.")
	case errors.Is(err, store.ErrActionTokenNotFound),
		errors.Is(err, store.ErrActionTokenUsed),
		errors.Is(err, store.ErrActionTokenExpired):
		apiAuthError(c, http.StatusBadRequest, actionTokenErrorCode(err),
			"The unsubscribe link is invalid or has expired.")
	default:
		s.logger.Error("unsubscribe: consume failed", "error", err)
		apiAuthError(c, http.StatusInternalServerError, api.CodeInternal, "An internal error occurred.")
	}
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
	link := s.securityAbsoluteURL(c, "/unsubscribe?token="+url.QueryEscape(token))
	return mail.UnsubscribeHeaders(typ, link)
}
