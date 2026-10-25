package cardtype

import (
	"strings"
	"testing"
)

// TestChoiceMultiBackFollowsOptionOrder pins the one place a display order reaches the card type: the back lists the
// correct options in the order the answer controls showed them, while the front keeps stored order so its Options stay
// parallel with the stored options slice.
func TestChoiceMultiBackFollowsOptionOrder(t *testing.T) {
	fields := map[string]any{
		"question": "pick-multi",
		"options":  []any{"alpha", "beta", "gamma", "delta"},
		"answers":  []any{0, 2},
	}
	// Display slots: 0=gamma, 1=alpha, 2=delta, 3=beta; the correct options are alpha and gamma, so the back reads gamma first.
	order := []int{2, 0, 3, 1}

	t.Run("with display order", func(t *testing.T) {
		card := Card{Template: "forward", Fields: fields, OptionOrder: order}
		front, err := choiceMultiType{}.Render(card, SideFront)
		if err != nil {
			t.Fatalf("front render: %v", err)
		}
		wantFront := []string{"alpha", "beta", "gamma", "delta"}
		if len(front.Options) != len(wantFront) {
			t.Fatalf("front Options = %v, want stored order %v", front.Options, wantFront)
		}
		for i := range wantFront {
			if front.Options[i] != wantFront[i] {
				t.Fatalf("front Options = %v, want stored order %v", front.Options, wantFront)
			}
		}
		back, err := choiceMultiType{}.Render(card, SideBack)
		if err != nil {
			t.Fatalf("back render: %v", err)
		}
		if back.Body != "gamma, alpha" {
			t.Errorf("back = %q, want %q (correct options in display order)", back.Body, "gamma, alpha")
		}
	})

	t.Run("without display order", func(t *testing.T) {
		back, err := choiceMultiType{}.Render(Card{Template: "forward", Fields: fields}, SideBack)
		if err != nil {
			t.Fatalf("back render: %v", err)
		}
		if back.Body != "alpha, gamma" {
			t.Errorf("back = %q, want %q (stored order unchanged)", back.Body, "alpha, gamma")
		}
	})

	t.Run("order that does not fit the options", func(t *testing.T) {
		back, err := choiceMultiType{}.Render(Card{Template: "forward", Fields: fields, OptionOrder: []int{0, 1}}, SideBack)
		if err != nil {
			t.Fatalf("back render: %v", err)
		}
		if back.Body != "alpha, gamma" {
			t.Errorf("back = %q, want %q (fall back to stored order)", back.Body, "alpha, gamma")
		}
	})
}

// TestChoiceSingleIgnoresOptionOrder records that a single-choice card needs no display-order handling on the back:
// its back is the correct option's own text, and the front is the stored option list.
func TestChoiceSingleIgnoresOptionOrder(t *testing.T) {
	fields := map[string]any{
		"question": "pick-single",
		"options":  []any{"alpha", "beta", "gamma"},
		"answer":   2,
	}
	card := Card{Template: "forward", Fields: fields, OptionOrder: []int{2, 0, 1}}
	front, err := choiceSingleType{}.Render(card, SideFront)
	if err != nil {
		t.Fatalf("front render: %v", err)
	}
	if strings.Join(front.Options, ",") != "alpha,beta,gamma" {
		t.Errorf("front Options = %v, want stored order", front.Options)
	}
	back, err := choiceSingleType{}.Render(card, SideBack)
	if err != nil {
		t.Fatalf("back render: %v", err)
	}
	if back.Body != "gamma" {
		t.Errorf("back = %q, want the correct option %q", back.Body, "gamma")
	}
}
