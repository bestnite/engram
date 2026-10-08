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

// 管理面板审计检索的查询解析（ROADMAP.md M6-7）。
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
//   - user 支持用户名（非纯数字）或 id；查不到的用户命中空集而不是退化成「不过滤」。
//   - target_id 非数字、日期格式非法时返回 notice 码（稳定英文），由调用方映射文案。
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
			if u, err := s.users.ByUsername(ctx, userRaw); err == nil {
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
		if id, err := strconv.ParseUint(targetIDRaw, 10, 64); err == nil {
			filter.TargetID = id
		} else {
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

// usernamesFor 批量取当前页涉及的用户名；查不到的用户不写入，由调用方回退到 #id。
func (s *Server) usernamesFor(ctx context.Context, rows []store.AuditLog) map[uint64]string {
	names := make(map[uint64]string)
	if s.users == nil {
		return names
	}
	for i := range rows {
		id := rows[i].UserID
		if id == nil {
			continue
		}
		if _, seen := names[*id]; seen {
			continue
		}
		if u, err := s.users.ByID(ctx, *id); err == nil {
			names[*id] = u.Username
		}
	}
	return names
}
