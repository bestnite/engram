package mcp

import (
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// 本文件覆盖不透明对外 id 的两条边界：入参 schema 只声明字符串、数字主键不被接受；
// 未知/非法对外 id 经 api 的 ByPublicID 解析失败，统一回 not_found。

// findTool 从 tools/list 里取出指定工具；不存在则测试失败。
func findTool(t *testing.T, cs *sdkmcp.ClientSession, name string) *sdkmcp.Tool {
	t.Helper()
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tl := range res.Tools {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found in tools/list", name)
	return nil
}

// propType 返回工具入参 schema 里某属性的 JSON Schema 节点。
func propType(t *testing.T, tl *sdkmcp.Tool, field string) map[string]any {
	t.Helper()
	schema, ok := tl.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("%s: input schema is %T, want map[string]any", tl.Name, tl.InputSchema)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: input schema has no properties: %v", tl.Name, schema)
	}
	node, ok := props[field].(map[string]any)
	if !ok {
		t.Fatalf("%s: field %q missing from input schema: %v", tl.Name, field, props)
	}
	return node
}

// schemaHasType 判断某个 schema 节点是否声明了目标类型。可选字段（如切片）会被推断成
// 可空类型（type 为 ["null","array"]），因此这里同时接受裸字符串与类型数组。
func schemaHasType(node map[string]any, want string) bool {
	switch tv := node["type"].(type) {
	case string:
		return tv == want
	case []any:
		for _, v := range tv {
			if v == want {
				return true
			}
		}
	}
	return false
}

// TestToolSchemasDeclareStringIDs 断言每个指向主键的入参在 JSON Schema 里都是字符串
// （或字符串数组），即客户端看到的契约就是对外 id，而不是数字。
func TestToolSchemasDeclareStringIDs(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "schema-user")
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview}))

	stringFields := map[string]string{
		"create_deck":   "preset_id",
		"search_notes":  "deck_id",
		"export_deck":   "deck_id",
		"create_notes":  "deck_id",
		"update_note":   "note_id",
		"delete_note":   "note_id",
		"get_due_cards": "deck_id",
		"submit_review": "card_id",
	}
	for tool, field := range stringFields {
		node := propType(t, findTool(t, cs, tool), field)
		if !schemaHasType(node, "string") {
			t.Errorf("%s.%s schema type = %v, want string", tool, field, node["type"])
		}
	}

	arrayFields := map[string]string{
		"bulk_notes":    "note_ids",
		"get_due_cards": "deck_ids",
	}
	for tool, field := range arrayFields {
		node := propType(t, findTool(t, cs, tool), field)
		if !schemaHasType(node, "array") {
			t.Errorf("%s.%s schema type = %v, want array", tool, field, node["type"])
		}
		items, _ := node["items"].(map[string]any)
		if items == nil || !schemaHasType(items, "string") {
			t.Errorf("%s.%s items = %v, want an array of strings", tool, field, node["items"])
		}
	}
}

// TestToolsRejectNumericIDs 断言数字主键不被接受：给字符串字段传数字时，输入校验在进入
// 处理器之前就拒绝，客户端拿到 isErr，而不是一次成功的调用。
func TestToolsRejectNumericIDs(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "numeric-user")
	deck := seedDeck(t, db, u.ID)
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview}))

	numeric := map[string]map[string]any{
		"search_notes":  {"deck_id": deck.ID},
		"export_deck":   {"deck_id": deck.ID},
		"get_due_cards": {"deck_id": deck.ID},
		"submit_review": {"card_id": deck.ID, "rating": 3},
		"update_note":   {"note_id": deck.ID, "fields": map[string]any{"front": "x", "back": "y"}},
	}
	for name, args := range numeric {
		if _, isErr, text := callTool(t, cs, name, args); !isErr {
			t.Errorf("%s accepted a numeric id (%v), want a schema rejection", name, args)
		} else if text == "" {
			t.Errorf("%s rejected a numeric id with an empty error text", name)
		}
	}
}

// TestToolsUnknownPublicIDIsNotFound 覆盖负例：未知（或空串）对外 id 经 ByPublicID 解析失败，
// 每个工具都回 not_found，与 REST 同一 code，绝不落到数字主键上。
func TestToolsUnknownPublicIDIsNotFound(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "unknown-id-user")
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview}))

	const missing = "01920000-0000-7000-8000-000000000000"
	cases := []struct {
		name string
		args map[string]any
	}{
		{"search_notes", map[string]any{"deck_id": missing}},
		{"export_deck", map[string]any{"deck_id": missing}},
		{"create_notes", map[string]any{"deck_id": missing, "notes": []any{}}},
		{"get_due_cards", map[string]any{"deck_id": missing}},
		{"get_due_cards", map[string]any{"deck_ids": []any{missing}}},
		{"update_note", map[string]any{"note_id": missing, "fields": map[string]any{"front": "x", "back": "y"}}},
		{"delete_note", map[string]any{"note_id": missing}},
		{"submit_review", map[string]any{"card_id": missing, "rating": 3}},
	}
	for _, tc := range cases {
		_, isErr, text := callTool(t, cs, tc.name, tc.args)
		if !isErr {
			t.Errorf("%s(%v) succeeded with an unknown public id, want not_found", tc.name, tc.args)
			continue
		}
		if !strings.Contains(text, api.CodeNotFound) {
			t.Errorf("%s(%v) error text = %q, want code %s", tc.name, tc.args, text, api.CodeNotFound)
		}
	}
}
