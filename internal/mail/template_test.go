package mail

import (
	"strings"
	"testing"
)

// These cases pin down the rendering order: render and sanitize first, substitute variables
// afterwards, then add styles. Reversing the order is stored injection -- variable values are
// user-controlled data.

func renderOne(t *testing.T, in RenderInput) Rendered {
	t.Helper()
	out, err := Render(in)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return out
}

// TestRenderWithoutTemplateRendersBuiltInBody asserts that a mail with no custom template is a
// designed mail: the built-in Markdown body goes through the same renderer, so the HTML segment is
// present and carries the fixed shell. "No template" is no longer a plain-text special case.
func TestRenderWithoutTemplateRendersBuiltInBody(t *testing.T) {
	got := renderOne(t, RenderInput{
		Type:            TypePasswordReset,
		Site:            "Example",
		Lang:            "en",
		Vars:            Vars{"url": "https://example.com/reset", "expires": "tomorrow", "site": "Example"},
		FallbackSubject: "Reset your password",
		FallbackBodyMD:  "## Reset your password\n\nUse the link below.\n\n[Choose a new password]({{url}})",
	})
	if got.Subject != "Reset your password" {
		t.Errorf("subject = %q, want the sender's subject verbatim", got.Subject)
	}
	if got.HTML == "" {
		t.Fatal("HTML = \"\", want the built-in body rendered to HTML")
	}
	for _, want := range []string{"<!DOCTYPE html>", `<html lang="en">`, "<h2", "<p", buttonStyle} {
		if !strings.Contains(got.HTML, want) {
			t.Errorf("HTML does not contain %q:\n%s", want, got.HTML)
		}
	}
	if !strings.Contains(got.Text, "Choose a new password (https://example.com/reset)") {
		t.Errorf("text = %q, want the link as label and URL", got.Text)
	}
}

// TestRenderTemplateMarkdownAndShell asserts that a custom template goes Markdown -> HTML and is
// wrapped in the shell.
func TestRenderTemplateMarkdownAndShell(t *testing.T) {
	got := renderOne(t, RenderInput{
		Type:    TypePasswordReset,
		Site:    "Example",
		Vars:    Vars{"url": "https://example.com/reset", "expires": "2026-01-01", "site": "Example"},
		Subject: "Reset for {{site}}",
		BodyMD:  "## Hi\n\n**Bold** paragraph.\n\n[Open]({{url}})\n\nExpires {{expires}}.",
	})
	if got.Subject != "Reset for Example" {
		t.Errorf("subject = %q, want the template subject with vars substituted", got.Subject)
	}
	for _, want := range []string{"<h2", "Bold</strong>", "https://example.com", "<!DOCTYPE html>"} {
		if !strings.Contains(got.HTML, want) {
			t.Errorf("HTML does not contain %q:\n%s", want, got.HTML)
		}
	}
	if strings.Contains(got.HTML, "{{") {
		t.Errorf("HTML = %q, want no placeholder left", got.HTML)
	}
}

// TestRenderEscapesVariableValues is the security negative case: when a variable value contains HTML
// or Markdown markup it must be visible verbatim (escaped) in the HTML segment, never interpreted as
// markup. The values come from user-controlled data (usernames, deck names, failure reasons).
func TestRenderEscapesVariableValues(t *testing.T) {
	cases := []struct {
		name       string
		value      string
		wantInHTML string
		wantAbsent string
	}{
		{"html tag", `<script>alert(1)</script>`, "&lt;script&gt;alert(1)&lt;/script&gt;", "<script>alert(1)</script>"},
		{"markdown bold", `**not bold**`, "**not bold**", "<strong>not bold</strong>"},
		{"attribute break", `" onmouseover="alert(1)`, `&#34; onmouseover=&#34;alert(1)`, `" onmouseover="alert(1)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderOne(t, RenderInput{
				Type:    TypePasswordReset,
				Site:    "Example",
				Vars:    Vars{"url": "https://example.com", "expires": "x", "username": tc.value},
				Subject: "s",
				BodyMD:  "Hello {{username}}",
			})
			if !strings.Contains(got.HTML, tc.wantInHTML) {
				t.Errorf("HTML = %q, want it to contain %q", got.HTML, tc.wantInHTML)
			}
			if strings.Contains(got.HTML, tc.wantAbsent) {
				t.Errorf("HTML = %q, must not contain raw %q", got.HTML, tc.wantAbsent)
			}
		})
	}
}

// TestRenderRejectsValueThatLooksLikePlaceholder guards the "never send literal braces" rule:
// substitution is one-shot, so a value that itself contains {{...}} stays literal and the render
// fails instead of delivering placeholder text to the recipient.
func TestRenderRejectsValueThatLooksLikePlaceholder(t *testing.T) {
	_, err := Render(RenderInput{
		Type:    TypePasswordReset,
		Vars:    Vars{"url": "https://example.com", "expires": "x", "username": "{{url}}"},
		Subject: "s",
		BodyMD:  "Hi {{username}}",
	})
	if err == nil {
		t.Fatal("Render() error = nil, want an unresolved-placeholder error")
	}
	if !strings.Contains(err.Error(), "{{url}}") {
		t.Errorf("error = %v, want it to name the leftover placeholder", err)
	}
}

// TestRenderRejectsSpellingTheTokeniserCannotMatch asserts that a mistyped placeholder (one the
// lowercase-only tokeniser does not recognise) fails the render instead of reaching the recipient.
func TestRenderRejectsSpellingTheTokeniserCannotMatch(t *testing.T) {
	_, err := Render(RenderInput{
		Type:    TypePasswordReset,
		Vars:    Vars{"url": "https://example.com", "expires": "x"},
		Subject: "s",
		BodyMD:  "Hi {{ URL }}",
	})
	if err == nil {
		t.Fatal("Render() error = nil, want an unresolved-placeholder error")
	}
}

// TestRenderNoBodyIsError asserts that a caller with neither a template nor a built-in body gets an
// error instead of an empty mail.
func TestRenderNoBodyIsError(t *testing.T) {
	if _, err := Render(RenderInput{Type: TypePasswordReset}); err == nil {
		t.Fatal("Render() error = nil, want an error when there is no body at all")
	}
}

// TestRenderUnsubscribeRow asserts that the unsubscribe entry goes into both the HTML footer and the
// plain text segment (optional-class mail must be able to reach it), while mail without an
// unsubscribe URL (class A) renders neither.
func TestRenderUnsubscribeRow(t *testing.T) {
	base := RenderInput{
		Type:             TypeReviewReminder,
		Site:             "Example",
		Vars:             Vars{"count": "7", "url": "https://example.com/review", "unsubscribe_url": "https://example.com/u/t"},
		Subject:          "due",
		FallbackBodyMD:   "## Time to review\n\n{{count}} cards are due.",
		UnsubscribeURL:   "https://example.com/u/t",
		UnsubscribeLabel: "Unsubscribe",
		FooterNote:       "You get this because reminders are on.",
	}
	got := renderOne(t, base)
	if !strings.Contains(got.HTML, "https://example.com/u/t") {
		t.Errorf("HTML = %q, want the unsubscribe link in the footer", got.HTML)
	}
	if !strings.Contains(got.Text, "Unsubscribe: https://example.com/u/t") {
		t.Errorf("text = %q, want the unsubscribe line reachable without HTML", got.Text)
	}

	noUnsub := base
	noUnsub.UnsubscribeURL = ""
	noUnsub.UnsubscribeLabel = ""
	noUnsub.FooterNote = ""
	got = renderOne(t, noUnsub)
	if strings.Contains(got.HTML, "Unsubscribe") {
		t.Errorf("HTML = %q, want no unsubscribe row for this type", got.HTML)
	}
}

// TestRenderOptionalVarMissingLeavesNoPlaceholder asserts that when a template references an optional
// variable the sender did not pass, what is left is an empty string rather than the literal {{x}} (the
// user would see braces in the mail).
func TestRenderOptionalVarMissingLeavesNoPlaceholder(t *testing.T) {
	got := renderOne(t, RenderInput{
		Type:    TypePasswordReset,
		Vars:    Vars{"url": "https://example.com", "expires": "x"},
		Subject: "s",
		BodyMD:  "Hi {{username}}",
	})
	if strings.Contains(got.HTML, "{{") || strings.Contains(got.Text, "{{") {
		t.Errorf("html = %q / text = %q, want placeholders gone", got.HTML, got.Text)
	}
}

// TestRenderRejectsMissingRequiredVar asserts that a sender forgetting a required variable **errors
// out instead of sending a crippled mail**: a password mail without the reset link is worse than
// nothing.
func TestRenderRejectsMissingRequiredVar(t *testing.T) {
	_, err := Render(RenderInput{
		Type:    TypePasswordReset,
		Vars:    Vars{"expires": "x"},
		Subject: "s",
		BodyMD:  "Reset: {{url}}",
	})
	if err == nil {
		t.Fatal("Render() error = nil, want a required-variable error")
	}
	if !strings.Contains(err.Error(), "url") {
		t.Errorf("error = %v, want it to name the missing variable", err)
	}
}

// TestRenderSubjectStaysSingleLine asserts that newlines in the subject are flattened: a multi-line
// subject breaks the mail headers.
func TestRenderSubjectStaysSingleLine(t *testing.T) {
	got := renderOne(t, RenderInput{
		Type:    TypePasswordReset,
		Vars:    Vars{"url": "https://example.com", "expires": "x"},
		Subject: "a\nb",
		BodyMD:  "x {{url}}",
	})
	if strings.ContainsAny(got.Subject, "\r\n") {
		t.Errorf("subject = %q, want a single line", got.Subject)
	}
}

// TestRenderLangAttribute asserts that the shell declares the language only when one is supplied: a
// blank value drops the attribute rather than writing lang="", which would claim the document has no
// language. The value goes into an attribute, so it must be escaped.
func TestRenderLangAttribute(t *testing.T) {
	cases := []struct {
		name     string
		lang     string
		want     string
		wantGone string
	}{
		{"english", "en", `<html lang="en">`, ""},
		{"regional", "zh-CN", `<html lang="zh-CN">`, ""},
		{"empty", "", `<html>`, `lang=`},
		{"whitespace only", "   ", `<html>`, `lang=`},
		{"attribute escape", `en" onload="x`, `<html lang="en&#34; onload=&#34;x">`, `" onload="`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderOne(t, RenderInput{
				Type:   TypePasswordReset,
				Site:   "Example",
				Lang:   tc.lang,
				Vars:   Vars{"url": "https://example.com", "expires": "x"},
				BodyMD: "x",
			})
			if !strings.Contains(got.HTML, tc.want) {
				t.Errorf("HTML does not contain %q:\n%s", tc.want, got.HTML)
			}
			if tc.wantGone != "" && strings.Contains(got.HTML, tc.wantGone) {
				t.Errorf("HTML still contains %q:\n%s", tc.wantGone, got.HTML)
			}
			if strings.Contains(got.HTML, "{{") {
				t.Errorf("HTML still contains a shell placeholder:\n%s", got.HTML)
			}
		})
	}
}

// TestRenderPlainTextIsDerivedFromTheRenderedBody asserts that the text-only part is not the Markdown
// source: markers are gone, headings stay as text, links keep their URL, and no placeholder survives.
func TestRenderPlainTextIsDerivedFromTheRenderedBody(t *testing.T) {
	got := renderOne(t, RenderInput{
		Type:    TypePasswordReset,
		Site:    "Example",
		Vars:    Vars{"url": "https://example.com/reset", "expires": "2026-01-01"},
		Subject: "s",
		BodyMD:  "## Title\n\nSome **bold** text.\n\n[Reset]({{url}})\n\n> Note {{expires}}.",
	})
	for _, bad := range []string{"##", "**", "{{", "[Reset]"} {
		if strings.Contains(got.Text, bad) {
			t.Errorf("text = %q, must not contain Markdown marker %q", got.Text, bad)
		}
	}
	for _, want := range []string{"Title", "Some bold text.", "Reset (https://example.com/reset)", "Note 2026-01-01."} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("text = %q, want %q", got.Text, want)
		}
	}
}

// TestRenderJobFailedReasonInPre asserts that the failure reason is rendered as a code block and is
// escaped there: a stack trace must survive line breaks without becoming markup.
func TestRenderJobFailedReasonInPre(t *testing.T) {
	got := renderOne(t, RenderInput{
		Type: TypeJobFailed,
		Site: "Example",
		Vars: Vars{
			"site": "Example", "job_id": "42", "kind": "optimize",
			"reason": "line one <script>alert(1)</script>\nline two",
		},
		Subject:        "failed",
		FallbackBodyMD: "## A background job failed\n\nReason:\n\n```\n{{reason}}\n```",
	})
	start := strings.Index(got.HTML, "<pre")
	if start < 0 {
		t.Fatalf("HTML has no <pre> block:\n%s", got.HTML)
	}
	pre := got.HTML[start:]
	end := strings.Index(pre, "</pre>")
	if end < 0 {
		t.Fatalf("HTML has an unterminated <pre> block:\n%s", got.HTML)
	}
	pre = pre[:end]
	if !strings.Contains(pre, "line one") || !strings.Contains(pre, "line two") {
		t.Errorf("pre block = %q, want both reason lines", pre)
	}
	if strings.Contains(pre, "<script>") {
		t.Errorf("pre block = %q, must escape the reason", pre)
	}
	if !strings.Contains(pre, "&lt;script&gt;") {
		t.Errorf("pre block = %q, want the reason escaped", pre)
	}
	if !strings.Contains(pre, "white-space:pre-wrap") {
		t.Errorf("pre block = %q, want the code-block style", pre)
	}
	if !strings.Contains(got.Text, "line one <script>alert(1)</script>") {
		t.Errorf("text = %q, want the decoded reason", got.Text)
	}
}

// TestValidate pins down the validation on save: known variables are optional, unknown ones must be
// rejected, required ones must be written.
func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"ok", "Reset: {{url}} (expires {{expires}})", ""},
		// Required admits only variables "without which the mail is useless": display details such as the
		// expiry time are no longer required, so "only expires written" is the failure of a missing url.
		{"missing required", "Reset here, expires {{expires}}", "url"},
		{"unknown var", "Reset: {{url}} {{expires}} {{pasword}}", "pasword"},
		{"no placeholders at all", "Reset here", "url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(TypePasswordReset, "", tc.body)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidateRejectsUnknownType asserts that a type with no sender integrated does not accept templates.
func TestValidateRejectsUnknownType(t *testing.T) {
	if err := Validate(Type("no_such_type"), "", "{{anything}}"); err == nil {
		t.Fatal("Validate() error = nil, want an error for a type without senders")
	}
}

// TestPlaceholdersKeepsOrderAndDedupes asserts that the variable list shown on the admin page is in a
// stable order and deduplicated.
func TestPlaceholdersKeepsOrderAndDedupes(t *testing.T) {
	got := Placeholders("{{b}} {{a}} {{b}} {{c}}")
	want := []string{"b", "a", "c"}
	if len(got) != len(want) {
		t.Fatalf("Placeholders() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Placeholders() = %v, want %v (order must follow first appearance)", got, want)
		}
	}
}

// TestVarsTableCoversEveryTypeWithSenders asserts that "the table has variables" and "a sender
// exists" agree: without a variable table an admin cannot configure a template; a type with no sender
// in the table would grow a template on the page that never sends.
func TestVarsTableCoversEveryTypeWithSenders(t *testing.T) {
	for _, def := range Catalog() {
		if !SupportsTemplate(def.Type) {
			t.Errorf("type %q is in the catalogue but has no template variables", def.Type)
		}
	}
	for typ := range mailVars {
		found := false
		for _, def := range Catalog() {
			if def.Type == typ {
				found = true
			}
		}
		if !found {
			t.Errorf("type %q has template variables but is not in the catalogue", typ)
		}
	}
}

// TestRequiredVarsAlwaysIncludeLinks guards the design constraint: a type that needs a link to be
// closed out has the link as required.
func TestRequiredVarsAlwaysIncludeLinks(t *testing.T) {
	cases := map[Type]string{
		TypePasswordReset:     "url",
		TypeEmailVerification: "url",
		TypeInvite:            "url",
		TypeReviewReminder:    "url",
	}
	for typ, want := range cases {
		got := RequiredVars(typ)
		found := false
		for _, name := range got {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("RequiredVars(%q) = %v, want it to include %q", typ, got, want)
		}
	}
	// Optional-class mail must list the unsubscribe link as required: marketing mail without an
	// unsubscribe entry is non-compliant.
	for _, typ := range []Type{TypeReviewReminder, TypeStudyDigest} {
		got := RequiredVars(typ)
		found := false
		for _, name := range got {
			if name == "unsubscribe_url" {
				found = true
			}
		}
		if !found {
			t.Errorf("RequiredVars(%q) = %v, want it to include unsubscribe_url", typ, got)
		}
	}
}

// TestRenderOrFallbackUsesBuiltInBodyForInvalidTemplate asserts the promise that a broken template
// still produces a designed mail: an invalid custom template (subject and body) is dropped and the
// built-in body is rendered instead.
func TestRenderOrFallbackUsesBuiltInBodyForInvalidTemplate(t *testing.T) {
	out := RenderOrFallback(RenderInput{
		Type:            TypePasswordReset,
		Site:            "Example",
		Vars:            Vars{"url": "https://example.com/reset", "site": "Example"},
		Subject:         "custom",
		BodyMD:          "This template dropped the required link",
		FallbackSubject: "Reset",
		FallbackBodyMD:  "## Reset\n\n[Go]({{url}})",
	})
	if out.HTML == "" {
		t.Fatal("HTML = \"\", want the built-in body rendered")
	}
	if !strings.Contains(out.HTML, `<h2`) || !strings.Contains(out.HTML, buttonStyle) {
		t.Errorf("HTML = %q, want the built-in designed body", out.HTML)
	}
	if out.Subject != "Reset" {
		// A template is one unit: the custom subject goes with the custom body, so the mail does not
		// announce something the recipient does not get.
		t.Errorf("subject = %q, want the built-in subject", out.Subject)
	}
}

// TestRenderOrFallbackReturnsPlainTextOnRenderError asserts the last line of defence: when even the
// renderer fails, the function still returns a subject and text and no error, so sending never fails
// because of a template.
func TestRenderOrFallbackReturnsPlainTextOnRenderError(t *testing.T) {
	out := RenderOrFallback(RenderInput{
		Type:            TypePasswordReset,
		Vars:            Vars{"url": "https://example.com/reset"},
		FallbackSubject: "Reset",
		FallbackBodyMD:  "Reset at {{url}}\n\n{{ typo }}",
	})
	if out.Subject != "Reset" {
		t.Errorf("subject = %q, want the fallback subject", out.Subject)
	}
	if out.HTML != "" {
		t.Errorf("HTML = %q, want the plain-text fallback only", out.HTML)
	}
	if !strings.Contains(out.Text, "Reset at https://example.com/reset") {
		t.Errorf("text = %q, want the built-in Markdown with variables substituted", out.Text)
	}
}
