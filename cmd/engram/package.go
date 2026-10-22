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

// cliOut is the diagnostic/report output target for the CLI; tests replace it to capture output (defaults to stdout).
var cliOut io.Writer = os.Stdout

// CLI entry points for deck packages.
//
// The CLI is a session-less operations/automation channel: export/import name the acting user via --user, and
// business and permission decisions reuse internal/api's service methods, so there is no second set of rules.

// openPackageCLI opens the database and assembles the needed dependencies through the same service path as serve.
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

// resolveCLIUser decides the acting user for export (read-only) operations: an explicit --user wins, and when it is absent it
// falls back to the deck owner (export is read-only, taking your own content). Import no longer goes through here: import writes
// under an account, so the identity must be stated explicitly (CLI identity ruling); see resolveImportUser.
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

// resolveImportUser resolves the value of `import --user`, accepting a numeric id or a username.
//
// Import writes content under some account, so the caller must state the identity: an empty value, a value naming a
// nonexistent user, and an ambiguous value where "a number looks like both an id and a username that point to different users"
// are all rejected by name, never falling back to any default account (CLI identity ruling).
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
			// The same value hits both an id and a username pointing to different users — guessing wrong means handing the cards to someone else.
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

// runExport handles `engram export`: export one deck package (--deck + --package).
func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	deckID := fs.Uint64("deck", 0, "deck id to export as a .edeck card package")
	out := fs.String("package", "", "output .edeck path")
	userID := fs.Uint64("user", 0, "acting user id (default: the deck owner)")
	includeProgress := fs.Bool("include-progress", false, "include the caller's own review progress")
	includeReviews := fs.Bool("include-reviews", false, "include review logs (requires --include-progress)")
	noMedia := fs.Bool("no-media", false, "do not inline media bytes")
	includeWeights := fs.Bool("include-weights", false, "include the caller's FSRS weights in preset.json")
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
	pkg, err := apiSrv.ExportDeckPackage(ctx, actor.ID, *deckID, store.PackageOptions{
		IncludeProgress: *includeProgress,
		IncludeMedia:    !*noMedia,
		IncludeReviews:  *includeReviews,
		IncludeWeights:  *includeWeights,
	})
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

// runImport handles `engram import`: import one deck package and print the same report JSON as REST.
func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	in := fs.String("package", "", "input .edeck path")
	userRef := fs.String("user", "", "acting user id or username (required: the import never guesses the owner)")
	target := fs.String("target", "", "new_deck (default), into_deck:<id> or replace_deck:<id>")
	dryRun := fs.Bool("dry-run", false, "validate and count without writing")
	onConflict := fs.String("on-conflict", "", "conflict policy: skip, update (default) or fail")
	skipMissing := fs.Bool("skip-missing-media", false, "skip missing media instead of failing")
	applyWeights := fs.Bool("apply-weights", false, "use the package's FSRS weights for the new deck's preset (new_deck only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return errors.New("import: --package <path> is required")
	}
	// Validate the identity before touching the database: without --user it exits non-zero right away, never opens the package, let alone writes any deck/note.
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
		ApplyWeights:     *applyWeights,
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
