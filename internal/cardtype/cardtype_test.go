package cardtype

import (
	"encoding/json"
	"strings"
	"testing"
)

// fakeType is a fake card type for tests: defined only in the test file, so the core pipeline needs no change for it
// (acceptance: register a fake card type and assert the core pipeline can handle it).
// It also implements the three optional capability interfaces, to verify the narrow interface assertions.
type fakeType struct{}

func (fakeType) Label() string { return "cardtype.fake" }

// Describe makes the fake card type satisfy CardType's new self-describing method; the content matches its prompt field.
func (fakeType) Describe() Description {
	return Description{
		Kind:          "fake",
		LabelKey:      "cardtype.fake",
		AnswerControl: AnswerNone,
		FrontField:    "prompt",
		BackField:     "prompt",
		Fields:        fieldsWithCommon(FieldSpec{Key: "prompt", Control: ControlTextarea, Required: true}),
	}
}

func (fakeType) Validate(fields map[string]any) error {
	if _, err := stringField(fields, "prompt"); err != nil {
		return err
	}
	return nil
}

func (fakeType) Cards(note Note) []Card {
	return []Card{{Template: "forward", Ordinal: 0, Fields: note.Fields}}
}

func (fakeType) Render(card Card, side Side) (RenderResult, error) {
	return RenderResult{Body: "fake:" + card.Fields["prompt"].(string)}, nil
}

func (fakeType) Grade(input any) (int, map[string]any, bool) {
	return 3, map[string]any{"input": input}, true
}

func (fakeType) ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error) { return raw, nil }

func (fakeType) GivenText(map[string]any, map[string]any) string { return "" }

func (fakeType) PromptContext(note Note) PromptContext {
	return PromptContext{Question: note.Fields["prompt"].(string)}
}

func (fakeType) ReferenceRefs(note Note) []string { return []string{"ref:1"} }

// TestRegistryHandlesFakeType asserts the registry treats the fake card type as a first-class citizen.
func TestRegistryHandlesFakeType(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("fake", fakeType{}); err != nil {
		t.Fatalf("register fake: %v", err)
	}

	t.Run("lookup", func(t *testing.T) {
		got, ok := r.Lookup("fake")
		if !ok || got != (fakeType{}) {
			t.Fatalf("Lookup = (%v, %v), want fakeType", got, ok)
		}
	})

	t.Run("validate", func(t *testing.T) {
		if err := r.Validate("fake", map[string]any{"prompt": "hi"}); err != nil {
			t.Fatalf("Validate valid: %v", err)
		}
		if err := r.Validate("fake", map[string]any{}); err == nil {
			t.Fatal("Validate missing field: want error")
		}
	})

	t.Run("cards", func(t *testing.T) {
		cards, err := r.Cards(Note{Kind: "fake", Fields: map[string]any{"prompt": "hi"}})
		if err != nil {
			t.Fatalf("Cards: %v", err)
		}
		if len(cards) != 1 || cards[0].Template != "forward" || cards[0].Ordinal != 0 {
			t.Fatalf("Cards = %+v, want one forward card", cards)
		}
	})

	t.Run("optional capabilities", func(t *testing.T) {
		impl, _ := r.Lookup("fake")
		if g, ok := impl.(Grader); !ok {
			t.Fatal("fake should satisfy Grader")
		} else if rating, detail, ok := g.Grade("x"); !ok || rating != 3 || detail["input"] != "x" {
			t.Fatalf("Grade = (%d, %v, %v), want (3, detail, true)", rating, detail, ok)
		}
		if _, ok := impl.(PromptContexter); !ok {
			t.Fatal("fake should satisfy PromptContexter")
		}
		if _, ok := impl.(ReferenceRefer); !ok {
			t.Fatal("fake should satisfy ReferenceRefer")
		}
	})
}

// TestRegistryUnknownKind asserts an unknown card type returns a readable English error.
func TestRegistryUnknownKind(t *testing.T) {
	err := Validate("does_not_exist", nil)
	if err == nil || !strings.Contains(err.Error(), `unknown card type "does_not_exist"`) {
		t.Fatalf("Validate unknown kind = %v", err)
	}
	if _, err := Cards(Note{Kind: "does_not_exist"}); err == nil || !strings.Contains(err.Error(), "unknown card type") {
		t.Fatalf("Cards unknown kind = %v", err)
	}
}

// TestRegisterRejectsDuplicatesAndEmpty asserts duplicate registration and an empty kind are rejected.
func TestRegisterRejectsDuplicatesAndEmpty(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("", fakeType{}); err == nil {
		t.Fatal("empty kind: want error")
	}
	if err := r.Register("fake", nil); err == nil {
		t.Fatal("nil type: want error")
	}
	if err := r.Register("fake", fakeType{}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register("fake", fakeType{}); err == nil {
		t.Fatal("duplicate register: want error")
	}
}
