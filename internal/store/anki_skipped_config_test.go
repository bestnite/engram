package store

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"

	"git.nite07.com/nite/engram/internal/anki"
	"google.golang.org/protobuf/encoding/protowire"
)

func skippedConfigArchive(t *testing.T, topBad, otherBad, single bool) []byte {
	t.Helper()
	raw, err := os.ReadFile(ankiFixturePath("stock.anki21b.apkg"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := anki.ReadWithLimits(bytes.NewReader(raw), int64(len(raw)), DefaultPackageImportLimits().ankiLimits())
	if err != nil {
		t.Fatal(err)
	}
	plan := planAnkiDecks(pkg)
	var configID int64
	for _, deck := range pkg.Decks {
		if ankiDeckName(deck.Name) == plan.TopName {
			configID = deck.ConfigID
			break
		}
	}
	var config []byte
	addFloat := func(n protowire.Number, f float32) {
		config = protowire.AppendTag(config, n, protowire.Fixed32Type)
		config = protowire.AppendFixed32(config, math.Float32bits(f))
	}
	addFloat(1, 3)
	addFloat(1, 12)
	addFloat(2, 7)
	addFloat(37, .75)
	config = protowire.AppendTag(config, 16, protowire.VarintType)
	config = protowire.AppendVarint(config, 1234)
	for _, w := range packageTestWeights() {
		addFloat(6, float32(w))
	}
	bad := []byte{0x32, 1, 0}
	statements := []fixtureSQL{{query: `DELETE FROM deck_config`}}
	if !single {
		data := config
		if topBad {
			data = bad
		}
		statements = append(statements, fixtureSQL{`INSERT INTO deck_config (id, name, mtime_secs, usn, config) VALUES (?, ?, 0, 0, ?)`, []any{configID, "Top", data}})
	}
	data := config
	if !single {
		data = append([]byte(nil), config...)
		data = protowire.AppendTag(data, 16, protowire.VarintType)
		data = protowire.AppendVarint(data, 4321)
	}
	if otherBad {
		data = bad
	}
	statements = append(statements, fixtureSQL{`INSERT INTO deck_config (id, name, mtime_secs, usn, config) VALUES (?, ?, 0, 0, ?)`, []any{configID + 10000, "Other", data}})
	return mutateAnkiFixture(t, "stock.anki21b.apkg", statements...)
}

func TestAnkiImportSkippedDeckConfigs(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "skipped_config")[0]
			for _, tc := range []struct {
				name                               string
				topBad, otherBad, single, defaults bool
			}{
				{"top invalid", true, false, false, true},
				{"other invalid", false, true, false, false},
				{"both valid", false, false, false, false},
				{"single different id", false, false, true, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					raw := skippedConfigArchive(t, tc.topBad, tc.otherBad, tc.single)
					report, err := NewDeckStore(db).ImportPackage(context.Background(), owner, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck, ApplyWeights: true, MediaRoot: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					if report.DeckConfigSkipped != tc.topBad {
						t.Fatalf("DeckConfigSkipped = %v, want %v", report.DeckConfigSkipped, tc.topBad)
					}
					got := deckPreset(t, db, report.DeckID)
					if tc.defaults {
						want := NewPreset(owner, got.Name)
						got.ID, got.PublicID, got.CreatedAt, got.UpdatedAt = want.ID, want.PublicID, want.CreatedAt, want.UpdatedAt
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("preset = %+v, defaults = %+v", got, want)
						}
						if report.WeightsApplied {
							t.Fatal("unexpected weights")
						}
						return
					}
					if got.LearningSteps != "3m,12m" || got.RelearningSteps != "7m" || got.MaximumIntervalDays != 1234 || got.DesiredRetention != .75 || got.WeightsJSON == nil || !report.WeightsApplied {
						t.Fatalf("preset = %+v, report = %+v", got, report)
					}
					var weights []float64
					if err := json.Unmarshal([]byte(*got.WeightsJSON), &weights); err != nil {
						t.Fatal(err)
					}
					want := packageTestWeights()
					for i := range want {
						want[i] = float64(float32(want[i]))
					}
					if !reflect.DeepEqual(weights, want) {
						t.Fatalf("weights = %v, want %v", weights, want)
					}
				})
			}
		})
	}
}
