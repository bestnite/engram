package web

import (
	"context"

	"git.nite07.com/nite/engram/internal/i18n"
	"git.nite07.com/nite/engram/internal/mail"
)

// This file is the template rendering entry point shared by every mail sender in the web layer.
//
// The sender's role is fixed: **it only supplies the variable values and the built-in body**; rendering, fallback and escaping all belong to the mail package.
// That way adding a new kind of mail does not mean writing "string concatenation + remember to escape" all over again.

// mailTemplateLookup wraps the template store into a lookup function (the same implementation the periodic worker shares).
func (s *Server) mailTemplateLookup() mail.LookupFunc {
	return mail.StoreLookup(s.mailTemplates, s.logger)
}

// renderMail renders one mail: a custom template wins, the built-in body is the fallback.
//
// When unsubURL is non-empty (optional-class mail) an unsubscribe entry is rendered in the HTML footer and at the end of the plain-text part; type A passes an empty string,
// so no unsubscribe line appears in the footer either — the same rule as "type A structurally cannot carry an unsubscribe header".
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
