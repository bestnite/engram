package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
func grantRole(t *testing.T, srv *Server, deckID, userID uint64, role string, ownerCookies []*http.Cookie, ownerCSRF string) {
	t.Helper()
	rec := postForm(t, srv, "/decks/"+u64str(deckID)+"/sharing/grant", url.Values{
		"csrf_token": {ownerCSRF}, "user_id": {u64str(userID)}, "role": {role},
	}, ownerCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("grant %s status = %d, want 303 (body %s)", role, rec.Code, snippet(rec.Body.String()))
	}
}

// revokeGrant 让 owner 撤销 userID 在该卡组上的授权。
func revokeGrant(t *testing.T, srv *Server, deckID, userID uint64, ownerCookies []*http.Cookie, ownerCSRF string) {
	t.Helper()
	rec := postForm(t, srv, "/decks/"+u64str(deckID)+"/sharing/revoke", url.Values{
		"csrf_token": {ownerCSRF}, "user_id": {u64str(userID)},
	}, ownerCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
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

// TestMediaAccessResidualEditorInjectionIsDenied 是反转后的 F2c 用例（旧名 …StillGrants）：
// 共享卡组的 editor B 编辑 A 写的 note、把 A 的 media 引用注入进去——写前校验对**写入之前的状态**
// 求值，B 读不到 A 的 media，故整次写入被拒（400），字段与映射均不变，B 也读不到 A 的媒体。
func TestMediaAccessResidualEditorInjectionIsDenied(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "residual deck")
	mediaA := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	target := "/media/" + mediaA
	// A 写一条不含任何媒体引用的普通 note（created_by=A）。
	note := noteBy(t, db, deck.ID, &ownerID, map[string]any{"front": "plain", "back": "x"})

	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-editor-attacker")
	// 基线：B 此时读不到 A 的 media。
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("pre-grant B GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
	grantRole(t, srv, deck.ID, bID, store.RoleEditor, ownerCookies, ownerCSRF)

	// B 编辑 A 写的 note，把 A 的 media 引用注入进去：必须被拒。
	save := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), url.Values{
		"csrf_token":  {bCSRF},
		"note_id":     {u64str(note.ID)},
		"field.front": {fmt.Sprintf("![](%s)", target)},
		"field.back":  {"x"},
	}, bCookies)
	if save.Code != http.StatusBadRequest {
		t.Fatalf("editor injection status = %d, want 400 (rejected) (body %s)", save.Code, snippet(save.Body.String()))
	}

	// 库里没有脏数据：note 字段未变（仍无引用），且没有为它建立 media_notes 映射。
	var reloaded store.Note
	if err := db.First(&reloaded, note.ID).Error; err != nil {
		t.Fatalf("reload note: %v", err)
	}
	if reloaded.FieldsJSON != note.FieldsJSON {
		t.Errorf("note fields changed on a rejected write: got %s want %s", reloaded.FieldsJSON, note.FieldsJSON)
	}
	if n := mediaNoteRows(t, db, mediaA, note.ID); n != 0 {
		t.Errorf("rejected injection created %d media_notes rows, want 0", n)
	}
	// 注入未生效：B 仍读不到 A 的媒体。
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("after rejected injection B GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
}

// TestMediaAccessEditorInjectionOnCreateIsDenied 覆盖新建入口：共享卡组的 editor B 新建一条
// 引用 A 的 media 的卡片——写前校验拒绝，且库里没有留下任何 note / 映射行。
func TestMediaAccessEditorInjectionOnCreateIsDenied(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "create injection deck")
	mediaA := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-editor-creator")
	grantRole(t, srv, deck.ID, bID, store.RoleEditor, ownerCookies, ownerCSRF)

	before, _, err := store.NewNoteStore(db).List(context.Background(), store.NoteListOptions{DeckID: deck.ID})
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}

	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes", url.Values{
		"csrf_token":  {bCSRF},
		"kind":        {"basic"},
		"field.front": {"![](/media/" + mediaA + ")"},
		"field.back":  {"x"},
	}, bCookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("editor create injection status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	after, _, err := store.NewNoteStore(db).List(context.Background(), store.NoteListOptions{DeckID: deck.ID})
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("rejected create left %d notes, want %d", len(after), len(before))
	}
	var mapping int64
	if err := db.Model(&store.MediaNote{}).Where("media_sha = ?", mediaA).Count(&mapping).Error; err != nil {
		t.Fatalf("count media_notes: %v", err)
	}
	if mapping != 0 {
		t.Errorf("rejected create left %d media_notes rows, want 0", mapping)
	}
}

// TestMediaAccessEditorKeepsExistingReferenceAndAddsOwnUpload 覆盖「不误伤」：
// B 是 A 卡组的 editor，保存 A 的卡（字段含 A 的图）被允许；B 再往这张卡里插入自己刚上传的图也被允许。
func TestMediaAccessEditorKeepsExistingReferenceAndAddsOwnUpload(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "no false positive deck")
	mediaA := uploadAndSha(t, srv, ownerCookies, ownerCSRF, pngBody())
	note := referenceMediaBy(t, db, deck.ID, &ownerID, mediaA)

	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-editor-keeping")
	grantRole(t, srv, deck.ID, bID, store.RoleEditor, ownerCookies, ownerCSRF)

	// 1) 原样保存 A 的卡（保留 A 的图）：旧引用不是本次新引入的，允许。
	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), url.Values{
		"csrf_token":  {bCSRF},
		"note_id":     {u64str(note.ID)},
		"field.front": {"![](/media/" + mediaA + ")"},
		"field.back":  {"edited by editor"},
	}, bCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("editor save with existing reference status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	// 2) B 上传自己的图（editor 可向卡组塞媒体），再插入到同一张卡：新引用对 B 可读（uploaders），允许。
	mediaB := uploadToDeckAndSha(t, srv, deck.ID, bCookies, bCSRF, append(pngBody(), 'b'))
	rec = postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), url.Values{
		"csrf_token":  {bCSRF},
		"note_id":     {u64str(note.ID)},
		"field.front": {"![](/media/" + mediaA + ") ![](/media/" + mediaB + ")"},
		"field.back":  {"edited by editor"},
	}, bCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("editor adding own upload status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	if r := getWithCookies(t, srv, "/media/"+mediaB, bCookies); r.Code != http.StatusOK {
		t.Fatalf("B GET own uploaded media = %d, want 200 (body %s)", r.Code, r.Body.String())
	}
	if r := getWithCookies(t, srv, "/media/"+mediaA, bCookies); r.Code != http.StatusOK {
		t.Fatalf("B GET A's media referenced by shared note = %d, want 200 (body %s)", r.Code, r.Body.String())
	}
}

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
	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), url.Values{
		"csrf_token":  {ownerCSRF},
		"note_id":     {u64str(note.ID)},
		"field.front": {"no media any more"},
		"field.back":  {"x"},
	}, ownerCookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save after removing reference status = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	if n := mediaNoteRows(t, db, mediaSha, note.ID); n != 0 {
		t.Errorf("stale media_notes row survived reference removal: %d rows", n)
	}
	if rec := getWithCookies(t, srv, target, readerCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("reader GET after reference removal = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}
