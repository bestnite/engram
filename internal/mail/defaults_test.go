package mail

import (
	"sort"
	"strings"
	"testing"
)

// echoTF is an assertable fake translate function: it records every call as `id(k1=v1,k2=v2)`.
//
// It is used instead of the real language packs in order to assert the **value mapping** -- the
// digest's three lines all write `{{.count}}` but read three different numbers, and only pinning down
// "which variable's value did this line get" shows whether the mapping is right.
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

// TestDefaultTemplateIsSavableAsIs is the precondition of the "editor pre-fills the default" feature.
//
// The most common admin action is **save without changing anything** (first take a look at what the
// default looks like). The pre-filled text must pass save validation, otherwise that action returns a
// 400 -- and it should be the safest operation of all.
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
		// Required variables must appear in the body, written in template form (`{{name}}`) -- it is
		// filled into the editor verbatim.
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

// TestDefaultTemplateRendersWithRealValues asserts that the pre-filled default is not merely
// "savable" but really renders: after swapping the placeholders for real values and running one pass
// through the render pipeline, the values must appear in the body.
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

// TestDefaultTemplateDigestMapsEachCount pins down the value mapping of the digest's three lines:
// "reviewed / new cards / due" all write `{{.count}}` in the language pack but read three different
// variables.
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

// TestDefaultTemplateEmailChangeVariant asserts that email change confirmation uses the other set of
// copy ids (within the same type).
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

// TestDefaultTemplateUnknownType asserts that an unknown type returns ok=false rather than an empty
// template.
func TestDefaultTemplateUnknownType(t *testing.T) {
	if _, _, ok := DefaultTemplate(Type("nope"), VariantDefault, echoTF(), Vars{}); ok {
		t.Error("unknown type should report no built-in template")
	}
}
