package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/store"
)

// TestCreateNoteForEveryKind 是 M2-12 的验收测试：登录用户为每一种已注册题型都建一张 note，
// 断言每种题型的卡片数（basic 1 / basic_both 2 / cloze 每个序号 1 张 / 其余 1 张）与审计。
// 覆盖集必须与 cardtype.Kinds() 完全一致，否则这里直接失败——防止注册新题型后验收范围悄悄缩小。
func TestCreateNoteForEveryKind(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Create deck")

	cases := []struct {
		kind  string
		form  url.Values
		cards int
	}{
		{"basic", url.Values{"field.front": {"Q1"}, "field.back": {"A1"}}, 1},
		{"basic_both", url.Values{"field.front": {"Q2"}, "field.back": {"A2"}}, 2},
		{"cloze", url.Values{"field.text": {"{{c1::one}} and {{c2::two}}"}}, 2},
		{"list", url.Values{"field.prompt": {"Name the steps"}, "field.items": {"a\nb\nc"}, "field.ordered": {"true"}}, 1},
		{"typed", url.Values{"field.prompt": {"2+2"}, "field.answer": {"4"}, "field.accept": {"four"}, "field.ignore_case": {"true"}}, 1},
		{"numeric", url.Values{"field.prompt": {"pi to 2dp"}, "field.value": {"3.14"}, "field.tolerance_absolute": {"0.01"}}, 1},
		{"choice_single", url.Values{"field.question": {"Capital of France?"}, "field.options": {"Paris\nLondon"}, "field.answer": {"0"}}, 1},
		{"choice_multi", url.Values{"field.question": {"Pick even numbers"}, "field.options": {"2\n3\n4"}, "field.answers": {"0\n2"}}, 1},
		{"true_false", url.Values{"field.statement": {"The sky is blue"}, "field.answer": {"true"}}, 1},
		{"short_answer", url.Values{"field.prompt": {"Why is the sky blue?"}, "field.reference": {"Rayleigh scattering"}}, 1},
	}

	kinds := cardtype.Kinds()
	if len(kinds) != len(cases) {
		t.Fatalf("test covers %d kinds but Registry.Kinds() has %d (%v); update the acceptance set", len(cases), len(kinds), kinds)
	}

	cardStore := store.NewCardStore(db)
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			form := url.Values{"csrf_token": {csrf}, "kind": {tc.kind}}
			for k, v := range tc.form {
				form[k] = v
			}
			rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes", form, cookies)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("POST create %s status = %d, want 303 (body %s)", tc.kind, rec.Code, snippet(rec.Body.String()))
			}
			var note store.Note
			if err := db.Where("deck_id = ? AND kind = ?", deck.ID, tc.kind).First(&note).Error; err != nil {
				t.Fatalf("note %s was not persisted: %v", tc.kind, err)
			}
			cards, err := cardStore.ByNote(context.Background(), note.ID)
			if err != nil {
				t.Fatalf("load cards for %s: %v", tc.kind, err)
			}
			if len(cards) != tc.cards {
				t.Errorf("%s produced %d cards, want %d", tc.kind, len(cards), tc.cards)
			}
			if note.Source == nil || *note.Source != "manual" {
				t.Errorf("%s note source = %v, want manual", tc.kind, note.Source)
			}
		})
	}

	audit := store.NewAuditStore(db)
	if n, err := audit.CountByAction(context.Background(), store.ActionNoteCreate); err != nil || n != int64(len(cases)) {
		t.Errorf("note.create audit rows = (%d, %v), want (%d, nil)", n, err, len(cases))
	}
}

// TestCreateNoteFormRendersLocalizedFieldLabels 覆盖 M2-10 的页面侧验收：
// 每种题型的每个字段都必须渲染出本地化标签，而不是原始 key。
// 缺 key 时 Localizer.T 会回退成 "note.field.<name>"，因此断言响应体不含该前缀即可。
func TestCreateNoteFormRendersLocalizedFieldLabels(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Labels deck")

	for _, kind := range cardtype.Kinds() {
		t.Run(kind, func(t *testing.T) {
			rec := getWithCookies(t, srv, "/decks/"+u64str(deck.ID)+"/new-note/fields?kind="+kind, cookies)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET fields fragment status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if strings.Contains(body, "note.field.") {
				t.Errorf("fields fragment for %s rendered a raw key; body = %s", kind, snippet(body))
			}
			for _, spec := range createFieldSpecsFor(kind) {
				if !strings.Contains(body, "field."+spec.name) {
					t.Errorf("fields fragment for %s is missing input for field %q", kind, spec.name)
				}
			}
		})
	}
}

// TestCreateNoteRejectsInvalidFields 覆盖验收的负例：字段集不合法时返回 400 与本地化错误，
// 且不写入任何 note。
func TestCreateNoteRejectsInvalidFields(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Invalid deck")

	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes", url.Values{
		"csrf_token":     {csrf},
		"kind":           {"choice_single"},
		"field.question": {"Only one option"},
		"field.options":  {"lonely"},
		"field.answer":   {"0"},
	}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid POST status = %d, want 400 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "字段校验失败") {
		t.Errorf("error body does not carry the localized prefix; body = %s", snippet(body))
	}
	if !strings.Contains(body, "options") {
		t.Errorf("error body does not name the offending field; body = %s", snippet(body))
	}
	_, total, err := store.NewNoteStore(db).List(context.Background(), store.NoteListOptions{DeckID: deck.ID})
	if err != nil || total != 0 {
		t.Errorf("invalid create wrote %d notes (err %v), want 0", total, err)
	}
}

// TestCreateNoteRequiresLoginAndCSRF 覆盖写操作的两道防线：匿名跳登录、缺 CSRF 403。
func TestCreateNoteRequiresLoginAndCSRF(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedDeck(t, db, ownerID, "Guard deck")
	path := "/decks/" + u64str(deck.ID) + "/new-note"

	if code := get(t, srv, path, nil).Code; code != http.StatusSeeOther {
		t.Errorf("anonymous GET create page status = %d, want 303 redirect to login", code)
	}
	rec := postForm(t, srv, "/decks/"+u64str(deck.ID)+"/notes", url.Values{
		"kind":        {"basic"},
		"field.front": {"Q"},
		"field.back":  {"A"},
	}, cookies)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST without CSRF status = %d, want 403", rec.Code)
	}
}
