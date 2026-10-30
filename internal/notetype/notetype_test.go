package notetype

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

func validNormalConfig() Config {
	return Config{
		Kind: KindNormal,
		Fields: []Field{
			{ID: "f0", Name: "Front"},
			{ID: "f1", Name: "Back"},
		},
		Templates: []Template{
			{ID: "t0", Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{FrontSide}}\n\n<hr id=answer>\n\n{{Back}}"},
		},
		SortFieldID: "f0",
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  func() Config
		rule    Rule
		wantErr string
	}{
		{
			name:   "valid normal note type",
			config: validNormalConfig,
		},
		{
			name: "valid cloze note type",
			config: func() Config {
				return Config{
					Kind:      KindCloze,
					Fields:    []Field{{ID: "f0", Name: "Text"}},
					Templates: []Template{{ID: "t0", Name: "Cloze", QFormat: "{{cloze:Text}}"}},
				}
			},
		},
		{
			name: "valid filtered and conditional references",
			config: func() Config {
				config := validNormalConfig()
				config.Templates[0].QFormat = "{{#Front}}{{text:Back}}{{/Front}}\n\n{{tts en_US voices=A,B speed=1.2:Back}}\n\n{{type:Back}}"
				return config
			},
		},
		{
			name: "unknown kind",
			config: func() Config {
				config := validNormalConfig()
				config.Kind = Kind("weird")
				return config
			},
			rule:    RuleInvalidKind,
			wantErr: `notetype: invalid_kind: kind "weird" is not "normal" or "cloze"`,
		},
		{
			name: "duplicate field name",
			config: func() Config {
				config := validNormalConfig()
				config.Fields[1].Name = "Front"
				return config
			},
			rule:    RuleDuplicateFieldName,
			wantErr: `notetype: duplicate_field_name: duplicate field name "Front"`,
		},
		{
			name: "duplicate template name",
			config: func() Config {
				config := validNormalConfig()
				config.Templates = append(config.Templates, Template{ID: "t1", Name: "Card 1", QFormat: "{{Back}}"})
				return config
			},
			rule:    RuleDuplicateTemplateName,
			wantErr: `notetype: duplicate_template_name: duplicate template name "Card 1"`,
		},
		{
			name: "empty field name",
			config: func() Config {
				config := validNormalConfig()
				config.Fields[0].Name = "   "
				return config
			},
			rule:    RuleEmptyFieldName,
			wantErr: "notetype: empty_field_name: field 1 has an empty name",
		},
		{
			name: "empty template name",
			config: func() Config {
				config := validNormalConfig()
				config.Templates[0].Name = "\t"
				return config
			},
			rule:    RuleEmptyTemplateName,
			wantErr: "notetype: empty_template_name: template 1 has an empty name",
		},
		{
			name: "cloze note type with two templates",
			config: func() Config {
				config := validNormalConfig()
				config.Kind = KindCloze
				config.Templates = append(config.Templates, Template{ID: "t1", Name: "Card 2", QFormat: "{{Back}}"})
				return config
			},
			rule:    RuleClozeTemplateCount,
			wantErr: "notetype: cloze_template_count: cloze note type has 2 templates; at most one is allowed",
		},
		{
			name: "unknown field in the front",
			config: func() Config {
				config := validNormalConfig()
				config.Templates[0].QFormat = "{{Front}} {{Missing}}"
				return config
			},
			rule:    RuleUnknownField,
			wantErr: `notetype: unknown_field: template "Card 1" front references unknown field "Missing"`,
		},
		{
			name: "unknown field behind a filter chain",
			config: func() Config {
				config := validNormalConfig()
				config.Templates[0].QFormat = "{{text:cloze:Missing}}"
				return config
			},
			rule:    RuleUnknownField,
			wantErr: `notetype: unknown_field: template "Card 1" front references unknown field "Missing"`,
		},
		{
			name: "duplicate field id",
			config: func() Config {
				config := validNormalConfig()
				config.Fields[1].ID = "f0"
				return config
			},
			rule:    RuleDuplicateFieldID,
			wantErr: `notetype: duplicate_field_id: field id "f0" is used by more than one field`,
		},
		{
			name: "duplicate template id",
			config: func() Config {
				config := validNormalConfig()
				config.Templates = append(config.Templates, Template{ID: "t0", Name: "Card 2", QFormat: "{{Back}}"})
				return config
			},
			rule:    RuleDuplicateTemplateID,
			wantErr: `notetype: duplicate_template_id: template id "t0" is used by more than one template`,
		},
		{
			name: "empty field id",
			config: func() Config {
				config := validNormalConfig()
				config.Fields[1].ID = ""
				return config
			},
			rule:    RuleEmptyFieldID,
			wantErr: "notetype: empty_field_id: field 2 has an empty id",
		},
		{
			name: "empty template id",
			config: func() Config {
				config := validNormalConfig()
				config.Templates[0].ID = ""
				return config
			},
			rule:    RuleEmptyTemplateID,
			wantErr: "notetype: empty_template_id: template 1 has an empty id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config().Validate()
			if test.rule == "" {
				if err != nil {
					t.Fatalf("Validate() returned %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() returned nil, want %s", test.rule)
			}
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Validate() returned %T, want *ValidationError", err)
			}
			if validationErr.Rule != test.rule {
				t.Errorf("Rule = %q, want %q", validationErr.Rule, test.rule)
			}
			if err.Error() != test.wantErr {
				t.Errorf("Error() = %q, want %q", err.Error(), test.wantErr)
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "Basic", value: "Basic"},
		{name: "surrounding whitespace", value: "  Basic  "},
		{name: "empty", value: "", wantErr: "notetype: empty_note_type_name: note type name is empty"},
		{name: "whitespace only", value: " \t\n", wantErr: "notetype: empty_note_type_name: note type name is empty"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateName(test.value)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateName(%q) returned %v, want nil", test.value, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateName(%q) returned nil, want an error", test.value)
			}
			if err.Error() != test.wantErr {
				t.Errorf("Error() = %q, want %q", err.Error(), test.wantErr)
			}
		})
	}
}

func TestReferencedFields(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     []string
	}{
		{name: "replacement", template: "{{Front}}", want: []string{"Front"}},
		{name: "conditional", template: "{{#Front}}x{{/Front}}", want: []string{"Front"}},
		{name: "inverted conditional", template: "{{^Back}}x{{/Back}}", want: []string{"Back"}},
		{name: "filter", template: "{{text:f1}}", want: []string{"f1"}},
		{name: "filter chain", template: "{{text:cloze:Text}}", want: []string{"Text"}},
		{name: "tts filter", template: "{{tts en_US voices=A,B speed=1.2:f2}}", want: []string{"f2"}},
		{name: "type filter", template: "{{type:Back}}", want: []string{"Back"}},
		{
			name:     "special fields",
			template: "{{FrontSide}}{{Tags}}{{Type}}{{Deck}}{{Subdeck}}{{Card}}{{CardFlag}}{{CardID}}",
			want:     nil,
		},
		{name: "cloze conditional", template: "{{#c1}}x{{/c1}}{{^c12}}y{{/c12}}", want: nil},
		{name: "cloze numbers list", template: "{{#c1,2}}x{{/c1,2}}", want: nil},
		{name: "inline cloze", template: "{{c1::answer}}", want: nil},
		{name: "delimiter declaration", template: "{{=<% %>=}}<%Front%>", want: nil},
		{name: "html comment", template: "<!-- {{Missing}} -->{{Front}}", want: []string{"Front"}},
		{name: "empty field name", template: "{{text:}}", want: nil},
		{name: "unique in order", template: "{{Back}}{{Front}}{{Back}}", want: []string{"Back", "Front"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ReferencedFields(test.template)
			if !slices.Equal(got, test.want) {
				t.Errorf("ReferencedFields(%q) = %v, want %v", test.template, got, test.want)
			}
		})
	}
}

func TestConfigJSONKeys(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  []string
	}{
		{
			name:  "config",
			value: Config{},
			want: []string{
				"kind", "fields", "templates", "css", "sort_field_id",
				"latex_pre", "latex_post", "latex_svg", "original_stock_kind", "anki_origin",
			},
		},
		{
			name:  "field",
			value: Field{},
			want: []string{
				"id", "name", "sticky", "rtl", "font", "font_size", "description",
				"plain_text", "collapsed", "exclude_from_search", "tag", "prevent_deletion",
			},
		},
		{
			name:  "template",
			value: Template{},
			want: []string{
				"id", "name", "qfmt", "afmt", "bqfmt", "bafmt",
				"browser_font", "browser_font_size", "answer",
			},
		},
		{
			name:  "answer spec",
			value: AnswerSpec{},
			want:  []string{"control", "bindings"},
		},
		{
			name:  "anki origin",
			value: AnkiOrigin{},
			want:  []string{"notetype_id", "mtime"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.value)
			if err != nil {
				t.Fatalf("json.Marshal() returned %v", err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &object); err != nil {
				t.Fatalf("json.Unmarshal() returned %v", err)
			}
			keys := make([]string, 0, len(object))
			for key := range object {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			slices.Sort(test.want)
			if !slices.Equal(keys, test.want) {
				t.Errorf("JSON keys = %v, want %v", keys, test.want)
			}
		})
	}
}
