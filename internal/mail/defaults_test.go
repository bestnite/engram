package mail

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/i18n"
	"golang.org/x/text/language"
)

// echoTF is an assertable fake translate function: it records every call as `id(k1=v1,k2=v2)`.
//
// It is used instead of the real language packs in order to assert the **value mapping** -- the
// digest's lines all write `{{.count}}` but read different numbers, and only pinning down "which
// variable's value did this line get" shows whether the mapping is right.
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
// default looks like). The pre-filled text must pass save validation, otherwise that action returns
// a 400 -- and it should be the safest operation of all.
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
// "savable" but really renders: the HTML body goes through the same pipeline, and the real
// values substituted afterwards show up in the HTML and in the derived plain text.
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
		if strings.Contains(out.Text, "{{") || strings.Contains(out.HTML, "{{") {
			t.Errorf("%s: rendered output still contains a placeholder", typ)
		}
		usesURL := false
		for _, name := range Placeholders(body) {
			if name == "url" {
				usesURL = true
			}
		}
		if usesURL && !strings.Contains(out.Text, "V<url>") {
			t.Errorf("%s: rendered text does not contain the url value:\n%s", typ, out.Text)
		}
	}
}

// TestDefaultTemplateDigestMapsEachCount pins down the value mapping of the digest's statistics list:
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

// ctaTypes are the types whose built-in body carries a call to action. They must produce a button:
// without one the mail says "use the button below" and shows nothing to press.
var ctaTypes = map[Type]bool{
	TypePasswordReset:         true,
	TypeEmailVerification:     true,
	TypeDeckShared:            true,
	TypeDeckPermissionChanged: true,
	TypeInvite:                true,
	TypeReviewReminder:        true,
	TypeStudyDigest:           true,
	TypeOptimizeDone:          true,
}

// mailKeyPattern matches an untranslated language pack id. A missing key makes the localizer return
// the id itself, so finding one in the output means the built-in body asked for copy that does not
// exist. It cannot match ordinary words: `\b` before `mail` rejects "email.".
var mailKeyPattern = regexp.MustCompile(`\bmail\.[a-z0-9_.]+`)

// sampleVars returns one real value per variable of the type. Values are deliberately plain: this test
// is about the copy and the layout, not about escaping (the escaping cases live in template_test.go).
func sampleVars(t Type) Vars {
	out := Vars{}
	for name := range PlaceholderVars(t) {
		switch name {
		case "site":
			out[name] = "Example"
		case "url":
			out[name] = "https://example.com/a/b"
		case "unsubscribe_url":
			out[name] = "https://example.com/unsubscribe"
		case "expires":
			out[name] = "2026-01-01 00:00 UTC"
		case "role":
			out[name] = "readable"
		default:
			out[name] = "sample-" + name
		}
	}
	return out
}

// TestDefaultBodiesRenderWithRealCatalogs is the acceptance test for the built-in bodies: every type
// is built with a real localizer in both languages and rendered through the production path, so a
// missing language pack key or an unrenderable built-in body fails here rather than in a mailbox.
//
// It also asserts the two invariants of the built-in default: the mail has a title (h2) and a button
// for the types that offer an action, and saving the pre-filled default unchanged produces exactly the
// mail that is sent when no template is stored.
func TestDefaultBodiesRenderWithRealCatalogs(t *testing.T) {
	translator, err := i18n.New()
	if err != nil {
		t.Fatalf("load language packs: %v", err)
	}
	for _, code := range translator.SupportedCodes() {
		loc := translator.Localizer(language.Make(code))
		for _, def := range Catalog() {
			typ := def.Type
			// Every variant is rendered too: an alternate wording has its own language pack keys, and a
			// missing one would otherwise only show up in the one flow that sends it.
			variants := []string{VariantDefault}
			if typ == TypeEmailVerification {
				variants = append(variants, VariantEmailChange)
			}
			for _, variant := range variants {
				t.Run(code+"/"+string(typ)+"/"+variant, func(t *testing.T) {
					vars := sampleVars(typ)
					// The sender builds its built-in subject with the real values; the admin editor gets the
					// same body but a subject written with placeholders.
					fallbackSubject, fallbackBody, ok := DefaultTemplate(typ, variant, loc.Tf, vars)
					if !ok {
						t.Fatal("no built-in template")
					}
					editorSubject, editorBody, _ := DefaultTemplate(typ, variant, loc.Tf, PlaceholderVars(typ))

					// No custom template: the built-in HTML body is the fallback body.
					sent := renderOne(t, RenderInput{
						Type:            typ,
						Site:            "Example",
						Lang:            code,
						Vars:            vars,
						FallbackSubject: fallbackSubject,
						FallbackBodyMD:  fallbackBody,
						FooterNote:      loc.T("mail.footer.automated"),
					})
					// The editor pre-filled with the default and saved unchanged.
					saved := renderOne(t, RenderInput{
						Type:       typ,
						Site:       "Example",
						Lang:       code,
						Vars:       vars,
						Subject:    editorSubject,
						BodyMD:     editorBody,
						FooterNote: loc.T("mail.footer.automated"),
					})

					if sent.HTML != saved.HTML || sent.Text != saved.Text || sent.Subject != saved.Subject {
						t.Errorf("saving the default unchanged changed the mail:\nsent HTML:\n%s\nsaved HTML:\n%s", sent.HTML, saved.HTML)
					}
					for name, out := range map[string]string{"subject": sent.Subject, "html": sent.HTML, "text": sent.Text} {
						if m := mailKeyPattern.FindString(out); m != "" {
							t.Errorf("%s contains the untranslated message id %q", name, m)
						}
						if strings.Contains(out, "{{") {
							t.Errorf("%s contains a placeholder:\n%s", name, out)
						}
					}
					if !strings.Contains(sent.HTML, "<h2") {
						t.Errorf("HTML has no title heading:\n%s", sent.HTML)
					}
					if ctaTypes[typ] && !strings.Contains(sent.HTML, buttonStyle) {
						t.Errorf("CTA mail has no button:\n%s", sent.HTML)
					}
					if !ctaTypes[typ] && strings.Contains(sent.HTML, buttonStyle) {
						t.Errorf("mail without a CTA rendered a button:\n%s", sent.HTML)
					}
				})
			}
		}
	}
}

// TestDefaultBodiesUsePlaceholdersNotValues is the injection guard on the built-in definitions: the
// body builder must only ever emit `{{name}}`, so a real value passed to DefaultTemplate cannot appear
// inside the returned HTML template.
func TestDefaultBodiesUsePlaceholdersNotValues(t *testing.T) {
	for _, def := range Catalog() {
		typ := def.Type
		real := Vars{}
		for name := range PlaceholderVars(typ) {
			real[name] = "REAL-" + name
		}
		_, body, _ := DefaultTemplate(typ, VariantDefault, echoTF(), real)
		if strings.Contains(body, "REAL-") {
			t.Errorf("%s: built-in body embeds a real variable value:\n%s", typ, body)
		}
		if !strings.Contains(body, "<h2 ") {
			t.Errorf("%s: built-in body has no title heading:\n%s", typ, body)
		}
	}
}
