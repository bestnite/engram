package cardtype

import "testing"

// TestOptionCount pins the one reader of an option field's size: the display order and the answer
// controls both need the count, so it must report the stored entries for either array shape and
// report zero for anything that is not an array.
func TestOptionCount(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		want   int
	}{
		{name: "decoded json array", fields: map[string]any{"options": []any{"a", "b", "c"}}, want: 3},
		{name: "go string slice", fields: map[string]any{"options": []string{"a", "b"}}, want: 2},
		{name: "empty array", fields: map[string]any{"options": []any{}}, want: 0},
		{name: "missing field", fields: map[string]any{}, want: 0},
		{name: "null field", fields: map[string]any{"options": nil}, want: 0},
		{name: "not an array", fields: map[string]any{"options": "a, b"}, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OptionCount(tc.fields, "options"); got != tc.want {
				t.Errorf("OptionCount(%v) = %d, want %d", tc.fields, got, tc.want)
			}
		})
	}
}
