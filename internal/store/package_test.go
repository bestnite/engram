package store

import (
	"context"
	"encoding/json"
	"testing"

	"gorm.io/gorm"
)

// ---- test fixtures ----

// packageDatabases returns fresh test databases on this machine with the tables created: a temporary SQLite database + a gated PostgreSQL one.
// The deck package cases run the same assertions on both drivers -- the PG side gets an independent schema per run via internal/pgtest,
// equivalent in isolation to the temporary SQLite database (the PG branch has to really run, not just pass on SQLite).
func packageDatabases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	out := testDatabases(t)
	for driver, db := range out {
		if err := db.AutoMigrate(AllModels()...); err != nil {
			t.Fatalf("AutoMigrate %s: %v", driver, err)
		}
	}
	return out
}

func seedPackageDeck(t *testing.T, db *gorm.DB, owner uint64) (deckID uint64, noteIDs []uint64) {
	t.Helper()
	ctx := context.Background()
	presetID := seedPresetRow(t, db, owner)
	d := Deck{OwnerUserID: owner, Name: "源卡组", PresetID: presetID, Description: "source"}
	if err := NewDeckStore(db).Create(ctx, &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	ns := NewNoteStore(db)
	type seed struct {
		kind      string
		fields    map[string]any
		ref       string
		tags      []string
		sourceURL string
		extra     any
		deckPath  string
	}
	seeds := []seed{
		{"basic", map[string]any{"front": "capital?", "back": `\(E=mc^2\)`}, "ext:1", []string{"geo"}, "https://example.com/notes/1", map[string]any{"licence": "example"}, ""},
		{"cloze", map[string]any{"text": "The {{c1::mitochondrion}} is the powerhouse."}, "", []string{"bio"}, "", nil, ""},
		{"choice_single", map[string]any{"question": "2+2?", "options": []any{"3", "4"}, "answer": float64(1)}, "", nil, "", nil, "Lesson 1::Verbs"},
		{"typed", map[string]any{"prompt": "spell", "answer": "ok"}, "", []string{"lang"}, "", nil, ""},
	}
	for _, s := range seeds {
		n := Note{DeckID: d.ID, NoteTypeID: Ptr(stockNoteTypeID(t, db, d.ID, s.kind)), TagsJSON: TagsJSON(s.tags), CreatedBy: Ptr(owner), Source: Ptr("manual")}
		if s.ref != "" {
			n.ExternalRef = Ptr(s.ref)
		}
		if s.sourceURL != "" {
			n.SourceURL = Ptr(s.sourceURL)
		}
		if s.extra != nil {
			raw, err := json.Marshal(s.extra)
			if err != nil {
				t.Fatalf("encode extra: %v", err)
			}
			n.ExtraJSON = Ptr(string(raw))
		}
		cards, err := ns.Create(ctx, &n, s.fields)
		if err != nil {
			t.Fatalf("create note %s: %v", s.kind, err)
		}
		noteIDs = append(noteIDs, n.ID)
		if s.deckPath != "" {
			if err := db.Model(&Card{}).Where("id = ?", cards[0].ID).Update("deck_path", s.deckPath).Error; err != nil {
				t.Fatalf("seed deck path: %v", err)
			}
		}
		if s.kind == "basic" {
			if err := db.Create(&CardState{CardID: cards[0].ID, UserID: owner, State: "review", StepIndex: 0, Reps: 3, Version: 2}).Error; err != nil {
				t.Fatalf("seed card state: %v", err)
			}
		}
	}
	return d.ID, noteIDs
}

func seedPresetDeck(t *testing.T, db *gorm.DB, owner uint64) uint64 {
	t.Helper()
	presetID := seedPresetRow(t, db, owner)
	d := Deck{OwnerUserID: owner, Name: "empty", PresetID: presetID}
	if err := NewDeckStore(db).Create(context.Background(), &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return d.ID
}
