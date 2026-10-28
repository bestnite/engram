package web

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"git.nite07.com/nite/engram/internal/mail"
)

// TestMailPrefsCatalogKeysExistInBothCatalogs asserts every mail type label key (def.LabelKey) exists in both the Chinese and English sets:
// the admin mail templates API hands these keys out, so a missing one would be a dangling identifier.
func TestMailPrefsCatalogKeysExistInBothCatalogs(t *testing.T) {
	zh := catalogKeys(t, filepath.Join("..", "i18n", "locales", "zh-CN.yaml"))
	en := catalogKeys(t, filepath.Join("..", "i18n", "locales", "en.yaml"))

	for _, def := range mail.Catalog() {
		key := def.LabelKey
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
