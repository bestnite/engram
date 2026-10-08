package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gorm.io/gorm"
)

// ---- 测试夹具 ----

// packageDatabases 返回本机可用的、已建表的全新测试库：SQLite 临时库 + 门控的 PostgreSQL。
// 卡组包用例在两种驱动上跑同一套断言——PG 侧经 internal/pgtest 每次拿独立 schema，
// 与 SQLite 的临时库隔离程度等价（要求 PG 分支真正跑起来，而不是只在 SQLite 上过）。
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

// seedPackageDeck 建一个含多题型 note 的源卡组，并写入一条属于 owner 的进度。
func seedPackageDeck(t *testing.T, db *gorm.DB, owner uint64) (deckID uint64, noteIDs []uint64) {
	t.Helper()
	ctx := context.Background()
	presetID := seedPresetRow(t, db, owner)
	d := Deck{OwnerUserID: owner, Name: "源卡组", PresetID: presetID, Visibility: DeckVisibilityPrivate, Description: "source"}
	if err := NewDeckStore(db).Create(ctx, &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	ns := NewNoteStore(db)
	type seed struct {
		kind   string
		fields map[string]any
		ref    string
		tags   []string
	}
	seeds := []seed{
		{"basic", map[string]any{"front": "capital?", "back": `\(E=mc^2\)`}, "ext:1", []string{"geo"}},
		{"cloze", map[string]any{"text": "The {{c1::mitochondrion}} is the powerhouse."}, "", []string{"bio"}},
		{"choice_single", map[string]any{"question": "2+2?", "options": []any{"3", "4"}, "answer": float64(1)}, "", nil},
		{"typed", map[string]any{"prompt": "spell", "answer": "ok"}, "", []string{"lang"}},
	}
	for _, s := range seeds {
		n := Note{DeckID: d.ID, Kind: s.kind, TagsJSON: TagsJSON(s.tags), CreatedBy: Ptr(owner), Source: Ptr("manual")}
		if s.ref != "" {
			n.ExternalRef = Ptr(s.ref)
		}
		cards, err := ns.Create(ctx, &n, s.fields)
		if err != nil {
			t.Fatalf("create note %s: %v", s.kind, err)
		}
		noteIDs = append(noteIDs, n.ID)
		if s.kind == "basic" {
			if err := db.Create(&CardState{CardID: cards[0].ID, UserID: owner, State: "review", StepIndex: 0, Reps: 3, Version: 2}).Error; err != nil {
				t.Fatalf("seed card state: %v", err)
			}
		}
	}
	return d.ID, noteIDs
}

// seedPresetDeck 建一个空卡组（无 note），供媒体用例使用。
func seedPresetDeck(t *testing.T, db *gorm.DB, owner uint64) uint64 {
	t.Helper()
	presetID := seedPresetRow(t, db, owner)
	d := Deck{OwnerUserID: owner, Name: "empty", PresetID: presetID, Visibility: DeckVisibilityPrivate}
	if err := NewDeckStore(db).Create(context.Background(), &d); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return d.ID
}

// exportZip 导出并序列化成 zip 字节。
func exportZip(t *testing.T, db *gorm.DB, owner, deckID uint64, opts PackageOptions) []byte {
	t.Helper()
	pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, deckID, opts)
	if err != nil {
		t.Fatalf("ExportPackage: %v", err)
	}
	var buf bytes.Buffer
	if err := pkg.WriteZip(&buf); err != nil {
		t.Fatalf("WriteZip: %v", err)
	}
	return buf.Bytes()
}

// ---- 导出 ----

// TestExportPackageValidatesAgainstSchema 断言导出包通过 schema/deck-package.schema.json（SQLite + PG）。
func TestExportPackageValidatesAgainstSchema(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "pkg_schema_owner")[0]
			deckID, _ := seedPackageDeck(t, db, owner)

			pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, deckID, PackageOptions{IncludeProgress: true, IncludeMedia: true})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			doc := pkg.Document()
			// JSON 往返统一数字类型，再交给真库（draft 2020-12）对照仓库 schema 校验。
			encoded, _ := json.Marshal(doc)
			var instance any
			if err := json.Unmarshal(encoded, &instance); err != nil {
				t.Fatalf("re-decode document: %v", err)
			}
			if err := validateAgainstSchema(t, instance, filepath.Join("..", "..", "schema", "deck-package.schema.json")); err != nil {
				t.Fatalf("exported package does not validate against deck-package.schema.json: %v", err)
			}
		})
	}
}

// TestExportProgressIsolatedPerUser 断言包里绝不包含导出者以外任何人的进度
func TestExportProgressIsolatedPerUser(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			users := seedUsers(t, db, "pkg_iso_owner", "pkg_iso_reader")
			owner, reader := users[0], users[1]
			deckID, noteIDs := seedPackageDeck(t, db, owner)

			// reader 在同一卡组的首张卡上也有自己的进度。
			cards, err := NewCardStore(db).ByNote(context.Background(), noteIDs[0])
			if err != nil || len(cards) == 0 {
				t.Fatalf("load cards: %v", err)
			}
			if err := db.Create(&CardState{CardID: cards[0].ID, UserID: reader, State: "learning", Reps: 99, Version: 7}).Error; err != nil {
				t.Fatalf("seed reader state: %v", err)
			}

			pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, deckID, PackageOptions{IncludeProgress: true, IncludeMedia: false})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			if pkg.Progress == nil || len(pkg.Progress.CardStates) != 1 {
				t.Fatalf("progress card_states = %v, want exactly the owner's one row", pkg.Progress)
			}
			for _, st := range pkg.Progress.CardStates {
				if st.Reps == 99 || st.State == "learning" {
					t.Fatalf("package leaked another user's progress: %+v", st)
				}
			}
			if pkg.Manifest.ExportedBy != "pkg_iso_owner" {
				t.Fatalf("manifest.exported_by = %q, want owner username", pkg.Manifest.ExportedBy)
			}
		})
	}
}

// TestExportMediaOffDeclaresHonestly 断言 include_media=0 时无媒体条目且 manifest 如实声明
func TestExportMediaOffDeclaresHonestly(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "pkg_media_owner")[0]
			deckID := seedPresetDeck(t, db, owner)
			// 一条引用媒体的 note，确保"关闭媒体"路径确实被走到。
			n := Note{DeckID: deckID, Kind: "basic", TagsJSON: "[]", CreatedBy: Ptr(owner)}
			if _, err := NewNoteStore(db).Create(context.Background(), &n, map[string]any{
				"front": "img?", "back": "media/0000000000000000000000000000000000000000000000000000000000000000.png",
			}); err != nil {
				t.Fatalf("create note: %v", err)
			}
			pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, deckID, PackageOptions{IncludeMedia: false})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			if pkg.Manifest.IncludeMedia {
				t.Fatal("manifest.include_media = true, want false")
			}
			if len(pkg.Media) != 0 || len(pkg.MediaBytes) != 0 {
				t.Fatalf("media not empty with include_media=0: %v", pkg.Media)
			}
			if pkg.Manifest.Counts.Media != 0 {
				t.Fatalf("manifest.counts.media = %d, want 0", pkg.Manifest.Counts.Media)
			}
			if _, ok := pkg.Document()["media.json"]; ok {
				t.Fatal("media.json present with include_media=0")
			}
		})
	}
}

// ---- 打包与 schema 校验辅助 ----

// buildZip 把一组条目打成 zip；条目名保持原样，供安全拒绝用例构造恶意归档。
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// buildSymlinkZip 构造一个含软链条目的 zip，用于安全拒绝用例。
func buildSymlinkZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "link"}
	h.SetMode(os.ModeSymlink | 0o777)
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatalf("zip symlink header: %v", err)
	}
	if _, err := w.Write([]byte("../target")); err != nil {
		t.Fatalf("zip symlink write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// validateAgainstSchema 用真库（santhosh-tekuri/jsonschema v6，draft 2020-12）校验一份文档，
// 对照仓库里的 schema 文件；用它替换手写的子集校验器，关键字覆盖与规范一致。
func validateAgainstSchema(t *testing.T, instance any, schemaPath string) error {
	t.Helper()
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	// 用 schema 的 $id 作为资源 URL，$ref 才能解析到文件内部的定义。
	if err := c.AddResource("https://example.com/schema/deck-package.schema.json", doc); err != nil {
		return fmt.Errorf("add schema resource: %w", err)
	}
	sch, err := c.Compile("https://example.com/schema/deck-package.schema.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	return sch.Validate(instance)
}
