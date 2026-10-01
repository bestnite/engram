package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestAuditSearchFilters 是 M6-7 的验收：seeded fixture 下，每个过滤条件各自
// 返回精确预期的行数。fixture 的时间戳全部显式给定，不受运行时刻影响。
func TestAuditSearchFilters(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "audit-search.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	base := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	fixture := []AuditLog{
		{UserID: Ptr(uint64(1)), Action: "deck.grant", TargetType: Ptr("deck"), TargetID: Ptr(uint64(7)), CreatedAt: base},
		{UserID: Ptr(uint64(1)), Action: "deck.revoke", TargetType: Ptr("deck"), TargetID: Ptr(uint64(7)), CreatedAt: base.Add(24 * time.Hour)},
		{UserID: Ptr(uint64(2)), Action: "deck.grant", TargetType: Ptr("deck"), TargetID: Ptr(uint64(9)), CreatedAt: base.Add(48 * time.Hour)},
		{UserID: Ptr(uint64(2)), Action: "setting.update", TargetType: Ptr("setting"), TargetID: Ptr(uint64(3)), CreatedAt: base.Add(72 * time.Hour)},
		// 时区边界：本地（Asia/Shanghai，UTC+8）的 3 月 11 日，UTC 落在 3 月 10 日。
		{UserID: Ptr(uint64(2)), Action: "user.login_succeeded", TargetType: nil, CreatedAt: base.Add(11*time.Hour + 30*time.Minute)},
	}
	if err := db.Create(&fixture).Error; err != nil {
		t.Fatalf("seed audit rows: %v", err)
	}
	s := NewAuditStore(db)

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation(Asia/Shanghai): %v", err)
	}
	// 本地 2026-03-10 一天 → UTC [2026-03-09 16:00, 2026-03-10 16:00)。
	// 只有 r1（UTC 3/10 12:00 = 本地 3/10 20:00）落在本地 3/10；r5（UTC 3/10 23:30）
	// 在本地已是 3/11，据此证明按用户时区的自然日切分，而不是按 UTC 日。
	day10 := time.Date(2026, 3, 10, 0, 0, 0, 0, shanghai)

	cases := []struct {
		name   string
		filter AuditFilter
		want   int64
	}{
		{"by user", AuditFilter{UserID: 1}, 2},
		{"by action", AuditFilter{Action: "deck.grant"}, 2},
		{"by target type", AuditFilter{TargetType: "deck"}, 3},
		{"by target type and id", AuditFilter{TargetType: "deck", TargetID: 7}, 2},
		{"by date range only", AuditFilter{From: base, To: base.Add(48 * time.Hour)}, 3},
		{"by date range inclusive upper day (UTC)", AuditFilter{From: base, To: base.Add(72*time.Hour + time.Second)}, 5},
		{"by local-day boundary", AuditFilter{From: day10, To: day10.AddDate(0, 0, 1)}, 1},
		{"by user and action", AuditFilter{UserID: 2, Action: "deck.grant"}, 1},
		{"no filter returns all", AuditFilter{}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, total, err := s.Search(context.Background(), tc.filter)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			t.Logf("filter %+v -> total=%d rows=%d (want %d)", tc.filter, total, len(rows), tc.want)
			if total != tc.want {
				t.Errorf("total = %d, want %d", total, tc.want)
			}
			if int64(len(rows)) != tc.want {
				t.Errorf("len(rows) = %d, want %d", len(rows), tc.want)
			}
		})
	}
}

// TestAuditSearchPagingAndCap 断言分页与硬上限：Limit 超过上限时被截到上限，
// Offset 生效，且返回行数不超过请求页大小。
func TestAuditSearchPagingAndCap(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "audit-page.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	base := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if err := db.Create(&AuditLog{Action: "note.create", CreatedAt: base.Add(time.Duration(i) * time.Minute)}).Error; err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
	s := NewAuditStore(db)
	rows, total, err := s.Search(context.Background(), AuditFilter{Limit: 1000, Offset: 2})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(rows) != 3 {
		t.Errorf("len(rows) = %d, want 3 (offset 2 of 5)", len(rows))
	}
	// 倒序：offset 2 之后应从第 3 条（id 3）开始。
	if len(rows) == 3 && rows[0].ID != 3 {
		t.Errorf("first row after offset = id %d, want 3", rows[0].ID)
	}
	if auditSearchMaxLimit != 200 || auditSearchDefaultLimit != 50 {
		t.Errorf("limits drifted: default %d max %d", auditSearchDefaultLimit, auditSearchMaxLimit)
	}
}
