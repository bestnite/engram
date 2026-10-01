// Command flashcard 是服务唯一入口：单二进制 + 子命令（DESIGN.md §10.2）。
// 依赖在这里显式装配（AGENTS.md §2.4）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"gorm.io/gorm"

	"example.com/flashcard/internal/config"
	"example.com/flashcard/internal/store"
	"example.com/flashcard/internal/web"
)

// version 由构建时注入：-ldflags "-X main.version=<tag>"；未注入时为 dev。
var version = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		// 默认子命令是 serve，与 DESIGN.md §10.2 一致。
		return runServe(ctx)
	}
	switch args[0] {
	case "serve":
		return runServe(ctx)
	case "schema":
		return runSchema(ctx, args[1:])
	case "export":
		return runExport(args[1:])
	case "optimize":
		return runOptimize(args[1:])
	case "version":
		fmt.Println(version)
		return nil
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q: valid values are serve, schema sync, export, optimize, version", args[0])
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: flashcard <command>")
	fmt.Fprintln(os.Stderr, "  serve           start the HTTP server (default)")
	fmt.Fprintln(os.Stderr, "  schema sync     run AutoMigrate and the registered destructive migrations")
	fmt.Fprintln(os.Stderr, "  export          export the database (implemented in M4)")
	fmt.Fprintln(os.Stderr, "  optimize        run a parameter-optimisation job (implemented in M9)")
	fmt.Fprintln(os.Stderr, "  version         print the version string")
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// loadConfig 读取环境变量；数据库可用时再叠加 settings 表覆盖值（DESIGN.md §8.4 的优先级）。
func loadConfig(ctx context.Context, db *gorm.DB) (*config.Config, error) {
	cfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		return nil, err
	}
	// settings 表可能还不存在（首次启动尚未迁移），此时只用环境变量。
	if db != nil && db.Migrator().HasTable(&store.Setting{}) {
		settings, err := store.LoadSettings(ctx, db)
		if err != nil {
			return nil, err
		}
		cfg, err = config.Load(os.LookupEnv, settings)
		if err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func runServe(ctx context.Context) error {
	logger := newLogger()
	slog.SetDefault(logger)

	envCfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		return err
	}
	db, err := store.Open(envCfg.Get(config.KeyDBDriver).Value, envCfg.Get(config.KeyDBDSN).Value)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(ctx, db)
	if err != nil {
		return err
	}

	if parseBool(cfg.Get(config.KeyAutoMigrate).Value) {
		applied, err := store.Sync(ctx, db, store.BuiltinMigrations)
		if err != nil {
			return err
		}
		logger.Info("schema synchronized", "migrations_applied", applied)
	}

	srv, err := web.New(cfg.Get(config.KeyHTTPAddr).Value, web.Deps{
		DB:     db,
		Logger: logger,
		SchemaVersion: func(ctx context.Context) (int, error) {
			return store.CurrentVersion(ctx, db)
		},
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Run(ctx)
}

func runSchema(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "sync" {
		return errors.New("usage: flashcard schema sync")
	}
	logger := newLogger()
	cfg, err := config.Load(os.LookupEnv, nil)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.Get(config.KeyDBDriver).Value, cfg.Get(config.KeyDBDSN).Value)
	if err != nil {
		return err
	}
	applied, err := store.Sync(ctx, db, store.BuiltinMigrations)
	if err != nil {
		return err
	}
	version, err := store.CurrentVersion(ctx, db)
	if err != nil {
		return err
	}
	logger.Info("schema synchronized", "migrations_applied", applied, "schema_version", version)
	return nil
}

func runExport(args []string) error {
	return errors.New("export is not implemented yet (planned for M4)")
}

func runOptimize(args []string) error {
	return errors.New("optimize is not implemented yet (planned for M9)")
}

// parseBool 接受 "1"/"true"/"yes" 之类的常见写法；无法解析时按 false 处理。
func parseBool(raw string) bool {
	if raw == "" {
		return false
	}
	if v, err := strconv.ParseBool(raw); err == nil {
		return v
	}
	return false
}
