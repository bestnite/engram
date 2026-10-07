package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 F2/F2c：GET /media/<sha256> 的读取鉴权，以及 note 写入的写前媒体校验。
//
// 读取口径（DESIGN.md §6.3「鉴权只需一处（有卡组访问权的登录用户）」）：一个登录用户能读到某
// media 的充要条件是——① media_uploaders 里存在指向他的记录（他提供过这份字节，去重命中也算，
// 永久有效），或② media_notes 映射里存在一条指向「他可见卡组内、未软删的 note」的记录。
// 无权限与不存在统一 404，不泄露存在性。
//
// F2c（已修复，2026-10-06）：共享卡组的 editor 曾能把他人媒体引用注入别人写的 note，令映射一
// 建立即自我满足、从而读到该媒体（旧的 TestMediaAccessResidualEditorInjectionStillGrants）。
// 现在 note 写入前会对**写入之前的状态**校验本次新引入的引用是否写入者可读，越权引用在写入时即
// 被拒，映射不会被建立。本文件逐入口（web 编辑器新建/编辑）钉死这一点，并覆盖不误伤、撤销共享
// 后仍可读自己上传的媒体、去重命中登记全部上传者等用例。
//
// 夹具一律经 note 写入方法构造（NoteStore 的写入方法），不直接 GORM 插 note——否则映射不会建立，
// 测的就不是生产路径。

// uploadAndSha 上传一份字节并解出返回的 media sha256。字节内容决定 sha256，调用方据此控制是否去重。
func uploadAndSha(t *testing.T, srv *Server, cookies []*http.Cookie, csrf string, body []byte) string {
	t.Helper()
	rec := uploadMedia(t, srv, cookies, csrf, "pic.png", "image/png", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if !mediaShaOK(saved.Sha256) {
		t.Fatalf("upload returned sha %q, want 64-hex sha256", saved.Sha256)
	}
	return saved.Sha256
}

// uploadToDeckAndSha 从卡组内上传入口（要求 editor）上传一份字节并解出 sha256。
func uploadToDeckAndSha(t *testing.T, srv *Server, deckID uint64, cookies []*http.Cookie, csrf string, body []byte) string {
	t.Helper()
	rec := uploadMediaTo(t, srv, cookies, "/decks/"+u64str(deckID)+"/media", csrf, "pic.png", "image/png", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("deck upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if !mediaShaOK(saved.Sha256) {
		t.Fatalf("deck upload returned sha %q, want 64-hex sha256", saved.Sha256)
	}
	return saved.Sha256
}

// noteBy 经 note 写入方法建一条指定作者与字段的 note；author 为 nil 时 created_by IS NULL。
// 走写入路径是必须的：media_notes 映射由写入方法建立，直接插 note 会绕过被测逻辑。
func noteBy(t *testing.T, db *gorm.DB, deckID uint64, author *uint64, fields map[string]any) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, Kind: "basic", CreatedBy: author}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note, fields); err != nil {
		t.Fatalf("create note: %v", err)
	}
	return note
}

// referenceMediaBy 是 noteBy 的媒体引用特化：字段里精确引用 mediaSha，作者由 author 指定。
func referenceMediaBy(t *testing.T, db *gorm.DB, deckID uint64, author *uint64, mediaSha string) *store.Note {
	t.Helper()
	return noteBy(t, db, deckID, author, map[string]any{
		"front": fmt.Sprintf("![](/media/%s)", mediaSha),
		"back":  "x",
	})
}

// grantRole 让 owner 把卡组的指定角色授予 userID。
// 直接写授权表：SSR 的共享表单入口已随页面层删除，SPA 的共享写入走 /api/v1/decks/:id/sharing/grants。
func grantRole(t *testing.T, srv *Server, deckID, userID uint64, role string, ownerCookies []*http.Cookie, ownerCSRF string) {
	t.Helper()
	if err := store.NewGrantStore(srv.db).Grant(context.Background(), deckID, userID, role, nil); err != nil {
		t.Fatalf("grant %s on deck %d to user %d: %v", role, deckID, userID, err)
	}
}

// revokeGrant 让 owner 撤销 userID 在该卡组上的授权（同样直接写授权表）。
func revokeGrant(t *testing.T, srv *Server, deckID, userID uint64, ownerCookies []*http.Cookie, ownerCSRF string) {
	t.Helper()
	if err := store.NewGrantStore(srv.db).Revoke(context.Background(), deckID, userID); err != nil {
		t.Fatalf("revoke grant on deck %d from user %d: %v", deckID, userID, err)
	}
}

// mediaNoteRows 返回 (media_sha, note_id) 的映射行数，用来断言「写被拒时库里没有脏数据」。
func mediaNoteRows(t *testing.T, db *gorm.DB, sha string, noteID uint64) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&store.MediaNote{}).
		Where("media_sha = ? AND note_id = ?", sha, noteID).Count(&n).Error; err != nil {
		t.Fatalf("count media_notes: %v", err)
	}
	return n
}

// TestMediaAccessFollowsVisibleDeckGrantAndRevoke 是 F2 的主验收：
// private 卡组里被 note 引用的 media，对无授权用户 404；授权为 reader 后 200；撤销后立即 404。
func TestMediaAccessFollowsVisibleDeckGrantAndRevoke(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "private media deck")
	mediaSha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	referenceMediaBy(t, db, deck.ID, &ownerID, mediaSha)
	target := "/media/" + mediaSha

	readerID, readerCookies, _ := createUserAndLogin(t, srv, db, "media-outsider")

	// 1) 无授权：卡组是 private 且未授权 → 即使被引用也不放行。
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("ungranted reader GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 2) 授权 reader → 下一个请求即可读（映射走可见卡组谓词）。
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusOK {
		t.Fatalf("granted reader GET %s = %d, want 200 (body %s)", target, rec.Code, rec.Body.String())
	}

	// 3) 撤销 → 立即生效（不允许无失效策略的权限缓存）。
	revokeGrant(t, srv, deck.ID, readerID, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after revoke reader GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestMediaAccessLapsesWhenReferencingNoteSoftDeleted 覆盖「软删 note 不再授权」：
// 访问者不是上传者也不是 map 之外的行；唯一来源是可见卡组里一条 note 的引用；软删该 note 后读取变 404。
func TestMediaAccessLapsesWhenReferencingNoteSoftDeleted(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	// note 作者取上传者，访问者（owner）刻意不是上传者，确保放行只可能来自「映射」这一支。
	uploaderID, uploaderCookies, uploaderCSRF := createUserAndLogin(t, srv, db, "media-uploader")
	deck := seedDeck(t, db, ownerID, "private media deck")
	mediaSha := uploadAndSha(t, srv, uploaderCookies, uploaderCSRF, pngBody())
	note := referenceMediaBy(t, db, deck.ID, &uploaderID, mediaSha)
	target := "/media/" + mediaSha

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

// TestMediaAccessUploaderPreviewWithoutReference 覆盖口径①：上传者刚上传、尚未被任何卡片引用的
// media 仍可读（编辑器预览）——靠 media_uploaders。
func TestMediaAccessUploaderPreviewWithoutReference(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	mediaSha := uploadAndSha(t, srv, cookies, csrf, pngBody())
	target := "/media/" + mediaSha
	if rec := getWithCookies(t, srv, target, cookies); rec.Code != http.StatusOK {
		t.Fatalf("uploader GET unreferenced upload = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessExactShaBoundary 覆盖精确匹配：库里同时存在两个 sha，note 只引用其中一个。
// 未被引用的那个（由别人上传、owner 不可见）必须 404——映射按完整 64 位 sha 匹配，不做前缀匹配。
func TestMediaAccessExactShaBoundary(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	_, otherCookies, otherCSRF := createUserAndLogin(t, srv, db, "media-boundary-other")
	deck := seedDeck(t, db, ownerID, "boundary deck")

	// 别人上传两份不同字节的媒体；只有第二份被 owner 可见卡组里的 note 引用。
	unreferenced := uploadAndSha(t, srv, otherCookies, otherCSRF, pngBody())
	referenced := uploadAndSha(t, srv, otherCookies, otherCSRF, append(pngBody(), 'x'))
	referenceMediaBy(t, db, deck.ID, nil, referenced)

	if rec := getWithCookies(t, srv, "/media/"+referenced, ownerCookies); rec.Code != http.StatusOK {
		t.Fatalf("owner GET referenced sha = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// 未被引用的 sha 存在（上传者是别人），没有任何可见卡组的 note 引用它 → 404。
	if rec := getWithCookies(t, srv, "/media/"+unreferenced, ownerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("owner GET unreferenced sha = %d, want 404 (must not match any other sha) (body %s)",
			rec.Code, rec.Body.String())
	}
}

// TestMediaAccessSharedDeckReferenceByOtherAuthorGrants 保证不误伤「别人写的引用」：
// A 把卡组共享给 B（reader），引用 A 的 media 的 note 也是 A 写的 → B 仍能读。
func TestMediaAccessSharedDeckReferenceByOtherAuthorGrants(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "shared media deck")
	mediaSha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	referenceMediaBy(t, db, deck.ID, &ownerID, mediaSha)
	target := "/media/" + mediaSha

	bID, bCookies, _ := createUserAndLogin(t, srv, db, "media-shared-reader")
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("ungranted reader GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
	grantRole(t, srv, deck.ID, bID, store.RoleReader, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("reader GET media referenced by another author's note = %d, want 200 (body %s)",
			rec.Code, rec.Body.String())
	}
}

// TestMediaAccessOwnUploadWithSelfReferenceGrants 覆盖口径①：B 在自己卡组上传的 media
// 即使也被 B 自己的 note 引用，B 仍可读（走 media_uploaders）。
func TestMediaAccessOwnUploadWithSelfReferenceGrants(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-selfupload")
	deckB := seedDeck(t, db, bID, "B upload deck")
	mediaSha := uploadAndSha(t, srv, bCookies, bCSRF, pngBody())
	referenceMediaBy(t, db, deckB.ID, &bID, mediaSha)
	target := "/media/" + mediaSha
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("uploader GET own referenced media = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessNullAuthorReferenceGrants 钉死 NULL 的处理：created_by IS NULL 的 note
// （服务端导入/系统生成）在读者可见卡组里引用媒体时，读者仍可读（映射不看作者）。
func TestMediaAccessNullAuthorReferenceGrants(t *testing.T) {
	srv, db, _, ownerCookies, ownerCSRF := newNotesServer(t)
	mediaSha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	bID, bCookies, _ := createUserAndLogin(t, srv, db, "media-null-author")
	deckB := seedDeck(t, db, bID, "B null deck")
	referenceMediaBy(t, db, deckB.ID, nil, mediaSha)
	target := "/media/" + mediaSha
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("B GET media referenced by a NULL-author note = %d, want 200 (body %s)",
			rec.Code, rec.Body.String())
	}
}

// TestMediaAccessResidualEditorInjectionIsDenied 的写前校验断言现在由 internal/api 的
// media_access_test.go 覆盖（REST PATCH/POST 是唯一写入传输）；web 侧只保留读取鉴权用例。
// 下面这些用例仍覆盖「映射只在可见卡组内授权读取」的读侧行为。
//
// TestMediaAccessUploaderKeepsAccessAfterGrantRevoked 覆盖「A 撤销共享后 B 仍可读自己上传的媒体」：
// B 在共享期间上传的字节经 media_uploaders 获得永久归属，撤销授权不影响。
func TestMediaAccessUploaderKeepsAccessAfterGrantRevoked(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "revoke uploader deck")
	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-revoked-uploader")
	grantRole(t, srv, deck.ID, bID, store.RoleEditor, ownerCookies, ownerCSRF)
	mediaB := uploadToDeckAndSha(t, srv, deck.ID, bCookies, bCSRF, pngBody())

	revokeGrant(t, srv, deck.ID, bID, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, "/media/"+mediaB, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("after revoke B GET own upload = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessDedupeHitRegistersEveryUploader 覆盖去重命中：B 上传与 C 已有字节完全相同的媒体，
// B 也拿到 media_uploaders 记录（幂等、不建重复行）。这正是「A 撤销共享后 B 仍可读自己上传的媒体」
// 在去重下成立的前提。
func TestMediaAccessDedupeHitRegistersEveryUploader(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	cID, cCookies, cCSRF := createUserAndLogin(t, srv, db, "media-dedupe-c")
	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-dedupe-b")

	body := pngBody()
	shaC := uploadAndSha(t, srv, cCookies, cCSRF, body)
	shaB := uploadAndSha(t, srv, bCookies, bCSRF, body)
	if shaC != shaB {
		t.Fatalf("dedupe failed: shas %s and %s differ", shaC, shaB)
	}

	var rows int64
	if err := db.Model(&store.MediaUploader{}).Where("media_sha = ?", shaB).Count(&rows).Error; err != nil {
		t.Fatalf("count media_uploaders: %v", err)
	}
	if rows != 2 {
		t.Fatalf("media_uploaders rows for deduped sha = %d, want 2 (both uploaders)", rows)
	}
	for _, uid := range []uint64{bID, cID} {
		var n int64
		if err := db.Model(&store.MediaUploader{}).
			Where("media_sha = ? AND user_id = ?", shaB, uid).Count(&n).Error; err != nil {
			t.Fatalf("count uploader %d: %v", uid, err)
		}
		if n != 1 {
			t.Errorf("uploader %d has %d rows for sha %s, want exactly 1", uid, n, shaB)
		}
	}
	// 上传者能读自己上传的（去重命中）字节，即使它最早由别人上传。
	if rec := getWithCookies(t, srv, "/media/"+shaB, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("dedupe uploader B GET = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessReferenceRemovalLapsesAccess 证明映射是「重建」而非「追加」：
// A 删掉 note 里的媒体引用后再保存，映射旧行必须被清掉，读者随即失去读取权。
func TestMediaAccessReferenceRemovalLapsesAccess(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "reference removal deck")
	mediaSha := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	note := referenceMediaBy(t, db, deck.ID, &ownerID, mediaSha)
	target := "/media/" + mediaSha

	readerID, readerCookies, _ := createUserAndLogin(t, srv, db, "media-ref-removed-reader")
	grantRole(t, srv, deck.ID, readerID, store.RoleReader, ownerCookies, ownerCSRF)
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusOK {
		t.Fatalf("reader GET before removal = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// A 去掉引用并保存（同一写入方法 → 重建映射）。
	// 页面层的 note 写入口已删除；这里直接走 store 的写入方法（重建映射的唯一的实现）。
	if _, err := store.NewNoteStore(db).Update(context.Background(), note, map[string]any{
		"front": "no media any more", "back": "x",
	}); err != nil {
		t.Fatalf("save after removing reference: %v", err)
	}

	if n := mediaNoteRows(t, db, mediaSha, note.ID); n != 0 {
		t.Errorf("stale media_notes row survived reference removal: %d rows", n)
	}
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("reader GET after reference removal = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}
