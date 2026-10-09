package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// F27b：创建/改名与卡组包 manifest 共用同一套卡组名界限（≤200 字符、按 rune 计、
// 拒 C0 控制字符、拒非法 UTF-8；空名仍报 ErrDeckNameRequired）。
// 这里在真 SQLite（以及门控的 PG）上验证 store 层的创建与改名两条写入路径。

func TestDeckNameLimitCreateAndRename(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "deck_name_limit_owner")[0]
			presetID := seedPresetRow(t, db, owner)
			decks := NewDeckStore(db)

			name200 := strings.Repeat("a", 200)
			name201 := strings.Repeat("a", 201)

			t.Run("create 200 accepted", func(t *testing.T) {
				d := &Deck{OwnerUserID: owner, Name: name200, PresetID: presetID}
				if err := decks.Create(ctx, d); err != nil {
					t.Fatalf("Create(200 chars) error = %v, want nil", err)
				}
				got, err := decks.ByID(ctx, d.ID)
				if err != nil {
					t.Fatalf("ByID() error = %v", err)
				}
				if n := len([]rune(got.Name)); n != 200 {
					t.Fatalf("stored name = %d runes, want 200", n)
				}
			})

			t.Run("create 201 rejected", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: name201, PresetID: presetID})
				if !errors.Is(err, ErrDeckNameInvalid) {
					t.Fatalf("Create(201 chars) error = %v, want ErrDeckNameInvalid", err)
				}
			})

			t.Run("rename 200 accepted then 201 rejected", func(t *testing.T) {
				d := &Deck{OwnerUserID: owner, Name: "rename seed", PresetID: presetID}
				if err := decks.Create(ctx, d); err != nil {
					t.Fatalf("Create() error = %v", err)
				}
				d.Name = name200
				if err := decks.Update(ctx, owner, d.ID, &d.Name, nil); err != nil {
					t.Fatalf("Update(200 chars) error = %v, want nil", err)
				}
				d.Name = name201
				if err := decks.Update(ctx, owner, d.ID, &d.Name, nil); !errors.Is(err, ErrDeckNameInvalid) {
					t.Fatalf("Update(201 chars) error = %v, want ErrDeckNameInvalid", err)
				}
				// 被拒的改名不得落库：库里仍是上一次合法的 200 字符名。
				got, err := decks.ByID(ctx, d.ID)
				if err != nil {
					t.Fatalf("ByID() error = %v", err)
				}
				if n := len([]rune(got.Name)); n != 200 {
					t.Fatalf("rejected rename leaked to DB: name = %d runes, want 200", n)
				}
			})

			t.Run("200 hanzi counted by character", func(t *testing.T) {
				// 200 个汉字 = 600 字节：按 rune 计应通过，按字节会误拒。
				hanzi := strings.Repeat("汉", 200)
				if len(hanzi) <= 200 {
					t.Fatalf("fixture is not multi-byte: len = %d", len(hanzi))
				}
				if err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: hanzi, PresetID: presetID}); err != nil {
					t.Fatalf("Create(200 hanzi) error = %v, want nil", err)
				}
			})

			t.Run("control character rejected", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "bad\nname", PresetID: presetID})
				if !errors.Is(err, ErrDeckNameInvalid) {
					t.Fatalf("Create(newline in name) error = %v, want ErrDeckNameInvalid", err)
				}
			})

			t.Run("invalid UTF-8 rejected", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "bad\xffname", PresetID: presetID})
				if !errors.Is(err, ErrDeckNameInvalid) {
					t.Fatalf("Create(invalid UTF-8) error = %v, want ErrDeckNameInvalid", err)
				}
			})

			t.Run("empty name stays ErrDeckNameRequired", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "", PresetID: presetID})
				if !errors.Is(err, ErrDeckNameRequired) {
					t.Fatalf("Create(empty) error = %v, want ErrDeckNameRequired", err)
				}
				if errors.Is(err, ErrDeckNameInvalid) {
					t.Fatalf("empty name must not become ErrDeckNameInvalid")
				}
			})
		})
	}
}

// TestDeckNameLimitExportImportRoundTrip 断言共享界限的对称性：一个 200 字符的卡组名
// 能原样导出成卡组包，再被导入端（另一个新账号）接受 —— F27b 修的就是“导出端能产出、
// 导入端不收”。名字按字符计，往返后逐字符相同。
func TestDeckNameLimitExportImportRoundTrip(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			users := seedUsers(t, db, "deck_name_export", "deck_name_import")
			exporter, importer := users[0], users[1]
			ctx := context.Background()
			deckID := seedPresetDeck(t, db, exporter)

			name := strings.Repeat("x", 200)
			d, err := NewDeckStore(db).ByID(ctx, deckID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			d.Name = name
			if err := NewDeckStore(db).Update(ctx, exporter, d.ID, &d.Name, nil); err != nil {
				t.Fatalf("rename to 200 chars: %v", err)
			}

			raw := exportZip(t, db, exporter, deckID, PackageOptions{})
			report := importPackageOK(t, db, importer, raw, PackageImportOptions{})
			if got := deckNameOf(t, db, report.DeckID); got != name {
				t.Fatalf("round-tripped name = %d runes, want the 200-rune name unchanged", len([]rune(got)))
			}
		})
	}
}
