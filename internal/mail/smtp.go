package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SMTP transport implementation.
//
// It uses the standard library net/smtp: it covers the full EHLO / STARTTLS / AUTH / DATA handshake
// and adds no dependencies (the AGENTS.md selection principle: if a well-maintained wheel exists, use
// it). Implicit TLS (465) needs a crypto/tls connection established first and then handed to
// smtp.NewClient, which the standard library does not offer directly, so it is assembled by hand
// here.

// DefaultTimeout is the timeout of a single SMTP interaction (connect + handshake + delivery).
const DefaultTimeout = 30 * time.Second

// Sender is the transport abstraction for delivering one mail; tests can inject a stand-in and thus
// avoid a real SMTP service.
type Sender interface {
	// Send delivers one mail; the returned error should carry the server error text for retries and
	// display.
	Send(ctx context.Context, cfg SMTPConfig, m Message) error
	// Test only performs the connect and auth handshake to verify the configuration works; it surfaces
	// the server error text the same way.
	Test(ctx context.Context, cfg SMTPConfig) error
}

// SMTPSender is the default implementation of Sender (net/smtp).
type SMTPSender struct {
	// Timeout is the interaction timeout; <=0 means DefaultTimeout.
	Timeout time.Duration
}

func (s SMTPSender) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

// Addr returns the address in host:port form.
func (c SMTPConfig) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Send performs one delivery: connect -> auth -> MAIL/RCPT/DATA -> QUIT. Any step that fails returns
// an error prefixed with the stage, with the server text preserved verbatim (the admin panel shows
// it).
func (s SMTPSender) Send(ctx context.Context, cfg SMTPConfig, m Message) error {
	client, err := s.dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := s.authenticate(client, cfg); err != nil {
		return err
	}
	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := client.Rcpt(m.To); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(buildMessage(cfg, m)); err != nil {
		_ = w.Close()
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finish message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("QUIT: %w", err)
	}
	return nil
}

// Test only connects and authenticates, then QUITs; used by the admin panel's "test connection".
func (s SMTPSender) Test(ctx context.Context, cfg SMTPConfig) error {
	client, err := s.dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := s.authenticate(client, cfg); err != nil {
		return err
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("QUIT: %w", err)
	}
	return nil
}

// TestConnection tests the connection with the default SMTPSender, for the admin panel to call
// directly.
func TestConnection(ctx context.Context, cfg SMTPConfig) error {
	return SMTPSender{}.Test(ctx, cfg)
}

// dial opens the connection to the SMTP server according to the TLS mode.
func (s SMTPSender) dial(ctx context.Context, cfg SMTPConfig) (*smtp.Client, error) {
	addr := cfg.Addr()
	d := net.Dialer{Timeout: s.timeout()}
	if NormalizeTLSMode(cfg.TLSMode) == TLSModeImplicit {
		conn, err := tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return nil, fmt.Errorf("connect %s (implicit TLS): %w", addr, err)
		}
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("smtp handshake: %w", err)
		}
		return client, nil
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", addr, err)
	}
	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("smtp handshake: %w", err)
	}
	if NormalizeTLSMode(cfg.TLSMode) == TLSModeStartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("STARTTLS: %w", err)
		}
	}
	return client, nil
}

// authenticate does PLAIN auth when a username/password is configured; when both are empty it is
// skipped (an unauthenticated relay).
func (s SMTPSender) authenticate(client *smtp.Client, cfg SMTPConfig) error {
	if cfg.Username == "" && cfg.Password == "" {
		return nil
	}
	if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
		return fmt.Errorf("AUTH: %w", err)
	}
	return nil
}

// buildMessage assembles the RFC 5322 message bytes. When TextBody and HTMLBody are both present it
// uses multipart/alternative. Extra headers are emitted sorted by name, so the same input produces the
// same bytes (which makes test assertions easy).
func buildMessage(cfg SMTPConfig, m Message) []byte {
	var b bytes.Buffer
	// Headers are encoded per RFC 2047: a non-ASCII subject/display name sent as is gets squashed into
	// "?" (see encodeHeaderValue).
	writeHeader(&b, "From", encodeAddress(cfg.From))
	writeHeader(&b, "To", encodeAddress(m.To))
	writeHeader(&b, "Subject", encodeHeaderValue(m.Subject))
	writeHeader(&b, "Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader(&b, "MIME-Version", "1.0")

	hasText := strings.TrimSpace(m.TextBody) != ""
	hasHTML := strings.TrimSpace(m.HTMLBody) != ""
	switch {
	case hasText && hasHTML:
		const boundary = "engram-mail-boundary"
		writeHeader(&b, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
		for _, k := range sortedHeaderKeys(m.Headers) {
			writeHeader(&b, k, encodeHeaderValue(m.Headers[k]))
		}
		b.WriteString("\r\n")
		b.WriteString("--" + boundary + "\r\n")
		writeHeader(&b, "Content-Type", "text/plain; charset=utf-8")
		b.WriteString("\r\n")
		b.WriteString(m.TextBody)
		b.WriteString("\r\n")
		b.WriteString("--" + boundary + "\r\n")
		writeHeader(&b, "Content-Type", "text/html; charset=utf-8")
		b.WriteString("\r\n")
		b.WriteString(m.HTMLBody)
		b.WriteString("\r\n")
		b.WriteString("--" + boundary + "--\r\n")
	case hasHTML:
		writeHeader(&b, "Content-Type", "text/html; charset=utf-8")
		for _, k := range sortedHeaderKeys(m.Headers) {
			writeHeader(&b, k, encodeHeaderValue(m.Headers[k]))
		}
		b.WriteString("\r\n")
		b.WriteString(m.HTMLBody)
	default:
		writeHeader(&b, "Content-Type", "text/plain; charset=utf-8")
		for _, k := range sortedHeaderKeys(m.Headers) {
			writeHeader(&b, k, encodeHeaderValue(m.Headers[k]))
		}
		b.WriteString("\r\n")
		b.WriteString(m.TextBody)
	}
	return b.Bytes()
}

// encodeHeaderValue encodes header values that need it per RFC 2047.
//
// A header may only be ASCII under RFC 5322: writing a Chinese subject straight into Subject gets it
// squashed into "?" by a relay or client, and what the recipient sees is a row of question marks (a
// real-world example: "?????? Engram ??????????????????"). mime.QEncoding.Encode returns purely
// ASCII input unchanged, so it can be called on every text header without thinking.
func encodeHeaderValue(value string) string {
	if isASCII(value) {
		return value
	}
	return mime.QEncoding.Encode("utf-8", value)
}

// encodeAddress MIME-encodes only the display name in "display name <address>". The address itself
// (addr-spec) must be ASCII, and an RFC 2047 encoded-word is not allowed there; encoding the whole
// string would make some clients unable to parse the recipient. A bare address is returned as is.
func encodeAddress(value string) string {
	lt := strings.LastIndex(value, "<")
	if lt < 0 {
		return encodeHeaderValue(value)
	}
	name := strings.TrimSpace(value[:lt])
	addr := value[lt:]
	if name == "" {
		return addr
	}
	return encodeHeaderValue(name) + " " + addr
}

// isASCII reports whether s is entirely ASCII; if so no MIME encoding is needed.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

func writeHeader(b *bytes.Buffer, name, value string) {
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString("\r\n")
}

func sortedHeaderKeys(h map[string]string) []string {
	if len(h) == 0 {
		return nil
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
