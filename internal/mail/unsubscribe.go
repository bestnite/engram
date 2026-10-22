package mail

import "strings"

// This file is the header side of (RFC 8058 one-click unsubscribe): only "optional types" carry an
// unsubscribe header.
//
// The classification still rests on Class from the catalog: only a class whose CanDisable is true
// (B/C/D) may be unsubscribed from by the user, which is why the mail carries an unsubscribe entry at
// all; class A security/transactional mail never carries one, since allowing security mail to be
// unsubscribed is like leaving a back door.
//
// Only the bare headers are built here (no token issuance, no database access): the sender issues the
// token with ActionTokenService, and the plaintext only ever appears in the unsubscribe URL, never in
// a log. Senders just call UnsubscribeHeaders with the URL; the header names and values are defined
// only here, so B/C/D cannot drift apart in spelling.

// The two RFC 8058 unsubscribe header names; the values are English and stable (mail protocol fields,
// not localized).
const (
	// HeaderListUnsubscribe points at the one-click unsubscribe address (the https URL of RFC 8058).
	HeaderListUnsubscribe = "List-Unsubscribe"
	// HeaderListUnsubscribePost declares that the address supports One-Click POST (RFC 8058).
	HeaderListUnsubscribePost = "List-Unsubscribe-Post"
)

// ListUnsubscribePostValue is the fixed value of List-Unsubscribe-Post (RFC 8058 §3.1). Mail clients
// use that value to POST to the URL in List-Unsubscribe with a body equal to it.
const ListUnsubscribePostValue = "List-Unsubscribe=One-Click"

// CanUnsubscribe reports whether a type is an "optional type": registered in the catalog and in a
// class the user can turn off. An unregistered type returns false (a mistyped type name neither sends
// mail nor carries an unsubscribe header).
func CanUnsubscribe(t Type) bool {
	def, ok := Lookup(t)
	if !ok {
		return false
	}
	return CanDisable(def.Class)
}

// UnsubscribeHeaders builds the RFC 8058 unsubscribe headers for optional-type mail.
//
// url is the absolute unsubscribe address carrying the one-time token (assembled by the sender); when
// it is empty or the type is not unsubscribable, it returns nil and the caller therefore writes no
// Headers -- so class A mail can never carry an unsubscribe header.
func UnsubscribeHeaders(t Type, url string) map[string]string {
	if !CanUnsubscribe(t) {
		return nil
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return nil
	}
	return map[string]string{
		HeaderListUnsubscribe:     "<" + url + ">",
		HeaderListUnsubscribePost: ListUnsubscribePostValue,
	}
}
