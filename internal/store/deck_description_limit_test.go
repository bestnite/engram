package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// 待办第 5 项：卡组描述与卡组名共用同一套界限（≤2000 字符、按 rune 计、拒 C0 控制字符、
// 拒非法 UTF-8；空描述允许）。修复前描述完全不校验，网页能建出「自己的导入器会拒」的卡组。
// 这里在真 SQLite（以及门控的 PG）上验证 store 层的创建与改名两条写入路径。

// assertDescriptionRejected 断言 store 层拒绝了该描述写入。
func assertDescriptionRejected(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("description write was accepted, want a rejection")
	}
	if errors.Is(err, ErrDeckNameInvalid) {
		t.Fatalf("description rejection must not reuse ErrDeckNameInvalid: %v", err)
	}
}

func TestDeckDescriptionLimitCreateAndRename(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "deck_desc_limit_owner")[0]
			presetID := seedPresetRow(t, db, owner)
			decks := NewDeckStore(db)

			desc2000 := strings.Repeat("d", 2000)
			desc2001 := strings.Repeat("d", 2001)

			t.Run("create 2000 accepted", func(t *testing.T) {
				d := &Deck{OwnerUserID: owner, Name: "desc-limit", PresetID: presetID, Description: desc2000}
				if err := decks.Create(ctx, d); err != nil {
					t.Fatalf("Create(description 2000) error = %v, want nil", err)
				}
				got, err := decks.ByID(ctx, d.ID)
				if err != nil {
					t.Fatalf("ByID() error = %v", err)
				}
				if n := len([]rune(got.Description)); n != 2000 {
					t.Fatalf("stored description = %d runes, want 2000", n)
				}
			})

			t.Run("create 2001 rejected", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-limit-2", PresetID: presetID, Description: desc2001})
				if err == nil {
					t.Fatalf("Create(description 2001) error = nil, want a rejection")
				}
				if !errors.Is(err, ErrDeckDescriptionInvalid) {
					t.Fatalf("Create(description 2001) error = %v, want ErrDeckDescriptionInvalid", err)
				}
			})

			t.Run("rename 2000 accepted then 2001 rejected", func(t *testing.T) {
				d := &Deck{OwnerUserID: owner, Name: "desc-rename", PresetID: presetID}
				if err := decks.Create(ctx, d); err != nil {
					t.Fatalf("Create() error = %v", err)
				}
				d.Description = desc2000
				if err := decks.Update(ctx, owner, d); err != nil {
					t.Fatalf("Update(description 2000) error = %v, want nil", err)
				}
				d.Description = desc2001
				if err := decks.Update(ctx, owner, d); !errors.Is(err, ErrDeckDescriptionInvalid) {
					t.Fatalf("Update(description 2001) error = %v, want ErrDeckDescriptionInvalid", err)
				}
				// 被拒的改名不得落库：库里仍是上一次合法的 2000 字符描述。
				got, err := decks.ByID(ctx, d.ID)
				if err != nil {
					t.Fatalf("ByID() error = %v", err)
				}
				if n := len([]rune(got.Description)); n != 2000 {
					t.Fatalf("rejected update leaked to DB: description = %d runes, want 2000", n)
				}
			})

			t.Run("2000 hanzi counted by character", func(t *testing.T) {
				// 2000 个汉字 = 6000 字节：按 rune 计应通过，按字节会误拒。
				hanzi := strings.Repeat("汉", 2000)
				if len(hanzi) <= 2000 {
					t.Fatalf("fixture is not multi-byte: len = %d", len(hanzi))
				}
				if err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-hanzi", PresetID: presetID, Description: hanzi}); err != nil {
					t.Fatalf("Create(2000 hanzi description) error = %v, want nil", err)
				}
			})

			t.Run("control character rejected", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-ctrl", PresetID: presetID, Description: "bad\ndesc"})
				if !errors.Is(err, ErrDeckDescriptionInvalid) {
					t.Fatalf("Create(newline in description) error = %v, want ErrDeckDescriptionInvalid", err)
				}
			})

			t.Run("invalid UTF-8 rejected", func(t *testing.T) {
				err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-utf8", PresetID: presetID, Description: "bad\xffdesc"})
				if !errors.Is(err, ErrDeckDescriptionInvalid) {
					t.Fatalf("Create(invalid UTF-8 description) error = %v, want ErrDeckDescriptionInvalid", err)
				}
			})

			t.Run("empty description allowed", func(t *testing.T) {
				if err := decks.Create(ctx, &Deck{OwnerUserID: owner, Name: "desc-empty", PresetID: presetID, Description: ""}); err != nil {
					t.Fatalf("Create(empty description) error = %v, want nil", err)
				}
			})
		})
	}
}

// TestDeckDescriptionLimitExportImportRoundTrip 断言共享界限的对称性：一个 2000 字符描述的
// 卡组能原样导出成卡组包，再被导入端（另一个新账号）接受 —— 修复前网页能建出 2000+ 描述的
// 卡组，导入器却会拒，往返不对称。
func TestDeckDescriptionLimitExportImportRoundTrip(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			users := seedUsers(t, db, "deck_desc_export", "deck_desc_import")
			exporter, importer := users[0], users[1]
			ctx := context.Background()
			deckID := seedPresetDeck(t, db, exporter)

			desc := strings.Repeat("x", 2000)
			d, err := NewDeckStore(db).ByID(ctx, deckID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			d.Description = desc
			if err := NewDeckStore(db).Update(ctx, exporter, d); err != nil {
				t.Fatalf("set 2000-char description: %v", err)
			}

			raw := exportZip(t, db, exporter, deckID, PackageOptions{})
			report := importPackageOK(t, db, importer, raw, PackageImportOptions{})
			got, err := NewDeckStore(db).ByID(ctx, report.DeckID)
			if err != nil {
				t.Fatalf("ByID(imported) error = %v", err)
			}
			if len([]rune(got.Description)) != len([]rune(desc)) {
				t.Fatalf("round-tripped description = %d runes, want %d", len([]rune(got.Description)), len([]rune(desc)))
			}
		})
	}
}
