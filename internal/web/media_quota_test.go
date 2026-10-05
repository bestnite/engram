package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/media"
	"git.nite07.com/nite/engram/internal/store"
	"gorm.io/gorm"
)

// 本文件覆盖 M2-13：在单文件上限之外的每用户媒体总量配额。
// 三条验收主线：超限拒绝（稳定 code + 点名限额的本地化文案）、删除引用后配额释放、
// 默认不限（不配就不是一个自拟的数字）。

// quotaPNG 造一段总字节数固定的 PNG（8 字节签名 + 填充），用于精确控制用量。
func quotaPNG(total int) []byte {
	if total < 8 {
		total = 8
	}
	b := make([]byte, total)
	copy(b, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	return b
}

// mediaErr 解出统一错误信封的 code 与本地化 message。
type mediaErrResp struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// TestMediaQuotaDefaultUnlimited 验收：不配置配额时上传不受总量限制（默认 off）。
func TestMediaQuotaDefaultUnlimited(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	// 显式清空环境变量，确保走“未配置”路径；不写 settings 表。
	t.Setenv(envMediaUserQuotaBytes, "")
	rec := uploadMedia(t, srv, cookies, csrf, "big.png", "image/png", quotaPNG(6000))
	if rec.Code != http.StatusCreated {
		t.Fatalf("default upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	t.Logf("default (no env, no setting) accepts a 6000 B upload: status 201")
}

// TestMediaQuotaExceededRejectedLocalized 验收：超限时用稳定 code 与点名限额/已用量的
// 本地化文案拒绝；默认（zh-CN）与 ?lang=en 两种语言都给。
func TestMediaQuotaExceededRejectedLocalized(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	t.Setenv(envMediaUserQuotaBytes, "")
	ctx := context.Background()
	if err := store.PutSetting(ctx, db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting() error = %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")

	// 先上传 1500 字节并让 owner 的 note 引用它 → 已用量 1500。
	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500))
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d, want 201", first.Code)
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, saved.Sha256)

	// 再传 1000 字节：1500+1000 > 2048 → 拒绝。
	rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota status = %d, want 413 (body %s)", rec.Code, rec.Body.String())
	}
	var e mediaErrResp
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if e.Error.Code != media.CodeQuotaExceeded {
		t.Fatalf("error code = %q, want %q", e.Error.Code, media.CodeQuotaExceeded)
	}
	// 文案点名限额与已用量（人类可读形式）。
	if !strings.Contains(e.Error.Message, humanBytes(2048)) || !strings.Contains(e.Error.Message, humanBytes(1500)) {
		t.Fatalf("zh message %q must name limit %s and used %s", e.Error.Message, humanBytes(2048), humanBytes(1500))
	}

	// ?lang=en：英文文案同样点名数字。
	recEn := uploadMediaTo(t, srv, cookies, "/media?lang=en", csrf, "m2.png", "image/png", quotaPNG(1000))
	if recEn.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("en over-quota status = %d, want 413", recEn.Code)
	}
	var eEn mediaErrResp
	if err := json.Unmarshal(recEn.Body.Bytes(), &eEn); err != nil {
		t.Fatalf("decode en error body: %v", err)
	}
	if eEn.Error.Code != media.CodeQuotaExceeded {
		t.Fatalf("en error code = %q, want %q", eEn.Error.Code, media.CodeQuotaExceeded)
	}
	if !strings.Contains(eEn.Error.Message, humanBytes(2048)) || !strings.Contains(eEn.Error.Message, "quota") {
		t.Fatalf("en message %q must be English and name the limit %s", eEn.Error.Message, humanBytes(2048))
	}
	t.Logf("over-quota rejected: code=%s zh=%q en=%q", e.Error.Code, e.Error.Message, eEn.Error.Message)
}

// TestMediaQuotaFreesAfterDeleteReference 验收：删除引用（note 软删除）后配额重新释放。
func TestMediaQuotaFreesAfterDeleteReference(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	t.Setenv(envMediaUserQuotaBytes, "")
	ctx := context.Background()
	if err := store.PutSetting(ctx, db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting() error = %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")

	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", quotaPNG(1500))
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	note := referenceMedia(t, db, deck.ID, ownerID, saved.Sha256)

	if rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over-quota status = %d, want 413", rec.Code)
	}

	// 删除引用它的 note → 用量归零 → 同一文件这次应被接受。
	if err := db.Delete(note).Error; err != nil {
		t.Fatalf("soft delete note: %v", err)
	}
	if usage, err := store.UserMediaUsage(ctx, db, ownerID); err != nil {
		t.Fatalf("UserMediaUsage() error = %v", err)
	} else if usage.Bytes != 0 {
		t.Fatalf("usage after delete = %d, want 0", usage.Bytes)
	}
	if rec := uploadMedia(t, srv, cookies, csrf, "m2.png", "image/png", quotaPNG(1000)); rec.Code != http.StatusCreated {
		t.Fatalf("post-delete upload status = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	t.Logf("quota released after deleting the reference note: usage 1500 -> 0, upload of 1000 B accepted (201)")
}

// TestMediaQuotaDedupeDoesNotChargeTwice 验收口径：同一 blob 已被该用户计费时，
// 再次上传同一份字节不再叠加（去重不重复收费）。
func TestMediaQuotaDedupeDoesNotChargeTwice(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	t.Setenv(envMediaUserQuotaBytes, "")
	ctx := context.Background()
	if err := store.PutSetting(ctx, db, settingKeyMediaUserQuotaBytes, "2048", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting() error = %v", err)
	}
	deck := seedDeck(t, db, ownerID, "quota deck")
	body := quotaPNG(1500)

	first := uploadMedia(t, srv, cookies, csrf, "m1.png", "image/png", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first upload status = %d, want 201", first.Code)
	}
	var saved mediaUploadResp
	if err := json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decode first upload: %v", err)
	}
	referenceMedia(t, db, deck.ID, ownerID, saved.Sha256)

	// 1500 已计费；同 sha 再来一次若按 1500+1500=3000 计会超限，但去重后新增量为 0。
	again := uploadMedia(t, srv, cookies, csrf, "m1-copy.png", "image/png", body)
	if again.Code != http.StatusCreated {
		t.Fatalf("dedupe re-upload status = %d, want 201 (body %s)", again.Code, again.Body.String())
	}
}

// referenceMedia 让 owner 建一条引用指定媒体 sha256 的 note（模拟编辑器把 /media/<sha256> 插入字段）。
// 夹具经 note 写入方法构造：media_notes 映射由写入路径建立，测试因此走的是生产路径（L2）。
func referenceMedia(t *testing.T, db *gorm.DB, deckID, ownerID uint64, mediaSha string) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, Kind: "basic", CreatedBy: &ownerID}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note,
		map[string]any{"front": "![](/media/" + mediaSha + ")", "back": "x"}); err != nil {
		t.Fatalf("create referencing note: %v", err)
	}
	return note
}
