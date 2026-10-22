package mail

import (
	"context"
	"net"
	"strings"
	"testing"
)

// TestSMTPSenderDeliversAndBuildsMessage verifies the real transport layer against an in-process
// fake SMTP service: delivery succeeds, and the body and custom headers appear in the message.
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

// TestSMTPSenderSurfacesServerError shows that the server error text is surfaced verbatim (for the
// admin panel's test connection to display).
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

// TestTestConnectionFailsOnClosedPort covers the "wrong host" path: when the connection is refused,
// the error text is surfaced.
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

// TestTestConnectionSucceeds covers the success path.
func TestTestConnectionSucceeds(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := SMTPConfig{Host: f.host(), Port: f.port(), From: "no-reply@example.com", TLSMode: TLSModeNone}
	if err := TestConnection(context.Background(), cfg); err != nil {
		t.Fatalf("TestConnection() error = %v, want nil", err)
	}
}

// deadAddr returns a local address nobody is listening on right now (listen first, then close, and
// the port is released immediately).
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

// atoi is the restricted port parsing used in tests.
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
