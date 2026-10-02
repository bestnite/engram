package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 本文件是 M1-18（邮件类型目录与每用户偏好）的持久化层。
//
// 为什么用独立表而不是给 users 加一个 JSON 列：models.go 是单写者热点（AGENTS.md §6.4），
// 给 User 加列必须改那份热点文件；独立表只需在 AllModels() 登记一行（本任务只允许改那一行），
// 并且偏好与账号生命周期解耦——删账号时偏好随行一起消失，不需要额外清理 users 行。
//
// 存储只保存**用户的显式选择**，不保存由目录推导的默认值：
// 「能不能关、默认开还是关」是 internal/mail 目录的唯一职责；这里若也存一份默认值，
// 目录改动后旧行就会与目录分歧。读取方拿到 choices 后交给 mail.ResolveEnabled 求有效值。
//
// 选择以 JSON 对象（TEXT，两库通用，AGENTS.md §2.3 第 4 条）存储：键是 mail.Type 的
// 字符串形式，值是该类型的显式开关。JSON 里没有的键表示「用户没选过」，回落到目录默认。

// EmailPref 是每用户的邮件类型偏好；每人至多一行。
type EmailPref struct {
	UserID uint64 `gorm:"primaryKey;column:user_id" json:"user_id"`
	// ChoicesJSON 是 {"<type>": bool} 的 JSON 文本；空串或 "{}" 表示没有任何显式选择。
	ChoicesJSON string    `gorm:"column:choices_json;not null" json:"choices_json"`
	UpdatedAt   time.Time `gorm:"not null" json:"updated_at"`
}

func (EmailPref) TableName() string { return "email_prefs" }

// EmailPrefStore 封装 email_prefs 的 GORM 访问。
type EmailPrefStore struct {
	db *gorm.DB
}

// NewEmailPrefStore 构造邮件偏好存储。
func NewEmailPrefStore(db *gorm.DB) *EmailPrefStore { return &EmailPrefStore{db: db} }

// Choices 返回用户的显式选择；没有任何选择时返回空 map（不是 nil，调用方可直接读）。
// 未登记的行与空 JSON 都按「没有选择」处理：这是全新用户与「清空过偏好」的同一语义。
func (s *EmailPrefStore) Choices(ctx context.Context, userID uint64) (map[string]bool, error) {
	var row EmailPref
	err := s.db.WithContext(ctx).First(&row, "user_id = ?", userID).Error
	if err == gorm.ErrRecordNotFound {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load email preferences: %w", err)
	}
	return decodeChoices(row.ChoicesJSON)
}

// SetChoices 覆盖写入用户的全部显式选择（upsert，两库都用 clause.OnConflict）。
// 传 nil 或空 map 等价于清空选择，落库为 "{}"；行保留，因为「存在但为空」与「不存在」语义相同。
func (s *EmailPrefStore) SetChoices(ctx context.Context, userID uint64, choices map[string]bool, at time.Time) error {
	encoded, err := encodeChoices(choices)
	if err != nil {
		return err
	}
	row := EmailPref{UserID: userID, ChoicesJSON: encoded, UpdatedAt: at.UTC()}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{"choices_json": encoded, "updated_at": at.UTC()}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("save email preferences: %w", err)
	}
	return nil
}

// decodeChoices 把存储里的 JSON 文本解成 map；空串/空对象返回空 map。
// 损坏的 JSON 是数据缺陷，报错而不是静默当成空选择——静默会让用户以为设置还在。
func decodeChoices(raw string) (map[string]bool, error) {
	if raw == "" || raw == "{}" {
		return map[string]bool{}, nil
	}
	var out map[string]bool
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode email preferences %q: %w", raw, err)
	}
	if out == nil {
		out = map[string]bool{}
	}
	return out, nil
}

// encodeChoices 把 map 编成 JSON 文本；nil/空 map 编成 "{}"。
// json.Marshal 对 map 键排序，因此同一组选择每次落库字节一致，便于比对与测试。
func encodeChoices(choices map[string]bool) (string, error) {
	if len(choices) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(choices)
	if err != nil {
		return "", fmt.Errorf("encode email preferences: %w", err)
	}
	return string(b), nil
}
