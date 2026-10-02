package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// buildBulkBody 生成 n 条合法 basic note 的批量导入请求体，external_ref 用 b:<i>。
func buildBulkBody(n int) string {
	var b strings.Builder
	b.WriteString(`{"notes":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"kind":"basic","fields":{"front":"q%d","back":"a%d"},"external_ref":"b:%d"}`, i, i, i)
	}
	b.WriteString(`]}`)
	return b.String()
}

// TestBulkImportReportsInvalidRowIndexAndImportsValidRows 是 M4-4 的核心验收：
// 一个含单行非法数据的批次把合法行导入，并在报告里给出失败行的下标。
func TestBulkImportReportsInvalidRowIndexAndImportsValidRows(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_mixed", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%d/notes", deck.ID)

	// 下标 1 缺必填字段 back（非法），下标 0 与 2 合法。
	body := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"q0","back":"a0"},"external_ref":"r:0"},` +
		`{"kind":"basic","fields":{"front":"q1"},"external_ref":"r:1"},` +
		`{"kind":"basic","fields":{"front":"q2","back":"a2"},"external_ref":"r:2"}]}`

	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("import status = %d, want 200 (body %s)", status, raw)
	}
	var resp importResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("import response is not JSON: %v (%s)", err, raw)
	}
	if resp.Created != 2 || resp.Updated != 0 || resp.Skipped != 0 {
		t.Errorf("created/updated/skipped = %d/%d/%d, want 2/0/0", resp.Created, resp.Updated, resp.Skipped)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly one row error", resp.Errors)
	}
	if resp.Errors[0].Index != 1 {
		t.Errorf("error index = %d, want 1", resp.Errors[0].Index)
	}
	if strings.TrimSpace(resp.Errors[0].Reason) == "" {
		t.Error("error reason is empty, want the failing field named")
	}
	// 合法行必须真的落库（2 条 note、2 张 card），非法行不落库。
	assertNoteCardCount(t, env, deck.ID, 2, 2)
}

// TestBulkImportBatchesTwoHundredRowsPerTransaction 校验 205 条会跨多个 200 条事务批次，
// 且重复提交仍然幂等（跨批次不发生重复建卡）。
func TestBulkImportBatchesTwoHundredRowsPerTransaction(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_batches", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%d/notes", deck.ID)

	const total = 205 // > ImportBatchSize(200)，强制至少两个事务批次。
	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, buildBulkBody(total))
	if status != http.StatusOK {
		t.Fatalf("first import status = %d, want 200 (body %s)", status, raw)
	}
	var first importResponse
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("first import response is not JSON: %v (%s)", err, raw)
	}
	if first.Created != total || len(first.Errors) != 0 {
		t.Fatalf("first import = created %d errors %+v, want %d/0", first.Created, first.Errors, total)
	}
	assertNoteCardCount(t, env, deck.ID, total, total)

	// 重复提交同一批：全部按 external_ref 命中，skip 策略下全部跳过，不产生重复卡。
	body := `{"on_conflict":"skip",` + buildBulkBody(total)[1:]
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("re-import status = %d, want 200 (body %s)", status, raw)
	}
	var second importResponse
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatalf("re-import response is not JSON: %v (%s)", err, raw)
	}
	if second.Created != 0 || second.Skipped != total || len(second.Errors) != 0 {
		t.Errorf("re-import = created %d skipped %d errors %+v, want 0/%d/0",
			second.Created, second.Skipped, second.Errors, total)
	}
	assertNoteCardCount(t, env, deck.ID, total, total)
}

// TestBulkImportIsolatesRowErrorWithinBatch 触发批次内真实的数据库级行错误（同一 external_ref
// 在同一请求里出现两次 -> 第二行撞唯一约束），证明失败行被隔离并带下标上报，
// 同批其余合法行仍然提交（保存点回滚路径）。
func TestBulkImportIsolatesRowErrorWithinBatch(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_isolation", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%d/notes", deck.ID)

	// 下标 0 与 1 用了同一个 external_ref：第一遍规划时都查不到已有行，都会尝试新建；
	// 批内第二行撞 (deck_id, external_ref) 唯一约束，应只报它，0 与 2 正常落库。
	body := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"a","back":"A"},"external_ref":"dup"},` +
		`{"kind":"basic","fields":{"front":"b","back":"B"},"external_ref":"dup"},` +
		`{"kind":"basic","fields":{"front":"c","back":"C"},"external_ref":"uniq"}]}`

	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("import status = %d, want 200 (body %s)", status, raw)
	}
	var resp importResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("import response is not JSON: %v (%s)", err, raw)
	}
	if resp.Created != 2 {
		t.Errorf("created = %d, want 2 (valid rows survive a row-level failure)", resp.Created)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Index != 1 {
		t.Fatalf("errors = %+v, want exactly one at index 1", resp.Errors)
	}
	assertNoteCardCount(t, env, deck.ID, 2, 2)
}

// TestBulkImportResumesAfterPartialFailure 证明失败可续：第一次导入有一行非法时合法行已落库；
// 修正该行后按 skip 重放，已导入的行跳过（不重做），只补建缺失的一行。
func TestBulkImportResumesAfterPartialFailure(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	user := seedUser(t, env.db, "bulk_resume", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%d/notes", deck.ID)

	broken := `{"notes":[` +
		`{"kind":"basic","fields":{"front":"a","back":"A"},"external_ref":"r:0"},` +
		`{"kind":"basic","fields":{"front":"b"},"external_ref":"r:1"},` +
		`{"kind":"basic","fields":{"front":"c","back":"C"},"external_ref":"r:2"}]}`
	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, broken)
	if status != http.StatusOK {
		t.Fatalf("broken import status = %d, want 200 (body %s)", status, raw)
	}
	var first importResponse
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("broken import response is not JSON: %v (%s)", err, raw)
	}
	if first.Created != 2 || len(first.Errors) != 1 {
		t.Fatalf("broken import = created %d errors %+v, want 2/1", first.Created, first.Errors)
	}
	assertNoteCardCount(t, env, deck.ID, 2, 2)

	// 修正 r:1 后整批重放：skip 让已导入的两行不重做，只为修复行建一条。
	fixed := `{"on_conflict":"skip","notes":[` +
		`{"kind":"basic","fields":{"front":"a","back":"A"},"external_ref":"r:0"},` +
		`{"kind":"basic","fields":{"front":"b","back":"B"},"external_ref":"r:1"},` +
		`{"kind":"basic","fields":{"front":"c","back":"C"},"external_ref":"r:2"}]}`
	status, raw = doJSON(t, router, http.MethodPost, path, k.Plaintext, fixed)
	if status != http.StatusOK {
		t.Fatalf("resume import status = %d, want 200 (body %s)", status, raw)
	}
	var resumed importResponse
	if err := json.Unmarshal(raw, &resumed); err != nil {
		t.Fatalf("resume import response is not JSON: %v (%s)", err, raw)
	}
	if resumed.Created != 1 || resumed.Skipped != 2 || len(resumed.Errors) != 0 {
		t.Errorf("resume import = created %d skipped %d errors %+v, want 1/2/0",
			resumed.Created, resumed.Skipped, resumed.Errors)
	}
	assertNoteCardCount(t, env, deck.ID, 3, 3)
}
