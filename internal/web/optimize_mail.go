package web

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是 C 类「参数优化完成」（optimize_done）的发信方。
//
// 它由作业成功钩子调用，**没有请求上下文**：绝对地址只能靠 BASE_URL 拼，没配就跳过退订
// 入口（宁少一个入口，也不拼一个点不开的相对链接）。类型属于「默认关」的可选类，所以只有
// 主动开过的用户才会收到。

// absoluteURLCtx 用 BASE_URL 拼绝对地址；未配置时返回空串（作业路径没有请求可推导）。
func (s *Server) absoluteURLCtx(path string) string {
	base := strings.TrimRight(strings.TrimSpace(s.baseURL), "/")
	if base == "" {
		return ""
	}
	return base + path
}

// optionalUnsubscribeLinkCtx 与 optionalUnsubscribeLink 相同，但没有请求上下文。
func (s *Server) optionalUnsubscribeLinkCtx(ctx context.Context, userID uint64, typ mail.Type) string {
	if s.tokens == nil || !mail.CanUnsubscribe(typ) {
		return ""
	}
	token, err := s.tokens.Issue(ctx, userID, store.ActionTokenUnsubscribe, string(typ), auth.UnsubscribeTTL)
	if err != nil {
		s.logger.Error("unsubscribe: issue token failed", "user_id", userID, "type", string(typ), "error", err)
		return ""
	}
	link := s.absoluteURLCtx("/unsubscribe?token=" + url.QueryEscape(token))
	if link == "" {
		s.logger.Info("optimize mail: BASE_URL is not configured; unsubscribe link omitted")
	}
	return link
}

// NotifyOptimizeDone 在参数优化成功后通知预设属主。签名与 jobs.SuccessFunc 对齐。
//
// 作业行只记 TargetID（预设 id），所以属主要经预设反查——作业表没有属主列，也不该为此加一列。
func (s *Server) NotifyOptimizeDone(ctx context.Context, job store.Job) {
	if job.Kind != jobs.KindOptimize || job.TargetID == nil || *job.TargetID == 0 {
		return
	}
	preset, err := s.presets.ByID(ctx, *job.TargetID)
	if err != nil || preset == nil {
		s.logger.Error("optimize mail: load preset failed", "preset_id", *job.TargetID, "error", err)
		return
	}
	owner, err := s.users.ByID(ctx, preset.OwnerUserID)
	if err != nil || owner == nil {
		s.logger.Error("optimize mail: load preset owner failed", "preset_id", preset.ID, "error", err)
		return
	}
	if s.mail == nil || !s.mail.Configured() {
		s.logger.Info("optimize mail: smtp is not configured; notification skipped", "preset_id", preset.ID)
		return
	}
	choices, err := store.NewEmailPrefStore(s.db).Choices(ctx, owner.ID)
	if err != nil {
		s.logger.Error("optimize mail: read recipient preferences failed", "user_id", owner.ID, "error", err)
		return
	}
	// C 类默认关：只有主动开启的用户才收这封信。
	if !mail.ResolveEnabled(choices, mail.TypeOptimizeDone) {
		return
	}

	loc := s.userLocalizer(owner)
	site := securitySiteName(loc)
	link := s.absoluteURLCtx("/presets")
	unsubURL := s.optionalUnsubscribeLinkCtx(ctx, owner.ID, mail.TypeOptimizeDone)
	reviewCount := optimizeReviewCount(job)

	vars := mail.Vars{
		"site": site, "preset": preset.Name,
		"count": strconv.Itoa(reviewCount), "url": link,
	}
	if unsubURL != "" {
		vars["unsubscribe_url"] = unsubURL
	}
	footerNote, unsubLabel := "", ""
	if unsubURL != "" {
		footerNote = loc.T("mail.footer.note")
		unsubLabel = loc.T("mail.footer.unsubscribe")
	}
	fallbackSubject, fallbackText, _ := mail.DefaultTemplate(mail.TypeOptimizeDone, mail.VariantDefault, loc.Tf, vars)
	tpl := mail.Resolve(s.mailTemplateLookup(), mail.TypeOptimizeDone, loc.Locale(), s.siteDefaultLocale(ctx))
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             mail.TypeOptimizeDone,
		Site:             site,
		Vars:             vars,
		Subject:          tpl.Subject,
		BodyMD:           tpl.Body,
		FallbackSubject:  fallbackSubject,
		FallbackText:     fallbackText,
		UnsubscribeURL:   unsubURL,
		FooterNote:       footerNote,
		UnsubscribeLabel: unsubLabel,
	})
	msg := mail.Message{
		To: owner.Email, Type: string(mail.TypeOptimizeDone),
		Subject: out.Subject, TextBody: out.Text, HTMLBody: out.HTML,
	}
	msg.Headers = mail.UnsubscribeHeaders(mail.TypeOptimizeDone, unsubURL)
	if err := s.mail.Enqueue(ctx, msg); err != nil {
		s.logger.Error("optimize mail: enqueue failed", "preset_id", preset.ID, "error", err)
	}
}

// optimizeReviewCount 从作业结果里取「用了多少条复习」，取不到就返回 0。
//
// 它只用于正文里的一个数字，缺了不该影响发信，所以不返回错误（与 spa_presets.go 读同一份
// result_json，字段名以 store.OptimizeResult 为准）。
func optimizeReviewCount(job store.Job) int {
	if job.ResultJSON == nil || strings.TrimSpace(*job.ResultJSON) == "" {
		return 0
	}
	var result store.OptimizeResult
	if err := json.Unmarshal([]byte(*job.ResultJSON), &result); err != nil {
		return 0
	}
	return int(result.ReviewsUsed)
}
