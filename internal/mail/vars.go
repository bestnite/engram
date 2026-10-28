package mail

// This file declares the **template variables** of every mail type: the variable name, whether it is
// required, and the language pack key used by the admin page to show what it is for. Senders pass
// values by this same table, and the admin page validates and displays by this same table -- so a
// variable cannot disagree between "the sender has it" and "the page does not know it".
//
// Variable names are part of the interface (they are what an admin writes in a template), always
// lowercase English with underscores; renaming one invalidates existing templates, so the Spec here
// and the senders have to be changed together.

// VarSpec describes one template variable.
type VarSpec struct {
	// Name is the name written in the template: `{{url}}` spells it.
	Name string
	// When Required is true, a custom template lacking it is refused on save, and sending falls back
	// to the built-in body.
	//
	// **The criterion is "without it the mail is useless or cannot be closed out", which deliberately
	// admits only two kinds**: (1) a link the recipient has to click (reset, verification, invite,
	// review, stats, deck page) -- a password mail without the link is waste paper; (2) deck_shared's
	// deck and inviter (without which the mail does not say what it is about). **Display details are
	// never required**: an expiry time, a due count or a pass rate missing just means slightly less
	// information, and marking it required would only make admins hit baffling failures when saving.
	//
	// The unsubscribe link is the one declared-but-never-required variable: Render appends the entry to
	// the HTML footer and the plain-text tail itself, so a template cannot drop it, and requiring it in
	// the template would reject the built-in default, which never mentions it.
	Required bool
	// NoteKey is the language pack key with which the admin page shows what this variable is for.
	NoteKey string
}

// mailVars is the single variable table. A type not in the table means that type does not support
// custom templates yet (the sender does not exist yet), so the admin page renders no editor for it.
//
// unsubscribe_url is declared so a custom template can place the entry inline, but it is never
// required: the entry is rendered by Render (plain-text tail) and the fixed shell (HTML footer), so a
// template cannot remove it, and requiring it in the template would reject the built-in default.
var mailVars = map[Type][]VarSpec{
	TypePasswordReset: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "username", Required: false, NoteKey: "admin.mail.var.username"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.reset_url"},
		{Name: "expires", Required: false, NoteKey: "admin.mail.var.expires"},
	},
	TypeEmailVerification: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.verify_url"},
		{Name: "expires", Required: false, NoteKey: "admin.mail.var.expires"},
	},
	TypeNewDeviceLogin: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "username", Required: false, NoteKey: "admin.mail.var.username"},
		{Name: "time", Required: false, NoteKey: "admin.mail.var.time"},
		{Name: "ip", Required: false, NoteKey: "admin.mail.var.ip"},
		{Name: "url", Required: false, NoteKey: "admin.mail.var.settings_url"},
	},
	TypeCredentialChanged: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "what", Required: false, NoteKey: "admin.mail.var.what"},
		{Name: "time", Required: false, NoteKey: "admin.mail.var.time"},
	},
	TypeAccountStatus: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "status", Required: false, NoteKey: "admin.mail.var.status"},
		{Name: "reason", Required: false, NoteKey: "admin.mail.var.reason"},
	},
	TypeInvite: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.invite_url"},
		{Name: "expires", Required: false, NoteKey: "admin.mail.var.expires"},
	},
	TypeReviewReminder: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "count", Required: false, NoteKey: "admin.mail.var.due_count"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.review_url"},
		{Name: "unsubscribe_url", Required: false, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeStudyDigest: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "count", Required: false, NoteKey: "admin.mail.var.reviewed_count"},
		{Name: "rate", Required: false, NoteKey: "admin.mail.var.pass_rate"},
		{Name: "days", Required: false, NoteKey: "admin.mail.var.streak_days"},
		// The digest has six statistics lines, and every variable the built-in body uses must be listed
		// here -- "used but not registered" is judged a typo by Validate, and the pre-filled default could
		// not be saved back.
		{Name: "new_cards", Required: false, NoteKey: "admin.mail.var.new_cards"},
		{Name: "due", Required: false, NoteKey: "admin.mail.var.due_count"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.stats_url"},
		{Name: "unsubscribe_url", Required: false, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeDeckShared: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "inviter", Required: true, NoteKey: "admin.mail.var.inviter"},
		{Name: "deck", Required: true, NoteKey: "admin.mail.var.deck"},
		{Name: "role", Required: false, NoteKey: "admin.mail.var.role"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.deck_url"},
		{Name: "unsubscribe_url", Required: false, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeDeckPermissionChanged: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "actor", Required: true, NoteKey: "admin.mail.var.actor"},
		{Name: "deck", Required: true, NoteKey: "admin.mail.var.deck"},
		{Name: "role", Required: false, NoteKey: "admin.mail.var.role"},
		{Name: "url", Required: false, NoteKey: "admin.mail.var.deck_url"},
		{Name: "unsubscribe_url", Required: false, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeOptimizeDone: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "preset", Required: false, NoteKey: "admin.mail.var.preset"},
		{Name: "count", Required: false, NoteKey: "admin.mail.var.reviewed_count"},
		{Name: "url", Required: true, NoteKey: "admin.mail.var.presets_url"},
		{Name: "unsubscribe_url", Required: false, NoteKey: "admin.mail.var.unsubscribe_url"},
	},
	TypeJobFailed: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		{Name: "job_id", Required: false, NoteKey: "admin.mail.var.job_id"},
		{Name: "kind", Required: false, NoteKey: "admin.mail.var.job_kind"},
		{Name: "reason", Required: false, NoteKey: "admin.mail.var.fail_reason"},
		{Name: "url", Required: false, NoteKey: "admin.mail.var.jobs_url"},
	},
	TypeMediaDiskAlert: {
		{Name: "site", Required: false, NoteKey: "admin.mail.var.site"},
		// The default body names the user who hit the quota, and the sender passes this value; without
		// the entry here the editor pre-fill would lose the name and a custom template using it would
		// be rejected as an unknown variable.
		{Name: "username", Required: false, NoteKey: "admin.mail.var.username"},
		{Name: "used", Required: false, NoteKey: "admin.mail.var.media_used"},
		{Name: "limit", Required: false, NoteKey: "admin.mail.var.media_limit"},
	},
}

// VarSpecs returns the variable table of a type; a second return value of false means the type does
// not support custom templates. It is not named Vars: that is the type of the template variable
// **values** (template.go), and sharing the name would shadow one with the other.
func VarSpecs(t Type) ([]VarSpec, bool) {
	specs, ok := mailVars[t]
	return specs, ok
}

// RequiredVars returns the required variable names, for save validation and test assertions.
func RequiredVars(t Type) []string {
	specs, ok := mailVars[t]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		if s.Required {
			out = append(out, s.Name)
		}
	}
	return out
}
