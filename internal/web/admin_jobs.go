package web

import (
	"time"
)

// Support functions for the admin panel jobs page.
//
// After the SSR jobs page was removed, the job list and cancellation live on the /api/v1/admin/jobs* JSON endpoints
// (admin_jobs_api.go); only the time formatting and page size that are still reused are kept here.

// adminJobsPageSize is the number of rows per page in the job list.
const adminJobsPageSize = 20

// formatJobTime formats a nullable timestamp; returns an empty string for nil.
func formatJobTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}
