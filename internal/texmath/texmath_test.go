package texmath

import (
	"reflect"
	"testing"
)

// TestRanges 断言行内与块级公式都被识别，区间包含分隔符，公式里的反斜杠命令不会提前收束。
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

// TestContains 断言只有完整落在公式内的区间才算在公式里。
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
