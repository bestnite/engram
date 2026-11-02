package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"git.nite07.com/nite/engram/internal/notetype"
)

// BenchmarkNoteTypeUpdate measures a note type save on a local SQLite database at several type
// sizes. The numbers describe one machine and are recorded for comparison, not guarded by a
// threshold: the statement-count tests are what pin the complexity. Run it with
//
//	go test ./internal/store/ -run '^$' -bench BenchmarkNoteTypeUpdate -benchtime=3x
//
// The `css` case changes only presentation data, so it must not walk the notes. The `template`
// case changes one template's answer format, so it keeps the same generated card set but still
// resyncs every note; it measures the batched card read.
func BenchmarkNoteTypeUpdate(b *testing.B) {
	for _, size := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("css/%d_notes", size), func(b *testing.B) {
			benchmarkNoteTypeUpdate(b, size, func(cfg *notetype.Config, i int) {
				cfg.CSS = fmt.Sprintf(".card { color: #%06x; }", i&0xffffff)
			})
		})
		b.Run(fmt.Sprintf("template/%d_notes", size), func(b *testing.B) {
			benchmarkNoteTypeUpdate(b, size, func(cfg *notetype.Config, i int) {
				cfg.Templates[0].AFormat = fmt.Sprintf("{{Back}} (variant %d)", i)
			})
		})
	}
}

func benchmarkNoteTypeUpdate(b *testing.B, size int, mutate func(cfg *notetype.Config, i int)) {
	b.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(b.TempDir(), "bench.db")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		b.Fatalf("open sqlite: %v", err)
	}
	b.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(AllModels()...); err != nil {
		b.Fatalf("AutoMigrate() error = %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	owner := User{Username: "bench_owner", Email: "bench_owner@example.com", DisplayName: "Bench Owner",
		Role: RoleUser, Status: StatusActive, Locale: "zh-CN", Timezone: "Asia/Shanghai", CreatedAt: now}
	if err := db.Create(&owner).Error; err != nil {
		b.Fatalf("create user: %v", err)
	}
	preset := Preset{OwnerUserID: owner.ID, Name: "default", DesiredRetention: 0.9,
		LearningSteps: "1m,10m", RelearningSteps: "10m", MaximumIntervalDays: 36500,
		EnableFuzz: Ptr(true), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&preset).Error; err != nil {
		b.Fatalf("create preset: %v", err)
	}
	deck := Deck{OwnerUserID: owner.ID, Name: "Bench", PresetID: preset.ID, CreatedAt: now}
	if err := db.Create(&deck).Error; err != nil {
		b.Fatalf("create deck: %v", err)
	}
	types := NewNoteTypeStore(db)
	nt := NoteType{DeckID: deck.ID, Name: "Bench"}
	if err := types.Create(ctx, &nt, noteTypeConfig("Card 1")); err != nil {
		b.Fatalf("create note type: %v", err)
	}
	notes := NewNoteStore(db)
	for i := 0; i < size; i++ {
		note := Note{DeckID: deck.ID, NoteTypeID: &nt.ID}
		if _, err := notes.Create(ctx, &note, map[string]any{"Front": "Q", "Back": "A"}); err != nil {
			b.Fatalf("create note %d: %v", i, err)
		}
	}

	current := nt
	baseCfg := noteTypeConfig("Card 1")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cfg := baseCfg
		cfg.Templates = append([]notetype.Template(nil), baseCfg.Templates...)
		mutate(&cfg, i)
		if _, err := types.Update(ctx, &current, cfg, NoteTypeUpdateOptions{
			ExpectedVersion:    current.Version,
			ConfirmCardRemoval: true,
		}); err != nil {
			b.Fatalf("Update() at %d notes: %v", size, err)
		}
	}
}
