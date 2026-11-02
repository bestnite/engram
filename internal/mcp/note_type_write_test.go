package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

func writeConfig(prefix string) notetype.Config {
	return notetype.Config{Kind: notetype.KindNormal, Fields: []notetype.Field{{ID: prefix + "f", Name: "Front"}, {ID: prefix + "b", Name: "Back"}}, Templates: []notetype.Template{{ID: prefix + "one", Name: "One", QFormat: "{{Front}}", AFormat: "{{Back}}"}, {ID: prefix + "two", Name: "Two", QFormat: "{{Back}}", AFormat: "{{Front}}"}}, SortFieldID: prefix + "f"}
}

func mustTool(t *testing.T, cs *sdkmcp.ClientSession, name string, args any) map[string]any {
	t.Helper()
	out, bad, text := callTool(t, cs, name, args)
	if bad {
		t.Fatalf("%s: %s", name, text)
	}
	return out
}

func failTool(t *testing.T, cs *sdkmcp.ClientSession, name string, args any, parts ...string) {
	t.Helper()
	_, bad, text := callTool(t, cs, name, args)
	if !bad {
		t.Fatalf("%s unexpectedly succeeded: %s", name, text)
	}
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Fatalf("%s error %q lacks %q", name, text, part)
		}
	}
}

type writeSnapshot struct {
	Types   []store.NoteType
	Notes   []store.Note
	Cards   []store.Card
	States  []store.CardState
	Reviews []store.Review
	Audit   []store.AuditLog
}

func snapshot(t *testing.T, db *gorm.DB) writeSnapshot {
	t.Helper()
	var s writeSnapshot
	for _, dst := range []any{&s.Types, &s.Notes, &s.Cards, &s.States, &s.Reviews} {
		if err := db.Unscoped().Order("1").Find(dst).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Authentication audits each HTTP request independently of the business operation.
	if err := db.Where("action <> ?", "api.request").Order("id").Find(&s.Audit).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func unchanged(t *testing.T, db *gorm.DB, before writeSnapshot) {
	t.Helper()
	if after := snapshot(t, db); !reflect.DeepEqual(before, after) {
		t.Fatalf("database changed\nbefore: %+v\nafter: %+v", before, after)
	}
}

type writeFixture struct {
	db    *gorm.DB
	cs    *sdkmcp.ClientSession
	user  *store.User
	deck  *store.Deck
	keyID uint64
}

func newWriteFixture(t *testing.T) writeFixture {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "write_owner")
	d := seedDeck(t, db, u.ID)
	key := newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite})
	var k store.APIKey
	if err := db.Where("user_id = ?", u.ID).First(&k).Error; err != nil {
		t.Fatal(err)
	}
	return writeFixture{db, connect(t, ts.URL, key), u, d, k.ID}
}

func (f writeFixture) createType(t *testing.T, name string, cfg notetype.Config) map[string]any {
	t.Helper()
	return mustTool(t, f.cs, "create_note_type", map[string]any{"deck_id": f.deck.PublicID, "name": name, "config": cfg})
}

func (f writeFixture) createNote(t *testing.T, nt map[string]any) (store.Note, []store.Card) {
	t.Helper()
	mustTool(t, f.cs, "create_notes", map[string]any{"deck_id": f.deck.PublicID, "notes": []any{map[string]any{"note_type": nt["id"], "fields": map[string]any{"Front": "Question", "Back": "Answer"}}}})
	var n store.Note
	if err := f.db.Where("deck_id = ?", f.deck.ID).Order("id desc").First(&n).Error; err != nil {
		t.Fatal(err)
	}
	var cards []store.Card
	if err := f.db.Where("note_id = ?", n.ID).Order("id").Find(&cards).Error; err != nil || len(cards) != 2 {
		t.Fatalf("cards: %v %v", cards, err)
	}
	for _, c := range cards {
		if err := f.db.Create(&store.CardState{CardID: c.ID, UserID: f.user.ID, State: "review", Reps: 3}).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Create(&store.Review{CardID: c.ID, UserID: f.user.ID, Rating: 3, GradeSource: "self", ReviewedAt: time.Now().UTC(), ReviewDay: "2026-10-31", StateBefore: 2}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return n, cards
}

func (f writeFixture) audit(t *testing.T, action string) {
	t.Helper()
	var row store.AuditLog
	if err := f.db.Where("action = ?", action).Order("id desc").First(&row).Error; err != nil || row.APIKeyID == nil || *row.APIKeyID != f.keyID {
		t.Fatalf("audit %s: %+v %v", action, row, err)
	}
}

func equalJSON(t *testing.T, a, b any) {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var xx, yy any
	if json.Unmarshal(x, &xx) != nil || json.Unmarshal(y, &yy) != nil || !reflect.DeepEqual(xx, yy) {
		t.Fatalf("JSON mismatch: %s != %s", x, y)
	}
}

func TestM20MetadataAndCreate(t *testing.T) {
	a, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "metadata_owner")
	d := seedDeck(t, db, u.ID)
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite}))
	equalJSON(t, mustTool(t, cs, "list_note_type_stocks", map[string]any{}), a.NoteTypeStocks())
	equalJSON(t, mustTool(t, cs, "list_answer_controls", map[string]any{}), a.AnswerControls())
	created := mustTool(t, cs, "create_note_type", map[string]any{"deck_id": d.PublicID, "stock_id": "engram:basic"})
	stock, ok := notetype.EngramStockByKind("basic")
	if !ok {
		t.Fatal("engram stock basic is missing")
	}
	basic := stock.Config
	if created["name"] != "Basic" {
		t.Fatalf("name: %v", created)
	}
	equalJSON(t, created["config"], basic)
	preferred := mustTool(t, cs, "create_note_type", map[string]any{"deck_id": d.PublicID, "stock_id": "engram:basic", "config": writeConfig("ignored-")})
	equalJSON(t, preferred["config"], basic)
	cfg := writeConfig("custom-")
	custom := mustTool(t, cs, "create_note_type", map[string]any{"deck_id": d.PublicID, "name": "Custom", "config": cfg})
	equalJSON(t, custom["config"], cfg)
	failTool(t, cs, "create_note_type", map[string]any{"deck_id": d.PublicID, "stock_id": "unknown"}, "invalid_request")
	failTool(t, cs, "create_note_type", map[string]any{"deck_id": d.PublicID, "name": "Missing"}, "invalid_request")
	failTool(t, cs, "create_note_type", map[string]any{"deck_id": d.PublicID, "name": "Invalid", "config": map[string]any{}}, "validating")
	args := map[string]any{"deck_id": d.PublicID, "note_type_id": custom["id"], "name": "Custom", "config": cfg}
	failTool(t, cs, "update_note_type", args)
	args["version"] = -100
	before := snapshot(t, db)
	failTool(t, cs, "update_note_type", args, "version_conflict")
	unchanged(t, db, before)
}

func TestM20UpdateRemoval(t *testing.T) {
	f := newWriteFixture(t)
	cfg := writeConfig("s-")
	nt := f.createType(t, "Source", cfg)
	_, cards := f.createNote(t, nt)
	cfg.Templates = cfg.Templates[:1]
	args := map[string]any{"deck_id": f.deck.PublicID, "note_type_id": nt["id"], "name": "Source", "config": cfg, "version": nt["version"]}
	before := snapshot(t, f.db)
	failTool(t, f.cs, "update_note_type", args, "card_removal_unconfirmed", "cards_added=0", "cards_removed=1", "reviews_on_removed_cards=1")
	unchanged(t, f.db, before)
	args["dry_run"] = true
	out := mustTool(t, f.cs, "update_note_type", args)
	checkCounts(t, out, map[string]int{"cards_added": 0, "cards_removed": 1, "reviews_on_removed_cards": 1})
	unchanged(t, f.db, before)
	args["dry_run"] = false
	args["confirm_card_removal"] = true
	checkCounts(t, mustTool(t, f.cs, "update_note_type", args), map[string]int{"cards_added": 0, "cards_removed": 1, "reviews_on_removed_cards": 1})
	f.audit(t, store.ActionNoteTypeUpdate)
	checkProgress(t, f.db, cards)
	var kept, removed store.Card
	if err := f.db.Unscoped().First(&kept, cards[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Unscoped().First(&removed, cards[1].ID).Error; err != nil {
		t.Fatal(err)
	}
	if kept.DeletedAt.Valid || !removed.DeletedAt.Valid {
		t.Fatalf("card visibility: kept=%+v removed=%+v", kept, removed)
	}
}

func checkCounts(t *testing.T, out map[string]any, counts map[string]int) {
	t.Helper()
	for k, v := range counts {
		if fmt.Sprint(out[k]) != fmt.Sprint(v) {
			t.Fatalf("%s = %v want %d, output %v", k, out[k], v, out)
		}
	}
}

func checkProgress(t *testing.T, db *gorm.DB, cards []store.Card) {
	t.Helper()
	for _, c := range cards {
		var state store.CardState
		var review store.Review
		var card store.Card
		if err := db.Unscoped().First(&card, c.ID).Error; err != nil || card.PublicID != c.PublicID {
			t.Fatalf("card: %+v %v", card, err)
		}
		if err := db.Where("card_id = ?", c.ID).First(&state).Error; err != nil || state.Reps != 3 {
			t.Fatalf("state: %+v %v", state, err)
		}
		if err := db.Where("card_id = ?", c.ID).First(&review).Error; err != nil || review.Rating != 3 {
			t.Fatalf("review: %+v %v", review, err)
		}
	}
}

func TestM20DeletePurge(t *testing.T) {
	f := newWriteFixture(t)
	nt := f.createType(t, "Source", writeConfig("s-"))
	note, cards := f.createNote(t, nt)
	args := map[string]any{"deck_id": f.deck.PublicID, "note_type_id": nt["id"]}
	before := snapshot(t, f.db)
	failTool(t, f.cs, "delete_note_type", args, "note_type_in_use")
	unchanged(t, f.db, before)
	mustTool(t, f.cs, "delete_note", map[string]any{"note_id": note.PublicID})
	before = snapshot(t, f.db)
	failTool(t, f.cs, "delete_note_type", args, "note_type_purge_unconfirmed", "deleted_notes=1", "cards=2", "reviews=2")
	unchanged(t, f.db, before)
	args["confirm_purge"] = true
	mustTool(t, f.cs, "delete_note_type", args)
	f.audit(t, store.ActionNoteTypeDelete)
	var count int64
	for _, q := range []struct {
		model  any
		column string
		value  any
	}{{&store.NoteType{}, "public_id", nt["id"]}, {&store.Note{}, "public_id", note.PublicID}, {&store.Card{}, "public_id", cards[0].PublicID}, {&store.Card{}, "public_id", cards[1].PublicID}} {
		if err := f.db.Unscoped().Model(q.model).Where(q.column+" = ?", q.value).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("purge %s: %d %v", q.value, count, err)
		}
	}
}

func TestM20ChangeNoteType(t *testing.T) {
	for _, destructive := range []bool{false, true} {
		t.Run(fmt.Sprint(destructive), func(t *testing.T) {
			f := newWriteFixture(t)
			source := f.createType(t, "Source", writeConfig("s-"))
			target := f.createType(t, "Target", writeConfig("t-"))
			note, cards := f.createNote(t, source)
			mapping := map[string]string{"s-one": "t-one"}
			if !destructive {
				mapping["s-two"] = "t-two"
			}
			args := map[string]any{"deck_id": f.deck.PublicID, "note_type_id": source["id"], "target_note_type": target["id"], "field_mapping": map[string]string{"s-f": "t-f", "s-b": "t-b"}, "template_mapping": mapping}
			before := snapshot(t, f.db)
			if destructive {
				failTool(t, f.cs, "change_note_type", args, "card_removal_unconfirmed", "notes_moved=1", "cards_moved=1", "cards_added=1", "cards_removed=1", "reviews_on_removed_cards=1")
				unchanged(t, f.db, before)
			}
			args["dry_run"] = true
			preview := mustTool(t, f.cs, "change_note_type", args)
			counts := map[string]int{"notes_moved": 1, "cards_moved": 2, "cards_added": 0, "cards_removed": 0, "reviews_on_removed_cards": 0}
			if destructive {
				counts = map[string]int{"notes_moved": 1, "cards_moved": 1, "cards_added": 1, "cards_removed": 1, "reviews_on_removed_cards": 1}
			}
			checkCounts(t, preview, counts)
			unchanged(t, f.db, before)
			args["dry_run"] = false
			args["confirm_card_removal"] = true
			out := mustTool(t, f.cs, "change_note_type", args)
			for _, k := range []string{"notes_moved", "cards_moved", "cards_added", "cards_removed", "reviews_on_removed_cards"} {
				if out[k] != preview[k] {
					t.Fatalf("%s preview=%v result=%v", k, preview, out)
				}
			}
			var updated store.Note
			if err := f.db.First(&updated, note.ID).Error; err != nil || updated.NoteTypeID == nil {
				t.Fatal(err)
			}
			var targetRow store.NoteType
			if err := f.db.Where("public_id = ?", target["id"]).First(&targetRow).Error; err != nil || *updated.NoteTypeID != targetRow.ID {
				t.Fatalf("note target %+v %v", updated, err)
			}
			checkProgress(t, f.db, cards)
			f.audit(t, store.ActionNoteTypeChange)
		})
	}
}

func TestM20EmptyCards(t *testing.T) {
	f := newWriteFixture(t)
	nt := f.createType(t, "Source", writeConfig("s-"))
	note, cards := f.createNote(t, nt)
	mustTool(t, f.cs, "update_note", map[string]any{"note_id": note.PublicID, "note_type": nt["id"], "fields": map[string]any{"Front": "Question", "Back": ""}})
	var reverse store.Card
	for _, c := range cards {
		if c.Template == "s-two" {
			reverse = c
		}
	}
	args := map[string]any{"deck_id": f.deck.PublicID}
	list := mustTool(t, f.cs, "list_empty_cards", args)
	checkCounts(t, list, map[string]int{"count": 1})
	items := list["cards"].([]any)
	entry := items[0].(map[string]any)
	if entry["id"] != reverse.PublicID || entry["note_id"] != note.PublicID || entry["note_type_id"] != nt["id"] {
		t.Fatalf("empty card %v", entry)
	}
	before := snapshot(t, f.db)
	failTool(t, f.cs, "cleanup_empty_cards", args, "card_removal_unconfirmed", "cards_removed=1", "reviews_on_removed_cards=1")
	unchanged(t, f.db, before)
	args["dry_run"] = true
	checkCounts(t, mustTool(t, f.cs, "cleanup_empty_cards", args), map[string]int{"cards_removed": 1, "reviews_on_removed_cards": 1})
	unchanged(t, f.db, before)
	args["dry_run"] = false
	args["confirm_card_removal"] = true
	checkCounts(t, mustTool(t, f.cs, "cleanup_empty_cards", args), map[string]int{"cards_removed": 1, "reviews_on_removed_cards": 1})
	var removed store.Card
	if err := f.db.Unscoped().First(&removed, reverse.ID).Error; err != nil || !removed.DeletedAt.Valid {
		t.Fatalf("card: %+v %v", removed, err)
	}
	checkProgress(t, f.db, cards)
	f.audit(t, store.ActionCardEmptyCleanup)
}

func TestM20ScopesRolesAndForeignTypes(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	owner := seedUser(t, db, "roles_owner")
	reader := seedUser(t, db, "roles_reader")
	d := seedDeck(t, db, owner.ID)
	other := seedDeck(t, db, owner.ID)
	if err := db.Create(&store.DeckGrant{DeckID: d.ID, UserID: reader.ID, Role: store.RoleReader}).Error; err != nil {
		t.Fatal(err)
	}
	admin := connect(t, ts.URL, newKey(t, keys, owner.ID, []string{store.ScopeAdmin}))
	read := connect(t, ts.URL, newKey(t, keys, owner.ID, []string{store.ScopeRead}))
	write := connect(t, ts.URL, newKey(t, keys, owner.ID, []string{store.ScopeWrite}))
	review := connect(t, ts.URL, newKey(t, keys, owner.ID, []string{store.ScopeReview}))
	readerCS := connect(t, ts.URL, newKey(t, keys, reader.ID, []string{store.ScopeRead, store.ScopeWrite}))
	cfg := writeConfig("s-")
	local := mustTool(t, admin, "create_note_type", map[string]any{"deck_id": d.PublicID, "name": "Local", "config": cfg})
	foreign := mustTool(t, admin, "create_note_type", map[string]any{"deck_id": other.PublicID, "name": "Foreign", "config": cfg})
	inputs := map[string]map[string]any{
		"list_note_type_stocks": {}, "list_answer_controls": {},
		"list_empty_cards":    {"deck_id": d.PublicID},
		"create_note_type":    {"deck_id": d.PublicID, "stock_id": "engram:basic"},
		"update_note_type":    {"deck_id": d.PublicID, "note_type_id": local["id"], "name": "Local", "config": cfg, "version": local["version"]},
		"delete_note_type":    {"deck_id": d.PublicID, "note_type_id": local["id"]},
		"change_note_type":    {"deck_id": d.PublicID, "note_type_id": local["id"], "target_note_type": local["id"]},
		"cleanup_empty_cards": {"deck_id": d.PublicID},
	}
	for name, args := range inputs {
		t.Run("scope_"+name, func(t *testing.T) {
			cs := read
			if toolScopes[name] == store.ScopeRead {
				cs = write
			}
			failTool(t, cs, name, args, "scope_required")
		})
	}
	failTool(t, review, "list_empty_cards", inputs["list_empty_cards"], "scope_required")
	mustTool(t, read, "list_empty_cards", inputs["list_empty_cards"])
	for _, name := range []string{"list_empty_cards", "create_note_type", "update_note_type", "delete_note_type", "change_note_type", "cleanup_empty_cards"} {
		t.Run("reader_"+name, func(t *testing.T) { failTool(t, readerCS, name, inputs[name], "insufficient_role") })
	}
	for _, name := range []string{"update_note_type", "delete_note_type", "change_note_type"} {
		t.Run("foreign_"+name, func(t *testing.T) {
			args := map[string]any{}
			for k, v := range inputs[name] {
				args[k] = v
			}
			args["note_type_id"] = foreign["id"]
			failTool(t, admin, name, args, "not_found")
		})
	}
	args := map[string]any{}
	for k, v := range inputs["change_note_type"] {
		args[k] = v
	}
	args["target_note_type"] = foreign["id"]
	failTool(t, admin, "change_note_type", args, "not_found")
}
