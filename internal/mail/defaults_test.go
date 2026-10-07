package mail

import (
	"sort"
	"strings"
	"testing"
)

// echoTF 是一个可断言的假翻译函数：把每一次调用记成 `id(k1=v1,k2=v2)`。
//
// 用它而不是真语言包，是为了断言**取值映射**——摘要的三行都写 `{{.count}}`，
// 读的却是三个不同的数，只有把「这一行拿到了哪个变量的值」钉住才知道映射对不对。
func echoTF() TfFunc {
	return func(id string, data map[string]any) string {
		keys := make([]string, 0, len(data))
		for k := range data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+stringOf(data[k]))
		}
		return id + "(" + strings.Join(parts, ",") + ")"
	}
}

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "?"
}

// TestDefaultTemplateIsSavableAsIs 是「编辑框预填默认」这个功能的前提。
//
// 管理员最常见的动作是**不改就存**（先看一眼默认长什么样）。预填的文本必须能通过保存
// 校验，否则那个动作会得到一个 400——而它本来应该是最安全的一次操作。
func TestDefaultTemplateIsSavableAsIs(t *testing.T) {
	for _, def := range Catalog() {
		typ := def.Type
		subject, body, ok := DefaultTemplate(typ, VariantDefault, echoTF(), PlaceholderVars(typ))
		if !ok {
			t.Errorf("%s: no built-in template", typ)
			continue
		}
		if strings.TrimSpace(subject) == "" || strings.TrimSpace(body) == "" {
			t.Errorf("%s: built-in subject/body is empty (%q / %q)", typ, subject, body)
		}
		if err := Validate(typ, subject, body); err != nil {
			t.Errorf("%s: the built-in template does not pass Validate: %v\nbody = %q", typ, err, body)
		}
		// 必填变量必须出现在正文里，且用的是模板写法（`{{name}}`）——它会被原样填进编辑框。
		used := map[string]bool{}
		for _, name := range Placeholders(body) {
			used[name] = true
		}
		for _, name := range RequiredVars(typ) {
			if !used[name] {
				t.Errorf("%s: built-in body does not use the required variable {{%s}}", typ, name)
			}
		}
	}
}

// TestDefaultTemplateRendersWithRealValues 断言预填的默认不只是「能存」，还真的能渲染：
// 把占位符换成真实值后走一次渲染管线，值必须出现在正文里。
func TestDefaultTemplateRendersWithRealValues(t *testing.T) {
	for _, def := range Catalog() {
		typ := def.Type
		placeholder := PlaceholderVars(typ)
		subject, body, _ := DefaultTemplate(typ, VariantDefault, echoTF(), placeholder)

		real := Vars{}
		for name := range placeholder {
			real[name] = "V<" + name + ">"
		}
		out, err := Render(RenderInput{
			Type: typ, Site: "SITE", Vars: real,
			Subject: subject, BodyMD: body,
		})
		if err != nil {
			t.Fatalf("%s: rendering the built-in template failed: %v", typ, err)
		}
		if !strings.Contains(out.Text, "V<url>") && typ != TypeJobFailed && typ != TypeMediaDiskAlert && typ != TypeCredentialChanged && typ != TypeAccountStatus && typ != TypeNewDeviceLogin {
			t.Errorf("%s: rendered text does not contain the url value:\n%s", typ, out.Text)
		}
		if strings.Contains(out.Text, "{{") {
			t.Errorf("%s: rendered text still contains a placeholder:\n%s", typ, out.Text)
		}
	}
}

// TestDefaultTemplateDigestMapsEachCount 钉住摘要那三行的取值映射：
// 「已复习 / 新卡 / 到期」在语言包里都写 `{{.count}}`，但读的是三个不同的变量。
func TestDefaultTemplateDigestMapsEachCount(t *testing.T) {
	typ := TypeStudyDigest
	_, body, _ := DefaultTemplate(typ, VariantDefault, echoTF(), PlaceholderVars(typ))
	for _, want := range []string{
		"mail.digest.reviewed(count={{count}})",
		"mail.digest.new_cards(count={{new_cards}})",
		"mail.digest.due(count={{due}})",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("digest built-in body is missing %q:\n%s", want, body)
		}
	}
}

// TestDefaultTemplateEmailChangeVariant 断言改邮箱确认用的是另一套文案 id（同一个类型）。
func TestDefaultTemplateEmailChangeVariant(t *testing.T) {
	subject, body, ok := DefaultTemplate(TypeEmailVerification, VariantEmailChange, echoTF(), PlaceholderVars(TypeEmailVerification))
	if !ok {
		t.Fatal("email change variant has no built-in template")
	}
	if !strings.Contains(subject, "mail.verify.change_subject") {
		t.Errorf("subject = %q, want the change variant", subject)
	}
	if !strings.Contains(body, "mail.verify.change_body") {
		t.Errorf("body does not use the change variant:\n%s", body)
	}
}

// TestDefaultTemplateUnknownType 断言未知类型返回 ok=false 而不是空模板。
func TestDefaultTemplateUnknownType(t *testing.T) {
	if _, _, ok := DefaultTemplate(Type("nope"), VariantDefault, echoTF(), Vars{}); ok {
		t.Error("unknown type should report no built-in template")
	}
}
