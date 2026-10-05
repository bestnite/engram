package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/store"
)

// F27b：网页建卡组表单对超长名回显本地化提示（默认 zh-CN，来自语言包，不是英文兜底），
// 且不落库。
func TestDeckCreateRejectsOverLongName(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)

	rec := postForm(t, srv, "/decks", url.Values{
		"csrf_token": {csrf},
		"name":       {strings.Repeat("a", 201)},
	}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /decks (201-char name) status = %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "卡组名称过长或含非法字符。") {
		t.Fatalf("deck create error page is missing the localized message: %s", snippet(body))
	}
	if strings.Contains(body, "The deck name is invalid.") {
		t.Fatalf("deck create error page leaked the English fallback instead of the translation: %s", snippet(body))
	}
	var n int64
	if err := db.Model(&store.Deck{}).Count(&n).Error; err != nil {
		t.Fatalf("count decks: %v", err)
	}
	if n != 0 {
		t.Fatalf("deck count = %d, want 0 (over-long name must not be persisted)", n)
	}
}
