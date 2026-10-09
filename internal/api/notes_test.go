package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// doJSON 发一个带 bearer key 的 JSON 请求并返回状态码与响应体。
func doJSON(t *testing.T, h http.Handler, method, path, plaintext, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if plaintext != "" {
		req.Header.Set("Authorization", "Bearer "+plaintext)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// TestBulkImportIsIdempotentByExternalRef 是核心验收：
// 同一批数据重复提交不产生重复卡，且 created/updated/skipped 计数正确。
func TestBulkImportIsIdempotentByExternalRef(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "importer", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite, store.ScopeRead}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)

	body := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"a","back":"b"},"external_ref":"n:1"},` +
		`{"kind":"basic","fields":{"front":"c","back":"d"},"external_ref":"n:2"}]}`

	// 第一次导入：全部新建。
	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("first import status = %d, want 200 (body %s)", status, raw)
	}
	var first importResponse
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("first import response is not JSON: %v (%s)", err, raw)
	}
	if first.Created != 2 || first.Updated != 0 || first.Skipped != 0 {
		t.Errorf("first import = created %d updated %d skipped %d, want 2/0/0",
			first.Created, first.Updated, first.Skipped)
	}
	if len(first.Errors) != 0 {
		t.Errorf("first import errors = %v, want none", first.Errors)
	}

	// 第二次导入同一批：按 external_ref 命中已有卡，全部更新，不新增。
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("second import status = %d, want 200 (body %s)", status, raw)
	}
	var second importResponse
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatalf("second import response is not JSON: %v (%s)", err, raw)
	}
	if second.Created != 0 || second.Updated != 2 || second.Skipped != 0 {
		t.Errorf("second import = created %d updated %d skipped %d, want 0/2/0",
			second.Created, second.Updated, second.Skipped)
	}

	// 第三次 on_conflict=skip：全部跳过，仍不新增。
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext,
		`{"on_conflict":"skip","notes":[`+
			`{"kind":"basic","fields":{"front":"a","back":"b"},"external_ref":"n:1"},`+
			`{"kind":"basic","fields":{"front":"c","back":"d"},"external_ref":"n:2"}]}`)
	if status != http.StatusOK {
		t.Fatalf("skip import status = %d, want 200 (body %s)", status, raw)
	}
	var third importResponse
	if err := json.Unmarshal(raw, &third); err != nil {
		t.Fatalf("skip import response is not JSON: %v (%s)", err, raw)
	}
	if third.Created != 0 || third.Updated != 0 || third.Skipped != 2 {
		t.Errorf("skip import = created %d updated %d skipped %d, want 0/0/2",
			third.Created, third.Updated, third.Skipped)
	}

	// 库里的 note / card 数必须是 2 —— 重复导入没有产生重复卡。
	assertNoteCardCount(t, env, deck.ID, 2, 2)

	// dry_run 只算不写：新 ref 报告 created=1，但库里仍是 2 张。
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext,
		`{"dry_run":true,"notes":[{"kind":"basic","fields":{"front":"e","back":"f"},"external_ref":"n:3"}]}`)
	if status != http.StatusOK {
		t.Fatalf("dry_run status = %d, want 200 (body %s)", status, raw)
	}
	var dry importResponse
	if err := json.Unmarshal(raw, &dry); err != nil {
		t.Fatalf("dry_run response is not JSON: %v (%s)", err, raw)
	}
	if dry.Created != 1 || !dry.DryRun {
		t.Errorf("dry_run = created %d dry_run %v, want 1/true", dry.Created, dry.DryRun)
	}
	assertNoteCardCount(t, env, deck.ID, 2, 2)
}

// assertNoteCardCount 断言卡组下的note与card数量。
func assertNoteCardCount(t *testing.T, env *testEnv, deckID uint64, wantNotes, wantCards int64) {
	t.Helper()
	var notes int64
	if err := env.db.Model(&store.Note{}).Where("deck_id = ?", deckID).Count(&notes).Error; err != nil {
		t.Fatalf("count notes: %v", err)
	}
	if notes != wantNotes {
		t.Errorf("notes = %d, want %d", notes, wantNotes)
	}
	var cards int64
	if err := env.db.Model(&store.Card{}).
		Joins("JOIN notes ON notes.id = cards.note_id").
		Where("notes.deck_id = ?", deckID).Count(&cards).Error; err != nil {
		t.Fatalf("count cards: %v", err)
	}
	if cards != wantCards {
		t.Errorf("cards = %d, want %d", cards, wantCards)
	}
}

// TestImportRejectsScopeAndMalformedBody 覆盖写 scope 与请求体校验的反面用例。
func TestImportRejectsScopeAndMalformedBody(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "scope_user", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	readKey := seedKey(t, env.keys, user.ID, []string{store.ScopeRead}, nil)
	writeKey := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%s/notes", deck.PublicID)

	// 只有 read scope 的 key 调写接口 → 403 scope_required。
	status, raw := doJSON(t, router, http.MethodPost, path, readKey.Plaintext,
		`{"notes":[{"kind":"basic","fields":{"front":"a","back":"b"}}]}`)
	if status != http.StatusForbidden {
		t.Fatalf("read key write status = %d, want 403 (body %s)", status, raw)
	}

	// 空 notes 数组 → 400 invalid_request。
	status, raw = doJSON(t, router, http.MethodPost, path, writeKey.Plaintext, `{"notes":[]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("empty notes status = %d, want 400 (body %s)", status, raw)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Error.Code != CodeInvalidRequest {
		t.Fatalf("error envelope = %s, want code %q", raw, CodeInvalidRequest)
	}
}
