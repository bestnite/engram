package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// deckSettingsPath returns the path of the SPA deck settings API.
func deckSettingsPath(deckPublicID string) string {
	return "/api/v1/decks/" + deckPublicID + "/settings"
}

// decodeDeckSettings decodes the response body; terminates the test on failure.
func decodeDeckSettings(t *testing.T, raw []byte) deckSettingsResponse {
	t.Helper()
	var body deckSettingsResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode SPA deck settings: %v", err)
	}
	return body
}

// seedQueueCards writes n new cards and m due review cards, for the queue/quota assertions.
func seedQueueCards(t *testing.T, db *gorm.DB, ownerID, deckID uint64, newCards, dueCards int) {
	t.Helper()
	now := time.Now().UTC()
	for i := 0; i < newCards; i++ {
		seedCardRow(t, db, deckID, "new", now.Add(time.Duration(i)*time.Minute))
	}
	stability, difficulty := 5.0, 5.0
	last := now.Add(-48 * time.Hour)
	for i := 0; i < dueCards; i++ {
		id := seedCardRow(t, db, deckID, "due", now)
		due := now.Add(-time.Hour)
		if err := db.Create(&store.CardState{CardID: id, UserID: ownerID, State: "review",
			DueAt: &due, Stability: &stability, Difficulty: &difficulty, LastReviewAt: &last}).Error; err != nil {
			t.Fatalf("create card state: %v", err)
		}
	}
}

// TestDeckSettingsOwnerViewReportsCapsAndUsage is the read acceptance test for the settings API: what the owner gets for
// new_per_day / reviews_per_day are the raw DB values, and today's used/remaining comes from schedule.DeckBudgets.
func TestDeckSettingsOwnerViewReportsCapsAndUsage(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA settings deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 5, ReviewsPerDay: 10}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	// 2 new cards introduced today and 3 reviews → 3 new cards remaining, 7 reviews remaining.
	seedTodayUsage(t, db, ownerID, deck.ID, 2, 3)

	rec := getWithCookies(t, srv, deckSettingsPath(deck.PublicID), cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %s)", deckSettingsPath(deck.PublicID), rec.Code, snippet(rec.Body.String()))
	}
	body := decodeDeckSettings(t, rec.Body.Bytes())
	if body.DeckID != deck.PublicID || body.DeckName != "SPA settings deck" {
		t.Errorf("deck identity = %s/%q, want %s/%q", body.DeckID, body.DeckName, deck.PublicID, deck.Name)
	}
	if body.NewPerDay != 5 || body.ReviewsPerDay != 10 {
		t.Errorf("caps = %d/%d, want 5/10 (verbatim column values)", body.NewPerDay, body.ReviewsPerDay)
	}
	if body.NewUsed != 2 || body.ReviewUsed != 3 {
		t.Errorf("used = %d/%d, want 2/3", body.NewUsed, body.ReviewUsed)
	}
	if body.NewLeft != 3 || body.ReviewLeft != 7 {
		t.Errorf("left = %d/%d, want 3/7 (cap minus used)", body.NewLeft, body.ReviewLeft)
	}
	if body.NewUnlimited || body.ReviewUnlimited {
		t.Errorf("unlimited = %v/%v, want false/false for non-zero caps", body.NewUnlimited, body.ReviewUnlimited)
	}
}

// TestDeckSettingsUpdateTakesEffectAndAudits is the write acceptance test: after a PATCH lowers the caps, the two DB columns
// immediately become the submitted values, the queue shrinks accordingly, and a deck.caps_change audit row is left behind.
func TestDeckSettingsUpdateTakesEffectAndAudits(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA capped deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 5, ReviewsPerDay: 5}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	seedQueueCards(t, db, ownerID, deck.ID, 5, 3)
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 5 || r != 3 {
		t.Fatalf("before update queue = %d new / %d review, want 5/3", n, r)
	}

	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		`{"new_per_day":2,"reviews_per_day":1}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 2 || caps.ReviewsPerDay != 1 {
		t.Fatalf("stored caps = %d/%d, want 2/1", caps.NewPerDay, caps.ReviewsPerDay)
	}
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 2 || r != 1 {
		t.Errorf("after update queue = %d new / %d review, want 2/1 (effective immediately)", n, r)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckCaps, n)
	}
	body := decodeDeckSettings(t, rec.Body.Bytes())
	if body.NewPerDay != 2 || body.ReviewsPerDay != 1 {
		t.Errorf("PATCH response caps = %d/%d, want 2/1", body.NewPerDay, body.ReviewsPerDay)
	}
}

// TestDeckSettingsZeroRoundTripsAsUnlimited asserts 0 is persisted as-is and explicitly expressed as "unlimited":
// the response has new_per_day=0 and unlimited=true, and the queue is no longer capped.
func TestDeckSettingsZeroRoundTripsAsUnlimited(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA unlimited deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 1, ReviewsPerDay: 1}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	seedQueueCards(t, db, ownerID, deck.ID, 5, 3)
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 1 || r != 1 {
		t.Fatalf("before update queue = %d new / %d review, want 1/1", n, r)
	}

	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		`{"new_per_day":0,"reviews_per_day":0}`, cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH 0/0 = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	// 0 must be stored as 0 as-is and must not be overridden by the store defaults (20/200).
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 0 || caps.ReviewsPerDay != 0 {
		t.Fatalf("stored caps = %d/%d, want 0/0 (0 means unlimited, not the default)", caps.NewPerDay, caps.ReviewsPerDay)
	}
	body := decodeDeckSettings(t, rec.Body.Bytes())
	if body.NewPerDay != 0 || body.ReviewsPerDay != 0 {
		t.Errorf("response caps = %d/%d, want 0/0", body.NewPerDay, body.ReviewsPerDay)
	}
	if !body.NewUnlimited || !body.ReviewUnlimited {
		t.Errorf("unlimited = %v/%v, want true/true (0 must be expressed explicitly)", body.NewUnlimited, body.ReviewUnlimited)
	}
	if body.NewLeft != 0 || body.ReviewLeft != 0 {
		t.Errorf("left = %d/%d, want 0/0 with unlimited set", body.NewLeft, body.ReviewLeft)
	}
	if n, r := deckQueueCountsNow(t, srv, db, ownerID, deck.ID); n != 5 || r != 3 {
		t.Errorf("after update queue = %d new / %d review, want 5/3 (unlimited)", n, r)
	}
}

// TestDeckSettingsRejectsNonMember is a mandatory negative case: a user unrelated to the deck can neither read nor change it, the column values stay unchanged,
// and no deck.caps_change audit row is written.
func TestDeckSettingsRejectsNonMember(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA owned deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 7, ReviewsPerDay: 8}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	_, u2Cookies, u2CSRF := createUserAndLogin(t, srv, db, "settings_intruder")

	if rec := getWithCookies(t, srv, deckSettingsPath(deck.PublicID), u2Cookies); rec.Code != http.StatusForbidden {
		t.Errorf("non-owner GET = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		`{"new_per_day":0,"reviews_per_day":0}`, u2Cookies, u2CSRF)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner PATCH = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 7 || caps.ReviewsPerDay != 8 {
		t.Errorf("caps changed by a non-owner: got %d/%d, want 7/8", caps.NewPerDay, caps.ReviewsPerDay)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 0 {
		t.Errorf("deck.caps_change audit rows = %d, want 0 after a denied write", n)
	}
}

// TestDeckSettingsRequiresCSRF is a mandatory negative case: a PATCH without CSRF is stopped by the middleware and the column values stay unchanged.
func TestDeckSettingsRequiresCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA csrf deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 4, ReviewsPerDay: 4}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		`{"new_per_day":0,"reviews_per_day":0}`, cookies, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH without CSRF = %d, want 403 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 4 || caps.ReviewsPerDay != 4 {
		t.Errorf("caps changed despite missing CSRF: got %d/%d, want 4/4", caps.NewPerDay, caps.ReviewsPerDay)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 0 {
		t.Errorf("deck.caps_change audit rows = %d, want 0 when CSRF is missing", n)
	}
}

// TestDeckSettingsRejectsInvalidInput is a mandatory negative case: non-numeric / negative / missing field / empty body are always
// 400, with no DB write and no audit row.
func TestDeckSettingsRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "negative new", body: `{"new_per_day":-1,"reviews_per_day":5}`},
		{name: "negative reviews", body: `{"new_per_day":5,"reviews_per_day":-2}`},
		{name: "not a number", body: `{"new_per_day":"abc","reviews_per_day":5}`},
		{name: "missing new", body: `{"reviews_per_day":5}`},
		{name: "missing both", body: `{}`},
		{name: "empty body", body: ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, ownerID, cookies, csrf := newNotesServer(t)
			deck := seedReviewDeck(t, db, ownerID, "SPA invalid deck")
			if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
				store.DeckCaps{NewPerDay: 6, ReviewsPerDay: 6}); err != nil {
				t.Fatalf("SetCaps: %v", err)
			}
			rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID), tc.body, cookies, csrf)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PATCH %s = %d, want 400 (body %s)", tc.body, rec.Code, snippet(rec.Body.String()))
			}
			if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 6 || caps.ReviewsPerDay != 6 {
				t.Errorf("caps changed on invalid input: got %d/%d, want 6/6", caps.NewPerDay, caps.ReviewsPerDay)
			}
			// A rejected request has zero side effects: not even an audit row should be written.
			if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckCaps); err != nil {
				t.Fatalf("count audit rows: %v", err)
			} else if n != 0 {
				t.Errorf("deck.caps_change audit rows = %d, want 0 on a rejected update", n)
			}
		})
	}
}

// TestDeckSettingsRequiresSession asserts anonymous access gets 401 rather than an SSR page's 303 redirect.
func TestDeckSettingsRequiresSession(t *testing.T) {
	srv, db, ownerID, _, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA anon deck")
	rec := getWithCookies(t, srv, deckSettingsPath(deck.PublicID), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET = %d, want 401 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "unauthorized" {
		t.Errorf("anonymous error code = %q, want unauthorized", body.Error.Code)
	}
}

// TestDeckSettingsSwitchesPreset is the write acceptance test for switching a deck's schedule: when a PATCH carries preset_id the deck is scheduled by
// the new preset, the response echoes the new value, and a deck.preset_change audit row is left behind.
func TestDeckSettingsSwitchesPreset(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA switchable deck")
	second := store.NewPreset(ownerID, "second preset")
	if err := store.NewPresetStore(db).Create(context.Background(), &second); err != nil {
		t.Fatalf("create second preset: %v", err)
	}

	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		fmt.Sprintf(`{"new_per_day":2,"reviews_per_day":3,"preset_id":"%s"}`, second.PublicID), cookies, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := decodeDeckSettings(t, rec.Body.Bytes())
	if body.PresetID != second.PublicID {
		t.Errorf("response preset_id = %s, want %s", body.PresetID, second.PublicID)
	}
	stored, err := store.NewDeckStore(db).ByID(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("read deck: %v", err)
	}
	if stored.PresetID != second.ID {
		t.Errorf("stored preset_id = %d, want %d", stored.PresetID, second.ID)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckPreset); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 1 {
		t.Errorf("audit rows for %s = %d, want 1", store.ActionDeckPreset, n)
	}
}

// TestDeckSettingsRejectsForeignPreset is a mandatory negative case: attaching someone else's preset would also read out their tuned
// scheduling parameters, so it must be rejected with 400, with the deck's preset_id not moving a single byte and no audit row written.
func TestDeckSettingsRejectsForeignPreset(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA foreign preset deck")
	intruder := store.User{Username: "preset_intruder", Email: "preset_intruder@example.com",
		DisplayName: "intruder", Role: store.RoleUser, Status: store.StatusActive,
		Locale: "zh-CN", Timezone: "Asia/Shanghai", CreatedAt: time.Now().UTC()}
	if err := db.Create(&intruder).Error; err != nil {
		t.Fatalf("create intruder: %v", err)
	}
	foreign := store.NewPreset(intruder.ID, "foreign preset")
	if err := store.NewPresetStore(db).Create(context.Background(), &foreign); err != nil {
		t.Fatalf("create foreign preset: %v", err)
	}

	rec := jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		fmt.Sprintf(`{"new_per_day":2,"reviews_per_day":3,"preset_id":"%s"}`, foreign.PublicID), cookies, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH foreign preset = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if envelope.Error.Code != "invalid_request" {
		t.Errorf("error code = %q, want invalid_request", envelope.Error.Code)
	}
	stored, err := store.NewDeckStore(db).ByID(context.Background(), deck.ID)
	if err != nil {
		t.Fatalf("read deck: %v", err)
	}
	if stored.PresetID != deck.PresetID {
		t.Errorf("stored preset_id = %d, want %d (unchanged after rejection)", stored.PresetID, deck.PresetID)
	}
	if n, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionDeckPreset); err != nil {
		t.Fatalf("count audit rows: %v", err)
	} else if n != 0 {
		t.Errorf("audit rows for %s = %d, want 0 (no write happened)", store.ActionDeckPreset, n)
	}
}

// TestDeckSettingsMemberEditsOwnSettings asserts a sharing member can open the deck settings and change **their own** study settings:
// the member sees their own default preset and default caps, the change writes only the member's own settings, and the owner's deck columns and preset stay unchanged;
// a member attaching the owner's preset is rejected (the counter-case).
func TestDeckSettingsMemberEditsOwnSettings(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "SPA shared settings deck")
	if err := store.NewDeckStore(db).SetCaps(context.Background(), ownerID, deck.ID,
		store.DeckCaps{NewPerDay: 7, ReviewsPerDay: 8}); err != nil {
		t.Fatalf("SetCaps: %v", err)
	}
	memberID, memberCookies, memberCSRF := createUserAndLogin(t, srv, db, "settings_member")
	grantRole(t, srv, deck.ID, memberID, store.RoleReader, ownerCookies, ownerCSRF)

	// The page path is open to members too (they get the app shell).
	assertShell(t, getWithCookies(t, srv, "/decks/"+deck.PublicID+"/settings", memberCookies))

	rec := getWithCookies(t, srv, deckSettingsPath(deck.PublicID), memberCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("member GET = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	got := decodeDeckSettings(t, rec.Body.Bytes())
	ownerPresetPublic := srv.presetPublicID(context.Background(), deck.PresetID)
	if got.Role != store.RoleReader || got.PresetID == ownerPresetPublic || got.NewPerDay != store.DefaultNewPerDay {
		t.Fatalf("member view = %+v, want role reader, the member's own preset and default caps", got)
	}

	rec = jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		`{"new_per_day":2,"reviews_per_day":0}`, memberCookies, memberCSRF)
	if rec.Code != http.StatusOK {
		t.Fatalf("member PATCH = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if after := decodeDeckSettings(t, rec.Body.Bytes()); after.NewPerDay != 2 || !after.ReviewUnlimited {
		t.Errorf("member settings after PATCH = %+v, want 2 new per day and unlimited reviews", after)
	}
	if caps := deckCapsFromDB(t, db, deck.ID); caps.NewPerDay != 7 || caps.ReviewsPerDay != 8 {
		t.Errorf("owner caps changed by the member: got %d/%d, want 7/8", caps.NewPerDay, caps.ReviewsPerDay)
	}

	rec = jsonRequest(t, srv, http.MethodPatch, deckSettingsPath(deck.PublicID),
		fmt.Sprintf(`{"new_per_day":2,"reviews_per_day":0,"preset_id":%q}`, ownerPresetPublic), memberCookies, memberCSRF)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("member attaching the owner's preset = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	var reloaded store.Deck
	if err := db.First(&reloaded, deck.ID).Error; err != nil {
		t.Fatalf("reload deck: %v", err)
	}
	if reloaded.PresetID != deck.PresetID {
		t.Errorf("owner preset changed to %d by the member", reloaded.PresetID)
	}
}
