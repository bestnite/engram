package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// 表单错误必须本地化，且超限描述不能留下卡组行。
func TestDeckCreateRejectsOverLongDescription(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	rec := postForm(t, srv, "/decks", url.Values{
		"csrf_token": {csrf}, "name": {"description"}, "description": {strings.Repeat("汉", 2001)},
	}, cookies)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "卡组描述不合法") {
		t.Fatalf("invalid description response = %d %s", rec.Code, snippet(rec.Body.String()))
	}
	var count int64
	if err := db.Model(&store.Deck{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid description left %d decks", count)
	}
}
