package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMailTemplateStoreUpsertAndDelete covers every action of the template store. It runs both drivers: the unique key is a
// composite primary key and OnConflict provides the overwrite, both of which are driver-specific (AGENTS.md §2.3.4).
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

			// no row is a normal state, not an error.
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

			// storing again replaces wholesale rather than inserting a second row.
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

			// another locale of the same type is an independent row.
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
			// deleting a missing row is not a failure: the intent is 'back to the built-in', and that state already holds.
			if err := store.Delete(ctx, "password_reset", "zh-CN"); err != nil {
				t.Errorf("Delete() of a missing row error = %v, want nil", err)
			}
		})
	}
}
