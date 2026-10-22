package mail

import (
	"strings"
	"testing"
)

// These cases pin down the rendering order: render and sanitize first, substitute variables
// afterwards. Reversing the order is stored injection -- variable values are user-controlled data.

func renderOne(t *testing.T, in RenderInput) Rendered {
	t.Helper()
	out, err := Render(in)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return out
}

// TestRenderWithoutTemplateUsesFallbackVerbatim asserts that without a custom template the behavior
// is exactly as before: subject and plain text come from the sender and the HTML segment is empty.
// This guarantees that shipping the template mechanism changes no existing mail.
func TestRenderWithoutTemplateUsesFallbackVerbatim(t *testing.T) {
	got := renderOne(t, RenderInput{
		Type:            TypePasswordReset,
		Site:            "Engram",
		Vars:            Vars{"url": "https://example.com/reset", "expires": "tomorrow"},
		FallbackSubject: "Reset your password",
		FallbackText:    "line one\nline two",
	})
	if got.Subject != "Reset your password" || got.Text != "line one\nline two" {
		t.Errorf("fallback = %q / %q, want the sender's own text verbatim", got.Subject, got.Text)
	}
	if got.HTML != "" {
		t.Errorf("HTMLBody = %q, want empty when no template is set", got.HTML)
	}
}

// TestRenderTemplateMarkdownAndShell asserts that a custom template goes Markdown -> HTML and is
// wrapped in the shell.
func TestRenderTemplateMarkdownAndShell(t *testing.T) {
	got := renderOne(t, RenderInput{
		Type:    TypePasswordReset,
		Site:    "Engram",
		Vars:    Vars{"url": "https://example.com/reset", "expires": "2026-01-01", "site": "Engram"},
		Subject: "Reset for {{site}}",
		BodyMD:  "**Hi**\n\n[Reset]({{url}})\n\nExpires {{expires}}.",
	})
	if got.Subject != "Reset for Engram" {
		t.Errorf("subject = %q, want the template subject with vars substituted", got.Subject)
	}
	if !strings.Contains(got.HTML, "<strong>Hi</strong>") {
		t.Errorf("HTML = %q, want markdown rendered", got.HTML)
	}
	if !strings.Contains(got.HTML, "https://example.com/reset") {
		t.Errorf("HTML = %q, want the link target substituted", got.HTML)
	}
	if !strings.Contains(got.HTML, "<!DOCTYPE html>") {
		t.Errorf("HTML = %q, want the fixed shell", got.HTML)
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
				Site:    "Engram",
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

// TestRenderDoesNotRescanSubstitutedValues asserts that substitution is one-shot: a {{...}} written
// inside a value is not substituted a second time (otherwise a user could use someone else's value as
// a template directive).
func TestRenderDoesNotRescanSubstitutedValues(t *testing.T) {
	got := renderOne(t, RenderInput{
		Type:    TypePasswordReset,
		Vars:    Vars{"url": "https://example.com", "expires": "x", "username": "{{url}}"},
		Subject: "s",
		BodyMD:  "Hi {{username}}",
	})
	if !strings.Contains(got.HTML, "{{url}}") {
		t.Errorf("HTML = %q, want the literal value kept unsubstituted", got.HTML)
	}
}

// TestRenderUnsubscribeRow asserts that the unsubscribe entry goes into both the HTML footer and the
// plain text segment (optional-class mail must be able to reach it), while mail without an
// unsubscribe URL (class A) renders neither.
func TestRenderUnsubscribeRow(t *testing.T) {
	base := RenderInput{
		Type:             TypeReviewReminder,
		Site:             "Engram",
		Vars:             Vars{"count": "7", "url": "https://example.com/review", "unsubscribe_url": "https://example.com/u/t"},
		Subject:          "due",
		BodyMD:           "{{count}} cards are due.",
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

// TestValidateBodyRejectsUnknownType asserts that a type with no sender integrated does not accept
// templates.
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
