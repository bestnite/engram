package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"git.nite07.com/nite/engram/internal/apkg"
)

func TestAnkiProgressPackageApproximationWarning(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name        string
			reviews     []apkg.WriteReview
			carried     bool
			wantWarning bool
		}{
			{name: "partial", reviews: []apkg.WriteReview{{ID: 1600000000000, CardID: 50, Ease: 3, Type: 1, Ivl: 7, Factor: 2500}}},
			{name: "no_selected_history", reviews: []apkg.WriteReview{{ID: 1600000000000, CardID: 50, Ease: 0, Type: 4, Factor: 0}}, wantWarning: true},
			{name: "no_history_carried", carried: true},
			{name: "complete", reviews: []apkg.WriteReview{{ID: 1600000000000, CardID: 50, Ease: 3, Type: 0}, {ID: 1600086400000, CardID: 50, Ease: 4, Type: 1}}},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				user := seedUsers(t, db, "warning_"+driver+"_"+tc.name)[0]
				card := apkg.WriteCard{ID: 50, NoteID: 40, DeckID: 20, Type: 2, Queue: 2, Ivl: 7, Factor: 2500, Reps: 2}
				if tc.carried {
					card.Stability, card.Difficulty, card.LastReview = Ptr(4.5), Ptr(6.2), Ptr(int64(1600000000))
				}
				pkg := apkg.WritePackage{Collection: apkg.WriteCollection{
					CreatedAt:   1600000000,
					NoteTypes:   []apkg.WriteNoteType{{ID: 10, Name: "Basic", Fields: []apkg.Field{{Name: "Front"}}, Templates: []apkg.Template{{Name: "Card", QFormat: "{{Front}}", AFormat: "{{Front}}"}}}},
					Decks:       []apkg.WriteDeck{{ID: 20, Name: "Imported", ConfigID: 30}},
					DeckConfigs: []apkg.WriteDeckConfig{{ID: 30, Name: "Default", DesiredRetention: .9, LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500}},
					Notes:       []apkg.WriteNote{{ID: 40, GUID: "guid", NoteTypeID: 10, Fields: []string{"front"}}},
					Cards:       []apkg.WriteCard{card}, Reviews: tc.reviews,
				}}
				var raw, logs bytes.Buffer
				if err := apkg.WriteArchive(&raw, &pkg); err != nil {
					t.Fatal(err)
				}
				previous := slog.Default()
				slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
				defer slog.SetDefault(previous)
				report, err := NewDeckStore(db).ImportPackage(context.Background(), user, &raw, PackageImportOptions{Target: "new_deck", IncludeProgress: true, MediaRoot: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				if report.ProgressApplied != 1 {
					t.Fatalf("report = %+v", report)
				}
				if !tc.wantWarning {
					if logs.Len() != 0 {
						t.Fatalf("unexpected log: %s", logs.String())
					}
					return
				}
				var record map[string]any
				if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
					t.Fatalf("expected one warning: %s: %v", logs.String(), err)
				}
				if record["level"] != "WARN" || record["msg"] != "Anki import memory approximation without selected history is not aligned with Anki" || fmt.Sprint(record["user_id"]) != fmt.Sprint(user) || record["cards"] != float64(1) {
					t.Fatalf("unexpected log: %v", record)
				}
				t.Logf("captured warning: %s", bytes.TrimSpace(logs.Bytes()))
			})
		}
	}
}
