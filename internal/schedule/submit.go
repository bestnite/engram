package schedule

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"example.com/flashcard/internal/store"
)

// ErrVersionConflict 表示调用方持有的 expected_version 与库里不一致：客户端重放、
// 双开窗口，或一次评分被提交了两次。调用方应把它映射成 HTTP 409（DESIGN.md §3.4）。
var ErrVersionConflict = errors.New("schedule: card state version conflict")

// grade_source 的合法取值（DESIGN.md §2.2、§14）。typed/llm 属于作答类与未来 LLM 评分。
const (
	GradeSourceSelf  = "self"
	GradeSourceTyped = "typed"
	GradeSourceLLM   = "llm"
)

// SubmitInput 是一次评分提交的入参。
//
// 事务边界由调用方提供：tx 必须是调用方开启的事务（或裸句柄，此时不保证单事务），
// Submit 只通过它读写 card_states / reviews，自己不 Begin/Commit。这样评分提交能与
// 外层更广的操作共用一个事务（AGENTS.md §2.4、DESIGN.md §3.4）。
type SubmitInput struct {
	CardID uint64
	UserID uint64
	// Rating 是自评分，1–4；越界直接报英文错误。
	Rating Rating
	// ExpectedVersion 是调用方读到的 card_states.version；不匹配返回 ErrVersionConflict。
	ExpectedVersion int
	// ElapsedMS 是用户答题耗时（毫秒），可空；写入 reviews.elapsed_ms。
	ElapsedMS *int
	// GradeSource 为空时按 self 处理；只接受 self/typed/llm。
	GradeSource string
	// GradeDetailJSON 是判分细节原文，可空（作答类题型或未来 LLM 评分使用）。
	GradeDetailJSON *string
	// Scheduler 用于计算新状态，必填。
	Scheduler *Scheduler
	// Now 为零值时取当前时间；到期日与复习日都以它为准。
	Now time.Time
	// Location/Timezone/DayCutoffHour 决定 review_day 的切分（DESIGN.md §3.3）。
	Location      *time.Location
	Timezone      string
	DayCutoffHour int
}

// SubmitResult 返回提交后的结果：新状态行、写入的 review id，以及调度结果。
type SubmitResult struct {
	// State 是落库后的新 card_states 行。
	State store.CardState
	// ReviewID 是本次写入的 reviews.id。
	ReviewID uint64
	// Outcome 是调度器算出的调度结果（到期/间隔/稳定性等）。
	Outcome Outcome
}

// Submit 在给定的 tx 内提交一次评分：读取并锁定当前状态、校验 expected_version、
// 计算新状态、UPSERT card_states、INSERT reviews（DESIGN.md §3.4）。
//
// 所有写操作都走同一个 tx：调用方回滚时，状态更新与 review 行一并消失，
// 因此“事务失败后不留 reviews 行”成立。重复提交同一 expected_version 时，
// 第一次已把 version 推进，第二次在版本校验处直接返回 ErrVersionConflict，不写任何行。
func Submit(ctx context.Context, tx *gorm.DB, in SubmitInput) (SubmitResult, error) {
	if tx == nil {
		return SubmitResult{}, errors.New("schedule: submit review: transaction is required")
	}
	if in.Scheduler == nil {
		return SubmitResult{}, errors.New("schedule: submit review: scheduler is required")
	}
	if in.CardID == 0 || in.UserID == 0 {
		return SubmitResult{}, errors.New("schedule: submit review: card id and user id are required")
	}
	if !in.Rating.Valid() {
		return SubmitResult{}, fmt.Errorf("schedule: submit review: invalid rating %d", int(in.Rating))
	}
	source := in.GradeSource
	if source == "" {
		source = GradeSourceSelf
	}
	switch source {
	case GradeSourceSelf, GradeSourceTyped, GradeSourceLLM:
	default:
		return SubmitResult{}, fmt.Errorf("schedule: submit review: invalid grade source %q", source)
	}

	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	// 1. 取当前状态并加行锁（SQLite 没有 FOR UPDATE，靠单写连接串行化，见 Open）。
	cur, err := loadStateForUpdate(ctx, tx, in.CardID, in.UserID)
	if err != nil {
		return SubmitResult{}, err
	}
	stateBefore := StateNew
	version := 0
	if cur != nil {
		stateBefore, err = ParseState(cur.State)
		if err != nil {
			return SubmitResult{}, err
		}
		version = cur.Version
	}

	// 2. 乐观锁校验：不匹配直接返回冲突，绝不写入。
	if version != in.ExpectedVersion {
		return SubmitResult{}, fmt.Errorf("%w: card %d user %d holds version %d, expected %d",
			ErrVersionConflict, in.CardID, in.UserID, version, in.ExpectedVersion)
	}

	// 3. 计算新状态。没有状态行时按一张全新卡处理（state=new）。
	base := cur
	if base == nil {
		base = &store.CardState{CardID: in.CardID, UserID: in.UserID, State: StateNew.String()}
	}
	outcome, err := in.Scheduler.Next(base, now, in.Rating)
	if err != nil {
		return SubmitResult{}, err
	}

	// 4. 写 card_states（version + 1）。UPSERT 兼顾“首次评分建行”与“已有行更新”。
	newState := stateFromOutcome(base, outcome, now, version+1)
	if err := upsertState(ctx, tx, &newState); err != nil {
		return SubmitResult{}, err
	}

	// 5. 写 reviews。所有 §2.2 列出的字段都写全，它是参数优化的唯一燃料。
	review := reviewFromOutcome(in, base, outcome, stateBefore, now)
	if err := tx.WithContext(ctx).Create(&review).Error; err != nil {
		return SubmitResult{}, fmt.Errorf("schedule: submit review: insert review for card %d: %w", in.CardID, err)
	}

	return SubmitResult{State: newState, ReviewID: review.ID, Outcome: outcome}, nil
}

// loadStateForUpdate 按 (card_id, user_id) 读取状态行；PostgreSQL 下加 FOR UPDATE 行锁。
// 未找到返回 (nil, nil) —— 全新卡没有状态行是正常情况，不是错误。
func loadStateForUpdate(ctx context.Context, tx *gorm.DB, cardID, userID uint64) (*store.CardState, error) {
	var st store.CardState
	q := tx.WithContext(ctx).Model(&store.CardState{})
	// SQLite（glebarez）不支持 SELECT ... FOR UPDATE；它靠单写连接串行化写入。
	if tx.Dialector.Name() == "postgres" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := q.Where("card_id = ? AND user_id = ?", cardID, userID).First(&st).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("schedule: load card state (card %d, user %d): %w", cardID, userID, err)
	}
	return &st, nil
}

// stateFromOutcome 把调度结果摊平成要落库的 card_states 行；version 由调用方给出。
func stateFromOutcome(base *store.CardState, o Outcome, now time.Time, version int) store.CardState {
	due := o.Due.UTC()
	stability := o.Stability
	difficulty := o.Difficulty
	lastReview := now
	return store.CardState{
		CardID:        base.CardID,
		UserID:        base.UserID,
		State:         o.State.String(),
		DueAt:         &due,
		StepIndex:     o.RemainingSteps,
		Stability:     &stability,
		Difficulty:    &difficulty,
		Reps:          o.Reps,
		Lapses:        o.Lapses,
		ScheduledDays: o.ScheduledDays,
		ElapsedDays:   elapsedDaysSince(base.LastReviewAt, now),
		LastReviewAt:  &lastReview,
		Version:       version,
	}
}

// upsertState 用 GORM 的 clause.OnConflict（双库各自生成正确的 SQL，DESIGN.md §2.3）
// 写入状态行：首次评分建行，之后按 (card_id, user_id) 更新。
func upsertState(ctx context.Context, tx *gorm.DB, st *store.CardState) error {
	cols := []string{
		"state", "due_at", "step_index", "stability", "difficulty",
		"reps", "lapses", "scheduled_days", "elapsed_days", "last_review_at", "version",
	}
	err := tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "card_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns(cols),
	}).Create(st).Error
	if err != nil {
		return fmt.Errorf("schedule: submit review: upsert card state (card %d, user %d): %w", st.CardID, st.UserID, err)
	}
	return nil
}

// reviewFromOutcome 构造 append-only 的复习日志行。
//
// 取值口径：state_before 是评分前的状态枚举；interval_days / stability / difficulty 是
// 本次评分产生的新值（供留存率与记忆强度统计）；duration_days 是距上一次复习的天数
// （即本次评分前的 elapsed days，可为空——首次评分没有上一次）。
func reviewFromOutcome(in SubmitInput, base *store.CardState, o Outcome, stateBefore State, now time.Time) store.Review {
	interval := o.IntervalDays
	stability := o.Stability
	difficulty := o.Difficulty
	loc := queueLocation(in.Location, in.Timezone)
	cutoff := normalizedCutoff(in.DayCutoffHour)
	row := store.Review{
		CardID:          in.CardID,
		UserID:          in.UserID,
		Rating:          int(in.Rating),
		GradeSource:     gradeSourceOrSelf(in.GradeSource),
		GradeDetailJSON: in.GradeDetailJSON,
		ReviewedAt:      now,
		ReviewDay:       ReviewDay(now, loc, cutoff),
		ElapsedMS:       in.ElapsedMS,
		DurationDays:    durationDaysSince(base, now),
		StateBefore:     int(stateBefore),
		IntervalDays:    &interval,
		Stability:       &stability,
		Difficulty:      &difficulty,
	}
	return row
}

// normalizedCutoff 与队列构建的默认规则保持一致：0 视为未设置，用文档化默认 4。
func normalizedCutoff(hour int) int {
	if hour < 0 || hour > 23 {
		return DefaultDayCutoffHour
	}
	if hour == 0 {
		return DefaultDayCutoffHour
	}
	return hour
}

// durationDaysSince 返回距上次复习的天数（可为小数）；无上次复习时返回 nil，
// 因为没有可依据的间隔，写 0 会污染统计。
func durationDaysSince(base *store.CardState, now time.Time) *float64 {
	if base == nil || base.LastReviewAt == nil {
		return nil
	}
	d := now.Sub(base.LastReviewAt.UTC()).Hours() / 24
	if d < 0 {
		d = 0
	}
	return &d
}

// queueLocation 解析时区；未指定时退回 UTC（复习日仍可计算，不阻塞提交）。
func queueLocation(loc *time.Location, tz string) *time.Location {
	if loc != nil {
		return loc
	}
	if tz != "" {
		if loaded, err := time.LoadLocation(tz); err == nil {
			return loaded
		}
	}
	return time.UTC
}

func gradeSourceOrSelf(source string) string {
	if source == "" {
		return GradeSourceSelf
	}
	return source
}

// elapsedDaysSince 返回距上次复习的整天数；无上次复习时为 0。
func elapsedDaysSince(last *time.Time, now time.Time) int {
	if last == nil {
		return 0
	}
	d := now.Sub(last.UTC()).Hours() / 24
	if d < 0 {
		return 0
	}
	return int(d)
}
