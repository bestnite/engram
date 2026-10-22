package mail

import (
	"context"
	"mime"
	"strings"
	"testing"
)

// TestSMTPSenderEncodesNonASCIIHeaders asserts that a non-ASCII subject and display name are encoded
// per RFC 2047.
//
// A header may only be ASCII under RFC 5322: a Chinese subject written straight into Subject gets
// squashed into "?" by a relay or client, and the recipient sees a row of question marks (a
// real-world example: "?????? Engram ??????????????????"). At the same time the address itself must
// stay ASCII -- an encoded-word is not allowed in addr-spec.
func TestSMTPSenderEncodesNonASCIIHeaders(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := SMTPConfig{
		Host: f.host(), Port: f.port(), TLSMode: TLSModeNone,
		From: "Engram 服务 <no-reply@example.com>",
	}
	const subject = "你的复习提醒：3 张卡到期"
	if err := (SMTPSender{}).Send(context.Background(), cfg, Message{
		To:       "学习者 <learner@example.com>",
		Subject:  subject,
		TextBody: "body",
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	got := f.received()[0]

	if !strings.Contains(got, "Subject: =?utf-8?") {
		t.Errorf("subject is not RFC 2047 encoded:\n%s", got)
	}
	// Decoding it back must match the original character for character.
	raw := headerLine(got, "Subject: ")
	if raw == "" {
		t.Fatalf("no Subject header in the delivered message:\n%s", got)
	}
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(raw)
	if err != nil {
		t.Fatalf("DecodeHeader(%q) error = %v", raw, err)
	}
	if decoded != subject {
		t.Errorf("decoded subject = %q, want %q", decoded, subject)
	}

	// The address must be ASCII-parseable and must not be wrapped in an encoded-word.
	for _, addr := range []string{"<no-reply@example.com>", "<learner@example.com>"} {
		if !strings.Contains(got, addr) {
			t.Errorf("delivered message lost the plain address %s:\n%s", addr, got)
		}
	}
}

// TestSMTPSenderLeavesASCIIHeadersAlone asserts that purely ASCII headers are not encoded (no
// unnecessary =?utf-8? is introduced).
func TestSMTPSenderLeavesASCIIHeadersAlone(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := SMTPConfig{
		Host: f.host(), Port: f.port(), TLSMode: TLSModeNone,
		From: "Engram <no-reply@example.com>",
	}
	if err := (SMTPSender{}).Send(context.Background(), cfg, Message{
		To: "learner@example.com", Subject: "Your review reminder", TextBody: "body",
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	got := f.received()[0]
	if !strings.Contains(got, "Subject: Your review reminder") {
		t.Errorf("ASCII subject was rewritten:\n%s", got)
	}
	if strings.Contains(got, "=?utf-8?") {
		t.Errorf("ASCII-only message carries an encoded word:\n%s", got)
	}
}

// headerLine returns the first line starting with prefix (newline and surrounding blanks stripped).
func headerLine(msg, prefix string) string {
	for _, line := range strings.Split(msg, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
