package store

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/anki"
	"github.com/open-spaced-repetition/go-fsrs/v4"
)

func TestAnkiReplayHistoricalStartingState(t *testing.T) {
	engine := fsrs.NewFSRS(fsrs.DefaultParam())
	start := &fsrs.MemoryState{Stability: 30, Difficulty: 6}
	history := fsrs.ReviewEntries{{Rating: fsrs.Good, DeltaT: 1}}
	states, err := engine.HistoricalMemoryStates(history, start)
	if err != nil || len(states) != 2 || states[0] != *start {
		t.Fatalf("states=%v err=%v", states, err)
	}
	last, err := engine.MemoryState(history, start)
	if err != nil || states[1] != *last {
		t.Fatalf("states=%v final=%v err=%v", states, last, err)
	}
}

func TestAnkiReplayAge(t *testing.T) {
	for _, tc := range []struct{ stamp, want int64 }{{300000000, 0}, {221799000, 0}, {136400000, 0}, {135400000, 0}, {135399000, 1}} {
		if got := ankiReplayAge(221799, tc.stamp); got != tc.want {
			t.Errorf("stamp %d: got %d want %d", tc.stamp, got, tc.want)
		}
	}
}

func TestAnkiReplayControls(t *testing.T) {
	type control struct {
		Package    string  `json:"package"`
		Timezone   string  `json:"timezone"`
		Rollover   int     `json:"rollover"`
		Now        int64   `json:"now"`
		Offset     int     `json:"offset_seconds"`
		NextDay    int64   `json:"next_day_at"`
		Stability  float64 `json:"stability"`
		Difficulty float64 `json:"difficulty"`
	}
	for _, file := range []string{"memory-replay-controls.golden.json", "memory-replay-environments.golden.json"} {
		var golden struct {
			Controls map[string]control `json:"controls"`
		}
		raw, err := os.ReadFile(ankiFixturePath(file))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &golden); err != nil {
			t.Fatal(err)
		}
		for driver, db := range packageDatabases(t) {
			for label, tc := range golden.Controls {
				t.Run(file+"/"+driver+"/"+label, func(t *testing.T) {
					loc, err := time.LoadLocation(tc.Timezone)
					if err != nil {
						t.Fatal(err)
					}
					now := time.Unix(tc.Now, 0)
					_, offset := now.In(loc).Zone()
					if offset != tc.Offset || ankiReplayNextDay(now, loc, tc.Rollover) != tc.NextDay {
						t.Fatalf("oracle timing mismatch: %+v offset=%d", tc, offset)
					}
					pkg, err := anki.ReadFile(ankiFixturePath(tc.Package))
					if err != nil {
						t.Fatal(err)
					}
					user := seedUsers(t, db, "control_"+driver+"_"+file+"_"+label)[0]
					if err := db.Model(&User{}).Where("id = ?", user).Updates(map[string]any{"timezone": tc.Timezone, "day_cutoff_hour": tc.Rollover}).Error; err != nil {
						t.Fatal(err)
					}
					plan, report := planAnkiDecks(pkg), &PackageImportReport{}
					if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: true, Now: func() time.Time { return now }}, report); err != nil {
						t.Fatal(err)
					}
					var state CardState
					if err := db.Where("user_id = ?", user).First(&state).Error; err != nil {
						t.Fatal(err)
					}
					if state.Stability == nil || state.Difficulty == nil || math.Abs(*state.Stability-tc.Stability) > 1e-4 || math.Abs(*state.Difficulty-tc.Difficulty) > 1e-4 {
						t.Fatalf("state=%+v want=%+v", state, tc)
					}
				})
			}
		}
	}
}
