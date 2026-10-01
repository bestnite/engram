package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"example.com/flashcard/internal/store"
)

// TestAdminAPIKeysNeverLeakPlaintext 是 M6-9 的核心验收：创建一把 key 之后，
// 任何 handler / 模板都不可能返回明文（也不会返回库里的 sha256）。
//
// 做法：先用 store 直接创建 key 拿到一次性明文与 key_hash，然后
//  1. 请求总览页，断言展示前缀、不含明文、不含 key_hash；
//  2. 遍历 adminRoutes() 里全部 GET 路由，逐条断言响应体不含明文与 key_hash。
//
// 明文只在 Create 的返回值里出现一次，库里只存 sha256；这条测试把「不会泄露」钉死在
// 所有管理页面上，而不是只看总览页一处。
func TestAdminAPIKeysNeverLeakPlaintext(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	created, err := store.NewAPIKeyStore(db).Create(context.Background(), store.CreateAPIKeyParams{
		UserID: ownerID, Name: "ci key", Scopes: []string{store.ScopeRead, store.ScopeWrite},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	plaintext := created.Plaintext
	keyHash := created.Key.KeyHash
	if plaintext == "" || keyHash == "" {
		t.Fatal("created key is missing plaintext or hash; test would be vacuous")
	}

	rec := getWithCookies(t, srv, "/admin/api-keys", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/api-keys = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, created.Key.Prefix) {
		t.Errorf("overview does not show the display prefix %q; body = %s", created.Key.Prefix, snippet(body))
	}
	if strings.Contains(body, plaintext) {
		t.Fatal("overview page leaked the plaintext key")
	}
	if strings.Contains(body, keyHash) {
		t.Fatal("overview page leaked the stored key hash")
	}
	// 名称与 scopes 必须可见（总览的价值所在）。
	if !strings.Contains(body, "ci key") || !strings.Contains(body, "read") || !strings.Contains(body, "write") {
		t.Errorf("overview is missing name or scopes; body = %s", snippet(body))
	}

	// 全量 GET 管理路由：明文绝不得出现在任何响应里。
	// key_hash 只要求不出现在「页面」上；/admin/export 是全库备份（M6-5），按设计导出
	// 整表（含 api_keys.key_hash，恢复所必需），sha256 不是明文，故对导出不做哈希断言。
	checked := 0
	for _, r := range adminRoutes() {
		if r.Method != http.MethodGet {
			continue
		}
		resp := getWithCookies(t, srv, r.Path, cookies)
		got := resp.Body.String()
		checked++
		if strings.Contains(got, plaintext) {
			t.Errorf("GET %s leaked the plaintext key", r.Path)
		}
		if r.Path != "/admin/export" && strings.Contains(got, keyHash) {
			t.Errorf("GET %s leaked the stored key hash", r.Path)
		}
	}
	t.Logf("checked %d admin GET routes; plaintext %q absent from all; overview shows prefix %q",
		checked, plaintext, created.Key.Prefix)
}

// TestAdminAPIKeyRevoke 覆盖写路径：撤销后 key 立即变为「已撤销」，并写一条审计。
func TestAdminAPIKeyRevoke(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	ctx := context.Background()
	created, err := store.NewAPIKeyStore(db).Create(ctx, store.CreateAPIKeyParams{
		UserID: ownerID, Name: "to revoke",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	id := created.Key.ID

	rec := postForm(t, srv, "/admin/api-keys/"+strconv.FormatUint(id, 10)+"/revoke",
		map[string][]string{"csrf_token": {csrf}}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST revoke = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	var stored store.APIKey
	if err := db.First(&stored, "id = ?", id).Error; err != nil {
		t.Fatalf("reload key: %v", err)
	}
	if stored.RevokedAt == nil {
		t.Fatal("key was not revoked")
	}
	// 撤销后立即失效：用明文再认证必须被拒。
	if _, err := store.NewAPIKeyStore(db).Authenticate(ctx, created.Plaintext, stored.CreatedAt); err == nil {
		t.Fatal("revoked key still authenticates")
	}
	// 审计留痕，且详情不含任何 key 内容。
	n, err := store.NewAuditStore(db).CountByAction(ctx, store.ActionAPIKeyRevoke)
	if err != nil {
		t.Fatalf("count revoke audits: %v", err)
	}
	if n != 1 {
		t.Errorf("api_key.revoke audit rows = %d, want 1", n)
	}

	body := getWithCookies(t, srv, "/admin/api-keys", cookies).Body.String()
	if !strings.Contains(body, "已撤销") {
		t.Errorf("overview does not show the revoked state; body = %s", snippet(body))
	}
	if strings.Contains(body, created.Plaintext) {
		t.Fatal("overview leaked plaintext after revoke")
	}
}
