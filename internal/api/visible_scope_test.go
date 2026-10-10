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

// visibleFixture 装配「一个 owner + 一个 viewer」的可见性场景：
// viewer 被授权 granted，denied 与 other 都是 owner 的卡组但没有授权给 viewer。
type visibleFixture struct {
	env     *testEnv
	owner   *store.User
	viewer  *store.User
	granted *store.Deck
	denied  *store.Deck
	other   *store.Deck
	key     *store.CreatedAPIKey
}

// newVisibleFixture 建立上述三个卡组并给 viewer 一个 read key。
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

// listDeckIDsViaHTTP 用 key 调 GET /api/v1/decks，返回响应里的卡组对外 id 集合。
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

// TestListDecksUsesVisibleScope 是对这条规则的验收：REST 列表必须与网页列表页同口径
// ——自有 ∪ 被 deck_grants 授权。别人没授权给我的卡组一律不得出现，且撤销授权即时生效。
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

	// 撤销授权即时生效：同一个 key 的下一次请求就不再看到它。
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

// TestStatsVisibleContentButFullReviewHistory 是对这条规则的验收：
//   - notes/cards/due/decks 只算可见集合（被授权卡组计入，撤销后不再计入）；
//   - reviews_today / reviews_total / retention 按本人历史返回，撤销授权不追溯；
//   - 没有可见卡组的用户不得因提前 return 把历史复习数字清零。
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

	// viewer 今日的三条复习：两条到期复习（rating=4、rating=1）与一条新卡首次复习。
	// 留存只数到期复习 = 1/2；新卡那条只进复习量。
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

	// 撤销授权：内容计数归零，但复习历史必须原样保留（不得提前 return 清零）。
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
