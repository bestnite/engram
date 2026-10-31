package schedule

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
	"gorm.io/gorm"
)

func TestImportedAnkiMemoryPreview(t *testing.T) {
	dbs := map[string]*gorm.DB{"sqlite": newTestDB(t)}
	if db, ok := pgtest.Open(t); ok {
		if err := db.AutoMigrate(store.AllModels()...); err != nil {
			t.Fatal(err)
		}
		dbs["postgres"] = db
	}
	for driver, db := range dbs {
		t.Run(driver, func(t *testing.T) {
			user := store.User{Username: "preview", Email: "preview@example.com", DisplayName: "preview", Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			for i, tc := range []struct{ s, d float64 }{{0, 5}, {5, 0}, {-1, 5}, {5, 11}, {5, 5}} {
				for _, history := range []bool{false, true} {
					t.Run(fmt.Sprintf("case_%d/history_%t", i, history), func(t *testing.T) {
						pkg := anki.WritePackage{Collection: anki.WriteCollection{
							CreatedAt:   1600000000,
							NoteTypes:   []anki.WriteNoteType{{ID: 1000, Name: "Basic", Fields: []anki.Field{{Ord: 0, Name: "Front"}, {Ord: 1, Name: "Back"}}, Templates: []anki.Template{{Ord: 0, Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Back}}"}}}},
							Decks:       []anki.WriteDeck{{ID: 1, Name: "Deck", ConfigID: 2}},
							DeckConfigs: []anki.WriteDeckConfig{{ID: 2, Name: "Default", DesiredRetention: .9, LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500}},
							Notes:       []anki.WriteNote{{ID: 1, GUID: "guid", NoteTypeID: 1000, Fields: []string{"front", "back"}}},
							Cards:       []anki.WriteCard{{ID: 1, NoteID: 1, DeckID: 1, Type: 2, Queue: 2, Due: 5, Ivl: 7, Stability: &tc.s, Difficulty: &tc.d}},
						}}
						if history {
							pkg.Collection.Reviews = []anki.WriteReview{{ID: 1600000000000, CardID: 1, Ease: 3, Type: 0}, {ID: 1600086400000, CardID: 1, Ease: 4, Type: 1}}
						}
						var archive bytes.Buffer
						if err := anki.WriteArchive(&archive, &pkg); err != nil {
							t.Fatal(err)
						}
						report, err := store.NewDeckStore(db).ImportPackage(context.Background(), user.ID, &archive, store.PackageImportOptions{Target: store.PackageTargetNewDeck, IncludeProgress: true, MediaRoot: t.TempDir()})
						if err != nil {
							t.Fatal(err)
						}
						var states []store.CardState
						if err := db.Joins("JOIN cards ON cards.id = card_states.card_id").Joins("JOIN notes ON notes.id = cards.note_id").Where("notes.deck_id = ? AND card_states.user_id = ?", report.DeckID, user.ID).Find(&states).Error; err != nil {
							t.Fatal(err)
						}
						want := 1
						if i < 4 && !history {
							want = 0
						}
						if len(states) != want {
							t.Fatalf("states=%+v report=%+v", states, report)
						}
						for j := range states {
							if _, err := mustScheduler(t, testPreset(t)).Preview(&states[j], time.Unix(1700000000, 0)); err != nil {
								t.Fatalf("Preview: %v", err)
							}
						}
					})
				}
			}
		})
	}
}
