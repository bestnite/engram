package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// TestNoteTypeStocksList covers the static stock definition list: engram's ten definitions, the four
// common templates, then the two Anki definitions engram has no equivalent for, each with an
// unambiguous source-prefixed id and a catalog key. A shape engram defines itself is not offered
// twice, so the picker never shows two entries with the same label.
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
	if len(body.Stocks) != 16 {
		t.Fatalf("stock count = %d, want 16 (engram ten, four templates, two Anki-only)", len(body.Stocks))
	}
	if body.Stocks[0].ID != "engram:basic" || body.Stocks[0].Source != stockSourceEngram || body.Stocks[0].LabelKey != "notetype.stock.basic" {
		t.Fatalf("first stock = %+v, want engram basic", body.Stocks[0])
	}
	if body.Stocks[10].ID != "template:vocabulary" || body.Stocks[10].Source != stockSourceTemplate || body.Stocks[10].LabelKey != "notetype.stock.vocabulary" {
		t.Fatalf("eleventh stock = %+v, want the vocabulary template", body.Stocks[10])
	}
	if body.Stocks[14].ID != "engram:basic_optional_reversed" || body.Stocks[15].ID != "engram:image_occlusion" {
		t.Fatalf("last two stocks = %+v, %+v, want the two Anki-only definitions", body.Stocks[14], body.Stocks[15])
	}
	ids := map[string]bool{}
	names := map[string]string{}
	labelKeys := map[string]string{}
	for _, stock := range body.Stocks {
		if stock.Name == "" {
			t.Errorf("stock %q has no name", stock.ID)
		}
		if ids[stock.ID] {
			t.Errorf("duplicate stock id %q", stock.ID)
		}
		ids[stock.ID] = true
		if other, exists := names[stock.Name]; exists {
			t.Errorf("stocks %q and %q both display the name %q", other, stock.ID, stock.Name)
		}
		names[stock.Name] = stock.ID
		// The catalog key carries the kind alone, so two definitions sharing a kind would also
		// share a label; the picker would show the same name twice again.
		if other, exists := labelKeys[stock.LabelKey]; exists {
			t.Errorf("stocks %q and %q both use the label key %q", other, stock.ID, stock.LabelKey)
		}
		labelKeys[stock.LabelKey] = stock.ID
	}
	// Every definition is listed under the source engram owns; an id in the reference release's
	// namespace would offer the same shape twice or name a source the picker no longer has.
	for id := range ids {
		if strings.HasPrefix(id, "anki:") {
			t.Errorf("stock %q still uses the reference release's id namespace", id)
		}
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

	// An Anki id whose shape engram defines itself is no longer offered, so it does not resolve.
	code, raw = doJSON(t, router, http.MethodPost, base, key, `{"stock_id":"anki:basic"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("POST replaced stock type = %d, want 400 (body %s)", code, raw)
	}
	assertErrorCode(t, raw, CodeInvalidRequest)

	// The Anki definitions engram does not replace still create, styled like engram's own.
	code, raw = doJSON(t, router, http.MethodPost, base, key, `{"stock_id":"engram:image_occlusion"}`)
	if code != http.StatusCreated {
		t.Fatalf("POST Anki-only stock type = %d, want 201 (body %s)", code, raw)
	}
	var occlusion NoteTypeJSON
	if err := json.Unmarshal(raw, &occlusion); err != nil {
		t.Fatalf("created Anki-only note type is not JSON: %v (%s)", err, raw)
	}
	if occlusion.Name != "Image Occlusion" || occlusion.Config.CSS != notetype.DefaultCSS {
		t.Fatalf("created Anki-only note type = %+v, want image occlusion with DefaultCSS", occlusion)
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
