package web

import (
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 清空配置必须保存 NULL 而不是 0；渲染显示默认 4，但午夜输入仍原样显示 0。
func TestSettingsCutoffCanClearToDefault(t *testing.T) {
	srv, db, id, cookies, csrf := newNotesServer(t)
	// GET /settings 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 回退设置页（DESIGN.md §8.5）。
	srv.spa = nil
	if rec := postRawCutoff(t, srv, cookies, csrf, "0"); rec.Code != http.StatusSeeOther {
		t.Fatalf("midnight status=%d", rec.Code)
	}
	if rec := postRawCutoff(t, srv, cookies, csrf, ""); rec.Code != http.StatusSeeOther {
		t.Fatalf("clear status=%d", rec.Code)
	}
	u, err := store.NewUserStore(db).ByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if u.DayCutoffHour != nil {
		t.Fatal("clear did not store NULL")
	}
	if got := cutoffInputValue(t, getWithCookies(t, srv, "/settings", cookies).Body.String()); got != "4" {
		t.Fatalf("default display=%s", got)
	}
}
