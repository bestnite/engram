package i18n

import (
	"strings"
	"testing"

	"example.com/flashcard/internal/cardtype"
)

// documentedKinds 是 DESIGN.md §6.2 冻结的十个内置题型。
//
// 注册表当前可能尚未全部注册（short_answer 属未来规划 §14 的口子），但语言包仍应
// 预先覆盖，这样将来题型一注册就立刻有显示名，不需要同时补语言包。
var documentedKinds = []string{
	"basic", "basic_both", "cloze", "list",
	"typed", "numeric", "choice_single", "choice_multi", "true_false", "short_answer",
}

// gradedFieldKeys 是作答类与简答题的字段名（DESIGN.md §6.2 字段名冻结表）。
// 编辑页/新建页对每个字段都必须渲染出标签而不是原始 key。
var gradedFieldKeys = []string{
	"question", "answer", "accept", "options", "answers", "statement",
	"value", "unit", "tolerance_absolute", "tolerance_relative",
	"ignore_case", "ignore_whitespace", "regex", "reference",
}

// TestCardTypeLabelsCoverRegistry 是 M2-10 的验收测试：枚举 Registry.Kinds()，
// 任一题型在中英任一套语言包里缺显示名（cardtype.<kind>）就失败。
// 同时校验文档冻结的十个题型与全部作答类字段名都有标签，避免目录被悄悄裁剪。
func TestCardTypeLabelsCoverRegistry(t *testing.T) {
	zh := catalogKeys(t, "locales/zh-CN.yaml")
	en := catalogKeys(t, "locales/en.yaml")
	catalogs := map[string]map[string]string{"zh-CN": zh, "en": en}

	// 1) 注册表里每一个题型都必须有 cardtype.<kind> 标签。
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

	// 2) 文档冻结的十个题型与字段名也要有标签（含尚未注册的 short_answer）。
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

// TestCatalogLabelsAreTranslatedNotKeyIDs 断言题型/字段标签是真正的译文而不是键名本身，
// 防止把 key 当译文复制进语言包这种"看起来有标签"的假象。
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
