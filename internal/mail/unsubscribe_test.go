package mail

import "testing"

// TestUnsubscribeHeadersOnlyForOptionalTypes 是验收项「A 类不带退订头」的单元证据：
// 可选类型（B/C/D，CanDisable 为真）才得到两个 RFC 8058 头；A 类与未登记类型一律返回 nil。
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

	// A 类：不可关闭 → 绝不带退订头。
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

	// 未登记的类型：既不发信也不带退订头。
	if h := UnsubscribeHeaders(Type("no_such_type"), url); h != nil {
		t.Errorf("UnsubscribeHeaders(unknown) = %v, want nil", h)
	}
	// 空 URL：拿不到退订地址时不写半个头。
	if h := UnsubscribeHeaders(TypeInvite, "  "); h != nil {
		t.Errorf("UnsubscribeHeaders(invite, empty url) = %v, want nil", h)
	}
}
