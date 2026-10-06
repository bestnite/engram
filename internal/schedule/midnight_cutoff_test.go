package schedule

import (
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 默认队列与显式午夜队列必须在同一时刻使用不同的复习日额度。
func TestQueueCutoffDistinguishesUnsetAndMidnight(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 0, 30, 0, 0, time.UTC)
	deck := seedDeck(t, db, now)
	if err := store.NewDeckStore(db).SetCaps(t.Context(), 1, deck, store.DeckCaps{NewPerDay: 1, ReviewsPerDay: 200}); err != nil {
		t.Fatal(err)
	}
	card := seedCard(t, db, deck, "forward", now)
	// 配额按卡组 JOIN，日志必须挂在同一组真实卡上。
	seedReview(t, db, 1, card, "2026-10-01", int(StateNew), now.Add(-time.Hour))
	builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
	for _, tc := range []struct {
		name string
		hour *int
		want int
	}{{"unset", nil, 0}, {"midnight", store.Ptr(0), 1}, {"four", store.Ptr(4), 0}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := builder.Build(t.Context(), 1, QueueOptions{DeckID: deck, Now: now, Location: time.UTC, DayCutoffHour: tc.hour})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("queue size=%d, want %d", len(got), tc.want)
			}
		})
	}
}

// 评分写入与埋藏的次日边界都要尊重午夜，不只是统计页显示正确。
func TestSubmitAndBuryRespectMidnight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hour    *int
		day     string
		dueHour int
	}{{"unset", nil, "2026-10-01", 4}, {"midnight", store.Ptr(0), "2026-10-02", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			now := time.Date(2026, 10, 2, 0, 30, 0, 0, time.UTC)
			deck := seedDeck(t, db, now)
			card := seedCard(t, db, deck, "forward", now)
			res := mustSubmit(t, db, SubmitInput{CardID: card, UserID: 1, Rating: Good, Scheduler: mustScheduler(t, testPreset(t)), Now: now, Location: time.UTC, DayCutoffHour: tc.hour})
			var row store.Review
			if err := db.First(&row, res.ReviewID).Error; err != nil {
				t.Fatal(err)
			}
			if row.ReviewDay != tc.day {
				t.Fatalf("written day=%s, want %s", row.ReviewDay, tc.day)
			}
			st, err := Bury(t.Context(), db, BuryInput{CardID: card, UserID: 1, Now: now, Location: time.UTC, DayCutoffHour: tc.hour})
			if err != nil {
				t.Fatal(err)
			}
			expectedDay := 2
			if tc.hour != nil {
				expectedDay = 3
			}
			want := time.Date(2026, 10, expectedDay, tc.dueHour, 0, 0, 0, time.UTC)
			if st.DueAt == nil || !st.DueAt.Equal(want) {
				t.Fatalf("buried due=%v, want %v", st.DueAt, want)
			}
		})
	}
}
