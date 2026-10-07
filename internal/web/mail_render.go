package web

import (
	"context"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
)

// 本文件是 web 层所有发信方共用的模板渲染入口。
//
// 发信方的分工固定：**它只负责给出变量值与内置正文**，渲染、回退、转义都归 mail 包。
// 这样新增一种邮件时不会又写一遍「拼字符串 + 记得转义」。

// mailTemplateLookup 把模板存储包成查找函数（实现与周期 worker 共用同一份）。
func (s *Server) mailTemplateLookup() mail.LookupFunc {
	return mail.StoreLookup(s.mailTemplates, s.logger)
}

// renderMail 渲染一封邮件：自定义模板优先，内置正文兜底。
//
// unsubURL 非空时（可选类邮件）在 HTML 页脚与纯文本段末尾渲染退订入口；A 类传空串，
// 页脚也不会出现退订行——与「A 类结构上不可能带退订头」同一条口径。
func (s *Server) renderMail(ctx context.Context, loc *i18n.Localizer, t mail.Type, vars mail.Vars,
	fbSubject, fbText, unsubURL string) (string, string, string) {
	locale := loc.Locale()
	siteDefault := s.siteDefaultLocale(ctx)
	tpl := mail.Resolve(s.mailTemplateLookup(), t, locale, siteDefault)

	footerNote, unsubLabel := "", ""
	if unsubURL != "" {
		footerNote = loc.T("mail.footer.note")
		unsubLabel = loc.T("mail.footer.unsubscribe")
	}
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             t,
		Site:             securitySiteName(loc),
		Vars:             vars,
		Subject:          tpl.Subject,
		BodyMD:           tpl.Body,
		FallbackSubject:  fbSubject,
		FallbackText:     fbText,
		UnsubscribeURL:   unsubURL,
		FooterNote:       footerNote,
		UnsubscribeLabel: unsubLabel,
	})
	return out.Subject, out.Text, out.HTML
}
