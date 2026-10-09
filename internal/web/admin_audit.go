package web

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/store"
)

// 管理面板审计检索的查询解析。
//
// 时间口径：库里 created_at 统一 UTC；页面按当前管理员的时区展示，
// 日期范围过滤把用户时区的自然日边界换算成 UTC 瞬时值再交给 store。
// SSR 审计页删除后，检索在 /api/v1/admin/audit 的 JSON 端点上（admin_read.go），
// 过滤解析与分页大小由这里共享。

// adminAuditPageSize 是审计列表每页行数。
const adminAuditPageSize = 50

// auditLocation 返回当前管理员用于展示与日期换算的时区；回退规则见 store.LoadLocation。
func auditLocation(u *store.User) *time.Location {
	if u == nil {
		return time.UTC
	}
	return store.LoadLocation(u.Timezone)
}

// parseAuditDay 把 YYYY-MM-DD 按给定时区解析成该日零点；格式非法时 ok=false。
func parseAuditDay(raw string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// auditFilterFromQuery 把审计检索的查询参数解析成 store.AuditFilter（JSON 端点与页面共用）。
//   - user 支持对外 id 或用户名（两者都不是纯数字）；查不到的用户命中空集而不是退化成「不过滤」。
//   - target_id 是对外 id：按 target_type 反查数字主键；日期格式非法时返回 notice 码（稳定英文），
//     由调用方映射文案。
//
// 返回的第二个值是原样的 user 查询串，第三个值是 notice 码（空串表示无提示）。
func (s *Server) auditFilterFromQuery(c *gin.Context, userLoc *time.Location, page int) (store.AuditFilter, string, string) {
	ctx := c.Request.Context()
	userRaw := strings.TrimSpace(c.Query("user"))
	filter := store.AuditFilter{
		Action:     strings.TrimSpace(c.Query("action")),
		TargetType: strings.TrimSpace(c.Query("target_type")),
		Limit:      adminAuditPageSize,
		Offset:     (page - 1) * adminAuditPageSize,
	}
	notice := ""
	if userRaw != "" {
		switch {
		case s.users != nil && !isNumeric(userRaw):
			// 管理面板显示的是对外 id，所以先按对外 id 查，再退回按用户名查。
			if u, err := s.users.ByPublicID(ctx, userRaw); err == nil {
				filter.UserID = u.ID
			} else if u, err := s.users.ByUsername(ctx, userRaw); err == nil {
				filter.UserID = u.ID
			} else {
				// 用户不存在：不能静默退化成「不过滤」（那会把「查无此人」误报成全量结果）。
				// 用一个不可能存在的 id（int64 上界）命中空集，避免绑定 uint64 最高位。
				filter.UserID = uint64(math.MaxInt64)
				notice = "user_not_found"
			}
		default:
			if id, err := strconv.ParseUint(userRaw, 10, 64); err == nil {
				filter.UserID = id
			} else {
				notice = "user_not_found"
			}
		}
	}
	if targetIDRaw := strings.TrimSpace(c.Query("target_id")); targetIDRaw != "" {
		// 目标 id 对外是对外 id：按 target_type 反查数字主键；同时兼容历史的全数字输入。
		if id, ok := s.auditTargetNumericID(ctx, filter.TargetType, targetIDRaw); ok {
			filter.TargetID = id
		} else if id, err := strconv.ParseUint(targetIDRaw, 10, 64); err == nil {
			filter.TargetID = id
		} else {
			// 无法解析时不能静默退化成「不过滤」（那会把「查不到」误报成全量结果）：
			// 用一个不可能存在的 id 命中空集，与 user 过滤同一口径。
			filter.TargetID = uint64(math.MaxInt64)
			notice = "invalid_target"
		}
	}
	if fromRaw := strings.TrimSpace(c.Query("from")); fromRaw != "" {
		if t, ok := parseAuditDay(fromRaw, userLoc); ok {
			filter.From = t
		} else {
			notice = "invalid_date"
		}
	}
	if toRaw := strings.TrimSpace(c.Query("to")); toRaw != "" {
		if t, ok := parseAuditDay(toRaw, userLoc); ok {
			// to 是「含当日」的上界，换算成次日零点（不含），保证整日都在范围内。
			filter.To = t.AddDate(0, 0, 1)
		} else {
			notice = "invalid_date"
		}
	}
	return filter, userRaw, notice
}

// isNumeric 判断字符串是否为纯数字（区分「按 id」与「按用户名」两种输入）。
func isNumeric(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// usersForAudit 批量取当前页涉及的用户，供审计行的操作者对外 id 与用户名使用；
// 查不到的用户不写入，调用方把该行的 user_id 渲染成 null，绝不回退到数字主键。
func (s *Server) usersForAudit(ctx context.Context, rows []store.AuditLog) map[uint64]*store.User {
	users := make(map[uint64]*store.User)
	if s.users == nil {
		return users
	}
	for i := range rows {
		id := rows[i].UserID
		if id == nil {
			continue
		}
		if _, seen := users[*id]; seen {
			continue
		}
		if u, err := s.users.ByID(ctx, *id); err == nil {
			users[*id] = u
		}
	}
	return users
}

// auditTargetPublicID 把审计行的数字目标 id 换成对外 id；无法解析（类型未知或目标行已删）
// 时返回 nil，调用方据此把 target.id 渲染为 null，绝不回退到数字主键。
func (s *Server) auditTargetPublicID(ctx context.Context, targetType string, id *uint64) *string {
	if id == nil {
		return nil
	}
	var publicID string
	switch targetType {
	case "user":
		publicID = s.userPublicID(ctx, *id)
	case "deck":
		publicID = s.deckPublicID(ctx, *id)
	case "preset":
		publicID = s.presetPublicID(ctx, *id)
	case "job":
		if s.jobStore != nil {
			if job, err := s.jobStore.ByID(ctx, *id); err == nil {
				publicID = job.PublicID
			}
		}
	case "invite":
		// invites 没有按主键取行的入口（只按 token 或对外 id），直接取 public_id。
		_ = s.db.WithContext(ctx).Table("invites").Select("public_id").Where("id = ?", *id).Scan(&publicID).Error
	case "api_key":
		if k, err := store.NewAPIKeyStore(s.db).ByID(ctx, *id); err == nil {
			publicID = k.PublicID
		}
	case "identity":
		if s.identities != nil {
			if ident, err := s.identities.ByID(ctx, *id); err == nil {
				publicID = ident.PublicID
			}
		}
	case "card":
		if s.cards != nil {
			if c, err := s.cards.ByID(ctx, *id); err == nil {
				publicID = c.PublicID
			}
		}
	}
	if publicID == "" {
		return nil
	}
	return &publicID
}

// auditTargetNumericID 是 auditTargetPublicID 的反向映射：按目标类型把对外 id 换回数字主键，
// 供审计检索的 target_id 过滤使用（列表显示对外 id，过滤也必须认它）。
// 类型未知、目标行已删或缺少对应存储时返回 false，调用方据此给提示而不是静默放行。
func (s *Server) auditTargetNumericID(ctx context.Context, targetType, publicID string) (uint64, bool) {
	switch targetType {
	case "user":
		if s.users != nil {
			if u, err := s.users.ByPublicID(ctx, publicID); err == nil {
				return u.ID, true
			}
		}
	case "deck":
		if s.decks != nil {
			if d, err := s.decks.ByPublicID(ctx, publicID); err == nil {
				return d.ID, true
			}
		}
	case "preset":
		if s.presets != nil {
			if p, err := s.presets.ByPublicID(ctx, publicID); err == nil {
				return p.ID, true
			}
		}
	case "job":
		if s.jobStore != nil {
			if j, err := s.jobStore.ByPublicID(ctx, publicID); err == nil {
				return j.ID, true
			}
		}
	case "invite":
		if s.invites != nil {
			if inv, err := s.invites.ByPublicID(ctx, publicID); err == nil {
				return inv.ID, true
			}
		}
	case "api_key":
		if k, err := store.NewAPIKeyStore(s.db).ByPublicID(ctx, publicID); err == nil {
			return k.ID, true
		}
	case "identity":
		if s.identities != nil {
			if ident, err := s.identities.ByPublicID(ctx, publicID); err == nil {
				return ident.ID, true
			}
		}
	case "card":
		if s.cards != nil {
			if c, err := s.cards.ByPublicID(ctx, publicID); err == nil {
				return c.ID, true
			}
		}
	}
	return 0, false
}
