package web

import (
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 清空配置必须保存 NULL 而不是 0；0 本身就是合法切点（午夜），两者不得混淆。
// 资料写入走 SPA 的 JSON PATCH（SSR 的 /settings/profile 表单已删除）。
func TestSettingsCutoffCanClearToDefault(t *testing.T) {
	srv, db, id, cookies, csrf := newNotesServer(t)

	midnight := 0
	if rec := patchProfileCutoff(t, srv, cookies, csrf, &midnight); rec.Code != http.StatusOK {
		t.Fatalf("midnight status=%d body=%s", rec.Code, snippet(rec.Body.String()))
	}
	u, err := store.NewUserStore(db).ByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if u.DayCutoffHour == nil || *u.DayCutoffHour != 0 {
		t.Fatalf("midnight cutoff stored as %v, want 0", u.DayCutoffHour)
	}

	if rec := patchProfileCutoff(t, srv, cookies, csrf, nil); rec.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", rec.Code, snippet(rec.Body.String()))
	}
	u, err = store.NewUserStore(db).ByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if u.DayCutoffHour != nil {
		t.Fatal("clear did not store NULL")
	}
}
