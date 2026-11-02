package notetype

import "testing"

// clozeConfigForTest is one config whose template grades cloze blanks.
func clozeConfigForTest() Config {
	return Config{
		Kind:   KindCloze,
		Fields: []Field{{ID: "text", Name: "text"}},
		Templates: []Template{{
			ID: "cloze", Name: "Cloze", QFormat: "{{cloze:text}}{{type:cloze:text}}",
			AFormat: "{{cloze:text}}", Answer: &AnswerSpec{Control: clozeBlanksControl, Bindings: map[string]string{"text": "text"}},
		}},
	}
}

func TestEnsureMetadataFieldAddsOnlyForClozeBlanks(t *testing.T) {
	cfg := clozeConfigForTest()
	if !EnsureMetadataField(&cfg) {
		t.Fatal("EnsureMetadataField returned false for a cloze_blanks config")
	}
	if len(cfg.Fields) != 2 || cfg.Fields[1].Name != MetadataFieldName || cfg.Fields[1].ID != MetadataFieldName {
		t.Fatalf("fields = %+v, want text and engram_meta", cfg.Fields)
	}
	field := cfg.Fields[1]
	if !field.PlainText || !field.Collapsed || !field.ExcludeFromSearch || !field.PreventDeletion {
		t.Errorf("metadata flags = %+v, want all four set", field)
	}
	if EnsureMetadataField(&cfg) {
		t.Error("second EnsureMetadataField call reported a change")
	}
	if len(cfg.Fields) != 2 {
		t.Errorf("second call changed the field count to %d", len(cfg.Fields))
	}

	other := Config{Kind: KindNormal, Fields: []Field{{ID: "f", Name: "Front"}}, Templates: []Template{{ID: "t", Name: "Card", QFormat: "{{Front}}"}}}
	before := len(other.Fields)
	if EnsureMetadataField(&other) {
		t.Error("EnsureMetadataField changed a config without cloze_blanks")
	}
	if len(other.Fields) != before {
		t.Errorf("config without cloze_blanks gained a field: %+v", other.Fields)
	}
}

func TestEnsureMetadataFieldPreservesExistingField(t *testing.T) {
	cfg := clozeConfigForTest()
	cfg.Fields = append(cfg.Fields, Field{ID: "m1", Name: MetadataFieldName, Description: "kept", Font: "Courier"})
	if !EnsureMetadataField(&cfg) {
		t.Fatal("EnsureMetadataField reported no change while normalising flags")
	}
	var found Field
	for _, field := range cfg.Fields {
		if field.Name == MetadataFieldName {
			found = field
		}
	}
	if found.ID != "m1" || found.Description != "kept" || found.Font != "Courier" {
		t.Errorf("existing field = %+v, want its id, description and font preserved", found)
	}
	if !found.PlainText || !found.Collapsed || !found.ExcludeFromSearch || !found.PreventDeletion {
		t.Errorf("metadata flags = %+v, want all four set", found)
	}
	if len(cfg.Fields) != 2 {
		t.Errorf("fields = %d, want the existing field reused", len(cfg.Fields))
	}
}

func TestEnsureMetadataFieldUsesUniqueID(t *testing.T) {
	cfg := clozeConfigForTest()
	cfg.Fields = append(cfg.Fields, Field{ID: MetadataFieldName, Name: "Taken"})
	if !EnsureMetadataField(&cfg) {
		t.Fatal("EnsureMetadataField reported no change")
	}
	if got := cfg.Fields[len(cfg.Fields)-1].ID; got != MetadataFieldName+"_1" {
		t.Errorf("appended metadata field id = %q, want %q", got, MetadataFieldName+"_1")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("config with the appended metadata field does not validate: %v", err)
	}
}

func TestEnsureMetadataFieldLeavesNonClozeMetadataFlagsAlone(t *testing.T) {
	cfg := Config{Kind: KindNormal, Fields: []Field{{ID: "f", Name: "Front"}, {ID: "m", Name: MetadataFieldName}}, Templates: []Template{{ID: "t", Name: "Card", QFormat: "{{Front}}"}}}
	if EnsureMetadataField(&cfg) {
		t.Error("EnsureMetadataField changed a config without cloze_blanks")
	}
	field := cfg.Fields[1]
	if field.PlainText || field.Collapsed || field.ExcludeFromSearch || field.PreventDeletion {
		t.Errorf("metadata flags changed without cloze_blanks: %+v", field)
	}
}
