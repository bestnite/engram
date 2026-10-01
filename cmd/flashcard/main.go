// Command flashcard 是服务唯一入口：单二进制 + 子命令（DESIGN.md §10.2）。
// 依赖在这里显式装配（AGENTS.md §2.4）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
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

func runServe(ctx context.Context) error {
	_ = ctx
	return errors.New("serve is not implemented yet: the HTTP skeleton lands in M0-5")
}

func runSchema(ctx context.Context, args []string) error {
	_ = ctx
	if len(args) != 1 || args[0] != "sync" {
		return errors.New("usage: flashcard schema sync")
	}
	return errors.New("schema sync is not implemented yet: schema versioning lands in M0-4")
}

func runExport(args []string) error {
	_ = args
	return errors.New("export is not implemented yet (planned for M4)")
}

func runOptimize(args []string) error {
	_ = args
	return errors.New("optimize is not implemented yet (planned for M9)")
}
