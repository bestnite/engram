package auth

import (
	"context"
	"errors"
	"fmt"

	"example.com/engram/internal/store"
)

// Auditor 是 store.AuditStore 的薄封装：业务代码（web handler、后续 API/MCP）只依赖它写审计，
// 不直接触碰存储层的具体类型（AGENTS.md §2.4：最小依赖放在消费者侧）。
//
// 审计是“尽力而为”：写失败时调用方应记英文日志并继续业务，不能因为审计表故障而回滚
// 已经发生的变更 —— 但也不能静默吞掉，否则审计会悄悄缺行。
type Auditor struct {
	store *store.AuditStore
}

// NewAuditor 构造审计门面；存储为空时返回英文错误，避免审计被静默丢弃。
func NewAuditor(audit *store.AuditStore) (*Auditor, error) {
	if audit == nil {
		return nil, errors.New("auth: audit store is required")
	}
	return &Auditor{store: audit}, nil
}

// Record 写一条审计；detail 由 store 层序列化为 JSON。
func (a *Auditor) Record(ctx context.Context, e store.AuditEntry) error {
	if err := a.store.Write(ctx, e); err != nil {
		return fmt.Errorf("record audit: %w", err)
	}
	return nil
}
