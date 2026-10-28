package mail

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal in-process SMTP server, used in tests to verify delivery and retries
// without depending on the internet or a real SMTP service. It reads commands line by line and
// answers with fixed status codes; rcptReject switches RCPT TO to a permanent rejection.
type fakeSMTP struct {
	ln   net.Listener
	addr string
	mu   sync.Mutex
	msgs []string // bodies of the successfully received messages
	// When rcptReject is true, RCPT TO is rejected outright (the permanent failure path).
	rcptReject bool
}

// newFakeSMTP starts a minimal SMTP service on a loopback port.
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

// host returns the host name (without the port).
func (f *fakeSMTP) host() string {
	h, _, _ := net.SplitHostPort(f.addr)
	return h
}

// port returns the listening port.
func (f *fakeSMTP) port() int {
	_, p, _ := net.SplitHostPort(f.addr)
	n := 0
	for _, c := range p {
		n = n*10 + int(c-'0')
	}
	return n
}

// setRcptReject makes RCPT TO be rejected (the permanent failure path that is not retried).
func (f *fakeSMTP) setRcptReject(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rcptReject = v
}

// received returns a copy of the successfully received message bodies.
func (f *fakeSMTP) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.msgs...)
}

// serve accepts connections and starts a goroutine for each session.
func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

// handle processes the commands of one session; it implements only the minimal command set delivery
// needs (EHLO/HELO/MAIL/RCPT/DATA/QUIT/RSET).
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

// finishTransaction records one successfully received mail.
func (f *fakeSMTP) finishTransaction(body *strings.Builder, say func(string)) {
	f.mu.Lock()
	f.msgs = append(f.msgs, body.String())
	f.mu.Unlock()
	say("250 2.0.0 accepted")
}
