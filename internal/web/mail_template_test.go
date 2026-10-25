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
// the HTML part carries the rendered Markdown, and the plain-text part is derived from the same tree (no Markdown markers, links keep their URL);
// at the same time **type A mail still has no unsubscribe entry** (the shell footer must not add an unsubscribe line to security mail).
func TestPasswordResetMailUsesAdminTemplate(t *testing.T) {
	ts := newSecurityServer(t, true)
	saveTemplate(t, ts, "password_reset", "en", "Reset for {{site}}",
		"**Click here**: [reset]({{url}})\n\nLink expires {{expires}}.")

	_, row := issueResetToken(t, ts)
	if !strings.HasPrefix(row.Subject, "Reset for ") {
		t.Errorf("subject = %q, want the subject from the template", row.Subject)
	}
	if !strings.Contains(row.HTMLBody, "Click here</strong>") {
		t.Errorf("html body = %q, want the template rendered as markdown", row.HTMLBody)
	}
	if !strings.Contains(row.HTMLBody, "/reset-password?token=") {
		t.Errorf("html body = %q, want the reset link substituted", row.HTMLBody)
	}
	if strings.Contains(row.HTMLBody, "{{") {
		t.Errorf("html body = %q, want no placeholder left", row.HTMLBody)
	}
	// The plain-text part is derived from the rendered tree: Markdown emphasis is gone, and the link
	// keeps its target as "label (url)" so a plain-text reader can still open it.
	if strings.Contains(row.TextBody, "**") {
		t.Errorf("text body = %q, want no Markdown markers", row.TextBody)
	}
	if !strings.Contains(row.TextBody, "reset (http") || !strings.Contains(row.TextBody, "/reset-password?token=") {
		t.Errorf("text body = %q, want the link as label (url)", row.TextBody)
	}
	if !strings.Contains(row.TextBody, "Link expires") {
		t.Errorf("text body = %q, want the rest of the template body", row.TextBody)
	}
	if strings.Contains(row.HTMLBody, "Unsubscribe") || strings.Contains(row.TextBody, "Unsubscribe") {
		t.Errorf("class A mail carries an unsubscribe entry: %q", row.HTMLBody)
	}
}

// TestPasswordResetMailWithoutTemplateRendersBuiltinHTML asserts that with no template the built-in body goes through the
// same renderer: the HTML part is the fixed shell plus the built-in body, and the plain-text part still carries the link.
func TestPasswordResetMailWithoutTemplateRendersBuiltinHTML(t *testing.T) {
	ts := newSecurityServer(t, true)
	_, row := issueResetToken(t, ts)
	if !strings.Contains(row.HTMLBody, "<!DOCTYPE html>") || !strings.Contains(row.HTMLBody, `<html lang="en">`) {
		t.Errorf("html body = %q, want the fixed HTML shell", row.HTMLBody)
	}
	if !strings.Contains(row.HTMLBody, "Reset your password") {
		t.Errorf("html body = %q, want the built-in heading", row.HTMLBody)
	}
	if !strings.Contains(row.HTMLBody, "/reset-password?token=") {
		t.Errorf("html body = %q, want the reset link", row.HTMLBody)
	}
	// Every mail carries a footer now; class A has the automated note and no unsubscribe entry.
	if !strings.Contains(row.HTMLBody, "This message was sent automatically.") {
		t.Errorf("html body = %q, want the automated footer note", row.HTMLBody)
	}
	if strings.Contains(row.HTMLBody, "Unsubscribe") {
		t.Errorf("html body = %q, want no unsubscribe entry for class A", row.HTMLBody)
	}
	if !strings.Contains(row.TextBody, "/reset-password?token=") {
		t.Errorf("text body = %q, want the built-in body", row.TextBody)
	}
	if strings.Contains(row.TextBody, "{{") || strings.Contains(row.TextBody, "**") {
		t.Errorf("text body = %q, want a rendered plain-text part", row.TextBody)
	}
}

// TestBrokenTemplateFallsBackToBuiltinBody is the regression for the promise "a broken template still does not stop the mail going out": when the template drops a required
// variable (here bypassing save validation and writing straight to the database), the mail must still go out with the built-in subject and body,
// with a usable link reaching the recipient, rather than a letter with no link or an empty HTML part.
func TestBrokenTemplateFallsBackToBuiltinBody(t *testing.T) {
	ts := newSecurityServer(t, true)
	saveTemplate(t, ts, "password_reset", "en", "Broken", "There is no link in this template at all.")

	_, row := issueResetToken(t, ts)
	if !strings.Contains(row.TextBody, "/reset-password?token=") {
		t.Errorf("text body = %q, want the built-in body with a working link", row.TextBody)
	}
	if !strings.Contains(row.HTMLBody, "/reset-password?token=") {
		t.Errorf("html body = %q, want the built-in HTML body with a working link", row.HTMLBody)
	}
	if !strings.Contains(row.HTMLBody, "Reset your password") {
		t.Errorf("html body = %q, want the built-in body after a rejected template", row.HTMLBody)
	}
	if row.Subject != "Reset your Engram password" {
		t.Errorf("subject = %q, want the built-in subject after a rejected template", row.Subject)
	}
}
