package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 媒体库列表（分页）——DESIGN.md §6.3 的读取判定用于列表。
//
// 为什么列表要有上限：它是给「挑一张图插入卡片」的场景用的，一页取全库既慢又把内存
// 当缓冲用。上限写死在这里，让 REST、web 选择器与将来的调用方共用同一条口径。
const (
	// DefaultMediaPageSize 是未指定 limit 时的默认页大小。
	DefaultMediaPageSize = 24
	// MaxMediaPageSize 是单页允许的最大条数；超过它一律收敛到它（REST 层对超限值返回 400）。
	MaxMediaPageSize = 100
)

// ErrInvalidMediaCursor 表示游标串无法解码。REST 层据此返回 400，而不是静默从头开始
// ——静默重开会把「翻页」变成「反复看第一页」，调用方无从察觉。
var ErrInvalidMediaCursor = errors.New("store: invalid media cursor")

// mediaBaseColumns 是并集两支共同选择的列；顺序一致才能 UNION。
const mediaBaseColumns = "media.sha256, media.rel_path, media.mime, media.bytes, media.width, media.height, media.created_by, media.created_at"

// readableMediaUnionQuery 返回「某用户可读媒体」的并集子查询（两个来源 UNION 去重）：
//  1. media_uploaders 里存在指向 userID 的记录——他提供过这份字节（去重命中也算，不可撤销）；
//  2. media_notes JOIN notes（未软删）且 note 所属卡组落在 visibleDeckIDsQuery 范围内。
//
// 它是「可读媒体集合」的唯一来源，读取判定（mediaReadableByUser 的前两支）与列表共用它，
// 避免两处各写一套 SQL 而漂移。第三支（按分享会话读取）只对 /media 代理路径开放，
// 选择器与 REST 列表的使用者都是登录后的本人，不含它。
//
// UNION（而非 UNION ALL）确保同一份字节两路命中时只出现一次。
func readableMediaUnionQuery(db *gorm.DB, userID uint64) *gorm.DB {
	uploaded := db.Model(&Media{}).Select(mediaBaseColumns).
		Joins("JOIN media_uploaders u ON u.media_sha = media.sha256").
		Where("u.user_id = ?", userID)
	referenced := db.Model(&Media{}).Select(mediaBaseColumns).
		Joins("JOIN media_notes mn ON mn.media_sha = media.sha256").
		Joins("JOIN notes n ON n.id = mn.note_id AND n.deleted_at IS NULL").
		Where("n.deck_id IN (?)", visibleDeckIDsQuery(db, userID))
	return db.Raw("? UNION ?", uploaded, referenced)
}

// ListReadableMedia 按 keyset 返回 userID 可读的媒体，排序 media.created_at DESC,
// media.sha256 DESC；返回本页行与下一页游标（空串表示到底）。
//
// 排序键包含主键 sha256，所以全序唯一：同一游标重复请求得到同一页，既不重复也不遗漏。
// 不变量：limit 收敛到 [1, MaxMediaPageSize]（非法值由调用方按 400 处理，这里只做安全网）。
func ListReadableMedia(ctx context.Context, db *gorm.DB, userID uint64, limit int, cursor string) ([]Media, string, error) {
	if db == nil || userID == 0 {
		return nil, "", nil
	}
	if limit < 1 {
		limit = DefaultMediaPageSize
	}
	if limit > MaxMediaPageSize {
		limit = MaxMediaPageSize
	}
	q := db.WithContext(ctx).Table("(?) AS m", readableMediaUnionQuery(db, userID))
	if cursor != "" {
		at, sha, err := decodeMediaCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		// 严格小于上一页末项：先比 created_at，同刻再比 sha256。
		q = q.Where("m.created_at < ? OR (m.created_at = ? AND m.sha256 < ?)", at, at, sha)
	}
	// 多取一行判断是否还有下一页，返回时不带它。
	var rows []Media
	if err := q.Order("m.created_at DESC, m.sha256 DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", fmt.Errorf("store: list readable media: %w", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = encodeMediaCursor(rows[len(rows)-1])
	}
	return rows, next, nil
}

// encodeMediaCursor 把一行的排序键编码成不透明游标：base64url(created_at_unix_nano:sha256)。
func encodeMediaCursor(m Media) string {
	raw := strconv.FormatInt(m.CreatedAt.UTC().UnixNano(), 10) + ":" + m.Sha256
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeMediaCursor 解析游标；任何形状不对的输入都返回 ErrInvalidMediaCursor。
func decodeMediaCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", ErrInvalidMediaCursor
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return time.Time{}, "", ErrInvalidMediaCursor
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, "", ErrInvalidMediaCursor
	}
	if !isMediaSha(parts[1]) {
		return time.Time{}, "", ErrInvalidMediaCursor
	}
	return time.Unix(0, nanos).UTC(), parts[1], nil
}

// isMediaSha 校验 64 位小写十六进制，与 web 层 /media/:sha 的形状要求一致。
func isMediaSha(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
