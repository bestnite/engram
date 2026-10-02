package web

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/mail"
	"git.nite07.com/nite/engram/internal/store"
)

// startFakeSMTP 在测试进程内起一个最小 SMTP 服务，只应答投递所需命令。
// 返回 "host:port"；测试结束自动关闭。不依赖外网或真实 SMTP 服务。
func startFakeSMTP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				w := bufio.NewWriter(c)
				say := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
				say("220 fake ready")
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					if inData {
						if line == "." {
							inData = false
							say("250 accepted")
						}
						continue
					}
					switch {
					case strings.HasPrefix(strings.ToUpper(line), "EHLO"),
						strings.HasPrefix(strings.ToUpper(line), "HELO"):
						say("250 greet")
					case strings.HasPrefix(strings.ToUpper(line), "DATA"):
						say("354 go")
						inData = true
					case strings.HasPrefix(strings.ToUpper(line), "QUIT"):
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// deadAddr 返回一个当前无人监听的本地地址。
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

// TestAdminSMTPPageShowsUnconfigured 是 M1-17 的机制层验收：未配置时页面明确说"未配置"，
// 而不是静默。
func TestAdminSMTPPageShowsUnconfigured(t *testing.T) {
	srv, _, _, cookies, _ := newNotesServer(t)
	srv.secrets = mustCodec(t)

	rec := getWithCookies(t, srv, "/admin/smtp", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/smtp = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	// 精简后的说明只保留「未配置时哪些流程会被禁用」这一条有用信息（文案从简）。
	if !strings.Contains(body, "未配置 SMTP 时") {
		t.Errorf("SMTP page does not explain what stays disabled without SMTP; body = %s", snippet(body))
	}
	if !strings.Contains(body, "未配置") {
		t.Errorf("SMTP page does not show the not-configured status; body = %s", snippet(body))
	}
}

// TestAdminSMTPSaveAndShowConfigured 覆盖保存：填 host/from/口令后页面显示已配置，
// 且口令明文绝不出现在页面上或库里。
func TestAdminSMTPSaveAndShowConfigured(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)

	const pw = "smtp-test-plaintext-pw"
	rec := postForm(t, srv, "/admin/smtp", url.Values{
		"csrf_token":    {csrf},
		"smtp_host":     {"smtp.example.com"},
		"smtp_port":     {"587"},
		"smtp_from":     {"no-reply@example.com"},
		"smtp_username": {"mailer"},
		"smtp_password": {pw},
		"smtp_tls_mode": {"starttls"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/smtp = %d, want 303 (body %s)", rec.Code, snippet(rec.Body.String()))
	}

	page := getWithCookies(t, srv, "/admin/smtp", cookies)
	body := page.Body.String()
	if !strings.Contains(body, "smtp.example.com") {
		t.Errorf("saved host is not shown; body = %s", snippet(body))
	}
	if !strings.Contains(body, "已配置") {
		t.Errorf("password does not show as configured; body = %s", snippet(body))
	}
	// 口令明文绝不能被回显。
	if strings.Contains(body, pw) {
		t.Fatal("SMTP page leaked the password plaintext")
	}
	// 库里只能是密文（带版本前缀）。
	var row store.Setting
	if err := db.First(&row, "key = ?", mail.SettingKeySMTPPassword).Error; err != nil {
		t.Fatalf("load stored password: %v", err)
	}
	if strings.Contains(row.Value, pw) {
		t.Fatalf("stored SMTP password contains plaintext: %q", row.Value)
	}
	if !strings.Contains(row.Value, "v1:") {
		t.Errorf("stored SMTP password is not a versioned ciphertext: %q", row.Value)
	}
}

// TestAdminSMTPTestConnectionSurfacesServerError 是 M1-17 的验收点：
// 错主机时页面显示服务端的原始错误文本。
func TestAdminSMTPTestConnectionSurfacesServerError(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)
	addr := deadAddr(t)
	host, port, _ := net.SplitHostPort(addr)

	rec := postForm(t, srv, "/admin/smtp/test", url.Values{
		"csrf_token":    {csrf},
		"smtp_host":     {host},
		"smtp_port":     {port},
		"smtp_from":     {"no-reply@example.com"},
		"smtp_tls_mode": {"starttls"},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /admin/smtp/test = %d, want 200 (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "连接失败") {
		t.Errorf("page does not show the localized failure prefix; body = %s", snippet(body))
	}
	if !strings.Contains(body, "connect") {
		t.Errorf("page does not surface the server error text; body = %s", snippet(body))
	}
}

// TestAdminSMTPTestConnectionSucceeds 覆盖测试连接的成功路径。
func TestAdminSMTPTestConnectionSucceeds(t *testing.T) {
	srv, _, _, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)
	addr := startFakeSMTP(t)
	host, port, _ := net.SplitHostPort(addr)

	rec := postForm(t, srv, "/admin/smtp/test", url.Values{
		"csrf_token":    {csrf},
		"smtp_host":     {host},
		"smtp_port":     {port},
		"smtp_from":     {"no-reply@example.com"},
		"smtp_tls_mode": {"none"},
	}, cookies)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "连接成功") {
		t.Fatalf("test connection did not report success: status=%d body=%s", rec.Code, snippet(body))
	}
}

// TestAdminSMTPRejectsInvalidPortAndTLS 覆盖反面用例：非法端口与 TLS 模式不落库。
func TestAdminSMTPRejectsInvalidPortAndTLS(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)

	for _, tc := range []struct{ name, field, value, notice string }{
		{"port", "smtp_port", "70000", "invalid_port"},
		{"tls", "smtp_tls_mode", "ssl", "invalid_tls_mode"},
	} {
		rec := postForm(t, srv, "/admin/smtp", url.Values{
			"csrf_token": {csrf}, tc.field: {tc.value},
		}, cookies)
		if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), tc.notice) {
			t.Errorf("%s: status=%d location=%q, want 303 with %s", tc.name, rec.Code, rec.Header().Get("Location"), tc.notice)
		}
	}
	settings, err := store.LoadSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if _, ok := settings[mail.SettingKeySMTPPort]; ok {
		t.Error("invalid port was persisted")
	}
	if _, ok := settings[mail.SettingKeySMTPTLSMode]; ok {
		t.Error("invalid TLS mode was persisted")
	}
}

// TestAdminSMTPConfigWritesAudit 证明 SMTP 配置变更留痕。
func TestAdminSMTPConfigWritesAudit(t *testing.T) {
	srv, db, _, cookies, csrf := newNotesServer(t)
	srv.secrets = mustCodec(t)
	before, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionSettingUpdate)
	if err != nil {
		t.Fatalf("count audits: %v", err)
	}
	rec := postForm(t, srv, "/admin/smtp", url.Values{
		"csrf_token": {csrf}, "smtp_host": {"smtp.example.com"}, "smtp_from": {"no-reply@example.com"},
	}, cookies)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/smtp = %d, want 303", rec.Code)
	}
	after, err := store.NewAuditStore(db).CountByAction(context.Background(), store.ActionSettingUpdate)
	if err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if after <= before {
		t.Errorf("setting.update audits = %d, want > %d", after, before)
	}
}

// TestAdminNavHasSMTPEntry 证明导航里有 SMTP 入口且不是置灰项。
func TestAdminNavHasSMTPEntry(t *testing.T) {
	srv, cookies, _ := newAdminServer(t)
	body := getWithCookies(t, srv, "/admin", cookies).Body.String()
	if !strings.Contains(body, "邮件（SMTP）") {
		t.Errorf("admin nav is missing the SMTP entry; body = %s", snippet(body))
	}
}

// mustCodec 构造一个测试用编解码器（32 字节占位密钥）。
func mustCodec(t *testing.T) *store.SecretCodec {
	t.Helper()
	codec, err := store.NewSecretCodec(testSecretKey)
	if err != nil {
		t.Fatalf("NewSecretCodec: %v", err)
	}
	return codec
}
