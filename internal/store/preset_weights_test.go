package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/open-spaced-repetition/go-fsrs/v4"
)

func TestPresetWeights(t *testing.T) {
	defaults := fsrs.DefaultWeights()
	encoded := func(n int) string {
		b, err := json.Marshal(defaults[:n])
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	tests := []struct {
		name string
		raw  *string
		n    int
		err  string
	}{
		{name: "NULL"},
		{name: "empty", raw: Ptr("")},
		{name: "whitespace", raw: Ptr(" \t ")},
		{name: "null", raw: Ptr(" null ")},
		{name: "21 weights", raw: Ptr(encoded(21)), n: 21},
		{name: "17 weights", raw: Ptr(encoded(17)), n: 17},
		{name: "19 weights", raw: Ptr(encoded(19)), n: 19},
		{name: "invalid length", raw: Ptr("[1,2,3]"), err: "migrate preset weights"},
		{name: "invalid JSON", raw: Ptr("not json"), err: "decode preset weights"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PresetWeights(tc.raw)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := defaults
			if tc.n != 0 {
				want, err = fsrs.MigrateWeights(defaults[:tc.n])
				if err != nil {
					t.Fatal(err)
				}
			}
			if got != want {
				t.Errorf("weights = %v, want %v", got, want)
			}
		})
	}
}
