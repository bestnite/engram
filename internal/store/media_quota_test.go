package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestUserMediaUsageDedupesAndReleases 覆盖 M2-13 的计量口径：
//   - 同一 blob 被同一用户的多个 note/多处引用只计一次（去重）；
//   - 不同用户各自引用同一 blob 互不影响（各自计一次，不重复计入同一人）；
//   - 删除引用（note 软删除）即释放配额；
//   - 卡组包形态的 media/<sha>.<ext> 引用也能识别。
func TestUserMediaUsageDedupesAndReleases(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t)
	if err := AutoMigrate(ctx, db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	now := time.Now().UTC()

	u1 := seedUsers(t, db, "usage-one")[0]
	u2 := seedUsers(t, db, "usage-two")[0]
	deckID := seedDeck(t, db, u1)

	sha1 := strings.Repeat("a", 64)
	sha2 := strings.Repeat("b", 64)
	m1 := Media{Sha256: sha1, RelPath: "aa/" + sha1 + ".png", Mime: "image/png", Bytes: 1000, CreatedAt: now}
	m2 := Media{Sha256: sha2, RelPath: "bb/" + sha2 + ".png", Mime: "image/png", Bytes: 500, CreatedAt: now}
	if err := db.Create(&m1).Error; err != nil {
		t.Fatalf("create media 1: %v", err)
	}
	if err := db.Create(&m2).Error; err != nil {
		t.Fatalf("create media 2: %v", err)
	}

	// u1 的 note：同时以 /media/<id> 引用 m1（出现两次）与 m2。
	noteU1 := Note{
		DeckID: deckID, Kind: "basic", TagsJSON: "[]",
		FieldsJSON: fmt.Sprintf(`{"front":"![](/media/%d) /media/%d","back":"![](/media/%d)"}`,
			m1.ID, m1.ID, m2.ID),
		CreatedBy: &u1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&noteU1).Error; err != nil {
		t.Fatalf("create u1 note: %v", err)
	}
	// u2 的 note：只引用 m1（跨用户重复引用，不应影响 u1 的用量）。
	noteU2 := Note{
		DeckID: deckID, Kind: "basic", TagsJSON: "[]",
		FieldsJSON: fmt.Sprintf(`{"front":"![](/media/%d)"}`, m1.ID),
		CreatedBy:  &u2, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&noteU2).Error; err != nil {
		t.Fatalf("create u2 note: %v", err)
	}

	usage1, err := UserMediaUsage(ctx, db, u1)
	if err != nil {
		t.Fatalf("UserMediaUsage(u1) error = %v", err)
	}
	if usage1.Bytes != 1500 {
		t.Fatalf("u1 usage = %d, want 1500 (m1 counted once + m2)", usage1.Bytes)
	}
	if len(usage1.Sha256) != 2 || !usage1.Sha256[sha1] || !usage1.Sha256[sha2] {
		t.Fatalf("u1 sha set = %v, want {%s,%s}", usage1.Sha256, sha1, sha2)
	}
	usage2, err := UserMediaUsage(ctx, db, u2)
	if err != nil {
		t.Fatalf("UserMediaUsage(u2) error = %v", err)
	}
	if usage2.Bytes != 1000 {
		t.Fatalf("u2 usage = %d, want 1000 (its own reference to m1)", usage2.Bytes)
	}

	// 卡组包形态的引用也识别：给 u1 加一条只引用 m2 的 sha 形态 note → 仍只加 500。
	pkgNote := Note{
		DeckID: deckID, Kind: "basic", TagsJSON: "[]",
		FieldsJSON: fmt.Sprintf(`{"front":"![](media/%s.png)"}`, sha2),
		CreatedBy:  &u1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&pkgNote).Error; err != nil {
		t.Fatalf("create sha-form note: %v", err)
	}
	if usage1, err = UserMediaUsage(ctx, db, u1); err != nil {
		t.Fatalf("UserMediaUsage(u1) after sha note error = %v", err)
	}
	if usage1.Bytes != 1500 {
		t.Fatalf("u1 usage after sha-form note = %d, want 1500 (no double count)", usage1.Bytes)
	}

	// 删除 u1 的两条引用 note（软删除）→ 配额全部释放；u2 不受影响。
	if err := db.Delete(&noteU1).Error; err != nil {
		t.Fatalf("soft delete u1 note: %v", err)
	}
	if err := db.Delete(&pkgNote).Error; err != nil {
		t.Fatalf("soft delete pkg note: %v", err)
	}
	if usage1, err = UserMediaUsage(ctx, db, u1); err != nil {
		t.Fatalf("UserMediaUsage(u1) after delete error = %v", err)
	}
	if usage1.Bytes != 0 || len(usage1.Sha256) != 0 {
		t.Fatalf("u1 usage after delete = %d (%d shas), want 0", usage1.Bytes, len(usage1.Sha256))
	}
	if usage2, err = UserMediaUsage(ctx, db, u2); err != nil {
		t.Fatalf("UserMediaUsage(u2) after u1 delete error = %v", err)
	}
	if usage2.Bytes != 1000 {
		t.Fatalf("u2 usage after u1 delete = %d, want 1000 (unchanged)", usage2.Bytes)
	}
}
