package digest

import (
	"context"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

func TestDigestSeparatesRatingStatesAndPreservesLegacyVariables(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			db := newTestDB(t)
			uid := seedUser(t, db, "learner", "learner@example.com", locale, "UTC", 4)
			card := seedCard(t, db, uid)
			now := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
			for _, state := range []int{0, 1, 2, 3} {
				seedReview(t, db, uid, card, "2026-06-01", 3, state, 5)
			}
			seedReview(t, db, uid, card, "2026-05-25", 3, 0, 5)
			other := seedUser(t, db, "other", "other@example.com", locale, "UTC", 4)
			seedReview(t, db, other, card, "2026-06-01", 3, 0, 5)
			stats, err := ComputeStats(context.Background(), db, uid, now, time.UTC, 4)
			if err != nil {
				t.Fatal(err)
			}
			if stats.Reviewed != 4 || stats.NewCards != 1 || stats.ReviewCount != 3 || stats.NewAvailable != 1 || stats.DueNow != 0 {
				t.Fatalf("unexpected counts: %+v", stats)
			}
			setDigestPref(t, db, uid, true)
			enq := &fakeEnqueuer{configured: true}
			w := newWorker(t, db, enq, func() time.Time { return now })
			if _, err := w.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := []string{"New ratings: 1", "Reviews: 3", "New cards available now: 1", "Due now: 0"}
			if locale == "zh-CN" {
				want = []string{"新学次数：1", "复习次数：3", "待新学：1", "待复习：0"}
			}
			if enq.count() != 1 {
				t.Fatalf("mails=%d", enq.count())
			}
			for _, text := range want {
				if !strings.Contains(enq.last().TextBody, text) {
					t.Fatalf("missing %q: %s", text, enq.last().TextBody)
				}
			}
			if err := db.Create(&store.MailTemplate{Type: string(mail.TypeStudyDigest), Locale: locale, Subject: "Legacy {{count}}", BodyMD: "<p>{{count}}/{{new_cards}}/{{due}}/{{new_count}}/{{review_count}}/{{new_available}}/{{review_available}} <a href=\"{{url}}\">Stats</a></p>"}).Error; err != nil {
				t.Fatal(err)
			}
			now = now.AddDate(0, 0, 7)
			// Move the history into the new window while keeping every state classification intact.
			if err := db.Model(&store.Review{}).Where("user_id = ? AND review_day = ?", uid, "2026-06-01").Update("review_day", "2026-06-08").Error; err != nil {
				t.Fatal(err)
			}
			if _, err := w.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if enq.count() != 2 || !strings.Contains(enq.last().TextBody, "4/1/0/1/3/1/0") {
				t.Fatalf("legacy/new variables: %+v", enq.msgs)
			}
		})
	}
}
