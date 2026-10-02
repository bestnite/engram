package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/engram/internal/cardtype"
)

// seedDeck 建一个归属给定用户的卡组（连同它引用的预设），返回卡组主键。
func seedDeck(t *testing.T, db *gorm.DB, ownerID uint64) uint64 {
	t.Helper()
	presetID := seedPresetRow(t, db, ownerID)
	d := Deck{OwnerUserID: ownerID, Name: "Deck", Description: "",
		Visibility: DeckVisibilityPrivate, PresetID: presetID, CreatedAt: time.Now().UTC()}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return d.ID
}

// cardTemplates 把一组 card 摊平成 template→id，便于断言复用与顺序。
func cardTemplates(cards []Card) map[string]uint64 {
	out := make(map[string]uint64, len(cards))
	for _, c := range cards {
		out[c.Template] = c.ID
	}
	return out
}

// TestNoteCreateGeneratesCards 覆盖 M2-5 的第一半：创建 note 时经 cardtype 生成 cards。
func TestNoteCreateGeneratesCards(t *testing.T) {
	cases := []struct {
		name          string
		kind          string
		fields        map[string]any
		wantTemplates []string
		wantOrdinals  []int
	}{
		{"basic", "basic", map[string]any{"front": "q", "back": "a"}, []string{"forward"}, []int{0}},
		{"basic_both", "basic_both", map[string]any{"front": "q", "back": "a"}, []string{"forward", "reverse"}, []int{0, 1}},
		{"cloze", "cloze", map[string]any{"text": "a {{c1::x}} b {{c2::y}}"}, []string{"cloze:1", "cloze:2"}, []int{0, 1}},
		{"list", "list", map[string]any{"prompt": "p", "items": []any{"a", "b"}}, []string{"forward"}, []int{0}},
	}
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_create_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					n := &Note{DeckID: deckID, Kind: tc.kind}
					created, err := notes.Create(ctx, n, tc.fields)
					if err != nil {
						t.Fatalf("Create() error = %v", err)
					}
					if n.ID == 0 {
						t.Fatal("Create() did not populate note ID")
					}
					if len(created) != len(tc.wantTemplates) {
						t.Fatalf("Create() produced %d cards, want %d", len(created), len(tc.wantTemplates))
					}
					if n.FieldsJSON == "" || n.TagsJSON != "[]" {
						t.Errorf("Create() left FieldsJSON=%q TagsJSON=%q, want encoded fields and []", n.FieldsJSON, n.TagsJSON)
					}
					got, err := cards.ByNote(ctx, n.ID)
					if err != nil {
						t.Fatalf("ByNote() error = %v", err)
					}
					if len(got) != len(tc.wantTemplates) {
						t.Fatalf("ByNote() returned %d cards, want %d", len(got), len(tc.wantTemplates))
					}
					for i, c := range got {
						if c.Template != tc.wantTemplates[i] || c.Ordinal != tc.wantOrdinals[i] {
							t.Errorf("card[%d] = {%s, %d}, want {%s, %d}",
								i, c.Template, c.Ordinal, tc.wantTemplates[i], tc.wantOrdinals[i])
						}
						if c.ID == 0 || c.NoteID != n.ID {
							t.Errorf("card[%d] id/note_id = %d/%d, want nonzero/%d", i, c.ID, c.NoteID, n.ID)
						}
					}
				})
			}
		})
	}
}

// TestNoteUpdateKeepsCardIDs 是 M2-5 的验收用例：
// 更新 note 字段后，已存在的 cards 与其 id 保持不变。
func TestNoteUpdateKeepsCardIDs(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_update_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			// 双向卡：两张卡，最容易暴露\"删旧插新\"导致 id 变化。
			n := &Note{DeckID: deckID, Kind: "basic_both"}
			created, err := notes.Create(ctx, n, map[string]any{"front": "q1", "back": "a1"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			before := cardTemplates(created)
			if len(before) != 2 {
				t.Fatalf("setup produced %d cards, want 2", len(before))
			}

			// 改字段（内容变化，template 不变）。
			n2 := &Note{ID: n.ID, Kind: "basic_both"}
			updated, err := notes.Update(ctx, n2, map[string]any{"front": "q1-edited", "back": "a1-edited"})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			after := cardTemplates(updated)
			if len(after) != len(before) {
				t.Fatalf("Update() changed card count to %d, want %d", len(after), len(before))
			}
			for template, id := range before {
				if after[template] != id {
					t.Errorf("card %q id changed from %d to %d after Update(), want unchanged",
						template, id, after[template])
				}
			}

			// 从数据库重读，确认不是内存里的假象，且没有多余行。
			reloaded, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() after update error = %v", err)
			}
			if len(reloaded) != 2 {
				t.Fatalf("after Update() DB has %d cards, want 2", len(reloaded))
			}
			got := cardTemplates(reloaded)
			for template, id := range before {
				if got[template] != id {
					t.Errorf("reloaded card %q id = %d, want %d", template, got[template], id)
				}
			}

			// 幂等：同样的字段再更新一次，id 仍不变。
			if _, err := notes.Update(ctx, &Note{ID: n.ID, Kind: "basic_both"},
				map[string]any{"front": "q1-edited", "back": "a1-edited"}); err != nil {
				t.Fatalf("second Update() error = %v", err)
			}
			reloaded, err = cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() after second update error = %v", err)
			}
			for template, id := range before {
				if cardTemplates(reloaded)[template] != id {
					t.Errorf("card %q id changed after idempotent Update()", template)
				}
			}
		})
	}
}

// TestNoteUpdateGrowingClozeAddsCardsOnly 覆盖题型引起的 template 集合变化：
// 新序号只增卡，既有卡片与其 id 不受影响。
func TestNoteUpdateGrowingClozeAddsCardsOnly(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_cloze_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			n := &Note{DeckID: deckID, Kind: "cloze"}
			created, err := notes.Create(ctx, n, map[string]any{"text": "a {{c1::x}} b {{c2::y}}"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			before := cardTemplates(created)
			if len(before) != 2 {
				t.Fatalf("setup produced %d cards, want 2", len(before))
			}

			// 增加第 3 个序号：cloze:1 / cloze:2 必须保留原 id，只新增 cloze:3。
			if _, err := notes.Update(ctx, &Note{ID: n.ID, Kind: "cloze"},
				map[string]any{"text": "a {{c1::x}} b {{c2::y}} c {{c3::z}}"}); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			reloaded, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() error = %v", err)
			}
			if len(reloaded) != 3 {
				t.Fatalf("after Update() DB has %d cards, want 3", len(reloaded))
			}
			after := cardTemplates(reloaded)
			for _, template := range []string{"cloze:1", "cloze:2"} {
				if after[template] != before[template] {
					t.Errorf("card %q id = %d, want unchanged %d", template, after[template], before[template])
				}
			}
			if after["cloze:3"] == 0 {
				t.Error("new card cloze:3 was not created")
			}
		})
	}
}

// TestNoteSoftDeleteHidesCardsAndRestore 覆盖 M2-5 的另一半：
// 软删除 note 后其 cards 不可见，恢复后重新可见，且期间不物理删行。
func TestNoteSoftDeleteHidesCardsAndRestore(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_delete_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			n := &Note{DeckID: deckID, Kind: "basic_both"}
			created, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			ids := cardTemplates(created)

			if err := notes.Delete(ctx, n.ID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := notes.ByID(ctx, n.ID); !IsNotFound(err) {
				t.Errorf("ByID() after Delete() error = %v, want ErrRecordNotFound", err)
			}
			visible, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() after Delete() error = %v", err)
			}
			if len(visible) != 0 {
				t.Errorf("ByNote() after Delete() returned %d cards, want 0", len(visible))
			}
			for template, id := range ids {
				if _, err := cards.ByID(ctx, id); !IsNotFound(err) {
					t.Errorf("ByID(%d) for %q after Delete() error = %v, want ErrRecordNotFound", id, template, err)
				}
			}

			// 行必须还在（进度挂在 card 上，不能物理删）。
			var cardRows int64
			if err := db.Unscoped().Model(&Card{}).Where("note_id = ?", n.ID).Count(&cardRows).Error; err != nil {
				t.Fatalf("count card rows: %v", err)
			}
			if cardRows != 2 {
				t.Errorf("card rows after Delete() = %d, want 2 (soft delete must not remove rows)", cardRows)
			}

			// 重复删除已删除的 note → not found。
			if err := notes.Delete(ctx, n.ID); !IsNotFound(err) {
				t.Errorf("second Delete() error = %v, want ErrRecordNotFound", err)
			}

			if err := notes.Restore(ctx, n.ID); err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			if _, err := notes.ByID(ctx, n.ID); err != nil {
				t.Errorf("ByID() after Restore() error = %v", err)
			}
			restored, err := cards.ByNote(ctx, n.ID)
			if err != nil {
				t.Fatalf("ByNote() after Restore() error = %v", err)
			}
			if len(restored) != 2 {
				t.Fatalf("ByNote() after Restore() returned %d cards, want 2", len(restored))
			}
			for template, id := range ids {
				if cardTemplates(restored)[template] != id {
					t.Errorf("card %q id changed across delete/restore, want %d", template, id)
				}
			}

			// 恢复不存在的 note → not found。
			if err := notes.Restore(ctx, 999999); !IsNotFound(err) {
				t.Errorf("Restore(missing) error = %v, want ErrRecordNotFound", err)
			}
		})
	}
}

// TestNoteCardTemplateUniqueness 覆盖 M2-5 的\"强制 (note_id, template) 唯一\"：
// 数据库唯一索引是兜底，管线本身也不会产生重复 template。
func TestNoteCardTemplateUniqueness(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_unique_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)

			n := &Note{DeckID: deckID, Kind: "basic"}
			if _, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"}); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			// 数据库兜底：同 (note_id, template) 的裸插入必须失败。
			dup := Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: time.Now().UTC()}
			if err := db.Create(&dup).Error; err == nil {
				t.Error("duplicate (note_id, template) insert was accepted, want unique violation")
			}

			// 管线侧防线：同一 note 内重复 template 在写库前被拒。
			err := db.Transaction(func(tx *gorm.DB) error {
				wanted := []cardtype.Card{
					{Template: "forward", Ordinal: 0},
					{Template: "forward", Ordinal: 1},
				}
				_, err := syncCards(tx, n.ID, wanted, time.Now().UTC())
				return err
			})
			if err == nil {
				t.Error("syncCards() accepted duplicate templates, want an error")
			}

			// 结果里没有重复 template。
			var rows []Card
			if err := db.Unscoped().Where("note_id = ?", n.ID).Find(&rows).Error; err != nil {
				t.Fatalf("load cards: %v", err)
			}
			seen := map[string]int{}
			for _, c := range rows {
				seen[c.Template]++
			}
			for template, count := range seen {
				if count != 1 {
					t.Errorf("template %q appears %d times for one note, want 1", template, count)
				}
			}
		})
	}
}

// TestNoteValidationRejectsInvalid 覆盖创建路径的负例：缺卡组、未知题型、字段不合法。
func TestNoteValidationRejectsInvalid(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_validate_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)

			if _, err := notes.Create(ctx, &Note{Kind: "basic"}, map[string]any{"front": "q", "back": "a"}); !errors.Is(err, ErrNoteDeckRequired) {
				t.Errorf("Create(no deck) error = %v, want ErrNoteDeckRequired", err)
			}
			if _, err := notes.Create(ctx, &Note{DeckID: deckID}, map[string]any{"front": "q", "back": "a"}); !errors.Is(err, ErrNoteKindRequired) {
				t.Errorf("Create(no kind) error = %v, want ErrNoteKindRequired", err)
			}
			if _, err := notes.Create(ctx, &Note{DeckID: deckID, Kind: "nope"}, map[string]any{"front": "q"}); err == nil {
				t.Error("Create(unknown kind) error = nil, want an error")
			}
			if _, err := notes.Create(ctx, &Note{DeckID: deckID, Kind: "basic"}, map[string]any{"front": "q"}); err == nil {
				t.Error("Create(missing back) error = nil, want a validation error")
			}

			// 非法内容绝不能落库。
			var count int64
			if err := db.Model(&Note{}).Count(&count).Error; err != nil {
				t.Fatalf("count notes: %v", err)
			}
			if count != 0 {
				t.Errorf("notes persisted after failed creates = %d, want 0", count)
			}
		})
	}
}
