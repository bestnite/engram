package web

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/store"
)

// newNotesServer 装配一个具备账号、会话、卡组与笔记存储的测试服务，并登录一个 owner。
// 返回的 csrf 是服务端会话绑定的 token，供写操作表单使用（M2-7 的 POST 都过 CSRF）。
func newNotesServer(t *testing.T) (srv *Server, db *gorm.DB, ownerID uint64, cookies []*http.Cookie, csrf string) {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	mgr, err := auth.NewSessionManager(users, sessions, auth.SessionConfig{Secret: testSessionSecret})
	if err != nil {
		t.Fatalf("NewSessionManager() error = %v", err)
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		t.Fatalf("NewAuditor() error = %v", err)
	}
	srv, err = New("127.0.0.1:0", Deps{
		DB:            db,
		Logger:        discardLogger(),
		SchemaVersion: func(ctx context.Context) (int, error) { return store.CurrentVersion(ctx, db) },
		Accounts:      accounts,
		Sessions:      mgr,
		Users:         users,
		Decks:         store.NewDeckStore(db),
		Notes:         store.NewNoteStore(db),
		Cards:         store.NewCardStore(db),
		Presets:       store.NewPresetStore(db),
		Auditor:       auditor,
		LoginLimiter: auth.NewLoginLimiter(auth.LimiterConfig{
			Sleep: func(context.Context, time.Duration) error { return nil },
		}),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	owner, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "owner", Email: "owner@example.com", Password: "Sup3rSecret!", Role: store.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("CreateLocalUser() error = %v", err)
	}
	login := postForm(t, srv, "/login", url.Values{
		"username": {"owner"}, "password": {"Sup3rSecret!"},
	}, nil)
	if login.Code != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303", login.Code)
	}
	var sess store.Session
	if err := db.Order("created_at desc").First(&sess).Error; err != nil {
		t.Fatalf("load session row: %v", err)
	}
	return srv, db, owner.ID, login.Result().Cookies(), sess.CSRFToken
}

// seedDeck 建一个属于 owner 的卡组（含一个调度预设，满足外键列）。
func seedDeck(t *testing.T, db *gorm.DB, ownerID uint64, name string) *store.Deck {
	t.Helper()
	now := time.Now().UTC()
	preset := store.Preset{OwnerUserID: ownerID, Name: "preset", LearningSteps: "1m,10m",
		RelearningSteps: "10m", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{OwnerUserID: ownerID, Name: name, Visibility: store.DeckVisibilityPrivate,
		PresetID: preset.ID, CreatedAt: now}
	if err := store.NewDeckStore(db).Create(context.Background(), &deck); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return &deck
}

// seedBasic 建一张 basic note；tags 非空时写入 tags_json。
func seedBasic(t *testing.T, db *gorm.DB, deckID uint64, front, back string, tags ...string) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, Kind: "basic"}
	if len(tags) > 0 {
		raw, _ := jsonMarshalStrings(tags)
		note.TagsJSON = raw
	}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note,
		map[string]any{"front": front, "back": back}); err != nil {
		t.Fatalf("create note %q: %v", front, err)
	}
	return note
}

// jsonMarshalStrings 是测试用的最小 JSON 数组编码，避免测试再引入一组编码逻辑。
func jsonMarshalStrings(xs []string) (string, error) {
	quoted := make([]string, len(xs))
	for i, x := range xs {
		quoted[i] = `"` + strings.ReplaceAll(x, `"`, `\"`) + `"`
	}
	return "[" + strings.Join(quoted, ",") + "]", nil
}

// TestNoteListRenders100NotesWithPaging 是 M2-7 的分页验收：100 条笔记分两页，页内容不重叠。
func TestNoteListRenders100NotesWithPaging(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Paging deck")
	for i := 1; i <= 100; i++ {
		seedBasic(t, db, deck.ID, front(i), back(i))
	}

	page1 := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes", cookies)
	if page1.Code != http.StatusOK {
		t.Fatalf("GET list page 1 status = %d, want 200", page1.Code)
	}
	body1 := page1.Body.String()
	if !strings.Contains(body1, "Q100") || !strings.Contains(body1, "Q051") {
		t.Errorf("page 1 does not render the first page window:\n%s", snippet(body1))
	}
	if strings.Contains(body1, "Q001") {
		t.Errorf("page 1 leaked a note from page 2")
	}
	if !strings.Contains(body1, "page=2") {
		t.Errorf("page 1 does not advertise the second page: %s", snippet(body1))
	}

	page2 := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes?page=2", cookies)
	body2 := page2.Body.String()
	if !strings.Contains(body2, "Q001") || !strings.Contains(body2, "Q050") {
		t.Errorf("page 2 does not render the remaining notes:\n%s", snippet(body2))
	}
	if strings.Contains(body2, "Q100") {
		t.Errorf("page 2 leaked a note from page 1")
	}
	if n, _, err := store.NewNoteStore(db).List(context.Background(), store.NoteListOptions{DeckID: deck.ID}); err != nil || len(n) != 50 {
		t.Fatalf("store List() returned (%d notes, %v), want (50, nil)", len(n), err)
	}
}

// TestNoteListSearchAndFilters 断言 ?q=、?tag=、?kind= 与 ?status= 各自生效。
func TestNoteListSearchAndFilters(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Filter deck")
	seedBasic(t, db, deck.ID, "Q010", "answer one", "alpha")
	seedBasic(t, db, deck.ID, "Q020", "answer two", "alpha")
	third := seedBasic(t, db, deck.ID, "Q030", "answer three", "beta")
	// 一条 cloze，用来验证题型筛选与不同题型的摘要。
	cloze := &store.Note{DeckID: deck.ID, Kind: "cloze"}
	if _, err := store.NewNoteStore(db).Create(context.Background(), cloze,
		map[string]any{"text": "the {{c1::capital}} of France"}); err != nil {
		t.Fatalf("create cloze: %v", err)
	}
	base := "/decks/" + u64str(deck.ID) + "/notes"

	// 搜索正面。
	body := getWithCookies(t, srv, base+"?q=Q010", cookies).Body.String()
	if !strings.Contains(body, "Q010") || strings.Contains(body, "Q020") {
		t.Errorf("search by front did not filter: %s", snippet(body))
	}
	// 搜索背面（A side）。
	body = getWithCookies(t, srv, base+"?q=answer+two", cookies).Body.String()
	if !strings.Contains(body, "Q020") || strings.Contains(body, "Q030") {
		t.Errorf("search by back did not filter: %s", snippet(body))
	}
	// 标签筛选。
	body = getWithCookies(t, srv, base+"?tag=alpha", cookies).Body.String()
	if !strings.Contains(body, "Q010") || !strings.Contains(body, "Q020") || strings.Contains(body, "Q030") {
		t.Errorf("tag filter did not apply: %s", snippet(body))
	}
	// 题型筛选。
	body = getWithCookies(t, srv, base+"?kind=cloze", cookies).Body.String()
	if !strings.Contains(body, "capital") || strings.Contains(body, "Q010") {
		t.Errorf("kind filter did not apply: %s", snippet(body))
	}
	// 软删除状态筛选：删掉 Q030 后，默认列表看不到，deleted 列表能看到。
	if _, err := store.NewNoteStore(db).DeleteMany(context.Background(), []uint64{third.ID}); err != nil {
		t.Fatalf("DeleteMany() error = %v", err)
	}
	body = getWithCookies(t, srv, base, cookies).Body.String()
	if strings.Contains(body, "Q030") {
		t.Errorf("soft-deleted note still shows in the default list")
	}
	body = getWithCookies(t, srv, base+"?status=deleted", cookies).Body.String()
	if !strings.Contains(body, "Q030") {
		t.Errorf("deleted filter did not show the soft-deleted note: %s", snippet(body))
	}
}

// TestNotePreviewOverHtmx 是 M2-7 的预览验收：预览端点返回渲染后的 HTML 片段而非整页。
func TestNotePreviewOverHtmx(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Preview deck")
	note := seedBasic(t, db, deck.ID, "old front", "old back")

	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/preview", url.Values{
		"csrf_token":  {csrf},
		"note_id":     {u64str(note.ID)},
		"field.front": {"**bold** and \\(a^2+b^2\\)"},
		"field.back":  {"plain back"},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST preview status = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") || strings.Contains(body, "<html") {
		t.Errorf("preview returned a full page instead of a fragment")
	}
	if !strings.Contains(body, "<strong>bold</strong>") {
		t.Errorf("preview did not render Markdown: %s", body)
	}
	if !strings.Contains(body, `\(a^2+b^2\)`) {
		t.Errorf("preview did not preserve the MathJax delimiter: %s", body)
	}
	if !strings.Contains(body, "plain back") {
		t.Errorf("preview did not include the edited back side: %s", body)
	}
	if strings.Contains(body, "old front") {
		t.Errorf("preview still renders the stale field value")
	}
}

// TestNoteEditSavesFields 断言编辑页 POST 保存并写审计。
func TestNoteEditSavesFields(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Edit deck")
	note := seedBasic(t, db, deck.ID, "front", "back")

	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), url.Values{
		"csrf_token":  {csrf},
		"note_id":     {u64str(note.ID)},
		"field.front": {"updated front"},
		"field.back":  {"updated back"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST edit status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	saved, err := store.NewNoteStore(db).ByID(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("reload note: %v", err)
	}
	fields, err := store.ParseFields(saved.FieldsJSON)
	if err != nil {
		t.Fatalf("ParseFields() error = %v", err)
	}
	if fields["front"] != "updated front" || fields["back"] != "updated back" {
		t.Errorf("saved fields = %v, want the posted values", fields)
	}
	audit := store.NewAuditStore(db)
	if n, err := audit.CountByAction(context.Background(), store.ActionNoteUpdate); err != nil || n != 1 {
		t.Errorf("note.update audit rows = (%d, %v), want (1, nil)", n, err)
	}
}

// TestNoteBulkActions 断言批量软删除与加标签都生效并各写一行审计。
func TestNoteBulkActions(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Bulk deck")
	a := seedBasic(t, db, deck.ID, "Q001", "A001")
	b := seedBasic(t, db, deck.ID, "Q002", "A002")
	c := seedBasic(t, db, deck.ID, "Q003", "A003")
	other := seedDeck(t, db, ownerID, "Other deck")
	foreign := seedBasic(t, db, other.ID, "Q999", "A999")

	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/bulk", url.Values{
		"csrf_token": {csrf},
		"action":     {"delete"},
		"note_ids":   {u64str(a.ID), u64str(b.ID)},
		"redirect":   {"/decks/" + u64str(deck.ID) + "/notes"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("bulk delete status = %d, want 303", rec.Code)
	}
	noteStore := store.NewNoteStore(db)
	active, total, err := noteStore.List(context.Background(), store.NoteListOptions{DeckID: deck.ID})
	if err != nil || total != 1 || len(active) != 1 || active[0].ID != c.ID {
		t.Fatalf("after bulk delete active = (%d notes, total %d, %v), want only note %d", len(active), total, err, c.ID)
	}
	if foreignNote, err := noteStore.ByID(context.Background(), foreign.ID); err != nil || foreignNote.DeckID != other.ID {
		t.Errorf("foreign note was affected by a bulk action in another deck")
	}

	rec = postForm(t, srv, "/decks/"+u64str(deck.ID)+"/bulk", url.Values{
		"csrf_token": {csrf},
		"action":     {"tag"},
		"tag":        {"gamma"},
		"note_ids":   {u64str(c.ID)},
		"redirect":   {"/decks/" + u64str(deck.ID) + "/notes"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("bulk tag status = %d, want 303", rec.Code)
	}
	tagged, err := noteStore.ByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("reload tagged note: %v", err)
	}
	tags, err := store.ParseTags(tagged.TagsJSON)
	if err != nil || !contains(tags, "gamma") {
		t.Errorf("tags after bulk tag = (%v, %v), want to contain gamma", tags, err)
	}
	audit := store.NewAuditStore(db)
	for action, want := range map[string]int64{
		store.ActionNoteDelete: 1,
		store.ActionNoteTagAdd: 1,
	} {
		if n, err := audit.CountByAction(context.Background(), action); err != nil || n != want {
			t.Errorf("audit rows for %q = (%d, %v), want (%d, nil)", action, n, err, want)
		}
	}
}

// TestNotePagesRejectAnonymousAndForeign 覆盖负例：未登录重定向、非 owner 403、缺 CSRF 403。
func TestNotePagesRejectAnonymousAndForeign(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Private deck")
	note := seedBasic(t, db, deck.ID, "Q001", "A001")

	if code := get(t, srv, "/decks/"+u64str(deck.ID)+"/notes", nil).Code; code != http.StatusSeeOther {
		t.Errorf("anonymous GET list status = %d, want 303 redirect to login", code)
	}

	// 另一个用户（非 owner）访问同一卡组必须被拒。
	users := store.NewUserStore(db)
	sessions := store.NewSessionStore(db)
	accounts, err := auth.NewAccountService(users, sessions, auth.NewPasswordHasher(auth.Params{
		Memory: 8 * 1024, Time: 1, Threads: 1, SaltLength: 16, KeyLength: 32,
	}))
	if err != nil {
		t.Fatalf("NewAccountService() error = %v", err)
	}
	if _, err := accounts.CreateLocalUser(context.Background(), auth.CreateUserInput{
		Username: "stranger", Email: "stranger@example.com", Password: "Sup3rSecret!", Role: store.RoleUser,
	}); err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	strangerLogin := postForm(t, srv, "/login", url.Values{
		"username": {"stranger"}, "password": {"Sup3rSecret!"},
	}, nil)
	if strangerLogin.Code != http.StatusSeeOther {
		t.Fatalf("stranger login status = %d, want 303", strangerLogin.Code)
	}
	foreign := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes", strangerLogin.Result().Cookies())
	if foreign.Code != http.StatusForbidden {
		t.Errorf("non-owner GET list status = %d, want 403", foreign.Code)
	}
	if rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), strangerLogin.Result().Cookies()); rec.Code != http.StatusForbidden {
		t.Errorf("non-owner GET editor status = %d, want 403", rec.Code)
	}

	// 有会话但缺 CSRF token 的预览请求必须 403。
	noCSRF := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/preview", url.Values{
		"note_id": {u64str(note.ID)},
	}, cookies)
	if noCSRF.Code != http.StatusForbidden {
		t.Errorf("preview without CSRF status = %d, want 403", noCSRF.Code)
	}
}

// front / back 是测试里生成可检索文案的两个小工具。
func front(i int) string { return "Q" + pad3(i) }
func back(i int) string  { return "A" + pad3(i) }

func pad3(i int) string {
	s := "000" + itoa(i)
	return s[len(s)-3:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func u64str(v uint64) string { return itoa(int(v)) }

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
