package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestBuryCard covers the one bury implementation shared by REST, MCP and the web review page: the owner and a reader
// can bury (it writes their own progress only), a stranger and an unknown card are refused, and a buried card leaves the
// queue until the start of the caller's next review day in their own timezone.
func TestBuryCard(t *testing.T) {
	cases := []struct {
		name     string
		actor    string // owner, reader, stranger
		unknown  bool
		timezone string
		want     int
		wantDue  time.Time
	}{
		// env.now is 2026-10-02 12:00 UTC; the cutoff is 04:00 local.
		{name: "owner in UTC", actor: "owner", timezone: "UTC", want: http.StatusOK, wantDue: time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)},
		{name: "reader of a shared deck", actor: "reader", timezone: "UTC", want: http.StatusOK, wantDue: time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)},
		{name: "next day follows the user's timezone", actor: "owner", timezone: "Asia/Shanghai", want: http.StatusOK, wantDue: time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)},
		{name: "stranger is refused", actor: "stranger", timezone: "UTC", want: http.StatusForbidden},
		{name: "unknown card", actor: "owner", unknown: true, timezone: "UTC", want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			ctx := context.Background()
			owner := seedUser(t, env.db, "bury_owner", store.RoleUser)
			deck, note := newDeckWithNote(t, env, owner.ID)
			var card store.Card
			if err := env.db.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
				t.Fatalf("load card: %v", err)
			}
			actor := owner
			switch tc.actor {
			case "reader":
				actor = seedUser(t, env.db, "bury_reader", store.RoleUser)
				if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, actor.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
					t.Fatalf("grant: %v", err)
				}
			case "stranger":
				actor = seedUser(t, env.db, "bury_stranger", store.RoleUser)
			}
			actor.Timezone = tc.timezone
			cardID := card.PublicID
			if tc.unknown {
				cardID = "01a00000-0000-7000-8000-000000000000"
			}

			res, err := env.api.BuryCard(ctx, actor, cardID)
			if tc.want != http.StatusOK {
				if err == nil || asServiceError(err).Status != tc.want {
					t.Fatalf("BuryCard() error = %v, want status %d", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuryCard() error = %v", err)
			}
			if !res.DueAt.Equal(tc.wantDue) {
				t.Errorf("due_at = %v, want %v", res.DueAt, tc.wantDue)
			}
			// A buried new card is still new and no longer in today's queue.
			queue, err := env.api.DueCards(ctx, actor, []uint64{deck.ID}, nil, 500)
			if err != nil {
				t.Fatalf("DueCards() error = %v", err)
			}
			for _, c := range queue {
				if c.CardID == card.PublicID {
					t.Errorf("buried card %s is still in today's queue", card.PublicID)
				}
			}
			var reviews int64
			env.db.Model(&store.Review{}).Where("card_id = ?", card.ID).Count(&reviews)
			if reviews != 0 {
				t.Errorf("bury wrote %d review rows, want none", reviews)
			}
		})
	}
}

// TestBuryCardREST covers POST /api/v1/cards/:id/bury: a review-scoped key buries, a key without the review scope is refused.
func TestBuryCardREST(t *testing.T) {
	cases := []struct {
		name   string
		scopes []string
		want   int
	}{
		{name: "review scope", scopes: []string{store.ScopeReview}, want: http.StatusOK},
		{name: "read scope only", scopes: []string{store.ScopeRead}, want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 60, 60)
			user := seedUser(t, env.db, "bury_rest", store.RoleUser)
			_, note := newDeckWithNote(t, env, user.ID)
			var card store.Card
			if err := env.db.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
				t.Fatalf("load card: %v", err)
			}
			key := seedKey(t, env.keys, user.ID, tc.scopes, nil)
			status, body := doJSON(t, env.router(), http.MethodPost, "/api/v1/cards/"+card.PublicID+"/bury", key.Plaintext, "")
			if status != tc.want {
				t.Fatalf("POST bury = %d, want %d (body %s)", status, tc.want, body)
			}
			if tc.want != http.StatusOK {
				return
			}
			var res BuryResult
			if err := json.Unmarshal(body, &res); err != nil || res.CardID != card.PublicID || res.DueAt.IsZero() {
				t.Errorf("POST bury body = %s (%v), want the card and its new due time", body, err)
			}
		})
	}
}
