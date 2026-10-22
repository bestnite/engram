package mail

// This file is the email type catalog: **defined in one place**, shared by the preferences page and
// every sender.
//
// The classification rests on **whether the user can turn it off**, not on the mail content -- allowing
// security mail to be turned off is like leaving a back door. So "default on/off" and "can it be
// disabled" are both derived from Class instead of being annotated per type: once a type belongs to a
// Class, its default and its disable-ability are uniquely determined, and the preferences page and the
// senders cannot disagree.
//
// This package holds only the catalog and the decision logic, and implements no sending behavior.
// Senders, when integrated in wave 2, call Lookup / CanDisable / DefaultEnabled / ResolveEnabled and
// copy none of the rules here.
//
// Naming boundary: this file must not define Message, Outbox, ErrNotConfigured, Configured -- those are
// transport-layer symbols; this package is developed in parallel, and a duplicate declaration would
// collide.

// Class is the broad category of an email type; the classification rests on whether the user can turn
// it off.
type Class string

const (
	// ClassSecurity is class A security/transactional: on by default, cannot be disabled, no unsubscribe
	// header.
	ClassSecurity Class = "security"
	// ClassCollab is class B collaboration/authorization: on by default, can be disabled.
	ClassCollab Class = "collab"
	// ClassStudy is class C study/operational: off by default, can be disabled.
	ClassStudy Class = "study"
	// ClassAdmin is class D admin notification: on by default, can be disabled, sent to admins.
	ClassAdmin Class = "admin"
)

// Type is the stable English identifier of an email type; the preferences page, the senders and
// storage all use it.
type Type string

const (
	// A security/transactional: on by default, cannot be disabled, no unsubscribe header.
	TypePasswordReset     Type = "password_reset"     // Password reset
	TypeEmailVerification Type = "email_verification" // Email verification and email-change confirmation
	TypeNewDeviceLogin    Type = "new_device_login"   // New device / new IP login alert
	TypeCredentialChanged Type = "credential_changed" // Password/TOTP/recovery-code change notice
	TypeAccountStatus     Type = "account_status"     // Account disabled or deleted notice

	// B collaboration/authorization: on by default, can be disabled.
	TypeDeckShared            Type = "deck_shared"             // a deck was shared with you
	TypeDeckPermissionChanged Type = "deck_permission_changed" // deck permission changed or revoked
	TypeInvite                Type = "invite"                  // the invite code is sent to the invitee

	// C study/operational: off by default, can be disabled.
	TypeReviewReminder Type = "review_reminder" // review due reminder
	TypeStudyDigest    Type = "study_digest"    // study digest
	TypeOptimizeDone   Type = "optimize_done"   // parameter optimization finished

	// Two class C types were removed on 2026-10-07: streak about to break (streak_at_risk) and
	// import/export done (import_export_done). The reason was not "not implemented yet" but that **they
	// had no trigger point**: the former had no rule for "about to break", the latter had no asynchronous
	// import/export job (when it finishes synchronously the user is looking at the result page, so sending
	// mail is meaningless; that type was reserved for a future asynchronous large-file flow all along).
	// Every type in the catalog must have a sender, otherwise the preferences page grows a switch that
	// never sends anything. To bring them back, build the trigger point first.

	// D admin notification: on by default, can be disabled, sent to admins.
	TypeJobFailed      Type = "job_failed"       // job failed
	TypeMediaDiskAlert Type = "media_disk_alert" // media quota / disk alert
)

// Definition describes one email type: which class it belongs to, and the language pack key the
// preferences page shows it with. The default and the disable-ability are deliberately not fields --
// they are uniquely derived from Class (DefaultEnabled / CanDisable); writing one per type would let
// the two places drift.
type Definition struct {
	// Type is the stable English identifier and also the key of the preference map in storage.
	Type Type
	// Class is the owning category, which decides the default and whether it can be disabled.
	Class Class
	// LabelKey is the language pack key of this type's display name (mail.prefs.type.<type>).
	LabelKey string
}

// classOrder is the fixed order in which the preferences page shows the categories, and the sort order
// of Catalog().
var classOrder = []Class{ClassSecurity, ClassCollab, ClassStudy, ClassAdmin}

// catalog is the single catalog definition, fixed in the order A -> B -> C -> D. Adding a type means
// adding one line here; the preferences page and the senders both read from here, there is no second
// list.
var catalog = []Definition{
	// A security/transactional
	{Type: TypePasswordReset, Class: ClassSecurity, LabelKey: "mail.prefs.type.password_reset"},
	{Type: TypeEmailVerification, Class: ClassSecurity, LabelKey: "mail.prefs.type.email_verification"},
	{Type: TypeNewDeviceLogin, Class: ClassSecurity, LabelKey: "mail.prefs.type.new_device_login"},
	{Type: TypeCredentialChanged, Class: ClassSecurity, LabelKey: "mail.prefs.type.credential_changed"},
	{Type: TypeAccountStatus, Class: ClassSecurity, LabelKey: "mail.prefs.type.account_status"},

	// B collaboration/authorization
	{Type: TypeDeckShared, Class: ClassCollab, LabelKey: "mail.prefs.type.deck_shared"},
	{Type: TypeDeckPermissionChanged, Class: ClassCollab, LabelKey: "mail.prefs.type.deck_permission_changed"},
	{Type: TypeInvite, Class: ClassCollab, LabelKey: "mail.prefs.type.invite"},

	// C study/operational
	{Type: TypeReviewReminder, Class: ClassStudy, LabelKey: "mail.prefs.type.review_reminder"},
	{Type: TypeStudyDigest, Class: ClassStudy, LabelKey: "mail.prefs.type.study_digest"},
	{Type: TypeOptimizeDone, Class: ClassStudy, LabelKey: "mail.prefs.type.optimize_done"},

	// D admin notification
	{Type: TypeJobFailed, Class: ClassAdmin, LabelKey: "mail.prefs.type.job_failed"},
	{Type: TypeMediaDiskAlert, Class: ClassAdmin, LabelKey: "mail.prefs.type.media_disk_alert"},
}

// byType is the lookup index of catalog, built once at package init.
var byType = func() map[Type]Definition {
	m := make(map[Type]Definition, len(catalog))
	for _, d := range catalog {
		m[d.Type] = d
	}
	return m
}()

// Catalog returns a copy of the catalog, fixed in the order A -> B -> C -> D (the preferences page
// renders the groups from it). Returning a copy instead of the internal slice means callers cannot
// accidentally rewrite the single catalog.
func Catalog() []Definition {
	out := make([]Definition, len(catalog))
	copy(out, catalog)
	return out
}

// Lookup fetches a definition by type; an unregistered type returns false. A sender must Lookup before
// deciding to send: unknown types are never sent, so a mistyped type name cannot deliver silently.
func Lookup(t Type) (Definition, bool) {
	d, ok := byType[t]
	return d, ok
}

// ClassOrder returns a copy of the display order of the categories (A -> B -> C -> D).
func ClassOrder() []Class {
	out := make([]Class, len(classOrder))
	copy(out, classOrder)
	return out
}

// CanDisable reports whether a category can be turned off by the user. Only class A
// security/transactional cannot be disabled: allowing security mail to be turned off is like leaving a
// back door.
func CanDisable(c Class) bool {
	return c != ClassSecurity
}

// DefaultEnabled reports the default switch of a category: A/B/D default on, C defaults off.
func DefaultEnabled(c Class) bool {
	return c != ClassStudy
}

// AdminOnly reports whether a category is aimed at admins only. Only class D admin notification is
// admin-only: a non-admin's preferences page does not show it and does not accept a choice for it. The
// decision lives in the catalog next to CanDisable/DefaultEnabled for the same reason -- "which
// category is for whom" is defined in one place, so display and saving cannot disagree.
func AdminOnly(c Class) bool {
	return c == ClassAdmin
}

// ResolveEnabled is the single decision shared by the senders and the preferences page: it applies the
// catalog rules on top of the user's explicit choice.
//
//   - A class that cannot be disabled (A) is always true and the user's choice is ignored -- even an
//     A=false in storage cannot take effect;
//   - A class that can be disabled prefers the user's explicit choice (choices[t]) and falls back to
//     the class default when there is none;
//   - An unregistered type returns false: a mistyped type name should not deliver.
//
// The keys of choices are the string form of Type (consistent with the storage layer) and contain
// only the choices for disable-able types.
func ResolveEnabled(choices map[string]bool, t Type) bool {
	def, ok := Lookup(t)
	if !ok {
		return false
	}
	if !CanDisable(def.Class) {
		return true
	}
	if v, ok := choices[string(t)]; ok {
		return v
	}
	return DefaultEnabled(def.Class)
}
