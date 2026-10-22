package i18n

import (
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// TestCardTypeDescriptionLabelsCoverCatalogs asserts that the LabelKey of every card type
// description exists in both the Chinese and the English language pack: the description is the
// contract the frontend renders against, so a missing key degrades the card type name on the
// page into the key itself.
func TestCardTypeDescriptionLabelsCoverCatalogs(t *testing.T) {
	catalogs := map[string]map[string]string{
		"zh-CN": catalogKeys(t, "locales/zh-CN.yaml"),
		"en":    catalogKeys(t, "locales/en.yaml"),
	}
	descs := cardtype.Descriptions()
	if len(descs) == 0 {
		t.Fatal("cardtype.Descriptions() is empty; the registry was not loaded")
	}
	for _, d := range descs {
		for code, catalog := range catalogs {
			if strings.TrimSpace(catalog[d.LabelKey]) == "" {
				t.Errorf("locale %s is missing the label key %q for card type %q", code, d.LabelKey, d.Kind)
			}
		}
	}
}
