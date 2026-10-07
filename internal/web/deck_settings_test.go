package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// ddAfterLabel 返回页面上某个 <dt>标签</dt> 之后第一个 </dd> 内的文本。
// 设置页的今日额度是 dt/dd 成对渲染的，用它按本地化标签取值，避免按数字猜位置。
func ddAfterLabel(body, label string) string {
	idx := strings.Index(body, ">"+label+"</dt>")
	if idx < 0 {
		return ""
	}
	rest := body[idx:]
	start := strings.Index(rest, "<dd")
	if start < 0 {
		return ""
	}
	rest = rest[start:]
	gt := strings.Index(rest, ">")
	if gt < 0 {
		return ""
	}
	rest = rest[gt+1:]
	end := strings.Index(rest, "</dd>")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// seedTodayUsage 写入今日的已用记录：newUsed 次「引入新卡」（state_before=0）与
// reviewUsed 次「复习」（state_before<>0）。每张卡都配一行未来的 card_states，
// 避免它们以「新卡」身份再次入队而干扰计数。
func seedTodayUsage(t *testing.T, db *gorm.DB, ownerID, deckID uint64, newUsed, reviewUsed int) {
	t.Helper()
	now := time.Now().UTC()
	day := schedule.ReviewDay(now, time.UTC, schedule.DefaultDayCutoffHour)
	stability, difficulty := 5.0, 5.0
	future := now.Add(48 * time.Hour)
	last := now.Add(-48 * time.Hour)
	seed := func(n int, stateBefore int) {
		for i := 0; i < n; i++ {
			cardID := seedCardRow(t, db, deckID, "used", now)
			if err := db.Create(&store.Review{CardID: cardID, UserID: ownerID, Rating: 3, GradeSource: "self",
				ReviewedAt: now.Add(-time.Hour), ReviewDay: day, StateBefore: stateBefore}).Error; err != nil {
				t.Fatalf("create review: %v", err)
			}
			if err := db.Create(&store.CardState{CardID: cardID, UserID: ownerID, State: "review",
				DueAt: &future, Stability: &stability, Difficulty: &difficulty, LastReviewAt: &last}).Error; err != nil {
				t.Fatalf("create card state: %v", err)
			}
		}
	}
	seed(newUsed, int(schedule.StateNew))
	seed(reviewUsed, int(schedule.StateReview))
}

// deckQueueCountsNow 用真实队列（Build(DeckID=d)）数一个卡组今天能刷的新卡与复习卡
// （复习数含学习/再学习到期卡），与列表页那两个数同源。
func deckQueueCountsNow(t *testing.T, srv *Server, db *gorm.DB, userID, deckID uint64) (newN, reviewN int) {
	t.Helper()
	preset := store.NewPreset(userID, "counts preset")
	sched, err := schedule.NewScheduler(&preset)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	builder := schedule.NewQueueBuilder(db, srv.decks, sched)
	items, err := builder.Build(context.Background(), userID,
		schedule.QueueOptions{DeckID: deckID, Now: time.Now().UTC(), Location: time.UTC, NewOrder: schedule.NewOrderCreated})
	if err != nil {
		t.Fatalf("Build(DeckID=%d): %v", deckID, err)
	}
	for _, it := range items {
		switch it.Kind {
		case schedule.QueueNew:
			newN++
		case schedule.QueueReview, schedule.QueueLearning:
			reviewN++
		}
	}
	return newN, reviewN
}

// deckCapsFromDB 回读卡组的两列上限，用于断言「未写库」或写入值。
func deckCapsFromDB(t *testing.T, db *gorm.DB, deckID uint64) store.DeckCaps {
	t.Helper()
	caps, err := store.NewDeckStore(db).Caps(context.Background(), deckID)
	if err != nil {
		t.Fatalf("read deck caps: %v", err)
	}
	return caps
}
