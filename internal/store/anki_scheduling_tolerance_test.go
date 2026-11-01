package store

import (
	"bytes"
	"context"
	"encoding/json"
	"git.nite07.com/nite/engram/internal/anki"
	"log/slog"
	"testing"
)

func schedulingToleranceArchive(t *testing.T, modern bool, query string) []byte {
	t.Helper()
	fixture := "stock.anki21.apkg"
	if modern {
		fixture = "stock.anki21b.apkg"
	}
	return mutateAnkiFixture(t, fixture,
		fixtureSQL{query: `INSERT INTO revlog (id, cid, usn, ease, ivl, lastIvl, factor, time, type) SELECT 1, id, 0, 3, 1, 1, 2500, 100, 1 FROM cards LIMIT 1`},
		fixtureSQL{query: query},
	)
}

func TestAnkiImportUnreadableReviewsDiscarded(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range []struct {
				name, query string
			}{
				{"real ease", `UPDATE revlog SET ease = 2.5`},
				{"valid ease", `UPDATE revlog SET ease = 3`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					raw := schedulingToleranceArchive(t, false, tc.query)
					owner := seedUsers(t, db, "discarded_"+tc.name)[0]
					report, err := NewDeckStore(db).ImportPackage(context.Background(), owner, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck, IncludeProgress: false, MediaRoot: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					if report.ReviewsDiscarded != 1 || !report.ProgressDiscarded {
						t.Fatalf("reviews discarded = %d, progress discarded = %v; want 1, true", report.ReviewsDiscarded, report.ProgressDiscarded)
					}
				})
			}
		})
	}
}

func TestAnkiImportSchedulingTolerance(t *testing.T) {
	tests := []struct {
		name, query string
		modern      bool
	}{
		{"real revlog interval", `UPDATE revlog SET ivl = 7.5`, false},
		{"real card due", `UPDATE cards SET due = 7.5`, false},
		{"real card factor", `UPDATE cards SET factor = 2500.5`, false},
		{"invalid dconf", `UPDATE col SET dconf = 'invalid'`, false},
		{"invalid deck config", `UPDATE deck_config SET config = x'320100'`, true},
	}
	golden := loadAnkiGolden(t, "stock")
	wantCards := 0
	for _, note := range golden.Notes {
		wantCards += len(note.Cards)
	}
	if len(golden.Notes) == 0 || wantCards == 0 {
		t.Fatal("golden has no content")
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					raw := schedulingToleranceArchive(t, tc.modern, tc.query)
					owner := seedUsers(t, db, "tolerance_"+tc.name)[0]
					report, err := NewDeckStore(db).ImportPackage(context.Background(), owner, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck, IncludeProgress: false, MediaRoot: t.TempDir()})
					if err != nil {
						t.Fatalf("ImportPackage: %v", err)
					}
					if report.NotesCreated != len(golden.Notes) || report.CardsCreated != wantCards {
						t.Fatalf("report: notes = %d, cards = %d; want %d, %d", report.NotesCreated, report.CardsCreated, len(golden.Notes), wantCards)
					}
					var notes, cards int64
					if err := db.Model(&Note{}).Where("deck_id = ?", report.DeckID).Count(&notes).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Model(&Card{}).Where("note_id IN (?)", db.Model(&Note{}).Select("id").Where("deck_id = ?", report.DeckID)).Count(&cards).Error; err != nil {
						t.Fatal(err)
					}
					if notes != int64(len(golden.Notes)) || cards != int64(wantCards) {
						t.Fatalf("stored: notes = %d, cards = %d; want %d, %d", notes, cards, len(golden.Notes), wantCards)
					}
				})
			}
		})
	}
}

func TestAnkiImportProgressDiscardedForSkippedCard(t *testing.T) {
	pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
	if err != nil {
		t.Fatal(err)
	}
	archive := mutateAnkiFixture(t, "stock.anki2.apkg",
		fixtureSQL{query: "UPDATE cards SET queue = 1.5, type = 2 WHERE id = (SELECT MIN(id) FROM cards)"},
		fixtureSQL{query: "DELETE FROM revlog"},
		fixtureSQL{query: "INSERT INTO revlog (id, cid, usn, ease, ivl, lastIvl, factor, time, type) SELECT 1600000000000, MIN(id), 0, 3, 1, 0, 2500, 100, 1 FROM cards"},
	)
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				progress bool
			}{{"progress_on", true}, {"progress_off", false}} {
				t.Run(tc.name, func(t *testing.T) {
					user := seedUsers(t, db, "skipped_card_"+driver+"_"+tc.name)[0]
					var logs bytes.Buffer
					previous := slog.Default()
					slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
					defer slog.SetDefault(previous)
					report, err := NewDeckStore(db).ImportPackage(context.Background(), user, bytes.NewReader(archive), PackageImportOptions{Target: PackageTargetNewDeck, IncludeProgress: tc.progress, MediaRoot: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					if report.CardsCreated != len(pkg.Cards)-1 {
						t.Fatalf("cards_created = %d, want %d", report.CardsCreated, len(pkg.Cards)-1)
					}
					if report.CardsSkipped != 1 {
						t.Fatalf("cards_skipped = %d", report.CardsSkipped)
					}
					if report.ReviewsDiscarded != 1 || report.ReviewsSkipped != 0 || report.ReviewsApplied != 0 {
						t.Fatalf("report = %+v", report)
					}
					if !report.ProgressDiscarded {
						t.Fatalf("progress_discarded = false, want true: %+v", report)
					}
					var count int64
					if err := db.Model(&Review{}).Where("user_id = ?", user).Count(&count).Error; err != nil || count != 0 {
						t.Fatalf("reviews = %d, error = %v", count, err)
					}
					if tc.progress {
						var logEntry struct {
							Level   string `json:"level"`
							Message string `json:"msg"`
							Reviews int    `json:"reviews_discarded"`
							UserID  uint64 `json:"user_id"`
						}
						if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &logEntry); err != nil || logEntry.Level != "WARN" || logEntry.Message != "Anki import discarded reviews for skipped cards" || logEntry.Reviews != 1 || logEntry.UserID != user {
							t.Fatalf("warn log = %q, error = %v", logs.String(), err)
						}
						t.Logf("warn: %s", bytes.TrimSpace(logs.Bytes()))
					}
					encoded, err := json.Marshal(report)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(encoded, []byte(`"cards_skipped":1`)) {
						t.Fatalf("report = %s", encoded)
					}
				})
			}
		})
	}
}
