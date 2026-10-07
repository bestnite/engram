package web

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedGradedNote 建一张作答类 note（判分相关测试共用）。
func seedGradedNote(t *testing.T, db *gorm.DB, deckID uint64, kind string, fields map[string]any) *store.Note {
	t.Helper()
	note := &store.Note{DeckID: deckID, Kind: kind}
	if _, err := store.NewNoteStore(db).Create(context.Background(), note, fields); err != nil {
		t.Fatalf("create %s note: %v", kind, err)
	}
	return note
}

// derefDetail 让测试日志可读：nil 详情打印 <nil>。
func derefDetail(raw *string) string {
	if raw == nil {
		return "<nil>"
	}
	return *raw
}
