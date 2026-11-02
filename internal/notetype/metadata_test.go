package notetype

import (
	"errors"
	"reflect"
	"testing"
)

func TestMetadataTemplateReferencesRejected(t *testing.T) {
	for _, format := range []string{"{{engram_meta}}", "{{text:engram_meta}}", "{{#engram_meta}}x{{/engram_meta}}", "{{^engram_meta}}x{{/engram_meta}}", "{{=<% %>=}}<%engram_meta%>"} {
		for _, side := range []string{"front", "back", "browser-front", "browser-back"} {
			cfg := Config{Kind: KindNormal, Fields: []Field{{ID: "f", Name: "Front"}, {ID: "m", Name: MetadataFieldName}}, Templates: []Template{{ID: "t", Name: "Card", QFormat: "{{Front}}"}}}
			switch side {
			case "front":
				cfg.Templates[0].QFormat = format
			case "back":
				cfg.Templates[0].AFormat = format
			case "browser-front":
				cfg.Templates[0].BQFormat = format
			case "browser-back":
				cfg.Templates[0].BAFormat = format
			}
			var ve *ValidationError
			if err := cfg.Validate(); !errors.As(err, &ve) || ve.Rule != RuleMetadataTemplate {
				t.Errorf("%s %s: %v", side, format, err)
			}
		}
	}
}

func TestClozeGenerationIgnoresOnlyMetadata(t *testing.T) {
	cfg := Config{Kind: KindCloze, Fields: []Field{{ID: "a", Name: "Text"}, {ID: "b", Name: MetadataFieldName}, {ID: "c", Name: "engram_notes"}}}
	for _, metadata := range []string{`{"text":"{{c9::x}}"}`, `broken {{c9::x}}`} {
		got, err := GenerateCards(cfg, RenderData{Fields: map[string]string{"a": "{{c1::x}}", "b": metadata, "c": "{{c7::y}}"}})
		want := []GeneratedCard{{Template: "cloze:1", Ordinal: 0}, {Template: "cloze:7", Ordinal: 6}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("cards = %v, %v", got, err)
		}
	}
}
