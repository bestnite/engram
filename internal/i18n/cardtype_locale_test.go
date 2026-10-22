package i18n

import (
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
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

// gradedFieldKeys is the field names of graded and short-answer card types (the frozen field name table).
// The edit/new page must render a label for every one of these fields instead of the raw key.
var gradedFieldKeys = []string{
	"question", "answer", "accept", "options", "answers", "statement",
	"value", "unit", "tolerance_absolute", "tolerance_relative",
	"ignore_case", "ignore_whitespace", "regex", "reference",
}

// TestCardTypeLabelsCoverRegistry is the acceptance test: enumerate Registry.Kinds(), and fail if any
// card type lacks a display name (cardtype.<kind>) in either language pack.
// It also checks that the ten card types frozen in the docs and every graded field name have labels,
// so the catalog cannot be quietly trimmed.
func TestCardTypeLabelsCoverRegistry(t *testing.T) {
	zh := catalogKeys(t, "locales/zh-CN.yaml")
	en := catalogKeys(t, "locales/en.yaml")
	catalogs := map[string]map[string]string{"zh-CN": zh, "en": en}

	// 1) Every card type in the registry must have a cardtype.<kind> label.
	kinds := cardtype.Kinds()
	if len(kinds) == 0 {
		t.Fatal("cardtype.Kinds() is empty; the registry was not loaded")
	}
	for _, kind := range kinds {
		key := "cardtype." + kind
		for code, catalog := range catalogs {
			if strings.TrimSpace(catalog[key]) == "" {
				t.Errorf("locale %s is missing the label %q for registered card type %q", code, key, kind)
			}
		}
	}

	// 2) The ten card types frozen in the docs and the field names must have labels too
	// (including short_answer, which is not registered yet).
	for _, kind := range documentedKinds {
		key := "cardtype." + kind
		for code, catalog := range catalogs {
			if strings.TrimSpace(catalog[key]) == "" {
				t.Errorf("locale %s is missing the documented label %q", code, key)
			}
		}
	}
	for _, field := range gradedFieldKeys {
		key := "note.field." + field
		for code, catalog := range catalogs {
			if strings.TrimSpace(catalog[key]) == "" {
				t.Errorf("locale %s is missing the field label %q", code, key)
			}
		}
	}
}

// TestCatalogLabelsAreTranslatedNotKeyIDs asserts that card type/field labels are real translations
// rather than the key names themselves, guarding against the illusion of "having a label" created by
// copying the key into the catalog as its translation.
func TestCatalogLabelsAreTranslatedNotKeyIDs(t *testing.T) {
	for _, name := range []string{"locales/zh-CN.yaml", "locales/en.yaml"} {
		catalog := catalogKeys(t, name)
		prefixes := []string{"cardtype.", "note.field.", "notes.create."}
		for id, translation := range catalog {
			for _, prefix := range prefixes {
				if strings.HasPrefix(id, prefix) && translation == id {
					t.Errorf("%s: message %q has its own id as the translation", name, id)
				}
			}
		}
	}
}
