package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// 管理面板的用户管理（DESIGN.md §8.4；AGENTS.md §5 M6-2）所需的用户存储扩展。
//
// 放在独立文件而不是塞进 user.go / models.go：这两处是其它里程碑的单写者热点，
// 并行工作时尽量不动它们。这里只新增方法，不改动既有行为。

// AdminListUsersPageSize 是管理面板用户列表的每页条数；同时作为一次渲染的上限，
// 避免一次把全库用户全画出来（M6-2 的「分页或上限保护」）。
const AdminListUsersPageSize = 50

// ListForAdmin 按搜索词分页列出用户。query 为空表示不筛选；命中用户名、邮箱或显示名。
// 搜索一律 LOWER(...) 比较：SQLite 的 LIKE 对 ASCII 默认不区分大小写，而 PostgreSQL 区分，
// 统一转小写才能让两库行为一致（DESIGN.md §2.3 的双库约定）。
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
		stmts := []string{
			// 直接按 user_id 引用的表。
			"DELETE FROM sessions WHERE user_id = ?",
			"DELETE FROM identities WHERE user_id = ?",
			"DELETE FROM api_keys WHERE user_id = ?",
			"DELETE FROM card_states WHERE user_id = ?",
			"DELETE FROM reviews WHERE user_id = ?",
			"DELETE FROM deck_grants WHERE user_id = ?",
			// 授权/分享链接的 created_by 是可空引用，置空即可，不必删别人的卡组授权。
			"UPDATE deck_grants SET created_by = NULL WHERE created_by = ?",
			"DELETE FROM share_links WHERE created_by = ?",
			"DELETE FROM media WHERE created_by = ?",
			// 其拥有的卡组：先卡片、再笔记、再卡组，最后预设（deck 引用 preset）。
			`DELETE FROM cards WHERE note_id IN (
				SELECT id FROM notes WHERE deck_id IN (SELECT id FROM decks WHERE owner_user_id = ?))`,
			`DELETE FROM notes WHERE deck_id IN (SELECT id FROM decks WHERE owner_user_id = ?)`,
			"DELETE FROM decks WHERE owner_user_id = ?",
			"DELETE FROM presets WHERE owner_user_id = ?",
			// 最后删用户本体。
			"DELETE FROM users WHERE id = ?",
		}
		for _, stmt := range stmts {
			if err := tx.Exec(stmt, userID).Error; err != nil {
				return fmt.Errorf("delete user data: %w", err)
			}
		}
		return nil
	})
}
