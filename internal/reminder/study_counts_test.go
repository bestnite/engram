package reminder

import (
	"context"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

func TestReminderNewCardsAndQueueGates(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
		want bool
	}{
		{"no state row", "missing", true},
		{"explicit New", "new", true},
		{"mixed new and review", "mixed", true},
		{"learning is review", "learning", true},
		{"relearning is review", "relearning", true},
		{"paused", "paused", false},
		{"new goal exhausted", "limit", false},
		{"buried", "buried", false},
		{"suspended", "suspended", false},
		{"access revoked", "revoked", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			now := shanghaiAt(t, 20, 0)
			uid := seedUserWithDueCard(t, db, "learner", "learner@example.com", "en", "Asia/Shanghai", 4, now.Add(-time.Hour))
			enableReminder(t, db, uid)
			var state store.CardState
			if err := db.Where("user_id = ?", uid).Take(&state).Error; err != nil {
				t.Fatal(err)
			}
			var deck store.Deck
			if err := db.Where("owner_user_id = ?", uid).Take(&deck).Error; err != nil {
				t.Fatal(err)
			}
			if tc.mode == "mixed" {
				var card store.Card
				if err := db.First(&card, state.CardID).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&store.Card{NoteID: card.NoteID, Template: "reverse", CreatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			} else if tc.mode == "learning" || tc.mode == "relearning" {
				if err := db.Model(&state).Update("state", tc.mode).Error; err != nil {
					t.Fatal(err)
				}
			} else if tc.mode == "missing" || tc.mode == "paused" || tc.mode == "limit" || tc.mode == "revoked" {
				if err := db.Delete(&state).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				updates := map[string]any{"state": "new", "due_at": nil}
				if tc.mode == "buried" {
					updates["due_at"] = now.Add(time.Hour)
				}
				if tc.mode == "suspended" {
					updates["suspended_at"] = now
				}
				if err := db.Model(&state).Updates(updates).Error; err != nil {
					t.Fatal(err)
				}
			}
			switch tc.mode {
			case "paused":
				if _, err := store.NewDeckStore(db).SetPaused(context.Background(), uid, deck.ID, true, now); err != nil {
					t.Fatal(err)
				}
			case "limit":
				if err := db.Model(&deck).Update("new_per_day", 1).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&store.Review{UserID: uid, CardID: state.CardID, Rating: 3, GradeSource: "self", StateBefore: 0, ReviewDay: "2026-06-01", ReviewedAt: now.Add(-time.Hour)}).Error; err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if err := db.Model(&deck).Update("owner_user_id", uid+100).Error; err != nil {
					t.Fatal(err)
				}
			}
			enq := &fakeEnqueuer{configured: true}
			r := newReminder(t, db, enq, func() time.Time { return now })
			if _, err := r.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			sent, err := store.HasReminderBeenSent(context.Background(), db, uid, "2026-06-01")
			if err != nil {
				t.Fatal(err)
			}
			if !tc.want {
				if enq.count() != 0 || sent {
					t.Fatalf("empty queue: mails=%d ledger=%v", enq.count(), sent)
				}
				return
			}
			if enq.count() != 1 || !sent {
				t.Fatalf("mails=%d ledger=%v", enq.count(), sent)
			}
			fresh, review := "1", "0"
			if tc.mode == "mixed" {
				review = "1"
			}
			if tc.mode == "learning" || tc.mode == "relearning" {
				fresh, review = "0", "1"
			}
			if body := enq.last().TextBody; !strings.Contains(body, fresh+" new cards to learn and "+review+" cards to review") {
				t.Fatalf("wrong separated counts: %s", body)
			}
		})
	}
}

func TestReminderLegacyCountRemainsReviewOnly(t *testing.T) {
	db := newTestDB(t)
	now := shanghaiAt(t, 20, 0)
	uid := seedUserWithDueCard(t, db, "legacy", "legacy@example.com", "en", "Asia/Shanghai", 4, now.Add(-time.Hour))
	enableReminder(t, db, uid)
	if err := db.Where("user_id = ?", uid).Delete(&store.CardState{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&store.MailTemplate{Type: string(mail.TypeReviewReminder), Locale: "en", Subject: "Legacy {{count}}", BodyMD: "<p>{{count}}/{{new_available}}/{{review_available}} <a href=\"{{url}}\">Study</a></p>"}).Error; err != nil {
		t.Fatal(err)
	}
	enq := &fakeEnqueuer{configured: true}
	r := newReminderWithTemplates(t, db, enq, func() time.Time { return now })
	if _, err := r.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if enq.count() != 1 || !strings.Contains(enq.last().TextBody, "0/1/0") {
		t.Fatalf("legacy count changed: %+v", enq.msgs)
	}
}
