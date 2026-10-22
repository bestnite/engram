package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// Stable error values of the identity-linking decision; the transport layer maps them to stable English codes and localized text
// (the bootstrap text "please contact an administrator to link your account" is a page responsibility, this package produces no user-visible text).
var (
	// ErrIdentityLinkDenied means that under the closed policy there is neither an existing link nor an automatic match, so only an administrator can link manually.
	ErrIdentityLinkDenied = errors.New("identity link denied by registration policy")
)

// oidcPlaceholderEmailSuffix is the placeholder email suffix synthesized for OIDC accounts when there is no email claim.
// localhost is the placeholder value the repository allows and a real domain is never written;
// the synthesized email keeps users.email's NOT NULL + UNIQUE constraints satisfied and exposes no external hostname.
const oidcPlaceholderEmailSuffix = "@localhost"

// OAuthProfile is the normalized input from the IdP that the linking decision needs.
// Email is the raw claim value the caller supplies as needed; this package only normalizes case and whitespace.
type OAuthProfile struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	// Name is the display name returned by the IdP (the claim name is configurable). It is used only as the
	// display name when auto-creating an account; the linking decision looks only at Subject and Email, so it affects no branch.
	Name string
}

// LinkDecisionKind identifies the identity-linking branch, with values kept stable for audits and test assertions.
type LinkDecisionKind string

const (
	// LinkDecisionExisting hits linking case 1: (provider, subject) is already linked, so log straight into the matching internal user.
	LinkDecisionExisting LinkDecisionKind = "existing"
	// LinkDecisionAutoLink hits linking case 2: the email is verified and matches an internal user, so link automatically.
	LinkDecisionAutoLink LinkDecisionKind = "auto_link"
	// LinkDecisionCreateUser hits linking case 3: under the open / invite policy, create an account automatically and link it.
	LinkDecisionCreateUser LinkDecisionKind = "create_user"
	// LinkDecisionDeny hits linking case 3: under the closed policy, deny the login and wait for an administrator to link.
	LinkDecisionDeny LinkDecisionKind = "deny"
)

// LinkDecision is the output of the pure decision function: which branch was hit, and the target user for existing / auto_link.
type LinkDecision struct {
	Kind   LinkDecisionKind
	UserID uint64
	// Err is non-nil only when Kind == LinkDecisionDeny.
	Err error
}

// DecideIdentityLink is the pure decision function over the three automatic identity-linking branches: it touches no database and depends on no OIDC protocol.
//
// The input covers four situations (existing = the sub already exists, matched = the internal user the email hits):
//  1. existing non-empty → log in directly, ignoring the email; (sub already exists)
//  2. EmailVerified and Email non-empty and matched non-empty → link automatically; (verified email)
//  3. otherwise → follow the registration policy: open / invite creates an account, closed denies. (unverified email / no email)
//
// matched must be the same address as profile.Email, otherwise it counts as no match: this guarantees "link automatically only when the email really matches".
// An unverified email never auto-links even when it collides with an existing account (preventing account takeover from a controllable email claim alone).
func DecideIdentityLink(profile OAuthProfile, existing *store.Identity, matched *store.User, policy string) LinkDecision {
	if existing != nil {
		return LinkDecision{Kind: LinkDecisionExisting, UserID: existing.UserID}
	}
	email := normalizeEmail(profile.Email)
	if profile.EmailVerified && email != "" && matched != nil && strings.EqualFold(matched.Email, email) {
		return LinkDecision{Kind: LinkDecisionAutoLink, UserID: matched.ID}
	}
	// Reaching here while the email is already taken by an existing account can only mean "an unverified email collided": creating the account must violate the unique constraint.
	// Better to reject than to quietly attach the new identity to someone else's account.
	if matched != nil {
		return LinkDecision{Kind: LinkDecisionDeny, Err: ErrIdentityLinkDenied}
	}
	switch ParseRegistrationPolicy(policy) {
	case PolicyOpen, PolicyInvite:
		return LinkDecision{Kind: LinkDecisionCreateUser}
	default:
		return LinkDecision{Kind: LinkDecisionDeny, Err: ErrIdentityLinkDenied}
	}
}

// normalizeEmail normalizes an email: lowercase + trim surrounding whitespace. An empty string stays empty, meaning the IdP returned no email.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// IdentityLinkStore is the storage capability the linking flow needs, defined on the consumer side (AGENTS.md §2.4).
// The production implementation is newStoreIdentityLinkStore; tests can inject an in-memory stand-in, so the decision and flow need no real OIDC.
type IdentityLinkStore interface {
	// FindIdentity looks up the link by (provider, subject); (nil, nil) when not linked.
	FindIdentity(ctx context.Context, provider, subject string) (*store.Identity, error)
	// FindUserByID looks up an internal user by primary key; (nil, nil) when absent.
	FindUserByID(ctx context.Context, id uint64) (*store.User, error)
	// FindUserByEmail looks up an internal user by email; (nil, nil) when absent.
	FindUserByEmail(ctx context.Context, email string) (*store.User, error)
	// LinkIdentity writes one link row.
	LinkIdentity(ctx context.Context, ident *store.Identity) error
	// CreateOIDCUser creates an internal account with no local password.
	CreateOIDCUser(ctx context.Context, in CreateUserInput) (*store.User, error)
}

// IdentityLinkService chains the whole identity-linking flow: look up the link → auto-link → create an account per policy.
// It implements no OIDC protocol interaction; the caller only has to hand the normalized OAuthProfile to Resolve.
type IdentityLinkService struct {
	store  IdentityLinkStore
	audit  *Auditor
	logger *slog.Logger
	now    func() time.Time
}

// NewIdentityLinkService constructs the linking service. store is required; audit may be nil (no audit is recorded);
// when logger is nil, slog.Default() is used; an audit write failure only logs in English and does not affect a link already made (principle).
func NewIdentityLinkService(st IdentityLinkStore, audit *Auditor, logger *slog.Logger) (*IdentityLinkService, error) {
	if st == nil {
		return nil, errors.New("auth: identity link store is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &IdentityLinkService{store: st, audit: audit, logger: logger,
		now: func() time.Time { return time.Now().UTC() }}, nil
}

// Resolve performs the linking decision for one OIDC first login and returns the internal user that should be logged in.
// The second return value is the branch that was hit (for the caller to log and audit), the third is the error.
func (s *IdentityLinkService) Resolve(ctx context.Context, profile OAuthProfile, policy string) (*store.User, LinkDecision, error) {
	if strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Subject) == "" {
		return nil, LinkDecision{}, errors.New("auth: provider and subject are required")
	}

	existing, err := s.store.FindIdentity(ctx, profile.Provider, profile.Subject)
	if err != nil {
		return nil, LinkDecision{}, fmt.Errorf("find identity: %w", err)
	}

	// Only when the IdP supplied an email do we look up an account with the same name; the matched user, verified or not, goes to the pure function,
	// which decides whether it suffices to trigger auto-linking or must deny account creation.
	email := normalizeEmail(profile.Email)
	var matched *store.User
	if email != "" {
		matched, err = s.store.FindUserByEmail(ctx, email)
		if err != nil {
			return nil, LinkDecision{}, fmt.Errorf("find user by email: %w", err)
		}
	}

	decision := DecideIdentityLink(profile, existing, matched, policy)
	switch decision.Kind {
	case LinkDecisionExisting:
		u, err := s.store.FindUserByID(ctx, decision.UserID)
		if err != nil {
			return nil, decision, fmt.Errorf("find user: %w", err)
		}
		if u == nil {
			return nil, decision, fmt.Errorf("identity references a missing user: id=%d", decision.UserID)
		}
		if u.Status != store.StatusActive {
			return nil, decision, ErrUserDisabled
		}
		return u, decision, nil

	case LinkDecisionAutoLink:
		if matched == nil || matched.Status != store.StatusActive {
			// The link target is disabled: do not create a new link and do not let the login through.
			return nil, decision, ErrUserDisabled
		}
		if err := s.link(ctx, profile, matched.ID, email); err != nil {
			return nil, decision, err
		}
		return matched, decision, nil

	case LinkDecisionCreateUser:
		created, err := s.store.CreateOIDCUser(ctx, CreateUserInput{
			Username:    oidcUsername(profile),
			Email:       oidcAccountEmail(profile),
			DisplayName: oidcDisplayName(profile),
		})
		if err != nil {
			return nil, decision, fmt.Errorf("create oidc user: %w", err)
		}
		if err := s.link(ctx, profile, created.ID, email); err != nil {
			return nil, decision, err
		}
		return created, decision, nil

	default:
		return nil, decision, decision.Err
	}
}

// link writes one identities row and records an audit entry; an audit failure only logs in English and does not roll back the link (principle).
func (s *IdentityLinkService) link(ctx context.Context, profile OAuthProfile, userID uint64, email string) error {
	ident := &store.Identity{
		UserID:   userID,
		Provider: profile.Provider,
		Subject:  profile.Subject,
		LinkedAt: s.now(),
	}
	if email != "" {
		ident.Email = &email
	}
	if err := s.store.LinkIdentity(ctx, ident); err != nil {
		return fmt.Errorf("link identity: %w", err)
	}
	if s.audit == nil {
		return nil
	}
	err := s.audit.Record(ctx, store.AuditEntry{
		UserID:     store.Ptr(userID),
		Action:     store.ActionIdentityLink,
		TargetType: "user",
		TargetID:   store.Ptr(userID),
		Detail: map[string]any{
			"provider":       profile.Provider,
			"subject":        profile.Subject,
			"email_verified": profile.EmailVerified,
		},
	})
	if err != nil {
		s.logger.Error("record identity link audit failed",
			"user_id", userID, "provider", profile.Provider, "error", err)
	}
	return nil
}

// oidcUsername generates a deterministic and unique login name for auto-created accounts: the email local part (oidc when there is no email)
// plus a hash prefix of provider+subject. The hash guarantees the same IdP identity gets the same name on repeated logins,
// and that two different identities never collide (users.username is unique).
func oidcUsername(profile OAuthProfile) string {
	sum := sha256.Sum256([]byte(profile.Provider + "\x00" + profile.Subject))
	suffix := hex.EncodeToString(sum[:])[:8]
	base := sanitizeUsername(emailLocalPart(profile.Email))
	if base == "" {
		base = "oidc"
	}
	if len(base) > 24 {
		base = base[:24]
	}
	return base + "-" + suffix
}

// oidcAccountEmail returns the email to persist: use the IdP's when given, otherwise synthesize a placeholder so the unique constraint holds.
func oidcAccountEmail(profile OAuthProfile) string {
	if email := normalizeEmail(profile.Email); email != "" {
		return email
	}
	return oidcUsername(profile) + oidcPlaceholderEmailSuffix
}

// oidcDisplayName builds the display name: the IdP's name claim first, then the email local part, finally the login name; it produces no user-visible text.
func oidcDisplayName(profile OAuthProfile) string {
	if name := strings.TrimSpace(profile.Name); name != "" {
		return name
	}
	if local := emailLocalPart(profile.Email); local != "" {
		return local
	}
	return oidcUsername(profile)
}

// emailLocalPart takes the part before @; an empty string when there is no @ or it is empty.
func emailLocalPart(email string) string {
	email = normalizeEmail(email)
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return ""
	}
	return email[:at]
}

// sanitizeUsername keeps only the characters an account name may contain (lowercase letters, digits, dot, underscore, hyphen),
// replacing the rest with hyphens; the result may be empty, in which case the caller adds a default prefix.
func sanitizeUsername(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}

// NewStoreIdentityLinkStore adapts the three concrete stores into an IdentityLinkStore: it normalizes "not found" into
// (nil, nil), so the decision function above need not understand GORM's sentinel errors. Production assembly is in cmd/engram.
func NewStoreIdentityLinkStore(identities *store.IdentityStore, users *store.UserStore, accounts *AccountService) IdentityLinkStore {
	return &storeIdentityLinkStore{identities: identities, users: users, accounts: accounts}
}

type storeIdentityLinkStore struct {
	identities *store.IdentityStore
	users      *store.UserStore
	accounts   *AccountService
}

func (a *storeIdentityLinkStore) FindIdentity(ctx context.Context, provider, subject string) (*store.Identity, error) {
	ident, err := a.identities.ByProviderSubject(ctx, provider, subject)
	if errors.Is(err, store.ErrIdentityNotFound) {
		return nil, nil
	}
	return ident, err
}

func (a *storeIdentityLinkStore) FindUserByID(ctx context.Context, id uint64) (*store.User, error) {
	u, err := a.users.ByID(ctx, id)
	if store.IsNotFound(err) {
		return nil, nil
	}
	return u, err
}

func (a *storeIdentityLinkStore) FindUserByEmail(ctx context.Context, email string) (*store.User, error) {
	u, err := a.users.ByEmail(ctx, email)
	if store.IsNotFound(err) {
		return nil, nil
	}
	return u, err
}

func (a *storeIdentityLinkStore) LinkIdentity(ctx context.Context, ident *store.Identity) error {
	return a.identities.Create(ctx, ident)
}

func (a *storeIdentityLinkStore) CreateOIDCUser(ctx context.Context, in CreateUserInput) (*store.User, error) {
	return a.accounts.CreateOIDCUser(ctx, in)
}
