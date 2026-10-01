package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
)

// ---- M5-7 导入：往返一致 / 幂等 / 安全 / 进度归属 ----

// TestImportRoundTripIdentical 断言导出→导入空库后 note 数/字段/标签完全一致，且二次导入不产生重复卡。
// SQLite 与 PostgreSQL 各跑一遍，源库与目标库都是全新实例。
func TestImportRoundTripIdentical(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_rt_owner")[0]
			deckID, _ := seedPackageDeck(t, src, owner)
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: false})

			importer := seedUsers(t, dst, "pkg_rt_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if report.NotesCreated != 4 {
				t.Fatalf("notes_created = %d, want 4", report.NotesCreated)
			}

			var srcNotes, dstNotes []Note
			src.Where("deck_id = ?", deckID).Order("kind ASC").Find(&srcNotes)
			dst.Where("deck_id = ?", report.DeckID).Order("kind ASC").Find(&dstNotes)
			if len(srcNotes) != len(dstNotes) {
				t.Fatalf("note count: dst=%d src=%d", len(dstNotes), len(srcNotes))
			}
			for i := range srcNotes {
				if srcNotes[i].Kind != dstNotes[i].Kind {
					t.Fatalf("note %d kind: %q != %q", i, dstNotes[i].Kind, srcNotes[i].Kind)
				}
				if CanonicalFieldsJSON(srcNotes[i].FieldsJSON) != CanonicalFieldsJSON(dstNotes[i].FieldsJSON) {
					t.Fatalf("note %d fields differ:\n src=%s\n dst=%s", i, srcNotes[i].FieldsJSON, dstNotes[i].FieldsJSON)
				}
				if srcNotes[i].TagsJSON != dstNotes[i].TagsJSON {
					t.Fatalf("note %d tags: %q != %q", i, dstNotes[i].TagsJSON, srcNotes[i].TagsJSON)
				}
			}
			var formula Note
			if err := dst.Where("deck_id = ? AND kind = ?", report.DeckID, "basic").First(&formula).Error; err != nil {
				t.Fatalf("load formula note: %v", err)
			}
			if !bytes.Contains([]byte(formula.FieldsJSON), []byte(`\\(E=mc^2\\)`)) {
				t.Fatalf("formula not preserved: %s", formula.FieldsJSON)
			}

			// 二次导入同一包：不产生重复卡（默认 update 命中指纹）。
			var cardsBefore int64
			dst.Model(&Card{}).Where("note_id IN (SELECT id FROM notes WHERE deck_id = ?)", report.DeckID).Count(&cardsBefore)
			report2, err := NewDeckStore(dst).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{Target: fmt.Sprintf("into_deck:%d", report.DeckID)})
			if err != nil {
				t.Fatalf("second ImportPackage: %v", err)
			}
			if report2.NotesCreated != 0 || report2.CardsCreated != 0 {
				t.Fatalf("second import created notes=%d cards=%d, want 0/0", report2.NotesCreated, report2.CardsCreated)
			}
			var cardsAfter int64
			dst.Model(&Card{}).Where("note_id IN (SELECT id FROM notes WHERE deck_id = ?)", report.DeckID).Count(&cardsAfter)
			if cardsBefore != cardsAfter {
				t.Fatalf("card count changed on re-import: %d -> %d", cardsBefore, cardsAfter)
			}
		})
	}
}

// TestImportRejectsUnsafeArchive 断言含路径穿越 / 软链的包被拒（M5-7 验收）。
func TestImportRejectsUnsafeArchive(t *testing.T) {
	cases := []struct {
		name string
		zip  func() []byte
	}{
		{"path traversal", func() []byte { return buildZip(t, map[string]string{"../evil.txt": "x", "manifest.json": "{}"}) }},
		{"absolute path", func() []byte { return buildZip(t, map[string]string{"/etc/passwd": "x"}) }},
		{"symlink", func() []byte { return buildSymlinkZip(t) }},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					raw := tc.zip()
					_, err := NewDeckStore(db).ImportPackage(context.Background(), 1, bytes.NewReader(raw), PackageImportOptions{})
					var pe *PackageError
					if !errors.As(err, &pe) || pe.Code != CodePackageUnsafeEntry {
						t.Fatalf("error = %v, want PackageError code %s", err, CodePackageUnsafeEntry)
					}
				})
			}
		})
	}
}

// TestReadPackageArchiveRejectsBomb 断言条目数超限的包被拒（防 zip bomb）。
// 纯函数、不碰数据库，无需按驱动重跑。
func TestReadPackageArchiveRejectsBomb(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 5; i++ {
		files[fmt.Sprintf("f%d", i)] = "x"
	}
	raw := buildZip(t, files)
	_, err := ReadPackageArchive(bytes.NewReader(raw), PackageLimits{MaxEntries: 3, MaxFileBytes: 1 << 20, MaxTotalBytes: 1 << 20})
	var pe *PackageError
	if !errors.As(err, &pe) || pe.Code != CodePackageTooLarge {
		t.Fatalf("error = %v, want PackageError code %s", err, CodePackageTooLarge)
	}
}

// TestImportUnknownKindListsEntries 断言未知 kind 报错并逐条列出出错的条目（M5-7 验收）。
func TestImportUnknownKindListsEntries(t *testing.T) {
	raw := buildZip(t, map[string]string{
		"manifest.json": `{"format_version":1,"exported_at":"2026-10-02T00:00:00Z","deck":{"name":"d"},"include_progress":false,"include_media":false,"include_reviews":false,"counts":{"notes":2,"cards":0}}`,
		"notes.json":    `[{"kind":"basic","fields":{"front":"a","back":"b"}},{"kind":"bogus_type","fields":{"x":"y"}}]`,
		"cards.json":    `[]`,
		"preset.json":   `{"desired_retention":0.9,"learning_steps":"1m","relearning_steps":"10m","maximum_interval_days":100,"enable_fuzz":true,"weights":null,"weights_optimized_at":null,"weights_review_count":null}`,
	})
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			_, err := NewDeckStore(db).ImportPackage(context.Background(), 1, bytes.NewReader(raw), PackageImportOptions{})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageUnknownKind {
				t.Fatalf("error = %v, want PackageError code %s", err, CodePackageUnknownKind)
			}
			if len(pe.Entries) != 1 || pe.Entries[0] != "notes[1] kind=bogus_type" {
				t.Fatalf("entries = %v, want [notes[1] kind=bogus_type]", pe.Entries)
			}
		})
	}
}

// TestImportDiscardsOthersProgress 断言他人 progress.json 默认被丢弃并告知（M5-7 验收）。
func TestImportDiscardsOthersProgress(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	sameOwner := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		dst2 := sameOwner[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_prog_owner")[0]
			deckID, _ := seedPackageDeck(t, src, owner)
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeProgress: true, IncludeMedia: false})

			other := seedUsers(t, dst, "pkg_prog_other")[0]
			report, err := NewDeckStore(dst).ImportPackage(context.Background(), other, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			if !report.ProgressDiscarded || report.ProgressApplied != 0 {
				t.Fatalf("report discarded=%v applied=%d, want discarded=true applied=0", report.ProgressDiscarded, report.ProgressApplied)
			}
			var states int64
			dst.Model(&CardState{}).Where("user_id = ?", other).Count(&states)
			if states != 0 {
				t.Fatalf("other user got %d progress rows, want 0", states)
			}

			// 同一用户导入自己的包：进度被应用。
			self := seedUsers(t, dst2, "pkg_prog_owner")[0]
			report2, err := NewDeckStore(dst2).ImportPackage(context.Background(), self, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck})
			if err != nil {
				t.Fatalf("ImportPackage (same user): %v", err)
			}
			if report2.ProgressApplied != 1 {
				t.Fatalf("progress_applied = %d, want 1", report2.ProgressApplied)
			}
		})
	}
}

// TestImportDryRunWritesNothing 断言 dry_run 只计数、不写库（SQLite + PG）。
func TestImportDryRunWritesNothing(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_dry_owner")[0]
			deckID, _ := seedPackageDeck(t, src, owner)
			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeMedia: false})

			importer := seedUsers(t, dst, "pkg_dry_importer")[0]
			report, err := NewDeckStore(dst).ImportPackage(context.Background(), importer, bytes.NewReader(raw), PackageImportOptions{DryRun: true})
			if err != nil {
				t.Fatalf("dry run ImportPackage: %v", err)
			}
			if report.NotesCreated != 4 {
				t.Fatalf("dry_run notes_created = %d, want 4", report.NotesCreated)
			}
			var decks, notes int64
			dst.Model(&Deck{}).Count(&decks)
			dst.Model(&Note{}).Count(&notes)
			if decks != 0 || notes != 0 {
				t.Fatalf("dry run wrote to the database: decks=%d notes=%d", decks, notes)
			}
		})
	}
}
