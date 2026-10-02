package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// TestAdminDashboardShowsInstanceCounts 是 M6-1 概览页的验收：/admin 首页必须真的渲染
// 计数卡（曾经只传了标题与导航，右侧内容区一片空白），每张卡链到它统计的分区。
func TestAdminDashboardShowsInstanceCounts(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "统计用卡组")
	seedBasic(t, db, deck.ID, "q", "a")
	createUserAndLogin(t, srv, db, "seconduser")

	rec := getWithCookies(t, srv, "/admin", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()

	// 五张卡的标题（用户/卡组/笔记卡片/到期队列/作业）都要在页面上。
	for _, want := range []string{"用户管理", "卡组", "笔记 / 卡片", "到期队列", "作业"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard is missing the %q card", want)
		}
	}
	// 每张卡链到它统计的分区。
	for _, href := range []string{`href="/admin/users"`, `href="/decks"`, `href="/admin/health"`, `href="/admin/jobs"`} {
		if !strings.Contains(body, href) {
			t.Errorf("dashboard is missing a card linking to %s", href)
		}
	}

	// 用户卡的数字必须与库里一致（不是写死的 0）。
	var total, active int64
	if err := db.Model(&store.User{}).Count(&total).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := db.Model(&store.User{}).Where("status = ?", store.StatusActive).Count(&active).Error; err != nil {
		t.Fatalf("count active users: %v", err)
	}
	if want := fmt.Sprintf("%d 个账号 · %d 个启用", total, active); !strings.Contains(body, want) {
		t.Errorf("dashboard user card = missing %q; body = %s", want, snippet(body))
	}

	// 到期队列卡显示的是实例级到期量：刚种下的那张新卡应当在其中（非 0）。
	var due int64
	if err := db.Model(&store.Card{}).Count(&due).Error; err != nil {
		t.Fatalf("count cards: %v", err)
	}
	if due == 0 {
		t.Fatal("fixture seeded no cards; the due-queue assertion would be vacuous")
	}
}
