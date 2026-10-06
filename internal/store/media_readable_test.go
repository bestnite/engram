package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

// 本文件覆盖媒体库列表（GET /api/v1/media 与选择器共用的 store 原语）：
//   - 可读集合是「我上传」∪「我可读卡组里未软删 note 引用」的并集，必须去重；
//   - keyset 分页在同一游标下不重不漏，排序 media.created_at DESC, sha256 DESC 稳定；
//   - 别人 private 卡组里引用的媒体不得出现；他人 public 卡组里的引用按同一谓词应出现；
//   - 授权撤销后立即从列表消失（不缓存）。
//
// 媒体行直接落库以精确控制 created_at（排序键）；note 一律经 NoteStore 写入方法构造，
// 保证 media_notes 由写入路径重建（AGENTS.md 的夹具约定）。

// testMediaSha 生成一个稳定的 64 位小写十六进制 sha（i 不同即不同）。
func testMediaSha(i int) string { return fmt.Sprintf("%064x", i) }

// seedListMediaRow 直接写一行 media，created_at 由调用方给定（列表排序键）。
func seedListMediaRow(t *testing.T, db *gorm.DB, sha string, createdAt time.Time) {
	t.Helper()
	row := Media{Sha256: sha, RelPath: sha[:2] + "/" + sha + ".png", Mime: "image/png", Bytes: 12, CreatedAt: createdAt}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create media %s: %v", sha, err)
	}
}

// listAllReadable 按 limit 反复翻页，返回全量 sha 顺序；同一游标重复请求也走同一条路径，
// 便于断言「不重不漏」。
func listAllReadable(t *testing.T, db *gorm.DB, userID uint64, limit int) []string {
	t.Helper()
	var out []string
	cursor := ""
	for page := 0; page < 100; page++ {
		rows, next, err := ListReadableMedia(context.Background(), db, userID, limit, cursor)
		if err != nil {
			t.Fatalf("ListReadableMedia page %d error = %v", page, err)
		}
		for _, m := range rows {
			out = append(out, m.Sha256)
		}
		if next == "" {
			return out
		}
		cursor = next
	}
	t.Fatalf("pagination did not terminate after 100 pages")
	return out
}

// TestListReadableMediaPaginatesStableAndDeduped 断言并集去重与 keyset 分页稳定。
func TestListReadableMediaPaginatesStableAndDeduped(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			users := seedUsers(t, db, "list_owner", "list_other")
			owner, other := users[0], users[1]
			deck := seedDeck(t, db, owner)
			ctx := context.Background()
			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

			// m0..m4 由 owner 上传；m5 由 other 上传、但被 owner 可见卡组的 note 引用（只走第二支）。
			for i := 0; i < 5; i++ {
				sha := testMediaSha(i + 1)
				seedListMediaRow(t, db, sha, base.Add(time.Duration(i)*time.Minute))
				if err := RecordMediaUploader(ctx, db, sha, owner); err != nil {
					t.Fatalf("RecordMediaUploader(%s): %v", sha, err)
				}
			}
			shaRefOnly := testMediaSha(99)
			seedListMediaRow(t, db, shaRefOnly, base.Add(10*time.Minute))
			if err := RecordMediaUploader(ctx, db, shaRefOnly, other); err != nil {
				t.Fatalf("RecordMediaUploader(ref-only): %v", err)
			}

			// 一张 note 同时引用 m3（owner 也上传过，并集里两路命中，必须只出现一次）
			// 与只被引用、非 owner 上传的 sha（第二支）。
			saveRefNote(t, db, deck, Ptr(owner), refFront(testMediaSha(3), shaRefOnly))

			got := listAllReadable(t, db, owner, 2)
			want := []string{shaRefOnly, testMediaSha(5), testMediaSha(4), testMediaSha(3), testMediaSha(2), testMediaSha(1)}
			if len(got) != len(want) {
				t.Fatalf("readable media = %v (%d), want %v (%d)", got, len(got), want, len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("readable media order[%d] = %s, want %s (all: %v)", i, got[i], want[i], got)
				}
			}

			// 单页取全量（limit 足够大）：并集必须去重，同一份字节两路命中只出现一次。
			// 这条断言不能只靠分页覆盖——keyset 的严格小于会把「同排序键的重复行」挡在下一页之外，
			// 所以只有一页装得下全部行时才真正验证 UNION 的去重。
			single, singleNext, err := ListReadableMedia(ctx, db, owner, 10, "")
			if err != nil {
				t.Fatalf("single page error = %v", err)
			}
			if singleNext != "" {
				t.Fatalf("single page returned a cursor %q while the whole set fits", singleNext)
			}
			if len(single) != len(want) {
				shas := make([]string, 0, len(single))
				for _, m := range single {
					shas = append(shas, m.Sha256)
				}
				t.Fatalf("single page has %d rows, want %d deduped (got %v)", len(single), len(want), shas)
			}

			// 同一游标重复请求必须给出同一页（keyset 稳定），不重不漏。
			first, nextCursor, err := ListReadableMedia(ctx, db, owner, 2, "")
			if err != nil {
				t.Fatalf("first page error = %v", err)
			}
			if nextCursor == "" {
				t.Fatalf("first page returned an empty cursor while more pages exist")
			}
			again, nextAgain, err := ListReadableMedia(ctx, db, owner, 2, "")
			if err != nil {
				t.Fatalf("repeat first page error = %v", err)
			}
			if len(again) != len(first) || nextAgain != nextCursor {
				t.Fatalf("repeating the first page drifted: first %v/%q vs again %v/%q", first, nextCursor, again, nextAgain)
			}

			// 末页 next_cursor 必须为空串。
			last, lastNext, err := ListReadableMedia(ctx, db, owner, 2, nextCursorOf(t, db, owner, 4))
			if err != nil {
				t.Fatalf("last page error = %v", err)
			}
			if len(last) == 0 || lastNext != "" {
				t.Fatalf("last page = (%d rows, cursor %q), want non-empty rows and empty cursor", len(last), lastNext)
			}
		})
	}
}

// nextCursorOf 连续翻页累计到第 n 项，返回第 n 项的游标（用于直接取末页）。
func nextCursorOf(t *testing.T, db *gorm.DB, userID uint64, n int) string {
	t.Helper()
	cursor := ""
	seen := 0
	for {
		rows, next, err := ListReadableMedia(context.Background(), db, userID, 1, cursor)
		if err != nil {
			t.Fatalf("walk cursor error = %v", err)
		}
		if len(rows) == 0 {
			t.Fatalf("walk cursor exhausted before %d items", n)
		}
		seen++
		if seen >= n {
			return next
		}
		cursor = next
	}
}

// TestListReadableMediaExcludesOthersPrivate 断言他人 private 卡组里的媒体不出现，
// 而他人 public 卡组里的引用按同一可见性谓词出现。
func TestListReadableMediaExcludesOthersPrivate(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			users := seedUsers(t, db, "scope_owner", "scope_other")
			owner, other := users[0], users[1]
			ctx := context.Background()
			base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

			privateDeck := seedDeck(t, db, other)
			publicDeck := seedDeck(t, db, other)
			if err := db.Model(&Deck{}).Where("id = ?", publicDeck).Update("visibility", DeckVisibilityPublic).Error; err != nil {
				t.Fatalf("make deck public: %v", err)
			}

			secret := testMediaSha(201)
			seedListMediaRow(t, db, secret, base)
			if err := RecordMediaUploader(ctx, db, secret, other); err != nil {
				t.Fatalf("record uploader: %v", err)
			}
			saveRefNote(t, db, privateDeck, Ptr(other), refFront(secret))

			shared := testMediaSha(202)
			seedListMediaRow(t, db, shared, base.Add(time.Minute))
			if err := RecordMediaUploader(ctx, db, shared, other); err != nil {
				t.Fatalf("record uploader: %v", err)
			}
			saveRefNote(t, db, publicDeck, Ptr(other), refFront(shared))

			got := listAllReadable(t, db, owner, 10)
			for _, sha := range got {
				if sha == secret {
					t.Fatalf("list leaked media %s referenced only by another user's private deck: %v", secret, got)
				}
			}
			found := false
			for _, sha := range got {
				if sha == shared {
					found = true
				}
			}
			if !found {
				t.Fatalf("list omitted media %s referenced by another user's public deck: %v", shared, got)
			}
		})
	}
}

// TestListReadableMediaDropsAfterGrantRevoked 断言授权撤销后媒体立即从列表消失（不缓存）。
func TestListReadableMediaDropsAfterGrantRevoked(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			users := seedUsers(t, db, "revoke_owner", "revoke_viewer")
			owner, viewer := users[0], users[1]
			ctx := context.Background()
			deck := seedDeck(t, db, owner)

			sha := testMediaSha(301)
			seedListMediaRow(t, db, sha, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
			if err := RecordMediaUploader(ctx, db, sha, owner); err != nil {
				t.Fatalf("record uploader: %v", err)
			}
			saveRefNote(t, db, deck, Ptr(owner), refFront(sha))

			grants := NewGrantStore(db)
			if err := grants.Grant(ctx, deck, viewer, RoleReader, Ptr(owner)); err != nil {
				t.Fatalf("grant: %v", err)
			}
			if got := listAllReadable(t, db, viewer, 10); len(got) != 1 || got[0] != sha {
				t.Fatalf("viewer readable before revoke = %v, want [%s]", got, sha)
			}
			if err := grants.Revoke(ctx, deck, viewer); err != nil {
				t.Fatalf("revoke: %v", err)
			}
			if got := listAllReadable(t, db, viewer, 10); len(got) != 0 {
				t.Fatalf("viewer readable after revoke = %v, want empty", got)
			}
		})
	}
}

// TestListReadableMediaRejectsInvalidCursor 断言非法游标返回稳定错误（供 REST 映射 400）。
func TestListReadableMediaRejectsInvalidCursor(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			if _, _, err := ListReadableMedia(context.Background(), db, 1, 10, "not-a-cursor"); !errors.Is(err, ErrInvalidMediaCursor) {
				t.Fatalf("ListReadableMedia(invalid cursor) error = %v, want ErrInvalidMediaCursor", err)
			}
		})
	}
}
