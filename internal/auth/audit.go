package auth

import (
	"context"
	"errors"
	"fmt"

	"git.nite07.com/nite/engram/internal/store"
)

// Auditor is a thin wrapper around store.AuditStore: business code (web handlers and later API/MCP) depends only on it to write audit entries,
// and never touches the concrete storage types directly (AGENTS.md §2.4: keep dependencies minimal on the consumer side).
//
// Audit is "best effort": on a write failure the caller should log in English and carry on with the business, and must not roll back
// a change that already happened just because the audit table failed — but it must not silently swallow it either, or audit rows go quietly missing.
type Auditor struct {
	store *store.AuditStore
}

// NewAuditor constructs the audit facade; an empty store returns an English error so audit entries are not silently dropped.
func NewAuditor(audit *store.AuditStore) (*Auditor, error) {
	if audit == nil {
		return nil, errors.New("auth: audit store is required")
	}
	return &Auditor{store: audit}, nil
}

// Record writes one audit entry; detail is serialized to JSON by the store layer.
func (a *Auditor) Record(ctx context.Context, e store.AuditEntry) error {
	if err := a.store.Write(ctx, e); err != nil {
		return fmt.Errorf("record audit: %w", err)
	}
	return nil
}
