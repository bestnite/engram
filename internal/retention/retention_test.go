package retention

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// 本包按 AGENTS.md §2.5 的规矩用真库（sqlite 文件）而不是 mock：保留期的边界正是要验的东西。

type fixture struct {
	sessions     *store.SessionStore
	tokens       *store.ActionTokenStore
	invites      *store.InviteStore
	shareInvites *store.DeckShareInviteStore
	fingerprints *store.LoginFingerprintStore
	logs         *bytes.Buffer
	db           *gorm.DB
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := store.Open("sqlite", filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := store.AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return fixture{
		sessions:     store.NewSessionStore(db),
		tokens:       store.NewActionTokenStore(db),
		invites:      store.NewInviteStore(db),
		shareInvites: store.NewDeckShareInviteStore(db),
		fingerprints: store.NewLoginFingerprintStore(db),
		logs:         &bytes.Buffer{},
		db:           db,
	}
}

// deps 组装一轮回收所需的依赖，日志收集到 fixture 的缓冲区。
func (f fixture) deps() Deps {
	return Deps{
		Sessions:     f.sessions,
		ActionTokens: f.tokens,
		Invites:      f.invites,
		ShareInvites: f.shareInvites,
		Fingerprints: f.fingerprints,
		Logger:       slog.New(slog.NewTextHandler(f.logs, nil)),
		Interval:     time.Hour,
	}
}

// sweepOnce 同步跑一轮回收并断言它没有报错。
//
// 刻意直接调 sweep 而不是走 Start/Stop：后者是异步的，Stop 会在轮次跑完前取消上下文，
// 断言就变成与调度赛跑（曾被这条坑到——四类删除全报 context canceled）。
func (f fixture) sweepOnce(t *testing.T) {
	t.Helper()
	if err := sweep(context.Background(), f.deps()); err != nil {
		t.Fatalf("sweep() error = %v", err)
	}
}

// TestWorkerRunsASweepOnStart 钉住「接线真的会跑」：worker 启动即跑一轮，因此启动后不久
// 过期会话就应当消失。这是 Start 契约（启动即跑一轮）在本包上的端到端体现。
func TestWorkerRunsASweepOnStart(t *testing.T) {
	f := newFixture(t)
	f.addSession(t, "session-expired", -time.Minute)

	lifecycle, err := New(f.deps())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifecycle.Start(ctx)
	defer lifecycle.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := f.sessions.ByID(context.Background(), "session-expired"); err != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("starting the worker did not reap the expired session")
}

// pinLocalZone 把进程的 time.Local 换成固定偏移，测试结束时还原。
//
// 为什么要这么做：这一层缺陷（界定值带本地偏移 → SQLite 字符串比较把窗口整体平移）只在
// 「本地偏移 ≠ 0」的机器上显形，而 CI 常年跑在 UTC——不固定时区，回归就抓不住。行按写入方的
// 真实时区（UTC）落库，因此界定值一旦退回本地时区，下面的断言必然变红。
func pinLocalZone(t *testing.T, offsetHours int) {
	t.Helper()
	original := time.Local
	time.Local = time.FixedZone("TST", offsetHours*3600)
	t.Cleanup(func() { time.Local = original })
}

// addSession 写入一条会话行。
func (f fixture) addSession(t *testing.T, id string, expiresIn time.Duration) {
	t.Helper()
	if err := f.sessions.Create(context.Background(), &store.Session{
		ID: id, UserID: 1, CSRFToken: "t-" + id,
		CreatedAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(expiresIn),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
}

// addToken 写入一条令牌行并返回它的明文（供 Peek 复查）。
func (f fixture) addToken(t *testing.T, purpose string, expiresIn time.Duration) string {
	t.Helper()
	plaintext := purpose + "-" + expiresIn.String()
	if err := f.tokens.Create(context.Background(), &store.ActionToken{
		Purpose: purpose, UserID: 1, TokenHash: store.HashActionToken(plaintext),
		CreatedAt: time.Now().UTC().Add(-24 * time.Hour), ExpiresAt: time.Now().UTC().Add(expiresIn),
	}); err != nil {
		t.Fatalf("create action token: %v", err)
	}
	return plaintext
}

// TestSweepRespectsEachRetentionWindow 是本包的核心断言：四类行各自的保留期边界。
func TestSweepRespectsEachRetentionWindow(t *testing.T) {
	pinLocalZone(t, 8)
	f := newFixture(t)
	ctx := context.Background()

	// 会话：到期即删。
	f.addSession(t, "session-expired", -time.Minute)
	f.addSession(t, "session-live", time.Hour)

	// 令牌：过期后再留 ActionTokenGrace。
	pastGrace := f.addToken(t, "password_reset", -ActionTokenGrace-time.Minute) // 该删
	inGrace := f.addToken(t, "password_reset", -ActionTokenGrace+time.Hour)     // 刚过期，仍在宽限期
	notExpired := f.addToken(t, "email_verification", time.Hour)                // 未过期

	// 邀请：到期即删。
	expiredAt := time.Now().UTC().Add(-time.Hour)
	staleInvite := store.Invite{Token: "invite-expired", Role: "user", CreatedAt: time.Now().UTC().Add(-48 * time.Hour), ExpiresAt: &expiredAt}
	if err := f.invites.Create(ctx, &staleInvite); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	futureAt := time.Now().UTC().Add(24 * time.Hour)
	liveInvite := store.Invite{Token: "invite-live", Role: "user", CreatedAt: time.Now().UTC(), ExpiresAt: &futureAt}
	if err := f.invites.Create(ctx, &liveInvite); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// 卡组共享邀请：到期即删（未到期的必须留下——它还是「待接受」）。
	seedShareInvite(t, f, 7, 11, -time.Hour) // 已过期，该删
	seedShareInvite(t, f, 8, 12, time.Hour)  // 还有一小时，留下

	// 登录指纹：最后出现超过 FingerprintRetention 才删。
	if _, err := f.fingerprints.Touch(ctx, 1, "stale-device", time.Now().Add(-FingerprintRetention-time.Hour)); err != nil {
		t.Fatalf("touch stale-device: %v", err)
	}
	if _, err := f.fingerprints.Touch(ctx, 1, "recent-device", time.Now().Add(-FingerprintRetention+time.Hour)); err != nil {
		t.Fatalf("touch recent-device: %v", err)
	}

	f.sweepOnce(t)

	// 卡组共享邀请：过期的没了，未到期的还在（读路径也判过期，这里验的是回收真的删了）。
	if _, err := f.shareInvites.ByDeckAndUser(ctx, 7, 11); !errors.Is(err, store.ErrShareInviteNotFound) {
		t.Errorf("expired deck share invite should be gone, got err = %v", err)
	}
	if _, err := f.shareInvites.ByDeckAndUser(ctx, 8, 12); err != nil {
		t.Errorf("live deck share invite must survive: %v", err)
	}

	// 会话：过期的没了，有效还在。
	if _, err := f.sessions.ByID(ctx, "session-expired"); err == nil {
		t.Error("expired session should be gone")
	}
	if _, err := f.sessions.ByID(ctx, "session-live"); err != nil {
		t.Errorf("live session must survive: %v", err)
	}

	// 令牌：只有超过宽限期的那枚被删（另两枚仍能被 Peek 找到——过期的报 expired，未过期的直接返回）。
	if _, err := f.tokens.Peek(ctx, "password_reset", pastGrace, time.Now()); !errors.Is(err, store.ErrActionTokenNotFound) {
		t.Errorf("token past the grace period should be gone, got err = %v", err)
	}
	if _, err := f.tokens.Peek(ctx, "password_reset", inGrace, time.Now()); !errors.Is(err, store.ErrActionTokenExpired) {
		t.Errorf("token inside the grace period should still exist but read as expired, got err = %v", err)
	}
	if _, err := f.tokens.Peek(ctx, "email_verification", notExpired, time.Now()); err != nil {
		t.Errorf("unexpired token should still be readable: %v", err)
	}

	// 邀请：过期的那张没了，未过期的还在。
	rows, err := f.invites.List(ctx)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	var tokens []string
	for _, r := range rows {
		tokens = append(tokens, r.Token)
	}
	if len(tokens) != 1 || tokens[0] != "invite-live" {
		t.Errorf("invites after sweep = %v, want only invite-live", tokens)
	}

	// 指纹：被删的判为新设备，保留的判为已知设备。
	knownRecent, err := f.fingerprints.Touch(ctx, 1, "recent-device", time.Now())
	if err != nil {
		t.Fatalf("touch recent-device: %v", err)
	}
	if !knownRecent {
		t.Error("a device seen within the retention window should still count as known")
	}
	knownStale, err := f.fingerprints.Touch(ctx, 1, "stale-device", time.Now())
	if err != nil {
		t.Fatalf("touch stale-device: %v", err)
	}
	if knownStale {
		t.Error("a device last seen before the retention window should count as new again")
	}
}

// TestSweepKeepsInvitesWithoutExpiry 单独钉住可空 expires_at 这个尖锐边界：
// NULL 表示「不过期」，不能被当成过期清掉。
func TestSweepKeepsInvitesWithoutExpiry(t *testing.T) {
	pinLocalZone(t, 8)
	f := newFixture(t)
	ctx := context.Background()
	never := store.Invite{Token: "never-expires", Role: "user", CreatedAt: time.Now().UTC().Add(-365 * 24 * time.Hour)}
	if err := f.invites.Create(ctx, &never); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	// 行按写入方的真实时区（UTC）落库——时间列存 UTC 是这几张表的一致约定，
	// 夹具写本地时间会让「过期行」在与 UTC 界定值比较时反而显得还没到期。
	past := time.Now().UTC().Add(-time.Hour)
	stale := store.Invite{Token: "expired", Role: "user", CreatedAt: time.Now().UTC().Add(-48 * time.Hour), ExpiresAt: &past}
	if err := f.invites.Create(ctx, &stale); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	f.sweepOnce(t)

	rows, err := f.invites.List(ctx)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	var tokens []string
	for _, r := range rows {
		tokens = append(tokens, r.Token)
	}
	if len(tokens) != 1 || tokens[0] != "never-expires" {
		t.Errorf("invites after sweep = %v, want only never-expires", tokens)
	}
}

// TestNewRequiresEveryStore 断言四类存储缺一即装配失败。
func TestNewRequiresEveryStore(t *testing.T) {
	f := newFixture(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	base := Deps{
		Sessions: f.sessions, ActionTokens: f.tokens, Invites: f.invites,
		ShareInvites: f.shareInvites, Fingerprints: f.fingerprints, Logger: logger,
	}
	if _, err := New(base); err != nil {
		t.Fatalf("New() with all deps error = %v", err)
	}
	for name, mutate := range map[string]func(*Deps){
		"sessions":      func(d *Deps) { d.Sessions = nil },
		"tokens":        func(d *Deps) { d.ActionTokens = nil },
		"invites":       func(d *Deps) { d.Invites = nil },
		"share_invites": func(d *Deps) { d.ShareInvites = nil },
		"fingerprints":  func(d *Deps) { d.Fingerprints = nil },
		"logger":        func(d *Deps) { d.Logger = nil },
	} {
		d := base
		mutate(&d)
		if _, err := New(d); err == nil {
			t.Errorf("New() without %s should fail", name)
		}
	}
}

// seedShareInvite 直接写一条卡组共享邀请：本用例只验回收窗口，不需要真的建卡组与用户
// （这两列没有外键约束，id 用任意值即可）。
func seedShareInvite(t *testing.T, f fixture, deckID, userID uint64, ttl time.Duration) {
	t.Helper()
	now := time.Now().UTC()
	err := f.shareInvites.Invite(context.Background(), store.DeckShareInvite{
		DeckID: deckID, UserID: userID, Role: store.RoleReader, InvitedBy: 1,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	})
	if err != nil {
		t.Fatalf("seed share invite: %v", err)
	}
}
