package notetype

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The golden fixtures record what Anki 26.09.3 rendered for a set of notes.
// question_nodes_text and answer_nodes_text are the template output before the
// collection layer adds the card CSS and the audio markers, which makes them the
// exact contract of this package's renderer.

type templateGolden struct {
	Fixture   string                `json:"fixture"`
	NoteTypes []stockGoldenNoteType `json:"note_types"`
	Notes     []templateGoldenNote  `json:"notes"`
}

type templateGoldenNote struct {
	GUID         string               `json:"guid"`
	NoteTypeName string               `json:"notetype_name"`
	Fields       map[string]string    `json:"fields"`
	Cards        []templateGoldenCard `json:"cards"`
}

type templateGoldenCard struct {
	ID                int64  `json:"id"`
	Ordinal           int    `json:"ordinal"`
	QuestionNodesText string `json:"question_nodes_text"`
	AnswerNodesText   string `json:"answer_nodes_text"`
	FrontIsEmpty      bool   `json:"front_is_empty"`
}

// blankFrontMessage is the text Anki's collection layer appends when a front
// template renders blank. The template engine does not produce it, so the test
// checks that the rendered front plus this message reproduces the golden.
const blankFrontMessage = "<div>The front of this card is blank.<br><a href='https://docs.ankiweb.invalid/templates/errors.html#front-of-card-is-blank'>More information</a></div>"

func loadTemplateGolden(t *testing.T, fixture string) templateGolden {
	t.Helper()
	encoded, err := os.ReadFile("testdata/anki/" + fixture + ".golden.json")
	if err != nil {
		t.Fatalf("read %s golden: %v", fixture, err)
	}
	var golden templateGolden
	if err := json.Unmarshal(encoded, &golden); err != nil {
		t.Fatalf("decode %s golden: %v", fixture, err)
	}
	return golden
}

// TestRenderGolden renders every card of the stock, syntax and filter fixtures
// and compares the result with Anki's recorded output byte for byte.
func TestRenderGolden(t *testing.T) {
	for _, fixture := range []string{"stock", "template-syntax", "template-filters"} {
		t.Run(fixture, func(t *testing.T) {
			golden := loadTemplateGolden(t, fixture)
			configs := make(map[string]Config, len(golden.NoteTypes))
			for _, noteType := range golden.NoteTypes {
				configs[noteType.Name] = noteType.config(t)
			}
			if fixture == "stock" {
				assertStockConfigsMatchGolden(t, configs)
			}

			cards := 0
			for _, note := range golden.Notes {
				cfg, exists := configs[note.NoteTypeName]
				if !exists {
					t.Fatalf("note %q uses the unknown note type %q", note.GUID, note.NoteTypeName)
				}
				for _, card := range note.Cards {
					cards++
					checkGoldenCard(t, cfg, note, card)
				}
			}
			t.Logf("%s: %d note types, %d notes, %d cards", fixture, len(golden.NoteTypes), len(golden.Notes), cards)
		})
	}
}

func assertStockConfigsMatchGolden(t *testing.T, configs map[string]Config) {
	t.Helper()
	for _, config := range StockConfigs() {
		if config.OriginalStockKind == nil {
			t.Fatal("stock note type has no original stock kind")
		}
		name := StockName(*config.OriginalStockKind)
		goldenConfig, exists := configs[name]
		if !exists {
			t.Errorf("golden has no note type %q", name)
			continue
		}
		if !configsEqual(config, goldenConfig) {
			t.Errorf("%s: StockConfigs() differs from the golden config", name)
		}
	}
}

func configsEqual(a, b Config) bool {
	return reflect.DeepEqual(a, b)
}

func checkGoldenCard(t *testing.T, cfg Config, note templateGoldenNote, card templateGoldenCard) {
	t.Helper()
	tmpl, err := templateForCard(cfg, card.Ordinal)
	if err != nil {
		t.Errorf("%s card %d: %v", note.GUID, card.Ordinal, err)
		return
	}

	data := RenderData{
		Fields:       fieldsByName(cfg, note.Fields),
		Tags:         []string{"alpha", "beta"},
		TypeName:     note.NoteTypeName,
		DeckName:     "Placeholder::Lesson 1",
		SubdeckName:  "Lesson 1",
		CardName:     tmpl.Name,
		CardFlag:     "flag0",
		CardID:       strconv.FormatInt(card.ID, 10),
		ClozeOrdinal: 0,
	}
	if cfg.Kind == KindCloze {
		data.ClozeOrdinal = card.Ordinal + 1
	}

	front, err := Render(cfg, tmpl, SideFront, data)
	if err != nil {
		t.Errorf("%s card %d (%s): front: %v", note.GUID, card.Ordinal, tmpl.Name, err)
		return
	}
	back, err := Render(cfg, tmpl, SideBack, data)
	if err != nil {
		t.Errorf("%s card %d (%s): back: %v", note.GUID, card.Ordinal, tmpl.Name, err)
		return
	}

	problem := FrontProblem(cfg, tmpl, data)
	switch {
	case card.FrontIsEmpty && cfg.Kind == KindNormal:
		if problem == nil || problem.Code != CodeEmptyFront {
			t.Errorf("%s card %d: FrontProblem() = %v, want code %q", note.GUID, card.Ordinal, problem, CodeEmptyFront)
		}
		// The golden question is the rendered front followed by the message
		// the collection layer appends; the answer is that message alone.
		wantFront, found := strings.CutSuffix(card.QuestionNodesText, blankFrontMessage)
		if !found {
			t.Fatalf("%s card %d: golden question does not end with the blank-front message: %q", note.GUID, card.Ordinal, card.QuestionNodesText)
		}
		if front != wantFront {
			t.Errorf("%s card %d: front:\n got %q\nwant %q", note.GUID, card.Ordinal, front, wantFront)
		}
		if card.AnswerNodesText != blankFrontMessage {
			t.Errorf("%s card %d: golden answer is not the blank-front message: %q", note.GUID, card.Ordinal, card.AnswerNodesText)
		}
		// The rendered back is not compared: Anki replaces the whole answer
		// with the message once the front is blank, which is the collection
		// layer's decision, so the golden records the message rather than the
		// back template output.
	default:
		if problem != nil {
			t.Errorf("%s card %d: FrontProblem() = %v, want nil", note.GUID, card.Ordinal, problem)
		}
		if front != card.QuestionNodesText {
			t.Errorf("%s card %d: front:\n got %q\nwant %q", note.GUID, card.Ordinal, front, card.QuestionNodesText)
		}
		if back != card.AnswerNodesText {
			t.Errorf("%s card %d: back:\n got %q\nwant %q", note.GUID, card.Ordinal, back, card.AnswerNodesText)
		}
	}
}

// templateForCard returns the template a card ordinal belongs to. A cloze note
// type has one template and its ordinals are cloze numbers; every other note
// type uses the ordinal as the template position.
func templateForCard(cfg Config, ordinal int) (Template, error) {
	if cfg.Kind == KindCloze {
		if len(cfg.Templates) == 0 {
			return Template{}, errNoTemplate
		}
		return cfg.Templates[0], nil
	}
	if ordinal < 0 || ordinal >= len(cfg.Templates) {
		return Template{}, errNoTemplate
	}
	return cfg.Templates[ordinal], nil
}

var errNoTemplate = errors.New("note type has no template for the card ordinal")

func fieldsByName(cfg Config, values map[string]string) map[string]string {
	fields := make(map[string]string, len(cfg.Fields))
	for _, field := range cfg.Fields {
		fields[field.ID] = values[field.Name]
	}
	return fields
}
