package mail

import (
	"context"
	"net"
	"strings"
	"testing"
)

// TestSMTPSenderDeliversAndBuildsMessage 用进程内假 SMTP 服务验证真实传输层：
// 投递成功、正文与自定义头出现在报文里。
func TestSMTPSenderDeliversAndBuildsMessage(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := SMTPConfig{
		Host: f.host(), Port: f.port(), From: "no-reply@example.com", TLSMode: TLSModeNone,
	}
	err := SMTPSender{}.Send(context.Background(), cfg, Message{
		To: "learner@example.com", Subject: "Hello", TextBody: "plain body",
		Headers: map[string]string{"X-Engram-Type": "invite"},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	got := f.received()
	if len(got) != 1 {
		t.Fatalf("received %d messages, want 1", len(got))
	}
	for _, want := range []string{"Subject: Hello", "plain body", "X-Engram-Type: invite"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("delivered message is missing %q:\n%s", want, got[0])
		}
	}
}

// TestSMTPSenderSurfacesServerError 证明服务端错误文本被原样带出（供管理面板的测试连接显示）。
func TestSMTPSenderSurfacesServerError(t *testing.T) {
	f := newFakeSMTP(t)
	f.setRcptReject(true)
	cfg := SMTPConfig{
		Host: f.host(), Port: f.port(), From: "no-reply@example.com", TLSMode: TLSModeNone,
	}
	err := SMTPSender{}.Send(context.Background(), cfg, Message{To: "nobody@example.com", Subject: "x"})
	if err == nil {
		t.Fatal("Send() error = nil, want the server's rejection")
	}
	if !strings.Contains(err.Error(), "550") {
		t.Errorf("error %q does not carry the server's status text", err)
	}
}

// TestTestConnectionFailsOnClosedPort 覆盖"错主机"路径：连接被拒时把错误文本带出。
func TestTestConnectionFailsOnClosedPort(t *testing.T) {
	addr := deadAddr(t)
	host, port := splitHostPort(t, addr)
	cfg := SMTPConfig{Host: host, Port: atoi(t, port), From: "no-reply@example.com", TLSMode: TLSModeNone}
	err := TestConnection(context.Background(), cfg)
	if err == nil {
		t.Fatal("TestConnection() error = nil, want a connection error")
	}
	if !strings.Contains(err.Error(), "connect") {
		t.Errorf("error %q does not name the failed connect stage", err)
	}
}

// TestTestConnectionSucceeds 覆盖成功路径。
func TestTestConnectionSucceeds(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := SMTPConfig{Host: f.host(), Port: f.port(), From: "no-reply@example.com", TLSMode: TLSModeNone}
	if err := TestConnection(context.Background(), cfg); err != nil {
		t.Fatalf("TestConnection() error = %v, want nil", err)
	}
}

// deadAddr 返回一个当前无人监听的本地地址（先监听再关闭，端口随即释放）。
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// atoi 是测试里受限的端口解析。
func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("atoi(%q): not a number", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}
