package web

import (
	"context"
)

// 本文件集中「数字主键 -> 对外 id」的反查助手。
//
// 审计详情、邮件变量这类出口没有直接携带模型，只有主键；它们同样会进入响应（管理面板审计、
// 邮件正文），因此这里把主键翻成对外 id。查不到（行已删或未装配）一律返回空串，
// 绝不回退到数字主键——否则不透明 id 的边界会从这些旁路漏出去。

// userPublicID 反查用户的对外 id；查不到返回空串。
func (s *Server) userPublicID(ctx context.Context, id uint64) string {
	if s.users == nil || id == 0 {
		return ""
	}
	u, err := s.users.ByID(ctx, id)
	if err != nil || u == nil {
		return ""
	}
	return u.PublicID
}

// presetPublicID 反查预设的对外 id；查不到返回空串。
func (s *Server) presetPublicID(ctx context.Context, id uint64) string {
	if s.presets == nil || id == 0 {
		return ""
	}
	p, err := s.presets.ByID(ctx, id)
	if err != nil || p == nil {
		return ""
	}
	return p.PublicID
}

// deckPublicID 反查卡组的对外 id；查不到返回空串。
func (s *Server) deckPublicID(ctx context.Context, id uint64) string {
	if s.decks == nil || id == 0 {
		return ""
	}
	d, err := s.decks.ByID(ctx, id)
	if err != nil || d == nil {
		return ""
	}
	return d.PublicID
}

// looksLikePublicID 报告字符串是否形如对外 id（UUIDv7 的 36 字符文本：8-4-4-4-12 十六进制）。
// 它只判形状，不查库：形状不对的参数在查询之前就能拒绝，避免拿任意串去命中索引。
func looksLikePublicID(raw string) bool {
	if len(raw) != 36 {
		return false
	}
	for i, r := range raw {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}
