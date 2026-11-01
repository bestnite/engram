package store

import (
	"context"
	"encoding/json"
	"reflect"
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
		{name: "empty array", raw: Ptr("[]"), err: "migrate preset weights"},
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

func TestAnkiWeights(t *testing.T) {
	defaults := fsrs.DefaultWeights()
	encode := func(n int) string {
		b, err := json.Marshal(defaults[:n])
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, tc := range []struct {
		name, raw string
		n         int
		invalid   bool
	}{
		{"empty", "", 0, false}, {"null", " null ", 0, false},
		{"empty array", "[]", 0, true}, {"21 weights", encode(21), 21, false},
		{"17 weights", encode(17), 17, false}, {"invalid length", "[1,2,3]", 0, true},
		{"invalid JSON", "not json", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ankiWeights(tc.raw)
			if tc.invalid {
				if err == nil || got != nil {
					t.Fatalf("ankiWeights = %v, %v; want nil and error", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.n == 0 {
				if got != nil {
					t.Fatalf("weights = %v, want nil", got)
				}
				return
			}
			want, err := fsrs.MigrateWeights(defaults[:tc.n])
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want[:]) {
				t.Errorf("weights = %v, want %v", got, want)
			}
		})
	}
}

func TestExportAnkiEmptyPresetWeights(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "anki_empty_weights")[0]
			fixture := seedAnkiExportDeck(t, db, owner)
			var deck Deck
			if err := db.First(&deck, fixture.deckID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&Preset{}).Where("id = ?", deck.PresetID).Update("weights_json", "[]").Error; err != nil {
				t.Fatal(err)
			}
			raw := "[]"
			if _, err := PresetWeights(&raw); err == nil {
				t.Fatal("PresetWeights accepted empty array")
			}
			for _, include := range []bool{true, false} {
				pkg, err := NewDeckStore(db).ExportAnkiPackage(context.Background(), owner, fixture.deckID, PackageOptions{IncludeDeckConfigs: Ptr(true), IncludeWeights: include})
				if include {
					if err == nil || len(pkg) != 0 {
						t.Fatalf("export with weights = %d bytes, %v; want error and no package", len(pkg), err)
					}
				} else if err != nil || len(pkg) == 0 {
					t.Fatalf("export without weights = %d bytes, %v", len(pkg), err)
				}
			}
		})
	}
}
