package web

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/flashcard/internal/store"
)

// auditTotalRE 从审计页里取出「命中： N」的 N；模板把标签与数字分两个文本节点渲染。
var auditTotalRE = regexp.MustCompile(`命中：\s*(\d+)`)

// auditTotal 请求一个审计页 URL 并解析出命中总数；解析失败时打印响应片段。
func auditTotal(t *testing.T, srv *Server, target string, cookies []*http.Cookie) int {
	t.Helper()
	rec := getWithCookies(t, srv, target, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200 (body %s)", target, rec.Code, snippet(rec.Body.String()))
	}
	m := auditTotalRE.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("GET %s: no match count in body: %s", target, snippet(rec.Body.String()))
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("GET %s: parse match count %q: %v", target, m[1], err)
	}
	return n
}

// TestAdminAuditFiltersReturnExactRows 是 M6-7 的 HTTP 层验收：每个过滤条件
// （用户 / 动作 / 目标 / 日期范围）各自返回精确预期的行数。fixture 的时间戳显式给定。
func TestAdminAuditFiltersReturnExactRows(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	aliceID := createRoleUser(t, db, "alice", store.RoleUser)

	// 清掉登录等已有审计行，让 fixture 的计数精确可控。
	if err := db.Where("1 = 1").Delete(&store.AuditLog{}).Error; err != nil {
		t.Fatalf("clear audit log: %v", err)
	}

	deck7, deckTarget := uint64(7), "deck"
	setting3, settingTarget := uint64(3), "setting"
	// owner 默认时区是 Asia/Shanghai（UTC+8）：UTC 3/10 12:00 = 本地 3/10 20:00，
	// UTC 3/10 23:30 = 本地 3/11 07:30，据此验证按用户时区切分自然日。
	base := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	rows := []store.AuditLog{
		{UserID: store.Ptr(ownerID), Action: "deck.grant", TargetType: &deckTarget, TargetID: &deck7, CreatedAt: base},
		{UserID: store.Ptr(aliceID), Action: "deck.grant", TargetType: &deckTarget, TargetID: &deck7, CreatedAt: base.Add(11*time.Hour + 30*time.Minute)},
		{UserID: store.Ptr(aliceID), Action: "deck.revoke", TargetType: &deckTarget, TargetID: &deck7, CreatedAt: base.Add(24 * time.Hour)},
		{UserID: store.Ptr(aliceID), Action: "setting.update", TargetType: &settingTarget, TargetID: &setting3, CreatedAt: base.Add(41 * time.Hour)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed audit rows: %v", err)
	}

	cases := []struct {
		name   string
		target string
		want   int
	}{
		{"no filter", "/admin/audit", 4},
		{"by action", "/admin/audit?action=deck.grant", 2},
		{"by user name", "/admin/audit?user=alice", 3},
		{"by user id", "/admin/audit?user=" + strconv.FormatUint(aliceID, 10), 3},
		{"by target type", "/admin/audit?target_type=deck", 3},
		{"by target type and id", "/admin/audit?target_type=deck&target_id=7", 3},
		{"by local date range", "/admin/audit?from=2026-03-11&to=2026-03-11", 2},
		{"by user and action", "/admin/audit?user=alice&action=deck.grant", 1},
		{"no match", "/admin/audit?action=does.not.exist", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := auditTotal(t, srv, tc.target, cookies); got != tc.want {
				t.Errorf("GET %s -> %d rows, want %d", tc.target, got, tc.want)
			}
		})
	}

	// 用户不存在时不能退化成「不过滤」：结果必须为 0，并给出提示。
	rec := getWithCookies(t, srv, "/admin/audit?user=nobody", cookies)
	if got := auditTotalRE.FindStringSubmatch(rec.Body.String()); got == nil || got[1] != "0" {
		t.Errorf("unknown user filter should match 0 rows, body: %s", snippet(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "没有匹配该用户名或编号的用户。") {
		t.Errorf("unknown user filter should show the not-found notice, body: %s", snippet(rec.Body.String()))
	}

	// 非法日期给出提示而不是 500。
	rec = getWithCookies(t, srv, "/admin/audit?from=not-a-date", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET invalid date = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "日期必须使用 YYYY-MM-DD 格式。") {
		t.Errorf("invalid date should show the format notice, body: %s", snippet(rec.Body.String()))
	}
}
