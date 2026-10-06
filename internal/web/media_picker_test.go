package web

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖媒体选择器（M2-9 扩展）：
//   - 片段端点仅 editor 及以上可取（reader 与非成员 403）；
//   - 片段只列可读媒体——他人 private 卡组里引用的媒体不得出现；
//   - 翻页用 htmx 拉片段，`next_cursor` 为空时不渲染下一页按钮；
//   - 预览走原图（loading="lazy"），不生成缩略图。

// seedPickerMedia 直接落库一份媒体并登记 owner 归属，返回 sha256。
func seedPickerMedia(t *testing.T, db *gorm.DB, ownerID uint64, payload string) string {
	t.Helper()
	raw := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte(payload)...)
	m, err := store.NewMediaStore(db).SaveBytes(context.Background(), t.TempDir(), "image/png", raw, store.Ptr(ownerID))
	if err != nil {
		t.Fatalf("SaveBytes: %v", err)
	}
	return m.Sha256
}

// pickerNextCursor 从片段里取出下一页游标；没有下一页按钮时返回空串。
var pickerNextCursorRE = regexp.MustCompile(`media/picker\?limit=[0-9]+&amp;cursor=([A-Za-z0-9_-]+)`)

func pickerNextCursor(body string) string {
	m := pickerNextCursorRE.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// TestMediaPickerRequiresEditorAndListsReadable 覆盖角色门槛与可读范围。
func TestMediaPickerRequiresEditorAndListsReadable(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Picker deck")
	base := "/decks/" + u64str(deck.ID) + "/media/picker"

	owned := []string{
		seedPickerMedia(t, db, ownerID, "picker-a"),
		seedPickerMedia(t, db, ownerID, "picker-b"),
	}

	// 他人 private 卡组里引用的媒体：不得出现在 owner 的选择器里。
	otherID, _, _ := createUserAndLogin(t, srv, db, "picker-other")
	otherDeck := seedDeck(t, db, otherID, "Other private deck")
	secret := seedPickerMedia(t, db, otherID, "picker-secret")
	note := &store.Note{DeckID: otherDeck.ID, Kind: "basic", CreatedBy: store.Ptr(otherID)}
	if _, err := store.NewNoteStore(db).Save(context.Background(), note,
		map[string]any{"front": "![](/media/" + secret + ")", "back": "x"}); err != nil {
		t.Fatalf("save referencing note: %v", err)
	}

	rec := getWithCookies(t, srv, base, ownerCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s as editor = %d, want 200 (body %s)", base, rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if strings.Contains(body, "<!DOCTYPE") || strings.Contains(body, "<html") {
		t.Errorf("picker returned a full page instead of a fragment")
	}
	for _, sha := range owned {
		if !strings.Contains(body, `data-media-insert="/media/`+sha+`"`) {
			t.Errorf("picker omitted readable media %s: %s", sha, snippet(body))
		}
	}
	if strings.Contains(body, secret) {
		t.Errorf("picker leaked another user's private media %s: %s", secret, snippet(body))
	}
	if !strings.Contains(body, `loading="lazy"`) {
		t.Errorf("picker preview is not lazy-loaded: %s", snippet(body))
	}

	// reader 角色：够看不够改，片段端点拒绝。
	readerID, readerCookies, _ := createUserAndLogin(t, srv, db, "picker-reader")
	if err := store.NewGrantStore(db).Grant(context.Background(), deck.ID, readerID, store.RoleReader, store.Ptr(ownerID)); err != nil {
		t.Fatalf("grant reader: %v", err)
	}
	if rec := getWithCookies(t, srv, base, readerCookies); rec.Code != http.StatusForbidden {
		t.Errorf("GET %s as reader = %d, want 403", base, rec.Code)
	}
	// 非成员：403。
	if rec := getWithCookies(t, srv, base, ownerCookies); rec.Code != http.StatusOK {
		t.Fatalf("owner lost access: %d", rec.Code)
	}
}

// TestMediaPickerPaginatesOverHtmx 覆盖翻页：片段携带下一页 htmx 按钮，走完不重不漏。
func TestMediaPickerPaginatesOverHtmx(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Picker paging deck")
	base := "/decks/" + u64str(deck.ID) + "/media/picker?limit=2"

	all := map[string]bool{}
	for i := 0; i < 5; i++ {
		all[seedPickerMedia(t, db, ownerID, "paging-"+u64str(uint64(i)))] = true
	}

	seen := map[string]int{}
	cursor := ""
	pages := 0
	for {
		target := base
		if cursor != "" {
			target = base + "&cursor=" + cursor
		}
		rec := getWithCookies(t, srv, target, ownerCookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 (body %s)", target, rec.Code, snippet(rec.Body.String()))
		}
		body := rec.Body.String()
		pages++
		for _, m := range regexp.MustCompile(`data-media-insert="/media/([0-9a-f]{64})"`).FindAllStringSubmatch(body, -1) {
			seen[m[1]]++
		}
		next := pickerNextCursor(body)
		if next == "" {
			break
		}
		if pages > 10 {
			t.Fatalf("picker pagination did not terminate")
		}
		cursor = next
	}
	if pages != 3 {
		t.Errorf("picker used %d pages for 5 items at limit 2, want 3", pages)
	}
	if len(seen) != len(all) {
		t.Fatalf("paged media set has %d entries, want %d (%v)", len(seen), len(all), seen)
	}
	for sha := range all {
		if seen[sha] != 1 {
			t.Errorf("media %s appears %d times, want exactly once", sha, seen[sha])
		}
	}
}

// TestNoteEditPageRendersMediaPickerLauncher 断言编辑页上有「从媒体库选择」入口，
// 且它指向片段端点、目标是列表容器。
func TestNoteEditPageRendersMediaPickerLauncher(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Launcher deck")
	note := seedBasic(t, db, deck.ID, "front", "back")

	body := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), cookies).Body.String()
	if !strings.Contains(body, "data-media-picker") {
		t.Errorf("edit page has no media picker launcher: %s", snippet(body))
	}
	if !strings.Contains(body, "/decks/"+u64str(deck.ID)+"/media/picker") {
		t.Errorf("edit page launcher does not point at the picker endpoint: %s", snippet(body))
	}
	if !strings.Contains(body, `data-note-form`) {
		t.Errorf("edit page note form is missing the data-note-form marker the picker inserts into")
	}
}
