package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// TestNoteTypeStocksList covers the static stock definition list: Anki's six definitions, the four
// common templates, then engram's ten, each with an unambiguous source-prefixed id and a catalog key.
func TestNoteTypeStocksList(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_stocks_owner", store.RoleUser)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil).Plaintext

	code, raw := doJSON(t, router, http.MethodGet, "/api/v1/note-type-stocks", key, "")
	if code != http.StatusOK {
		t.Fatalf("GET stock list = %d, want 200 (body %s)", code, raw)
	}
	var body NoteTypeStocksResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("stock list is not JSON: %v (%s)", err, raw)
	}
	if len(body.Stocks) != 20 {
		t.Fatalf("stock count = %d, want 20 (Anki six, four templates, engram ten)", len(body.Stocks))
	}
	if body.Stocks[0].ID != "anki:basic" || body.Stocks[0].Source != stockSourceAnki || body.Stocks[0].LabelKey != "notetype.stock.anki.basic" {
		t.Fatalf("first stock = %+v, want Anki basic", body.Stocks[0])
	}
	if body.Stocks[6].ID != "template:vocabulary" || body.Stocks[6].Source != stockSourceTemplate || body.Stocks[6].LabelKey != "notetype.stock.template.vocabulary" {
		t.Fatalf("seventh stock = %+v, want the vocabulary template", body.Stocks[6])
	}
	if body.Stocks[10].ID != "engram:basic" || body.Stocks[10].Source != stockSourceEngram || body.Stocks[10].LabelKey != "notetype.stock.engram.basic" {
		t.Fatalf("eleventh stock = %+v, want engram basic", body.Stocks[10])
	}
	ids := map[string]bool{}
	for _, stock := range body.Stocks {
		if stock.Name == "" {
			t.Errorf("stock %q has no name", stock.ID)
		}
		if ids[stock.ID] {
			t.Errorf("duplicate stock id %q", stock.ID)
		}
		ids[stock.ID] = true
	}
}

// TestCreateNoteTypeFromStock covers the stock creation shape: only the stock id is sent, the
// server builds the config, and an empty name falls back to the stock name. An unknown id is a
// request error, not a 500.
func TestCreateNoteTypeFromStock(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "note_type_stock_create", store.RoleUser)
	deck := seedDeck(t, env.db, owner.ID)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead, store.ScopeWrite}, nil).Plaintext
	base := fmt.Sprintf("/api/v1/decks/%s/note-types", deck.PublicID)

	code, raw := doJSON(t, router, http.MethodPost, base, key, `{"stock_id":"engram:numeric"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST stock note type = %d, want 201 (body %s)", code, raw)
	}
	var created NoteTypeJSON
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("created stock note type is not JSON: %v (%s)", err, raw)
	}
	want, ok := notetype.EngramStockByKind("numeric")
	if !ok {
		t.Fatalf("engram stock numeric is missing")
	}
	if created.Name != want.Name || created.Version != 0 {
		t.Fatalf("created stock note type = %+v, want name %q and version 0", created, want.Name)
	}
	if created.Config.Kind != want.Config.Kind || len(created.Config.Fields) != len(want.Config.Fields) || len(created.Config.Templates) != len(want.Config.Templates) {
		t.Fatalf("created config = %+v, want the engram numeric stock config", created.Config)
	}

	// A common template creates the same way, from its id alone.
	code, raw = doJSON(t, router, http.MethodPost, base, key, `{"stock_id":"template:listening"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST template note type = %d, want 201 (body %s)", code, raw)
	}
	var listening NoteTypeJSON
	if err := json.Unmarshal(raw, &listening); err != nil {
		t.Fatalf("created template note type is not JSON: %v (%s)", err, raw)
	}
	if listening.Name != "Listening (TTS)" || len(listening.Config.Fields) != 2 || listening.Config.Templates[0].QFormat != "{{tts zh_CN:Text}}" {
		t.Fatalf("created template note type = %+v, want the listening template", listening)
	}

	code, raw = doJSON(t, router, http.MethodPost, base, key, `{"stock_id":"anki:does_not_exist"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("POST unknown stock type = %d, want 400 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeInvalidRequest)
}

// TestAnswerControlsList covers the answer-control registry list the note type editor reads:
// every registered control with its declared roles, sorted by name, no hard-coded frontend list.
func TestAnswerControlsList(t *testing.T) {
	env := newTestEnv(t, 1000, 1000)
	router := env.router()
	owner := seedUser(t, env.db, "answer_controls_owner", store.RoleUser)
	key := seedKey(t, env.keys, owner.ID, []string{store.ScopeRead}, nil).Plaintext

	code, raw := doJSON(t, router, http.MethodGet, "/api/v1/answer-controls", key, "")
	if code != http.StatusOK {
		t.Fatalf("GET answer controls = %d, want 200 (body %s)", code, raw)
	}
	var body AnswerControlsResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("answer controls is not JSON: %v (%s)", err, raw)
	}
	if len(body.Controls) == 0 {
		t.Fatalf("answer controls are empty")
	}
	var numeric *AnswerControlJSON
	for i := range body.Controls {
		if body.Controls[i].Name == "numeric" {
			numeric = &body.Controls[i]
		}
	}
	if numeric == nil {
		t.Fatalf("numeric control is missing: %+v", body.Controls)
	}
	var value *AnswerControlRoleJSON
	for i := range numeric.Roles {
		if numeric.Roles[i].Name == "value" {
			value = &numeric.Roles[i]
		}
	}
	if value == nil || value.Kind != "number" || !value.Required {
		t.Fatalf("numeric roles = %+v, want a required number value role", numeric.Roles)
	}
	if value.LabelKey != "notetype.answer.role.value" {
		t.Fatalf("value role label_key = %q, want notetype.answer.role.value", value.LabelKey)
	}
	if numeric.LabelKey != "notetype.answer.control.numeric" {
		t.Fatalf("numeric label_key = %q, want notetype.answer.control.numeric", numeric.LabelKey)
	}
}
