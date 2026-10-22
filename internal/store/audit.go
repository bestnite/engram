package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Audit action constants are defined in one place. Values are always English, dot-separated and stable:
// action is the key for audit search, and bare strings at call sites would make history unaggregatable by action.
// Add a constant here when adding a write operation; do not start a second naming scheme.
const (
	ActionUserCreate         = "user.create"
	ActionUserLoginSucceeded = "user.login_succeeded"
	ActionUserLoginFailed    = "user.login_failed"
	ActionUserLogout         = "user.logout"
	// write operations on the card list / edit page.
	ActionNoteUpdate = "note.update"
	ActionNoteDelete = "note.delete"
	ActionNoteTagAdd = "note.tag_add"
	// Bulk tag actions: remove tags, and set tags wholesale. Side by side with ActionNoteTagAdd,
	// the three action names are each stable so history can be aggregated by action.
	ActionNoteTagRemove = "note.tag_remove"
	ActionNoteTagSet    = "note.tag_set"
	// creating a deck.
	ActionDeckCreate = "deck.create"
	// ActionDeckUpdate is the audit action written when a deck's name and description are changed.
	ActionDeckUpdate = "deck.update"
	// uploading media.
	ActionMediaUpload = "media.upload"

	// ActionSettingUpdate is the audit action written when the admin panel changes system settings.
	ActionSettingUpdate = "setting.update"
	// ActionMailTemplateUpdate is the audit action written when the admin panel saves or deletes a
	// mail template. The template decides what the mail every user receives looks like, same tier as system settings.
	ActionMailTemplateUpdate = "mail.template_update"

	// ActionInviteCreate is the audit action written when the admin panel creates an invite code.
	ActionInviteCreate = "invite.create"
	// ActionInviteRevoke is the audit action written when the admin panel revokes an invite code.
	ActionInviteRevoke = "invite.revoke"
	// revoking a review.
	ActionReviewUndo = "review.undo"
	// ActionCardSuspend / ActionCardUnsuspend is a user suspending / resuming one of their own cards (affects only them).
	ActionCardSuspend   = "card.suspend"
	ActionCardUnsuspend = "card.unsuspend"
	// Deck sharing: an unauthorized request was rejected (who tried to do what, and when, got blocked).
	ActionPermissionDenied = "permission.denied"
	// automatically linking an external identity on first OIDC login (writes one identities row).
	ActionIdentityLink = "identity.link"
	// the admin panel unlinking an external identity.
	ActionIdentityUnlink = "identity.unlink"
	// Grant changes (sharing page): grant / change role / revoke.
	ActionDeckRoleChange = "deck.role_change"
	ActionDeckRevoke     = "deck.revoke"
	// L3 share consent model: one each for invite, accept and decline, plus a user changing their own receive policy.
	ActionDeckShareInvite   = "deck.share_invite"
	ActionDeckShareAccept   = "deck.share_accept"
	ActionDeckShareReject   = "deck.share_reject"
	ActionSharePolicyUpdate = "share.policy_update"
	// cloning a deck (content copied into the caller's account). Not strictly a sharing change, but recorded all the same.
	ActionDeckClone = "deck.clone"
	// share links: create / revoke one / revoke all at once.
	ActionShareLinkCreate    = "share_link.create"
	ActionShareLinkRevoke    = "share_link.revoke"
	ActionShareLinkRevokeAll = "share_link.revoke_all"
	// a logged-in user holding the link adds that deck to their own list via 'join my decks' (share_link.join).
	// It is a self-service grant: the token is a credential the owner actively sent out, pressing the button is the
	// recipient's own consent, so no invite/accept two-step is needed. Once joined, the grant is independent of the link; revoking the link does not reclaim it.
	ActionShareLinkJoin = "share_link.join"
	// Deck daily quota change (new_per_day / reviews_per_day). What changes is 'how many cards this deck
	// still releases today', which must be recorded like any other deck change.
	ActionDeckCaps = "deck.caps_change"
	// 'one more round': append new-card quota for this deck today, for the caller only, recorded at the same tier as daily quota changes.
	ActionDeckExtraNew = "deck.extra_new"
	// preset parameter optimization: trigger the optimization job and one-click rollback to the default weights.
	ActionPresetOptimize       = "preset.optimize"
	ActionPresetOptimizeRevert = "preset.optimize.revert"
	// ActionPresetCreate / ActionPresetUpdate is the audit action written when creating and editing a preset on the presets page.
	ActionPresetCreate = "preset.create"
	ActionPresetUpdate = "preset.update"
	// deck and preset deletion.
	ActionDeckDelete   = "deck.delete"
	ActionPresetDelete = "preset.delete"
	// ActionDeckPreset is the audit action written when a deck switches scheduling preset: it decides which
	// FSRS parameters schedule this deck, same tier as the daily quota, and must be recorded.
	ActionDeckPreset = "deck.preset_change"
	// ActionJobCancel is the audit action written when the admin panel cancels a background job.
	ActionJobCancel = "job.cancel"

	// TOTP second factor: binding started / enabled / disabled / recovery codes regenerated / recovery code consumed / second step failed.
	// Recovery code plaintext never enters the audit (detail records only the count and the source IP).
	ActionTOTPBegin              = "totp.begin"
	ActionTOTPEnable             = "totp.enable"
	ActionTOTPDisable            = "totp.disable"
	ActionTOTPRecoveryRegenerate = "totp.recovery_regenerate"
	ActionTOTPRecoveryUsed       = "totp.recovery_used"
	ActionTOTPVerifyFailed       = "totp.verify_failed"

	// Mail preferences: recorded when a user saves the toggles for optional mail types (detail records only the changed types and toggles).
	ActionUserEmailPrefsUpdate = "user.email_prefs_update"

	// One-click unsubscribe: recorded when a user turns an optional mail type off with a no-login token.
	ActionUserEmailUnsubscribe = "user.email_unsubscribe"

	// OIDC 'test connection' and SMTP 'test connection'.
	// Both are high-impact actions: two 'test connection' clicks open outbound connections an admin specified.
	// Success and failure both need a row — failure also leaves the forensic trail of who tried to connect where, and when.
	// Audit rows record only the action and target metadata (test connection records host / issuer),
	// never credentials, passwords, tokens or mail bodies.
	ActionAdminOIDCTest = "admin.oidc_test"
	ActionAdminSMTPTest = "admin.smtp_test"
)

// AuditEntry is the input for one audit write.
// UserID / APIKeyID / TargetID may be null (nil lands in the DB as NULL): an unauthenticated failed login has no user_id,
// a system-initiated action has no api_key_id — faking them with 0 would pollute 'search by user' results.
type AuditEntry struct {
	UserID   *uint64
	APIKeyID *uint64
	Action   string
	// An empty TargetType means the action has no target object; TargetID is ignored in that case.
	TargetType string
	TargetID   *uint64
	// Detail is serialized into JSON and stored in detail_json; nil leaves that column empty.
	Detail any
}

// AuditStore wraps the audit_log table; Write is the single entry point where every write operation is recorded.
type AuditStore struct {
	db *gorm.DB
}

// NewAuditStore constructs the audit store.
func NewAuditStore(db *gorm.DB) *AuditStore { return &AuditStore{db: db} }

// Write writes one audit row.
// An empty action is rejected outright: a row with no action name cannot be searched, that is a caller bug and must not land silently.
func (s *AuditStore) Write(ctx context.Context, e AuditEntry) error {
	if e.Action == "" {
		return errors.New("audit action must not be empty")
	}
	row := AuditLog{
		UserID:    e.UserID,
		APIKeyID:  e.APIKeyID,
		Action:    e.Action,
		CreatedAt: time.Now().UTC(),
	}
	if e.TargetType != "" {
		targetType := e.TargetType
		row.TargetType = &targetType
		row.TargetID = e.TargetID
	}
	if e.Detail != nil {
		raw, err := json.Marshal(e.Detail)
		if err != nil {
			return fmt.Errorf("marshal audit detail: %w", err)
		}
		detail := string(raw)
		row.DetailJSON = &detail
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// List returns the most recent limit audit rows in reverse write order; used by the admin panel (M6) and by tests.
func (s *AuditStore) List(ctx context.Context, limit int) ([]AuditLog, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []AuditLog
	if err := s.db.WithContext(ctx).Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list audit log: %w", err)
	}
	return rows, nil
}

// CountByAction returns the number of audit rows for an action; tests use it to assert 'exactly one row per change'.
func (s *AuditStore) CountByAction(ctx context.Context, action string) (int64, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&AuditLog{}).
		Where("action = ?", action).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count audit log: %w", err)
	}
	return n, nil
}

// AuditFilter is the filter for audit search; the zero value of every field means 'do not filter on that dimension'.
//
// Time semantics: created_at is always stored in UTC; From is the lower bound (inclusive), To the upper bound (exclusive).
// Callers convert the user's timezone calendar-day boundaries into these two UTC instants — the store layer does not guess timezones.
type AuditFilter struct {
	// A non-zero UserID restricts the result to that user's rows; system actions (user_id NULL) will not match.
	UserID uint64
	// A non-empty Action matches the action name exactly (values come from this package's Action* constants).
	Action string
	// A non-empty TargetType matches the target type exactly.
	TargetType string
	// A non-zero TargetID matches the target ID exactly.
	TargetID uint64
	// From is the lower time bound (inclusive); zero means unlimited.
	From time.Time
	// To is the upper time bound (exclusive); zero means unlimited.
	To time.Time
	// Limit is the rows per page; <=0 falls back to the default, above the cap is clamped to the cap.
	Limit int
	// Offset is the number of rows to skip; negative values are treated as 0.
	Offset int
}

const (
	// auditSearchDefaultLimit is the default rows per page for audit search.
	auditSearchDefaultLimit = 50
	// auditSearchMaxLimit is the hard cap for audit search: the audit table grows fast, and no caller may pull the whole table at once.
	auditSearchMaxLimit = 200
)

// auditQuery builds the query from the filters; Count and Find each call it once so they never reuse one *gorm.DB session.
func (s *AuditStore) auditQuery(ctx context.Context, f AuditFilter) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&AuditLog{})
	if f.UserID != 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.TargetType != "" {
		q = q.Where("target_type = ?", f.TargetType)
	}
	if f.TargetID != 0 {
		q = q.Where("target_id = ?", f.TargetID)
	}
	if !f.From.IsZero() {
		q = q.Where("created_at >= ?", f.From.UTC())
	}
	if !f.To.IsZero() {
		q = q.Where("created_at < ?", f.To.UTC())
	}
	return q
}

// Search returns a page of audit rows and the total match count, ordered by id descending (newest first).
// The row count is bounded by auditSearchMaxLimit; paging uses LIMIT/OFFSET.
func (s *AuditStore) Search(ctx context.Context, f AuditFilter) ([]AuditLog, int64, error) {
	var total int64
	if err := s.auditQuery(ctx, f).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count audit log: %w", err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = auditSearchDefaultLimit
	}
	if limit > auditSearchMaxLimit {
		limit = auditSearchMaxLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	var rows []AuditLog
	if err := s.auditQuery(ctx, f).Order("id DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("search audit log: %w", err)
	}
	return rows, total, nil
}

// DistinctActions returns the action names present in the DB (ascending), for the action dropdown on the search page.
func (s *AuditStore) DistinctActions(ctx context.Context) ([]string, error) {
	var actions []string
	if err := s.db.WithContext(ctx).Model(&AuditLog{}).Distinct().Order("action ASC").
		Pluck("action", &actions).Error; err != nil {
		return nil, fmt.Errorf("list audit actions: %w", err)
	}
	return actions, nil
}

// Ptr returns the address of v, used to populate nullable fields (user_id / api_key_id / target_id).
func Ptr[T any](v T) *T { return &v }
