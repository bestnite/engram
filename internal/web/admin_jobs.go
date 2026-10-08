package web

import (
	"time"
)

// 管理面板作业页的支撑函数。
//
// SSR 作业页删除后，作业列表与取消在 /api/v1/admin/jobs* 的 JSON 端点上
// （admin_jobs_api.go）；这里只保留仍被复用的时间格式化与分页大小。

// adminJobsPageSize 是作业列表每页行数。
const adminJobsPageSize = 20

// formatJobTime 格式化可空时间戳；nil 返回空串。
func formatJobTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}
