package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// pauseEnv builds an owner with two decks, "shared" granted to a reader member and "solo"; each deck holds one new card
// and one review card due now for both users.
type pauseEnv struct {
	env                     *testEnv
	owner, member, stranger *store.User
	shared, solo            *store.Deck
}

func newPauseEnv(t *testing.T) pauseEnv {
	t.Helper()
	env := newTestEnv(t, 600, 600)
	ctx := context.Background()
	p := pauseEnv{
		env:      env,
		owner:    seedUser(t, env.db, "pause-owner", store.RoleUser),
		member:   seedUser(t, env.db, "pause-member", store.RoleUser),
		stranger: seedUser(t, env.db, "pause-stranger", store.RoleUser),
	}
	p.shared = seedDeck(t, env.db, p.owner.ID)
	p.solo = seedDeck(t, env.db, p.owner.ID)
	if err := store.NewGrantStore(env.db).Grant(ctx, p.shared.ID, p.member.ID, store.RoleReader, &p.owner.ID); err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	for _, d := range []*store.Deck{p.shared, p.solo} {
		seedTaggedNewCard(t, env.db, d.ID)
		seedTaggedNewCard(t, env.db, d.ID)
		var card store.Card
		if err := env.db.Joins("JOIN notes ON notes.id = cards.note_id").Where("notes.deck_id = ?", d.ID).
			Order("cards.id").Take(&card).Error; err != nil {
			t.Fatalf("load card: %v", err)
		}
		due := env.now.Add(-time.Hour)
		for _, u := range []*store.User{p.owner, p.member} {
			if err := env.db.Create(&store.CardState{CardID: card.ID, UserID: u.ID, State: "review", DueAt: &due}).Error; err != nil {
				t.Fatalf("create card state: %v", err)
			}
		}
	}
	return p
}

func (p pauseEnv) key(t *testing.T, u *store.User, scopes ...string) string {
	t.Helper()
	return seedKey(t, p.env.keys, u.ID, scopes, nil).Plaintext
}

func (p pauseEnv) setPaused(t *testing.T, key string, d *store.Deck, body string) (int, []byte) {
	t.Helper()
	return doJSON(t, p.env.router(), http.MethodPut, "/api/v1/decks/"+d.PublicID+"/paused", key, body)
}

// defaultDueDecks returns how many cards of each deck the whole-library queue serves.
func (p pauseEnv) defaultDueDecks(t *testing.T, key string) map[string]int {
	t.Helper()
	status, raw := doJSON(t, p.env.router(), http.MethodGet, "/api/v1/review/due?limit=500", key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /review/due status = %d (body %s)", status, raw)
	}
	var body struct {
		Cards []DueCard `json:"cards"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("due response is not JSON: %v", err)
	}
	out := map[string]int{}
	for _, c := range body.Cards {
		out[c.DeckID]++
	}
	return out
}

func (p pauseEnv) pausedFlags(t *testing.T, key string) map[string]bool {
	t.Helper()
	status, raw := doJSON(t, p.env.router(), http.MethodGet, "/api/v1/decks", key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /decks status = %d (body %s)", status, raw)
	}
	var body struct {
		Decks []DeckResponse `json:"decks"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decks response is not JSON: %v", err)
	}
	out := map[string]bool{}
	for _, d := range body.Decks {
		out[d.ID] = d.Paused
	}
	return out
}

func (p pauseEnv) summaryDue(t *testing.T, key string) int64 {
	t.Helper()
	status, raw := doJSON(t, p.env.router(), http.MethodGet, "/api/v1/stats/summary", key, "")
	if status != http.StatusOK {
		t.Fatalf("GET /stats/summary status = %d (body %s)", status, raw)
	}
	var body StatsSummary
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("summary response is not JSON: %v", err)
	}
	return body.Due
}

func (p pauseEnv) auditCount(t *testing.T, action string, d *store.Deck) int64 {
	t.Helper()
	var n int64
	if err := p.env.db.Model(&store.AuditLog{}).Where("action = ? AND target_id = ?", action, d.ID).Count(&n).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	return n
}

// TestDeckPauseREST is the acceptance over REST: pausing is idempotent and audited once per change, the deck list reports it,
// the default queue and the summary due count drop the deck, naming the deck still studies it, and resuming restores everything.
func TestDeckPauseREST(t *testing.T) {
	p := newPauseEnv(t)
	ownerKey := p.key(t, p.owner, store.ScopeRead, store.ScopeReview)

	if got := p.defaultDueDecks(t, ownerKey); got[p.solo.PublicID] == 0 || got[p.shared.PublicID] == 0 {
		t.Fatalf("default queue before pausing = %v, want both decks", got)
	}
	dueBefore := p.summaryDue(t, ownerKey)
	if dueBefore != 2 {
		t.Fatalf("summary due before pausing = %d, want 2", dueBefore)
	}

	for i := 0; i < 2; i++ {
		status, raw := p.setPaused(t, ownerKey, p.solo, `{"paused":true}`)
		if status != http.StatusOK {
			t.Fatalf("pause #%d status = %d (body %s)", i+1, status, raw)
		}
		var got DeckPauseStatus
		if err := json.Unmarshal(raw, &got); err != nil || got != (DeckPauseStatus{DeckID: p.solo.PublicID, Paused: true}) {
			t.Fatalf("pause #%d body = %s, want deck_id %s paused true", i+1, raw, p.solo.PublicID)
		}
	}
	if n := p.auditCount(t, store.ActionDeckPause, p.solo); n != 1 {
		t.Errorf("deck.pause audit rows after pausing twice = %d, want 1", n)
	}
	if flags := p.pausedFlags(t, ownerKey); !flags[p.solo.PublicID] || flags[p.shared.PublicID] {
		t.Errorf("GET /decks paused flags = %v, want only %s paused", flags, p.solo.PublicID)
	}
	if got := p.defaultDueDecks(t, ownerKey); got[p.solo.PublicID] != 0 || got[p.shared.PublicID] == 0 {
		t.Errorf("default queue after pausing = %v, want only %s", got, p.shared.PublicID)
	}
	if got := dueCount(t, p.env, p.solo, ownerKey); got == 0 {
		t.Error("naming the paused deck returned no cards, want its cards")
	}
	if got := p.summaryDue(t, ownerKey); got != dueBefore-1 {
		t.Errorf("summary due after pausing = %d, want %d", got, dueBefore-1)
	}

	for i := 0; i < 2; i++ {
		if status, raw := p.setPaused(t, ownerKey, p.solo, `{"paused":false}`); status != http.StatusOK {
			t.Fatalf("resume #%d status = %d (body %s)", i+1, status, raw)
		}
	}
	if n := p.auditCount(t, store.ActionDeckResume, p.solo); n != 1 {
		t.Errorf("deck.resume audit rows after resuming twice = %d, want 1", n)
	}
	if got := p.summaryDue(t, ownerKey); got != dueBefore {
		t.Errorf("summary due after resuming = %d, want %d", got, dueBefore)
	}
	if flags := p.pausedFlags(t, ownerKey); flags[p.solo.PublicID] {
		t.Errorf("GET /decks after resuming = %v, want %s active", flags, p.solo.PublicID)
	}
}

// TestDeckPauseIsPerUser: a reader member pauses the shared deck for themselves; the owner's list, queue and due count do not change.
func TestDeckPauseIsPerUser(t *testing.T) {
	p := newPauseEnv(t)
	ownerKey := p.key(t, p.owner, store.ScopeRead, store.ScopeReview)
	memberKey := p.key(t, p.member, store.ScopeRead, store.ScopeReview)

	if status, raw := p.setPaused(t, memberKey, p.shared, `{"paused":true}`); status != http.StatusOK {
		t.Fatalf("member pause status = %d (body %s)", status, raw)
	}
	if flags := p.pausedFlags(t, memberKey); !flags[p.shared.PublicID] {
		t.Errorf("member GET /decks = %v, want the shared deck paused", flags)
	}
	if got := p.defaultDueDecks(t, memberKey); len(got) != 0 {
		t.Errorf("member default queue = %v, want empty", got)
	}
	if flags := p.pausedFlags(t, ownerKey); flags[p.shared.PublicID] {
		t.Errorf("owner GET /decks = %v, want the shared deck active", flags)
	}
	if got := p.defaultDueDecks(t, ownerKey); got[p.shared.PublicID] == 0 {
		t.Errorf("owner default queue = %v, want the shared deck", got)
	}
	if got := p.summaryDue(t, ownerKey); got != 2 {
		t.Errorf("owner summary due = %d, want 2", got)
	}
}

// TestDeckPauseNegatives covers the rejected requests; none of them may write a pause row.
func TestDeckPauseNegatives(t *testing.T) {
	p := newPauseEnv(t)
	reviewKey := p.key(t, p.owner, store.ScopeReview)
	readWriteKey := p.key(t, p.owner, store.ScopeRead, store.ScopeWrite)
	strangerKey := p.key(t, p.stranger, store.ScopeReview)
	path := "/api/v1/decks/" + p.solo.PublicID + "/paused"

	cases := []struct {
		name, path, key, body string
		wantStatus            int
		wantCode              string
	}{
		{"key without review scope", path, readWriteKey, `{"paused":true}`, http.StatusForbidden, CodeScopeRequired},
		{"stranger cannot pause", path, strangerKey, `{"paused":true}`, http.StatusForbidden, CodeForbidden},
		{"missing paused field", path, reviewKey, `{}`, http.StatusBadRequest, CodeInvalidRequest},
		{"empty body", path, reviewKey, ``, http.StatusBadRequest, CodeInvalidRequest},
		{"paused is not a boolean", path, reviewKey, `{"paused":"yes"}`, http.StatusBadRequest, CodeInvalidRequest},
		{"unknown deck", "/api/v1/decks/01920000-0000-7000-8000-000000000000/paused", reviewKey, `{"paused":true}`, http.StatusNotFound, CodeNotFound},
		{"anonymous", path, "", `{"paused":true}`, http.StatusUnauthorized, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := doJSON(t, p.env.router(), http.MethodPut, tc.path, tc.key, tc.body)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", status, tc.wantStatus, raw)
			}
			if tc.wantCode == "" {
				return
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
	if err := p.env.db.Model(&store.DeckPause{}).Count(&rows).Error; err != nil {
		t.Fatalf("count pauses: %v", err)
	}
	if rows != 0 {
		t.Errorf("pause rows after rejected requests = %d, want 0", rows)
	}
}
