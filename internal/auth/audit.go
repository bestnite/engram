package auth

import (
	"context"
	"errors"
	"log/slog"

	"git.nite07.com/nite/engram/internal/store"
)

// Auditor is a thin wrapper around store.AuditStore: business code (web handlers and later API/MCP) depends only on it to write audit entries,
// and never touches the concrete storage types directly (AGENTS.md §2.4: keep dependencies minimal on the consumer side).
//
// Audit is "best effort": on a write failure the caller should log in English and carry on with the business, and must not roll back
// a change that already happened just because the audit table failed — but it must not silently swallow it either, or audit rows go quietly missing.
type Auditor struct {
	store  *store.AuditStore
	logger *slog.Logger
}

// NewAuditor constructs the audit facade; an empty store returns an English error so audit entries are not silently dropped.
// logger receives write failures; nil means slog.Default().
func NewAuditor(audit *store.AuditStore, logger *slog.Logger) (*Auditor, error) {
	if audit == nil {
		return nil, errors.New("auth: audit store is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Auditor{store: audit, logger: logger}, nil
}

// Record writes one audit entry; detail is serialized to JSON by the store layer. It is the single place the best-effort
// rule above is applied: a failed write is logged in English and never returned, so no caller can roll back or fail its
// change because of the audit table, and none has to remember to log. A nil Auditor (audit not wired) records nothing.
func (a *Auditor) Record(ctx context.Context, e store.AuditEntry) {
	if a == nil {
		return
	}
	if err := a.store.Write(ctx, e); err != nil {
		a.logger.Error("write audit log failed", "action", e.Action, "error", err)
	}
}
