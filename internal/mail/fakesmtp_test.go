package mail

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP 是一个进程内最小 SMTP 服务器，用于在测试里验证投递与重试，
// 不依赖外网或真实 SMTP 服务。它按行读取命令并应答固定状态码。
//
// 行为可通过 failFirst 控制：对第 N 次 DATA 之前的事务返回 4xx（可重试的瞬时错误），
// 用来验证 worker 的退避重试；errors 记录每次返回给客户端的错误文本，便于断言
// "最后一次错误"确实是服务端文本。
type fakeSMTP struct {
	ln   net.Listener
	addr string
	mu   sync.Mutex
	msgs []string // 成功接收的邮件正文
	// failNext 是还需要失败的次数（>0 时下一个事务失败）。
	failNext int
	// rcptReject 为 true 时对 RCPT TO 直接拒绝（永久失败路径）。
	rcptReject bool
	closed     bool
}

// newFakeSMTP 在本地回环端口启动最小 SMTP 服务。
func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln, addr: ln.Addr().String()}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

// host 返回主机名（不含端口）。
func (f *fakeSMTP) host() string {
	h, _, _ := net.SplitHostPort(f.addr)
	return h
}

// port 返回监听端口。
func (f *fakeSMTP) port() int {
	_, p, _ := net.SplitHostPort(f.addr)
	n := 0
	for _, c := range p {
		n = n*10 + int(c-'0')
	}
	return n
}

// setFailNext 让接下来 n 个事务失败，返回可重试的服务端错误文本。
func (f *fakeSMTP) setFailNext(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = n
}

// setRcptReject 让 RCPT TO 被拒（不重试的永久失败场景）。
func (f *fakeSMTP) setRcptReject(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rcptReject = v
}

// received 返回成功接收的邮件正文副本。
func (f *fakeSMTP) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.msgs...)
}

// serve 接受连接并为每个会话开一个 goroutine。
func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

// handle 逐个会话处理命令；只实现投递所需的最小命令集（EHLO/HELO/MAIL/RCPT/DATA/QUIT/RSET）。
func (f *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	say := func(s string) {
		_, _ = w.WriteString(s + "\r\n")
		_ = w.Flush()
	}
	say("220 fake ESMTP ready")
	var body strings.Builder
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
				f.finishTransaction(&body, say)
				continue
			}
			body.WriteString(line + "\n")
			continue
		}
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake greets you")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			if f.takeFail() {
				say("451 4.3.0 temporary local problem")
				continue
			}
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			f.mu.Lock()
			reject := f.rcptReject
			f.mu.Unlock()
			if reject {
				say("550 5.1.1 no such user")
				continue
			}
			say("250 ok")
		case strings.HasPrefix(cmd, "DATA"):
			say("354 end with <CRLF>.<CRLF>")
			body.Reset()
			inData = true
		case strings.HasPrefix(cmd, "RSET"):
			say("250 ok")
		case strings.HasPrefix(cmd, "QUIT"):
			say("221 bye")
			return
		default:
			say("502 5.5.2 command not implemented")
		}
	}
}

// takeFail 在需要失败时消耗一次额度并返回 true。
func (f *fakeSMTP) takeFail() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext > 0 {
		f.failNext--
		return true
	}
	return false
}

// finishTransaction 记录一封成功接收的邮件。
func (f *fakeSMTP) finishTransaction(body *strings.Builder, say func(string)) {
	f.mu.Lock()
	f.msgs = append(f.msgs, body.String())
	f.mu.Unlock()
	say("250 2.0.0 accepted")
}
