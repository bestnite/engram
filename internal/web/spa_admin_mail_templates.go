package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是管理面板「邮件模板」的 SPA JSON 端点（DESIGN.md §4.7）。
//
// 页面只列**已有变量表**的类型，而变量表只覆盖已有发信方的类型——所以管理页不会出现
// 「能配但永远不发」的模板槽。保存时的校验与发送时的校验是同一份（mail.Validate），
// 因此不存在「保存过了却发不出去」的组合。

// spaMailTemplateVar 是编辑器展示的一个变量。
type spaMailTemplateVar struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	NoteKey  string `json:"note_key"`
}

// spaMailTemplateType 是一种邮件类型及其变量表。
type spaMailTemplateType struct {
	Type     string               `json:"type"`
	Class    string               `json:"class"`
	LabelKey string               `json:"label_key"`
	Vars     []spaMailTemplateVar `json:"vars"`
}

// spaMailTemplateRow 是已保存的一份自定义模板。
type spaMailTemplateRow struct {
	Type      string `json:"type"`
	Locale    string `json:"locale"`
	Subject   string `json:"subject"`
	BodyMD    string `json:"body_md"`
	UpdatedAt string `json:"updated_at"`
}

// spaMailTemplatesResponse 是列表响应。
type spaMailTemplatesResponse struct {
	Locales []string `json:"locales"`
	// SiteDefaultLocale 是回退链第二级；界面据此说明「没有自定义时用哪一份」。
	SiteDefaultLocale string                `json:"site_default_locale"`
	Types             []spaMailTemplateType `json:"types"`
	Rows              []spaMailTemplateRow  `json:"rows"`
}

// spaMailTemplateSaveRequest 是保存请求：主题与正文都是 Markdown 原文。
type spaMailTemplateSaveRequest struct {
	Subject string `json:"subject"`
	BodyMD  string `json:"body_md"`
}

// spaMailTemplatePreviewRequest 是预览/测试发信请求。
type spaMailTemplatePreviewRequest struct {
	Type    string `json:"type"`
	Locale  string `json:"locale"`
	Subject string `json:"subject"`
	BodyMD  string `json:"body_md"`
}

// spaMailTemplatePreviewResponse 是渲染结果：纯文本段与 HTML 段都给，便于并排核对。
type spaMailTemplatePreviewResponse struct {
	Subject   string            `json:"subject"`
	Text      string            `json:"text"`
	HTML      string            `json:"html"`
	Variables map[string]string `json:"variables"`
}

// mailTemplateSampleValues 是预览与测试发信用的样例值。
//
// 刻意用与语言无关的占位（`user-name`、`NN`、示例域名），而不是本地化文案：它们是
// **样例数据**不是产品文案，做成语言包只会多出两套没人读的键。
var mailTemplateSampleValues = map[string]string{
	"site":            "Engram",
	"username":        "user-name",
	"inviter":         "user-name",
	"actor":           "user-name",
	"deck":            "deck-name",
	"preset":          "preset-name",
	"role":            "editor",
	"url":             "https://example.com/link",
	"reset_url":       "https://example.com/reset?token=EXAMPLE",
	"verify_url":      "https://example.com/verify?token=EXAMPLE",
	"invite_url":      "https://example.com/register?invite=EXAMPLE",
	"review_url":      "https://example.com/review",
	"stats_url":       "https://example.com/stats",
	"presets_url":     "https://example.com/presets",
	"deck_url":        "https://example.com/decks/1",
	"jobs_url":        "https://example.com/admin/jobs",
	"settings_url":    "https://example.com/settings",
	"unsubscribe_url": "https://example.com/unsubscribe?token=EXAMPLE",
	"expires":         "2026-01-01 04:00 UTC",
	"time":            "2026-01-01 04:00 UTC",
	"ip":              "203.0.113.7",
	"count":           "NN",
	"days":            "5",
	"rate":            "87.5%",
	"used":            "1.1 GiB",
	"limit":           "1.0 GiB",
	"job_id":          "42",
	"kind":            "optimize",
	"what":            "password",
	"status":          "disabled",
	"reason":          "optimizer exited with status 1",
}

// sampleVars 按该类型的变量表造一组样例值。
func sampleVars(t mail.Type) mail.Vars {
	specs, _ := mail.VarSpecs(t)
	out := make(mail.Vars, len(specs))
	for _, spec := range specs {
		if v, ok := mailTemplateSampleValues[spec.Name]; ok {
			out[spec.Name] = v
		} else {
			out[spec.Name] = "value"
		}
	}
	return out
}

// spaAdminMailTemplates 返回类型清单、变量表与已保存的模板。
func (s *Server) spaAdminMailTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	resp := spaMailTemplatesResponse{
		Locales:           s.i18n.SupportedCodes(),
		SiteDefaultLocale: s.siteDefaultLocale(ctx),
		Types:             make([]spaMailTemplateType, 0, len(mail.Catalog())),
	}
	for _, def := range mail.Catalog() {
		specs, supported := mail.VarSpecs(def.Type)
		if !supported {
			continue
		}
		item := spaMailTemplateType{Type: string(def.Type), Class: string(def.Class), LabelKey: def.LabelKey}
		for _, spec := range specs {
			item.Vars = append(item.Vars, spaMailTemplateVar{Name: spec.Name, Required: spec.Required, NoteKey: spec.NoteKey})
		}
		resp.Types = append(resp.Types, item)
	}
	rows, err := s.mailTemplates.List(ctx)
	if err != nil {
		s.logger.Error("spa admin: list mail templates failed", "error", err)
		spaAdminError(c, http.StatusInternalServerError, "load_failed")
		return
	}
	for _, row := range rows {
		resp.Rows = append(resp.Rows, spaMailTemplateRow{
			Type: row.Type, Locale: row.Locale, Subject: row.Subject, BodyMD: row.BodyMD,
			UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	_ = loc
	c.JSON(http.StatusOK, resp)
}

// spaAdminMailTemplateSave 保存一份模板：校验通过才写库，并写审计。
func (s *Server) spaAdminMailTemplateSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	mailType, locale, ok := s.mailTemplateTarget(c)
	if !ok {
		return
	}
	var req spaMailTemplateSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spaAdminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	subject := strings.TrimSpace(req.Subject)
	body := strings.TrimSpace(req.BodyMD)
	if body == "" {
		// 空正文等于「没有自定义」；那是删除的语义，不要让保存悄悄把它变成回退。
		spaAdminError(c, http.StatusBadRequest, "empty_body")
		return
	}
	if err := mail.Validate(mailType, subject, body); err != nil {
		s.logger.Warn("spa admin: rejected mail template", "mail_type", string(mailType), "locale", locale, "error", err)
		spaAdminError(c, http.StatusBadRequest, "invalid_template")
		return
	}
	ctx := c.Request.Context()
	if err := s.mailTemplates.Upsert(ctx, &store.MailTemplate{
		Type: string(mailType), Locale: locale, Subject: subject, BodyMD: body,
		UpdatedBy: store.Ptr(u.ID), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		s.logger.Error("spa admin: save mail template failed", "mail_type", string(mailType), "error", err)
		spaAdminError(c, http.StatusInternalServerError, "save_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		Action:     store.ActionMailTemplateUpdate,
		TargetType: "mail_template",
		Detail:     map[string]any{"type": string(mailType), "locale": locale, "op": "save"},
	})
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// spaAdminMailTemplateDelete 删掉一份自定义模板（回到内置正文），并写审计。
func (s *Server) spaAdminMailTemplateDelete(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	mailType, locale, ok := s.mailTemplateTarget(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := s.mailTemplates.Delete(ctx, string(mailType), locale); err != nil {
		s.logger.Error("spa admin: delete mail template failed", "mail_type", string(mailType), "error", err)
		spaAdminError(c, http.StatusInternalServerError, "delete_failed")
		return
	}
	s.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		Action:     store.ActionMailTemplateUpdate,
		TargetType: "mail_template",
		Detail:     map[string]any{"type": string(mailType), "locale": locale, "op": "delete"},
	})
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// spaAdminMailTemplatePreview 用样例值渲染一份草稿，供编辑器并排核对。
// 它不写库、不发信：预览必须是零副作用的。
func (s *Server) spaAdminMailTemplatePreview(c *gin.Context) {
	var req spaMailTemplatePreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spaAdminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	mailType := mail.Type(strings.TrimSpace(req.Type))
	if _, supported := mail.VarSpecs(mailType); !supported {
		spaAdminError(c, http.StatusBadRequest, "unknown_type")
		return
	}
	vars := sampleVars(mailType)
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:            mailType,
		Site:            vars["site"],
		Vars:            vars,
		Subject:         req.Subject,
		BodyMD:          req.BodyMD,
		FallbackSubject: "(built-in subject)",
		FallbackText:    "(no template: the built-in body is used)",
	})
	c.JSON(http.StatusOK, spaMailTemplatePreviewResponse{
		Subject: out.Subject, Text: out.Text, HTML: out.HTML, Variables: vars,
	})
}

// spaAdminMailTemplateTest 把渲染结果以样例值发给当前管理员。
//
// 它**绕过偏好**直接入队：这是一封测试信，管理员点它就是要看到邮件长什么样，被类型偏好
// 挡掉只会让人困惑。类型仍按真实类型记录，所以退订入口与标签都按该类型渲染。
func (s *Server) spaAdminMailTemplateTest(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		spaAdminError(c, http.StatusForbidden, "forbidden")
		return
	}
	if s.mail == nil || !s.mail.Configured() {
		spaAdminError(c, http.StatusServiceUnavailable, "mail_not_configured")
		return
	}
	var req spaMailTemplatePreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		spaAdminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	mailType := mail.Type(strings.TrimSpace(req.Type))
	specs, supported := mail.VarSpecs(mailType)
	if !supported {
		spaAdminError(c, http.StatusBadRequest, "unknown_type")
		return
	}
	to := strings.TrimSpace(u.Email)
	if to == "" {
		spaAdminError(c, http.StatusBadRequest, "no_email")
		return
	}
	loc := s.userLocalizer(u)
	vars := sampleVars(mailType)
	unsubURL := ""
	if mail.CanUnsubscribe(mailType) {
		// 测试信也带上退订入口，好让管理员看到它在邮件里的位置；令牌指向自己，失效即可。
		unsubURL = s.optionalUnsubscribeLink(c, u.ID, mailType)
		if unsubURL != "" {
			vars["unsubscribe_url"] = unsubURL
		}
	}
	footerNote, unsubLabel := "", ""
	if unsubURL != "" {
		footerNote = loc.T("mail.footer.note")
		unsubLabel = loc.T("mail.footer.unsubscribe")
	}
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             mailType,
		Site:             securitySiteName(loc),
		Vars:             vars,
		Subject:          req.Subject,
		BodyMD:           req.BodyMD,
		FallbackSubject:  loc.T("admin.mail.test.subject"),
		FallbackText:     loc.T("admin.mail.test.body"),
		UnsubscribeURL:   unsubURL,
		FooterNote:       footerNote,
		UnsubscribeLabel: unsubLabel,
	})
	msg := mail.Message{
		To: to, Type: string(mailType), Subject: out.Subject, TextBody: out.Text, HTMLBody: out.HTML,
	}
	msg.Headers = mail.UnsubscribeHeaders(mailType, unsubURL)
	if err := s.mail.Enqueue(c.Request.Context(), msg); err != nil {
		s.logger.Error("spa admin: enqueue test mail failed", "mail_type", string(mailType), "error", err)
		spaAdminError(c, http.StatusInternalServerError, "send_failed")
		return
	}
	_ = specs
	c.JSON(http.StatusOK, gin.H{"queued": true})
}

// mailTemplateTarget 解析并校验路径里的 (类型, 语言)；不合法时已写好响应。
func (s *Server) mailTemplateTarget(c *gin.Context) (mail.Type, string, bool) {
	mailType := mail.Type(strings.TrimSpace(c.Param("type")))
	if _, supported := mail.VarSpecs(mailType); !supported {
		spaAdminError(c, http.StatusBadRequest, "unknown_type")
		return "", "", false
	}
	locale := strings.TrimSpace(c.Param("locale"))
	if !s.supportedLocale(locale) {
		spaAdminError(c, http.StatusBadRequest, "invalid_locale")
		return "", "", false
	}
	return mailType, locale, true
}
