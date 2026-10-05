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

// setDeckVisibility 直接改一行卡组的可见性；用于验证「可见性即时生效」。
func setDeckVisibility(t *testing.T, env *testEnv, deckID uint64, visibility string) {
	t.Helper()
	if err := env.db.Model(&store.Deck{}).Where("id = ?", deckID).Update("visibility", visibility).Error; err != nil {
		t.Fatalf("set deck visibility: %v", err)
	}
}

// visibleFixture 装配「一个 owner + 一个 viewer」的可见性场景：
// viewer 被授权 granted、denied 未授权、public 对他人的 public、unlisted 是他人 unlisted。
type visibleFixture struct {
	env      *testEnv
	owner    *store.User
	viewer   *store.User
	granted  *store.Deck
	denied   *store.Deck
	public   *store.Deck
	unlisted *store.Deck
	key      *store.CreatedAPIKey
}

// newVisibleFixture 建立上述四个卡组并给 viewer 一个 read key。
func newVisibleFixture(t *testing.T) *visibleFixture {
	t.Helper()
	env := newTestEnv(t, 600, 600)
	owner := seedUser(t, env.db, "vis_owner", store.RoleUser)
	viewer := seedUser(t, env.db, "vis_viewer", store.RoleUser)

	granted := seedDeck(t, env.db, owner.ID)
	denied := seedDeck(t, env.db, owner.ID)
	public := seedDeck(t, env.db, owner.ID)
	setDeckVisibility(t, env, public.ID, store.DeckVisibilityPublic)
	unlisted := seedDeck(t, env.db, owner.ID)
	setDeckVisibility(t, env, unlisted.ID, store.DeckVisibilityUnlisted)

	if err := store.NewGrantStore(env.db).Grant(context.Background(), granted.ID, viewer.ID, store.RoleReader, store.Ptr(owner.ID)); err != nil {
		t.Fatalf("grant viewer: %v", err)
	}
	key := seedKey(t, env.keys, viewer.ID, []string{store.ScopeRead}, nil)
	return &visibleFixture{
		env: env, owner: owner, viewer: viewer,
		granted: granted, denied: denied, public: public, unlisted: unlisted, key: key,
	}
}

// listDeckIDsViaHTTP 用 key 调 GET /api/v1/decks，返回响应里的卡组 id 集合。
func listDeckIDsViaHTTP(t *testing.T, env *testEnv, key string) map[uint64]bool {
	t.Helper()
	status, raw := doJSON(t, env.router(), http.MethodGet, "/api/v1/decks", key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /decks status = %d, want 200 (body %s)", status, raw)
	}
	var body struct {
		Decks []struct {
			ID uint64 `json:"id"`
		} `json:"decks"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode decks: %v (%s)", err, raw)
	}
	out := make(map[uint64]bool, len(body.Decks))
	for _, d := range body.Decks {
		out[d.ID] = true
	}
	return out
}

// TestListDecksUsesVisibleScope 是 F5 的验收：REST 列表必须与网页列表页同口径
// ——自有 ∪ 被授权 ∪ 他人 public；未授权的 private 与任何 unlisted 都不得出现。
func TestListDecksUsesVisibleScope(t *testing.T) {
	f := newVisibleFixture(t)

	got := listDeckIDsViaHTTP(t, f.env, f.key.Plaintext)
	if !got[f.granted.ID] {
		t.Errorf("GET /decks missing granted private deck %d: %v", f.granted.ID, got)
	}
	if !got[f.public.ID] {
		t.Errorf("GET /decks missing other user's public deck %d: %v", f.public.ID, got)
	}
	if got[f.denied.ID] {
		t.Errorf("GET /decks leaked unauthorized private deck %d: %v", f.denied.ID, got)
	}
	if got[f.unlisted.ID] {
		t.Errorf("GET /decks leaked unlisted deck %d: %v", f.unlisted.ID, got)
	}

	// 可见性即时生效：public 改为 private 后，同一个 key 的下一次请求就不再看到它。
	setDeckVisibility(t, f.env, f.public.ID, store.DeckVisibilityPrivate)
	got = listDeckIDsViaHTTP(t, f.env, f.key.Plaintext)
	if got[f.public.ID] {
		t.Errorf("GET /decks still shows deck %d after it became private: %v", f.public.ID, got)
	}
	if !got[f.granted.ID] {
		t.Errorf("GET /decks lost granted deck %d after unrelated visibility change: %v", f.granted.ID, got)
	}
}

// TestStatsVisibleContentButFullReviewHistory 是 F6 的验收：
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

	// viewer 的两条历史复习：一条 rating=4、一条 rating=1，今日各一条。留存 = 1/2。
	day := schedule.ReviewDay(env.now, time.UTC, 4)
	for _, rating := range []int{4, 1} {
		if err := env.db.Create(&store.Review{
			CardID: card.ID, UserID: viewer.ID, Rating: rating, GradeSource: "self",
			ReviewedAt: env.now, ReviewDay: day, StateBefore: 0,
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
	if visible.ReviewsTotal != 2 || visible.ReviewsToday != 2 {
		t.Fatalf("Stats (granted) reviews = %+v, want total=2 today=2", visible)
	}
	if visible.Retention != 0.5 {
		t.Fatalf("Stats (granted) retention = %v, want 0.5", visible.Retention)
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
	if revoked.ReviewsTotal != 2 || revoked.ReviewsToday != 2 {
		t.Fatalf("Stats (revoked) reviews = %+v, want total=2 today=2 (history preserved)", revoked)
	}
	if revoked.Retention != 0.5 {
		t.Fatalf("Stats (revoked) retention = %v, want 0.5 (history preserved)", revoked.Retention)
	}
}
