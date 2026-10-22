package web

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedReviewDeck creates a deck that uses a valid scheduling preset (for review tests only).
// Unlike seedDeck: it goes through store.NewPreset + PresetStore.Create to get a valid retention and maximum interval,
// otherwise FSRS would produce meaningless scheduling from zero-valued parameters.
func seedReviewDeck(t *testing.T, db *gorm.DB, ownerID uint64, name string) *store.Deck {
	t.Helper()
	preset := store.NewPreset(ownerID, "review preset")
	if err := store.NewPresetStore(db).Create(context.Background(), &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{OwnerUserID: ownerID, Name: name, PresetID: preset.ID}
	if err := store.NewDeckStore(db).Create(context.Background(), &deck); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return &deck
}

// attrValue takes the value="..." that immediately follows name="<name>" in an HTML fragment.
// For the tests that still assert on SSR-rendered fragments (deck settings page and the like).
func attrValue(body, name string) string {
	marker := `name="` + name + `" value="`
	idx := strings.Index(body, marker)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
