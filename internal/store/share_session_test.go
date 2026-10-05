package store

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"
)

// 本文件覆盖 L3 的分享会话授权（share_session.go，DESIGN.md §5）：
//   - 登记后读取鉴权第三支放行，且只对登记的那个会话放行（会话隔离）；
//   - 授权过期后不再放行；RemoveExpired 负责回收；
//   - 会话作废（登出、改密码踢下线、禁用）时该会话的授权行一并删除。

// seedSession 插入一条服务端会话行，供授权与会话回收用例使用。
func seedSession(t *testing.T, db *gorm.DB, userID uint64, id string, expiresAt time.Time) {
	t.Helper()
	if err := NewSessionStore(db).Create(context.Background(), &Session{
		ID: id, UserID: userID, CSRFToken: "csrf-" + id,
		CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("create session %s: %v", id, err)
	}
}

// shareGrantCount 统计某会话的授权行数。
func shareGrantCount(t *testing.T, db *gorm.DB, sessionID string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&ShareSessionDeck{}).Where("session_id = ?", sessionID).Count(&n).Error; err != nil {
		t.Fatalf("count share_session_decks for %s: %v", sessionID, err)
	}
	return n
}

// TestShareSessionGrantAuthorizesMediaForItsSessionOnly 覆盖读取侧第三支与会话隔离：
// 分享授权只对登记它的那个会话放行；同一用户的另一个会话、以及无会话的调用都不放行。
func TestShareSessionGrantAuthorizesMediaForItsSessionOnly(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "share_owner", "share_visitor")
			owner, visitor := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("share-session"))
			saveRefNote(t, db, deckID, Ptr(owner), refFront(sha))

			now := time.Now().UTC()
			seedSession(t, db, visitor, "sess-open", now.Add(time.Hour))
			seedSession(t, db, visitor, "sess-other", now.Add(time.Hour))
			// 卡组必须仍有一条有效分享链接，读取才会放行（撤销/过期即时失效，见 share_session.go）。
			if _, _, err := NewShareLinkStore(db).Create(ctx, ShareLinkInput{DeckID: deckID, CreatedBy: owner}); err != nil {
				t.Fatalf("create share link: %v", err)
			}
			if err := NewShareSessionStore(db).Grant(ctx, "sess-open", deckID, nil, now.Add(time.Hour)); err != nil {
				t.Fatalf("Grant() error = %v", err)
			}

			if ok, err := MediaAccessibleToUser(ctx, db, visitor, "sess-open", sha); err != nil || !ok {
				t.Fatalf("opening session: readable=%v err=%v, want true", ok, err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, visitor, "sess-other", sha); err != nil || ok {
				t.Fatalf("other session: readable=%v err=%v, want false", ok, err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, visitor, "", sha); err != nil || ok {
				t.Fatalf("no session: readable=%v err=%v, want false", ok, err)
			}
		})
	}
}

// TestShareSessionGrantLapsesWhenExpired 覆盖过期：授权行过期后读取变 false，RemoveExpired 把它清掉。
func TestShareSessionGrantLapsesWhenExpired(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "expiry_owner", "expiry_visitor")
			owner, visitor := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			sha := saveMedia(t, db, t.TempDir(), owner, mediaBytes("share-expiry"))
			saveRefNote(t, db, deckID, Ptr(owner), refFront(sha))
			// 链接不过期，确保「过期」这一支只来自授权行本身。
			if _, _, err := NewShareLinkStore(db).Create(ctx, ShareLinkInput{DeckID: deckID, CreatedBy: owner}); err != nil {
				t.Fatalf("create share link: %v", err)
			}
			seedSession(t, db, visitor, "sess-expired", time.Now().UTC().Add(time.Hour))

			past := time.Now().UTC().Add(-time.Hour)
			if err := NewShareSessionStore(db).Grant(ctx, "sess-expired", deckID, &past, time.Now().UTC().Add(time.Hour)); err != nil {
				t.Fatalf("Grant() error = %v", err)
			}
			if ok, err := MediaAccessibleToUser(ctx, db, visitor, "sess-expired", sha); err != nil || ok {
				t.Fatalf("expired grant: readable=%v err=%v, want false", ok, err)
			}

			removed, err := NewShareSessionStore(db).RemoveExpired(ctx, time.Now().UTC())
			if err != nil {
				t.Fatalf("RemoveExpired() error = %v", err)
			}
			if removed != 1 {
				t.Errorf("RemoveExpired() removed %d rows, want 1", removed)
			}
			if n := shareGrantCount(t, db, "sess-expired"); n != 0 {
				t.Errorf("expired grant row survived RemoveExpired: %d rows", n)
			}
		})
	}
}

// TestShareSessionGrantRemovedWhenSessionRevoked 覆盖清理：登出（Revoke）与批量作废
// （RevokeAllForUserExcept / RevokeAllForUser）都要删掉被作废会话的授权行。
func TestShareSessionGrantRemovedWhenSessionRevoked(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "revoke_owner", "revoke_visitor")
			owner, visitor := users[0], users[1]
			deckID := seedDeck(t, db, owner)
			store := NewShareSessionStore(db)
			now := time.Now().UTC()

			// 1) 登出单个会话：只删该会话的行。
			seedSession(t, db, visitor, "sess-logout", now.Add(time.Hour))
			seedSession(t, db, visitor, "sess-keep", now.Add(time.Hour))
			for _, sid := range []string{"sess-logout", "sess-keep"} {
				if err := store.Grant(ctx, sid, deckID, nil, now.Add(time.Hour)); err != nil {
					t.Fatalf("Grant(%s) error = %v", sid, err)
				}
			}
			if err := NewSessionStore(db).Revoke(ctx, "sess-logout", now); err != nil {
				t.Fatalf("Revoke() error = %v", err)
			}
			if n := shareGrantCount(t, db, "sess-logout"); n != 0 {
				t.Errorf("revoked session still has %d grants, want 0", n)
			}
			if n := shareGrantCount(t, db, "sess-keep"); n != 1 {
				t.Errorf("kept session has %d grants, want 1", n)
			}

			// 2) 改密码保留当前会话：其它会话的授权被删，当前会话的保留。
			if err := NewSessionStore(db).RevokeAllForUserExcept(ctx, visitor, "sess-keep", now); err != nil {
				t.Fatalf("RevokeAllForUserExcept() error = %v", err)
			}
			if n := shareGrantCount(t, db, "sess-keep"); n != 1 {
				t.Errorf("kept session lost its grant: %d rows, want 1", n)
			}

			// 3) 全部作废（禁用/强制下线）：该用户所有会话的授权清空。
			if err := store.Grant(ctx, "sess-keep", deckID, nil, now.Add(time.Hour)); err != nil {
				t.Fatalf("Grant() error = %v", err)
			}
			if err := NewSessionStore(db).RevokeAllForUser(ctx, visitor, now); err != nil {
				t.Fatalf("RevokeAllForUser() error = %v", err)
			}
			if n := shareGrantCount(t, db, "sess-keep"); n != 0 {
				t.Errorf("after RevokeAllForUser: %d grants, want 0", n)
			}
			// owner 的会话不受影响（不同用户）。
			seedSession(t, db, owner, "sess-owner", now.Add(time.Hour))
			if err := store.Grant(ctx, "sess-owner", deckID, nil, now.Add(time.Hour)); err != nil {
				t.Fatalf("Grant(owner) error = %v", err)
			}
			if err := NewSessionStore(db).RevokeAllForUser(ctx, visitor, now); err != nil {
				t.Fatalf("RevokeAllForUser() second call error = %v", err)
			}
			if n := shareGrantCount(t, db, "sess-owner"); n != 1 {
				t.Errorf("other user's grant was removed: %d rows, want 1", n)
			}
		})
	}
}
