package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// containsTag 判断标签切片是否含目标值（api 包内测试辅助）。
func containsTag(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// importErrIndices 取出报告里所有行错误的下标，便于断言"只错这几行"。
func importErrIndices(t *testing.T, raw []byte) map[int]string {
	t.Helper()
	var resp importResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("import response is not JSON: %v (%s)", err, raw)
	}
	out := make(map[int]string, len(resp.Errors))
	for _, e := range resp.Errors {
		out[e.Index] = e.Reason
	}
	return out
}

// TestImportByNoteIDRewritesInPlaceKeepingCardsAndProgress 覆盖 Part 2：
// 一次导入按 note_id 改写多条 note 的字段与标签，保留每张 card 的 id 与所有用户的进度；
// 跨卡组 id 与"同时带 note_id 与 external_ref"的行各报一条行错误，其余行照常写入。
func TestImportByNoteIDRewritesInPlaceKeepingCardsAndProgress(t *testing.T) {
	env := newTestEnv(t, 60, 60)
	ctx := context.Background()
	user := seedUser(t, env.db, "import_byid", store.RoleUser)
	other := seedUser(t, env.db, "import_other", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	deckB := seedDeck(t, env.db, other.ID)

	n1 := seedBasicNote(t, env, deck.ID, "old1", "oldA", []string{"keep"})
	n2 := seedBasicNote(t, env, deck.ID, "old2", "oldB", nil)
	foreign := seedBasicNote(t, env, deckB.ID, "f", "f", nil)

	cardStore := store.NewCardStore(env.db)
	origCards, err := cardStore.ByNote(ctx, n1.ID)
	if err != nil || len(origCards) != 1 {
		t.Fatalf("load cards for note %d = (%d cards, %v), want 1 card", n1.ID, len(origCards), err)
	}
	origCardID := origCards[0].ID
	due := env.now.Add(24 * time.Hour)
	if err := env.db.Create(&store.CardState{
		CardID: origCardID, UserID: user.ID, State: "review", DueAt: &due, Reps: 4, Lapses: 2,
	}).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}

	k := seedKey(t, env.keys, user.ID, []string{store.ScopeWrite}, nil)
	router := env.router()
	path := fmt.Sprintf("/api/v1/decks/%d/notes", deck.ID)
	body := fmt.Sprintf(`{"notes":[`+
		`{"note_id":%d,"kind":"basic","fields":{"front":"new1","back":"newA"},"tags":["t1"]},`+
		`{"note_id":%d,"kind":"basic","fields":{"front":"new2","back":"newB"}},`+
		`{"note_id":%d,"kind":"basic","fields":{"front":"x","back":"y"}},`+
		`{"note_id":%d,"kind":"basic","fields":{"front":"z","back":"z"},"external_ref":"e:1"}`+
		`]}`, n1.ID, n2.ID, foreign.ID, n1.ID)

	status, raw := doJSON(t, router, http.MethodPost, path, k.Plaintext, body)
	if status != http.StatusOK {
		t.Fatalf("import status = %d, want 200 (body %s)", status, raw)
	}
	var resp importResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("import response is not JSON: %v (%s)", err, raw)
	}
	if resp.Updated != 2 || resp.Created != 0 {
		t.Errorf("updated/created = %d/%d, want 2/0", resp.Updated, resp.Created)
	}
	errs := importErrIndices(t, raw)
	if len(errs) != 2 || errs[2] == "" || errs[3] == "" {
		t.Fatalf("row errors = %+v, want exactly indices 2 and 3", errs)
	}
	if !strings.Contains(errs[2]+" "+errs[3], "note not found in deck") {
		t.Errorf("cross-deck row error = %q / %q, want one of them 'note not found in deck'", errs[2], errs[3])
	}

	// 字段与标签被就地改写。
	noteStore := store.NewNoteStore(env.db)
	got1, err := noteStore.ByID(ctx, n1.ID)
	if err != nil {
		t.Fatalf("reload note %d: %v", n1.ID, err)
	}
	fields1, _ := store.ParseFields(got1.FieldsJSON)
	if fields1["front"] != "new1" || fields1["back"] != "newA" {
		t.Errorf("note %d fields = %v, want the imported values", n1.ID, fields1)
	}
	if tags1, _ := store.ParseTags(got1.TagsJSON); !containsTag(tags1, "t1") {
		t.Errorf("note %d tags = %v, want to contain t1", n1.ID, tags1)
	}
	got2, err := noteStore.ByID(ctx, n2.ID)
	if err != nil {
		t.Fatalf("reload note %d: %v", n2.ID, err)
	}
	if fields2, _ := store.ParseFields(got2.FieldsJSON); fields2["front"] != "new2" {
		t.Errorf("note %d fields = %v, want the imported values", n2.ID, fields2)
	}

	// 现有 card 的 id 与它的复习进度必须原样保留。
	newCards, err := cardStore.ByNote(ctx, n1.ID)
	if err != nil || len(newCards) != 1 {
		t.Fatalf("reload cards for note %d = (%d, %v), want 1", n1.ID, len(newCards), err)
	}
	if newCards[0].ID != origCardID {
		t.Errorf("card id changed from %d to %d; in-place update must reuse the card", origCardID, newCards[0].ID)
	}
	var state store.CardState
	if err := env.db.Where("card_id = ? AND user_id = ?", origCardID, user.ID).First(&state).Error; err != nil {
		t.Fatalf("reload card state: %v", err)
	}
	if state.Reps != 4 || state.Lapses != 2 {
		t.Errorf("card state = (reps %d, lapses %d), want (4, 2): progress must survive an in-place rewrite", state.Reps, state.Lapses)
	}

	// 没有新建 note 或 card（跨卡组行被拒、其余行就地更新）。
	assertNoteCardCount(t, env, deck.ID, 2, 2)
}
