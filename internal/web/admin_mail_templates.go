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

// This file is the SPA JSON endpoint for the admin panel's "mail templates".
//
// The page only lists types that **already have a variable table**, and variable tables only cover types that already have a sender — so the admin page never shows
// a template slot that "can be configured but never sends". Validation at save time and at send time is the same code (mail.Validate),
// so a combination that "was saved yet cannot be sent" does not exist.

// mailTemplateVar is one variable shown in the editor.
type mailTemplateVar struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	NoteKey  string `json:"note_key"`
}

// mailTemplateType is one mail type and its variable table.
type mailTemplateType struct {
	Type     string            `json:"type"`
	Class    string            `json:"class"`
	LabelKey string            `json:"label_key"`
	Vars     []mailTemplateVar `json:"vars"`
}

// mailTemplateRow is one saved custom template.
type mailTemplateRow struct {
	Type      string `json:"type"`
	Locale    string `json:"locale"`
	Subject   string `json:"subject"`
	BodyMD    string `json:"body_md"`
	UpdatedAt string `json:"updated_at"`
}

// mailTemplateDefault is the **built-in body** of a type in a given language.
//
// It uses the same code as the sender's fallback (mail.DefaultTemplate), only substituting placeholders instead of real values,
// so "the default seen in the editor" and "the default actually sent" cannot differ — this is exactly what the editor is pre-filled with.
type mailTemplateDefault struct {
	Type    string `json:"type"`
	Locale  string `json:"locale"`
	Subject string `json:"subject"`
	BodyMD  string `json:"body_md"`
}

// mailTemplatesResponse is the list response.
type mailTemplatesResponse struct {
	Locales []string `json:"locales"`
	// SiteDefaultLocale is the second level of the fallback chain; the UI uses it to explain "which one is used when nothing is customized".
	SiteDefaultLocale string             `json:"site_default_locale"`
	Types             []mailTemplateType `json:"types"`
	Rows              []mailTemplateRow  `json:"rows"`
	// Defaults is the built-in body for each type × each language, for editor pre-fill and for "restore default" to compare against.
	Defaults []mailTemplateDefault `json:"defaults"`
}

// mailTemplateSaveRequest is the save request: subject and body are both raw Markdown.
type mailTemplateSaveRequest struct {
	Subject string `json:"subject"`
	BodyMD  string `json:"body_md"`
}

// mailTemplatePreviewRequest is the preview/test-send request.
type mailTemplatePreviewRequest struct {
	Type    string `json:"type"`
	Locale  string `json:"locale"`
	Subject string `json:"subject"`
	BodyMD  string `json:"body_md"`
}

// mailTemplatePreviewResponse is the render result: both the plain-text part and the HTML part are returned, for side-by-side checking.
type mailTemplatePreviewResponse struct {
	Subject   string            `json:"subject"`
	Text      string            `json:"text"`
	HTML      string            `json:"html"`
	Variables map[string]string `json:"variables"`
}

// mailTemplateSampleValues are the sample values used for preview and test send.
//
// Deliberately language-neutral placeholders (`user-name`, `NN`, an example domain) are used instead of localized copy: they are
// **sample data**, not product copy; putting them into language packs would only add two sets of keys nobody reads.
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
	"rate":            "87.5",
	"used":            "1.1 GiB",
	"limit":           "1.0 GiB",
	"job_id":          "42",
	"kind":            "optimize",
	"what":            "password",
	"status":          "disabled",
	"reason":          "optimizer exited with status 1",
}

// sampleVars builds a set of sample values from that type's variable table.
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

// adminMailTemplates returns the type list, the variable tables and the saved templates.
func (s *Server) adminMailTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	resp := mailTemplatesResponse{
		Locales:           s.i18n.SupportedCodes(),
		SiteDefaultLocale: s.siteDefaultLocale(ctx),
		Types:             make([]mailTemplateType, 0, len(mail.Catalog())),
	}
	for _, def := range mail.Catalog() {
		specs, supported := mail.VarSpecs(def.Type)
		if !supported {
			continue
		}
		item := mailTemplateType{Type: string(def.Type), Class: string(def.Class), LabelKey: def.LabelKey}
		for _, spec := range specs {
			item.Vars = append(item.Vars, mailTemplateVar{Name: spec.Name, Required: spec.Required, NoteKey: spec.NoteKey})
		}
		resp.Types = append(resp.Types, item)
	}
	// Built-in bodies: computed once per type × language (the count is small, so there is no need to cache, which also avoids the cache drifting from the language packs).
	locales := resp.Locales
	for _, def := range resp.Types {
		typ := mail.Type(def.Type)
		for _, code := range locales {
			loc := s.i18n.Localizer(s.i18n.Pick(code, "", ""))
			subject, body, ok := mail.DefaultTemplate(typ, mail.VariantDefault, loc.Tf, mail.PlaceholderVars(typ))
			if !ok {
				continue
			}
			resp.Defaults = append(resp.Defaults, mailTemplateDefault{
				Type: def.Type, Locale: code, Subject: subject, BodyMD: body,
			})
		}
	}
	rows, err := s.mailTemplates.List(ctx)
	if err != nil {
		s.logger.Error("spa admin: list mail templates failed", "error", err)
		adminError(c, http.StatusInternalServerError, "load_failed")
		return
	}
	for _, row := range rows {
		resp.Rows = append(resp.Rows, mailTemplateRow{
			Type: row.Type, Locale: row.Locale, Subject: row.Subject, BodyMD: row.BodyMD,
			UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	_ = loc
	c.JSON(http.StatusOK, resp)
}

// adminMailTemplateSave saves one template: writes to the DB only if validation passes, and writes an audit row.
func (s *Server) adminMailTemplateSave(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	mailType, locale, ok := s.mailTemplateTarget(c)
	if !ok {
		return
	}
	var req mailTemplateSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	subject := strings.TrimSpace(req.Subject)
	body := strings.TrimSpace(req.BodyMD)
	if body == "" {
		// An empty body means "no customization"; that is the semantics of deletion, so do not let a save quietly turn it into a fallback.
		adminError(c, http.StatusBadRequest, "empty_body")
		return
	}
	if err := mail.Validate(mailType, subject, body); err != nil {
		s.logger.Warn("spa admin: rejected mail template", "mail_type", string(mailType), "locale", locale, "error", err)
		adminError(c, http.StatusBadRequest, "invalid_template")
		return
	}
	ctx := c.Request.Context()
	if err := s.mailTemplates.Upsert(ctx, &store.MailTemplate{
		Type: string(mailType), Locale: locale, Subject: subject, BodyMD: body,
		UpdatedBy: store.Ptr(u.ID), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		s.logger.Error("spa admin: save mail template failed", "mail_type", string(mailType), "error", err)
		adminError(c, http.StatusInternalServerError, "save_failed")
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

// adminMailTemplateDelete deletes one custom template (falling back to the built-in body) and writes an audit row.
func (s *Server) adminMailTemplateDelete(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	mailType, locale, ok := s.mailTemplateTarget(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	if err := s.mailTemplates.Delete(ctx, string(mailType), locale); err != nil {
		s.logger.Error("spa admin: delete mail template failed", "mail_type", string(mailType), "error", err)
		adminError(c, http.StatusInternalServerError, "delete_failed")
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

// adminMailTemplatePreview renders a draft with the sample values, for the editor to check side by side.
// It writes no DB rows and sends no mail: preview must be side-effect free.
func (s *Server) adminMailTemplatePreview(c *gin.Context) {
	var req mailTemplatePreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	mailType := mail.Type(strings.TrimSpace(req.Type))
	if _, supported := mail.VarSpecs(mailType); !supported {
		adminError(c, http.StatusBadRequest, "unknown_type")
		return
	}
	// The preview copy follows the locale being edited: the request carries it, and the language pack
	// supplies the footer note. An unknown code falls back to the default locale.
	loc := s.i18n.Localizer(s.i18n.Pick(strings.TrimSpace(req.Locale), "", ""))
	vars := sampleVars(mailType)
	unsubURL := ""
	if mail.CanUnsubscribe(mailType) {
		// A sample unsubscribe address keeps the preview representative of an optional-type mail; it is
		// placeholder data and carries no token, so the preview stays side-effect free.
		unsubURL = vars["unsubscribe_url"]
	}
	footerNote, unsubLabel := loc.T("mail.footer.automated"), ""
	if unsubURL != "" {
		footerNote = loc.T("mail.footer.note")
		unsubLabel = loc.T("mail.footer.unsubscribe")
	}
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             mailType,
		Site:             vars["site"],
		Lang:             loc.Locale(),
		Vars:             vars,
		Subject:          req.Subject,
		BodyMD:           req.BodyMD,
		FallbackSubject:  "(built-in subject)",
		FallbackBodyMD:   "(no template: the built-in body is used)",
		UnsubscribeURL:   unsubURL,
		FooterNote:       footerNote,
		UnsubscribeLabel: unsubLabel,
	})
	c.JSON(http.StatusOK, mailTemplatePreviewResponse{
		Subject: out.Subject, Text: out.Text, HTML: out.HTML, Variables: vars,
	})
}

// adminMailTemplateTest sends the rendered result with sample values to the current admin.
//
// It **bypasses preferences** and enqueues directly: this is a test mail, the admin clicked it precisely to see what the mail looks like, and being blocked by the type preference
// would only be confusing. The type is still recorded as the real type, so the unsubscribe entry and the label are rendered for that type.
func (s *Server) adminMailTemplateTest(c *gin.Context) {
	u, ok := auth.CurrentUser(c)
	if !ok {
		adminError(c, http.StatusForbidden, "forbidden")
		return
	}
	if s.mail == nil || !s.mail.Configured() {
		adminError(c, http.StatusServiceUnavailable, "mail_not_configured")
		return
	}
	var req mailTemplatePreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		adminError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	mailType := mail.Type(strings.TrimSpace(req.Type))
	specs, supported := mail.VarSpecs(mailType)
	if !supported {
		adminError(c, http.StatusBadRequest, "unknown_type")
		return
	}
	to := strings.TrimSpace(u.Email)
	if to == "" {
		adminError(c, http.StatusBadRequest, "no_email")
		return
	}
	loc := s.userLocalizer(u)
	vars := sampleVars(mailType)
	unsubURL := ""
	if mail.CanUnsubscribe(mailType) {
		// The test mail carries the unsubscribe entry too, so the admin can see where it sits in the mail; the token points at the admin and may simply expire.
		unsubURL = s.optionalUnsubscribeLink(c, u.ID, mailType)
		if unsubURL != "" {
			vars["unsubscribe_url"] = unsubURL
		}
	}
	footerNote, unsubLabel := loc.T("mail.footer.automated"), ""
	if unsubURL != "" {
		footerNote = loc.T("mail.footer.note")
		unsubLabel = loc.T("mail.footer.unsubscribe")
	}
	out := mail.RenderOrFallback(mail.RenderInput{
		Type:             mailType,
		Site:             securitySiteName(loc),
		Lang:             loc.Locale(),
		Vars:             vars,
		Subject:          req.Subject,
		BodyMD:           req.BodyMD,
		FallbackSubject:  loc.T("admin.mail.test.subject"),
		FallbackBodyMD:   loc.T("admin.mail.test.body"),
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
		adminError(c, http.StatusInternalServerError, "send_failed")
		return
	}
	_ = specs
	c.JSON(http.StatusOK, gin.H{"queued": true})
}

// mailTemplateTarget parses and validates the (type, locale) in the path; the response has already been written when invalid.
func (s *Server) mailTemplateTarget(c *gin.Context) (mail.Type, string, bool) {
	mailType := mail.Type(strings.TrimSpace(c.Param("type")))
	if _, supported := mail.VarSpecs(mailType); !supported {
		adminError(c, http.StatusBadRequest, "unknown_type")
		return "", "", false
	}
	locale := strings.TrimSpace(c.Param("locale"))
	if !s.supportedLocale(locale) {
		adminError(c, http.StatusBadRequest, "invalid_locale")
		return "", "", false
	}
	return mailType, locale, true
}
