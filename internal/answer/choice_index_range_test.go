package answer

import "testing"

// A stored index far past the option list must be refused as a value, not
// converted into a negative int and used to index the option slice.
func TestChoiceControlsRefuseHugeNumericIndex(t *testing.T) {
	cases := []struct {
		control string
		fields  map[string]string
	}{
		{"choice_single", map[string]string{"options": "a<br>b<br>c", "answer": "1e100"}},
		{"choice_multi", map[string]string{"options": "a<br>b<br>c", "answers": "0<br>1e100"}},
	}
	for _, test := range cases {
		t.Run(test.control, func(t *testing.T) {
			bindings, err := ResolveBindings(specFor(test.control, test.fields), test.fields)
			if err != nil {
				t.Fatalf("ResolveBindings: %v", err)
			}
			control, ok := Lookup(test.control)
			if !ok {
				t.Fatalf("control %q is not registered", test.control)
			}
			for _, render := range []struct {
				side string
				fn   func(RenderContext) (string, error)
			}{
				{"back", control.RenderBack},
				{"front", control.RenderFront},
			} {
				html, err := render.fn(RenderContext{Bindings: bindings})
				if err == nil {
					t.Fatalf("Render%s() = %q, want the out-of-range index refused", render.side, html)
				}
			}
			if _, err := runGrade(t, test.control, test.fields, `[0]`, 0); err == nil {
				t.Fatal("Grade() error = nil, want the out-of-range index refused")
			}
		})
	}
}
