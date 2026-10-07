package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMailTemplateStoreUpsertAndDelete 覆盖模板存储的全部动作。跑双驱动：唯一键是
// 复合主键 + OnConflict 覆盖，这两点都是驱动相关的（AGENTS.md §2.3.4）。
func TestMailTemplateStoreUpsertAndDelete(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			store := NewMailTemplateStore(db)
			editor := uint64(7)
			first := &MailTemplate{
				Type: "password_reset", Locale: "zh-CN",
				Subject: "重置 {{site}}", BodyMD: "点这里：{{url}}", UpdatedBy: &editor,
				UpdatedAt: time.Now().UTC(),
			}

			// 没有行是正常状态，不是错误。
			if _, err := store.ByTypeLocale(ctx, "password_reset", "zh-CN"); !errors.Is(err, ErrMailTemplateNotFound) {
				t.Fatalf("ByTypeLocale() error = %v, want ErrMailTemplateNotFound", err)
			}

			if err := store.Upsert(ctx, first); err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}
			got, err := store.ByTypeLocale(ctx, "password_reset", "zh-CN")
			if err != nil {
				t.Fatalf("ByTypeLocale() error = %v", err)
			}
			if got.Subject != "重置 {{site}}" || got.BodyMD != "点这里：{{url}}" {
				t.Errorf("stored template = %+v, want the saved subject and body", got)
			}
			if got.UpdatedBy == nil || *got.UpdatedBy != editor {
				t.Errorf("UpdatedBy = %v, want %d", got.UpdatedBy, editor)
			}

			// 再存一次是整份覆盖，不是插入第二行。
			second := &MailTemplate{
				Type: "password_reset", Locale: "zh-CN",
				Subject: "new", BodyMD: "new body", UpdatedAt: time.Now().UTC(),
			}
			if err := store.Upsert(ctx, second); err != nil {
				t.Fatalf("Upsert() second error = %v", err)
			}
			rows, err := store.List(ctx)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("List() = %d rows, want 1 (upsert must overwrite, not insert)", len(rows))
			}
			if rows[0].Subject != "new" || rows[0].UpdatedBy != nil {
				t.Errorf("row after overwrite = %+v, want the new subject and a cleared editor", rows[0])
			}

			// 同一类型另一语言是独立一行。
			other := &MailTemplate{Type: "password_reset", Locale: "en", Subject: "s", BodyMD: "b", UpdatedAt: time.Now().UTC()}
			if err := store.Upsert(ctx, other); err != nil {
				t.Fatalf("Upsert(other locale) error = %v", err)
			}
			if rows, err = store.List(ctx); err != nil || len(rows) != 2 {
				t.Fatalf("List() = %d rows / %v, want 2", len(rows), err)
			}

			if err := store.Delete(ctx, "password_reset", "zh-CN"); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if _, err := store.ByTypeLocale(ctx, "password_reset", "zh-CN"); !errors.Is(err, ErrMailTemplateNotFound) {
				t.Errorf("after Delete() error = %v, want ErrMailTemplateNotFound", err)
			}
			// 删不存在的行不算失败：意图是「回到内置」，那个状态已经成立。
			if err := store.Delete(ctx, "password_reset", "zh-CN"); err != nil {
				t.Errorf("Delete() of a missing row error = %v, want nil", err)
			}
		})
	}
}
