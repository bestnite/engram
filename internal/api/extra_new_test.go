package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// extraNewEnv 建一个每日新卡上限为 2、共 5 张新卡的卡组，并把 2 张记为今天已引入，
// 即今天的学习目标已经完成；返回环境、属主与卡组。
func extraNewEnv(t *testing.T) (*testEnv, *store.User, *store.Deck) {
	t.Helper()
	env := newTestEnv(t, 600, 600)
	ctx := context.Background()
	owner := seedUser(t, env.db, "extra-owner", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	if err := store.NewDeckStore(env.db).SetCaps(ctx, owner.ID, deck.ID, store.DeckCaps{NewPerDay: 2}); err != nil {
		t.Fatalf("SetCaps() error = %v", err)
	}
	for i := 0; i < 5; i++ {
		seedTaggedNewCard(t, env.db, deck.ID, "t")
	}
	introduceToday(t, env, owner, deck.ID, 2)
	return env, owner, deck
}

// introduceToday 把卡组里 n 张新卡记为 user 今天已引入（写一行 state_before=0 的复习日志与复习态）。
func introduceToday(t *testing.T, env *testEnv, user *store.User, deckID uint64, n int) {
	t.Helper()
	var cards []store.Card
	if err := env.db.Joins("JOIN notes ON notes.id = cards.note_id").Where("notes.deck_id = ?", deckID).
		Order("cards.id").Limit(n).Find(&cards).Error; err != nil {
		t.Fatalf("load cards: %v", err)
	}
	day := env.api.reviewDay(user)
	for _, c := range cards {
		if err := env.db.Create(&store.Review{CardID: c.ID, UserID: user.ID, Rating: 3, GradeSource: "self",
			ReviewedAt: env.now, ReviewDay: day, StateBefore: 0}).Error; err != nil {
			t.Fatalf("create review: %v", err)
		}
		due := env.now.AddDate(0, 0, 3)
		if err := env.db.Create(&store.CardState{CardID: c.ID, UserID: user.ID, State: "review", DueAt: &due}).Error; err != nil {
			t.Fatalf("create card state: %v", err)
		}
	}
}

func decodeExtraNew(t *testing.T, raw []byte) ExtraNewStatus {
	t.Helper()
	var s ExtraNewStatus
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("response is not ExtraNewStatus JSON: %v (%s)", err, raw)
	}
	return s
}

func dueCount(t *testing.T, env *testEnv, deck *store.Deck, key string) int {
	t.Helper()
	q := url.Values{"deck": {deck.PublicID}, "limit": {"500"}}
	status, raw := doJSON(t, env.router(), http.MethodGet, "/api/v1/review/due?"+q.Encode(), key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /review/due status = %d (body %s)", status, raw)
	}
	var body struct {
		Cards []DueCard `json:"cards"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("due response is not JSON: %v", err)
	}
	return len(body.Cards)
}

// TestExtraNewREST 是验收：学完今天的目标后 GET 给出默认张数（每日上限，但不超过剩下的新卡），
// POST 缺省 count 追加每日上限、显式 count 按输入追加，队列随之放出新卡，并写一条审计。
func TestExtraNewREST(t *testing.T) {
	env, owner, deck := extraNewEnv(t)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeReview}, nil).Plaintext
	router := env.router()
	path := "/api/v1/decks/" + deck.PublicID + "/extra-new"

	if got := dueCount(t, env, deck, key); got != 0 {
		t.Fatalf("queue before extra = %d cards, want 0", got)
	}
	status, raw := doJSON(t, router, http.MethodGet, path, key, "")
	if status != http.StatusOK {
		t.Fatalf("GET extra-new status = %d (body %s)", status, raw)
	}
	s := decodeExtraNew(t, raw)
	want := ExtraNewStatus{DeckID: deck.PublicID, NewPerDay: 2, NewUsed: 2, NewLeft: 0, NewAvailable: 3, SuggestedCount: 2, MaxCount: ExtraNewMaxCount}
	if s != want {
		t.Fatalf("GET extra-new = %+v, want %+v", s, want)
	}

	// 缺省 count：追加每日上限 2 张。
	status, raw = doJSON(t, router, http.MethodPost, path, key, "")
	if status != http.StatusOK {
		t.Fatalf("POST extra-new (default) status = %d (body %s)", status, raw)
	}
	if s = decodeExtraNew(t, raw); s.ExtraNew != 2 || s.NewLeft != 2 || s.SuggestedCount != 1 {
		t.Fatalf("after default extra = %+v, want extra 2, left 2, suggested 1", s)
	}
	if got := dueCount(t, env, deck, key); got != 2 {
		t.Fatalf("queue after default extra = %d cards, want 2", got)
	}

	// 显式 count：再追加 1 张，累加到 3。
	status, raw = doJSON(t, router, http.MethodPost, path, key, `{"count":1}`)
	if status != http.StatusOK {
		t.Fatalf("POST extra-new (count 1) status = %d (body %s)", status, raw)
	}
	if s = decodeExtraNew(t, raw); s.ExtraNew != 3 || s.NewLeft != 3 || s.SuggestedCount != 0 {
		t.Fatalf("after count extra = %+v, want extra 3, left 3, suggested 0", s)
	}
	if got := dueCount(t, env, deck, key); got != 3 {
		t.Fatalf("queue after count extra = %d cards, want 3", got)
	}

	var audits int64
	if err := env.db.Model(&store.AuditLog{}).Where("action = ? AND target_id = ?", store.ActionDeckExtraNew, deck.ID).
		Count(&audits).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if audits != 2 {
		t.Errorf("deck.extra_new audit rows = %d, want 2", audits)
	}
}

// TestExtraNewIsPerUser 断言追加只对调用者自己生效：共享卡组的成员追加后，属主的队列不变。
func TestExtraNewIsPerUser(t *testing.T) {
	env, owner, deck := extraNewEnv(t)
	ctx := context.Background()
	member := seedUser(t, env.db, "extra-member", store.RoleUser)
	if err := store.NewGrantStore(env.db).Grant(ctx, deck.ID, member.ID, store.RoleReader, &owner.ID); err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	if err := store.NewDeckStore(env.db).SetStudySettings(ctx, member.ID, deck, nil, &store.DeckCaps{NewPerDay: 1}); err != nil {
		t.Fatalf("SetStudySettings() error = %v", err)
	}
	introduceToday(t, env, member, deck.ID, 1)
	ownerKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeReview}, nil).Plaintext
	memberKey := seedKey(t, env.keys, member.ID, []string{store.ScopeReview}, nil).Plaintext

	status, raw := doJSON(t, env.router(), http.MethodPost, "/api/v1/decks/"+deck.PublicID+"/extra-new", memberKey, `{"count":2}`)
	if status != http.StatusOK {
		t.Fatalf("member POST extra-new status = %d (body %s)", status, raw)
	}
	if got := dueCount(t, env, deck, memberKey); got != 2 {
		t.Errorf("member queue after extra = %d, want 2", got)
	}
	if got := dueCount(t, env, deck, ownerKey); got != 0 {
		t.Errorf("owner queue after member's extra = %d, want 0", got)
	}
}

// TestExtraNewNegatives 覆盖负例：scope 不够、非成员、不限额的卡组、count 越界、坏请求体、
// 带标签但标签过多，以及未知卡组。
func TestExtraNewNegatives(t *testing.T) {
	env, owner, deck := extraNewEnv(t)
	ctx := context.Background()
	stranger := seedUser(t, env.db, "extra-stranger", store.RoleUser)
	unlimited := seedDeck(t, env.db, owner.ID)
	if err := store.NewDeckStore(env.db).SetCaps(ctx, owner.ID, unlimited.ID, store.DeckCaps{NewPerDay: 0}); err != nil {
		t.Fatalf("SetCaps() error = %v", err)
	}
	reviewKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeReview}, nil).Plaintext
	readKey := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	strangerKey := seedKey(t, env.keys, stranger.ID, []string{store.ScopeReview}, nil).Plaintext
	path := "/api/v1/decks/" + deck.PublicID + "/extra-new"
	manyTags := url.Values{}
	for i := 0; i <= reviewMaxTags; i++ {
		manyTags.Add("tag", "t"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}

	cases := []struct {
		name, method, path, key, body string
		wantStatus                    int
		wantCode                      string
	}{
		{"read/write key cannot read", http.MethodGet, path, readKey, "", http.StatusForbidden, CodeScopeRequired},
		{"read/write key cannot add", http.MethodPost, path, readKey, "", http.StatusForbidden, CodeScopeRequired},
		{"stranger cannot read", http.MethodGet, path, strangerKey, "", http.StatusForbidden, CodeForbidden},
		{"stranger cannot add", http.MethodPost, path, strangerKey, `{"count":1}`, http.StatusForbidden, CodeForbidden},
		{"unlimited deck", http.MethodPost, "/api/v1/decks/" + unlimited.PublicID + "/extra-new", reviewKey, "", http.StatusBadRequest, CodeInvalidRequest},
		{"negative count", http.MethodPost, path, reviewKey, `{"count":-1}`, http.StatusBadRequest, CodeInvalidRequest},
		{"count over max", http.MethodPost, path, reviewKey, `{"count":1001}`, http.StatusBadRequest, CodeInvalidRequest},
		{"malformed body", http.MethodPost, path, reviewKey, `{"count":`, http.StatusBadRequest, CodeInvalidRequest},
		{"too many tags", http.MethodGet, path + "?" + manyTags.Encode(), reviewKey, "", http.StatusBadRequest, CodeInvalidRequest},
		{"unknown deck", http.MethodPost, "/api/v1/decks/01920000-0000-7000-8000-000000000000/extra-new", reviewKey, "", http.StatusNotFound, CodeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := doJSON(t, env.router(), tc.method, tc.path, tc.key, tc.body)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", status, tc.wantStatus, raw)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(raw, &body); err != nil || body.Error.Code != tc.wantCode {
				t.Fatalf("error code = %q, want %q (body %s)", body.Error.Code, tc.wantCode, raw)
			}
		})
	}

	var rows int64
	if err := env.db.Model(&store.DeckDailyExtra{}).Count(&rows).Error; err != nil {
		t.Fatalf("count daily extras: %v", err)
	}
	if rows != 0 {
		t.Errorf("daily extra rows after rejected requests = %d, want 0", rows)
	}
}

// TestExtraNewStatusWithTags 断言按标签复习时，可加张数只数带这些标签的新卡。
func TestExtraNewStatusWithTags(t *testing.T) {
	env, owner, deck := extraNewEnv(t)
	seedTaggedNewCard(t, env.db, deck.ID, "other")
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeReview}, nil).Plaintext

	cases := []struct {
		query         string
		wantAvailable int
	}{
		{"", 4},
		{"?tag=other", 1},
		{"?tag=missing", 0},
	}
	for _, tc := range cases {
		status, raw := doJSON(t, env.router(), http.MethodGet, "/api/v1/decks/"+deck.PublicID+"/extra-new"+tc.query, key, "")
		if status != http.StatusOK {
			t.Fatalf("GET extra-new%s status = %d (body %s)", tc.query, status, raw)
		}
		if s := decodeExtraNew(t, raw); s.NewAvailable != tc.wantAvailable {
			t.Errorf("GET extra-new%s new_available = %d, want %d", tc.query, s.NewAvailable, tc.wantAvailable)
		}
	}
}
