package web

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 管理面板的审计检索页（DESIGN.md §8.4；ROADMAP.md M6-7）。
//
// 安全与规模底线：
//   - 路由在 adminRoutes() 清单里，非 admin 一律 403（守卫先于 handler）。
//   - 纯读页，无写操作，因此不需要 CSRF。
//   - 列表一律分页（每页 adminAuditPageSize），store 层另有硬上限；审计表只增不减，
//     绝不允许一次拉全表。
//
// 时间口径：库里 created_at 统一 UTC；页面按当前管理员的时区展示，
// 日期范围过滤把用户时区的自然日边界换算成 UTC 瞬时值再交给 store。

// adminAuditPageSize 是审计列表每页行数。
const adminAuditPageSize = 50

// auditLocation 返回当前管理员用于展示与日期换算的时区；未设置或无法加载时回退 UTC。
func auditLocation(u *store.User) *time.Location {
	if u != nil && u.Timezone != "" {
		if loc, err := time.LoadLocation(u.Timezone); err == nil {
			return loc
		}
	}
	return time.UTC
}

// parseAuditDay 把 YYYY-MM-DD 按给定时区解析成该日零点；格式非法时 ok=false。
func parseAuditDay(raw string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
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

// auditPageHref 拼审计分页链接，保留当前全部过滤条件。
func (s *Server) auditPageHref(c *gin.Context, page int) string {
	q := url.Values{}
	for _, k := range []string{"user", "action", "target_type", "target_id", "from", "to"} {
		if v := strings.TrimSpace(c.Query(k)); v != "" {
			q.Set(k, v)
		}
	}
	q.Set("page", strconv.Itoa(page))
	return "/admin/audit?" + q.Encode()
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

// adminAuditPage 渲染审计检索页：按用户 / 动作 / 目标 / 日期范围过滤，分页展示。
func (s *Server) adminAuditPage(c *gin.Context) {
	loc, ok := s.localizer(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	actor, _ := auth.CurrentUser(c)
	userLoc := auditLocation(actor)

	page := pageParam(c.Query("page"))
	userRaw := strings.TrimSpace(c.Query("user"))
	action := strings.TrimSpace(c.Query("action"))
	targetType := strings.TrimSpace(c.Query("target_type"))
	targetIDRaw := strings.TrimSpace(c.Query("target_id"))
	fromRaw := strings.TrimSpace(c.Query("from"))
	toRaw := strings.TrimSpace(c.Query("to"))

	auditStore := store.NewAuditStore(s.db)
	filter := store.AuditFilter{
		Action:     action,
		TargetType: targetType,
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
				notice = loc.T("admin.audit.notice.user_not_found")
			}
		default:
			if id, err := strconv.ParseUint(userRaw, 10, 64); err == nil {
				filter.UserID = id
			} else {
				notice = loc.T("admin.audit.notice.user_not_found")
			}
		}
	}
	if targetIDRaw != "" {
		if id, err := strconv.ParseUint(targetIDRaw, 10, 64); err == nil {
			filter.TargetID = id
		} else {
			notice = loc.T("admin.audit.notice.invalid_target")
		}
	}
	if fromRaw != "" {
		if t, ok := parseAuditDay(fromRaw, userLoc); ok {
			filter.From = t
		} else {
			notice = loc.T("admin.audit.notice.invalid_date")
		}
	}
	if toRaw != "" {
		if t, ok := parseAuditDay(toRaw, userLoc); ok {
			// to 是「含当日」的上界，换算成次日零点（不含），保证整日都在范围内。
			filter.To = t.AddDate(0, 0, 1)
		} else {
			notice = loc.T("admin.audit.notice.invalid_date")
		}
	}

	list, total, err := auditStore.Search(ctx, filter)
	if err != nil {
		s.logger.Error("admin: search audit log failed", "error", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	pages := int((total + int64(adminAuditPageSize) - 1) / int64(adminAuditPageSize))
	if pages < 1 {
		pages = 1
	}

	// 用户名解析：只解析当前页出现的用户 id（最多一页），避免整表 join。
	names := s.usernamesFor(ctx, list)
	rows := make([]views.AdminAuditRow, 0, len(list))
	for i := range list {
		row := list[i]
		display := views.AdminAuditRow{
			Time:   row.CreatedAt.In(userLoc).Format("2006-01-02 15:04:05"),
			Action: row.Action,
		}
		if row.UserID != nil {
			if name, ok := names[*row.UserID]; ok {
				// 显示名不加 # 前缀，与「系统」区分。
				display.User = name
			} else {
				display.User = "#" + strconv.FormatUint(*row.UserID, 10)
			}
		} else {
			display.User = loc.T("admin.audit.user_system")
		}
		if row.TargetType != nil && *row.TargetType != "" {
			display.Target = *row.TargetType
			if row.TargetID != nil {
				display.Target += "#" + strconv.FormatUint(*row.TargetID, 10)
			}
		} else {
			display.Target = loc.T("admin.audit.target_empty")
		}
		if row.DetailJSON != nil {
			display.Detail = *row.DetailJSON
		} else {
			display.Detail = loc.T("admin.audit.detail_empty")
		}
		rows = append(rows, display)
	}

	// 动作下拉：库中实际出现过的动作 + 一个「全部」项。
	actionOptions := make([]views.AdminOption, 0, 1)
	actionOptions = append(actionOptions, views.AdminOption{Label: loc.T("admin.audit.filter.any")})
	if actions, err := auditStore.DistinctActions(ctx); err == nil {
		for _, a := range actions {
			actionOptions = append(actionOptions, views.AdminOption{
				Value: a, Label: a, Selected: a == action,
			})
		}
	}

	prevHref, nextHref := "", ""
	if page > 1 {
		prevHref = s.auditPageHref(c, page-1)
	}
	if page < pages {
		nextHref = s.auditPageHref(c, page+1)
	}

	renderHTML(c, views.AdminPage(views.AdminPageData{
		Layout:     s.adminLayout(c, loc, "admin.audit.title", "/admin/audit"),
		Heading:    loc.T("admin.audit.heading"),
		Intro:      loc.T("admin.audit.intro"),
		NavHeading: loc.T("admin.nav.heading"),
		Nav:        s.adminNav(loc, "/admin/audit"),
		Notice:     notice,

		AuditPage: true,
		AuditRows: rows,

		AuditFilterHeading: loc.T("admin.audit.filter.heading"),
		AuditUserLabel:     loc.T("admin.audit.filter.user"),
		AuditUserValue:     userRaw,
		AuditActionLabel:   loc.T("admin.audit.filter.action"),
		AuditActions:       actionOptions,
		AuditTargetLabel:   loc.T("admin.audit.filter.target_type"),
		AuditTargetValue:   targetType,
		AuditTargetIDLabel: loc.T("admin.audit.filter.target_id"),
		AuditTargetIDValue: targetIDRaw,
		AuditFromLabel:     loc.T("admin.audit.filter.from"),
		AuditFromValue:     fromRaw,
		AuditToLabel:       loc.T("admin.audit.filter.to"),
		AuditToValue:       toRaw,
		AuditFilterSubmit:  loc.T("admin.audit.filter.submit"),
		AuditFilterClear:   loc.T("admin.audit.filter.clear"),
		AuditRangeHint:     loc.T("admin.audit.filter.range_hint"),
		AuditClearHref:     "/admin/audit",

		ColAuditTime:   loc.T("admin.audit.col.time"),
		ColAuditUser:   loc.T("admin.audit.col.user"),
		ColAuditAction: loc.T("admin.audit.col.action"),
		ColAuditTarget: loc.T("admin.audit.col.target"),
		ColAuditDetail: loc.T("admin.audit.col.detail"),

		AuditTotalLabel: loc.T("admin.audit.total_label"),

		Page: page, Pages: pages, Total: total,
		PrevHref: prevHref, NextHref: nextHref,
		PrevLabel: loc.T("admin.jobs.prev"), NextLabel: loc.T("admin.jobs.next"),
		EmptyLabel: loc.T("admin.audit.empty"),
	}))
}
