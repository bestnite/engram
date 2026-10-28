package i18n

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

func newTestTranslator(t *testing.T) *Translator {
	t.Helper()
	tr, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return tr
}

// TestPickResolutionOrder covers the language resolution order: ?lang > user setting >
// Accept-Language > default. This is one of the acceptance points; assert it case by case so
// the order cannot be broken.
func TestPickResolutionOrder(t *testing.T) {
	tr := newTestTranslator(t)
	cases := []struct {
		name      string
		requested string
		user      string
		accept    string
		want      string
	}{
		{"user setting beats accept-language", "", "en", "zh-CN,zh;q=0.9", "en"},
		{"user setting beats accept-language reversed", "", "zh-CN", "en-US,en;q=0.9", "zh-CN"},
		{"explicit request beats user setting", "en", "zh-CN", "zh-CN", "en"},
		{"unsupported user setting falls through to header", "", "de", "en-GB,en;q=0.9", "en"},
		{"header region maps to base language", "", "", "en-US,en;q=0.9", "en"},
		{"header order decides between two supported languages", "", "", "fr-FR,en;q=0.9,zh;q=0.1", "en"},
		{"header quality decides between two supported languages", "", "", "en;q=0.2,zh-CN;q=0.9", "zh-CN"},
		{"nothing supported falls back to default", "", "", "fr-FR,de;q=0.8", DefaultLocaleCode},
		{"empty inputs fall back to default", "", "", "", DefaultLocaleCode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tr.Pick(tc.requested, tc.user, tc.accept)
			want, err := language.Parse(tc.want)
			if err != nil {
				t.Fatalf("parse want %q: %v", tc.want, err)
			}
			if got.String() != want.String() {
				t.Errorf("Pick(%q, %q, %q) = %q, want %q", tc.requested, tc.user, tc.accept, got, want)
			}
		})
	}
}

// TestCatalogsHaveIdenticalKeys is the language pack completeness check: fail this test if any
// language is missing a key.
func TestCatalogsHaveIdenticalKeys(t *testing.T) {
	zh := catalogKeys(t, "locales/zh-CN.yaml")
	en := catalogKeys(t, "locales/en.yaml")
	if len(zh) == 0 {
		t.Fatal("zh-CN.yaml contains no messages")
	}
	for id := range zh {
		if _, ok := en[id]; !ok {
			t.Errorf("en.yaml is missing key %q that zh-CN.yaml defines", id)
		}
	}
	for id := range en {
		if _, ok := zh[id]; !ok {
			t.Errorf("zh-CN.yaml is missing key %q that en.yaml defines", id)
		}
	}
}

// TestNewFromFSRejectsCatalogMissingAKey proves the completeness check is enforced at load time:
// construction fails when a language is missing a key, instead of waiting until untranslated text
// shows up on the page.
func TestNewFromFSRejectsCatalogMissingAKey(t *testing.T) {
	fsys := fstest.MapFS{
		"locales/zh-CN.yaml": &fstest.MapFile{Data: []byte("- id: app.name\n  translation: \"Engram\"\n- id: nav.today\n  translation: \"今日\"\n")},
		"locales/en.yaml":    &fstest.MapFile{Data: []byte("- id: app.name\n  translation: \"Engram\"\n")},
	}
	if _, err := NewFromFS(fsys, "locales"); err == nil {
		t.Fatal("NewFromFS() accepted a catalog missing a key, want an error")
	} else if !strings.Contains(err.Error(), "nav.today") {
		t.Errorf("error %v does not name the missing message id", err)
	}
}

func TestNewFromFSRejectsUnknownExtraKey(t *testing.T) {
	fsys := fstest.MapFS{
		"locales/zh-CN.yaml": &fstest.MapFile{Data: []byte("- id: app.name\n  translation: \"Engram\"\n")},
		"locales/en.yaml":    &fstest.MapFile{Data: []byte("- id: app.name\n  translation: \"Engram\"\n- id: only.in.en\n  translation: \"extra\"\n")},
	}
	if _, err := NewFromFS(fsys, "locales"); err == nil {
		t.Fatal("NewFromFS() accepted a key only present in one catalog, want an error")
	}
}

func TestLocalizeTranslatesAndFallsBackToID(t *testing.T) {
	tr := newTestTranslator(t)
	en := tr.Localizer(tr.Pick("", "en", ""))
	if got, want := en.T("cardtype.basic"), "Basic"; got != want {
		t.Errorf("en cardtype.basic = %q, want %q", got, want)
	}
	zh := tr.Localizer(tr.Pick("", "zh-CN", ""))
	if got, want := zh.T("cardtype.basic"), "问答"; got != want {
		t.Errorf("zh-CN cardtype.basic = %q, want %q", got, want)
	}
	// A missing key falls back to the key itself, so the problem is visible on the page rather than
	// disappearing into thin air.
	if got := en.T("does.not.exist"); got != "does.not.exist" {
		t.Errorf("missing key = %q, want the key itself", got)
	}
}

func TestLocalizerMapsTagToSupportedCode(t *testing.T) {
	tr := newTestTranslator(t)
	cases := map[string]string{"en-US": "en", "zh-TW": "zh-CN", "en": "en", "zh-CN": "zh-CN", "de": DefaultLocaleCode}
	for tag, want := range cases {
		parsed, err := language.Parse(tag)
		if err != nil {
			t.Fatalf("parse %q: %v", tag, err)
		}
		if got := tr.Localizer(parsed).Locale(); got != want {
			t.Errorf("Localizer(%q).Locale() = %q, want %q", tag, got, want)
		}
	}
}

func TestLocalizerContextRoundTrip(t *testing.T) {
	tr := newTestTranslator(t)
	if FromContext(context.Background()) != nil {
		t.Fatal("FromContext() returned a localizer for an empty context")
	}
	loc := tr.Localizer(tr.Pick("", "en", ""))
	ctx := WithLocalizer(context.Background(), loc)
	if got := FromContext(ctx); got == nil || got.Locale() != "en" {
		t.Fatalf("FromContext() = %v, want the en localizer", got)
	}
}

func TestSupportedCodesAreOrderedAndComplete(t *testing.T) {
	tr := newTestTranslator(t)
	want := []string{"zh-CN", "en"}
	got := tr.SupportedCodes()
	if len(got) != len(want) {
		t.Fatalf("SupportedCodes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SupportedCodes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// catalogKeys parses the language packs independently (without reusing the loader), as a second
// source of evidence for the completeness check.
func catalogKeys(t *testing.T, name string) map[string]string {
	t.Helper()
	data, err := localesFS.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var msgs []struct {
		ID          string `yaml:"id"`
		Translation string `yaml:"translation"`
	}
	if err := yaml.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	out := make(map[string]string, len(msgs))
	for _, m := range msgs {
		if strings.TrimSpace(m.Translation) == "" {
			t.Errorf("%s: key %q has an empty translation", name, m.ID)
		}
		out[m.ID] = m.Translation
	}
	return out
}
