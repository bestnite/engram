package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"gorm.io/gorm"
)

// ReviewStore 封装 reviews 表的读取。reviews 是 append-only 的，
// 因此这里只有读与流式遍历，没有更新/删除方法。
type ReviewStore struct {
	db *gorm.DB
}

// NewReviewStore 构造复习日志存储。
func NewReviewStore(db *gorm.DB) *ReviewStore { return &ReviewStore{db: db} }

// OptimizerReviewLog 是一行标准的优化器复习日志，字段名与上游 `review_logs` schema 完全一致，
// 可直接喂给 Rust 适配器。
//
// 依据的上游文档（字段与取值范围）：
// https://github.com/open-spaced-repetition/fsrs-optimizer#review-logs-schema
//
//	card_id         卡片唯一标识
//	review_time     复习时刻，UTC 毫秒
//	review_rating   评分 1=Again 2=Hard 3=Good 4=Easy
//	review_state    评分前的卡片状态 0=New 1=Learning 2=Review 3=Relearning
//	review_duration 本次复习耗时，毫秒，非负
//	timezone        用户 IANA 时区，用于界定「新的一天」
//	day_start       新的复习日从本地时间几点（0-23）起算
//
// 这些字段一一映射到 reviews 表的 (card_id, reviewed_at, rating, state_before, elapsed_ms)
// 与 users 表的 (timezone, day_cutoff_hour)；整数取值天然落在上游范围内（服务端写入时已校验）。
type OptimizerReviewLog struct {
	// CardID 对应 reviews.card_id。
	CardID uint64 `json:"card_id"`
	// ReviewTime 是 reviews.reviewed_at 的 UTC 毫秒（UnixMilli）；库里始终以 UTC 存储。
	ReviewTime int64 `json:"review_time"`
	// ReviewRating 是 reviews.rating（1-4）。
	ReviewRating int `json:"review_rating"`
	// ReviewState 是 reviews.state_before（0-3）。
	ReviewState int `json:"review_state"`
	// ReviewDuration 是 reviews.elapsed_ms，毫秒且非负；缺失或异常记 0，保证该字段永远可解析。
	ReviewDuration int `json:"review_duration"`
	// Timezone 是 users.timezone（IANA 名称）。
	Timezone string `json:"timezone"`
	// DayStart 是 users.day_cutoff_hour（0-23）。
	DayStart int `json:"day_start"`
}

// presetDeckIDsQuery 是「userID 当前把 presetID 用在哪些卡组上」的子查询：自己卡组上的预设列，
// 加上自己在共享卡组上的成员设置。优化某个预设只用这些卡组的复习记录。
func presetDeckIDsQuery(db *gorm.DB, userID, presetID uint64) *gorm.DB {
	return db.Raw("SELECT id FROM decks WHERE owner_user_id = ? AND preset_id = ? "+
		"UNION SELECT deck_id FROM deck_member_settings WHERE user_id = ? AND preset_id = ?",
		userID, presetID, userID, presetID)
}

// presetReviewsQuery 是 userID 在挂着 presetID 的卡组上的全部复习日志。
//
// 按卡组归属而不是按用户全量：一个用户可以给不同卡组配不同预设（例如语言与数学分开），
// 用全部日志训练会让每个预设都得到同一组权重，「按预设优化」就名存实亡。归属按卡组**当前**
// 的预设判定：卡组换了预设，它的历史跟着卡组走。卡片软删除不影响（历史仍有效）；卡组被删除后
// 它的复习记录找不到归属，不再参与任何预设的优化。
func presetReviewsQuery(ctx context.Context, db *gorm.DB, userID, presetID uint64) *gorm.DB {
	return db.WithContext(ctx).Table("reviews AS r").
		Joins("JOIN cards AS c ON c.id = r.card_id").
		Joins("JOIN notes AS n ON n.id = c.note_id").
		Where("r.user_id = ?", userID).
		Where("n.deck_id IN (?)", presetDeckIDsQuery(db, userID, presetID))
}

// StreamForPreset 用数据库游标逐行读出 userID 在挂着 presetID 的卡组上的复习日志，
// 按 reviewed_at、id 升序（时间顺序，也是优化器对日志的隐含假设），对每一行调用 emit；
// emit 返回错误时立即中止。
//
// 流式的意义：优化器门槛是「至少数百条复习」，但真实用户可能有数十万条；
// 用 Rows() 游标而不是 Find()，任意时刻内存里只有当前一行，峰值内存不随日志总量增长
// （与 internal/api 的 ExportCards 同一手法，先例）。
func (s *ReviewStore) StreamForPreset(ctx context.Context, userID, presetID uint64, emit func(*Review) error) error {
	rows, err := presetReviewsQuery(ctx, s.db, userID, presetID).
		Select("r.id, r.card_id, r.user_id, r.rating, r.state_before, r.reviewed_at, r.elapsed_ms").
		Order("r.reviewed_at ASC, r.id ASC").
		Rows()
	if err != nil {
		return fmt.Errorf("stream reviews for user %d preset %d: %w", userID, presetID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var r Review
		if err := s.db.ScanRows(rows, &r); err != nil {
			return fmt.Errorf("scan review row for user %d: %w", userID, err)
		}
		if err := emit(&r); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate reviews for user %d: %w", userID, err)
	}
	return nil
}

// ExportOptimizerLog 把 userID 在挂着 presetID 的卡组上的复习日志按标准 schema 流式写成 JSONL（每行一个 JSON 对象）
// 到 w，供适配器读取。时区与日切取自用户记录：优化器需要它们才能把跨午夜的复习
// 正确归到同一天。
//
// 返回值只在读取用户、遍历日志或写入失败时非 nil；单行编码失败（例如磁盘写满）会中止导出，
// 不会静默产出半截文件——调用方据此把作业标为 failed。
func (s *ReviewStore) ExportOptimizerLog(ctx context.Context, userID, presetID uint64, w io.Writer) error {
	var user User
	if err := s.db.WithContext(ctx).
		Select("timezone", "day_cutoff_hour").
		First(&user, "id = ?", userID).Error; err != nil {
		return fmt.Errorf("load user %d for review-log export: %w", userID, err)
	}
	tz := user.Timezone
	if tz == "" {
		// 列是 NOT NULL，空值只可能来自异常数据；上游要求 IANA 时区字符串，回退 UTC 保证文件合法。
		tz = "UTC"
	}
	// 导出必须与评分、队列及统计使用同一切点：NULL 为 4，显式 0 为午夜。
	dayStart := ResolveCutoff(user.DayCutoffHour)

	enc := json.NewEncoder(w)
	return s.StreamForPreset(ctx, userID, presetID, func(r *Review) error {
		return enc.Encode(OptimizerReviewLog{
			CardID:         r.CardID,
			ReviewTime:     r.ReviewedAt.UTC().UnixMilli(),
			ReviewRating:   r.Rating,
			ReviewState:    r.StateBefore,
			ReviewDuration: clampDurationMS(r.ElapsedMS),
			Timezone:       tz,
			DayStart:       dayStart,
		})
	})
}

// clampDurationMS 把可空的 elapsed_ms 转成非负毫秒：NULL 或负值记 0。上游要求
// review_duration 非负且可缺失；显式写 0 比省略更利于下游逐行解析。
func clampDurationMS(ms *int) int {
	if ms == nil || *ms < 0 {
		return 0
	}
	return *ms
}
