package mail

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net"
	netmail "net/mail"
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

// TestBuildMessageBoundaryCannotBeInjected covers the multipart boundary: a fixed boundary let a body
// that quotes it split the message into an extra part. Both bodies here contain the old constant and
// an "INJECTED PART" line, so a boundary that still collides would show up as a third part.
func TestBuildMessageBoundaryCannotBeInjected(t *testing.T) {
	cfg := SMTPConfig{From: "no-reply@example.com"}
	// The value follows the reported path: a user-supplied name reaches Render, whose parts are then
	// assembled into the SMTP message.
	payload := "My deck\r\n--engram-mail-boundary\r\nContent-Type: text/plain\r\n\r\nINJECTED PART\r\n--engram-mail-boundary--"
	rendered, err := Render(RenderInput{
		Type:    TypePasswordReset,
		Site:    "Example",
		Lang:    "en",
		Vars:    Vars{"url": "https://example.com/reset", "expires": "tomorrow", "site": payload},
		Subject: "boundary",
		BodyMD:  "<p>{{site}}</p>",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	m := Message{
		To:       "learner@example.com",
		Subject:  rendered.Subject,
		TextBody: rendered.Text,
		HTMLBody: rendered.HTML,
	}

	msg, boundary := builtMessageAndBoundary(t, cfg, m)
	if ct := msg.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/alternative") {
		t.Fatalf("Content-Type = %q, want a multipart/alternative value", ct)
	}
	if boundary == "engram-mail-boundary" {
		t.Errorf("boundary = %q, want a per-message value rather than the old constant", boundary)
	}
	for name, body := range map[string]string{"text": m.TextBody, "html": m.HTMLBody} {
		if strings.Contains(body, boundary) {
			t.Errorf("%s body contains the chosen boundary %q", name, boundary)
		}
	}

	reader := multipart.NewReader(msg.Body, boundary)
	textPart, err := reader.NextPart()
	if err != nil {
		t.Fatalf("read text part: %v", err)
	}
	if ct := textPart.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("first part Content-Type = %q, want text/plain", ct)
	}
	textPayload, err := io.ReadAll(textPart)
	if err != nil {
		t.Fatalf("read text payload: %v", err)
	}
	if !strings.Contains(string(textPayload), "INJECTED PART") {
		t.Errorf("text payload = %q, want the injected line inside it", textPayload)
	}

	htmlPart, err := reader.NextPart()
	if err != nil {
		t.Fatalf("read html part: %v", err)
	}
	if ct := htmlPart.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("second part Content-Type = %q, want text/html", ct)
	}
	htmlPayload, err := io.ReadAll(htmlPart)
	if err != nil {
		t.Fatalf("read html payload: %v", err)
	}
	if !strings.Contains(string(htmlPayload), "INJECTED PART") {
		t.Errorf("html payload = %q, want the injected line inside it", htmlPayload)
	}

	if _, err := reader.NextPart(); err != io.EOF {
		t.Errorf("third part error = %v, want io.EOF (exactly two parts)", err)
	}

	// The boundary is per message: two builds of the same input must not share it.
	_, again := builtMessageAndBoundary(t, cfg, m)
	if again == boundary {
		t.Errorf("two builds chose the same boundary %q, want a per-message value", boundary)
	}
}

// builtMessageAndBoundary builds one message, parses its headers and returns the message plus the
// boundary from its Content-Type.
func builtMessageAndBoundary(t *testing.T, cfg SMTPConfig, m Message) (*netmail.Message, string) {
	t.Helper()
	msg, err := netmail.ReadMessage(bytes.NewReader(buildMessage(cfg, m)))
	if err != nil {
		t.Fatalf("parse built message: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse Content-Type %q: %v", msg.Header.Get("Content-Type"), err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("media type = %q, want multipart/alternative", mediaType)
	}
	boundary := params["boundary"]
	if boundary == "" {
		t.Fatal("Content-Type carries no boundary parameter")
	}
	return msg, boundary
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
