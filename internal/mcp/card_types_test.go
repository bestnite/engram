package mcp

import (
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// Acceptance for list_card_types: it returns one self-description per frozen card kind (the same catalog REST GET /card-types
// serves), and is open only to keys with the read scope.

// frozenCardKinds is the set of card kinds the payload layer describes. It is written out here so this test fails when the
// catalog gains or loses a kind without the MCP tool changing.
var frozenCardKinds = []string{
	"basic", "basic_both", "choice_multi", "choice_single", "cloze",
	"list", "numeric", "short_answer", "true_false", "typed",
}

func TestListCardTypesMatchesRegistry(t *testing.T) {
	_, db, keys, ts := newEnv(t)
	u := seedUser(t, db, "mcp_card_types")
	cs := connect(t, ts.URL, newKey(t, keys, u.ID, []string{store.ScopeRead}))

	out, isErr, text := callTool(t, cs, "list_card_types", map[string]any{})
	if isErr {
		t.Fatalf("list_card_types error: %s", text)
	}
	kinds, _ := out["kinds"].([]any)
	if len(kinds) != len(frozenCardKinds) {
		t.Fatalf("list_card_types returned %d kinds, want %d", len(kinds), len(frozenCardKinds))
	}
	seen := map[string]bool{}
	for _, raw := range kinds {
		k, _ := raw.(map[string]any)
		kind, _ := k["kind"].(string)
		seen[kind] = true
		fields, _ := k["fields"].([]any)
		example, _ := k["example"].(map[string]any)
		if kind == "" || len(fields) == 0 || len(example) == 0 {
			t.Fatalf("kind entry = %v, want a kind, its fields and an example", k)
		}
	}
	for _, kind := range frozenCardKinds {
		if !seen[kind] {
			t.Errorf("list_card_types is missing kind %q", kind)
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
