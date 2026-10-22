package texmath

import (
	"reflect"
	"testing"
)

// TestRanges asserts that both inline and block formulas are recognized, that the range
// includes the delimiters, and that a backslash command inside a formula does not end it early.
func TestRanges(t *testing.T) {
	cases := []struct {
		name string
		text string
		want [][]int
	}{
		{"no math", "plain text", nil},
		{"inline", `a \(x\) b`, [][]int{{2, 7}}},
		{"display spans lines", "\\[a\nb\\]", [][]int{{0, 7}}},
		{"command inside", `\(\dfrac{a}{b}\)`, [][]int{{0, 16}}},
		{"two formulas", `\(a\) and \(b\)`, [][]int{{0, 5}, {10, 15}}},
		{"unclosed", `\(a`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Ranges(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Ranges(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestContains asserts that only ranges lying fully inside a formula count as inside it.
func TestContains(t *testing.T) {
	ranges := [][]int{{2, 10}}
	cases := []struct {
		name       string
		start, end int
		want       bool
	}{
		{"inside", 4, 6, true},
		{"whole formula", 2, 10, true},
		{"before", 0, 2, false},
		{"straddles start", 1, 5, false},
		{"straddles end", 8, 12, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Contains(ranges, tc.start, tc.end); got != tc.want {
				t.Errorf("Contains(%d, %d) = %v, want %v", tc.start, tc.end, got, tc.want)
			}
		})
	}
	if Contains(nil, 0, 1) {
		t.Error("Contains(nil) = true, want false")
	}
}
