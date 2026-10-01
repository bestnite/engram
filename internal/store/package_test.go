package store

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ---- 测试夹具 ----

// openPackageDB 打开一个全新的 SQLite 库并建表，模拟"导入空库"。
func openPackageDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "pkg.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db
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
		n := Note{DeckID: d.ID, Kind: s.kind, TagsJSON: tagsJSON(s.tags), CreatedBy: Ptr(owner), Source: Ptr("manual")}
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

// ---- M5-6 导出 ----

// TestExportPackageValidatesAgainstSchema 断言导出包通过 schema/deck-package.schema.json。
func TestExportPackageValidatesAgainstSchema(t *testing.T) {
	db := openPackageDB(t)
	owner := seedUsers(t, db, "pkg_schema_owner")[0]
	deckID, _ := seedPackageDeck(t, db, owner)

	pkg, err := NewDeckStore(db).ExportPackage(context.Background(), owner, deckID, PackageOptions{IncludeProgress: true, IncludeMedia: true})
	if err != nil {
		t.Fatalf("ExportPackage: %v", err)
	}
	doc := pkg.Document()
	// JSON 往返，让数字统一成 float64，与真实 JSON Schema 校验器的输入一致。
	encoded, _ := json.Marshal(doc)
	var instance any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		t.Fatalf("re-decode document: %v", err)
	}
	schemaRaw, err := os.ReadFile(filepath.Join("..", "..", "schema", "deck-package.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	if err := validateAgainstSchema(instance, schema, schema, "$"); err != nil {
		t.Fatalf("exported package does not validate against deck-package.schema.json: %v", err)
	}
}

// TestExportProgressIsolatedPerUser 断言包里绝不包含导出者以外任何人的进度（M5-6 验收）。
func TestExportProgressIsolatedPerUser(t *testing.T) {
	db := openPackageDB(t)
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
}

// TestExportMediaOffDeclaresHonestly 断言 include_media=0 时无媒体条目且 manifest 如实声明（M5-6 验收）。
func TestExportMediaOffDeclaresHonestly(t *testing.T) {
	db := openPackageDB(t)
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

// validateAgainstSchema 是测试用的 JSON Schema（draft 2020-12）子集校验器：
// 覆盖 deck-package.schema.json 用到的关键字，直接读仓库里的 schema 文件校验导出文档。
func validateAgainstSchema(inst any, schema map[string]any, root map[string]any, path string) error {
	if ref, ok := schema["$ref"].(string); ok {
		resolved, err := resolveLocalRef(root, ref)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		return validateAgainstSchema(inst, resolved, root, path)
	}
	if c, ok := schema["const"]; ok {
		if !jsonEqual(inst, c) {
			return fmt.Errorf("%s: const mismatch: got %v want %v", path, inst, c)
		}
	}
	if enum, ok := schema["enum"].([]any); ok && !matchesAny(inst, enum) {
		return fmt.Errorf("%s: %v not in enum %v", path, inst, enum)
	}
	if allOf, ok := schema["allOf"].([]any); ok {
		for _, s := range allOf {
			if err := validateAgainstSchema(inst, s.(map[string]any), root, path); err != nil {
				return err
			}
		}
	}
	if oneOf, ok := schema["oneOf"].([]any); ok {
		matches := 0
		var last error
		for _, s := range oneOf {
			if err := validateAgainstSchema(inst, s.(map[string]any), root, path); err == nil {
				matches++
			} else {
				last = err
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s: oneOf matched %d branches, want 1 (%v)", path, matches, last)
		}
	}
	if t, ok := schema["type"]; ok && !typeMatches(inst, t) {
		return fmt.Errorf("%s: type mismatch: got %T", path, inst)
	}
	if n, ok := asFloat(inst); ok {
		if v, ok := asFloat(schema["minimum"]); ok && n < v {
			return fmt.Errorf("%s: %v < minimum %v", path, n, v)
		}
		if v, ok := asFloat(schema["exclusiveMinimum"]); ok && n <= v {
			return fmt.Errorf("%s: %v <= exclusiveMinimum %v", path, n, v)
		}
		if v, ok := asFloat(schema["exclusiveMaximum"]); ok && n >= v {
			return fmt.Errorf("%s: %v >= exclusiveMaximum %v", path, n, v)
		}
	}
	if s, ok := inst.(string); ok {
		if v, ok := asFloat(schema["minLength"]); ok && len([]rune(s)) < int(v) {
			return fmt.Errorf("%s: string shorter than minLength %v", path, v)
		}
		if v, ok := asFloat(schema["maxLength"]); ok && len([]rune(s)) > int(v) {
			return fmt.Errorf("%s: string longer than maxLength %v", path, v)
		}
		if p, ok := schema["pattern"].(string); ok {
			re, err := regexp.Compile(p)
			if err != nil {
				return fmt.Errorf("%s: bad pattern %q: %v", path, p, err)
			}
			if !re.MatchString(s) {
				return fmt.Errorf("%s: %q does not match pattern %q", path, s, p)
			}
		}
	}
	if arr, ok := inst.([]any); ok {
		if v, ok := asFloat(schema["minItems"]); ok && len(arr) < int(v) {
			return fmt.Errorf("%s: array shorter than minItems %v", path, v)
		}
		if v, ok := asFloat(schema["maxItems"]); ok && len(arr) > int(v) {
			return fmt.Errorf("%s: array longer than maxItems %v", path, v)
		}
		if u, _ := schema["uniqueItems"].(bool); u {
			for i := 0; i < len(arr); i++ {
				for j := i + 1; j < len(arr); j++ {
					if reflect.DeepEqual(arr[i], arr[j]) {
						return fmt.Errorf("%s[%d]: duplicate item with uniqueItems", path, j)
					}
				}
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, v := range arr {
				if err := validateAgainstSchema(v, items, root, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	if obj, ok := inst.(map[string]any); ok {
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, ok := obj[r.(string)]; !ok {
					return fmt.Errorf("%s: missing required property %q", path, r)
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for k, sub := range props {
				if v, ok := obj[k]; ok {
					if err := validateAgainstSchema(v, sub.(map[string]any), root, path+"."+k); err != nil {
						return err
					}
				}
			}
		}
		if pat, ok := schema["patternProperties"].(map[string]any); ok {
			for p, sub := range pat {
				re, err := regexp.Compile(p)
				if err != nil {
					return fmt.Errorf("%s: bad patternProperties %q: %v", path, p, err)
				}
				for k, v := range obj {
					if re.MatchString(k) {
						if err := validateAgainstSchema(v, sub.(map[string]any), root, path+"."+k); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func resolveLocalRef(root map[string]any, ref string) (map[string]any, error) {
	const prefix = "#/"
	if !hasPrefix(ref, prefix) {
		return nil, fmt.Errorf("unsupported $ref %q", ref)
	}
	segments := splitSlash(ref[len(prefix):])
	var cur any = root
	for _, seg := range segments {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref %q does not resolve", ref)
		}
		cur, ok = m[seg]
		if !ok {
			return nil, fmt.Errorf("$ref %q does not resolve", ref)
		}
	}
	out, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref %q is not an object", ref)
	}
	return out, nil
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func splitSlash(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func typeMatches(inst any, t any) bool {
	switch tv := t.(type) {
	case string:
		return typeMatchesOne(inst, tv)
	case []any:
		for _, one := range tv {
			if typeMatchesOne(inst, one.(string)) {
				return true
			}
		}
		return false
	}
	return true
}

func typeMatchesOne(inst any, t string) bool {
	switch t {
	case "null":
		return inst == nil
	case "object":
		_, ok := inst.(map[string]any)
		return ok
	case "array":
		_, ok := inst.([]any)
		return ok
	case "string":
		_, ok := inst.(string)
		return ok
	case "boolean":
		_, ok := inst.(bool)
		return ok
	case "integer":
		n, ok := inst.(float64)
		return ok && n == float64(int64(n))
	case "number":
		_, ok := inst.(float64)
		return ok
	}
	return true
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func matchesAny(inst any, values []any) bool {
	for _, v := range values {
		if jsonEqual(inst, v) {
			return true
		}
	}
	return false
}

func jsonEqual(a, b any) bool {
	an, aok := asFloat(a)
	bn, bok := asFloat(b)
	if aok && bok {
		return an == bn
	}
	return reflect.DeepEqual(a, b)
}

// 让测试文件显式引用 time，避免未来常量调整时误删导入。
var _ = time.Now
