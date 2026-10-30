package mail

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// This file is the renderer for mail templates.
//
// Rendering happens in four steps, and the order is part of the security; do not swap them:
//  1. replace the {{var}} in the template with tokens carrying a per-render random salt;
//  2. replace the tokens with HTML-escaped variable values;
//  3. derive the plain-text part from the substituted HTML with notetype.PlainText;
//  4. wrap the result in the fixed shell held by the code (site name, footer, unsubscribe row).
//
// Nothing is sanitised and nothing is styled at render time: administrators write arbitrary HTML and
// CSS, and the built-in bodies already carry the inline styles, so the renderer only substitutes,
// derives the text part and applies the shell.
//
// **Why tokenise first and substitute afterwards**: variable values contain user-controlled data
// (usernames, deck names, failure reasons). Substituting into the template first would let a value
// such as `<b>x</b>` become markup. Tokenising first means the value never enters the template; step
// 2 is the only step that writes a value, and it escapes it.

// placeholderRe matches {{name}} in a template. Names are restricted to lowercase letters, digits
// and underscores: that way a mathematical notation in the template (the single-brace kind such as
// `\frac{x}{y}`) is not mistaken for a placeholder, and variable names are forced to stay stable
// English identifiers.
var placeholderRe = regexp.MustCompile(`\{\{([a-z0-9_]+)\}\}`)

// leftoverPlaceholderRe matches any brace-delimited placeholder left after substitution. It is wider
// than placeholderRe on purpose: the source may have spelled a placeholder in a way the tokeniser
// does not match (`{{URL}}`, `{{ url }}`), and that text must fail the render rather than reach the
// recipient as literal braces.
var leftoverPlaceholderRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// Vars are the variable values of one mail. The values are untrusted content and are escaped by
// context when rendering.
type Vars map[string]string

// RenderInput is all the input of one render.
type RenderInput struct {
	// Type decides the set of required variables (see vars.go).
	Type Type
	// Site is the site name, used in the shell heading.
	Site string
	// Lang is the language code written into the shell's html lang attribute. A blank value drops the
	// attribute entirely, leaving the client its default.
	Lang string
	// Vars are the variable values.
	Vars Vars

	// Subject and BodyMD are the admin's custom template. The field keeps the body_md name of the
	// stored column and the JSON API, but its content is HTML template text. A blank BodyMD means there
	// is no customization, and FallbackSubject/FallbackBodyMD (the built-in body assembled by the
	// sender) are used -- **the built-in body always lives in code**, so a corrupt or deleted template
	// cannot stop mail from being sent.
	Subject string
	BodyMD  string

	// FallbackSubject and FallbackBodyMD are the built-in body, as HTML template text. Both paths run
	// through the same renderer: the built-in body is not a plain-text special case.
	FallbackSubject string
	FallbackBodyMD  string

	// When UnsubscribeURL is non-empty, the unsubscribe entry is rendered at the bottom of the shell
	// and at the end of the plain text segment (only optional-class mail has it).
	UnsubscribeURL string
	// FooterNote and UnsubscribeLabel are passed in localized by the caller: this package does not
	// depend on i18n, and user-visible copy in mail must come from the language packs (AGENTS.md §2.1).
	FooterNote       string
	UnsubscribeLabel string
}

// Rendered is the render result: subject, plain text segment, HTML segment. A successful render always
// has a non-empty HTML segment; the plain-text segment is derived from the substituted body with
// notetype.PlainText.
type Rendered struct {
	Subject string
	Text    string
	HTML    string
}

// Render renders one mail.
//
// The body is the custom template when one is set, otherwise the sender's built-in body; both are
// HTML template text and go through the same pipeline, so "no template" and "the template saved
// unchanged" are the same mail.
func Render(in RenderInput) (Rendered, error) {
	custom := strings.TrimSpace(in.BodyMD) != ""
	bodyMD := in.BodyMD
	if !custom {
		bodyMD = in.FallbackBodyMD
	}
	if strings.TrimSpace(bodyMD) == "" {
		return Rendered{}, fmt.Errorf("mail type %q: no body to render", in.Type)
	}

	// A custom template must carry the type's required variables. The built-in body is exempt: it is
	// written by this package and must stay sendable even when a sender passes a value it considers
	// optional.
	if custom {
		if err := checkRequiredVars(in.Type, in.Vars); err != nil {
			return Rendered{}, err
		}
	}

	// Subject: when the template does not set one, the built-in subject is kept; the subject must be a
	// single line, otherwise it breaks the RFC 5322 header.
	subject := strings.TrimSpace(substituteRaw(in.Subject, in.Vars))
	subject = strings.ReplaceAll(subject, "\r", " ")
	subject = strings.ReplaceAll(subject, "\n", " ")
	if subject == "" {
		subject = in.FallbackSubject
	}

	// Step 1: placeholders become salted tokens in the template.
	tokenised, order, salt := tokenise(bodyMD)
	// Step 2: tokens become the HTML-escaped variable values.
	substituted := substituteTokens(tokenised, order, salt, in.Vars, true)
	if left := leftoverPlaceholderRe.FindString(substituted); left != "" {
		return Rendered{}, fmt.Errorf("mail type %q: unresolved placeholder %q in body", in.Type, left)
	}
	// Step 3: the text-only part is derived from the substituted HTML, so both parts say the same
	// thing. notetype.PlainText is the one HTML-to-text rule in the repository; links are expanded to
	// "label (url)" first so the text part still carries the destination.
	plain := mailPlainText(substituted)
	if in.UnsubscribeURL != "" && in.UnsubscribeLabel != "" {
		plain = strings.TrimRight(plain, "\n") + "\n\n" + in.UnsubscribeLabel + ": " + in.UnsubscribeURL
	}
	// Step 4: the fixed shell (site name, footer, unsubscribe row) closes the document.
	return Rendered{
		Subject: subject,
		Text:    plain,
		HTML:    wrapLayout(in.Site, in.Lang, substituted, in.FooterNote, in.UnsubscribeURL, in.UnsubscribeLabel),
	}, nil
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
// segment must be escaped. Substitution is one-shot: a value that itself looks like a placeholder is
// left as it is, and Render rejects the result rather than sending literal braces.
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

// substituteRaw substitutes variables directly in the **source**, for the subject and the plain-text
// fallback (bypassing the renderer and escaping).
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

// The fixed shell. The body is inserted into the card cell; the footer is one of three variants so
// that an absent footer leaves no empty band under the card, and the unsubscribe link is always a
// block of its own rather than glued to the end of the note.
//
// The shell is held by the code and not editable by admins: header, footer, width, font stack and
// the unsubscribe entry all live here, so one bad edit cannot break brand consistency or lose the
// unsubscribe entry. All styling is inline -- mail clients generally do not support <style>.
const shellOpen = `<!DOCTYPE html>
<html lang="{{LANG}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="x-apple-disable-message-reformatting">
<meta name="format-detection" content="telephone=no,address=no,email=no,date=no,url=no">
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
<title>{{SITE}}</title>
</head>
<body style="margin:0;padding:0;width:100%;background-color:#f4f4f5;-webkit-text-size-adjust:100%;-ms-text-size-adjust:100%;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;border-collapse:collapse;background-color:#f4f4f5;">
<tr>
<td align="center" style="padding:32px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:15px;line-height:1.6;color:#18181b;">
<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;border-collapse:collapse;">
<tr>
<td style="padding:0 0 14px;border-bottom:1px solid #d4d4d8;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:13px;font-weight:600;line-height:1.4;letter-spacing:-0.01em;color:#18181b;">{{SITE}}</td>
</tr>
<tr>
<td style="height:20px;font-size:20px;line-height:20px;">&nbsp;</td>
</tr>
<tr>
<td style="padding:28px 24px 14px;background-color:#ffffff;border:1px solid #e4e4e7;border-radius:12px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:15px;line-height:1.6;color:#18181b;">
{{BODY}}
</td>
</tr>
`

// shellFooterBoth is the footer when there is a note and a working unsubscribe link.
const shellFooterBoth = `<tr>
<td style="padding:18px 0 0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:12px;line-height:1.65;color:#52525b;">{{FOOTER_NOTE}}<div style="padding-top:8px;"><a href="{{UNSUBSCRIBE_URL}}" style="color:#52525b;text-decoration:underline;">{{UNSUBSCRIBE_LABEL}}</a></div></td>
</tr>
`

// shellFooterNote is the footer when there is only a note.
const shellFooterNote = `<tr>
<td style="padding:18px 0 0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:12px;line-height:1.65;color:#52525b;">{{FOOTER_NOTE}}</td>
</tr>
`

// shellFooterUnsubOnly is the footer when there is only an unsubscribe link.
const shellFooterUnsubOnly = `<tr>
<td style="padding:18px 0 0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;font-size:12px;line-height:1.65;color:#52525b;"><a href="{{UNSUBSCRIBE_URL}}" style="color:#52525b;text-decoration:underline;">{{UNSUBSCRIBE_LABEL}}</a></td>
</tr>
`

// shellClose closes the tables and the document.
const shellClose = `</table>
</td>
</tr>
</table>
</body>
</html>
`

// wrapLayout assembles the fixed shell. The footer row is built from one of three templates so that
// an absent footer leaves no empty band under the card, and the unsubscribe link is always a block
// of its own (never glued to the end of the note).
//
// {{LANG}} goes into an attribute and {{SITE}} / {{FOOTER_NOTE}} / {{UNSUBSCRIBE_LABEL}} into text, so
// all of them are escaped. A blank language drops the attribute instead of writing lang="": an empty
// value claims the document has no language, while an absent attribute leaves the client its default.
// {{BODY}} is the substituted template HTML; it is inserted verbatim. Nothing in it is sanitised.
//
// The shell placeholders are uppercase while template variables are lowercase, so the two naming
// schemes cannot collide: tokenise only matches lowercase names, and the strings.Replacer does not
// rescan the text it inserts.
func wrapLayout(site, lang, body, footerNote, unsubURL, unsubLabel string) string {
	// The <html> tag is decided before the general replacement: dropping the whole attribute needs the
	// tag, not just the value.
	open := shellOpen
	if lang = strings.TrimSpace(lang); lang == "" {
		open = strings.Replace(open, ` lang="{{LANG}}"`, "", 1)
	} else {
		open = strings.Replace(open, "{{LANG}}", html.EscapeString(lang), 1)
	}

	var b strings.Builder
	b.WriteString(strings.NewReplacer(
		"{{SITE}}", html.EscapeString(site),
		"{{BODY}}", body,
	).Replace(open))
	switch {
	case footerNote != "" && unsubURL != "" && unsubLabel != "":
		b.WriteString(strings.NewReplacer(
			"{{FOOTER_NOTE}}", html.EscapeString(footerNote),
			"{{UNSUBSCRIBE_URL}}", html.EscapeString(unsubURL),
			"{{UNSUBSCRIBE_LABEL}}", html.EscapeString(unsubLabel),
		).Replace(shellFooterBoth))
	case footerNote != "":
		b.WriteString(strings.Replace(shellFooterNote, "{{FOOTER_NOTE}}", html.EscapeString(footerNote), 1))
	case unsubURL != "" && unsubLabel != "":
		b.WriteString(strings.NewReplacer(
			"{{UNSUBSCRIBE_URL}}", html.EscapeString(unsubURL),
			"{{UNSUBSCRIBE_LABEL}}", html.EscapeString(unsubLabel),
		).Replace(shellFooterUnsubOnly))
	}
	b.WriteString(shellClose)
	return b.String()
}
