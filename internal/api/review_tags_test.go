package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedTaggedNewCard 建一个带标签的 note 与它的一张 card（无 card_states，即新卡），返回 note 的对外 id。
func seedTaggedNewCard(t *testing.T, db *gorm.DB, deckID uint64, tags ...string) string {
	t.Helper()
	now := time.Now().UTC()
	n := store.Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`,
		TagsJSON: store.TagsJSON(tags), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return n.PublicID
}

// TestReviewTags 覆盖标签与卡组范围组合的唯一校验：规范化后为空等同不带标签；
// 带标签时必须恰好一个卡组；标签数有上限。
func TestReviewTags(t *testing.T) {
	tooMany := make([]string, reviewMaxTags+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("t%d", i)
	}
	cases := []struct {
		name    string
		decks   []uint64
		tags    []string
		want    []string
		wantErr bool
	}{
		{"no tags, any scope", nil, nil, nil, false},
		{"blank tags are dropped", []uint64{1, 2}, []string{" ", ""}, nil, false},
		{"one deck, trimmed and deduped", []uint64{1}, []string{" 创新 ", "创新", "文化传承"}, []string{"创新", "文化传承"}, false},
		{"tags without a deck", nil, []string{"创新"}, nil, true},
		{"tags across two decks", []uint64{1, 2}, []string{"创新"}, nil, true},
		{"too many tags", []uint64{1}, tooMany, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReviewTags(tc.decks, tc.tags)
			if tc.wantErr {
				var se *ServiceError
				if !errors.As(err, &se) || se.Status != http.StatusBadRequest || se.Code != CodeInvalidRequest {
					t.Fatalf("ReviewTags() error = %v, want 400 invalid_request", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReviewTags() error = %v", err)
			}
			if len(got) != len(tc.want) || (len(got) > 0 && !reflect.DeepEqual(got, tc.want)) {
				t.Fatalf("ReviewTags() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDueCardsFiltersByTagREST 是 REST 验收：重复的 tag 参数取并集，只返回带有这些标签的卡。
func TestDueCardsFiltersByTagREST(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "tagreviewer", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	culture := seedTaggedNewCard(t, env.db, deck.ID, "文化传承")
	innov := seedTaggedNewCard(t, env.db, deck.ID, "创新", "高频")
	seedTaggedNewCard(t, env.db, deck.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeReview}, nil)
	router := env.router()

	cases := []struct {
		name string
		tags []string
		want []string
	}{
		{"one tag", []string{"文化传承"}, []string{culture}},
		{"union", []string{"文化传承", "高频"}, []string{culture, innov}},
		{"unknown tag", []string{"不存在"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{"deck": {deck.PublicID}, "tag": tc.tags, "limit": {"50"}}
			status, raw := doJSON(t, router, http.MethodGet, "/api/v1/review/due?"+q.Encode(), k.Plaintext, "")
			if status != http.StatusOK {
				t.Fatalf("GET /review/due status = %d, want 200 (body %s)", status, raw)
			}
			var body struct {
				Cards []DueCard `json:"cards"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("response is not JSON: %v (%s)", err, raw)
			}
			got := make([]string, 0, len(body.Cards))
			for _, c := range body.Cards {
				got = append(got, c.NoteID)
			}
			sort.Strings(got)
			want := append([]string{}, tc.want...)
			sort.Strings(want)
			if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
				t.Fatalf("notes in queue = %v, want %v", got, want)
			}
		})
	}
}

// TestDueCardsTagScopeNegatives 覆盖负例：带 tag 却没有卡组或跨多个卡组时返回 400，
// 而不是静默忽略标签或把不同卡组的同名标签合在一起。
func TestDueCardsTagScopeNegatives(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "tagneg", store.RoleUser)
	deckA := seedDeck(t, env.db, user.ID)
	deckB := seedDeck(t, env.db, user.ID)
	seedTaggedNewCard(t, env.db, deckA.ID, "创新")
	seedTaggedNewCard(t, env.db, deckB.ID, "创新")
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeReview}, nil)
	router := env.router()

	for _, q := range []url.Values{
		{"tag": {"创新"}},
		{"deck": {deckA.PublicID, deckB.PublicID}, "tag": {"创新"}},
	} {
		status, raw := doJSON(t, router, http.MethodGet, "/api/v1/review/due?"+q.Encode(), k.Plaintext, "")
		if status != http.StatusBadRequest {
			t.Errorf("GET /review/due?%s status = %d, want 400 (body %s)", q.Encode(), status, raw)
			continue
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.Error.Code != CodeInvalidRequest {
			t.Errorf("GET /review/due?%s error code = %q, want %q (body %s)", q.Encode(), body.Error.Code, CodeInvalidRequest, raw)
		}
	}
}

// TestListDeckTagsREST 覆盖卡组标签列表：本人卡组 200 并带 note 数；别人的私有卡组 403；
// 缺 read scope 的 key 403；未知卡组 404。
func TestListDeckTagsREST(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	owner := seedUser(t, env.db, "tagowner", store.RoleUser)
	stranger := seedUser(t, env.db, "tagstranger", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	seedTaggedNewCard(t, env.db, deck.ID, "创新", "文化传承")
	seedTaggedNewCard(t, env.db, deck.ID, "创新")
	ownerRead := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil)
	ownerReviewOnly := seedKey(t, env.keys, owner.ID, []string{store.ScopeReview}, nil)
	strangerRead := seedKey(t, env.keys, stranger.ID, []string{store.ScopeRead}, nil)
	router := env.router()
	path := "/api/v1/decks/" + deck.PublicID + "/tags"

	status, raw := doJSON(t, router, http.MethodGet, path, ownerRead.Plaintext, "")
	if status != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200 (body %s)", path, status, raw)
	}
	var body struct {
		Tags []store.TagCount `json:"tags"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, raw)
	}
	want := []store.TagCount{{Tag: "创新", Notes: 2}, {Tag: "文化传承", Notes: 1}}
	if !reflect.DeepEqual(body.Tags, want) {
		t.Fatalf("tags = %+v, want %+v", body.Tags, want)
	}

	negatives := []struct {
		name string
		path string
		key  string
		want int
	}{
		{"stranger cannot read a private deck", path, strangerRead.Plaintext, http.StatusForbidden},
		{"key without read scope", path, ownerReviewOnly.Plaintext, http.StatusForbidden},
		{"unknown deck", "/api/v1/decks/zzzzzzzzzzzz/tags", ownerRead.Plaintext, http.StatusNotFound},
	}
	for _, tc := range negatives {
		status, raw := doJSON(t, router, http.MethodGet, tc.path, tc.key, "")
		if status != tc.want {
			t.Errorf("%s: status = %d, want %d (body %s)", tc.name, status, tc.want, raw)
		}
	}
}
