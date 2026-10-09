package api

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// TestNoteImportSchemaAcceptsNoteIDAndRejectsUnknownKind 直接对 schema/note-import.schema.json
// 做机器校验：带 note_id 的请求体通过校验，未知 kind 与「同时给出 note_id 与
// external_ref」的请求体被拒。schema 是外部工具校验的机器可读契约，必须与实现同步。
func TestNoteImportSchemaAcceptsNoteIDAndRejectsUnknownKind(t *testing.T) {
	sch, err := jsonschema.NewCompiler().Compile("../../schema/note-import.schema.json")
	if err != nil {
		t.Fatalf("compile note-import schema: %v", err)
	}
	validate := func(body string) error {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(body))
		if err != nil {
			t.Fatalf("decode payload %s: %v", body, err)
		}
		return sch.Validate(doc)
	}

	// 新字段 note_id（对外 id，字符串）可通过校验。
	if err := validate(`{"notes":[{"note_id":"0198c0de-0000-7000-8000-000000000001","kind":"basic","fields":{"front":"q","back":"a"}}]}`); err != nil {
		t.Errorf("payload carrying note_id was rejected: %v", err)
	}
	// external_ref 单独出现仍然合法。
	if err := validate(`{"notes":[{"external_ref":"e:1","kind":"basic","fields":{"front":"q","back":"a"}}]}`); err != nil {
		t.Errorf("payload carrying only external_ref was rejected: %v", err)
	}
	// 未知 kind 无法匹配任何 oneOf 分支，必须被拒。
	if err := validate(`{"notes":[{"kind":"made_up","fields":{"x":"y"}}]}`); err == nil {
		t.Error("unknown kind was accepted, want a schema violation")
	}
	// note_id 与 external_ref 互斥，由 allOf + not + required 表达。
	if err := validate(`{"notes":[{"note_id":"0198c0de-0000-7000-8000-000000000001","external_ref":"e:1","kind":"basic","fields":{"front":"q","back":"a"}}]}`); err == nil {
		t.Error("payload carrying both note_id and external_ref was accepted, want a schema violation")
	}
	// note_id 是非空字符串：数字（旧的自增主键形态）不再被接受。
	if err := validate(`{"notes":[{"note_id":7,"kind":"basic","fields":{"front":"q","back":"a"}}]}`); err == nil {
		t.Error("payload carrying a numeric note_id was accepted, want a schema violation")
	}
}
