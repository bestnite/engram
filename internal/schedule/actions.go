package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// ErrNothingToUndo 表示该卡没有任何复习日志，Undo 无对象可回滚。
var ErrNothingToUndo = errors.New("schedule: no review to undo")

// ErrCardNotFound 表示要操作的目标卡片不存在。
var ErrCardNotFound = errors.New("schedule: card not found")

// UndoInput 是一次撤销的入参；tx 的语义与 Submit 相同（事务边界由调用方提供）。
type UndoInput struct {
	CardID    uint64
	UserID    uint64
	Scheduler *Scheduler
	// Now 为零值时取当前时间，仅用于审计时间戳。
	Now time.Time
}

// Rollback 撤销该卡最后一次评分：用最后一条 reviews 作为日志驱动 fsrs.Rollback 恢复状态，
// 删除该日志行，并把恢复后的状态写回 card_states（DESIGN.md §3.4）。
//
// 为什么需要“上一条日志”：reviews 只记录评分后的调度结果（interval_days/stability/difficulty），
// 评分前的 FSRS 快照没有独立列。上一条日志的评分后结果恰好就是本条的评分前快照，因此用它
// 重建 ReviewLog 的评分前字段；由此 due_at 与 interval 能精确恢复到本次评分之前的值。
// step_index（剩余学习步骤）由被撤销日志的 step_index_before 精确还原（M3-9）；旧行该列为
// NULL 时退回 fsrs.Rollback 的结果（会把 step_index 归零）。
func Rollback(ctx context.Context, tx *gorm.DB, in UndoInput) (store.CardState, error) {
	if tx == nil {
		return store.CardState{}, errors.New("schedule: undo: transaction is required")
	}
	if in.Scheduler == nil {
		return store.CardState{}, errors.New("schedule: undo: scheduler is required")
	}
	if in.CardID == 0 || in.UserID == 0 {
		return store.CardState{}, errors.New("schedule: undo: card id and user id are required")
	}

	// 取最近两条日志：last 是要撤销的，prev 提供评分前快照（可能不存在）。
	var logs []store.Review
	if err := tx.WithContext(ctx).Where("card_id = ? AND user_id = ?", in.CardID, in.UserID).
		Order("id DESC").Limit(2).Find(&logs).Error; err != nil {
		return store.CardState{}, fmt.Errorf("schedule: undo: load review log: %w", err)
	}
	if len(logs) == 0 {
		return store.CardState{}, fmt.Errorf("%w: card %d user %d", ErrNothingToUndo, in.CardID, in.UserID)
	}
	last := logs[0]
	var prev *store.Review
	if len(logs) > 1 {
		prev = &logs[1]
	}

	cur, err := loadStateForUpdate(ctx, tx, in.CardID, in.UserID)
	if err != nil {
		return store.CardState{}, err
	}
	if cur == nil {
		return store.CardState{}, fmt.Errorf("schedule: undo: card %d user %d has a review log but no state row", in.CardID, in.UserID)
	}

	stateBefore, err := ParseState(parseStateInt(last.StateBefore))
	if err != nil {
		return store.CardState{}, err
	}

	card, err := cardFromState(cur)
	if err != nil {
		return store.CardState{}, err
	}
	log, due, lastReview, scheduledDays := rebuildReviewLog(last, prev)
	restored, err := in.Scheduler.fsrs.Rollback(card, log)
	if err != nil {
		return store.CardState{}, fmt.Errorf("schedule: undo: rollback card %d: %w", in.CardID, err)
	}

	// 组装恢复后的状态行。状态/stability/difficulty/reps/lapses/step_index 取 Rollback 的结果
	// （step_index 的快照已由 rebuildReviewLog 从 step_index_before 重建）；
	// due/last_review/scheduled_days 用上一条日志重建，保证到期日与间隔精确还原。
	next := store.CardState{
		CardID:        in.CardID,
		UserID:        in.UserID,
		State:         stateBefore.String(),
		DueAt:         due,
		LastReviewAt:  lastReview,
		ScheduledDays: scheduledDays,
		StepIndex:     restored.RemainingSteps,
		Reps:          int(restored.Reps),
		Lapses:        int(restored.Lapses),
		Version:       cur.Version + 1,
	}
	if stateBefore != StateNew {
		stability := restored.Stability
		difficulty := restored.Difficulty
		next.Stability = &stability
		next.Difficulty = &difficulty
	}
	if next.LastReviewAt != nil {
		next.ElapsedDays = elapsedDaysSince(next.LastReviewAt, nowOr(in.Now))
	}
	written, err := upsertState(ctx, tx, &next, cur.Version)
	if err != nil {
		return store.CardState{}, err
	}
	// 守卫失败说明状态行在本次读取之后被并发改过：不做部分恢复，返回冲突哨兵。
	if !written {
		return store.CardState{}, fmt.Errorf("%w: card %d user %d changed concurrently while undoing",
			ErrVersionConflict, in.CardID, in.UserID)
	}

	// reviews 只增不改的例外就是 Undo：删除被撤销的那一行，并写一条审计说明。
	if err := tx.WithContext(ctx).Delete(&store.Review{}, last.ID).Error; err != nil {
		return store.CardState{}, fmt.Errorf("schedule: undo: delete review %d: %w", last.ID, err)
	}
	if err := writeUndoAudit(ctx, tx, in, last); err != nil {
		return store.CardState{}, err
	}
	return next, nil
}

// rebuildReviewLog 依据被撤销日志与它的上一条日志，重建 fsrs.Rollback 需要的 ReviewLog，
// 并返回评分前的到期日、上次复习时间与间隔天数。
func rebuildReviewLog(last store.Review, prev *store.Review) (fsrs.ReviewLog, *time.Time, *time.Time, int) {
	log := fsrs.ReviewLog{
		Rating: fsrs.Rating(last.Rating),
		State:  fsrs.State(last.StateBefore),
		Review: last.ReviewedAt.UTC(),
	}
	// 学习步骤游标：评分前的剩余步骤数只有 reviews.step_index_before 知道（M3-9）。
	// 旧行该列为 NULL，保持 0 —— 即旧行为（Undo 后 step_index 归零）。
	if last.StepIndexBefore != nil {
		log.RemainingSteps = *last.StepIndexBefore
	}
	if prev == nil {
		// 首次评分被撤销：恢复到全新卡（无到期日、无上次复习）。
		return log, nil, nil, 0
	}
	log.Due = prev.ReviewedAt.UTC()
	if prev.Stability != nil {
		log.Stability = *prev.Stability
	}
	if prev.Difficulty != nil {
		log.Difficulty = *prev.Difficulty
	}
	due := prev.ReviewedAt.UTC()
	scheduled := 0
	if prev.IntervalDays != nil {
		scheduled = int(math.Round(*prev.IntervalDays))
		due = addDays(prev.ReviewedAt.UTC(), *prev.IntervalDays)
	}
	log.ScheduledDays = uint64(maxInt(scheduled, 0))
	lastReview := prev.ReviewedAt.UTC()
	return log, &due, &lastReview, scheduled
}

// addDays 在 t 上加 d 天；取整到毫秒，因为 interval_days 是 REAL，亚毫秒精度无意义。
func addDays(t time.Time, d float64) time.Time {
	return t.Add(time.Duration(math.Round(d*24*60*60*1000)) * time.Millisecond)
}

// parseStateInt 把日志里的整数状态转成字符串，供 ParseState 复用同一套校验。
func parseStateInt(v int) string {
	switch State(v) {
	case StateNew:
		return "new"
	case StateLearning:
		return "learning"
	case StateReview:
		return "review"
	case StateRelearning:
		return "relearning"
	default:
		return fmt.Sprintf("invalid-%d", v)
	}
}

// writeUndoAudit 在同一个事务里写一条 audit_log，说明哪一条评分被撤销（DESIGN.md §3.4）。
func writeUndoAudit(ctx context.Context, tx *gorm.DB, in UndoInput, last store.Review) error {
	detail, err := json.Marshal(map[string]any{
		"review_id":    last.ID,
		"rating":       last.Rating,
		"state_before": last.StateBefore,
		"reviewed_at":  last.ReviewedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return fmt.Errorf("schedule: undo: marshal audit detail: %w", err)
	}
	detailStr := string(detail)
	targetType := "card"
	userID := in.UserID
	cardID := in.CardID
	row := store.AuditLog{
		UserID:     &userID,
		Action:     store.ActionReviewUndo,
		TargetType: &targetType,
		TargetID:   &cardID,
		DetailJSON: &detailStr,
		CreatedAt:  nowOr(in.Now).UTC(),
	}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("schedule: undo: write audit log: %w", err)
	}
	return nil
}

// Suspend 暂停一张卡（cards.suspended_at = at）。暂停是卡片级、对所有用户生效的
// 共享状态（DESIGN.md §2.2），因此不以 user_id 为条件。
func Suspend(ctx context.Context, tx *gorm.DB, cardID uint64, at time.Time) error {
	if tx == nil {
		return errors.New("schedule: suspend: transaction is required")
	}
	if cardID == 0 {
		return errors.New("schedule: suspend: card id is required")
	}
	if at.IsZero() {
		at = time.Now()
	}
	res := tx.WithContext(ctx).Model(&store.Card{}).Where("id = ?", cardID).
		Update("suspended_at", at.UTC())
	if res.Error != nil {
		return fmt.Errorf("schedule: suspend card %d: %w", cardID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: card %d", ErrCardNotFound, cardID)
	}
	return nil
}

// BuryInput 是一次“本日埋藏”的入参。
type BuryInput struct {
	CardID uint64
	UserID uint64
	Now    time.Time
	// Location/Timezone/DayCutoffHour 决定“明天”的边界。
	Location      *time.Location
	Timezone      string
	DayCutoffHour int
}

// Bury 把一张卡埋藏到下一个复习日开始：把它（该用户）的 due_at 推后到下一个切点，
// 从而在本日队列里消失，但不改动任何学习进度数值（DESIGN.md §3.3 的切点规则、§8.2 的 b 键）。
//
// 没有 card_states 行的新卡会先建一行（state=new、due_at=下一个切点）；队列构建对
// “新卡且 due_at 未到”的卡不取出，因此新卡也能被埋藏。
func Bury(ctx context.Context, tx *gorm.DB, in BuryInput) (store.CardState, error) {
	if tx == nil {
		return store.CardState{}, errors.New("schedule: bury: transaction is required")
	}
	if in.CardID == 0 || in.UserID == 0 {
		return store.CardState{}, errors.New("schedule: bury: card id and user id are required")
	}
	now := nowOr(in.Now)
	loc := queueLocation(in.Location, in.Timezone)
	due := nextReviewDayStart(now, loc, normalizedCutoff(in.DayCutoffHour))

	cur, err := loadStateForUpdate(ctx, tx, in.CardID, in.UserID)
	if err != nil {
		return store.CardState{}, err
	}
	row := store.CardState{
		CardID: in.CardID,
		UserID: in.UserID,
		State:  StateNew.String(),
		DueAt:  &due,
	}
	version := 0
	if cur != nil {
		// 保留原有的全部进度，只推迟到期日；version 推进以作废在途提交。
		row = *cur
		row.DueAt = &due
		version = cur.Version
	}
	row.Version = version + 1
	written, err := upsertState(ctx, tx, &row, version)
	if err != nil {
		return store.CardState{}, err
	}
	// 守卫失败说明状态行在本次读取之后被并发改过（并发首评或另一次埋藏）。
	if !written {
		return store.CardState{}, fmt.Errorf("%w: card %d user %d changed concurrently while burying",
			ErrVersionConflict, in.CardID, in.UserID)
	}
	return row, nil
}

// nextReviewDayStart 返回“下一个复习日”的起点（本地时间 = 日期 + 1，小时 = 切点），转成 UTC。
// 例如切点 4、本地 2026-10-02 12:00 → 2026-10-03 04:00 本地。
func nextReviewDayStart(now time.Time, loc *time.Location, cutoffHour int) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	day := local.Add(-time.Duration(cutoffHour) * time.Hour)
	next := time.Date(day.Year(), day.Month(), day.Day(), cutoffHour, 0, 0, 0, loc).AddDate(0, 0, 1)
	return next.UTC()
}

func nowOr(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
