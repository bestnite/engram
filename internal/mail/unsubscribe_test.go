package mail

import "testing"

// TestUnsubscribeHeadersOnlyForOptionalTypes is the unit-level evidence for the acceptance item
// "class A carries no unsubscribe header": only optional types (B/C/D, where CanDisable is true) get
// the two RFC 8058 headers; class A and unregistered types always return nil.
func TestUnsubscribeHeadersOnlyForOptionalTypes(t *testing.T) {
	const url = "https://example.com/unsubscribe?token=abc"

	for _, typ := range []Type{
		TypeInvite, TypeDeckShared, TypeDeckPermissionChanged, // B
		TypeReviewReminder, TypeStudyDigest, // C
		TypeJobFailed, TypeMediaDiskAlert, // D
	} {
		if !CanUnsubscribe(typ) {
			t.Errorf("CanUnsubscribe(%s) = false, want true (optional type)", typ)
		}
		h := UnsubscribeHeaders(typ, url)
		if h == nil {
			t.Errorf("UnsubscribeHeaders(%s) = nil, want RFC 8058 headers", typ)
			continue
		}
		if got := h[HeaderListUnsubscribe]; got != "<"+url+">" {
			t.Errorf("%s: List-Unsubscribe = %q, want %q", typ, got, "<"+url+">")
		}
		if got := h[HeaderListUnsubscribePost]; got != ListUnsubscribePostValue {
			t.Errorf("%s: List-Unsubscribe-Post = %q, want %q", typ, got, ListUnsubscribePostValue)
		}
	}

	// Class A: cannot be disabled -> never carries an unsubscribe header.
	for _, typ := range []Type{
		TypePasswordReset, TypeEmailVerification, TypeNewDeviceLogin,
		TypeCredentialChanged, TypeAccountStatus,
	} {
		if CanUnsubscribe(typ) {
			t.Errorf("CanUnsubscribe(%s) = true, want false (class A cannot be disabled)", typ)
		}
		if h := UnsubscribeHeaders(typ, url); h != nil {
			t.Errorf("UnsubscribeHeaders(%s) = %v, want nil (class A carries no unsubscribe header)", typ, h)
		}
	}

	// Unregistered types: neither send mail nor carry an unsubscribe header.
	if h := UnsubscribeHeaders(Type("no_such_type"), url); h != nil {
		t.Errorf("UnsubscribeHeaders(unknown) = %v, want nil", h)
	}
	// Empty URL: write no half header when no unsubscribe address is available.
	if h := UnsubscribeHeaders(TypeInvite, "  "); h != nil {
		t.Errorf("UnsubscribeHeaders(invite, empty url) = %v, want nil", h)
	}
}
