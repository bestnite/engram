package mail

import (
	"context"
	"mime"
	"strings"
	"testing"
)

// TestSMTPSenderEncodesNonASCIIHeaders 断言非 ASCII 的主题与显示名按 RFC 2047 编码。
//
// 头部按 RFC 5322 只能是 ASCII：中文主题原样写进 Subject 会被中继或客户端压成 "?"，
// 收件人看到一串问号（实测收到过 "?????? Engram ??????????????????"）。同时地址本身
// 必须保持 ASCII —— encoded-word 不允许出现在 addr-spec 里。
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
	// 解码回来必须与原文逐字相同。
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

	// 地址必须是 ASCII 可解析的，不能被 encoded-word 包住。
	for _, addr := range []string{"<no-reply@example.com>", "<learner@example.com>"} {
		if !strings.Contains(got, addr) {
			t.Errorf("delivered message lost the plain address %s:\n%s", addr, got)
		}
	}
}

// TestSMTPSenderLeavesASCIIHeadersAlone 断言纯 ASCII 头部不被编码（不引入无谓的 =?utf-8?）。
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

// headerLine 取出以 prefix 开头的第一行（去掉换行与首尾空白）。
func headerLine(msg, prefix string) string {
	for _, line := range strings.Split(msg, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
