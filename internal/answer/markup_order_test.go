package answer

import (
	"strings"
	"testing"
)

// TestChoiceFrontFollowsOptionOrder pins that the input markup is drawn in the display order the learner saw while
// every input value stays the stored option index, which is the value the frame posts back.
func TestChoiceFrontFollowsOptionOrder(t *testing.T) {
	fields := map[string]string{"options": "alpha<br>beta<br>gamma", "answer": "0"}
	bindings, err := ResolveBindings(specFor("choice_single", fields), fields)
	if err != nil {
		t.Fatalf("ResolveBindings: %v", err)
	}
	control, _ := Lookup("choice_single")
	markup, err := control.RenderFront(RenderContext{Bindings: bindings, OptionOrder: []int{2, 0, 1}})
	if err != nil {
		t.Fatalf("RenderFront: %v", err)
	}
	gamma, alpha, beta := strings.Index(markup, "gamma"), strings.Index(markup, "alpha"), strings.Index(markup, "beta")
	if gamma < 0 || alpha < 0 || beta < 0 || !(gamma < alpha && alpha < beta) {
		t.Errorf("RenderFront does not follow the display order: %s", markup)
	}
	if !strings.Contains(markup, `value="2"`) || !strings.Contains(markup, `value="1"`) {
		t.Errorf("RenderFront lost the stored option values: %s", markup)
	}
}

// TestChoiceFrontKeepsOptionMarkup pins that the label is the stored option
// HTML, not the parsed plain text: rich content reaches the frame intact while
// the parsed line stays the grading form and the display fragments stay aligned
// with it index for index.
func TestChoiceFrontKeepsOptionMarkup(t *testing.T) {
	fields := map[string]string{
		"options": `<b>alpha</b><br>\(x^2\) &amp; more<br><img src="option.png"> picture`,
		"answer":  "0",
	}
	bindings, err := ResolveBindings(specFor("choice_single", fields), fields)
	if err != nil {
		t.Fatalf("ResolveBindings: %v", err)
	}
	lines, err := bindings.RequiredLines("options")
	if err != nil {
		t.Fatalf("RequiredLines: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("parsed lines = %q, want 3", lines)
	}
	fragments := optionFragments(bindings["options"], len(lines))
	if len(fragments) != len(lines) {
		t.Fatalf("fragments = %q, want one per parsed line", fragments)
	}
	for i, line := range lines {
		if got := ParseText(fragments[i]); got != line {
			t.Errorf("fragment %d parses to %q, want the parsed line %q", i, got, line)
		}
	}

	control, _ := Lookup("choice_single")
	markup, err := control.RenderFront(RenderContext{Bindings: bindings})
	if err != nil {
		t.Fatalf("RenderFront: %v", err)
	}
	for _, want := range []string{`<b>alpha</b>`, `\(x^2\) &amp; more`, `<img src="option.png"> picture`} {
		if !strings.Contains(markup, want) {
			t.Errorf("RenderFront = %q, want the stored option markup %q", markup, want)
		}
	}
	if strings.Contains(markup, "&lt;b&gt;") || strings.Contains(markup, `\(x^2\) &amp;amp;`) {
		t.Errorf("RenderFront escaped the option markup: %s", markup)
	}

	// Grading still compares the plain text of the option, not its markup.
	result, err := control.Grade(GradeContext{Bindings: bindings, Answer: []byte(`0`)})
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if result.Score != 1 || result.Given != "alpha" {
		t.Errorf("Grade = score %v, given %q; want 1 and the plain option text", result.Score, result.Given)
	}
}
