package i18n

import (
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
)

// TestCardTypeDescriptionLabelsCoverCatalogs 断言每个题型自描述的 LabelKey 在中英两份
// 语言包里都有对应键：自描述是前端渲染的契约，缺键会让题型名在页面上退化成键名本身。
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
