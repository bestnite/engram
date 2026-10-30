package web

import (
	"context"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
)

// newStatsServer wires up a test server with sessions, logs in an owner, and seeds the minimal stats fixture.
//
// The fixture covers at least one non-zero value for every stats metric, so the page renders real aggregation results instead of a row of empty bars:
// two reviews today + one review yesterday (review volume and learning curve), one state row with stability and due date
// (due forecast and retention), one note with a tag (tag dimension), one deck (deck dimension).
func newStatsServer(t *testing.T) (srv *Server, db *gorm.DB, ownerID uint64, cookies []*http.Cookie) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, store.NewAPIKeyStore(db), auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	srv, err = New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Decks:         store.NewDeckStore(db),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "owner", Email: "owner@example.com", Password: "Sup3rSecret!", Role: store.RoleAdmin,
		// Aligned with seedStatsFixture's review-day semantics (UTC, midnight cutoff), otherwise "today" drifts
		// with the time of day under the default timezone Asia/Shanghai + 04:00 cutoff.
		Timezone: "UTC", DayCutoffHour: store.Ptr(0),
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	seedStatsFixture(t, db, owner.ID)

	// Login goes through the SPA's same-origin JSON endpoint: failure is Fatal, success returns the session cookie.
	cookies, _ = loginJSON(t, srv, db, "owner", "Sup3rSecret!")
	return srv, db, owner.ID, cookies
}

// seedStatsFixture writes a set of predictable stats fixtures.
func seedStatsFixture(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	today := reviewday.Day(now, time.UTC, 0)
	yesterday := reviewday.Shift(today, -1)
	dueSoon := now.Add(-1 * time.Hour)

	preset := store.NewPreset(userID, "Stats preset")
	if err := store.NewPresetStore(db).Create(ctx, &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{
		OwnerUserID: userID, Name: "Stats deck", Description: "",
		PresetID: preset.ID, CreatedAt: now,
	}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	note := store.Note{
		DeckID: deck.ID, FieldsJSON: `{"front":"f","back":"b"}`,
		TagsJSON: `["algebra"]`, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	card := store.Card{NoteID: note.ID, Template: "forward", CreatedAt: now}
	if err := db.Create(&card).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	stability := 5.0
	state := store.CardState{
		CardID: card.ID, UserID: userID, State: "review", DueAt: &dueSoon, Stability: &stability,
	}
	if err := db.Create(&state).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	// Three reviews: two today (one self-assessed, one machine-graded), one yesterday; stability must be non-null to enter a retention bucket.
	rows := []store.Review{
		{CardID: card.ID, UserID: userID, Rating: 3, GradeSource: "self", ReviewedAt: now, ReviewDay: today, StateBefore: 2, Stability: &stability},
		{CardID: card.ID, UserID: userID, Rating: 4, GradeSource: "typed", ReviewedAt: now, ReviewDay: today, StateBefore: 2, Stability: &stability},
		{CardID: card.ID, UserID: userID, Rating: 1, GradeSource: "self", ReviewedAt: now.Add(-24 * time.Hour), ReviewDay: yesterday, StateBefore: 0},
	}
	ms := 1500
	rows[0].ElapsedMS = &ms
	rows[1].ElapsedMS = &ms
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("create review: %v", err)
		}
	}
}

// TestStatsPageRedirectsAnonymous is a negative case: anonymous access to /stats is still redirected to the login page
// (moving the page to the SPA shell does not change the authorization decision; statsRoute goes through requireUser first).
func TestStatsPageRedirectsAnonymous(t *testing.T) {
	srv, _, _, _ := newStatsServer(t)
	rec := getWithCookies(t, srv, "/stats", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /stats (anonymous) status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("GET /stats (anonymous) Location = %q, want /login", loc)
	}
}

// TestStatsRouteServesShell is the acceptance point for the page migration: GET /stats returns the SPA app shell
// (index.html), and the client router renders the stats page instead of an SSR page.
//
// The deep link (?lang=) is carried through as-is; the shell uses revalidation/no-cache and an ETag to avoid long-lived caching of
// stale asset references. Data is still fetched client-side via GET /api/v1/stats/detail.
func TestStatsRouteServesShell(t *testing.T) {
	srv, _, _, cookies := newStatsServer(t)

	rec := getWithCookies(t, srv, "/stats?lang=en", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stats status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q, want containing no-cache", cc)
	}
	if etag := rec.Header().Get("ETag"); etag == "" {
		t.Errorf("ETag is missing")
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div id="app"></div>`) {
		t.Errorf("GET /stats did not return the SPA shell: %s", snippet(body))
	}
	// The SSR stats page copy must disappear: the page is now owned by the client view.
	if strings.Contains(body, "统计与洞察") {
		t.Errorf("GET /stats still renders the SSR stats page: %s", snippet(body))
	}
}

// TestStatsDetailAPIStillRequiresSession asserts moving the page to the SPA shell does not change the data endpoint's authentication:
// anonymous access to GET /api/v1/stats/detail still returns 401 JSON, and a logged-in session returns 200 JSON.
func TestStatsDetailAPIStillRequiresSession(t *testing.T) {
	srv, _, _, cookies := newStatsServer(t)

	anon := getWithCookies(t, srv, "/api/v1/stats/detail", nil)
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /api/v1/stats/detail status = %d, want 401", anon.Code)
	}
	if ct := anon.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("anonymous 401 Content-Type = %q, want application/json", ct)
	}

	authed := getWithCookies(t, srv, "/api/v1/stats/detail", cookies)
	if authed.Code != http.StatusOK {
		t.Fatalf("authenticated GET /api/v1/stats/detail status = %d, want 200 (body %s)", authed.Code, snippet(authed.Body.String()))
	}
	if !strings.Contains(authed.Body.String(), `"volume"`) {
		t.Errorf("authenticated stats detail body is missing the volume metric: %s", snippet(authed.Body.String()))
	}
}

// TestThirdPartyHostsDetectsPlantedHost self-tests the scanner: a planted CDN reference must be caught,
// otherwise TestStatsPageHasNoExternalRequests could pass falsely because the regex stopped working.
func TestThirdPartyHostsDetectsPlantedHost(t *testing.T) {
	planted := []string{
		`<script src="https://cdn.example.org/chart.js"></script>`,
		`<link rel="stylesheet" href="https://fonts.example.net/css"/>`,
		`<img src="//tracker.example.com/pixel.gif"/>`,
		`<iframe src="https://ads.example.com/frame"></iframe>`,
	}
	for _, html := range planted {
		if hosts := thirdPartyHosts(html); len(hosts) == 0 {
			t.Errorf("thirdPartyHosts() missed the planted host in %q", html)
		}
	}
	if hosts := thirdPartyHosts(`<link rel="stylesheet" href="/static/v/abc/css/tailwind.css"/>`); len(hosts) != 0 {
		t.Errorf("thirdPartyHosts() flagged a relative path as external: %v", hosts)
	}
	// An ordinary navigation link (such as the source repository in the footer) triggers no render-time request and must be let through.
	if hosts := thirdPartyHosts(`<a href="https://git.nite07.com/nite/engram">source</a>`); len(hosts) != 0 {
		t.Errorf("thirdPartyHosts() flagged a plain navigation link as an external request: %v", hosts)
	}
}

// thirdPartyHosts returns the third-party hosts in the HTML that would cause an external network request.
// It only counts the attributes that trigger loading (the src/href/data of script/link/img/iframe/source/video/audio/embed/object/track)
// as well as url(...) in CSS. <a href> is a navigation that only happens when the user clicks,
// not a render-time external request, so it is not counted — the source repository link in the footer is exactly that case.
func thirdPartyHosts(html string) []string {
	var hosts []string
	for _, m := range resourceHostRe.FindAllStringSubmatch(html, -1) {
		hosts = append(hosts, m[1])
	}
	for _, m := range resourceRelativeHostRe.FindAllStringSubmatch(html, -1) {
		hosts = append(hosts, m[1])
	}
	for _, m := range cssURLHostRe.FindAllStringSubmatch(html, -1) {
		hosts = append(hosts, m[1])
	}
	return hosts
}

var (
	// An absolute URL in a resource-loading tag's attribute: it would fire an external request at render time.
	resourceHostRe = regexp.MustCompile(`(?i)<(?:script|link|img|iframe|source|video|audio|embed|object|track)\b[^>]*\b(?:src|href|data)\s*=\s*"https?://([A-Za-z0-9._-]+)`)
	// Same, but in protocol-relative form (//host/...).
	resourceRelativeHostRe = regexp.MustCompile(`(?i)<(?:script|link|img|iframe|source|video|audio|embed|object|track)\b[^>]*\b(?:src|href|data)\s*=\s*"//([A-Za-z0-9._-]+)`)
	// An external host referenced by url(...) in CSS.
	cssURLHostRe = regexp.MustCompile(`(?i)url\(\s*["']?https?://([A-Za-z0-9._-]+)`)
)
