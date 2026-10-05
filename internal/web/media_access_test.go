package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖 F2/F2b：GET /media/:id 的读取鉴权。
//
// 口径（DESIGN.md §6.3「鉴权只需一处（有卡组访问权的登录用户）」）：一个登录用户能读到某 media
// 的充要条件是——「当前用户可见卡组内、未软删、且**不是当前用户自己写的** note 精确引用了它
// （/media/<id> 字符串）」或「该 media 的 created_by = 当前用户」（覆盖刚上传、尚未插入任何卡片
// 的编辑器预览；字节按 sha256 去重，不构成额外泄露）。无权限与不存在统一 404，不泄露存在性。
//
// F2b 收紧的来源是：note 字段由用户自己可写，若「自己写的引用」也算授权，攻击者只要在自己卡组
// 写 ![](/media/<目标 id>)，就能按 id 逐个枚举他人媒体。因此引用来源必须是「别人/系统写的 note」。
// created_by IS NULL 视为「不是我写的」，可作授权来源（服务端生成的行不由攻击者控制）。
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
// 访问者不是上传者也不是引用 note 的作者，唯一来源是可见卡组里别人写的引用；软删该 note 后
// 读取变 404。三者（上传者 / note 作者 / 访问者）刻意分开，确保放行只可能来自「引用」这一支。
func TestMediaAccessLapsesWhenReferencingNoteSoftDeleted(t *testing.T) {
	srv, db, ownerID, ownerCookies, _ := newNotesServer(t)
	// note 作者取上传者：F2b 后「自己写的引用」不再授权，访问者（owner）必须不是作者。
	uploaderID, uploaderCookies, uploaderCSRF := createUserAndLogin(t, srv, db, "media-uploader")
	deck := seedDeck(t, db, ownerID, "private media deck")
	mediaID := uploadAndID(t, srv, uploaderCookies, uploaderCSRF, pngBody())
	note := referenceMediaBy(t, db, deck.ID, &uploaderID, mediaID)
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

// noteBy 直接建一条指定作者与字段 JSON 的 note；author 为 nil 时写入 created_by IS NULL。
// 它把「这条引用是谁写的」变成可显式构造的输入——正是 F2b 鉴权要看的那一维。
func noteBy(t *testing.T, db *gorm.DB, deckID uint64, author *uint64, fieldsJSON string) *store.Note {
	t.Helper()
	now := time.Now().UTC()
	note := &store.Note{
		DeckID: deckID, Kind: "basic", TagsJSON: "[]",
		FieldsJSON: fieldsJSON, CreatedBy: author, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(note).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	return note
}

// referenceMediaBy 是 noteBy 的媒体引用特化：字段里精确引用 mediaID，作者由 author 指定
// （nil 表示服务端写的 created_by IS NULL 行）。
func referenceMediaBy(t *testing.T, db *gorm.DB, deckID uint64, author *uint64, mediaID uint64) *store.Note {
	t.Helper()
	return noteBy(t, db, deckID, author, fmt.Sprintf(`{"front":"![](/media/%d)","back":"x"}`, mediaID))
}

// TestMediaAccessSelfAuthoredReferenceDoesNotGrant 是 F2b 的核心负例：引用来源是用户自己可写的
// note 字段，若「自己写的引用」也算授权，攻击者只要在自己卡组写 ![](/media/<目标 id>) 就能按 id
// 逐个枚举他人媒体。此用例把该路径钉死为 404（修前是 200），并确认不误伤「自己上传的文件」。
func TestMediaAccessSelfAuthoredReferenceDoesNotGrant(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)

	// A（owner）上传两份不同字节的媒体作为攻击目标；A 从没把它们共享给任何人。
	mediaA1 := uploadAndID(t, srv, ownerCookies, ownerCSRF, pngBody())
	mediaA2 := uploadAndID(t, srv, ownerCookies, ownerCSRF, append(pngBody(), 'a'))
	targetA1 := "/media/" + strconv.FormatUint(mediaA1, 10)
	targetA2 := "/media/" + strconv.FormatUint(mediaA2, 10)

	// 不误伤：A 自己的卡组里 A 自己写的引用，因 media.created_by=A 走口径①，仍 200。
	deckA := seedDeck(t, db, ownerID, "A self deck")
	referenceMediaBy(t, db, deckA.ID, &ownerID, mediaA1)
	if rec := getWithCookies(t, srv, targetA1, ownerCookies); rec.Code != http.StatusOK {
		t.Fatalf("A GET own uploaded media = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// B 在自己卡组里写一条卡，字段同时引用 A 的两份媒体（一张卡写多个 id，模拟枚举）。
	bID, bCookies, _ := createUserAndLogin(t, srv, db, "media-selfref")
	deckB := seedDeck(t, db, bID, "B self deck")
	noteBy(t, db, deckB.ID, &bID, fmt.Sprintf(
		`{"front":"![](/media/%d) ![](/media/%d)","back":"x"}`, mediaA1, mediaA2))

	for _, target := range []string{targetA1, targetA2} {
		if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusNotFound {
			t.Fatalf("self-authored reference: B GET %s = %d, want 404 (body %s)",
				target, rec.Code, rec.Body.String())
		}
	}
}

// TestMediaAccessSharedDeckReferenceByOtherAuthorGrants 保证新条件不误伤「别人写的引用」：
// A 把卡组共享给 B（reader），引用 A 的 media 的 note 也是 A 写的 → B 仍能读（作者 A ≠ B）。
func TestMediaAccessSharedDeckReferenceByOtherAuthorGrants(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "shared media deck")
	mediaID := uploadAndID(t, srv, ownerCookies, ownerCSRF, pngBody())
	referenceMediaBy(t, db, deck.ID, &ownerID, mediaID)
	target := "/media/" + strconv.FormatUint(mediaID, 10)

	bID, bCookies, _ := createUserAndLogin(t, srv, db, "media-shared-reader")
	// 未授权前 404；授权 reader 后 200。
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("ungranted reader GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
	if rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/sharing/grant", url.Values{
		"csrf_token": {ownerCSRF}, "user_id": {u64str(bID)}, "role": {store.RoleReader},
	}, ownerCookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("grant status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("reader GET media referenced by another author's note = %d, want 200 (body %s)",
			rec.Code, rec.Body.String())
	}
}

// TestMediaAccessOwnUploadWithSelfReferenceGrants 覆盖口径①：B 在自己卡组上传的 media
// （created_by=B）即使也被 B 自己的 note 引用，B 仍可读——F2b 收紧的是「引用来源」，不是上传者。
func TestMediaAccessOwnUploadWithSelfReferenceGrants(t *testing.T) {
	srv, db, _, _, _ := newNotesServer(t)
	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-selfupload")
	deckB := seedDeck(t, db, bID, "B upload deck")
	mediaB := uploadAndID(t, srv, bCookies, bCSRF, pngBody())
	referenceMediaBy(t, db, deckB.ID, &bID, mediaB)
	target := "/media/" + strconv.FormatUint(mediaB, 10)
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("uploader GET own referenced media = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
}

// TestMediaAccessNullAuthorReferenceGrants 钉死 NULL 的处理：created_by IS NULL 视为
// 「不是我写的」，可作授权来源（服务端导入/系统生成的行不由攻击者控制），不得被新条件误挡。
func TestMediaAccessNullAuthorReferenceGrants(t *testing.T) {
	srv, db, _, ownerCookies, ownerCSRF := newNotesServer(t)
	mediaA := uploadAndID(t, srv, ownerCookies, ownerCSRF, pngBody())
	// 这条 note 不是任何用户写的（created_by IS NULL），放在 B 自己可见的卡组里。
	bID, bCookies, _ := createUserAndLogin(t, srv, db, "media-null-author")
	deckB := seedDeck(t, db, bID, "B null deck")
	referenceMediaBy(t, db, deckB.ID, nil, mediaA)
	target := "/media/" + strconv.FormatUint(mediaA, 10)
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("B GET media referenced by a NULL-author note = %d, want 200 (body %s)",
			rec.Code, rec.Body.String())
	}
}

// TestMediaAccessResidualEditorInjectionStillGrants 记录一个 F2b 修不到的残留（有意不修）：
// 共享卡组的 editor 能编辑别人写的 note，而 NoteStore.Update 不改 note.created_by。于是 B（editor）
// 把 /media/<A 的 id> 注入 A 写的 note 后，这条引用「不是 B 写的」，B 仍读到 A 的媒体。
//
// 本用例把残留钉成「成立」（断言 200）。残留一旦被堵上，这里会变 404，测试即失败并提醒反转断言。
func TestMediaAccessResidualEditorInjectionStillGrants(t *testing.T) {
	srv, db, ownerID, ownerCookies, ownerCSRF := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "residual deck")
	mediaA := uploadAndID(t, srv, ownerCookies, ownerCSRF, pngBody())
	target := "/media/" + strconv.FormatUint(mediaA, 10)
	// A 写一条不含任何媒体引用的普通 note（created_by=A）。
	note := noteBy(t, db, deck.ID, &ownerID, `{"front":"plain","back":"x"}`)

	bID, bCookies, bCSRF := createUserAndLogin(t, srv, db, "media-editor-attacker")
	// 基线：B 此时读不到 A 的 media。
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusNotFound {
		t.Fatalf("pre-grant B GET %s = %d, want 404 (body %s)", target, rec.Code, rec.Body.String())
	}
	// 授予 B editor：可编辑卡组内任意 note，包括别人写的。
	if rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/sharing/grant", url.Values{
		"csrf_token": {ownerCSRF}, "user_id": {u64str(bID)}, "role": {store.RoleEditor},
	}, ownerCookies); rec.Code != http.StatusSeeOther {
		t.Fatalf("grant editor status = %d, want 303 (body %s)", rec.Code, rec.Body.String())
	}
	// B 编辑 A 写的 note，把 A 的 media 引用注入进去。
	save := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes/"+u64str(note.ID), url.Values{
		"csrf_token":  {bCSRF},
		"note_id":     {u64str(note.ID)},
		"field.front": {"![](" + target + ")"},
		"field.back":  {"x"},
	}, bCookies)
	if save.Code != http.StatusSeeOther {
		t.Fatalf("editor note update status = %d, want 303 (body %s)", save.Code, save.Body.String())
	}
	// note.created_by 未被 Update 改写，仍是 A。
	var reloaded store.Note
	if err := db.First(&reloaded, note.ID).Error; err != nil {
		t.Fatalf("reload note: %v", err)
	}
	if reloaded.CreatedBy == nil || *reloaded.CreatedBy != ownerID {
		t.Fatalf("note.created_by after editor update = %v, want %d", reloaded.CreatedBy, ownerID)
	}
	// 残留成立：注入的引用作者是 A（不是 B），B 因此通过鉴权读到 A 的媒体。
	if rec := getWithCookies(t, srv, target, bCookies); rec.Code != http.StatusOK {
		t.Fatalf("residual: editor-injected reference did not grant B access: GET %s = %d, want 200 (body %s)",
			target, rec.Code, rec.Body.String())
	}
}
