package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"gorm.io/gorm"

	"example.com/flashcard/internal/api"
	"example.com/flashcard/internal/auth"
	"example.com/flashcard/internal/config"
	"example.com/flashcard/internal/store"
)

// cliOut 是 CLI 的诊断/报告输出目标；测试替换它来捕获输出（默认 stdout）。
var cliOut io.Writer = os.Stdout

// 卡组包的 CLI 入口（AGENTS.md M5-8、DESIGN.md §7.6）。
//
// CLI 是无会话的运维/自动化通道：导出/导入都通过 --user 指定acting user，
// 业务与权限判定复用 internal/api 的 service 方法，避免出现第二套规则。

// openPackageCLI 打开数据库并按与 serve 相同的 service 路径装配所需依赖。
func openPackageCLI(ctx context.Context) (*api.API, *store.UserStore, *gorm.DB, error) {
	cfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	db, err := store.Open(cfg.Get(config.KeyDBDriver).Value, cfg.Get(config.KeyDBDSN).Value)
	if err != nil {
		return nil, nil, nil, err
	}
	auditor, err := auth.NewAuditor(store.NewAuditStore(db))
	if err != nil {
		return nil, nil, nil, err
	}
	users := store.NewUserStore(db)
	apiSrv, err := api.New(api.Deps{
		DB:        db,
		Logger:    newLogger(),
		Keys:      store.NewAPIKeyStore(db),
		Users:     users,
		Decks:     store.NewDeckStore(db),
		Notes:     store.NewNoteStore(db),
		Presets:   store.NewPresetStore(db),
		Cards:     store.NewCardStore(db),
		Auditor:   auditor,
		MediaRoot: cfg.Get(config.KeyMediaDir).Value,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return apiSrv, users, db, nil
}

// resolveCLIUser 决定 CLI 操作的 acting user：显式 --user 优先；未给出时导出回落到卡组
// owner（只读拿自己的内容），导入回落到最早的管理员。
func resolveCLIUser(ctx context.Context, users *store.UserStore, db *gorm.DB, explicit uint64, deckID uint64) (*store.User, error) {
	if explicit != 0 {
		u, err := users.ByID(ctx, explicit)
		if err != nil {
			return nil, fmt.Errorf("load user %d: %w", explicit, err)
		}
		return u, nil
	}
	if deckID != 0 {
		deck, err := store.NewDeckStore(db).ByID(ctx, deckID)
		if err != nil {
			return nil, fmt.Errorf("load deck %d: %w", deckID, err)
		}
		return users.ByID(ctx, deck.OwnerUserID)
	}
	var u store.User
	err := db.WithContext(ctx).Where("role = ? AND status = ?", store.RoleAdmin, store.StatusActive).Order("id ASC").First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("no acting user: pass --user <id> (no active admin exists)")
	}
	if err != nil {
		return nil, fmt.Errorf("resolve default user: %w", err)
	}
	return &u, nil
}

// runExport 处理 `flashcard export`：带 --package 时导出卡组包；否则保留全库导出的占位错误。
func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	deckID := fs.Uint64("deck", 0, "deck id to export as a .fdeck card package")
	out := fs.String("package", "", "output .fdeck path")
	userID := fs.Uint64("user", 0, "acting user id (default: the deck owner)")
	includeProgress := fs.Bool("include-progress", false, "include the caller's own review progress")
	includeReviews := fs.Bool("include-reviews", false, "include review logs (requires --include-progress)")
	noMedia := fs.Bool("no-media", false, "do not inline media bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("export: --package <path> is required to export a deck package; full-database export is not implemented yet")
	}
	if *deckID == 0 {
		return errors.New("export: --deck <id> is required")
	}
	ctx := context.Background()
	apiSrv, users, db, err := openPackageCLI(ctx)
	if err != nil {
		return err
	}
	actor, err := resolveCLIUser(ctx, users, db, *userID, *deckID)
	if err != nil {
		return err
	}
	pkg, err := apiSrv.ExportDeckPackage(ctx, actor.ID, *deckID, *includeProgress, !*noMedia, *includeReviews)
	if err != nil {
		return err
	}
	f, err := os.Create(*out)
	if err != nil {
		return fmt.Errorf("create %s: %w", *out, err)
	}
	if err := pkg.WriteZip(f); err != nil {
		f.Close()
		return fmt.Errorf("write package: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", *out, err)
	}
	fmt.Fprintf(cliOut, "exported deck %d: %d notes, %d cards -> %s\n", *deckID, len(pkg.Notes), len(pkg.Cards), *out)
	return nil
}

// runImport 处理 `flashcard import`：导入一个卡组包并打印与 REST 一致的报告 JSON。
func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	in := fs.String("package", "", "input .fdeck path")
	userID := fs.Uint64("user", 0, "acting user id (default: the earliest active admin)")
	target := fs.String("target", "", "new_deck (default), into_deck:<id> or replace_deck:<id>")
	dryRun := fs.Bool("dry-run", false, "validate and count without writing")
	onConflict := fs.String("on-conflict", "", "conflict policy: skip, update (default) or fail")
	skipMissing := fs.Bool("skip-missing-media", false, "skip missing media instead of failing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("import: --package <path> is required")
	}
	ctx := context.Background()
	apiSrv, users, db, err := openPackageCLI(ctx)
	if err != nil {
		return err
	}
	actor, err := resolveCLIUser(ctx, users, db, *userID, 0)
	if err != nil {
		return err
	}
	f, err := os.Open(*in)
	if err != nil {
		return fmt.Errorf("open %s: %w", *in, err)
	}
	defer f.Close()
	report, err := apiSrv.ImportDeckPackage(ctx, actor, nil, f, store.PackageImportOptions{
		Target:           *target,
		DryRun:           *dryRun,
		OnConflict:       *onConflict,
		SkipMissingMedia: *skipMissing,
	})
	if err != nil {
		return err
	}
	enc, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(cliOut, string(enc))
	return nil
}
