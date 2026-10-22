package mail

import "strings"

// This file is the single definition of the "built-in body".
//
// The built-in body has two readers and **must come from the same place**:
//
//  1. the senders -- with no custom template (or with a broken template or a missing variable), this is
//     what goes out;
//  2. the admin page -- the email template editor pre-fills it, and the admin edits on top of this
//     original.
//
// Two implementations would inevitably drift, and the drift shows up in the hardest-to-spot way: the
// "default" the admin page shows and the body actually sent are not the same thing, and the admin
// thinks they are editing the default while they are editing something else. So the same piece of code
// here has **two uses**: pass real variables to get plain text that can be sent, pass placeholder
// variables (PlaceholderVars) to get template text.
//
// Why not the "write the defaults into the database" approach: the default copy would then freeze in
// the DB, later language pack changes would no longer affect it, and "restore defaults" would have to
// copy another copy back out of the code. Built-in defaults living in code forever means deleting or
// corrupting a template cannot stop mail from being sent.

// TfFunc is the minimal shape of "look up the copy by language pack id and substitute the variables"
// (the `Tf` of `*i18n.Localizer` will do).
type TfFunc func(msgID string, data map[string]any) string

// Variants: cases where one type identifier has two built-in wordings. Today only email verification
// has one -- registration verification and email-change confirmation both mean "confirm one email
// address", share the same variable set and the same preference switch, so they are the same type.
const (
	// VariantDefault is the primary wording.
	VariantDefault = ""
	// VariantEmailChange is the "confirm the new email" wording.
	VariantEmailChange = "change"
)

// defaultLine is one line of the built-in body: either language pack copy (ID) or the variable value
// itself (Var).
//
// Content such as links and the unsubscribe address is not copy but a variable -- writing those as
// language pack entries would make every language copy the same URL, and a copy error would go
// unnoticed.
type defaultLine struct {
	ID  string
	Var string
	// When Join is true the output is "<language pack copy>: <variable value>", i.e. the unsubscribe
	// entry line -- it has the same shape as the line the template path appends automatically (Render in
	// internal/mail/template.go).
	Join bool
	// Args maps "the variable name in the language pack" to "the key in the variable table", for cases
	// where the same `{{.count}}` on different lines means different things (the digest's "reviewed",
	// "new cards" and "due" all use `{{.count}}` but read three different numbers). When empty, the
	// whole variable table is passed in.
	Args map[string]string
}

func msg(id string) defaultLine    { return defaultLine{ID: id} }
func varLine(v string) defaultLine { return defaultLine{Var: v} }
func blank() defaultLine           { return defaultLine{} }

// defaultSpec is the built-in wording of one email type.
type defaultSpec struct {
	// subjectID is the language pack id of the subject.
	subjectID string
	// lines is the line sequence of the body; an empty ID together with an empty Var means a blank
	// line.
	lines func(v Vars) []defaultLine
}

// defaultSpecs is the built-in body of every type. The order and the line count match the original
// assembly of each sender word for word -- the point of extracting it is only to let the admin page see
// the same text, not to change the copy along the way.
var defaultSpecs = map[Type]defaultSpec{
	TypePasswordReset: {
		subjectID: "mail.reset.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{
				msg("mail.reset.greeting"), blank(),
				msg("mail.reset.body"), blank(),
				msg("mail.reset.link_label"), varLine("url"), blank(),
				msg("mail.reset.expires"),
			}
		},
	},
	TypeEmailVerification: {
		subjectID: "mail.verify.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{
				msg("mail.verify.greeting"), blank(),
				msg("mail.verify.body"), blank(),
				msg("mail.verify.link_label"), varLine("url"), blank(),
				msg("mail.verify.expires"),
			}
		},
	},
	TypeNewDeviceLogin: {
		subjectID: "mail.notice.new_device.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.notice.greeting"), blank(), msg("mail.notice.new_device.body")}
		},
	},
	TypeCredentialChanged: {
		subjectID: "mail.notice.credential.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.notice.greeting"), blank(), msg("mail.notice.credential.body")}
		},
	},
	TypeAccountStatus: {
		subjectID: "mail.notice.account.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.notice.greeting"), blank(), msg("mail.notice.account.body")}
		},
	},
	TypeDeckShared: {
		subjectID: "mail.deck_shared.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{
				msg("mail.deck_shared.greeting"), blank(),
				msg("mail.deck_shared.body"), blank(),
				msg("mail.deck_shared.link_label"), varLine("url"),
			}
		},
	},
	TypeDeckPermissionChanged: {
		subjectID: "mail.deck_permission.subject",
		lines: func(v Vars) []defaultLine {
			// On revoke role is empty: the body should say "access was revoked", not "the permission was
			// changed to empty". This test matches the senders -- it is derived from the variables, with no
			// extra parameter, so the default the admin page gets lands in the "granted" branch because the
			// placeholder is non-empty.
			line := msg("mail.deck_permission.granted")
			if strings.TrimSpace(v["role"]) == "" {
				line = msg("mail.deck_permission.revoked")
			}
			return []defaultLine{
				msg("mail.deck_permission.greeting"), blank(),
				line, blank(),
				msg("mail.deck_permission.link_label"), varLine("url"),
			}
		},
	},
	TypeInvite: {
		subjectID: "mail.invite.subject",
		lines: func(v Vars) []defaultLine {
			out := []defaultLine{
				msg("mail.invite.greeting"), blank(),
				msg("mail.invite.body"), blank(),
				msg("mail.invite.link_label"), varLine("url"),
			}
			// The expiry is an optional variable: without it that line does not appear (rather than leaving
			// a blank line).
			if strings.TrimSpace(v["expires"]) != "" {
				out = append(out, blank(), msg("mail.invite.expires"))
			}
			return out
		},
	},
	TypeReviewReminder: {
		subjectID: "mail.reminder.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.reminder.body")}
		},
	},
	TypeStudyDigest: {
		subjectID: "mail.digest.subject",
		lines: func(Vars) []defaultLine {
			// Three of the six lines write `{{.count}}` but read three different numbers, so the value is
			// specified per line.
			return []defaultLine{
				{ID: "mail.digest.reviewed", Args: map[string]string{"count": "count"}},
				{ID: "mail.digest.pass_rate", Args: map[string]string{"rate": "rate"}},
				{ID: "mail.digest.streak", Args: map[string]string{"days": "days"}},
				{ID: "mail.digest.new_cards", Args: map[string]string{"count": "new_cards"}},
				{ID: "mail.digest.due", Args: map[string]string{"count": "due"}},
				{ID: "mail.digest.link", Args: map[string]string{"url": "url"}},
			}
		},
	},
	TypeOptimizeDone: {
		subjectID: "mail.optimize.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{
				msg("mail.optimize.greeting"), blank(),
				msg("mail.optimize.body"), blank(),
				msg("mail.optimize.link_label"), varLine("url"),
			}
		},
	},
	TypeJobFailed: {
		subjectID: "mail.admin.job_failed.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.admin.greeting"), blank(), msg("mail.admin.job_failed.body")}
		},
	},
	TypeMediaDiskAlert: {
		subjectID: "mail.admin.media_alert.subject",
		lines: func(Vars) []defaultLine {
			return []defaultLine{msg("mail.admin.greeting"), blank(), msg("mail.admin.media_alert.body")}
		},
	},
}

// PlaceholderVars returns the placeholder variables of the type: every variable name maps to
// `{{name}}` itself.
//
// The admin page calls DefaultTemplate with it and gets **template text** (no real value substituted
// yet), which the admin edits directly; senders pass real variables instead. Both paths run the same
// code, so "the default seen on the page" and "the fallback actually sent" cannot differ.
func PlaceholderVars(t Type) Vars {
	specs, ok := VarSpecs(t)
	if !ok {
		return Vars{}
	}
	out := make(Vars, len(specs))
	for _, s := range specs {
		out[s.Name] = "{{" + s.Name + "}}"
	}
	return out
}

// DefaultTemplate returns the built-in body (subject + body) of a type.
//
// Real variables in -> plain text that can be sent as is; PlaceholderVars(t) in -> template text.
// The second return value says whether the type has a built-in wording (every type in the catalog
// should have one).
func DefaultTemplate(t Type, variant string, tf TfFunc, vars Vars) (subject, body string, ok bool) {
	spec, found := defaultSpecs[t]
	if !found {
		return "", "", false
	}
	subjectID := spec.subjectID
	if t == TypeEmailVerification && variant == VariantEmailChange {
		subjectID = "mail.verify.change_subject"
	}
	lines := spec.lines(vars)
	if t == TypeEmailVerification && variant == VariantEmailChange {
		lines = changeVariantLines(lines)
	}

	// The unsubscribe entry of optional-class mail has to appear in the body: the RFC 8058 header is only
	// visible to mail clients, not to the web UI or a plaintext reader (the same stance as "the unsubscribe
	// link is a required variable" in vars.go).
	if declaresVar(t, "unsubscribe_url") {
		if unsub := strings.TrimSpace(vars["unsubscribe_url"]); unsub != "" {
			lines = append(lines, blank(),
				defaultLine{ID: "mail.footer.unsubscribe", Var: "unsubscribe_url", Join: true})
		}
	}
	return tf(subjectID, anyMap(vars)), renderLines(lines, tf, vars), true
}

// changeVariantLines swaps the primary wording's lines for the "email change confirmation" set of copy
// ids.
//
// Both variants have exactly the same line order (greeting / body / link / expiry) and differ only in
// language pack ids, so ids are swapped here instead of writing a second line sequence -- a second copy
// would sooner or later be updated on one side only.
func changeVariantLines(lines []defaultLine) []defaultLine {
	swap := map[string]string{
		"mail.verify.greeting":   "mail.verify.change_greeting",
		"mail.verify.body":       "mail.verify.change_body",
		"mail.verify.link_label": "mail.verify.change_link_label",
		"mail.verify.expires":    "mail.verify.change_expires",
	}
	out := make([]defaultLine, 0, len(lines))
	for _, l := range lines {
		if to, ok := swap[l.ID]; ok {
			l.ID = to
		}
		out = append(out, l)
	}
	return out
}

// renderLines renders a line sequence into one block of text: a language pack line goes through tf, a
// variable line is the value itself.
func renderLines(lines []defaultLine, tf TfFunc, vars Vars) string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		switch {
		case l.Join && l.ID != "" && l.Var != "":
			out = append(out, tf(l.ID, anyMap(vars))+": "+vars[l.Var])
		case l.ID != "":
			out = append(out, tf(l.ID, lineData(l, vars)))
		case l.Var != "":
			out = append(out, vars[l.Var])
		default:
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

// lineData assembles the variables to hand the language pack for one line: when Args is empty the whole
// variable table goes in, otherwise the keys named by Args are picked one by one (a value may still be a
// `{{name}}` placeholder, depending on what the caller passed).
func lineData(l defaultLine, vars Vars) map[string]any {
	if len(l.Args) == 0 {
		return anyMap(vars)
	}
	data := make(map[string]any, len(l.Args))
	for msgVar, varsKey := range l.Args {
		data[msgVar] = vars[varsKey]
	}
	return data
}

// declaresVar reports whether the variable exists in the type's variable table. Using it instead of a
// separate switch field: "optional-class mail carries an unsubscribe entry" is already expressed by the
// variable table in vars.go, and annotating it a second time would inevitably drift.
func declaresVar(t Type, name string) bool {
	specs, ok := VarSpecs(t)
	if !ok {
		return false
	}
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// anyMap converts the variable table into the any map go-i18n needs (a type conversion only, the
// content is untouched).
func anyMap(vars Vars) map[string]any {
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		out[k] = v
	}
	return out
}
