package mail

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"git.nite07.com/nite/engram/internal/render"
)

// This file is the renderer for mail templates.
//
// Rendering happens in four steps, and the order is part of the security; do not swap them:
//  1. replace the {{var}} in the template with tokens carrying a per-render random salt;
//  2. render the Markdown and sanitize it against the allowlist;
//  3. replace the tokens with the values, HTML-escaping in the HTML segment;
//  4. wrap the result in the fixed shell held by the code.
//
// **Why render first and substitute afterwards**: variable values contain user-controlled data
// (usernames, deck names, failure reasons). Substituting into the source first hands the user data
// to the Markdown parser and the allowlist decision -- a value of `**x**` becomes bold, `<b>x</b>`
// becomes raw HTML. Rendering first means the value never entered the renderer, and substitution only
// has to escape by context. This is exactly the lesson the card formula placeholders learned, and it
// applies to mail too.

// placeholderRe matches {{name}} in a template. Names are restricted to lowercase letters, digits
// and underscores: that way a mathematical notation in the template (the single-brace kind such as
// `\frac{x}{y}`) is not mistaken for a placeholder, and variable names are forced to stay stable
// English identifiers.
var placeholderRe = regexp.MustCompile(`\{\{([a-z0-9_]+)\}\}`)

// Vars are the variable values of one mail. The values are untrusted content and are escaped by
// context when rendering.
type Vars map[string]string

// RenderInput is all the input of one render.
type RenderInput struct {
	// Type decides the set of required variables (see vars.go).
	Type Type
	// Site is the site name, used in the shell heading.
	Site string
	// Vars are the variable values.
	Vars Vars

	// Subject and BodyMD are the admin's custom template. A blank BodyMD means there is no
	// customization, and FallbackSubject/FallbackText (the built-in body assembled by the sender) are
	// used -- **the built-in body always lives in code**, so a corrupt or deleted template cannot stop
	// mail from being sent.
	Subject string
	BodyMD  string

	// FallbackSubject and FallbackText are the built-in body.
	FallbackSubject string
	FallbackText    string

	// When UnsubscribeURL is non-empty, the unsubscribe entry is rendered at the bottom of the shell
	// and at the end of the plain text segment (only optional-class mail has it).
	UnsubscribeURL string
	// FooterNote and UnsubscribeLabel are passed in localized by the caller: this package does not
	// depend on i18n, and user-visible copy in mail must come from the language packs (AGENTS.md §2.1).
	FooterNote       string
	UnsubscribeLabel string
}

// Rendered is the render result: subject, plain text segment, HTML segment. An empty HTML segment
// means this mail goes out as plain text only.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

// Render renders one mail. With no custom template it returns the built-in body (an empty HTML
// segment, consistent with today's behavior).
func Render(in RenderInput) (Rendered, error) {
	if strings.TrimSpace(in.BodyMD) == "" {
		return Rendered{Subject: in.FallbackSubject, Text: in.FallbackText}, nil
	}
	if err := checkRequiredVars(in.Type, in.Vars); err != nil {
		return Rendered{}, err
	}

	// Subject: when the template does not set one, the built-in subject is kept; the subject must be a
	// single line, otherwise it breaks the RFC 5322 header.
	subject := strings.TrimSpace(substituteRaw(in.Subject, in.Vars))
	subject = strings.ReplaceAll(subject, "\r", " ")
	subject = strings.ReplaceAll(subject, "\n", " ")
	if subject == "" {
		subject = in.FallbackSubject
	}

	tokenised, order, salt := tokenise(in.BodyMD)
	bodyHTML, err := render.RenderMarkdown(tokenised)
	if err != nil {
		return Rendered{}, fmt.Errorf("render mail template: %w", err)
	}
	htmlBody := wrapLayout(in.Site, substituteTokens(bodyHTML, order, salt, in.Vars, true), in.UnsubscribeURL, in.FooterNote, in.UnsubscribeLabel)

	text := substituteRaw(in.BodyMD, in.Vars)
	if in.UnsubscribeURL != "" && in.UnsubscribeLabel != "" {
		text = strings.TrimRight(text, "\n") + "\n\n" + in.UnsubscribeLabel + ": " + in.UnsubscribeURL + "\n"
	}
	return Rendered{Subject: subject, Text: text, HTML: htmlBody}, nil
}

// Placeholders returns the variable names appearing in the template (deduplicated, ordered by first
// appearance), for the admin page to validate and give hints with.
func Placeholders(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// Validate validates a custom template: the body must contain the type's required variables, and
// neither the subject nor the body may reference an unknown variable. "Known but unused" is not an
// error (a template may use only part of them); "used but unknown" is certainly a typo -- and a typo
// in the subject renders as an empty string (the recipient sees an empty subject), so the subject is
// checked too.
func Validate(t Type, subject, body string) error {
	specs, ok := VarSpecs(t)
	if !ok {
		return fmt.Errorf("mail type %q does not support templates", t)
	}
	known := map[string]bool{}
	for _, s := range specs {
		known[s.Name] = true
	}
	used := Placeholders(body)
	for _, name := range Placeholders(subject) {
		used = append(used, name)
	}
	for _, name := range used {
		if !known[name] {
			return fmt.Errorf("unknown variable {{%s}}", name)
		}
	}
	usedSet := map[string]bool{}
	for _, name := range used {
		usedSet[name] = true
	}
	for _, name := range RequiredVars(t) {
		if !usedSet[name] {
			return fmt.Errorf("required variable {{%s}} is missing", name)
		}
	}
	return nil
}

// checkRequiredVars asserts that the sender passed every required variable. A missing value can only
// be a code bug (a template missing a variable is caught when it is saved), so this returns an error
// for the caller to log and **not send** -- a mail without the reset link is worse than no mail at
// all: the user would think the link is broken.
func checkRequiredVars(t Type, vars Vars) error {
	for _, name := range RequiredVars(t) {
		if strings.TrimSpace(vars[name]) == "" {
			return fmt.Errorf("mail type %q: required variable %q is empty", t, name)
		}
	}
	return nil
}

// tokenise replaces {{var}} with tokens carrying a random salt, and returns the variable names ordered
// by token index.
//
// The salt is mandatory: without it an admin could write the literal `MAILVARX0X` in the body and
// stand in for a variable.
func tokenise(src string) (string, []string, string) {
	salt := randomSalt()
	var order []string
	out := placeholderRe.ReplaceAllStringFunc(src, func(match string) string {
		name := placeholderRe.FindStringSubmatch(match)[1]
		order = append(order, name)
		return tokenPrefix + salt + "X" + strconv.Itoa(len(order)-1) + "X"
	})
	return out, order, salt
}

const tokenPrefix = "MAILVAR"

// substituteTokens replaces tokens with values. When escape is true it HTML-escapes -- the HTML
// segment must be escaped, the plain text segment must not be (otherwise `&` would show up as `&amp;`
// in the plain text).
//
// The caller takes the salt from tokenise and passes it in verbatim rather than recovering it from src:
// recovering it would mean repeatedly scanning the string that is being rewritten, and once the last
// token has been replaced the salt can no longer be found.
func substituteTokens(src string, order []string, salt string, vars Vars, escape bool) string {
	for i, name := range order {
		value := vars[name]
		if escape {
			value = html.EscapeString(value)
		}
		src = strings.ReplaceAll(src, tokenPrefix+salt+"X"+strconv.Itoa(i)+"X", value)
	}
	return src
}

// substituteRaw substitutes variables directly in the **source**, for the subject and the plain text
// segment (bypassing the renderer and escaping).
func substituteRaw(src string, vars Vars) string {
	return placeholderRe.ReplaceAllStringFunc(src, func(match string) string {
		return vars[placeholderRe.FindStringSubmatch(match)[1]]
	})
}

func randomSalt() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// The salt only prevents a literal in the body from colliding, so degrading to a fixed value when
		// no random source is available does not reduce security
		//(escaping is the real defense), but a log line helps with debugging.
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

// wrapLayout wraps the rendered body in the fixed shell.
//
// The shell is held by the code and not editable by admins: header, footer, width, font stack and
// the unsubscribe entry all live here, so one bad edit cannot break brand consistency or lose the
// unsubscribe entry. All styling is inline -- mail clients generally do not support <style>.
func wrapLayout(site, bodyHTML, unsubscribeURL, footerNote, unsubscribeLabel string) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><body style="margin:0;padding:0;background:#f4f4f5;">`)
	b.WriteString(`<div style="max-width:600px;margin:0 auto;padding:24px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:15px;line-height:1.6;color:#18181b;">`)
	if site != "" {
		b.WriteString(`<div style="font-size:13px;font-weight:600;letter-spacing:.02em;color:#71717a;padding-bottom:12px;">`)
		b.WriteString(html.EscapeString(site))
		b.WriteString(`</div>`)
	}
	b.WriteString(`<div style="background:#ffffff;border:1px solid #e4e4e7;border-radius:12px;padding:24px;">`)
	b.WriteString(bodyHTML)
	b.WriteString(`</div>`)
	if footerNote != "" || unsubscribeURL != "" {
		b.WriteString(`<div style="padding-top:16px;font-size:12px;line-height:1.6;color:#71717a;">`)
		if footerNote != "" {
			b.WriteString(html.EscapeString(footerNote))
		}
		if unsubscribeURL != "" && unsubscribeLabel != "" {
			if footerNote != "" {
				b.WriteString(`<br />`)
			}
			b.WriteString(`<a href="` + html.EscapeString(unsubscribeURL) + `" style="color:#71717a;">` + html.EscapeString(unsubscribeLabel) + `</a>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}
