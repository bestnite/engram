package web

import (
	"encoding/json"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/notetype"
)

// TestFrontFaceHTMLFrameRequestChecksTheControl pins the two front paths: a frame request replaces the type marker
// with the answer control's input markup in the server-provided display order, and every other request strips the
// marker because the transitional SPA draws its own control.
func TestFrontFaceHTMLFrameRequestChecksTheControl(t *testing.T) {
	item := &cardFace{
		front: `<p>pick one</p>[[type:answer]]`,
		spec: &notetype.AnswerSpec{
			Control:  "choice_single",
			Bindings: map[string]string{"options": "options", "answer": "answer"},
		},
		values: map[string]string{"options": "ALPHA<br>BETA", "answer": "1"},
	}

	frame := frontFaceHTML(item, []int{1, 0}, true)
	if strings.Contains(frame, "[[type:answer]]") {
		t.Errorf("frame front kept the type marker: %s", frame)
	}
	if !strings.Contains(frame, `data-control="choice_single"`) {
		t.Errorf("frame front has no answer control: %s", frame)
	}
	if alpha, beta := strings.Index(frame, "ALPHA"), strings.Index(frame, "BETA"); alpha < beta {
		t.Errorf("frame front ignores the display order: %s", frame)
	}
	// The value stays the stored option index even though BETA is displayed first.
	if !strings.Contains(frame, `value="1"`) || !strings.Contains(frame, `value="0"`) {
		t.Errorf("frame front lost the stored option values: %s", frame)
	}

	plain := frontFaceHTML(item, []int{1, 0}, false)
	if strings.Contains(plain, "[[type:answer]]") || strings.Contains(plain, "data-control") {
		t.Errorf("non-frame front = %q, want the marker stripped and no control markup", plain)
	}
}

// TestFrontFaceHTMLChoiceKeepsOptionMarkup covers both choice controls end to end at the handler seam: the frame front
// carries the stored option HTML (so TeX delimiters and inline tags reach the sandboxed frame), and grading still
// compares the parsed plain text of the option.
func TestFrontFaceHTMLChoiceKeepsOptionMarkup(t *testing.T) {
	options := `<b>bold</b><br>\(x^2\) &amp; more<br><img src="option.png"> picture`
	fields := map[string]string{"options": options, "answer": "0", "answers": "0"}
	for _, control := range []string{"choice_single", "choice_multi"} {
		t.Run(control, func(t *testing.T) {
			bindings := map[string]string{"options": "options"}
			if control == "choice_single" {
				bindings["answer"] = "answer"
			} else {
				bindings["answers"] = "answers"
			}
			item := &cardFace{
				front:  `<p>pick one</p>[[type:options]]`,
				spec:   &notetype.AnswerSpec{Control: control, Bindings: bindings},
				values: fields,
			}
			frame := frontFaceHTML(item, nil, true)
			for _, want := range []string{`<b>bold</b>`, `\(x^2\) &amp; more`, `<img src="option.png"> picture`} {
				if !strings.Contains(frame, want) {
					t.Errorf("%s front = %q, want the raw option markup %q", control, frame, want)
				}
			}
			if strings.Contains(frame, "&lt;b&gt;") || strings.Contains(frame, "&lt;img") {
				t.Errorf("%s front escaped the option markup: %s", control, frame)
			}

			parsed, err := answer.ResolveBindings(*item.spec, item.values)
			if err != nil {
				t.Fatalf("ResolveBindings: %v", err)
			}
			gradeControl, ok := answer.Lookup(control)
			if !ok {
				t.Fatalf("answer control %q is not registered", control)
			}
			gradeAnswer := json.RawMessage(`0`)
			if control == "choice_multi" {
				gradeAnswer = json.RawMessage(`[0]`)
			}
			result, err := gradeControl.Grade(answer.GradeContext{Bindings: parsed, Answer: gradeAnswer})
			if err != nil {
				t.Fatalf("Grade: %v", err)
			}
			if result.Score != 1 || result.Given != "bold" {
				t.Errorf("%s Grade = score %v, given %q; want 1 and the plain option text", control, result.Score, result.Given)
			}
		})
	}
}
