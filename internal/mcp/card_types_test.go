package mcp

import (
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/store"
)

// list_card_types 的验收：返回与注册表一致的题型自描述（与 REST GET /card-types 同源），
// 且只对带 read scope 的 key 开放。

func TestListCardTypesMatchesRegistry(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_card_types")
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))

	out, isErr, text := callTool(t, cs, "list_card_types", map[string]any{})
	if isErr {
		t.Fatalf("list_card_types error: %s", text)
	}
	kinds, _ := out["kinds"].([]any)
	want := cardtype.Descriptions()
	if len(kinds) != len(want) {
		t.Fatalf("list_card_types returned %d kinds, want %d", len(kinds), len(want))
	}
	for i, raw := range kinds {
		k, _ := raw.(map[string]any)
		fields, _ := k["fields"].([]any)
		if k["kind"] != want[i].Kind || len(fields) != len(want[i].Fields) {
			t.Fatalf("kind %d = %v, want kind %q with %d fields", i, k, want[i].Kind, len(want[i].Fields))
		}
	}
}

func TestListCardTypesRequiresReadScope(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_card_types_review")
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeReview}))

	_, isErr, text := callTool(t, cs, "list_card_types", map[string]any{})
	if !isErr || !strings.Contains(text, "scope_required") {
		t.Fatalf("review-only list_card_types = (isErr=%v, %q), want scope_required", isErr, text)
	}
}
