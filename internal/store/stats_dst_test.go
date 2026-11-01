package store

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

// TestStatsDueForecastUsesTheLocalDayBoundary pins the forecast's day boundaries to the local calendar in a zone with
// daylight saving. Each case owns a deck, so one driver's database serves the whole table: the card's due_at is the only
// thing that differs, and the expected bucket says which side of a boundary it falls on.
//
// The observation moment is noon on the eve of each change. A fixed 24-hour bucket used to put the spring change's end of
// today an hour late (09:00Z instead of 08:00Z) and the autumn change's an hour early (08:00Z instead of 09:00Z), so the
// 08:30Z cases are the reported defect and the exact-boundary cases pin the half-open rule.
func TestStatsDueForecastUsesTheLocalDayBoundary(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load New York: %v", err)
	}
	cases := []struct {
		name string
		now  time.Time
		due  time.Time
		want DueForecast
	}{
		{
			name: "spring eve: before the end of today is today",
			now:  time.Date(2026, 3, 7, 12, 0, 0, 0, newYork),
			due:  time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC),
			want: DueForecast{Today: 1},
		},
		{
			name: "spring eve: the end of today is tomorrow's first instant",
			now:  time.Date(2026, 3, 7, 12, 0, 0, 0, newYork),
			due:  time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC),
			want: DueForecast{Tomorrow: 1},
		},
		{
			name: "spring eve: half an hour after the end of today is tomorrow",
			now:  time.Date(2026, 3, 7, 12, 0, 0, 0, newYork),
			due:  time.Date(2026, 3, 8, 8, 30, 0, 0, time.UTC),
			want: DueForecast{Tomorrow: 1},
		},
		{
			name: "fall eve: half an hour after the old fixed boundary is still today",
			now:  time.Date(2026, 10, 31, 12, 0, 0, 0, newYork),
			due:  time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC),
			want: DueForecast{Today: 1},
		},
		{
			name: "fall eve: the end of today is tomorrow's first instant",
			now:  time.Date(2026, 10, 31, 12, 0, 0, 0, newYork),
			due:  time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC),
			want: DueForecast{Tomorrow: 1},
		},
		{
			name: "fall eve: a day inside the 7-day window follows the calendar",
			now:  time.Date(2026, 10, 31, 12, 0, 0, 0, newYork),
			due:  time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
			want: DueForecast{Within7: 1},
		},
	}

	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate: %v", err)
			}
			p := NewPreset(1, "dst forecast")
			if err := db.Create(&p).Error; err != nil {
				t.Fatalf("create preset: %v", err)
			}
			stats := NewStatsStore(db)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					deckID := seedForecastCard(t, db, p.ID, tc.name, tc.due)
					got, err := stats.DueForecast(context.Background(), 1, deckID, tc.now, newYork, 4)
					if err != nil {
						t.Fatalf("DueForecast() error = %v", err)
					}
					if got != tc.want {
						t.Errorf("DueForecast() = %+v, want %+v", got, tc.want)
					}
				})
			}
		})
	}
}

// seedForecastCard creates a deck owned by user 1 with one scheduled review card due at due, and returns the deck id.
func seedForecastCard(t *testing.T, db *gorm.DB, presetID uint64, name string, due time.Time) uint64 {
	t.Helper()
	deck := Deck{OwnerUserID: 1, Name: name, PresetID: presetID}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	noteTypeID := stockNoteTypeID(t, db, deck.ID, "basic")
	note := Note{DeckID: deck.ID, NoteTypeID: Ptr(noteTypeID), FieldsJSON: `{"front":"q","back":"a"}`}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := Card{NoteID: note.ID, Template: "forward", Ordinal: 0}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	setState(t, db, card.ID, "review", due, 10)
	return deck.ID
}
