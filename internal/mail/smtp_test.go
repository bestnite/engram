package mail

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
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

// silentRelay accepts every connection and never writes a byte, the state a stalled SMTP relay
// leaves net/smtp in: it waits for the greeting with no deadline of its own.
func silentRelay(t *testing.T) (SMTPConfig, <-chan net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	conns := make(chan net.Conn, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conns <- conn
		}
	}()
	host, port := splitHostPort(t, ln.Addr().String())
	return SMTPConfig{Host: host, Port: atoi(t, port), From: "no-reply@example.com", TLSMode: TLSModeNone}, conns
}

// TestSMTPSenderBoundsAStalledRelay covers the configured timeout: a relay that accepts the
// connection and then stops talking must not hold the delivery past Timeout, and the sender must
// still deliver the next message.
func TestSMTPSenderBoundsAStalledRelay(t *testing.T) {
	cfg, conns := silentRelay(t)
	sender := SMTPSender{Timeout: 200 * time.Millisecond}

	start := time.Now()
	errc := make(chan error, 1)
	go func() {
		errc <- sender.Send(context.Background(), cfg, Message{To: "learner@example.com", Subject: "stalled"})
	}()
	var err error
	select {
	case err = <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("Send() did not return within 2s with Timeout=200ms: the stalled relay held the delivery")
	}
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Send() error = nil, want the stalled relay to be refused")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Send() returned after %v with Timeout=200ms, want it bounded by the timeout", elapsed)
	}
	select {
	case conn := <-conns:
		_ = conn.Close()
	default:
	}

	f := newFakeSMTP(t)
	working := SMTPConfig{Host: f.host(), Port: f.port(), From: "no-reply@example.com", TLSMode: TLSModeNone}
	if err := sender.Send(context.Background(), working, Message{To: "learner@example.com", Subject: "next"}); err != nil {
		t.Fatalf("Send() after the stalled relay = %v, want the next round to deliver", err)
	}
	if got := f.received(); len(got) != 1 {
		t.Errorf("received %d messages after the stalled relay, want 1", len(got))
	}
}

// TestSMTPSenderStopsOnContextCancellation covers the other bound: a context that ends while the
// client waits for the greeting closes the connection instead of waiting for the long timeout.
func TestSMTPSenderStopsOnContextCancellation(t *testing.T) {
	cfg, conns := silentRelay(t)
	sender := SMTPSender{Timeout: 30 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	errc := make(chan error, 1)
	go func() {
		errc <- sender.Test(ctx, cfg)
	}()
	var err error
	select {
	case err = <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("Test() did not return within 2s of the context being cancelled")
	}
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Test() error = nil, want the cancelled context to end the handshake")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Test() returned after %v with a 30s timeout, want the context to close the connection", elapsed)
	}
	select {
	case conn := <-conns:
		_ = conn.Close()
	default:
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
