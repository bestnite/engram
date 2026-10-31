package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

func TestAnkiImportWeightsSchedule(t *testing.T) {
	defaults := fsrs.DefaultWeights()
	valid := append([]float64(nil), defaults[:]...)
	valid[0] += 0.1
	cases := []struct {
		name               string
		weights            []float64
		apply              bool
		target             string
		applied, discarded bool
	}{
		{"option off", valid, false, store.PackageTargetNewDeck, false, true},
		{"valid", valid, true, store.PackageTargetNewDeck, true, false},
		{"invalid", []float64{1, 2, 3}, true, store.PackageTargetNewDeck, false, true},
		{"no weights", nil, true, store.PackageTargetNewDeck, false, false},
		{"existing deck", valid, true, "into_deck", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pkg := anki.WritePackage{Collection: anki.WriteCollection{
				CreatedAt:   1700000000,
				NoteTypes:   []anki.WriteNoteType{{ID: 10, Name: "Basic", Fields: []anki.Field{{Name: "Front"}, {Ord: 1, Name: "Back"}}, Templates: []anki.Template{{Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Back}}"}}}},
				Decks:       []anki.WriteDeck{{ID: 20, Name: "Weights", ConfigID: 30}},
				DeckConfigs: []anki.WriteDeckConfig{{ID: 30, Name: "Imported", DesiredRetention: .85, LearningSteps: []float64{2, 12}, RelearningSteps: []float64{3}, MaximumIntervalDays: 123, FSRSWeights: tc.weights}},
				Notes:       []anki.WriteNote{{ID: 40, GUID: "test-guid", NoteTypeID: 10, Fields: []string{"question", "answer"}}},
				Cards:       []anki.WriteCard{{ID: 50, NoteID: 40, DeckID: 20}},
			}}
			var raw bytes.Buffer
			if err := anki.WriteArchive(&raw, &pkg); err != nil {
				t.Fatal(err)
			}
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, "anki_weights", store.RoleUser)
			opts := store.PackageImportOptions{Target: tc.target, ApplyWeights: tc.apply, IncludeProgress: true}
			if tc.target != store.PackageTargetNewDeck {
				deck := seedDeck(t, env.db, user.ID)
				opts.Target = fmt.Sprintf("into_deck:%d", deck.ID)
			}
			report, err := store.NewDeckStore(env.db).ImportPackage(t.Context(), user.ID, bytes.NewReader(raw.Bytes()), opts)
			if err != nil {
				t.Fatal(err)
			}
			if report.WeightsApplied != tc.applied || report.WeightsDiscarded != tc.discarded {
				t.Fatalf("report: %+v", report)
			}
			var deck store.Deck
			if err := env.db.First(&deck, report.DeckID).Error; err != nil {
				t.Fatal(err)
			}
			var preset store.Preset
			if err := env.db.First(&preset, deck.PresetID).Error; err != nil {
				t.Fatal(err)
			}
			if (preset.WeightsJSON != nil) != tc.applied {
				t.Fatalf("weights: %v", preset.WeightsJSON)
			}
			if tc.applied {
				var got []float64
				if err := json.Unmarshal([]byte(*preset.WeightsJSON), &got); err != nil || !reflect.DeepEqual(got, tc.weights) {
					t.Fatalf("weights: %v, %v", got, err)
				}
			}
			if tc.target == store.PackageTargetNewDeck && (preset.DesiredRetention != .85 || preset.LearningSteps != "2m,12m" || preset.RelearningSteps != "3m" || preset.MaximumIntervalDays != 123) {
				t.Fatalf("preset: %+v", preset)
			}
			var states []store.CardState
			if err := env.db.Where("user_id = ?", user.ID).Find(&states).Error; err != nil {
				t.Fatal(err)
			}
			if len(states) == 0 {
				t.Fatal("no imported card states")
			}
			for i := range states {
				scheduler, err := schedule.NewScheduler(&preset)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := scheduler.Preview(&states[i], time.Unix(1700000000, 0)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
