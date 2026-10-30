package i18n

import (
	"strings"
	"testing"
)

// TestCardTypeDescriptionLabelsCoverCatalogs asserts that every card kind's label key exists in both
// the Chinese and the English language pack: the description is the contract the frontend renders
// against, so a missing key degrades the card type name on the page into the key itself.
//
// The kinds are listed literally because the description catalog belongs to the server's payload
// layer, which this leaf package must not import.
func TestCardTypeDescriptionLabelsCoverCatalogs(t *testing.T) {
	catalogs := map[string]map[string]string{
		"zh-CN": catalogKeys(t, "locales/zh-CN.yaml"),
		"en":    catalogKeys(t, "locales/en.yaml"),
	}
	for _, kind := range documentedKinds {
		labelKey := "cardtype." + kind
		for code, catalog := range catalogs {
			if strings.TrimSpace(catalog[labelKey]) == "" {
				t.Errorf("locale %s is missing the label key %q for card type %q", code, labelKey, kind)
			}
		}
	}
}
