package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SMTP 传输实现（DESIGN.md §4.7；AGENTS.md M1-17）。
//
// 用标准库 net/smtp：它覆盖 EHLO / STARTTLS / AUTH / DATA 的完整握手，且没有额外依赖
// （AGENTS.md 的选型原则：轮子已存在且维护良好就用它）。隐式 TLS（465）需要先建
// crypto/tls 连接再交给 smtp.NewClient，标准库不直接提供，这里手工拼。

// DefaultTimeout 是单次 SMTP 交互（连接 + 握手 + 投递）的超时。
const DefaultTimeout = 30 * time.Second

// Sender 是投递一封邮件的传输抽象；测试可注入替身，从而不依赖真实 SMTP 服务。
type Sender interface {
	// Send 投递一封邮件；返回的 error 应携带服务端错误文本，供重试与展示。
	Send(ctx context.Context, cfg SMTPConfig, m Message) error
	// Test 只做连接与认证握手，验证配置可用；同样把服务端错误文本带出来。
	Test(ctx context.Context, cfg SMTPConfig) error
}

// SMTPSender 是 Sender 的默认实现（net/smtp）。
type SMTPSender struct {
	// Timeout 是交互超时；<=0 时用 DefaultTimeout。
	Timeout time.Duration
}

func (s SMTPSender) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

// Addr 返回 host:port 形式的地址。
func (c SMTPConfig) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Send 完成一次投递：连接 -> 认证 -> MAIL/RCPT/DATA -> QUIT。
// 任何一步失败都返回带阶段前缀的错误，服务端文本原样保留（管理面板要显示它）。
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

// Test 只做连接与认证，随后 QUIT；用于管理面板的"测试连接"。
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

// TestConnection 用默认 SMTPSender 测试连接，供管理面板直接调用。
func TestConnection(ctx context.Context, cfg SMTPConfig) error {
	return SMTPSender{}.Test(ctx, cfg)
}

// dial 按 TLS 模式建立到 SMTP 服务器的连接。
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

// authenticate 在配置了口令/用户名时做 PLAIN 认证；都为空则跳过（无认证中继）。
func (s SMTPSender) authenticate(client *smtp.Client, cfg SMTPConfig) error {
	if cfg.Username == "" && cfg.Password == "" {
		return nil
	}
	if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
		return fmt.Errorf("AUTH: %w", err)
	}
	return nil
}

// buildMessage 组装 RFC 5322 邮件字节。TextBody/HTMLBody 同时存在时用 multipart/alternative。
// 附加头按名称排序输出，保证同样的输入产生同样的字节（便于测试断言）。
func buildMessage(cfg SMTPConfig, m Message) []byte {
	var b bytes.Buffer
	writeHeader(&b, "From", cfg.From)
	writeHeader(&b, "To", m.To)
	writeHeader(&b, "Subject", m.Subject)
	writeHeader(&b, "Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader(&b, "MIME-Version", "1.0")

	hasText := strings.TrimSpace(m.TextBody) != ""
	hasHTML := strings.TrimSpace(m.HTMLBody) != ""
	switch {
	case hasText && hasHTML:
		const boundary = "engram-mail-boundary"
		writeHeader(&b, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
		for _, k := range sortedHeaderKeys(m.Headers) {
			writeHeader(&b, k, m.Headers[k])
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
			writeHeader(&b, k, m.Headers[k])
		}
		b.WriteString("\r\n")
		b.WriteString(m.HTMLBody)
	default:
		writeHeader(&b, "Content-Type", "text/plain; charset=utf-8")
		for _, k := range sortedHeaderKeys(m.Headers) {
			writeHeader(&b, k, m.Headers[k])
		}
		b.WriteString("\r\n")
		b.WriteString(m.TextBody)
	}
	return b.Bytes()
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
