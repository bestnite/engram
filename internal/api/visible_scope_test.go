package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// visibleFixture sets up a visibility scenario of "one owner + one viewer":
// viewer is granted granted, while denied and other are both owner's decks not granted to viewer.
type visibleFixture struct {
	env     *testEnv
	owner   *store.User
	viewer  *store.User
	granted *store.Deck
	denied  *store.Deck
	other   *store.Deck
	key     *store.CreatedAPIKey
}

// newVisibleFixture creates the three decks above and gives viewer a read key.
func newVisibleFixture(t *testing.T) *visibleFixture {
	t.Helper()
	env := newTestEnv(t, 600, 600)
	owner := seedUser(t, env.db, "vis_owner", store.RoleUser)
	viewer := seedUser(t, env.db, "vis_viewer", store.RoleUser)

	granted := seedDeck(t, env.db, owner.ID)
	denied := seedDeck(t, env.db, owner.ID)
	other := seedDeck(t, env.db, owner.ID)

	if err := store.NewGrantStore(env.db).Grant(context.Background(), granted.ID, viewer.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}
	key := seedKey(t, env.keys, viewer.ID, []string{store.ScopeRead}, nil)
	return &visibleFixture{
		env: env, owner: owner, viewer: viewer,
		granted: granted, denied: denied, other: other, key: key,
	}
}

// listDeckIDsViaHTTP calls GET /api/v1/decks with a key and returns the set of deck external ids in the response.
func listDeckIDsViaHTTP(t *testing.T, env *testEnv, key string) map[string]bool {
	t.Helper()
	status, raw := doJSON(t, env.router(), http.MethodGet, "/api/v1/decks", key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", status, raw)
	}
	var body struct {
		Decks []struct {
			ID string `json:"id"`
		} `json:"decks"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode decks: %v (%s)", err, raw)
	}
	out := make(map[string]bool, len(body.Decks))
	for _, d := range body.Decks {
		out[d.ID] = true
	}
	return out
}

// TestListDecksUsesVisibleScope is the acceptance for this rule: the REST list must follow the same convention as the web list page
// - owned ∪ granted via deck_grants. A deck someone else has not granted me must never appear, and revoking a grant takes effect immediately.
func TestListDecksUsesVisibleScope(t *testing.T) {
	f := newVisibleFixture(t)

	got := listDeckIDsViaHTTP(t, f.env, f.key.Plaintext)
	if !got[f.granted.PublicID] {
		t.Errorf("GET /decks missing granted deck %s: %v", f.granted.PublicID, got)
	}
	for _, d := range []*store.Deck{f.denied, f.other} {
		if got[d.PublicID] {
			t.Errorf("GET /decks leaked another user's ungranted deck %s: %v", d.PublicID, got)
		}
	}

	// Revocation takes effect immediately: the same key's next request no longer sees it.
	if err := store.NewGrantStore(f.env.db).Revoke(context.Background(), f.granted.ID, f.viewer.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got = listDeckIDsViaHTTP(t, f.env, f.key.Plaintext)
	if got[f.granted.PublicID] {
		t.Errorf("GET /decks still shows deck %s after the grant was revoked: %v", f.granted.PublicID, got)
	}
	if len(got) != 0 {
		t.Errorf("GET /decks = %v, want empty (viewer owns nothing and holds no grants)", got)
	}
}

// TestStatsVisibleContentButFullReviewHistory is the acceptance for this rule:
//   - notes/cards/due/decks count only the visible set (granted decks are included, and excluded once revoked);
//   - reviews_today / reviews_total / retention are returned from the person's own history, and revoking a grant does not reach back;
//   - a user with no visible deck must not have their historical review numbers zeroed by an early return.
func TestStatsVisibleContentButFullReviewHistory(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "stats_owner", store.RoleUser)
	viewer := seedUser(t, env.db, "stats_viewer", store.RoleUser)
	deck, note := newDeckWithNote(t, env, owner.ID)
	ctx := context.Background()

	var card store.Card
	if err := env.db.Where("note_id = ?", note.ID).First(&card).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	grants := store.NewGrantStore(env.db)
	if err := grants.Grant(ctx, deck.ID, viewer.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}

	// viewer's three reviews today: two due reviews (rating=4, rating=1) and one new card's first review.
	// Retention counts only due reviews = 1/2; the new-card one only counts into the review volume.
	day := schedule.ReviewDay(env.now, time.UTC, 4)
	stability := 5.0
	for _, rv := range []struct{ rating, stateBefore int }{{4, 2}, {1, 2}, {3, 0}} {
		if err := env.db.Create(&store.Review{
			CardID: card.ID, UserID: viewer.ID, Rating: rv.rating, GradeSource: "self",
			ReviewedAt: env.now, ReviewDay: day, StateBefore: rv.stateBefore, Stability: &stability,
		}).Error; err != nil {
			t.Fatalf("create review: %v", err)
		}
	}
	due := env.now.Add(-time.Hour)
	if err := env.db.Create(&store.CardState{
		CardID: card.ID, UserID: viewer.ID, State: "review", DueAt: &due,
	}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}

	visible, err := env.api.Stats(ctx, viewer)
	if err != nil {
		t.Fatalf("Stats (granted) error = %v", err)
	}
	if visible.Decks != 1 || visible.Notes != 1 || visible.Cards != 1 || visible.Due != 1 {
		t.Fatalf("Stats (granted) content = %+v, want decks=1 notes=1 cards=1 due=1", visible)
	}
	if visible.ReviewsTotal != 3 || visible.ReviewsToday != 3 {
		t.Fatalf("Stats (granted) reviews = %+v, want total=3 today=3", visible)
	}
	if visible.Retention != 0.5 || visible.RetentionTotal != 2 || visible.RetentionPassed != 1 {
		t.Fatalf("Stats (granted) retention = %v (%d/%d), want 0.5 (1/2)", visible.Retention, visible.RetentionPassed, visible.RetentionTotal)
	}

	// Revoke the grant: content counts drop to zero, but the review history must be preserved as-is (no zeroing via an early return).
	if err := grants.Revoke(ctx, deck.ID, viewer.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked, err := env.api.Stats(ctx, viewer)
	if err != nil {
		t.Fatalf("Stats (revoked) error = %v", err)
	}
	if revoked.Decks != 0 || revoked.Notes != 0 || revoked.Cards != 0 || revoked.Due != 0 {
		t.Fatalf("Stats (revoked) content = %+v, want all content counts 0", revoked)
	}
	if revoked.ReviewsTotal != 3 || revoked.ReviewsToday != 3 {
		t.Fatalf("Stats (revoked) reviews = %+v, want total=3 today=3 (history preserved)", revoked)
	}
	if revoked.Retention != 0.5 || revoked.RetentionTotal != 2 {
		t.Fatalf("Stats (revoked) retention = %v over %d, want 0.5 over 2 (history preserved)", revoked.Retention, revoked.RetentionTotal)
	}
}
