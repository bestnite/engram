package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// This file is the acceptance for mail templates at real trigger points: it goes through POST /api/v1/auth/forgot-password
// and reads the real enqueued outbox row. The test account's language is en, so the templates are written in en too.

// saveTemplate writes straight to the database, bypassing the admin page's save validation — used to reach states like "the template is already broken".
func saveTemplate(t *testing.T, ts securityTestServer, typ, locale, subject, body string) {
	t.Helper()
	if err := store.NewMailTemplateStore(ts.db).Upsert(context.Background(), &store.MailTemplate{
		Type: typ, Locale: locale, Subject: subject, BodyMD: body, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save mail template: %v", err)
	}
}

// TestPasswordResetMailUsesAdminTemplate asserts that once a template is configured: the subject comes from the template, variables are substituted,
// and an HTML part is produced; at the same time **type A mail still has no unsubscribe entry** (the shell footer must not add an unsubscribe line to security mail).
func TestPasswordResetMailUsesAdminTemplate(t *testing.T) {
	ts := newSecurityServer(t, true)
	saveTemplate(t, ts, "password_reset", "en", "Reset for {{site}}",
		"**Click here**: [reset]({{url}})\n\nLink expires {{expires}}.")

	_, row := issueResetToken(t, ts)
	if !strings.HasPrefix(row.Subject, "Reset for ") {
		t.Errorf("subject = %q, want the subject from the template", row.Subject)
	}
	if !strings.Contains(row.HTMLBody, "<strong>Click here</strong>") {
		t.Errorf("html body = %q, want the template rendered as markdown", row.HTMLBody)
	}
	if !strings.Contains(row.HTMLBody, "/reset-password?token=") {
		t.Errorf("html body = %q, want the reset link substituted", row.HTMLBody)
	}
	if strings.Contains(row.HTMLBody, "{{") {
		t.Errorf("html body = %q, want no placeholder left", row.HTMLBody)
	}
	if !strings.Contains(row.TextBody, "**Click here**") {
		t.Errorf("text body = %q, want the template source with values substituted", row.TextBody)
	}
	if strings.Contains(row.HTMLBody, "Unsubscribe") || strings.Contains(row.TextBody, "Unsubscribe") {
		t.Errorf("class A mail carries an unsubscribe entry: %q", row.HTMLBody)
	}
}

// TestPasswordResetMailWithoutTemplateStaysPlainText is the regression for "shipping the template mechanism does not change existing mail":
// with no template the body is still plain text, the HTML part is empty, and the link is there as before.
func TestPasswordResetMailWithoutTemplateStaysPlainText(t *testing.T) {
	ts := newSecurityServer(t, true)
	_, row := issueResetToken(t, ts)
	if row.HTMLBody != "" {
		t.Errorf("html body = %q, want empty when no template is configured", row.HTMLBody)
	}
	if !strings.Contains(row.TextBody, "/reset-password?token=") {
		t.Errorf("text body = %q, want the built-in body", row.TextBody)
	}
}

// TestBrokenTemplateFallsBackToBuiltinBody is the regression for the promise "a broken template still does not stop the mail going out": when the template drops a required
// variable (here bypassing save validation and writing straight to the database), the mail must still go out, with the built-in body and a usable link reaching the recipient,
// rather than a letter with no link.
func TestBrokenTemplateFallsBackToBuiltinBody(t *testing.T) {
	ts := newSecurityServer(t, true)
	saveTemplate(t, ts, "password_reset", "en", "Broken", "There is no link in this template at all.")

	_, row := issueResetToken(t, ts)
	if !strings.Contains(row.TextBody, "/reset-password?token=") {
		t.Errorf("text body = %q, want the built-in body with a working link", row.TextBody)
	}
	if row.HTMLBody != "" {
		t.Errorf("html body = %q, want the built-in path (no HTML) after a rejected template", row.HTMLBody)
	}
	if strings.HasPrefix(row.Subject, "Broken") {
		t.Errorf("subject = %q, want the built-in subject after a rejected template", row.Subject)
	}
}
