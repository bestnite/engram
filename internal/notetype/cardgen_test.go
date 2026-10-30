package notetype

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The card-generation goldens record, for every fixture note, the card ordinals
// Anki 26.09.3 says the note should generate
// (cardgen_expected_ordinals, from Note.cloze_numbers_in_fields() and
// ParsedTemplate.renders_with_fields) next to the cards its collection actually
// holds. The two differ only where the collection forced a first card for a
// note that generates none, which is a write-path rule and not part of
// GenerateCards.

// cardgenGolden is the subset of a fixture golden this test needs. The structs
// are local to this file so that the test does not depend on the shape the
// rendering goldens use.
type cardgenGolden struct {
	Fixture   string              `json:"fixture"`
	NoteTypes []cardgenGoldenType `json:"note_types"`
	Notes     []cardgenGoldenNote `json:"notes"`
}

type cardgenGoldenType struct {
	Name              string                  `json:"name"`
	Kind              string                  `json:"kind"`
	CSS               string                  `json:"css"`
	SortFieldIndex    int                     `json:"sort_field_index"`
	LaTeXPre          string                  `json:"latex_pre"`
	LaTeXPost         string                  `json:"latex_post"`
	LaTeXSVG          bool                    `json:"latex_svg"`
	OriginalStockKind *string                 `json:"original_stock_kind"`
	Fields            []cardgenGoldenField    `json:"fields"`
	Templates         []cardgenGoldenTemplate `json:"templates"`
}

type cardgenGoldenField struct {
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

type cardgenGoldenTemplate struct {
	Name            string `json:"name"`
	Ord             int    `json:"ord"`
	QFormat         string `json:"qfmt"`
	AFormat         string `json:"afmt"`
	BQFormat        string `json:"bqfmt"`
	BAFormat        string `json:"bafmt"`
	BrowserFont     string `json:"browser_font"`
	BrowserFontSize int    `json:"browser_font_size"`
}

type cardgenGoldenNote struct {
	GUID                    string              `json:"guid"`
	NoteTypeName            string              `json:"notetype_name"`
	Fields                  map[string]string   `json:"fields"`
	CardgenExpectedOrdinals []int               `json:"cardgen_expected_ordinals"`
	Cards                   []cardgenGoldenCard `json:"cards"`
}

type cardgenGoldenCard struct {
	ID      int64 `json:"id"`
	Ordinal int   `json:"ordinal"`
}

func loadCardgenGolden(t *testing.T, fixture string) cardgenGolden {
	t.Helper()
	encoded, err := os.ReadFile("testdata/anki/" + fixture + ".golden.json")
	if err != nil {
		t.Fatalf("read %s golden: %v", fixture, err)
	}
	var golden cardgenGolden
	if err := json.Unmarshal(encoded, &golden); err != nil {
		t.Fatalf("decode %s golden: %v", fixture, err)
	}
	return golden
}

// config builds the Config the fixture's note type describes, with the
// positional field and template ids the golden's ord values imply.
func (g cardgenGoldenType) config(t *testing.T) Config {
	t.Helper()

	fields := make([]Field, len(g.Fields))
	sortFieldID := ""
	for i, field := range g.Fields {
		if field.Ord != i {
			t.Fatalf("golden field %q has ord %d at position %d", field.Name, field.Ord, i)
		}
		id := fmt.Sprintf("f%d", i)
		if field.Ord == g.SortFieldIndex {
			sortFieldID = id
		}
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

// cardgenFixtures are the fixtures whose goldens record expected card
// ordinals. cardgen is the dedicated card-generation fixture; the rest are
// rendering fixtures that carry the same record, so they cross-check the
// generator against note types and templates it was not built around.
var cardgenFixtures = []string{
	"cardgen",
	"stock",
	"template-syntax",
	"template-filters",
	"cloze",
	"audio-tts-latex",
	"typeanswer",
}

// TestGenerateCardsGolden compares the generated card set with the card list
// Anki recorded for every note of every fixture.
func TestGenerateCardsGolden(t *testing.T) {
	for _, fixture := range cardgenFixtures {
		t.Run(fixture, func(t *testing.T) {
			golden := loadCardgenGolden(t, fixture)
			configs := make(map[string]Config, len(golden.NoteTypes))
			for _, noteType := range golden.NoteTypes {
				configs[noteType.Name] = noteType.config(t)
			}

			generatedTotal, recordedTotal := 0, 0
			for _, note := range golden.Notes {
				cfg, exists := configs[note.NoteTypeName]
				if !exists {
					t.Fatalf("note %q uses the unknown note type %q", note.GUID, note.NoteTypeName)
				}
				data := cardgenGoldenRenderData(cfg, note)
				generated, err := GenerateCards(cfg, data)
				if err != nil {
					t.Errorf("%s: GenerateCards: %v", note.GUID, err)
					continue
				}
				generatedTotal += len(generated)
				recordedTotal += len(note.Cards)

				want := goldenExpectedCards(t, note.GUID, cfg, note.CardgenExpectedOrdinals)
				if diff := cardgenCompareSets(generated, want); diff != "" {
					t.Errorf("%s: generated cards differ from the golden:\n%s", note.GUID, diff)
				}

				// Every generated card must exist in the recorded collection.
				// The reverse does not hold: the collection forces card 0 for a
				// note that generates nothing, and keeps empty cards until the
				// user deletes them.
				recorded := make(map[int]bool, len(note.Cards))
				for _, card := range note.Cards {
					recorded[card.Ordinal] = true
				}
				for _, card := range generated {
					if !recorded[card.Ordinal] {
						t.Errorf("%s: generated card %+v but the recorded collection holds only ordinals %v",
							note.GUID, card, cardgenRecordedOrdinals(note.Cards))
					}
				}
			}
			t.Logf("%s: %d note types, %d notes, %d generated cards, %d recorded cards, %d expected ordinals",
				fixture, len(golden.NoteTypes), len(golden.Notes), generatedTotal, recordedTotal,
				cardgenExpectedCount(golden.Notes))
		})
	}
}

// cardgenGoldenRenderData builds the render data for a note. Card generation
// reads only the fields and the tags, but the full value is supplied so that a
// template testing a special field behaves as it would when the card is shown.
func cardgenGoldenRenderData(cfg Config, note cardgenGoldenNote) RenderData {
	cardID := ""
	if len(note.Cards) > 0 {
		cardID = strconv.FormatInt(note.Cards[0].ID, 10)
	}
	cardName := ""
	if len(cfg.Templates) > 0 {
		cardName = cfg.Templates[0].Name
	}
	return RenderData{
		Fields:      cardgenGoldenFieldValues(cfg, note.Fields),
		Tags:        []string{"alpha", "beta"},
		TypeName:    note.NoteTypeName,
		DeckName:    "Placeholder::Lesson 1",
		SubdeckName: "Lesson 1",
		CardName:    cardName,
		CardFlag:    "flag0",
		CardID:      cardID,
	}
}

func cardgenGoldenFieldValues(cfg Config, byName map[string]string) map[string]string {
	fields := make(map[string]string, len(cfg.Fields))
	for _, field := range cfg.Fields {
		fields[field.ID] = byName[field.Name]
	}
	return fields
}

// goldenExpectedCards turns the recorded ordinals into the card set they name.
func goldenExpectedCards(t *testing.T, guid string, cfg Config, ordinals []int) []GeneratedCard {
	t.Helper()
	cards := make([]GeneratedCard, 0, len(ordinals))
	for _, ordinal := range ordinals {
		if cfg.Kind == KindCloze {
			cards = append(cards, GeneratedCard{
				Template: clozeTemplatePrefix + strconv.Itoa(ordinal+1),
				Ordinal:  ordinal,
			})
			continue
		}
		if ordinal < 0 || ordinal >= len(cfg.Templates) {
			t.Errorf("%s: expected ordinal %d has no template in a %d-template note type",
				guid, ordinal, len(cfg.Templates))
			continue
		}
		cards = append(cards, GeneratedCard{Template: cfg.Templates[ordinal].ID, Ordinal: ordinal})
	}
	return cards
}

// cardgenCompareSets returns an empty string when both slices hold the same
// cards, otherwise a description of the difference.
func cardgenCompareSets(got, want []GeneratedCard) string {
	gotSet := make(map[GeneratedCard]bool, len(got))
	for _, card := range got {
		gotSet[card] = true
	}
	wantSet := make(map[GeneratedCard]bool, len(want))
	for _, card := range want {
		wantSet[card] = true
	}
	var lines []string
	for card := range gotSet {
		if !wantSet[card] {
			lines = append(lines, "  unexpected: "+cardgenCardText(card))
		}
	}
	for card := range wantSet {
		if !gotSet[card] {
			lines = append(lines, "  missing:    "+cardgenCardText(card))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func cardgenCardText(card GeneratedCard) string {
	return fmt.Sprintf("%s (ordinal %d)", card.Template, card.Ordinal)
}

func cardgenRecordedOrdinals(cards []cardgenGoldenCard) []int {
	ordinals := make([]int, len(cards))
	for i, card := range cards {
		ordinals[i] = card.Ordinal
	}
	return ordinals
}

func cardgenExpectedCount(notes []cardgenGoldenNote) int {
	total := 0
	for _, note := range notes {
		total += len(note.CardgenExpectedOrdinals)
	}
	return total
}

// TestGenerateCardsNormal checks the normal-note-type rule: a card exists for a
// template that references a field with content, and only for such a template.
func TestGenerateCardsNormal(t *testing.T) {
	cfg := cardgenConfig(KindNormal, []string{"Front", "Back"}, []string{
		"{{Front}}",
		"{{Back}}",
		"literal text only",
		"{{#Back}}{{Front}}{{/Back}}",
	})

	cases := []struct {
		name   string
		values map[string]string
		want   []GeneratedCard
	}{
		{
			name:   "empty front skips that template",
			values: map[string]string{"Front": "", "Back": "back"},
			want:   []GeneratedCard{{Template: "t1", Ordinal: 1}},
		},
		{
			name:   "both fields filled",
			values: map[string]string{"Front": "front", "Back": "back"},
			want: []GeneratedCard{
				{Template: "t0", Ordinal: 0},
				{Template: "t1", Ordinal: 1},
				{Template: "t3", Ordinal: 3},
			},
		},
		{
			name:   "whitespace and empty tags count as empty",
			values: map[string]string{"Front": "  <br> <div></div>", "Back": "back"},
			want:   []GeneratedCard{{Template: "t1", Ordinal: 1}},
		},
		{
			name:   "nothing filled",
			values: map[string]string{},
			want:   nil,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			data := RenderData{Fields: cardgenValues(cfg, testCase.values)}
			got, err := GenerateCards(cfg, data)
			if err != nil {
				t.Fatalf("GenerateCards: %v", err)
			}
			if diff := cardgenCompareSets(got, testCase.want); diff != "" {
				t.Errorf("generated cards differ:\n%s", diff)
			}
		})
	}
}

// TestGenerateCardsSpecialFields pins the special-field rules Anki applies:
// Tags counts only when the note has tags, and a note field that shares a
// special field's name wins over it.
func TestGenerateCardsSpecialFields(t *testing.T) {
	t.Run("tags conditional", func(t *testing.T) {
		cfg := cardgenConfig(KindNormal, []string{"Front"}, []string{"{{#Tags}}{{Front}}{{/Tags}}"})
		data := RenderData{Fields: cardgenValues(cfg, map[string]string{"Front": "hello"})}

		got, err := GenerateCards(cfg, data)
		if err != nil {
			t.Fatalf("GenerateCards: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("untagged note generated %v, want no card", got)
		}

		data.Tags = []string{"alpha", "beta"}
		got, err = GenerateCards(cfg, data)
		if err != nil {
			t.Fatalf("GenerateCards with tags: %v", err)
		}
		if diff := cardgenCompareSets(got, []GeneratedCard{{Template: "t0", Ordinal: 0}}); diff != "" {
			t.Errorf("tagged note: generated cards differ:\n%s", diff)
		}
	})

	t.Run("note field shadows special field", func(t *testing.T) {
		cfg := cardgenConfig(KindNormal, []string{"Front", "Deck"}, []string{"{{#Deck}}{{Front}}{{/Deck}}"})
		data := RenderData{
			Fields:   cardgenValues(cfg, map[string]string{"Front": "hello"}),
			DeckName: "Placeholder::Lesson 1",
		}

		got, err := GenerateCards(cfg, data)
		if err != nil {
			t.Fatalf("GenerateCards: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("empty Deck field generated %v, want no card", got)
		}

		data.Fields["f1"] = "custom deck"
		got, err = GenerateCards(cfg, data)
		if err != nil {
			t.Fatalf("GenerateCards with Deck filled: %v", err)
		}
		if diff := cardgenCompareSets(got, []GeneratedCard{{Template: "t0", Ordinal: 0}}); diff != "" {
			t.Errorf("filled Deck field: generated cards differ:\n%s", diff)
		}
	})
}

// TestGenerateCardsCloze checks the cloze rule: one card per number in any
// field, whether or not a template renders that field, with nested numbers and
// duplicates collapsing to one card.
func TestGenerateCardsCloze(t *testing.T) {
	cfg := cardgenConfig(KindCloze, []string{"Text", "Extra"}, []string{"{{cloze:Text}}"})

	cases := []struct {
		name   string
		values map[string]string
		want   []GeneratedCard
	}{
		{
			name:   "number in an unreferenced field",
			values: map[string]string{"Text": "{{c1::one}}", "Extra": "{{c2::two}}"},
			want: []GeneratedCard{
				{Template: "cloze:1", Ordinal: 0},
				{Template: "cloze:2", Ordinal: 1},
			},
		},
		{
			name:   "same number in two fields is one card",
			values: map[string]string{"Text": "{{c1::one}}", "Extra": "{{c1::uno}}"},
			want:   []GeneratedCard{{Template: "cloze:1", Ordinal: 0}},
		},
		{
			name:   "nested numbers all generate",
			values: map[string]string{"Text": "{{c1::outer {{c2::inner}}}} and {{c4::four}}"},
			want: []GeneratedCard{
				{Template: "cloze:1", Ordinal: 0},
				{Template: "cloze:2", Ordinal: 1},
				{Template: "cloze:4", Ordinal: 3},
			},
		},
		{
			name:   "zero is an image-occlusion placeholder, not a card",
			values: map[string]string{"Text": "{{c0::image-occlusion:rect:left=0.1}}"},
			want:   nil,
		},
		{
			name:   "numbers above the ordinal cap share the last ordinal",
			values: map[string]string{"Text": "{{c500::high}}"},
			want:   []GeneratedCard{{Template: "cloze:500", Ordinal: 499}},
		},
		{
			name:   "no clozes",
			values: map[string]string{"Text": "plain text"},
			want:   nil,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			data := RenderData{Fields: cardgenValues(cfg, testCase.values)}
			got, err := GenerateCards(cfg, data)
			if err != nil {
				t.Fatalf("GenerateCards: %v", err)
			}
			if diff := cardgenCompareSets(got, testCase.want); diff != "" {
				t.Errorf("generated cards differ:\n%s", diff)
			}
		})
	}
}

// TestEmptyExistingCards checks that exactly the stored cards the note no
// longer generates are reported, and that reporting leaves them in place.
func TestEmptyExistingCards(t *testing.T) {
	normal := cardgenConfig(KindNormal, []string{"Front", "Back"}, []string{"{{Front}}", "{{Back}}"})
	existing := []ExistingCard{
		{Template: "t0", Ordinal: 0},
		{Template: "t1", Ordinal: 1},
		{Template: "t9", Ordinal: 5},
	}
	data := RenderData{Fields: cardgenValues(normal, map[string]string{"Front": "front"})}

	empty, err := EmptyExistingCards(normal, data, existing)
	if err != nil {
		t.Fatalf("EmptyExistingCards: %v", err)
	}
	want := []ExistingCard{
		{Template: "t1", Ordinal: 1}, // front references an empty field
		{Template: "t9", Ordinal: 5}, // template no longer exists
	}
	if len(empty) != len(want) {
		t.Fatalf("EmptyExistingCards = %v, want %v", empty, want)
	}
	for i := range want {
		if empty[i] != want[i] {
			t.Errorf("empty card %d = %+v, want %+v", i, empty[i], want[i])
		}
	}
	if len(existing) != 3 {
		t.Errorf("EmptyExistingCards changed the stored card list: %v", existing)
	}

	cloze := cardgenConfig(KindCloze, []string{"Text"}, []string{"{{cloze:Text}}"})
	clozeData := RenderData{Fields: cardgenValues(cloze, map[string]string{"Text": "{{c1::one}}"})}
	clozeExisting := []ExistingCard{{Template: "cloze:1", Ordinal: 0}, {Template: "cloze:2", Ordinal: 1}}

	empty, err = EmptyExistingCards(cloze, clozeData, clozeExisting)
	if err != nil {
		t.Fatalf("EmptyExistingCards for cloze: %v", err)
	}
	if len(clozeExisting) != 2 {
		t.Fatalf("EmptyExistingCards changed the stored card list: %v", clozeExisting)
	}
	if len(empty) != 1 || empty[0] != clozeExisting[1] {
		t.Errorf("EmptyExistingCards = %v, want [%+v]", empty, clozeExisting[1])
	}
}

// TestGenerateCardsErrors checks that a template which cannot be rendered fails
// the call and names the template.
func TestGenerateCardsErrors(t *testing.T) {
	cases := []struct {
		name     string
		cfg      Config
		wantCode ErrorCode
	}{
		{
			name:     "unclosed conditional",
			cfg:      cardgenConfig(KindNormal, []string{"Front"}, []string{"{{#Front}}{{Front}}"}),
			wantCode: CodeUnclosedConditional,
		},
		{
			name:     "unknown field",
			cfg:      cardgenConfig(KindNormal, []string{"Front"}, []string{"{{Nope}}"}),
			wantCode: CodeUnknownField,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			data := RenderData{Fields: cardgenValues(testCase.cfg, map[string]string{"Front": "front"})}
			_, err := GenerateCards(testCase.cfg, data)
			if err == nil {
				t.Fatal("GenerateCards succeeded, want an error")
			}
			var renderErr *RenderError
			if !errors.As(err, &renderErr) {
				t.Fatalf("GenerateCards error %v does not wrap a RenderError", err)
			}
			if renderErr.Code != testCase.wantCode {
				t.Errorf("error code = %q, want %q", renderErr.Code, testCase.wantCode)
			}
			if !strings.Contains(err.Error(), `"t0"`) {
				t.Errorf("error %q does not name the template id", err)
			}
		})
	}

	t.Run("unknown kind", func(t *testing.T) {
		if _, err := GenerateCards(Config{Kind: "weird"}, RenderData{}); err == nil {
			t.Fatal("GenerateCards accepted an unknown kind")
		}
	})
}

// cardgenConfig builds a note type with the positional ids f0.. and t0.. the
// golden test also uses, so a card set can be written in the same terms.
func cardgenConfig(kind Kind, fieldNames, qFormats []string) Config {
	cfg := Config{Kind: kind}
	for i, name := range fieldNames {
		cfg.Fields = append(cfg.Fields, Field{ID: fmt.Sprintf("f%d", i), Name: name})
	}
	for i, qfmt := range qFormats {
		cfg.Templates = append(cfg.Templates, Template{
			ID:      fmt.Sprintf("t%d", i),
			Name:    fmt.Sprintf("Card %d", i+1),
			QFormat: qfmt,
			AFormat: "{{FrontSide}}",
		})
	}
	return cfg
}

func cardgenValues(cfg Config, byName map[string]string) map[string]string {
	values := make(map[string]string, len(cfg.Fields))
	for _, field := range cfg.Fields {
		values[field.ID] = byName[field.Name]
	}
	return values
}
