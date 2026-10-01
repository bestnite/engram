package cardtype

import (
	"strings"
	"testing"
)

// TestDefaultRegistryKinds 断言内置记忆类题型都已注册。
func TestDefaultRegistryKinds(t *testing.T) {
	for _, kind := range []string{"basic", "basic_both", "cloze", "list"} {
		if _, ok := Lookup(kind); !ok {
			t.Errorf("kind %q not registered", kind)
		}
	}
}

func TestBasicValidateAndCards(t *testing.T) {
	fields := map[string]any{"front": "Q", "back": "A"}
	if err := Validate("basic", fields); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	cards, err := Cards(Note{Kind: "basic", Fields: fields})
	if err != nil {
		t.Fatalf("Cards: %v", err)
	}
	if len(cards) != 1 || cards[0].Template != "forward" || cards[0].Ordinal != 0 {
		t.Fatalf("cards = %+v, want one forward card", cards)
	}

	front, _ := basicType{}.Render(cards[0], SideFront)
	if front.Body != "Q" {
		t.Errorf("front = %q, want Q", front.Body)
	}
	back, _ := basicType{}.Render(cards[0], SideBack)
	if back.Body != "A" {
		t.Errorf("back = %q, want A", back.Body)
	}
}

func TestBasicValidateErrors(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"missing front", map[string]any{"back": "A"}, `missing required field "front"`},
		{"missing back", map[string]any{"front": "Q"}, `missing required field "back"`},
		{"empty front", map[string]any{"front": "", "back": "A"}, `field "front" must not be empty`},
		{"wrong type", map[string]any{"front": 1, "back": "A"}, `field "front" must be a string`},
		{"bad source_url", map[string]any{"front": "Q", "back": "A", "source_url": 5}, `field "source_url" must be a non-empty string`},
		{"bad extra", map[string]any{"front": "Q", "back": "A", "extra": "x"}, `field "extra" must be an object`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate("basic", tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want substring %q", err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "basic: ") {
				t.Errorf("error = %q, want basic prefix", err)
			}
		})
	}
}

func TestBasicOptionalFieldsAccepted(t *testing.T) {
	fields := map[string]any{
		"front": "Q", "back": "A",
		"extra":      map[string]any{"k": "v"},
		"source_url": "https://example.com/x",
	}
	if err := Validate("basic", fields); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestBasicBothCards(t *testing.T) {
	fields := map[string]any{"front": "Q", "back": "A"}
	if err := Validate("basic_both", fields); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	cards, _ := Cards(Note{Kind: "basic_both", Fields: fields})
	if len(cards) != 2 {
		t.Fatalf("len(cards) = %d, want 2", len(cards))
	}
	if cards[0].Template != "forward" || cards[0].Ordinal != 0 {
		t.Errorf("card[0] = %+v", cards[0])
	}
	if cards[1].Template != "reverse" || cards[1].Ordinal != 1 {
		t.Errorf("card[1] = %+v", cards[1])
	}

	// 反向卡：back 当问题、front 当答案。
	front, _ := basicBothType{}.Render(cards[1], SideFront)
	if front.Body != "A" {
		t.Errorf("reverse front = %q, want A", front.Body)
	}
	back, _ := basicBothType{}.Render(cards[1], SideBack)
	if back.Body != "Q" {
		t.Errorf("reverse back = %q, want Q", back.Body)
	}
}

func TestListValidateAndRender(t *testing.T) {
	fields := map[string]any{
		"prompt": "Name the stages.",
		"items":  []any{"evaporation", "condensation"},
	}
	if err := Validate("list", fields); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	cards, _ := Cards(Note{Kind: "list", Fields: fields})
	if len(cards) != 1 || cards[0].Template != "forward" || cards[0].Ordinal != 0 {
		t.Fatalf("cards = %+v, want one forward card", cards)
	}

	front, _ := listType{}.Render(cards[0], SideFront)
	if front.Body != "Name the stages." || len(front.Extra) != 0 {
		t.Errorf("front = %+v, want prompt only", front)
	}
	back, _ := listType{}.Render(cards[0], SideBack)
	if back.Body != "Name the stages." || len(back.Extra) != 2 {
		t.Fatalf("back = %+v, want prompt plus two items", back)
	}
	if back.Extra[0] != "evaporation" || back.Extra[1] != "condensation" {
		t.Errorf("back.Extra = %v", back.Extra)
	}
}

func TestListValidateErrors(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"missing prompt", map[string]any{"items": []any{"a"}}, `missing required field "prompt"`},
		{"missing items", map[string]any{"prompt": "p"}, `missing required field "items"`},
		{"empty items", map[string]any{"prompt": "p", "items": []any{}}, `field "items" must not be empty`},
		{"non-string item", map[string]any{"prompt": "p", "items": []any{"a", 2}}, `field "items"[1] must be a string`},
		{"ordered wrong type", map[string]any{"prompt": "p", "items": []any{"a"}, "ordered": "yes"}, `field "ordered" must be a boolean`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate("list", tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// TestListOrderedOptional 断言 ordered 可省略（缺省 false）也可显式给出。
func TestListOrderedOptional(t *testing.T) {
	if err := Validate("list", map[string]any{"prompt": "p", "items": []any{"a"}}); err != nil {
		t.Fatalf("ordered omitted: %v", err)
	}
	if err := Validate("list", map[string]any{"prompt": "p", "items": []any{"a"}, "ordered": true}); err != nil {
		t.Fatalf("ordered true: %v", err)
	}
}
