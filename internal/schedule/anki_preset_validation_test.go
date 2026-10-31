package schedule

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
	"gorm.io/gorm"
)

func TestImportedAnkiPresetSchedulerLimits(t *testing.T) {
	dbs := map[string]*gorm.DB{"sqlite": newTestDB(t)}
	if db, ok := pgtest.Open(t); ok {
		if err := db.AutoMigrate(store.AllModels()...); err != nil {
			t.Fatal(err)
		}
		dbs["postgres"] = db
	}
	weights := []float64{0.5, 1.5, 2.5, 3.5, 4.5, 3.5, 3.5, 0.05, 1.0, 0.2, 0.8, 1.5, 0.06, 0.3, 1.6, 0.6, 1.9, 0.5, 0.1, 0.07, 0.15}
	for driver, db := range dbs {
		t.Run(driver, func(t *testing.T) {
			user := store.User{Username: "limits", Email: "limits@example.com", DisplayName: "limits", Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name      string
				max       int
				steps     []float64
				wantSteps string
				wantMax   int
			}{
				{"maximum", 50000, []float64{2, 20}, "2m,20m", 36500},
				{"negative", 1000, []float64{-1}, store.DefaultLearningSteps, 1000},
			} {
				t.Run(tc.name, func(t *testing.T) {
					pkg := anki.WritePackage{Collection: anki.WriteCollection{
						CreatedAt:   1600000000,
						NoteTypes:   []anki.WriteNoteType{{ID: 1000, Name: "Basic", Fields: []anki.Field{{Ord: 0, Name: "Front"}, {Ord: 1, Name: "Back"}}, Templates: []anki.Template{{Ord: 0, Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Back}}"}}}},
						Decks:       []anki.WriteDeck{{ID: 1, Name: "Deck", ConfigID: 2}},
						DeckConfigs: []anki.WriteDeckConfig{{ID: 2, Name: "Custom", DesiredRetention: .85, LearningSteps: tc.steps, RelearningSteps: []float64{15}, MaximumIntervalDays: tc.max, FSRSWeights: weights}},
						Notes:       []anki.WriteNote{{ID: 1, GUID: "guid", NoteTypeID: 1000, Fields: []string{"front", "back"}}},
						Cards:       []anki.WriteCard{{ID: 1, NoteID: 1, DeckID: 1}},
					}}
					var archive bytes.Buffer
					if err := anki.WriteArchive(&archive, &pkg); err != nil {
						t.Fatal(err)
					}
					report, err := store.NewDeckStore(db).ImportPackage(context.Background(), user.ID, &archive, store.PackageImportOptions{Target: store.PackageTargetNewDeck, ApplyWeights: true, MediaRoot: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					if !report.WeightsApplied {
						t.Fatalf("weights not applied: %+v", report)
					}
					var deck store.Deck
					if err := db.First(&deck, report.DeckID).Error; err != nil {
						t.Fatal(err)
					}
					var p store.Preset
					if err := db.First(&p, deck.PresetID).Error; err != nil {
						t.Fatal(err)
					}
					if p.LearningSteps != tc.wantSteps || p.RelearningSteps != "15m" || p.MaximumIntervalDays != tc.wantMax || p.DesiredRetention != .85 || p.WeightsJSON == nil {
						t.Fatalf("preset: %+v", p)
					}
					want, err := Parameters(&p)
					if err != nil {
						t.Fatal(err)
					}
					s, err := NewScheduler(&p)
					if err != nil {
						t.Fatal(err)
					}
					got := s.fsrs.Parameters
					if got.W != want.W || got.RequestRetention != want.RequestRetention || got.MaximumInterval != want.MaximumInterval || !reflect.DeepEqual(got.LearningSteps, want.LearningSteps) || !reflect.DeepEqual(got.RelearningSteps, want.RelearningSteps) {
						t.Fatalf("scheduler parameters: got %+v want %+v", got, want)
					}
					if got.W[0] != weights[0] {
						t.Fatalf("weights reset: %v", got.W)
					}
				})
			}
		})
	}
}
