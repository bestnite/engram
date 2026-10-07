package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 分享会话授权（L3）：登录用户通过分享链接打开的卡组计入其会话级可见集合，
// 媒体读取（mediaReadableByUser 的第三支）据此放行。
//
// 为什么记在服务端会话而不是 cookie：会话是服务端状态（sessions 表），登出/改密码/禁用都会
// 作废它，授权必须随之消失；写进签名 cookie 反而无法即时撤销。
//
// 为什么键是 (session_id, deck_id) 而不是 (user_id, deck_id)：分享链接的可见性只属于「打开过
// 它的那个会话」，同一用户换一个会话（另一台设备、另一个浏览器）不该继承；会话隔离由主键天然强制。
//
// 授权行只镜像「这个会话打开过这个卡组」，本身不携带 token：读取时还会要求该卡组当前仍有一条
// 有效分享链接（见 shareSessionDeckIDsQuery），所以链接一旦被撤销或过期，读取立即失效
// （「可即时撤销」「撤销/过期即时生效」）。

// ShareSessionGrantMaxTTL 是分享会话授权在「链接与会话都没有给出过期时刻」时的兜底上限。
//
// 理由：任何授权都不该永不过期。这个上限等于会话默认 TTL（auth.DefaultSessionTTL = 30 天），
// 也就是单个会话最长能活的时长；即使两处时间戳都缺失（防御性分支，正常路径走不到），
// 授权也不可能比创建它的会话活得更久。
const ShareSessionGrantMaxTTL = 30 * 24 * time.Hour

// ShareSessionStore 封装 share_session_decks：登记授权、过期回收。
type ShareSessionStore struct {
	db *gorm.DB
}

// NewShareSessionStore 构造分享会话授权存储。
func NewShareSessionStore(db *gorm.DB) *ShareSessionStore { return &ShareSessionStore{db: db} }

// Grant 登记 sessionID 通过分享链接打开过 deckID；过期时刻取链接过期与会话过期的较早者。
//
// 写入前顺手清掉已过期的行：过期的授权再也无法放行任何请求，是纯垃圾；清理挂在这个相对冷、
// 但一定会被触发的写入点上，避免再引入一个「永远不会跑」的后台任务。
func (s *ShareSessionStore) Grant(ctx context.Context, sessionID string, deckID uint64, linkExpiresAt *time.Time, sessionExpiresAt time.Time) error {
	if s == nil || s.db == nil || sessionID == "" || deckID == 0 {
		return nil
	}
	now := time.Now().UTC()
	if _, err := s.RemoveExpired(ctx, now); err != nil {
		return err
	}
	expiry := ShareSessionGrantExpiry(linkExpiresAt, sessionExpiresAt, now)
	row := ShareSessionDeck{SessionID: sessionID, DeckID: deckID, ExpiresAt: expiry}
	// 重复打开同一链接是常态（刷新页面）：按主键 upsert，不建第二行、不报错。
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}, {Name: "deck_id"}},
		DoUpdates: clause.Assignments(map[string]any{"expires_at": expiry}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("store: grant share session deck: %w", err)
	}
	return nil
}

// RemoveExpired 删除全部已过期的授权行，返回删除条数。
func (s *ShareSessionStore) RemoveExpired(ctx context.Context, now time.Time) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	res := s.db.WithContext(ctx).Where("expires_at <= ?", now).Delete(&ShareSessionDeck{})
	if res.Error != nil {
		return 0, fmt.Errorf("store: remove expired share session decks: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// ShareSessionGrantExpiry 计算一条分享授权行的过期时刻。
//
// 规则：链接过期与会话过期取较早者——任一失效，授权都失效。
// 两者都可能为空/为零（正常路径下会话过期总是存在）：此时用调用时刻加 ShareSessionGrantMaxTTL
// 兜底，保证不会写出永不过期的行。
func ShareSessionGrantExpiry(linkExpiresAt *time.Time, sessionExpiresAt, now time.Time) time.Time {
	expiry := now.Add(ShareSessionGrantMaxTTL)
	if linkExpiresAt != nil && !linkExpiresAt.IsZero() && linkExpiresAt.Before(expiry) {
		expiry = *linkExpiresAt
	}
	if !sessionExpiresAt.IsZero() && sessionExpiresAt.Before(expiry) {
		expiry = sessionExpiresAt
	}
	return expiry
}

// shareSessionDeckIDsQuery 返回「某会话通过分享链接拿到未过期访问权」的卡组 id 子查询。
//
// 两个条件缺一不可：
//   - 该会话在该卡组上有一条未过期的授权行（由 Grant 写入）；
//   - 该卡组当前仍有一条有效分享链接（未撤销、未过期）。
//
// 第二条让「撤销/过期即时生效」成立：授权行不携带 token，只镜像「打开过这个卡组」；
// 若该卡组不再有活着的链接，读取立即失效，即使该会话之前成功打开过。
func shareSessionDeckIDsQuery(db *gorm.DB, sessionID string, now time.Time) *gorm.DB {
	active := db.Model(&ShareLink{}).Select("deck_id").
		Where("revoked_at IS NULL").
		Where("(expires_at IS NULL OR expires_at > ?)", now)
	return db.Model(&ShareSessionDeck{}).Select("deck_id").
		Where("session_id = ? AND expires_at > ?", sessionID, now).
		Where("deck_id IN (?)", active)
}

// removeShareGrantsForSessionTx 删除一个会话的全部授权行（会话作废时调用，见 session.go）。
func removeShareGrantsForSessionTx(ctx context.Context, tx *gorm.DB, sessionID string) error {
	if tx == nil || sessionID == "" {
		return nil
	}
	if err := tx.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&ShareSessionDeck{}).Error; err != nil {
		return fmt.Errorf("store: remove share grants for session: %w", err)
	}
	return nil
}

// removeShareGrantsForUserTx 删除某用户全部会话的授权行；keepSessionID 非空时保留该会话的行。
func removeShareGrantsForUserTx(ctx context.Context, tx *gorm.DB, userID uint64, keepSessionID string) error {
	if tx == nil || userID == 0 {
		return nil
	}
	sessions := tx.Model(&Session{}).Select("id").Where("user_id = ?", userID)
	q := tx.WithContext(ctx).Where("session_id IN (?)", sessions)
	if keepSessionID != "" {
		q = q.Where("session_id <> ?", keepSessionID)
	}
	if err := q.Delete(&ShareSessionDeck{}).Error; err != nil {
		return fmt.Errorf("store: remove share grants for user sessions: %w", err)
	}
	return nil
}
