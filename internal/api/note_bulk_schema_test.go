package api

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// bulkActionsFromSchema 读出 schema 里 action 枚举的取值集合。
//
// 它直接解析 schema 文件本身的 JSON（不依赖校验库的内部结构），是「schema 与实现不许分叉」
// 的那根钉子：新增一个批量动作时，只要 schema 的枚举没跟上，或者 Go 侧的常量改了名，
// 这条测试就红 —— 而不是等到外部工具拿去校验才发现对不上。
func bulkActionsFromSchema(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(doc.Properties.Action.Enum) == 0 {
		t.Fatalf("%s: properties.action.enum is missing or empty", path)
	}
	out := append([]string{}, doc.Properties.Action.Enum...)
	sort.Strings(out)
	return out
}

// TestNoteBulkSchemaMatchesTheGoActions 断言 schema 的 action 枚举与 Go 侧常量完全一致，
// 并校验仓库里那份示例请求体真的能通过它自己的 schema（M4-12：schema 是给外部工具用的
// 机器可读契约，示例与契约必须同步）。
func TestNoteBulkSchemaMatchesTheGoActions(t *testing.T) {
	const path = "../../schema/note-bulk.schema.json"

	want := []string{bulkActionAddTags, bulkActionDelete, bulkActionRemoveTags, bulkActionSetTags}
	sort.Strings(want)
	if got := bulkActionsFromSchema(t, path); !equalStrings(got, want) {
		t.Fatalf("schema action enum = %v, want %v (the schema and the service must not drift)", got, want)
	}

	sch, err := jsonschema.NewCompiler().Compile(path)
	if err != nil {
		t.Fatalf("compile %s: %v", path, err)
	}
	raw, err := os.ReadFile("../../schema/examples/note-bulk.example.json")
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("decode example: %v", err)
	}
	if err := sch.Validate(doc); err != nil {
		t.Errorf("the shipped example does not validate against the shipped schema: %v", err)
	}
}

// TestNoteBulkSchemaRejectsInvalidShapes 覆盖 schema 表达得出的四类请求级拒绝：
// 未知 action、note_ids 越界、tag 动作缺 tags、delete 带 tags。
// 服务端对同一批请求体也一律 400 invalid_request（见 bulk_notes_test.go）。
func TestNoteBulkSchemaRejectsInvalidShapes(t *testing.T) {
	sch, err := jsonschema.NewCompiler().Compile("../../schema/note-bulk.schema.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	validate := func(body string) error {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(body))
		if err != nil {
			t.Fatalf("decode payload %s: %v", body, err)
		}
		return sch.Validate(doc)
	}

	ids501 := make([]string, 0, 501)
	for i := 1; i <= 501; i++ {
		ids501 = append(ids501, itoa(i))
	}
	tags21 := make([]string, 0, 21)
	for i := 1; i <= 21; i++ {
		tags21 = append(tags21, `"t`+itoa(i)+`"`)
	}

	valid := []string{
		`{"action":"delete","note_ids":[1,2,3]}`,
		`{"action":"add_tags","note_ids":[1],"tags":["a"]}`,
		`{"action":"remove_tags","note_ids":[1,2],"tags":["a","b"]}`,
		`{"action":"set_tags","note_ids":[1],"tags":["a"],"dry_run":true}`,
	}
	for _, body := range valid {
		if err := validate(body); err != nil {
			t.Errorf("valid body %s was rejected: %v", body, err)
		}
	}

	invalid := []string{
		`{"action":"archive","note_ids":[1]}`,
		`{"action":"delete","note_ids":[]}`,
		`{"action":"delete","note_ids":[` + strings.Join(ids501, ",") + `]}`,
		`{"action":"add_tags","note_ids":[1]}`,
		`{"action":"add_tags","note_ids":[1],"tags":[]}`,
		`{"action":"add_tags","note_ids":[1],"tags":[` + strings.Join(tags21, ",") + `]}`,
		`{"action":"delete","note_ids":[1],"tags":["x"]}`,
		`{"note_ids":[1]}`,
	}
	for _, body := range invalid {
		if err := validate(body); err == nil {
			t.Errorf("invalid body was accepted, want a schema violation: %s", body)
		}
	}
}

// itoa 是测试里拼数字的最小助手，避免为一个字符串化引入 fmt。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// equalStrings 判断两个已排序的字符串切片是否相同。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
