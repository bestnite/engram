package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// optionOrderPermutation reports whether order is a permutation of 0..n-1.
func optionOrderPermutation(order []int, n int) bool {
	if len(order) != n {
		return false
	}
	seen := make([]bool, n)
	for _, idx := range order {
		if idx < 0 || idx >= n || seen[idx] {
			return false
		}
		seen[idx] = true
	}
	return true
}

// TestOptionOrderShape pins the shape of the derived order: a valid permutation for every option count of 2 or more,
// and no order at all below 2 (a single option has nothing to shuffle).
func TestOptionOrderShape(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 7, 20} {
		got := OptionOrder(n, 3, 9, 0)
		if n < 2 {
			if len(got) != 0 {
				t.Errorf("OptionOrder(%d) = %v, want nil", n, got)
			}
			continue
		}
		if !optionOrderPermutation(got, n) {
			t.Errorf("OptionOrder(%d) = %v, want a permutation of 0..%d", n, got, n-1)
		}
	}
}

// TestOptionOrderStability pins determinism and the version dependency: the same (user, card, version) always gives the
// same order, so refreshes and transports agree; a different version gives a different order. The pair checked here is a
// fixed example chosen after observing it differ — it is not a statistical guarantee for other inputs.
func TestOptionOrderStability(t *testing.T) {
	const n = 4
	const userID, cardID = uint64(7), uint64(11)
	first := OptionOrder(n, userID, cardID, 0)
	second := OptionOrder(n, userID, cardID, 0)
	if len(first) != len(second) {
		t.Fatalf("same input gave lengths %d and %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("same (user, card, version) gave %v then %v", first, second)
		}
	}
	next := OptionOrder(n, userID, cardID, 1)
	if len(next) == len(first) {
		same := true
		for i := range first {
			if first[i] != next[i] {
				same = false
				break
			}
		}
		if same {
			t.Errorf("version 1 returned the same order as version 0: %v", next)
		}
	}
}

// TestDueCardsPublishesOptionOrder checks the published contract through the service: both choice kinds carry a
// permutation of their stored option indices, and every kind without option controls carries null.
func TestDueCardsPublishesOptionOrder(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		fields     string
		template   string
		wantOption bool
		wantN      int
	}{
		{
			name: "choice_single", kind: "choice_single", template: "forward",
			fields:     `{"question":"q","options":["a","b","c"],"answer":1}`,
			wantOption: true, wantN: 3,
		},
		{
			name: "choice_multi", kind: "choice_multi", template: "forward",
			fields:     `{"question":"q","options":["a","b","c","d"],"answers":[0,2]}`,
			wantOption: true, wantN: 4,
		},
		{
			name: "true_false", kind: "true_false", template: "forward",
			fields: `{"statement":"s","answer":true}`,
		},
		{
			name: "cloze", kind: "cloze", template: "cloze:1",
			fields: `{"text":"{{c1::x}}"}`,
		},
		{
			name: "list", kind: "list", template: "forward",
			fields: `{"prompt":"p","items":["a","b"]}`,
		},
		{
			name: "typed", kind: "typed", template: "forward",
			fields: `{"prompt":"p","answer":"a"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, 600, 600)
			user := seedUser(t, env.db, "optorder-"+safeName(tc.name), store.RoleUser)
			deck := seedDeck(t, env.db, user.ID)
			seedKindCard(t, env.db, deck.ID, tc.kind, tc.fields, tc.template)

			got, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, nil, 50)
			if err != nil {
				t.Fatalf("DueCards() error = %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("DueCards() returned %d cards, want 1", len(got))
			}
			order := got[0].OptionOrder
			// The JSON contract is what the REST transport and MCP publish, so check the key itself, not just the field.
			encoded, err := json.Marshal(got[0])
			if err != nil {
				t.Fatalf("marshal due card: %v", err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &raw); err != nil {
				t.Fatalf("decode due card: %v", err)
			}
			if _, ok := raw["option_order"]; !ok {
				t.Fatalf("due card JSON has no option_order key: %s", encoded)
			}
			if !tc.wantOption {
				if order != nil {
					t.Errorf("kind %s published option_order %v, want null", tc.kind, order)
				}
				if got := string(raw["option_order"]); got != "null" {
					t.Errorf("kind %s option_order JSON = %s, want null", tc.kind, got)
				}
				return
			}
			if !optionOrderPermutation(order, tc.wantN) {
				t.Errorf("kind %s option_order = %v, want a permutation of 0..%d", tc.kind, order, tc.wantN-1)
			}
			want := OptionOrder(tc.wantN, user.ID, mustCardID(t, env, got[0]), got[0].Version)
			if !equalInts(order, want) {
				t.Fatalf("kind %s option_order = %v, want the derived order %v", tc.kind, order, want)
			}
		})
	}
}

// mustCardID resolves the numeric card id behind a due card; the test needs it to recompute the expected order.
func mustCardID(t *testing.T, env *testEnv, card DueCard) uint64 {
	t.Helper()
	c, err := store.NewCardStore(env.db).ByPublicID(context.Background(), card.CardID)
	if err != nil {
		t.Fatalf("load card %q: %v", card.CardID, err)
	}
	return c.ID
}

// TestOptionOrderChangesAfterReview checks the review lifecycle of the order: a new card has a stable order, one
// submission advances the version and therefore the order, and a stored-index answer that is correct still scores 1.
func TestOptionOrderChangesAfterReview(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	user := seedUser(t, env.db, "optorder-review", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	card := seedKindCard(t, env.db, deck.ID, "choice_single",
		`{"question":"q","options":["a","b","c","d"],"answer":2}`, "forward")

	before := dueOptionOrder(t, env, user, deck.ID)
	if !optionOrderPermutation(before, 4) {
		t.Fatalf("before review option_order = %v, want a permutation of 0..3", before)
	}
	if again := dueOptionOrder(t, env, user, deck.ID); len(again) != len(before) || !equalInts(before, again) {
		t.Fatalf("an unreviewed card changed order between two calls: %v then %v", before, again)
	}

	// The answer is a stored option index (2 is the correct option), never a display slot.
	answer, err := json.Marshal(2)
	if err != nil {
		t.Fatal(err)
	}
	res, err := env.api.SubmitReview(context.Background(), user, nil, SubmitReviewInput{
		CardID: card.PublicID, Answer: answer, ExpectedVersion: 0,
	})
	if err != nil {
		t.Fatalf("SubmitReview() error = %v", err)
	}
	if res.Grade == nil {
		t.Fatalf("graded submission returned no grade result: %+v", res)
	}
	if res.Grade.Score != 1 {
		t.Errorf("correct stored-index answer scored %v, want 1", res.Grade.Score)
	}

	// The submission moves the card into the learning steps, so it leaves today's queue; pull its due time back into
	// the past to read the published order for the new version through the same transport.
	if err := env.db.Model(&store.CardState{}).
		Where("card_id = ? AND user_id = ?", card.ID, user.ID).
		Update("due_at", env.now.Add(-time.Hour)).Error; err != nil {
		t.Fatalf("make reviewed card due again: %v", err)
	}

	after := dueOptionOrder(t, env, user, deck.ID)
	if after == nil {
		t.Fatalf("after review option_order = nil, want the order for version %d", res.Version)
	}
	if equalInts(before, after) {
		t.Errorf("order did not change after a review: %v", after)
	}
}

// dueOptionOrder returns the option order the service publishes for the deck's single due card.
func dueOptionOrder(t *testing.T, env *testEnv, user *store.User, deckID uint64) []int {
	t.Helper()
	got, err := env.api.DueCards(context.Background(), user, []uint64{deckID}, nil, 50)
	if err != nil {
		t.Fatalf("DueCards() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("DueCards() returned %d cards, want 1", len(got))
	}
	return got[0].OptionOrder
}

// equalInts reports whether a and b hold the same indices in the same positions.
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
