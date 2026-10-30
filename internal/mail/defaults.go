package mail

import (
	"html"
	"regexp"
	"strings"

	"git.nite07.com/nite/engram/internal/mailstyle"
)

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
// here has **two uses**, and both return the same HTML: the body is always in template form
// (`{{name}}` placeholders), whether the caller passes real values or PlaceholderVars. Real values
// only decide which optional sections exist and fill the subject, which is plain text. Both uses go
// through the same renderer, so "save the default unchanged" and "save nothing" produce the same mail.
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

// defaultSpec is the built-in body of one mail type, as HTML template text.
type defaultSpec struct {
	subjectID string
	// body builds the HTML body from {{placeholder}} variables only. Real values are substituted
	// after template rendering, where HTML-escaping is the only thing left to do, so a value can never
	// turn into markup.
	// has reports whether a variable carries a value in this call; it is used only to decide whether
	// an optional section exists.
	body func(tf TfFunc, ph Vars, has func(string) bool) string
	// variants holds the alternate wordings of one type; only email verification has one.
	variants map[string]defaultVariant
}

// defaultVariant is one alternate wording of a type: its own subject and body.
type defaultVariant struct {
	subjectID string
	body      func(tf TfFunc, ph Vars, has func(string) bool) string
}

// defaultSpecs is the built-in body of every type.
//
// Security rule for every body function: it interpolates copy with ph (the value is the literal
// `{{name}}`) and never with a real value. Variable values carry user-controlled content (deck names,
// usernames, failure reasons); splicing one into the markup would let it become HTML. Real values are
// substituted by Render after template rendering, where escaping is the only thing left to do. has
// exists only to decide whether an optional section is present.
var defaultSpecs = map[Type]defaultSpec{
	TypePasswordReset: {
		subjectID: "mail.reset.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.reset.title"),
				htmlPara(tf, "mail.reset.body", anyMap(ph)),
				htmlCTA(tf, "mail.reset.link_label", "url"),
				htmlNote(tf, "mail.reset.note", anyMap(ph)),
			)
		},
	},
	TypeEmailVerification: {
		subjectID: "mail.verify.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.verify.title"),
				htmlPara(tf, "mail.verify.body", anyMap(ph)),
				htmlCTA(tf, "mail.verify.link_label", "url"),
				htmlNote(tf, "mail.verify.note", anyMap(ph)),
			)
		},
		variants: map[string]defaultVariant{
			VariantEmailChange: {
				subjectID: "mail.verify.change_subject",
				body: func(tf TfFunc, ph Vars, has func(string) bool) string {
					return htmlJoin(
						htmlHeading(tf, "mail.verify.change_title"),
						htmlPara(tf, "mail.verify.change_body", anyMap(ph)),
						htmlCTA(tf, "mail.verify.change_link_label", "url"),
						htmlNote(tf, "mail.verify.change_note", anyMap(ph)),
					)
				},
			},
		},
	},
	TypeNewDeviceLogin: {
		subjectID: "mail.notice.new_device.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.notice.new_device.title"),
				htmlPara(tf, "mail.notice.new_device.body", anyMap(ph)),
				htmlList(
					tf("mail.notice.new_device.time", anyMap(ph)),
					tf("mail.notice.new_device.ip", anyMap(ph)),
				),
				htmlNote(tf, "mail.notice.new_device.note", anyMap(ph)),
			)
		},
	},
	TypeCredentialChanged: {
		subjectID: "mail.notice.credential.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.notice.credential.title"),
				htmlPara(tf, "mail.notice.credential.body", anyMap(ph)),
				htmlNote(tf, "mail.notice.credential.note", anyMap(ph)),
			)
		},
	},
	TypeAccountStatus: {
		subjectID: "mail.notice.account.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.notice.account.title"),
				htmlPara(tf, "mail.notice.account.body", anyMap(ph)),
				htmlNote(tf, "mail.notice.account.note", anyMap(ph)),
			)
		},
	},
	TypeDeckShared: {
		subjectID: "mail.deck_shared.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.deck_shared.title"),
				htmlPara(tf, "mail.deck_shared.body", anyMap(ph)),
				htmlCTA(tf, "mail.deck_shared.link_label", "url"),
			)
		},
	},
	TypeDeckPermissionChanged: {
		subjectID: "mail.deck_permission.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			// An empty role means the access was revoked, not "changed to empty". has reads the real
			// variable, so the pre-filled editor (where every placeholder is non-empty) gets the
			// "granted" wording, exactly like a sender passing a role.
			lineID := "mail.deck_permission.granted"
			if !has("role") {
				lineID = "mail.deck_permission.revoked"
			}
			blocks := []string{
				htmlHeading(tf, "mail.deck_permission.title"),
				htmlPara(tf, lineID, anyMap(ph)),
			}
			if has("url") {
				blocks = append(blocks, htmlCTA(tf, "mail.deck_permission.link_label", "url"))
			}
			return htmlJoin(blocks...)
		},
	},
	TypeInvite: {
		subjectID: "mail.invite.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			note := tf("mail.invite.note", anyMap(ph))
			if has("expires") {
				note = tf("mail.invite.expires", anyMap(ph)) + " " + note
			}
			return htmlJoin(
				htmlHeading(tf, "mail.invite.title"),
				htmlPara(tf, "mail.invite.body", anyMap(ph)),
				htmlCTA(tf, "mail.invite.link_label", "url"),
				htmlNoteText(note),
			)
		},
	},
	TypeReviewReminder: {
		subjectID: "mail.reminder.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.reminder.title"),
				htmlPara(tf, "mail.reminder.body", anyMap(ph)),
				htmlCTA(tf, "mail.reminder.link_label", "url"),
			)
		},
	},
	TypeStudyDigest: {
		subjectID: "mail.digest.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			// Each statistics line writes `{{.count}}` but reads a different variable, so the value is
			// mapped per line; passing the whole table would let one line read another line's number.
			return htmlJoin(
				htmlHeading(tf, "mail.digest.title"),
				htmlPara(tf, "mail.digest.intro", nil),
				htmlList(
					tf("mail.digest.reviewed", map[string]any{"count": ph["count"]}),
					tf("mail.digest.pass_rate", map[string]any{"rate": ph["rate"]}),
					tf("mail.digest.streak", map[string]any{"days": ph["days"]}),
					tf("mail.digest.new_cards", map[string]any{"count": ph["new_cards"]}),
					tf("mail.digest.due", map[string]any{"count": ph["due"]}),
				),
				htmlCTA(tf, "mail.digest.link_label", "url"),
			)
		},
	},
	TypeOptimizeDone: {
		subjectID: "mail.optimize.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.optimize.title"),
				htmlPara(tf, "mail.optimize.body", anyMap(ph)),
				htmlCTA(tf, "mail.optimize.link_label", "url"),
			)
		},
	},
	TypeJobFailed: {
		subjectID: "mail.admin.job_failed.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.admin.job_failed.title"),
				htmlPara(tf, "mail.admin.job_failed.body", anyMap(ph)),
				htmlPara(tf, "mail.admin.job_failed.reason_label", nil),
				// The failure reason is an English technical string from the job runner, not copy: it is
				// kept in a preformatted block so line breaks in a stack trace survive.
				htmlPre("{{reason}}"),
				htmlNote(tf, "mail.admin.job_failed.note", anyMap(ph)),
			)
		},
	},
	TypeMediaDiskAlert: {
		subjectID: "mail.admin.media_alert.subject",
		body: func(tf TfFunc, ph Vars, has func(string) bool) string {
			return htmlJoin(
				htmlHeading(tf, "mail.admin.media_alert.title"),
				htmlPara(tf, "mail.admin.media_alert.body", anyMap(ph)),
				htmlNote(tf, "mail.admin.media_alert.note", anyMap(ph)),
			)
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
// The body always comes back in template form, for either variable table: pass PlaceholderVars(t) and
// the admin editor gets exactly what the sender's fallback renders. Real values only decide which
// optional sections exist (through has) and fill the subject, which is plain text and never goes
// through the renderer. The second return value says whether the type has a built-in wording
// (every type in the catalog should have one).
func DefaultTemplate(t Type, variant string, tf TfFunc, vars Vars) (subject, body string, ok bool) {
	spec, found := defaultSpecs[t]
	if !found {
		return "", "", false
	}
	ph := PlaceholderVars(t)
	has := func(name string) bool { return strings.TrimSpace(vars[name]) != "" }

	subjectID := spec.subjectID
	build := spec.body
	if t == TypeEmailVerification && variant == VariantEmailChange {
		v := spec.variants[VariantEmailChange]
		subjectID = v.subjectID
		build = v.body
	}

	// The body builder fills in the placeholder table for copy that carries variables, so a body
	// function never has to spell out anyMap(ph) and can never be handed a real value by mistake.
	bodyTF := func(id string, data map[string]any) string {
		if data == nil {
			data = anyMap(ph)
		}
		return tf(id, data)
	}
	return tf(subjectID, anyMap(vars)), build(bodyTF, ph, has), true
}

// htmlJoin concatenates already-rendered HTML blocks. Every helper returns its own trailing newline,
// so joining is appending; an empty block is dropped so an absent optional section leaves no gap.
func htmlJoin(blocks ...string) string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if strings.TrimSpace(b) == "" {
			continue
		}
		out = append(out, b)
	}
	return strings.Join(out, "")
}

// htmlHeading renders the page title. The mail uses h2 as its page title; h1 is left for custom
// templates that want a larger one.
func htmlHeading(tf TfFunc, id string) string {
	return "<h2 style=\"" + mailstyle.StyleAttr("h2") + "\">" + inlineHTML(tf(id, nil)) + "</h2>\n"
}

// htmlPara renders one paragraph of copy.
func htmlPara(tf TfFunc, id string, data map[string]any) string {
	return "<p style=\"" + mailstyle.StyleAttr("p") + "\">" + inlineHTML(tf(id, data)) + "</p>\n"
}

// htmlNote renders copy as a blockquote: the note is supplementary information, not a warning, so it
// gets a quiet gray style rather than a coloured box.
func htmlNote(tf TfFunc, id string, data map[string]any) string {
	return htmlNoteText(tf(id, data))
}

// htmlNoteText renders already-resolved copy as a blockquote, for a note assembled from more than one
// language pack entry.
func htmlNoteText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "<blockquote style=\"" + mailstyle.StyleAttr("blockquote") + "\">\n" +
		"<p style=\"" + mailstyle.StyleAttr("p") + "\">" + inlineHTML(text) + "</p>\n" +
		"</blockquote>\n"
}

// htmlCTA renders a call-to-action link. The target is always the `{{url}}`-style placeholder: the
// real address is substituted after template rendering, so it is HTML-escaped by context and can
// never inject markup. The anchor is the only content of its paragraph, which is the button rule the
// mail styles use.
func htmlCTA(tf TfFunc, labelID, urlVar string) string {
	label := tf(labelID, nil)
	if strings.TrimSpace(label) == "" {
		return ""
	}
	return "<p style=\"" + mailstyle.StyleAttr("p") + "\">" +
		"<a href=\"{{" + urlVar + "}}\" style=\"" + mailstyle.Button + "\">" + inlineHTML(label) + "</a>" +
		"</p>\n"
}

// htmlList renders the mail's list. A list line is language-pack copy shaped as `**label** value`
// (the bold convention of the built-in copy); empty lines are dropped so a line with no value leaves
// no bullet.
func htmlList(lines ...string) string {
	items := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		items = append(items, "<li style=\""+mailstyle.StyleAttr("li")+"\">"+inlineHTML(line)+"</li>\n")
	}
	if len(items) == 0 {
		return ""
	}
	return "<ul style=\"" + mailstyle.StyleAttr("ul") + "\">\n" + strings.Join(items, "") + "</ul>\n"
}

// htmlPre renders a preformatted block. The argument is template text (a `{{name}}` placeholder),
// not copy: the value is substituted after template rendering, so it must not be escaped here.
func htmlPre(text string) string {
	return "<pre style=\"" + mailstyle.StyleAttr("pre") + "\"><code style=\"" + mailstyle.StyleAttr("code") + "\">" +
		text + "\n</code></pre>\n"
}

// boldMarkup matches the `**...**` convention the built-in list copy uses for its labels.
var boldMarkup = regexp.MustCompile(`\*\*([^*]+)\*\*`)

// inlineHTML escapes copy for HTML text and turns the built-in copy's `**label**` convention into a
// styled strong element. The copy comes from the language packs, so it is text, never markup.
func inlineHTML(text string) string {
	escaped := html.EscapeString(text)
	return boldMarkup.ReplaceAllString(escaped, "<strong style=\""+mailstyle.StyleAttr("strong")+"\">$1</strong>")
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
