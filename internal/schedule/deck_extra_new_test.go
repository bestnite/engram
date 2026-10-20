package schedule

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
)

// extraNewDrivers 返回要跑的数据库：SQLite 总是跑；设置了 TEST_PG_DSN 时再跑一遍 PostgreSQL。
// 追加额度靠 clause.OnConflict 的累加 upsert 写入，两种驱动的语法与语义都要覆盖。
func extraNewDrivers() map[string]func(t *testing.T) *gorm.DB {
	return map[string]func(t *testing.T) *gorm.DB{
		"sqlite": newTestDB,
		"postgres": func(t *testing.T) *gorm.DB {
			db, ok := pgtest.Open(t)
			if !ok {
				t.Skipf("%s not set: skipping the PostgreSQL path", pgtest.EnvDSN)
			}
			if err := db.AutoMigrate(store.AllModels()...); err != nil {
				t.Fatalf("AutoMigrate: %v", err)
			}
			return db
		},
	}
}

// TestExtraNewRaisesTodaysNewBudget 覆盖「再来一组」的额度规则：今天追加的额度加在每日上限之上，
// Build、DeckCounts 与 DeckBudgets 三处同源；别的复习日的追加不生效；不限的卡组不受追加影响。
func TestExtraNewRaisesTodaysNewBudget(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	day := ReviewDay(now, time.UTC, DefaultDayCutoffHour)
	yesterday := ReviewDay(now.Add(-24*time.Hour), time.UTC, DefaultDayCutoffHour)
	at := now.Add(-time.Hour)
	future := now.Add(48 * time.Hour)
	last := now.Add(-48 * time.Hour)

	cases := []struct {
		name string
		// newPerDay 是卡组上限；introduced 是今天已引入的新卡数；fresh 是还没学过的新卡数。
		newPerDay, introduced, fresh int
		// extras 是要写入的追加额度，键是复习日。
		extras        map[string]int
		wantNew       int
		wantExtra     int
		wantUnlimited bool
	}{
		{name: "goal reached without extra", newPerDay: 2, introduced: 2, fresh: 5, wantNew: 0},
		{name: "extra opens another batch", newPerDay: 2, introduced: 2, fresh: 5, extras: map[string]int{day: 2}, wantNew: 2, wantExtra: 2},
		{name: "smaller extra opens a smaller batch", newPerDay: 2, introduced: 2, fresh: 5, extras: map[string]int{day: 1}, wantNew: 1, wantExtra: 1},
		{name: "extra capped by cards left in deck", newPerDay: 2, introduced: 2, fresh: 1, extras: map[string]int{day: 10}, wantNew: 1, wantExtra: 10},
		{name: "yesterday's extra does not apply", newPerDay: 2, introduced: 2, fresh: 5, extras: map[string]int{yesterday: 3}, wantNew: 0},
		{name: "unlimited deck ignores extra", newPerDay: 0, introduced: 2, fresh: 3, extras: map[string]int{day: 1}, wantNew: 3, wantExtra: 1, wantUnlimited: true},
	}
	for driver, open := range extraNewDrivers() {
		for _, tc := range cases {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				db := open(t)
				deckID := seedDeckCaps(t, db, now, tc.newPerDay, 0)
				for i := 0; i < tc.introduced; i++ {
					id := seedCard(t, db, deckID, "introduced", now)
					seedReview(t, db, 1, id, day, int(StateNew), at)
					seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
				}
				for i := 0; i < tc.fresh; i++ {
					seedCard(t, db, deckID, "fresh", now)
				}
				// 昨天的追加先写，今天的再写：AddDailyExtraNew 会顺手删掉更早复习日的行。
				if n, ok := tc.extras[yesterday]; ok {
					if _, err := store.AddDailyExtraNew(ctx, db, deckID, 1, yesterday, n); err != nil {
						t.Fatalf("AddDailyExtraNew(yesterday) error = %v", err)
					}
				}
				if n, ok := tc.extras[day]; ok {
					if _, err := store.AddDailyExtraNew(ctx, db, deckID, 1, day, n); err != nil {
						t.Fatalf("AddDailyExtraNew(today) error = %v", err)
					}
				}

				builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
				opts := QueueOptions{Now: now, DeckID: deckID}
				items, err := builder.Build(ctx, 1, opts)
				if err != nil {
					t.Fatalf("Build() error = %v", err)
				}
				if got := countKind(items, QueueNew); got != tc.wantNew {
					t.Errorf("Build() new cards = %d, want %d", got, tc.wantNew)
				}
				counts, err := builder.DeckCounts(ctx, 1, []uint64{deckID}, QueueOptions{Now: now})
				if err != nil {
					t.Fatalf("DeckCounts() error = %v", err)
				}
				if got := counts[deckID].New; got != tc.wantNew {
					t.Errorf("DeckCounts() new = %d, want %d", got, tc.wantNew)
				}
				budgets, err := builder.DeckBudgets(ctx, 1, []uint64{deckID}, QueueOptions{Now: now})
				if err != nil {
					t.Fatalf("DeckBudgets() error = %v", err)
				}
				b := budgets[deckID]
				if b.NewExtra != tc.wantExtra || b.NewUnlimited != tc.wantUnlimited {
					t.Errorf("DeckBudgets() extra=%d unlimited=%v, want extra=%d unlimited=%v", b.NewExtra, b.NewUnlimited, tc.wantExtra, tc.wantUnlimited)
				}
				if b.NewPerDay != tc.newPerDay {
					t.Errorf("DeckBudgets() NewPerDay = %d, want the unextended limit %d", b.NewPerDay, tc.newPerDay)
				}
			})
		}
	}
}

// TestAddDailyExtraNewAccumulatesAndDropsStaleDays 断言同一复习日的追加累加，
// 并且写入新复习日时更早复习日的行被删除，每对 (卡组, 用户) 只留一行。
func TestAddDailyExtraNewAccumulatesAndDropsStaleDays(t *testing.T) {
	for driver, open := range extraNewDrivers() {
		t.Run(driver, func(t *testing.T) { testAddDailyExtraNew(t, open(t)) })
	}
}

func testAddDailyExtraNew(t *testing.T, db *gorm.DB) {
	ctx := context.Background()
	deckID := seedDeckCaps(t, db, time.Now().UTC(), 5, 0)

	steps := []struct {
		day       string
		n         int
		wantTotal int
	}{
		{"2026-01-01", 5, 5},
		{"2026-01-01", 3, 8},
		{"2026-01-02", 4, 4},
	}
	for _, s := range steps {
		got, err := store.AddDailyExtraNew(ctx, db, deckID, 1, s.day, s.n)
		if err != nil {
			t.Fatalf("AddDailyExtraNew(%s, %d) error = %v", s.day, s.n, err)
		}
		if got != s.wantTotal {
			t.Errorf("AddDailyExtraNew(%s, %d) = %d, want %d", s.day, s.n, got, s.wantTotal)
		}
	}
	var rows int64
	if err := db.Model(&store.DeckDailyExtra{}).Where("deck_id = ? AND user_id = ?", deckID, 1).Count(&rows).Error; err != nil {
		t.Fatalf("count daily extras: %v", err)
	}
	if rows != 1 {
		t.Errorf("daily extra rows = %d, want 1 (stale days dropped)", rows)
	}
	// 另一个用户的追加互不影响。
	if got, err := store.AddDailyExtraNew(ctx, db, deckID, 2, "2026-01-02", 1); err != nil || got != 1 {
		t.Errorf("AddDailyExtraNew(other user) = %d, %v; want 1, nil", got, err)
	}
	if _, err := store.AddDailyExtraNew(ctx, db, deckID, 1, "2026-01-02", 0); err == nil {
		t.Errorf("AddDailyExtraNew(n=0) error = nil, want a rejection")
	}
}

// TestNewCardsAvailableIgnoresBudget 断言 NewCardsAvailable 数的是卡组里全部可引入的新卡：
// 不受每日额度截断，排除暂停的卡，带标签时只数带这些标签的卡。
func TestNewCardsAvailableIgnoresBudget(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := newTestDB(t)
	deckID := seedDeckCaps(t, db, now, 1, 0)
	seedTaggedCard(t, db, deckID, []string{"a"}, now)
	seedTaggedCard(t, db, deckID, []string{"a", "b"}, now)
	seedTaggedCard(t, db, deckID, []string{"b"}, now)
	suspended := seedTaggedCard(t, db, deckID, []string{"a"}, now)
	at := now.Add(-time.Hour)
	if err := db.Create(&store.CardState{UserID: 1, CardID: suspended, State: StateNew.String(), SuspendedAt: &at}).Error; err != nil {
		t.Fatalf("suspend card: %v", err)
	}

	builder := NewQueueBuilder(db, store.NewDeckStore(db), nil)
	cases := []struct {
		name string
		tags []string
		want int
	}{
		{"whole deck", nil, 3},
		{"one tag", []string{"a"}, 2},
		{"union of tags", []string{"a", "b"}, 3},
		{"unknown tag", []string{"zzz"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := builder.NewCardsAvailable(ctx, 1, deckID, tc.tags, now)
			if err != nil {
				t.Fatalf("NewCardsAvailable() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("NewCardsAvailable() = %d, want %d", got, tc.want)
			}
		})
	}
}
