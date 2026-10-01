package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/flashcard/internal/store"
)

// healthValueRE 从健康页取出 data-health=<key> 对应的值文本。
var healthValueRE = regexp.MustCompile(`data-health="([a-z]+)"[^>]*>([^<]*)<`)

// healthValues 请求健康页并解析出全部读数；解析失败时打印响应片段。
func healthValues(t *testing.T, srv *Server, cookies []*http.Cookie) map[string]string {
	t.Helper()
	rec := getWithCookies(t, srv, "/admin/health", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/health = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	out := make(map[string]string)
	for _, m := range healthValueRE.FindAllStringSubmatch(rec.Body.String(), -1) {
		out[m[1]] = m[2]
	}
	return out
}

// TestAdminHealthValuesMatchIndependentComputation 是 M6-8 的验收：数据库连通、
// schema 版本、媒体目录占用、到期队列量四个读数，各与测试中独立算出的值相等。
//
// 独立性：schema 版本直接读 store.CurrentVersion；媒体字节数由测试自己写文件后
// 自行累加；到期数由测试按 seed 的 fixture 手算（不调用页面实现）。
func TestAdminHealthValuesMatchIndependentComputation(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(24 * time.Hour)

	deck := seedDeck(t, db, ownerID, "health")
	mkCard := func(state string, due *time.Time, suspended bool) uint64 {
		note := store.Note{DeckID: deck.ID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&note).Error; err != nil {
			t.Fatalf("create note: %v", err)
		}
		card := store.Card{NoteID: note.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
		if suspended {
			card.SuspendedAt = &now
		}
		if err := db.Create(&card).Error; err != nil {
			t.Fatalf("create card: %v", err)
		}
		st := store.CardState{CardID: card.ID, UserID: ownerID, State: state, DueAt: due}
		if err := db.Create(&st).Error; err != nil {
			t.Fatalf("create card state: %v", err)
		}
		return card.ID
	}

	// 到期（计入）：复习卡与学习卡各一张。
	mkCard("review", &past, false)
	mkCard("learning", &past, false)
	// 未到期（不计）：due 在未来。
	mkCard("review", &future, false)
	// 暂停（不计）：即使到期也排除。
	mkCard("review", &past, true)
	// 新卡（不计）：due_at 为空。
	mkCard("new", nil, false)
	// note 软删除（不计）。
	deletedCardID := mkCard("review", &past, false)
	var card store.Card
	if err := db.First(&card, "id = ?", deletedCardID).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	if err := db.Model(&store.Note{}).Where("id = ?", card.NoteID).Update("deleted_at", now).Error; err != nil {
		t.Fatalf("soft delete note: %v", err)
	}
	// 手算：只有前两张计入。
	const wantDue = 2

	// 媒体目录：写两个小文件，字节数由测试自行累加（均 < 1024，渲染为 "N B"）。
	root := srv.media.Root()
	if err := os.MkdirAll(filepath.Join(root, "ab"), 0o755); err != nil {
		t.Fatalf("mkdir media subdir: %v", err)
	}
	files := map[string]int{
		filepath.Join(root, "ab", "one.bin"): 100,
		filepath.Join(root, "cd.bin"):        250,
	}
	var wantMedia int64
	for path, size := range files {
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatalf("write media file %s: %v", path, err)
		}
		wantMedia += int64(size)
	}

	wantSchema, err := store.CurrentVersion(ctx, db)
	if err != nil {
		t.Fatalf("CurrentVersion() error = %v", err)
	}

	values := healthValues(t, srv, cookies)

	gotSchema := values["schema"]
	gotMedia := values["media"]
	gotDue := values["due"]
	t.Logf("health page: database=%q schema=%q media=%q due=%q", values["database"], gotSchema, gotMedia, gotDue)
	t.Logf("independent: database=%q schema=%q media=%q due=%q",
		"已连通", strconv.Itoa(wantSchema), strconv.FormatInt(wantMedia, 10)+" B", strconv.Itoa(wantDue))

	if got := values["database"]; got != "已连通" {
		t.Errorf("database = %q, want %q (a live ping succeeded)", got, "已连通")
	}
	if got, want := values["schema"], strconv.Itoa(wantSchema); got != want {
		t.Errorf("schema = %q, want %q", got, want)
	}
	if got, want := values["media"], strconv.FormatInt(wantMedia, 10)+" B"; got != want {
		t.Errorf("media = %q, want %q", got, want)
	}
	if got, want := values["due"], strconv.Itoa(wantDue); got != want {
		t.Errorf("due = %q, want %q", got, want)
	}
}

// TestAdminHealthPageRequiresAdmin 断言非 admin 访问健康页被拒（与 M6-1 的清单测试互补：
// 这条直接锁住新增路由本身）。同时确认导航里的「健康」入口已点亮为链接。
func TestAdminHealthPageRequiresAdmin(t *testing.T) {
	srv, db, _, ownerCookies, _ := newNotesServer(t)
	_, bobCookies, _ := createUserAndLogin(t, srv, db, "bob")

	rec := getWithCookies(t, srv, "/admin/health", bobCookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin GET /admin/health = %d, want 403", rec.Code)
	}

	body := getWithCookies(t, srv, "/admin/health", ownerCookies).Body.String()
	if !strings.Contains(body, `href="/admin/health"`) {
		t.Errorf("admin nav does not link to /admin/health, body: %s", snippet(body))
	}
}
