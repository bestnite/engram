package notetype

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
)

// stockGolden is the part of testdata/anki/stock.golden.json this package checks
// against. The golden records what Anki returned for the fixture, so equality
// with it is the proof that a copied stock definition was not altered.
type stockGolden struct {
	NoteTypes []stockGoldenNoteType `json:"note_types"`
}

type stockGoldenNoteType struct {
	Name              string                `json:"name"`
	Kind              string                `json:"kind"`
	CSS               string                `json:"css"`
	SortFieldIndex    int                   `json:"sort_field_index"`
	LaTeXPre          string                `json:"latex_pre"`
	LaTeXPost         string                `json:"latex_post"`
	LaTeXSVG          bool                  `json:"latex_svg"`
	OriginalStockKind *string               `json:"original_stock_kind"`
	Fields            []stockGoldenField    `json:"fields"`
	Templates         []stockGoldenTemplate `json:"templates"`
}

type stockGoldenField struct {
	Name              string `json:"name"`
	Ord               int    `json:"ord"`
	Sticky            bool   `json:"sticky"`
	RTL               bool   `json:"rtl"`
	Font              string `json:"font"`
	FontSize          int    `json:"font_size"`
	Description       string `json:"description"`
	PlainText         bool   `json:"plain_text"`
	Collapsed         bool   `json:"collapsed"`
	ExcludeFromSearch bool   `json:"exclude_from_search"`
	Tag               *int   `json:"tag"`
	PreventDeletion   bool   `json:"prevent_deletion"`
}

type stockGoldenTemplate struct {
	Name            string `json:"name"`
	Ord             int    `json:"ord"`
	QFormat         string `json:"qfmt"`
	AFormat         string `json:"afmt"`
	BQFormat        string `json:"bqfmt"`
	BAFormat        string `json:"bafmt"`
	BrowserFont     string `json:"browser_font"`
	BrowserFontSize int    `json:"browser_font_size"`
}

// config converts a golden entry to the Config the generator is expected to
// produce, using the positional ids the stock definitions assign. Comparing the
// whole value covers every field and template option, not only the strings.
func (g stockGoldenNoteType) config(t *testing.T) Config {
	t.Helper()

	fields := make([]Field, len(g.Fields))
	sortFieldID := ""
	for i, field := range g.Fields {
		if field.Ord != i {
			t.Fatalf("golden field %q has ord %d at position %d", field.Name, field.Ord, i)
		}
		id := fmt.Sprintf("f%d", i)
		fields[i] = Field{
			ID:                id,
			Name:              field.Name,
			Sticky:            field.Sticky,
			RTL:               field.RTL,
			Font:              field.Font,
			FontSize:          field.FontSize,
			Description:       field.Description,
			PlainText:         field.PlainText,
			Collapsed:         field.Collapsed,
			ExcludeFromSearch: field.ExcludeFromSearch,
			Tag:               field.Tag,
			PreventDeletion:   field.PreventDeletion,
		}
		if field.Ord == g.SortFieldIndex {
			sortFieldID = id
		}
	}
	if sortFieldID == "" {
		t.Fatalf("golden sort field index %d is not a field ordinal", g.SortFieldIndex)
	}

	templates := make([]Template, len(g.Templates))
	for i, template := range g.Templates {
		if template.Ord != i {
			t.Fatalf("golden template %q has ord %d at position %d", template.Name, template.Ord, i)
		}
		templates[i] = Template{
			ID:              fmt.Sprintf("t%d", i),
			Name:            template.Name,
			QFormat:         template.QFormat,
			AFormat:         template.AFormat,
			BQFormat:        template.BQFormat,
			BAFormat:        template.BAFormat,
			BrowserFont:     template.BrowserFont,
			BrowserFontSize: template.BrowserFontSize,
		}
	}

	var stockKind *OriginalStockKind
	if g.OriginalStockKind != nil {
		kind := OriginalStockKind(*g.OriginalStockKind)
		stockKind = &kind
	}

	return Config{
		Kind:              Kind(g.Kind),
		Fields:            fields,
		Templates:         templates,
		CSS:               g.CSS,
		SortFieldID:       sortFieldID,
		LaTeXPre:          g.LaTeXPre,
		LaTeXPost:         g.LaTeXPost,
		LaTeXSVG:          g.LaTeXSVG,
		OriginalStockKind: stockKind,
	}
}

func TestStockConfigs(t *testing.T) {
	configs := StockConfigs()
	if len(configs) != 6 {
		t.Fatalf("StockConfigs() returned %d note types, want 6", len(configs))
	}

	seen := make(map[OriginalStockKind]struct{}, len(configs))
	for _, config := range configs {
		if config.OriginalStockKind == nil {
			t.Fatal("stock note type has no original stock kind")
		}
		kind := *config.OriginalStockKind
		if _, exists := seen[kind]; exists {
			t.Errorf("stock kind %q appears more than once", kind)
		}
		seen[kind] = struct{}{}

		name := StockName(kind)
		if name == "" {
			t.Errorf("StockName(%q) returned an empty name", kind)
		}
		if err := config.Validate(); err != nil {
			t.Errorf("%s: Validate() returned %v", name, err)
		}
		if config.CSS == "" {
			t.Errorf("%s: CSS is empty", name)
		}
		if config.LaTeXPre == "" || config.LaTeXPost == "" {
			t.Errorf("%s: LaTeX pre/post is empty", name)
		}
		if config.SortFieldID == "" {
			t.Errorf("%s: SortFieldID is empty", name)
		}
		if !slices.ContainsFunc(config.Fields, func(field Field) bool { return field.ID == config.SortFieldID }) {
			t.Errorf("%s: SortFieldID %q is not a field id", name, config.SortFieldID)
		}
	}

	for _, kind := range []OriginalStockKind{
		StockBasic,
		StockBasicAndReversed,
		StockBasicOptionalReversed,
		StockBasicTyping,
		StockCloze,
		StockImageOcclusion,
	} {
		if _, exists := seen[kind]; !exists {
			t.Errorf("StockConfigs() is missing %q", kind)
		}
	}
}

func TestStockConfigsAreIndependent(t *testing.T) {
	first := StockConfigs()
	first[0].Fields[0].Name = "Changed"
	*first[0].OriginalStockKind = StockImageOcclusion

	var tagged *int
	for _, config := range first {
		for _, field := range config.Fields {
			if field.Tag != nil {
				tagged = field.Tag
				break
			}
		}
		if tagged != nil {
			break
		}
	}
	if tagged == nil {
		t.Fatal("no stock field has a tag, so the tag copy cannot be checked")
	}
	*tagged = 999

	second := StockConfigs()
	if second[0].Fields[0].Name != "Front" {
		t.Errorf("a caller's edit changed a later StockConfigs result: field name is %q", second[0].Fields[0].Name)
	}
	if second[0].OriginalStockKind == nil || *second[0].OriginalStockKind != StockBasic {
		t.Errorf("a caller's edit changed a later StockConfigs result's stock kind")
	}
	for _, config := range second {
		for _, field := range config.Fields {
			if field.Tag != nil && *field.Tag == 999 {
				t.Errorf("a caller's edit changed a later StockConfigs result's field tag")
			}
		}
	}
}

func TestStockConfigsMatchGolden(t *testing.T) {
	encoded, err := os.ReadFile("testdata/anki/stock.golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var golden stockGolden
	if err := json.Unmarshal(encoded, &golden); err != nil {
		t.Fatalf("decode golden: %v", err)
	}

	noteTypes := make(map[string]stockGoldenNoteType, len(golden.NoteTypes))
	for _, noteType := range golden.NoteTypes {
		noteTypes[noteType.Name] = noteType
	}

	for _, config := range StockConfigs() {
		if config.OriginalStockKind == nil {
			t.Fatal("stock note type has no original stock kind")
		}
		name := StockName(*config.OriginalStockKind)
		noteType, exists := noteTypes[name]
		if !exists {
			t.Errorf("golden has no note type %q", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			want := noteType.config(t)
			if !reflect.DeepEqual(config, want) {
				t.Errorf("stock config differs from the golden:\n got: %+v\nwant: %+v", config, want)
			}
		})
	}
}
