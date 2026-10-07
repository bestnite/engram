package web

import (
	"net/url"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// userLocale 读出用户当前的语言设置（落库值）。
func userLocale(t *testing.T, db *gorm.DB, id uint64) string {
	t.Helper()
	u, err := store.NewUserStore(db).ByID(t.Context(), id)
	if err != nil {
		t.Fatalf("reload user %d: %v", id, err)
	}
	return u.Locale
}

// TestLangNextURL 覆盖回跳地址的构造：保留路径与其余查询参数，但去掉 lang，
// 否则旧的 ?lang 会继续覆盖刚写入的用户设置（这正是本缺陷的成因）。
func TestLangNextURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "/", want: "/"},
		{raw: "/settings", want: "/settings"},
		{raw: "/settings?lang=en", want: "/settings"},
		{raw: "/decks?page=2&lang=en", want: "/decks?page=2"},
		{raw: "/decks?lang=en&q=abc&page=2", want: "/decks?page=2&q=abc"},
	}
	for _, tc := range tests {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.raw, err)
		}
		if got := langNextURL(u); got != tc.want {
			t.Errorf("langNextURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
