package i18n

import (
	"strings"
	"testing"
)

// documentedKinds is the frozen set of ten built-in card types.
//
// The registry may not have all of them registered yet (short_answer is a planned gap), but the
// language packs should cover them in advance, so a card type has a display name the moment it is
// registered, without having to update the packs at the same time.
var documentedKinds = []string{
	"basic", "basic_both", "cloze", "list",
	"typed", "numeric", "choice_single", "choice_multi", "true_false", "short_answer",
}

// TestCardTypeLabelsCoverRegistry checks that every frozen card kind has a display name
// (cardtype.<kind>) in either language pack. The kinds are a literal list because the card kind
// registry belongs to the server's payload layer, which this leaf package must not import.
func TestCardTypeLabelsCoverRegistry(t *testing.T) {
	zh := catalogKeys(t, "locales/zh-CN.yaml")
	en := catalogKeys(t, "locales/en.yaml")
	catalogs := map[string]map[string]string{"zh-CN": zh, "en": en}

	// Every frozen card kind must have a label in both packs, including short_answer.
	for _, kind := range documentedKinds {
		key := "cardtype." + kind
		for code, catalog := range catalogs {
			if strings.TrimSpace(catalog[key]) == "" {
				t.Errorf("locale %s is missing the documented label %q", code, key)
			}
		}
	}
}

// TestCatalogLabelsAreTranslatedNotKeyIDs asserts that card type labels are real translations
// rather than the key names themselves, guarding against the illusion of "having a label" created by
// copying the key into the catalog as its translation.
func TestCatalogLabelsAreTranslatedNotKeyIDs(t *testing.T) {
	for _, name := range []string{"locales/zh-CN.yaml", "locales/en.yaml"} {
		catalog := catalogKeys(t, name)
		prefixes := []string{"cardtype.", "notes.create."}
		for id, translation := range catalog {
			for _, prefix := range prefixes {
				if strings.HasPrefix(id, prefix) && translation == id {
					t.Errorf("%s: message %q has its own id as the translation", name, id)
				}
			}
		}
	}
}
