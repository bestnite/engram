package mail

import (
	"log/slog"
	"strings"

	"git.nite07.com/nite/engram/internal/notetype"
)

// This file implements the template **fallback chain** and the "fall back to the built-in body when
// rendering fails" safety net.

// ResolvedTemplate is the result of the first two levels of the fallback chain; both fields empty
// means there is no custom template and the built-in body is used.
type ResolvedTemplate struct {
	Subject string
	Body    string
}

// LookupFunc fetches a custom template by (type, locale); ok false means that key has no
// customization.
//
// A function type rather than an interface: this package only cares about "can it be fetched"; the
// storage shape (a GORM table, a cache, a test stand-in) is up to the caller.
type LookupFunc func(t Type, locale string) (subject, body string, ok bool)

// Resolve walks the first two levels of the fallback chain: the exact locale first, then the site
// default locale.
//
// The third level is the built-in body and is not in this function -- the caller passes it in as the
// Fallback of RenderInput. That makes "built-in defaults always live in code" a structural fact
// rather than one branch of this function.
func Resolve(lookup LookupFunc, t Type, locale, siteDefault string) ResolvedTemplate {
	if lookup == nil {
		return ResolvedTemplate{}
	}
	if subject, body, ok := lookup(t, locale); ok {
		return ResolvedTemplate{Subject: subject, Body: body}
	}
	// When the locale equals the site default, do not query twice.
	if siteDefault != "" && siteDefault != locale {
		if subject, body, ok := lookup(t, siteDefault); ok {
			return ResolvedTemplate{Subject: subject, Body: body}
		}
	}
	return ResolvedTemplate{}
}

// RenderOrFallback renders the template; **on a render failure or a missing required variable it falls
// back to the built-in mail**.
//
// This path honors the promise that "a broken template still cannot stop mail from being sent": when
// an admin writes content into the template that fails to validate (or a sender forgets a required
// variable), the whole custom template -- subject and body -- is dropped and the built-in body is
// rendered instead, so the recipient still gets a proper mail rather than an empty one or a 500. If
// even that render fails, the built-in body with its variables substituted is sent as plain text. Failures are always logged -- a silent downgrade would mean template problems are never
// noticed.
func RenderOrFallback(in RenderInput) Rendered {
	// The template content is validated before rendering: a row may have been written straight into the
	// DB, or may predate a variable becoming required, and "a template that dropped the reset link"
	// must fall back to the built-in mail, not send a useless one. A template is one unit, so its
	// subject goes with its body: keeping a custom subject over a built-in body announces a mail the
	// recipient does not get. The validation lives at this layer rather than in Render: Render is pure
	// rendering (it only answers "what does this template render into"), and policy belongs to the
	// layer that sends the mail.
	if strings.TrimSpace(in.BodyMD) != "" {
		if err := Validate(in.Type, in.Subject, in.BodyMD); err != nil {
			slog.Error("mail template is invalid, falling back to the built-in mail",
				"mail_type", string(in.Type), "error", err)
			in.Subject = ""
			in.BodyMD = ""
		}
	}
	out, err := Render(in)
	if err == nil {
		return out
	}
	slog.Error("mail template render failed, falling back to the substituted built-in body",
		"mail_type", string(in.Type), "error", err)
	return Rendered{Subject: in.FallbackSubject, Text: notetype.PlainText(substituteRaw(in.FallbackBodyMD, in.Vars))}
}
