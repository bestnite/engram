package web

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"git.nite07.com/nite/engram/internal/mail"
)

// TestMailPrefsCatalogKeysExistInBothCatalogs asserts every language-pack key referenced by the catalog and the page exists in both the Chinese and English sets:
// catalog labels are built dynamically (def.LabelKey), so the static key scanner cannot see them and this is the fallback check.
func TestMailPrefsCatalogKeysExistInBothCatalogs(t *testing.T) {
	zh := catalogKeys(t, filepath.Join("..", "i18n", "locales", "zh-CN.yaml"))
	en := catalogKeys(t, filepath.Join("..", "i18n", "locales", "en.yaml"))

	keys := []string{
		"settings.mail_prefs.heading",
		"settings.mail_prefs.hint",
		"settings.mail_prefs.link",
		"mail.prefs.title",
		"mail.prefs.heading",
		"mail.prefs.intro",
		"mail.prefs.submit",
		"mail.prefs.saved",
		"mail.prefs.locked",
		"mail.prefs.reminder_time.heading",
		"mail.prefs.reminder_time.label",
		"mail.prefs.reminder_time.hint",
		"mail.prefs.reminder_time.default",
	}
	for _, class := range mail.ClassOrder() {
		// Assert only the title keys: the group notes (.note) were removed under "keep the copy lean", so those lines are no longer on the page.
		keys = append(keys, "mail.prefs.class."+string(class)+".heading")
	}
	for _, def := range mail.Catalog() {
		keys = append(keys, def.LabelKey)
	}
	for _, key := range keys {
		if !zh[key] {
			t.Errorf("zh-CN.yaml is missing the mail preference key %q", key)
		}
		if !en[key] {
			t.Errorf("en.yaml is missing the mail preference key %q", key)
		}
	}
}

// catalogKeys reads the set of message ids from one YAML language pack.
func catalogKeys(t *testing.T, path string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read catalog %s: %v", path, err)
	}
	var msgs []struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("parse catalog %s: %v", path, err)
	}
	out := make(map[string]bool, len(msgs))
	for _, m := range msgs {
		if m.ID != "" {
			out[m.ID] = true
		}
	}
	if len(out) == 0 {
		t.Fatalf("catalog %s parsed to zero messages", path)
	}
	return out
}
