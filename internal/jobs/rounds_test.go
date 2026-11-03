package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/worker"
)

// roundDrivers returns a migrated database per available driver: SQLite always, PostgreSQL when TEST_PG_DSN is set.
func roundDrivers(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	out := map[string]*gorm.DB{}
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "rounds.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	out["sqlite"] = db
	if pg, ok := pgtest.Open(t); ok {
		out["postgres"] = pg
	}
	for name, d := range out {
		if err := store.AutoMigrate(context.Background(), d); err != nil {
			t.Fatalf("AutoMigrate(%s): %v", name, err)
		}
	}
	return out
}

// TestCreateRound covers the row a recorded round becomes: a successful round and a failed one, both terminal, with
// the trigger, the summary as JSON and the error text.
func TestCreateRound(t *testing.T) {
	for name, db := range roundDrivers(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := NewStore(db)
			started := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
			finished := started.Add(1500 * time.Millisecond)
			cases := []struct {
				round      worker.Round
				wantStatus string
				wantErr    string
			}{
				{worker.Round{Kind: worker.KindRetention, Trigger: worker.TriggerAuto, StartedAt: started, FinishedAt: finished,
					Summary: worker.Summary{"sessions": 4}}, StatusSucceeded, ""},
				{worker.Round{Kind: worker.KindMailDelivery, Trigger: worker.TriggerManual, StartedAt: started, FinishedAt: finished,
					Err: errors.New("load due outbox messages: boom")}, StatusFailed, "load due outbox messages: boom"},
			}
			for _, tc := range cases {
				if err := st.CreateRound(ctx, tc.round); err != nil {
					t.Fatalf("CreateRound(%s): %v", tc.round.Kind, err)
				}
				list, _, err := st.List(ctx, tc.round.Kind, 10, 0)
				if err != nil || len(list) != 1 {
					t.Fatalf("List(%s) = %v, %v", tc.round.Kind, list, err)
				}
				got := list[0]
				if got.Status != tc.wantStatus || got.Trigger == nil || *got.Trigger != tc.round.Trigger || got.PublicID == "" {
					t.Fatalf("row = %+v", got)
				}
				if got.StartedAt == nil || !got.StartedAt.Equal(started) || got.FinishedAt == nil || !got.FinishedAt.Equal(finished) || !got.CreatedAt.Equal(started) {
					t.Fatalf("row times = created %v started %v finished %v", got.CreatedAt, got.StartedAt, got.FinishedAt)
				}
				if tc.wantErr == "" && got.Error != nil || tc.wantErr != "" && (got.Error == nil || *got.Error != tc.wantErr) {
					t.Fatalf("row error = %v, want %q", got.Error, tc.wantErr)
				}
				var summary map[string]int64
				if got.ResultJSON == nil || json.Unmarshal([]byte(*got.ResultJSON), &summary) != nil {
					t.Fatalf("result_json = %v", got.ResultJSON)
				}
				if len(summary) != len(tc.round.Summary) || summary["sessions"] != tc.round.Summary["sessions"] {
					t.Fatalf("summary = %v, want %v", summary, tc.round.Summary)
				}
			}
			// Recorded rounds are terminal, so they never count as the in-flight job.
			active, err := st.Active(ctx)
			if err != nil || active != nil {
				t.Fatalf("Active() = %+v, %v; want none", active, err)
			}
		})
	}
}

// TestDeleteStaleKeepsOptimizerRows: the 30-day purge deletes old periodic rows only; an equally old optimizer row
// (trigger user, or NULL from before the column) and a recent periodic row survive.
func TestDeleteStaleKeepsOptimizerRows(t *testing.T) {
	for name, db := range roundDrivers(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := NewStore(db)
			now := time.Now().UTC()
			old := now.Add(-31 * 24 * time.Hour)
			recent := now.Add(-time.Hour)
			rows := map[string]store.Job{
				"old auto":    {Kind: worker.KindRetention, Trigger: store.Ptr(worker.TriggerAuto), Status: StatusSucceeded, CreatedAt: old, FinishedAt: &old},
				"old manual":  {Kind: worker.KindDigest, Trigger: store.Ptr(worker.TriggerManual), Status: StatusFailed, CreatedAt: old, FinishedAt: &old},
				"recent auto": {Kind: worker.KindMediaGC, Trigger: store.Ptr(worker.TriggerAuto), Status: StatusSucceeded, CreatedAt: recent, FinishedAt: &recent},
				"old user":    {Kind: KindOptimize, Trigger: store.Ptr(TriggerUser), Status: StatusSucceeded, CreatedAt: old, FinishedAt: &old},
				"old legacy":  {Kind: KindOptimize, Status: StatusFailed, CreatedAt: old, FinishedAt: &old},
			}
			ids := map[string]uint64{}
			for label, row := range rows {
				if err := db.Create(&row).Error; err != nil {
					t.Fatalf("seed %s: %v", label, err)
				}
				ids[label] = row.ID
			}
			removed, err := st.DeleteStale(ctx, now.Add(-30*24*time.Hour))
			if err != nil {
				t.Fatalf("DeleteStale: %v", err)
			}
			if removed != 2 {
				t.Fatalf("DeleteStale removed %d, want 2", removed)
			}
			for label, id := range ids {
				_, err := st.ByID(ctx, id)
				gone := err != nil
				wantGone := label == "old auto" || label == "old manual"
				if gone != wantGone {
					t.Errorf("%s: gone = %v, want %v (err %v)", label, gone, wantGone, err)
				}
			}
		})
	}
}

// TestEnqueueWithRecordedRounds: recorded periodic rows never block an optimization enqueue, and the new job row is
// marked as user-triggered.
func TestEnqueueWithRecordedRounds(t *testing.T) {
	runner, st, _ := newTestRunner(t, time.Minute, okBuilder)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.CreateRound(ctx, worker.Round{Kind: worker.KindReminder, Trigger: worker.TriggerManual, StartedAt: now, FinishedAt: now}); err != nil {
		t.Fatalf("CreateRound: %v", err)
	}
	job, err := runner.Enqueue(ctx, KindOptimize, nil)
	if err != nil {
		t.Fatalf("Enqueue with recorded rounds present: %v", err)
	}
	if job.Trigger == nil || *job.Trigger != TriggerUser {
		t.Fatalf("enqueued job trigger = %v, want user", job.Trigger)
	}
	all, total, err := runner.List(ctx, "", 10, 0)
	if err != nil || total != 2 || len(all) != 2 {
		t.Fatalf("List(all) = %d rows, total %d, err %v; want 2", len(all), total, err)
	}
	only, total, err := runner.List(ctx, KindOptimize, 10, 0)
	if err != nil || total != 1 || len(only) != 1 || only[0].Kind != KindOptimize {
		t.Fatalf("List(optimize) = %+v, total %d, err %v", only, total, err)
	}
}
