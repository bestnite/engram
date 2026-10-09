package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// 管理面板的用户管理所需的用户存储扩展。
//
// 放在独立文件而不是塞进 user.go / models.go：这两处是其它里程碑的单写者热点，
// 并行工作时尽量不动它们。这里只新增方法，不改动既有行为。

// AdminListUsersPageSize 是管理面板用户列表的每页条数；同时作为一次渲染的上限，
// 避免一次把全库用户全画出来（「分页或上限保护」）。
const AdminListUsersPageSize = 50

// ListForAdmin 按搜索词分页列出用户。query 为空表示不筛选；命中用户名、邮箱或显示名。
// 搜索一律 LOWER(...) 比较：SQLite 的 LIKE 对 ASCII 默认不区分大小写，而 PostgreSQL 区分，
// 统一转小写才能让两库行为一致（双库约定）。
func (s *UserStore) ListForAdmin(ctx context.Context, query string, page, size int) ([]User, int64, error) {
	if size <= 0 || size > AdminListUsersPageSize {
		size = AdminListUsersPageSize
	}
	if page < 1 {
		page = 1
	}
	q := s.db.WithContext(ctx).Model(&User{})
	if query != "" {
		like := "%" + query + "%"
		q = q.Where("LOWER(username) LIKE LOWER(?) OR LOWER(email) LIKE LOWER(?) OR LOWER(display_name) LIKE LOWER(?)",
			like, like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count users for admin: %w", err)
	}
	var users []User
	if err := q.Order("id ASC").Limit(size).Offset((page - 1) * size).Find(&users).Error; err != nil {
		return nil, 0, fmt.Errorf("list users for admin: %w", err)
	}
	return users, total, nil
}

// ListAdmins 返回所有仍可登录的管理员账号（role = admin 且 status = active），按 id 升序。
//
// D 类管理员通知据此解析收件地址规定只用登录邮箱、不设单独收件
// 邮箱，所以收件人就是这些账号的 Email。只取 active 的管理员：被禁用的账号收不到信也不该
// 被当成投递目标。
func (s *UserStore) ListAdmins(ctx context.Context) ([]User, error) {
	var users []User
	if err := s.db.WithContext(ctx).
		Where("role = ? AND status = ?", RoleAdmin, StatusActive).
		Order("id ASC").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("list admins: %w", err)
	}
	return users, nil
}

// SetRole 只改角色一列，避免整行覆盖带来的并发丢写。
func (s *UserStore) SetRole(ctx context.Context, id uint64, role string) error {
	if err := s.db.WithContext(ctx).Model(&User{}).Where("id = ?", id).
		Update("role", role).Error; err != nil {
		return fmt.Errorf("set user role: %w", err)
	}
	return nil
}

// UserUsage 是一个用户的用量计数（卡组数、卡片数、复习次数），管理面板列表里展示。
type UserUsage struct {
	Decks   int64
	Cards   int64
	Reviews int64
}

// UsageCounts 统计某用户拥有的卡组数、这些卡组下的有效卡片数，以及其复习次数。
func (s *UserStore) UsageCounts(ctx context.Context, userID uint64) (UserUsage, error) {
	var u UserUsage
	db := s.db.WithContext(ctx)
	if err := db.Model(&Deck{}).Where("owner_user_id = ?", userID).Count(&u.Decks).Error; err != nil {
		return u, fmt.Errorf("count decks for user: %w", err)
	}
	// 只计未软删除的 note / card，与复习页的可见集合口径一致。
	if err := db.Raw(`SELECT COUNT(*) FROM cards c
		JOIN notes n ON n.id = c.note_id AND n.deleted_at IS NULL
		JOIN decks d ON d.id = n.deck_id
		WHERE d.owner_user_id = ? AND c.deleted_at IS NULL`, userID).Scan(&u.Cards).Error; err != nil {
		return u, fmt.Errorf("count cards for user: %w", err)
	}
	if err := db.Model(&Review{}).Where("user_id = ?", userID).Count(&u.Reviews).Error; err != nil {
		return u, fmt.Errorf("count reviews for user: %w", err)
	}
	return u, nil
}

// DeleteUser 彻底删除一个用户及其拥有的数据。
//
// 用户表被大量表引用（会话、身份、key、进度、复习、授权、卡组、预设……），
// 单条 DELETE 在 Postgres 上会被外键拦下，在 SQLite 上则留下孤儿行，因此按依赖顺序
// 在一个事务里显式清理。审计行刻意保留：删除动作本身要留痕（谁删了谁）。
func (s *UserStore) DeleteUser(ctx context.Context, userID uint64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return DeleteUserTx(ctx, tx, userID)
	})
}

// userCleanup 是删除用户时的一条语句；args 个数与语句里的占位符一致，取值都是被删用户的 id。
type userCleanup struct {
	stmt string
	args int
}

// userCleanups 列出删除用户时要处理的全部行，分三类：
//   - 只属于该用户的行：删除；
//   - 别人的内容里记着「由该用户创建 / 修改」的可空列：置空，内容本身留给它的主人；
//   - 媒体：**不删**媒体行。媒体按内容去重，同一份字节可能被别人的 note 引用，删行会让别人的
//     图片失效、文件变成没有元数据的孤儿。只删该用户的「上传者」记录，没有引用的媒体由回收任务处理。
var userCleanups = []userCleanup{
	{"DELETE FROM sessions WHERE user_id = ?", 1},
	{"DELETE FROM identities WHERE user_id = ?", 1},
	{"DELETE FROM api_keys WHERE user_id = ?", 1},
	{"DELETE FROM card_states WHERE user_id = ?", 1},
	{"DELETE FROM reviews WHERE user_id = ?", 1},
	{"DELETE FROM deck_grants WHERE user_id = ?", 1},
	{"DELETE FROM deck_share_invites WHERE user_id = ? OR invited_by = ?", 2},
	{"DELETE FROM share_allow WHERE from_user_id = ? OR to_user_id = ?", 2},
	{"DELETE FROM share_links WHERE created_by = ?", 1},
	{"DELETE FROM user_totp WHERE user_id = ?", 1},
	{"DELETE FROM totp_recovery_codes WHERE user_id = ?", 1},
	{"DELETE FROM email_prefs WHERE user_id = ?", 1},
	{"DELETE FROM reminder_log WHERE user_id = ?", 1},
	{"DELETE FROM digest_log WHERE user_id = ?", 1},
	{"DELETE FROM action_tokens WHERE user_id = ?", 1},
	{"DELETE FROM login_fingerprints WHERE user_id = ?", 1},
	{"DELETE FROM media_uploaders WHERE user_id = ?", 1},
	{"UPDATE deck_grants SET created_by = NULL WHERE created_by = ?", 1},
	{"UPDATE notes SET created_by = NULL WHERE created_by = ?", 1},
	{"UPDATE invites SET created_by = NULL WHERE created_by = ?", 1},
	{"UPDATE invites SET used_by = NULL WHERE used_by = ?", 1},
	{"UPDATE media SET created_by = NULL WHERE created_by = ?", 1},
	{"UPDATE settings SET updated_by = NULL WHERE updated_by = ?", 1},
	{"UPDATE mail_templates SET updated_by = NULL WHERE updated_by = ?", 1},
}

// DeleteUserTx 在调用方给定的事务里执行 DeleteUser 的清理（管理员闸门在同一事务里先做判定）。
//
// 该用户名下的卡组走与 DeckStore.Delete 同一份级联（deleteDeckTx）：别人在这些卡组上的授权、
// 进度、待接受的邀请与媒体映射都一并清掉，而不是只删卡片与笔记、留下悬空的行。
func DeleteUserTx(ctx context.Context, tx *gorm.DB, userID uint64) error {
	tx = tx.WithContext(ctx)
	var deckIDs []uint64
	if err := tx.Model(&Deck{}).Where("owner_user_id = ?", userID).Pluck("id", &deckIDs).Error; err != nil {
		return fmt.Errorf("delete user data: list owned decks: %w", err)
	}
	for _, id := range deckIDs {
		if err := deleteDeckTx(tx, id); err != nil {
			return fmt.Errorf("delete user data: deck %d: %w", id, err)
		}
	}
	for _, c := range userCleanups {
		args := make([]any, c.args)
		for i := range args {
			args[i] = userID
		}
		if err := tx.Exec(c.stmt, args...).Error; err != nil {
			return fmt.Errorf("delete user data: %w", err)
		}
	}
	// 预设在卡组之后删（卡组引用预设），用户本体最后删。
	for _, stmt := range []string{"DELETE FROM presets WHERE owner_user_id = ?", "DELETE FROM users WHERE id = ?"} {
		if err := tx.Exec(stmt, userID).Error; err != nil {
			return fmt.Errorf("delete user data: %w", err)
		}
	}
	return nil
}
