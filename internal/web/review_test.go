package web

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedReviewDeck 建一个使用合法调度预设的卡组（复习测试专用）。
// 与 seedDeck 的区别：走 store.NewPreset + PresetStore.Create，得到有效的保留率与最大间隔，
// 否则 FSRS 会在零值参数上产生无意义的调度。
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

// attrValue 从 HTML 片段里取 name="<name>" 紧随的 value="..."。
// 供仍按 SSR 渲染片段断言的测试（卡组设置页等）使用。
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
