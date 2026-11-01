package store

import (
	"context"
	"testing"
	"time"
)

// TestBackfillReviewScheduledDays asserts migration 0021 fills the snapshot with the scheduler's
// truncation of interval_days, keeps an existing snapshot, leaves a row without an interval NULL,
// and runs twice without changing anything the second time.
func TestBackfillReviewScheduledDays(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := AutoMigrate(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			reviewed := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			cases := []struct {
				name     string
				interval *float64
				snapshot *int
				want     *int
			}{
				{"fractional step", Ptr(1.5), nil, Ptr(1)},
				{"whole days", Ptr(2.0), nil, Ptr(2)},
				{"under a day", Ptr(0.5), nil, Ptr(0)},
				{"no interval", nil, nil, nil},
				{"existing snapshot", Ptr(1.5), Ptr(9), Ptr(9)},
			}
			rows := make([]Review, 0, len(cases))
			for i, tc := range cases {
				row := Review{
					PublicID:      "review-" + tc.name,
					CardID:        uint64(i + 1),
					UserID:        1,
					Rating:        3,
					GradeSource:   "self",
					ReviewedAt:    reviewed,
					ReviewDay:     "2026-10-02",
					StateBefore:   2,
					IntervalDays:  tc.interval,
					ScheduledDays: tc.snapshot,
					ID:            uint64(i + 1),
				}
				if err := db.Create(&row).Error; err != nil {
					t.Fatalf("create review %q: %v", tc.name, err)
				}
				rows = append(rows, row)
			}
			for run := 0; run < 2; run++ {
				if err := backfillReviewScheduledDays(db); err != nil {
					t.Fatalf("run %d: %v", run, err)
				}
				for i, tc := range cases {
					var got Review
					if err := db.First(&got, rows[i].ID).Error; err != nil {
						t.Fatalf("load review %q: %v", tc.name, err)
					}
					switch {
					case tc.want == nil && got.ScheduledDays != nil:
						t.Errorf("run %d: %s scheduled_days = %d, want NULL", run, tc.name, *got.ScheduledDays)
					case tc.want != nil && (got.ScheduledDays == nil || *got.ScheduledDays != *tc.want):
						t.Errorf("run %d: %s scheduled_days = %v, want %d", run, tc.name, got.ScheduledDays, *tc.want)
					}
				}
			}
		})
	}
}
