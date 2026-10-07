package web

import (
	"context"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// TestDefaultPresetSingleAcrossWebAndREST 是 M3-14「同一用户攒出两个默认预设」的回归。
//
// 修前：web 用本地化名创建默认预设（zh-CN 下是「默认」），REST 用字面量 "Default" 查找/创建，
// 两条入口对同一条逻辑预设给出不同名字，用户先经 web 建组再经 REST 建组就会各写一条。
// 修后两条入口共用 store.DefaultPresetName，同一用户只有一条默认预设。
//
// 两条路径都在本用例里真实走一遍：web 走表单 handler（默认语言 zh-CN），REST 走 api.CreateDeck
// （REST/MCP/CLI 共用的 service 方法）。
func TestDefaultPresetSingleAcrossWebAndREST(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	ctx := context.Background()

	// 经 web 建组：走 SPA 的同源 JSON 卡组端点（SSR 的 POST /decks 表单已删除）。
	rec := postJSONWithSession(srv, "/api/v1/decks", map[string]any{
		"name":        "web deck",
		"description": "",
	}, cookies, csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("web create deck = %d, want 201 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 经 REST 建组：复用同一 api 实例。
	owner, err := store.NewUserStore(db).ByID(ctx, ownerID)
	if err != nil {
		t.Fatalf("load owner: %v", err)
	}
	if _, err := srv.api.CreateDeck(ctx, owner, api.CreateDeckInput{Name: "rest deck"}); err != nil {
		t.Fatalf("REST create deck: %v", err)
	}

	presets, err := store.NewPresetStore(db).ListByOwner(ctx, ownerID)
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(presets) != 1 {
		t.Fatalf("default presets after web+REST deck creation = %d, want exactly 1", len(presets))
	}
	if presets[0].Name != store.DefaultPresetName {
		t.Errorf("default preset name = %q, want %q", presets[0].Name, store.DefaultPresetName)
	}

	// 两个卡组都建成了，确认用例真的走了两条建组路径。
	var decks int64
	if err := db.Model(&store.Deck{}).Where("owner_user_id = ?", ownerID).Count(&decks).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if decks != 2 {
		t.Errorf("decks = %d, want 2 (one from web, one from REST)", decks)
	}
}
