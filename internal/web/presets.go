package web

import (
	"context"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/jobs"
	"git.nite07.com/nite/engram/internal/store"
)

// registerPresetRoutes 挂载调度预设页入口。
//
// SSR 页面层已删除：GET /presets 只发应用壳，预设的列表/新建/编辑/优化/回退全部走
// /api/v1/presets* 的 JSON 端点（presets_api.go，与 SSR 页面同一批 store/jobs 方法）。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerPresetRoutes(router *gin.Engine) {
	if s.sessions == nil || s.presets == nil || s.jobRunner == nil {
		return
	}
	router.GET("/presets", s.presetRoute)
	// SPA 预设接口：同源 JSON 读写走 /api/v1/presets*。
	s.registerPresetAPIRoutes(router)
}

// optimizeJobFor 按对外 id 读取优化作业，并校验它确实属于该预设（kind 与 target 都对）。
// 任何不匹配都返回 nil：状态端点只允许看到本预设自己的作业。
func (s *Server) optimizeJobFor(ctx context.Context, presetID uint64, raw string) *store.Job {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	job, err := s.jobStore.ByPublicID(ctx, raw)
	if err != nil {
		return nil
	}
	if job.Kind != jobs.KindOptimize || job.TargetID == nil || *job.TargetID != presetID {
		return nil
	}
	return job
}

// validStepSpec 判断学习/再学习步骤串是否可解析；空串合法（关闭步骤）。
// 文法与 internal/schedule 的 parseSteps 一致（逗号分隔、s/m/h/d 后缀、缺省分钟），
// 但额外要求每个步骤为正：调度器会把 "-5m" 这类负数当合法输入，验收要求在此拒绝。
// 这里只判断「可解析」，不复制调度逻辑——调度器读库时仍走自己的 parseSteps。
func validStepSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return true
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := len(part)
		for idx > 0 {
			ch := part[idx-1]
			if (ch >= '0' && ch <= '9') || ch == '.' {
				break
			}
			idx--
		}
		num, unit := part[:idx], strings.ToLower(part[idx:])
		value, err := strconv.ParseFloat(num, 64)
		if err != nil || value <= 0 {
			return false
		}
		switch unit {
		case "", "m", "min", "s", "sec", "h", "hr", "d":
		default:
			return false
		}
	}
	return true
}

// sessionCSRF 取当前会话绑定的 CSRF token；SPA 页面外壳与 JSON 写路径共用同一份会话令牌。
func sessionCSRF(c *gin.Context) string {
	if sess, ok := auth.CurrentSession(c); ok {
		return sess.CSRFToken
	}
	return ""
}
