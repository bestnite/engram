package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 F2：GET /media/:id 的读取鉴权。
//
// 口径（DESIGN.md §6.3「鉴权只需一处（有卡组访问权的登录用户）」）：一个登录用户能读到某 media
// 的充要条件是——「当前用户可见卡组内、未软删的 note 精确引用了它（/media/<id> 字符串）」或
// 「该 media 的 created_by = 当前用户」（覆盖刚上传、尚未插入任何卡片的编辑器预览；字节按
// sha256 去重，不构成额外泄露）。无权限与不存在统一 404，不泄露存在性。
//
// 这些用例在真实 SQLite + 真实路由上把该口径钉死，重点是「修前是 200 的越权读取」。

// uploadAndID 上传一份字节并解出返回的 media id。字节内容决定 sha256，调用方据此控制是否去重。
func uploadAndID(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string, body []byte) uint64 {
	t.Helper()
	rec := uploadMedia(t, srv, cookies, csrf, "pic.png", "image/png", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if saved.ID == 0 {
		t.Fatalf("upload returned id 0")
	}
	return saved.ID
}

// TestMediaAccessFollowsVisibleDeckGrantAndRevoke 是 F2 的主验收：
// private 卡组里被 note 引用的 media，对无授权用户 404；授权为 reader 后 200；撤销后立即 404。
func TestMediaAccessFollowsVisibleDeckGrantAndRevoke(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "private media deck")
	mediaID := uploadAndID(t, srv, ownerCookies, ownerCSRF, pngBody())
	referenceMedia(t, db, deck.ID, ownerID, mediaID)
	target := "/media/" + strconv.FormatUint(mediaID, 10)

	readerID, readerCookies, _ := createUserAndLogin(t, srv, db, "media-outsider")

	// 1) 无授权：卡组是 private 且未授权 → 即使被引用也不放行（修前这里是 200）。
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("ungranted reader GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 2) 授权 reader → 下一个请求即可读。
	if rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/sharing/grant", url.Values{
		"csrf_token": {ownerCSRF}, "user_id": {u64str(readerID)}, "role": {store.RoleReader},
	}, ownerCookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("grant status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusOK {
		t.Fatalf("granted reader GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 3) 撤销 → 立即生效（不允许无失效策略的权限缓存）。
	if rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/sharing/revoke", url.Values{
		"csrf_token": {ownerCSRF}, "user_id": {u64str(readerID)},
	}, ownerCookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after revoke reader GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestMediaAccessLapsesWhenReferencingNoteSoftDeleted 覆盖「删除引用即失去读取权」：
// 访问者不是上传者，唯一来源是可见卡组里的引用；软删该 note 后自己的读取也变 404。
func TestMediaAccessLapsesWhenReferencingNoteSoftDeleted(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	// 上传者与访问者刻意分开，确保放行只可能来自「引用」这一支。
	_, uploaderCookies, uploaderCSRF := createUserAndLogin(t, srv, db, "media-uploader")
	deck := seedDeck(t, db, ownerID, "private media deck")
	mediaID := uploadAndID(t, srv, uploaderCookies, uploaderCSRF, pngBody())
	note := referenceMedia(t, db, deck.ID, ownerID, mediaID)
	target := "/media/" + strconv.FormatUint(mediaID, 10)

	if rec := getWithCookies(t, srv, target, ownerCookies); rec.Code != http.StatusOK {
		t.Fatalf("owner GET referenced media = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if err := db.Delete(note).Error; err != nil {
		t.Fatalf("soft delete note: %v", err)
	}
	if rec := getWithCookies(t, srv, target, ownerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after note soft delete owner GET = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessUploaderPreviewWithoutReference 覆盖口径第二支：上传者刚上传、
// 尚未被任何卡片引用的 media 仍可读（编辑器预览）。
func TestMediaAccessUploaderPreviewWithoutReference(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	mediaID := uploadAndID(t, srv, cookies, csrf, pngBody())
	target := "/media/" + strconv.FormatUint(mediaID, 10)
	if rec := getWithCookies(t, srv, target, cookies); rec.Code != http.StatusOK {
		t.Fatalf("uploader GET unreferenced upload = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessExactIDBoundary 覆盖精确匹配：库里同时存在 id=1 与 id=12，note 只引用 12。
// 请求 id=1 必须是 404——按前缀/子串匹配（把 /media/12 当成 /media/1）会在这里误放行。
func TestMediaAccessExactIDBoundary(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	_, otherCookies, otherCSRF := createUserAndLogin(t, srv, db, "media-boundary-other")
	deck := seedDeck(t, db, ownerID, "boundary deck")

	// 另一个用户先上传，占住 id=1；owner 再上传得到 id=2，随后把它改号为 12。
	firstID := uploadAndID(t, srv, otherCookies, otherCSRF, pngBody())
	if firstID != 1 {
		t.Fatalf("first media id = %d, want 1 on a fresh sqlite", firstID)
	}
	secondID := uploadAndID(t, srv, ownerCookies, ownerCSRF, append(pngBody(), 'x'))
	if err := db.Exec("UPDATE media SET id = ? WHERE id = ?", 12, secondID).Error; err != nil {
		t.Fatalf("renumber media id to 12: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, 12)

	if rec := getWithCookies(t, srv, "/media/12", ownerCookies); rec.Code != http.StatusOK {
		t.Fatalf("owner GET /media/12 = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// id=1 存在（created_by 是别人），但没有任何可见卡组的 note 引用它 → 404。
	if rec := getWithCookies(t, srv, "/media/1", ownerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("owner GET /media/1 = %d, want 404 (must not prefix-match /media/12) (body %s)", rec.Code, rec.Body.String())
	}
}
