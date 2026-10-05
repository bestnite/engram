package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// 本文件覆盖 L2 的媒体映射与写前校验（DESIGN.md §6.3）：
//   - media_notes 由 note 写入路径**重建**（删引用即失权，不是追加）；
//   - 写前校验拒绝「本次新引入的、写入者读不到的引用」，且对写入之前的状态求值；
//   - media_uploaders 在去重命中也登记全部提供者；
//   - 克隆后副本里的图对克隆者可读，不依赖源卡组仍可见；
//   - 包导入不会把「引用读不到媒体」的 note 落库。
//
// 所有 note 夹具都经 NoteStore 的写入方法构造（AGENTS/任务要求），映射因此一定与字段一致。

// mediaBytes 是一段带 PNG 魔数的测试字节，内容由 payload 区分。
func mediaBytes(payload string) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte(payload)...)
}

// saveMedia 落盘一份媒体并返回它的 sha256（登记上传者归属）。
func saveMedia(t *testing.T, db *gorm.DB, root string, owner uint64, raw []byte) string {
	t.Helper()
	m, err := NewMediaStore(db).SaveBytes(context.Background(), root, "image/png", raw, Ptr(owner))
	if err != nil {
		t.Fatalf("SaveBytes: %v", err)
	}
	return m.Sha256
}

// saveRefNote 经写入方法建一条引用给定 sha 的 basic note；作者为 author（可 nil）。
// actor 为 0（不标注），因此这里不触发写前校验——夹具要与「映射由写入建立」这件事对齐，
// 而不是要构造越权场景。
func saveRefNote(t *testing.T, db *gorm.DB, deckID uint64, author *uint64, front string) uint64 {
	t.Helper()
	n := Note{DeckID: deckID, Kind: "basic", CreatedBy: author}
	fields := map[string]any{"front": front, "back": "x"}
	if _, err := NewNoteStore(db).Save(context.Background(), &n, fields); err != nil {
		t.Fatalf("save referencing note: %v", err)
	}
	return n.ID
}

func refFront(shas ...string) string {
	parts := make([]string, 0, len(shas))
	for _, sha := range shas {
		parts = append(parts, "![](/media/"+sha+")")
	}
	return strings.Join(parts, " ")
}

// mappingShas 用映射算出「该用户可读卡组里被引用到的 sha 集合」，与读取鉴权第二支同源。
func mappingShas(t *testing.T, db *gorm.DB, userID uint64) map[string]bool {
	t.Helper()
	var rows []MediaNote
	if err := db.Model(&MediaNote{}).
		Joins("JOIN notes ON notes.id = media_notes.note_id AND notes.deleted_at IS NULL").
		Where("notes.deck_id IN (?)", visibleDeckIDsQuery(db, userID)).
		Find(&rows).Error; err != nil {
		t.Fatalf("load media_notes: %v", err)
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.MediaSha] = true
	}
	return out
}

// fieldsShas 扫描「该用户可读卡组里未软删 note 的字段」得到 sha 集合（旧口径，用作对照）。
func fieldsShas(t *testing.T, db *gorm.DB, userID uint64) map[string]bool {
	t.Helper()
	var notes []Note
	if err := db.Where("deck_id IN (?)", visibleDeckIDsQuery(db, userID)).Find(&notes).Error; err != nil {
		t.Fatalf("list visible notes: %v", err)
	}
	out := map[string]bool{}
	for i := range notes {
		fields, err := ParseFields(notes[i].FieldsJSON)
		if err != nil {
			continue
		}
		scanMediaRefs(fields, out)
	}
	return out
}

// TestMediaMappingInvariantMatchesFieldsScan 是核心不变量：随机夹具下，
// 「映射表算出的可读集」必须等于「扫 fields_json 算出的可读集」。
//
// 两条路径互相校验：映射一旦与字段漂移（漏写、或删引用后仍留旧行），这里必然不等。
// 夹具覆盖更新（去掉引用）、软删、以及另一个用户不可见的卡组，让两个方向都能被抓到。
func TestMediaMappingInvariantMatchesFieldsScan(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "invariant_owner", "invariant_other")
			owner, other := users[0], users[1]
			deckOwner := seedDeck(t, db, owner)
			deckOther := seedDeck(t, db, other)

			// 随机 sha 池；引用可以是库里没有的 sha（映射照样记录，因为字段里有）。
			rng := rand.New(rand.NewSource(20261006))
			pool := make([]string, 8)
			for i := range pool {
				pool[i] = fmt.Sprintf("%064x", rng.Int63())
			}
			pick := func() string { return pool[rng.Intn(len(pool))] }

			deckIDs := map[uint64]uint64{deckOwner: owner, deckOther: other}
			noteIDs := []uint64{}
			deckOf := map[uint64]uint64{}
			for i := 0; i < 60; i++ {
				deckID := deckOwner
				if rng.Intn(4) == 0 {
					deckID = deckOther
				}
				id := saveRefNote(t, db, deckID, Ptr(deckIDs[deckID]), refFront(pick(), pick(), pick()))
				noteIDs = append(noteIDs, id)
				deckOf[id] = deckID
			}
			// 随机更新：一半去掉全部引用，一半替换成新引用。
			//
			// 关键构造：「去掉引用」先让该 note 引用一个**本用例独有**的 sha（该 sha 不会出现在任何
			// 别的 note 里），再去掉它。追加式实现会把这条独有 sha 的旧映射行留在表里，于是映射集
			// 比字段集多出它——不变量据此必然报警（普通重叠 sha 池会被集合去重掩盖）。
			ns := NewNoteStore(db)
			unique := 0
			uniqueSha := func() string {
				unique++
				return fmt.Sprintf("%064x", uint64(0x9000000000000000)+uint64(unique))
			}
			for _, id := range noteIDs {
				switch rng.Intn(3) {
				case 0:
					nA := Note{ID: id, Kind: "basic"}
					if _, err := ns.Save(ctx, &nA, map[string]any{"front": refFront(uniqueSha()), "back": "x"}); err != nil {
						t.Fatalf("update note %d: %v", id, err)
					}
					nB := Note{ID: id, Kind: "basic"}
					if _, err := ns.Save(ctx, &nB, map[string]any{"front": "no refs", "back": "x"}); err != nil {
						t.Fatalf("update note %d: %v", id, err)
					}
				case 1:
					n := Note{ID: id, Kind: "basic"}
					if _, err := ns.Save(ctx, &n, map[string]any{"front": refFront(pick()), "back": "x"}); err != nil {
						t.Fatalf("update note %d: %v", id, err)
					}
				}
			}
			// 软删若干条：映射行可以留在表里，但两侧都必须把它排除。
			for _, id := range noteIDs[:5] {
				if err := ns.Delete(ctx, id); err != nil {
					t.Fatalf("delete note %d: %v", id, err)
				}
			}

			fromMapping := mappingShas(t, db, owner)
			fromFields := fieldsShas(t, db, owner)
			if len(fromFields) == 0 {
				t.Fatal("fixture produced no references; the invariant is vacuous")
			}
			if len(fromMapping) != len(fromFields) {
				t.Fatalf("mapping set size %d != fields set size %d (mapping=%v fields=%v)",
					len(fromMapping), len(fromFields), keys(fromMapping), keys(fromFields))
			}
			for sha := range fromFields {
				if !fromMapping[sha] {
					t.Errorf("sha %s referenced by a visible note but missing from media_notes", sha)
				}
			}
			for sha := range fromMapping {
				if !fromFields[sha] {
					t.Errorf("sha %s present in media_notes but not referenced by any visible note (stale row)", sha)
				}
			}
		})
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestMediaMappingRebuiltOnReferenceRemoval 证明映射是重建而非追加：去掉引用再保存后，
// 旧映射行必须消失，读者随即失权（旧的追加实现会留下旧行 → 读者仍可读）。
func TestMediaMappingRebuiltOnReferenceRemoval(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "rebuild_owner", "rebuild_reader")
			owner, reader := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			if err := NewGrantStore(db).Grant(ctx, deckID, reader, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant reader: %v", err)
			}
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("rebuild"))
			id := saveRefNote(t, db, deckID, Ptr(owner), refFront(sha))

			if ok, err := MediaAccessibleToUser(ctx, db, reader, sha); err != nil || !ok {
				t.Fatalf("reader before removal: readable=%v err=%v, want true", ok, err)
			}

			n := Note{ID: id, Kind: "basic"}
			if _, err := NewNoteStore(db).Save(ctx, &n, map[string]any{"front": "no refs", "back": "x"}); err != nil {
				t.Fatalf("update note: %v", err)
			}

			var rows int64
			if err := db.Model(&MediaNote{}).Where("note_id = ?", id).Count(&rows).Error; err != nil {
				t.Fatalf("count media_notes: %v", err)
			}
			if rows != 0 {
				t.Errorf("stale media_notes rows after reference removal = %d, want 0 (rebuild must delete old rows)", rows)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, reader, sha); err != nil || ok {
				t.Fatalf("reader after removal: readable=%v err=%v, want false", ok, err)
			}
		})
	}
}

// TestMediaMappingSoftDeletedNoteDoesNotAuthorize 覆盖软删：note 软删后映射行可以仍在，
// 但读取鉴权必须因 notes.deleted_at 而不再放行。
func TestMediaMappingSoftDeletedNoteDoesNotAuthorize(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "softdel_owner", "softdel_reader")
			owner, reader := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			if err := NewGrantStore(db).Grant(ctx, deckID, reader, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant reader: %v", err)
			}
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("softdel"))
			id := saveRefNote(t, db, deckID, Ptr(owner), refFront(sha))

			if ok, _ := MediaAccessibleToUser(ctx, db, reader, sha); !ok {
				t.Fatal("reader should read the media before the note is soft deleted")
			}
			if err := NewNoteStore(db).Delete(ctx, id); err != nil {
				t.Fatalf("soft delete note: %v", err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, reader, sha); err != nil || ok {
				t.Fatalf("reader after note soft delete: readable=%v err=%v, want false", ok, err)
			}
		})
	}
}

// TestMediaUploadersDedupeRegistersEveryUploader 覆盖 store 层媒体写入的去重登记：
// 同一份字节被两个用户先后提供，两人都拿到归属且各只有一行（幂等）。
func TestMediaUploadersDedupeRegistersEveryUploader(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "uploader_a", "uploader_b")
			a, b := users[0], users[1]
			root := t.TempDir()
			raw := mediaBytes("dedupe")

			shaA := saveMedia(t, db, root, a, raw)
			shaB := saveMedia(t, db, root, b, raw)
			if shaA != shaB {
				t.Fatalf("dedupe failed: %s != %s", shaA, shaB)
			}
			for _, uid := range []uint64{a, b} {
				var n int64
				if err := db.Model(&MediaUploader{}).
					Where("media_sha = ? AND user_id = ?", shaA, uid).Count(&n).Error; err != nil {
					t.Fatalf("count uploader: %v", err)
				}
				if n != 1 {
					t.Errorf("uploader %d rows = %d, want 1", uid, n)
				}
			}
			// 重复登记幂等：不新增行、不报错。
			if err := RecordMediaUploader(ctx, db, shaA, a); err != nil {
				t.Fatalf("idempotent RecordMediaUploader: %v", err)
			}
			var total int64
			if err := db.Model(&MediaUploader{}).Where("media_sha = ?", shaA).Count(&total).Error; err != nil {
				t.Fatalf("count uploaders: %v", err)
			}
			if total != 2 {
				t.Errorf("total uploaders = %d, want 2", total)
			}
		})
	}
}

// TestMediaWriteValidationRejectsUnreadableReference 覆盖写前校验本身：
// actor 是 B 时，新建/更新一条引用 B 读不到的媒体的 note 都必须被拒（*MediaWriteError），
// 且库里不留 note / 映射行；对写入之前的状态求值，使「映射一建立即自我满足」不成立。
func TestMediaWriteValidationRejectsUnreadableReference(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "writeval_a", "writeval_b")
			a, b := users[0], users[1]
			deckA := seedDeck(t, db, a)
			deckB := seedDeck(t, db, b)
			sha := saveMedia(t, db, t.TempDir(), a, mediaBytes("writeval"))

			// 新建：B 在自己的卡组里引用 A 的媒体 → 拒。
			n := Note{DeckID: deckB, Kind: "basic", CreatedBy: Ptr(b)}
			_, err := NewNoteStore(db).Save(WithActor(ctx, b), &n, map[string]any{
				"front": refFront(sha), "back": "x",
			})
			var mwe *MediaWriteError
			if !errors.As(err, &mwe) {
				t.Fatalf("create with unreadable ref: err=%v, want *MediaWriteError", err)
			}
			if n.ID != 0 {
				t.Errorf("rejected create assigned note id %d, want 0", n.ID)
			}
			var created int64
			if err := db.Model(&Note{}).Where("deck_id = ?", deckB).Count(&created).Error; err != nil {
				t.Fatalf("count notes: %v", err)
			}
			if created != 0 {
				t.Errorf("rejected create left %d notes", created)
			}
			var mapping int64
			if err := db.Model(&MediaNote{}).Where("media_sha = ?", sha).Count(&mapping).Error; err != nil {
				t.Fatalf("count media_notes: %v", err)
			}
			if mapping != 0 {
				t.Errorf("rejected create left %d media_notes rows", mapping)
			}

			// 更新：先给 B 建一条普通 note（actor 0），再让 B 注入 A 的媒体引用 → 拒，字段不变。
			id := saveRefNote(t, db, deckB, Ptr(b), "plain")
			n2 := Note{ID: id, Kind: "basic"}
			_, err = NewNoteStore(db).Save(WithActor(ctx, b), &n2, map[string]any{
				"front": refFront(sha), "back": "x",
			})
			if !errors.As(err, &mwe) {
				t.Fatalf("update with unreadable ref: err=%v, want *MediaWriteError", err)
			}
			reloaded, err := NewNoteStore(db).ByID(ctx, id)
			if err != nil {
				t.Fatalf("reload note: %v", err)
			}
			if strings.Contains(reloaded.FieldsJSON, sha) {
				t.Errorf("rejected update wrote the unreadable reference: %s", reloaded.FieldsJSON)
			}
			if n := func() int64 {
				var c int64
				_ = db.Model(&MediaNote{}).Where("note_id = ?", id).Count(&c).Error
				return c
			}(); n != 0 {
				t.Errorf("rejected update left %d media_notes rows", n)
			}
			_ = deckA
		})
	}
}

// TestCloneKeepsClonedMediaReadable 覆盖克隆端到端：B 克隆一份可见卡组后，副本里的图对 B 可读，
// 且不再依赖源卡组仍可见（把源卡组改成 private 后 B 仍能读副本里的图）。
func TestCloneKeepsClonedMediaReadable(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "clone_owner", "clone_reader")
			owner, cloner := users[0], users[1]

			presetID := seedPresetRow(t, db, owner)
			src := Deck{OwnerUserID: owner, Name: "public source", Visibility: DeckVisibilityPublic, PresetID: presetID}
			if err := NewDeckStore(db).Create(ctx, &src); err != nil {
				t.Fatalf("create source deck: %v", err)
			}
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("clone"))
			saveRefNote(t, db, src.ID, Ptr(owner), refFront(sha))

			clonerPreset := seedPresetRow(t, db, cloner)
			cloned, err := NewDeckStore(db).Clone(ctx, &src, cloner, "my copy", clonerPreset)
			if err != nil {
				t.Fatalf("Clone: %v", err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, cloner, sha); err != nil || !ok {
				t.Fatalf("cloner reads cloned media: readable=%v err=%v, want true", ok, err)
			}
			// 源卡组改为 private、并把源 note 软删：副本不再依赖源卡组仍可见。
			var reloaded Deck
			if err := db.First(&reloaded, src.ID).Error; err != nil {
				t.Fatalf("reload source deck: %v", err)
			}
			if err := db.Model(&Deck{}).Where("id = ?", src.ID).
				Update("visibility", DeckVisibilityPrivate).Error; err != nil {
				t.Fatalf("make source private: %v", err)
			}
			if _, err := NewNoteStore(db).DeleteMany(ctx, mustNoteIDs(t, db, src.ID), false); err != nil {
				t.Fatalf("soft delete source notes: %v", err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, cloner, sha); err != nil || !ok {
				t.Fatalf("cloner reads cloned media after source hidden: readable=%v err=%v, want true", ok, err)
			}
			// 副本里确实有一条引用该 sha 的 note（映射归属副本，而不是源）。
			fromMapping := mappingShas(t, db, cloner)
			if !fromMapping[sha] {
				t.Errorf("cloned media %s missing from the cloner's mapping set", sha)
			}
			_ = cloned
		})
	}
}

// mustNoteIDs 列出某卡组的 note id（含软删，用于软删前收集）。
func mustNoteIDs(t *testing.T, db *gorm.DB, deckID uint64) []uint64 {
	t.Helper()
	var ids []uint64
	if err := db.Model(&Note{}).Where("deck_id = ?", deckID).Pluck("id", &ids).Error; err != nil {
		t.Fatalf("list note ids: %v", err)
	}
	return ids
}

// TestImportPackageRejectsUnreadableMediaRef 覆盖包导入入口的越权注入：
// A 的包引用了 A 的媒体但未附带字节，B（无法读取该媒体）导入时整包被拒（稳定 code），
// 且事务回滚后 B 名下没有卡组 / note / 映射行。
func TestImportPackageRejectsUnreadableMediaRef(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "pkgforbid_owner", "pkgforbid_importer")
			owner, importer := users[0], users[1]
			srcDeck := seedPresetDeck(t, db, owner)
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("pkgforbid"))
			// 源 note 以包内形态引用该媒体；导出关闭媒体 → 包里没有字节。
			srcNote := Note{DeckID: srcDeck, Kind: "basic", CreatedBy: Ptr(owner)}
			if _, err := NewNoteStore(db).Save(ctx, &srcNote, map[string]any{
				"front": "q?", "back": "media/" + sha + ".png",
			}); err != nil {
				t.Fatalf("create source note: %v", err)
			}
			raw := exportZip(t, db, owner, srcDeck, PackageOptions{IncludeMedia: false})

			_, err := NewDeckStore(db).ImportPackage(ctx, importer, bytes.NewReader(raw), PackageImportOptions{
				SkipMissingMedia: true,
				MediaRoot:        t.TempDir(),
			})
			var pe *PackageError
			if !errors.As(err, &pe) || pe.Code != CodePackageMediaForbidden {
				t.Fatalf("import error = %v, want PackageError code %s", err, CodePackageMediaForbidden)
			}
			if !entryContains(pe.Entries, sha) {
				t.Errorf("rejection entries = %v, want one naming media/%s", pe.Entries, sha)
			}
			// 回滚彻底：导入者名下没有卡组、没有 note、没有映射。
			var deckCount, noteCount, mappingCount int64
			if err := db.Model(&Deck{}).Where("owner_user_id = ?", importer).Count(&deckCount).Error; err != nil {
				t.Fatalf("count importer decks: %v", err)
			}
			if err := db.Model(&Note{}).Where("created_by = ?", importer).Count(&noteCount).Error; err != nil {
				t.Fatalf("count importer notes: %v", err)
			}
			if err := db.Model(&MediaNote{}).
				Joins("JOIN notes ON notes.id = media_notes.note_id").
				Where("media_notes.media_sha = ? AND notes.created_by = ?", sha, importer).
				Count(&mappingCount).Error; err != nil {
				t.Fatalf("count media_notes: %v", err)
			}
			if deckCount != 0 || noteCount != 0 || mappingCount != 0 {
				t.Errorf("rejected import left decks=%d notes=%d mapping=%d, want 0/0/0",
					deckCount, noteCount, mappingCount)
			}
		})
	}
}
