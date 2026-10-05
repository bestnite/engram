package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/config"
	"git.nite07.com/nite/engram/internal/store"
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

// resolveCLIUser 决定导出（只读）操作的 acting user：显式 --user 优先，未给出时回落到卡组
// owner（导出只读，拿走自己的内容）。导入不再走这里：导入会写入账号名下，必须显式写明身份
// （DESIGN.md §7.6 的 CLI 身份裁定、AGENTS.md M5-12），见 resolveImportUser。
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

// resolveImportUser 解析 `import --user` 的取值，接受数字 id 或用户名。
//
// 导入会把内容写进某个账号名下，身份必须由调用者写明：空值、指向不存在的用户、以及
// 「数字既像 id 又像用户名且指向不同用户」的歧义取值都点名拒绝，绝不回落到任何默认账号
// （DESIGN.md §7.6 的 CLI 身份裁定、AGENTS.md M5-12）。
func resolveImportUser(ctx context.Context, users *store.UserStore, ref string) (*store.User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("import: --user <id|username> is required: the import files the deck under one account, so the acting identity must be explicit (refusing to fall back to any default user)")
	}
	if id, err := strconv.ParseUint(ref, 10, 64); err == nil {
		byID, idErr := users.ByID(ctx, id)
		byName, nameErr := users.ByUsername(ctx, ref)
		switch {
		case idErr == nil && nameErr == nil && byID.ID != byName.ID:
			// 同一个取值同时命中一个 id 和一个用户名，且指向不同用户 —— 猜错就等于把卡塞给别人。
			return nil, fmt.Errorf("import: --user %q is ambiguous: it matches user id %d and the username %q (user id %d); pass a value that names exactly one account", ref, byID.ID, ref, byName.ID)
		case idErr == nil:
			return byID, nil
		case nameErr == nil:
			return byName, nil
		case !errors.Is(idErr, gorm.ErrRecordNotFound):
			return nil, fmt.Errorf("import: resolve --user %q: %w", ref, idErr)
		default:
			return nil, fmt.Errorf("import: --user %q: no user with that id or username exists", ref)
		}
	}
	u, err := users.ByUsername(ctx, ref)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("import: --user %q: no user with that username exists", ref)
	}
	if err != nil {
		return nil, fmt.Errorf("import: resolve --user %q: %w", ref, err)
	}
	return u, nil
}

// runExport 处理 `engram export`：导出一个卡组包（--deck + --package）。
func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	deckID := fs.Uint64("deck", 0, "deck id to export as a .edeck card package")
	out := fs.String("package", "", "output .edeck path")
	userID := fs.Uint64("user", 0, "acting user id (default: the deck owner)")
	includeProgress := fs.Bool("include-progress", false, "include the caller's own review progress")
	includeReviews := fs.Bool("include-reviews", false, "include review logs (requires --include-progress)")
	noMedia := fs.Bool("no-media", false, "do not inline media bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("export: --package <path> is required to export a deck package")
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

// runImport 处理 `engram import`：导入一个卡组包并打印与 REST 一致的报告 JSON。
func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	in := fs.String("package", "", "input .edeck path")
	userRef := fs.String("user", "", "acting user id or username (required: the import never guesses the owner)")
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
	// 先校验身份再碰数据库：缺 --user 时直接非零退出，绝不打开包、更不会落任何卡组/笔记。
	if strings.TrimSpace(*userRef) == "" {
		return errors.New("import: --user <id|username> is required: the import files the deck under one account, so the acting identity must be explicit (refusing to fall back to the earliest admin)")
	}
	ctx := context.Background()
	apiSrv, users, _, err := openPackageCLI(ctx)
	if err != nil {
		return err
	}
	actor, err := resolveImportUser(ctx, users, *userRef)
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
