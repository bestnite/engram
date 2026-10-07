package mail

import (
	"strings"
	"testing"
)

// 这些用例锁的是渲染顺序（DESIGN.md §4.7）：先渲染清洗、后替换变量。顺序反过来就是
// 存储型注入——变量值是用户可控数据。

func renderOne(t *testing.T, in RenderInput) Rendered {
	t.Helper()
	out, err := Render(in)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	return out
}

// TestRenderWithoutTemplateUsesFallbackVerbatim 断言没有自定义模板时行为与今天完全一致：
// 主题与纯文本来自发信方，HTML 段为空。这条保证「模板机制上线」不会改变任何现有邮件。
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

// TestRenderTemplateMarkdownAndShell 断言自定义模板走 Markdown → HTML，并包进外壳。
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

// TestRenderEscapesVariableValues 是安全负例：变量值里有 HTML 或 Markdown 标记时，
// 在 HTML 段里必须原样可见（被转义），绝不能被当成标记解释。值来自用户可控数据
// （用户名、卡组名、失败原因）。
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

// TestRenderDoesNotRescanSubstitutedValues 断言替换是一次性的：值里再写 {{...}} 也
// 不会被二次替换（否则用户能把别人的值当模板指令用）。
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

// TestRenderUnsubscribeRow 断言退订入口同时进 HTML 页脚与纯文本段（可选类邮件必须可达），
// 没有退订 URL 的（A 类）则一处都不渲染。
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

// TestRenderOptionalVarMissingLeavesNoPlaceholder 断言模板引用了可选变量、发信方没传时
// 留下的是空串而不是字面量 {{x}}（用户会在邮件里看到花括号）。
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

// TestRenderRejectsMissingRequiredVar 断言发信方漏传必填变量时**报错而不是发出残信**：
// 一封没有重置链接的密码邮件比不发更糟。
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

// TestRenderSubjectStaysSingleLine 断言主题里的换行被压平：多行主题会破坏邮件头。
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

// TestValidateBody 锁定保存时的校验：认识的变量可选、不认识的必拒、必填的必写。
func TestValidateBody(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"ok", "Reset: {{url}} (expires {{expires}})", ""},
		{"missing required", "Reset: {{url}}", "expires"},
		{"unknown var", "Reset: {{url}} {{expires}} {{pasword}}", "pasword"},
		{"no placeholders at all", "Reset here", "url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBody(TypePasswordReset, tc.body)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateBody() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("ValidateBody() error = nil, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidateBodyRejectsUnknownType 断言没接入发信方的类型不接受模板。
func TestValidateBodyRejectsUnknownType(t *testing.T) {
	if err := ValidateBody(Type("no_such_type"), "{{anything}}"); err == nil {
		t.Fatal("ValidateBody() error = nil, want an error for a type without senders")
	}
}

// TestPlaceholdersKeepsOrderAndDedupes 断言管理页展示的变量清单是稳定顺序且去重的。
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

// TestVarsTableCoversEveryTypeWithSenders 断言「表里有变量」与「发信方存在」一致：
// 少了变量表，管理员就配不了模板；多了没有发信方的类型，页面会长出一个永远不发的模板。
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

// TestRequiredVarsAlwaysIncludeLinks 是设计约束的守卫：需要链接才能收场的类型，链接必填。
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
	// 可选类邮件必须把退订链接列进必填：没有退订入口的营销类邮件是违规的。
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
