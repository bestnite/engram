package mcp

import (
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/store"
)

// This file covers the two boundaries of opaque public ids: the input schema declares strings only, and numeric primary keys are not accepted;
// an unknown/invalid public id fails to resolve through api's ByPublicID and uniformly returns not_found.

// findTool takes the named tool out of tools/list; the test fails if it does not exist.
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

// propType returns the JSON Schema node of a property in a tool's input schema.
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

// schemaHasType reports whether a schema node declares the target type. Optional fields (such as slices) are inferred as
// nullable types (type is ["null","array"]), so this accepts both a bare string and a type array.
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

// TestToolSchemasDeclareStringIDs asserts every input argument pointing at a primary key is a string
// (or string array) in the JSON Schema, i.e. the contract the client sees is public ids, not numbers.
func TestToolSchemasDeclareStringIDs(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "schema-user")
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview}))

	stringFields := map[string]string{
		"create_deck":         "preset_id",
		"search_notes":        "deck_id",
		"export_deck":         "deck_id",
		"create_notes":        "deck_id",
		"update_note":         "note_id",
		"delete_note":         "note_id",
		"delete_deck":         "deck_id",
		"get_due_cards":       "deck_id",
		"submit_review":       "card_id",
		"list_deck_tags":      "deck_id",
		"add_extra_new_cards": "deck_id",
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

// TestToolsRejectNumericIDs asserts numeric primary keys are not accepted: when a number is passed for a string field, input validation rejects it
// before it reaches the handler, and the client gets isErr rather than a successful call.
func TestToolsRejectNumericIDs(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "numeric-user")
	deck := seedDeck(t, db, u.ID)
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead, store.ScopeWrite, store.ScopeReview}))

	numeric := map[string]map[string]any{
		"search_notes":        {"deck_id": deck.ID},
		"export_deck":         {"deck_id": deck.ID},
		"get_due_cards":       {"deck_id": deck.ID},
		"list_deck_tags":      {"deck_id": deck.ID},
		"add_extra_new_cards": {"deck_id": deck.ID},
		"submit_review":       {"card_id": deck.ID, "rating": 3},
		"update_note":         {"note_id": deck.ID, "fields": map[string]any{"front": "x", "back": "y"}},
		"delete_deck":         {"deck_id": deck.ID},
	}
	for name, args := range numeric {
		if _, isErr, text := callTool(t, cs, name, args); !isErr {
			t.Errorf("%s accepted a numeric id (%v), want a schema rejection", name, args)
		} else if text == "" {
			t.Errorf("%s rejected a numeric id with an empty error text", name)
		}
	}
}

// TestToolsUnknownPublicIDIsNotFound covers the negative case: an unknown (or empty string) public id fails to resolve through ByPublicID,
// and every tool returns not_found, the same code as REST, never falling through to a numeric primary key.
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
		{"list_deck_tags", map[string]any{"deck_id": missing}},
		{"add_extra_new_cards", map[string]any{"deck_id": missing}},
		{"update_note", map[string]any{"note_id": missing, "fields": map[string]any{"front": "x", "back": "y"}}},
		{"delete_note", map[string]any{"note_id": missing}},
		{"delete_deck", map[string]any{"deck_id": missing}},
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
