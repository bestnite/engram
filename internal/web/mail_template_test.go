package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件是邮件模板在真实触发点上的验收：走 POST /api/v1/auth/forgot-password，
// 读真实入队的 outbox 行。测试账号的语言是 en，所以模板也按 en 写。

// saveTemplate 直接写库，绕开管理页的保存校验——用来覆盖「模板先坏了」这类状态。
func saveTemplate(t *testing.T, ts securityTestServer, typ, locale, subject, body string) {
	t.Helper()
	if err := store.NewMailTemplateStore(ts.db).Upsert(context.Background(), &store.MailTemplate{
		Type: typ, Locale: locale, Subject: subject, BodyMD: body, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save mail template: %v", err)
	}
}

// TestPasswordResetMailUsesAdminTemplate 断言配了模板之后：主题取自模板、变量被替换、
// 产出 HTML 段；同时**A 类邮件仍然没有退订入口**（外壳页脚不该给安全邮件加退订行）。
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

// TestPasswordResetMailWithoutTemplateStaysPlainText 是「上线模板机制不改动现有邮件」的
// 回归：没有模板时正文仍是纯文本、HTML 段为空、链接照常在。
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

// TestBrokenTemplateFallsBackToBuiltinBody 是「模板坏了也不会让邮件发不出去」那条承诺的回归：模板丢了必填
// 变量（这里绕开保存校验直接写库）时，邮件必须照常发出去，收件人拿到内置正文与可用的链接，
// 而不是一封没有链接的信。
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
