package notetype_test

import (
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/notetype"
)

// TestEngramStockConfigs pins the ten engram stock note types: today's kinds, today's field names as field ids, the
// table's front templates and answer controls, and the bindings every declared role needs.
func TestEngramStockConfigs(t *testing.T) {
	type want struct {
		templates []string
		front     string
		control   string
		bindings  map[string]string
		fields    []string
	}
	cases := map[string]want{
		"basic": {
			templates: []string{"forward"},
			front:     "{{front}}",
			fields:    []string{"front", "back"},
		},
		"basic_both": {
			templates: []string{"forward", "reverse"},
			front:     "{{front}}",
			fields:    []string{"front", "back"},
		},
		"cloze": {
			templates: []string{"cloze"},
			front:     "{{cloze:text}}{{type:cloze:text}}",
			control:   "cloze_blanks",
			bindings:  map[string]string{"text": "text"},
			fields:    []string{"text"},
		},
		"typed": {
			templates: []string{"forward"},
			front:     "{{prompt}}{{type:answer}}",
			control:   "text",
			bindings: map[string]string{
				"answer": "answer", "accept": "accept",
				"ignore_case": "ignore_case", "ignore_whitespace": "ignore_whitespace",
				"regex": "regex",
			},
			fields: []string{"prompt", "answer", "accept", "ignore_case", "ignore_whitespace", "regex"},
		},
		"numeric": {
			templates: []string{"forward"},
			front:     "{{prompt}}{{type:value}}",
			control:   "numeric",
			bindings: map[string]string{
				"value": "value", "unit": "unit",
				"tolerance_absolute": "tolerance_absolute", "tolerance_relative": "tolerance_relative",
			},
			fields: []string{"prompt", "value", "unit", "tolerance_absolute", "tolerance_relative"},
		},
		"choice_single": {
			templates: []string{"forward"},
			front:     "{{question}}{{type:options}}",
			control:   "choice_single",
			bindings:  map[string]string{"options": "options", "answer": "answer"},
			fields:    []string{"question", "options", "answer"},
		},
		"choice_multi": {
			templates: []string{"forward"},
			front:     "{{question}}{{type:options}}",
			control:   "choice_multi",
			bindings:  map[string]string{"options": "options", "answers": "answers"},
			fields:    []string{"question", "options", "answers"},
		},
		"true_false": {
			templates: []string{"forward"},
			front:     "{{statement}}{{type:answer}}",
			control:   "true_false",
			bindings:  map[string]string{"answer": "answer"},
			fields:    []string{"statement", "answer"},
		},
		"list": {
			templates: []string{"forward"},
			front:     "{{prompt}}{{type:items}}",
			control:   "list",
			bindings:  map[string]string{"items": "items", "ordered": "ordered"},
			fields:    []string{"prompt", "items", "ordered"},
		},
		"short_answer": {
			templates: []string{"forward"},
			front:     "{{prompt}}{{type:reference}}",
			control:   "short_answer",
			bindings:  map[string]string{"reference": "reference"},
			fields:    []string{"prompt", "reference"},
		},
	}

	stocks := notetype.EngramStockConfigs()
	if len(stocks) != len(cases) {
		t.Fatalf("EngramStockConfigs() = %d note types, want %d", len(stocks), len(cases))
	}
	seen := map[string]bool{}
	for _, stock := range stocks {
		t.Run(stock.Kind, func(t *testing.T) {
			expected, ok := cases[stock.Kind]
			if !ok {
				t.Fatalf("unexpected kind %q", stock.Kind)
			}
			seen[stock.Kind] = true
			if err := stock.Config.Validate(); err != nil {
				t.Fatalf("Config.Validate() error = %v", err)
			}
			if stock.Name == "" {
				t.Error("note type name is empty")
			}
			if stock.Config.OriginalStockKind != nil {
				t.Error("original_stock_kind is set; an engram kind is not an Anki stock kind")
			}
			if got, want := stock.Config.SortFieldID, stock.Config.Fields[0].ID; got != want {
				t.Errorf("sort field = %q, want the first field %q", got, want)
			}
			var fieldIDs, fieldNames []string
			for _, field := range stock.Config.Fields {
				if field.ID != field.Name {
					t.Errorf("field id %q != name %q", field.ID, field.Name)
				}
				fieldIDs = append(fieldIDs, field.ID)
				fieldNames = append(fieldNames, field.Name)
			}
			if strings.Join(fieldIDs, ",") != strings.Join(expected.fields, ",") ||
				strings.Join(fieldNames, ",") != strings.Join(expected.fields, ",") {
				t.Errorf("fields = %v, want %v", fieldIDs, expected.fields)
			}
			var templateIDs []string
			for _, template := range stock.Config.Templates {
				templateIDs = append(templateIDs, template.ID)
			}
			if strings.Join(templateIDs, ",") != strings.Join(expected.templates, ",") {
				t.Errorf("templates = %v, want %v", templateIDs, expected.templates)
			}
			front := stock.Config.Templates[0].QFormat
			if front != expected.front {
				t.Errorf("front = %q, want %q", front, expected.front)
			}
			count := strings.Count(front, "{{type:")
			if expected.control == "" && count != 0 {
				t.Errorf("front has %d type placeholders, want none for a self-graded kind", count)
			}
			if expected.control != "" && count != 1 {
				t.Errorf("front has %d type placeholders, want exactly 1", count)
			}
			for _, template := range stock.Config.Templates {
				if template.AFormat == "" {
					t.Errorf("template %q has an empty back", template.ID)
				}
				if expected.control == "" {
					if template.Answer != nil {
						t.Errorf("template %q of the self-graded kind has an answer spec", template.ID)
					}
					continue
				}
				if template.Answer == nil {
					t.Fatalf("template %q has no answer spec, want control %q", template.ID, expected.control)
				}
				if template.Answer.Control != expected.control {
					t.Errorf("control = %q, want %q", template.Answer.Control, expected.control)
				}
				if err := answer.ValidateSpec(*template.Answer); err != nil {
					t.Errorf("answer.ValidateSpec() error = %v", err)
				}
				for role, field := range expected.bindings {
					if got := template.Answer.Bindings[role]; got != field {
						t.Errorf("binding %q = %q, want %q", role, got, field)
					}
				}
				// Every role whose field exists in the config must be bound; a
				// field the answer control reads cannot be left dangling.
				control, ok := answer.Lookup(template.Answer.Control)
				if !ok {
					t.Fatalf("control %q is not registered", template.Answer.Control)
				}
				declared, ok := control.(answer.Roles)
				if !ok {
					t.Fatalf("control %q does not declare its roles", template.Answer.Control)
				}
				for _, role := range declared.Roles() {
					if !fieldExists(stock.Config, role.Name) {
						continue
					}
					if _, bound := template.Answer.Bindings[role.Name]; !bound {
						t.Errorf("role %q has field %q but is not bound", role.Name, role.Name)
					}
				}
			}
		})
	}
	for kind := range cases {
		if !seen[kind] {
			t.Errorf("kind %q is missing from EngramStockConfigs()", kind)
		}
	}
}

// fieldPlainText reports whether the named field carries the plain_text flag.
func fieldPlainText(cfg notetype.Config, id string) bool {
	for _, field := range cfg.Fields {
		if field.ID == id {
			return field.PlainText
		}
	}
	return false
}

// fieldExists reports whether the config declares a field with this id.
func fieldExists(cfg notetype.Config, id string) bool {
	for _, field := range cfg.Fields {
		if field.ID == id {
			return true
		}
	}
	return false
}

// TestEngramStockByKind covers the lookup the migration uses.
func TestEngramStockByKind(t *testing.T) {
	stock, ok := notetype.EngramStockByKind("typed")
	if !ok || stock.Kind != "typed" {
		t.Fatalf("EngramStockByKind(typed) = %+v, %v", stock, ok)
	}
	if _, ok := notetype.EngramStockByKind("unknown"); ok {
		t.Error("EngramStockByKind(unknown) reported a match")
	}
}
