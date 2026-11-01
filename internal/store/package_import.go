package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// Deck package import.
//
// Safety is the focus: reject path traversal and symlinks before extraction, cap the total extracted size and the entry count (zip bomb defence);
// all business writes happen in one transaction (a failure leaves nothing half done); repeated imports de-duplicate by external_ref or content fingerprint.

// Three kinds of import target.
const (
	// PackageTargetNewDeck creates a new deck under the package's deck name (a suffix is added on a name clash).
	PackageTargetNewDeck = "new_deck"
	// PackagePresetNew creates a preset from the imported package.
	PackagePresetNew = "new"
	// PackagePresetReuse attaches an existing owned preset.
	PackagePresetReuse = "reuse"
	// PackagePresetCopy attaches a copy of an existing owned preset.
	PackagePresetCopy = "copy"
)

// PackageLimits holds the extraction safety caps ("safety").
type PackageLimits struct {
	MaxEntries    int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// DefaultPackageLimits gives conservative default caps against zip bombs.
func DefaultPackageLimits() PackageLimits {
	return PackageLimits{MaxEntries: 2000, MaxFileBytes: 32 << 20, MaxTotalBytes: 128 << 20}
}

// Settings keys and environment variables for the import caps. Each resolves as environment
// variable, then settings table, then its default, the same order media_max_bytes uses.
const (
	SettingKeyPackageMaxBytes         = "package_max_bytes"
	EnvPackageMaxBytes                = "PACKAGE_MAX_BYTES"
	SettingKeyPackageMaxEntries       = "package_max_entries"
	EnvPackageMaxEntries              = "PACKAGE_MAX_ENTRIES"
	SettingKeyPackageMaxUnpackedBytes = "package_max_unpacked_bytes"
	EnvPackageMaxUnpackedBytes        = "PACKAGE_MAX_UNPACKED_BYTES"
	// SettingKeyPackageMaxConcurrentImports bounds how many package imports run at the same time in
	// one server process. Every import may spool up to package_max_bytes to a temporary file before
	// it parses the package, so an unbounded number of simultaneous imports can fill the temporary
	// disk even though each one stays within its own cap.
	SettingKeyPackageMaxConcurrentImports = "package_max_concurrent_imports"
	EnvPackageMaxConcurrentImports        = "PACKAGE_MAX_CONCURRENT_IMPORTS"
)

// Defaults of the import caps: 2 GiB accepted package, 100 000 entries, 4 GiB decompressed.
const (
	defaultPackageMaxBytes         = 2 << 30
	defaultPackageMaxEntries       = 100_000
	defaultPackageMaxUnpackedBytes = 4 << 30
)

// DefaultPackageMaxConcurrentImports is the default number of package imports one server process
// runs at once.
const DefaultPackageMaxConcurrentImports = 2

// ResolvePackageMaxConcurrentImports resolves the concurrency cap: environment variable, then
// settings table, then the default 2.
func ResolvePackageMaxConcurrentImports(ctx context.Context, db *gorm.DB) int {
	return int(resolvePackageInt64(ctx, db, SettingKeyPackageMaxConcurrentImports, EnvPackageMaxConcurrentImports, DefaultPackageMaxConcurrentImports))
}

// PackageImportLimits are the deployment caps applied to one package import.
//
// MaxPackageBytes bounds the body a transport accepts before it is spooled. MaxEntries and
// MaxUnpackedBytes bound the .apkg archive.
type PackageImportLimits struct {
	MaxPackageBytes  int64
	MaxEntries       int
	MaxUnpackedBytes int64
}

// DefaultPackageImportLimits returns the documented defaults.
func DefaultPackageImportLimits() PackageImportLimits {
	return PackageImportLimits{
		MaxPackageBytes:  defaultPackageMaxBytes,
		MaxEntries:       defaultPackageMaxEntries,
		MaxUnpackedBytes: defaultPackageMaxUnpackedBytes,
	}
}

// effective fills every unset field with its default, so callers that do not resolve the
// settings (tests, the CLI) still import under the documented caps.
func (l PackageImportLimits) effective() PackageImportLimits {
	def := DefaultPackageImportLimits()
	if l.MaxPackageBytes <= 0 {
		l.MaxPackageBytes = def.MaxPackageBytes
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = def.MaxEntries
	}
	if l.MaxUnpackedBytes <= 0 {
		l.MaxUnpackedBytes = def.MaxUnpackedBytes
	}
	return l
}

// ResolvePackageLimits resolves all three import caps from the environment, the settings table
// and the defaults, in that order.
func ResolvePackageLimits(ctx context.Context, db *gorm.DB) PackageImportLimits {
	return PackageImportLimits{
		MaxPackageBytes:  resolvePackageInt64(ctx, db, SettingKeyPackageMaxBytes, EnvPackageMaxBytes, defaultPackageMaxBytes),
		MaxEntries:       int(resolvePackageInt64(ctx, db, SettingKeyPackageMaxEntries, EnvPackageMaxEntries, defaultPackageMaxEntries)),
		MaxUnpackedBytes: resolvePackageInt64(ctx, db, SettingKeyPackageMaxUnpackedBytes, EnvPackageMaxUnpackedBytes, defaultPackageMaxUnpackedBytes),
	}
}

// resolvePackageInt64 is the one resolution rule of a positive byte or count cap: a valid
// positive environment value wins, then a valid positive settings value, then the default.
func resolvePackageInt64(ctx context.Context, db *gorm.DB, key, env string, def int64) int64 {
	if raw := strings.TrimSpace(os.Getenv(env)); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	if db != nil {
		if settings, err := LoadSettings(ctx, db); err == nil {
			if raw, ok := settings[key]; ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && n > 0 {
					return n
				}
			}
		}
	}
	return def
}

// The length bounds for deck name and description live in deck.go alone (creation/rename and deck package import share one source).
// The old exported names are kept here so references already scattered around do not drift.
const (
	maxPackageDeckNameChars        = maxDeckNameChars
	maxPackageDeckDescriptionChars = maxDeckDescriptionChars
)

// deckMetaErrors validates the manifest's deck name and description, returning entry-level reasons (English, worded like the existing entries).
//
// Rules:
//   - lengths count runes: deck name ≤ 200, description ≤ 2000;
//   - reject every C0 control character (U+0000–U+001F), \n, 	 and \r included, with no exception carved out:
//     a deck name/description has no scenario needing a line break, and these characters wreck pages and logs and interfere with uniqueDeckName's display;
//   - reject invalid UTF-8: the rune count that follows and the persistence both assume the input is valid UTF-8.
//
// Empty values are not validated: an empty deck name is existing semantics (the caller falls back to Imported deck).
func deckMetaErrors(name, description string) []string {
	var entries []string
	if name != "" {
		entries = append(entries, textFieldErrors("deck.name", name, maxPackageDeckNameChars)...)
	}
	if description != "" {
		entries = append(entries, textFieldErrors("deck.description", description, maxPackageDeckDescriptionChars)...)
	}
	return entries
}

// textFieldErrors validates one text field's length, control characters and UTF-8 validity.
func textFieldErrors(field, value string, maxChars int) []string {
	if !utf8.ValidString(value) {
		// Under invalid UTF-8 a rune count is meaningless, so only this one entry is reported.
		return []string{field + ": invalid UTF-8"}
	}
	var entries []string
	if n := utf8.RuneCountInString(value); n > maxChars {
		entries = append(entries, fmt.Sprintf("%s: exceeds %d characters (got %d)", field, maxChars, n))
	}
	if r, ok := firstControlRune(value); ok {
		entries = append(entries, fmt.Sprintf("%s: contains a control character U+%04X", field, r))
	}
	return entries
}

// firstControlRune returns the first C0 control character (U+0000–U+001F) in the value.
func firstControlRune(s string) (rune, bool) {
	for _, r := range s {
		if r <= 0x1f {
			return r, true
		}
	}
	return 0, false
}

// packageDeckMetaError wraps entry-level reasons into a stable PackageError; nil when there are no reasons.
func packageDeckMetaError(entries []string) error {
	if len(entries) == 0 {
		return nil
	}
	return &PackageError{
		Code:    CodePackageDeckMetaInvalid,
		Message: "the deck name or description in the package is invalid",
		Entries: entries,
	}
}

// PackageImportOptions controls one import.
type PackageImportOptions struct {
	// PresetMode selects new, reuse or copy for new_deck; empty defaults to new.
	// For into_deck and replace_deck, only empty or new is accepted, without PresetID.
	PresetMode string
	// PresetID identifies the preset for reuse or copy. ValidateImportPreset checks its owner
	// during validation, and selectImportPreset checks its owner again before use.
	PresetID uint64
	// Target takes new_deck (the default), into_deck:<id>, replace_deck:<id>.
	Target string
	DryRun bool
	// OnConflict takes skip / update (the default) / fail (roll back the whole package).
	OnConflict string
	// AllowOthersProgress is an admin switch (off by default): allow someone else's progress in the package to be imported into one's own account.
	AllowOthersProgress bool
	// IncludeProgress imports scheduling from Anki packages for the importing user.
	IncludeProgress bool
	// With SkipMissingMedia=true, missing media is only counted and the import continues; default false (missing media fails the import and lists them).
	SkipMissingMedia bool
	// ApplyWeights defaults to off: the weights in the package are the exporter's memory curve, and by default the importer starts from the default weights, gathering their own
	// reviews before optimizing. It only takes effect with the new_deck target -- merge and replace leave the target deck's existing preset alone.
	// When off, the new preset's weights_optimized_at and weights_review_count are left empty as well.
	ApplyWeights bool
	// MediaQuotaBytes is the importer's currently effective per-user media quota in bytes; 0 means unlimited.
	//
	// The media bytes newly added by the package (de-duplicated by sha256, minus the shas the importer is already charged for) count against the **importer's** quota;
	// exceeding it fails the whole package and leaves no partial media. The caller resolves the value with media.ResolveUserQuotaBytes and passes it in,
	// so the web / REST / MCP / CLI entry points share one definition.
	MediaQuotaBytes int64
	// MediaRoot is the root directory for media bytes on disk; empty means media is not written to disk (only when needed).
	MediaRoot string
	// Limits are the deployment caps of this import. The zero value uses DefaultPackageImportLimits,
	// so a caller that does not resolve the settings still gets the documented caps.
	Limits PackageImportLimits
	// NewDeckName optionally overrides the deck name for new_deck.
	NewDeckName string
	// Now is an injectable clock; the zero value uses the system UTC time.
	Now func() time.Time
}

// PackageImportError is one per-entry import error: Entry names the offending package entry (e.g. notes[3]).
type PackageImportError struct {
	Entry  string `json:"entry"`
	Reason string `json:"reason"`
}

// PackageImportReport is the report of an import (dry_run included), shaped like the bulk import report.
type PackageImportReport struct {
	// PresetMode reports the selection for a new deck and is omitted otherwise to protect shared settings.
	PresetMode string `json:"preset_mode,omitempty"`
	// PresetName reports the selected new deck preset and is omitted otherwise to protect shared settings.
	PresetName string `json:"preset_name,omitempty"`
	Target     string `json:"target"`
	DryRun     bool   `json:"dry_run"`
	// DeckID is the internal numeric primary key, for internal uses such as audit; what is exposed externally is DeckPublicID.
	DeckID       uint64 `json:"-"`
	DeckPublicID string `json:"deck_id,omitempty"`
	NotesCreated int    `json:"notes_created"`
	NotesUpdated int    `json:"notes_updated"`
	NotesSkipped int    `json:"notes_skipped"`
	CardsCreated int    `json:"cards_created"`
	// CardsSkipped counts package card rows that were not imported.
	CardsSkipped    int `json:"cards_skipped"`
	MediaNew        int `json:"media_new"`
	MediaMissing    int `json:"media_missing"`
	ProgressApplied int `json:"progress_applied"`
	ProgressSkipped int `json:"progress_skipped"`
	// ProgressDiscarded means the package's card states or reviews were discarded during import.
	ProgressDiscarded bool   `json:"progress_discarded"`
	StatesSkipped     int    `json:"states_skipped"`
	StatesDiscarded   int    `json:"states_discarded"`
	ReviewsApplied    int    `json:"reviews_applied"`
	ReviewsSkipped    int    `json:"reviews_skipped"`
	ReviewsDiscarded  int    `json:"reviews_discarded"`
	MatchRule         string `json:"match_rule,omitempty"`
	// MissingMedia names every media reference that the package does not carry bytes for.
	MissingMedia []string `json:"missing_media,omitempty"`
	// UnplayableVideo names the video files a browser cannot play (anything other than mp4 or webm).
	UnplayableVideo []string `json:"unplayable_video,omitempty"`
	// ExternalAssets names every absolute script or stylesheet reference found in note fields, templates or CSS.
	ExternalAssets []string `json:"external_assets,omitempty"`
	// LatexMissingImages names every LaTeX image a note refers to that the package does not carry.
	LatexMissingImages []string             `json:"latex_missing_images,omitempty"`
	Errors             []PackageImportError `json:"errors"`
	// WeightsApplied means the package's weights were written into the new preset.
	WeightsApplied bool `json:"weights_applied"`
	// WeightsDiscarded means the package carried weights that were not adopted, including rejected Anki weights.
	WeightsDiscarded bool `json:"weights_discarded"`
	// WeightsInvalid means the target is a new deck, applying weights was requested,
	// the package supplied weights, and fsrs.MigrateWeights rejected them.
	WeightsInvalid    bool `json:"weights_invalid"`
	DeckConfigSkipped bool `json:"deck_config_skipped"`
	// CardsRemoved counts the stored cards this import soft-deleted because the note they belong to
	// moved to a note type that no longer declares their template. The card_states and reviews rows
	// of those cards stay.
	CardsRemoved int `json:"cards_removed"`
}

// ErrPackageDryRun is the internal dry_run sentinel: once planning is done it rolls the transaction back, leaving no writes.
var ErrPackageDryRun = errors.New("deck package: dry run rollback")

// packageSource returns random access to the package. A caller that already produced a file —
// the URL transport, the CLI, or a multipart part that spilled to disk — hands it over directly;
// any other reader is spooled to one temporary file, capped at package_max_bytes. The .apkg
// reader opens the zip from this source, so neither the archive nor its media is held in memory.
func packageSource(r io.Reader, maxBytes int64) (io.ReaderAt, int64, func(), error) {
	if src, ok := r.(interface {
		io.ReaderAt
		Stat() (os.FileInfo, error)
	}); ok {
		info, err := src.Stat()
		if err != nil {
			return nil, 0, nil, &PackageError{Code: CodePackageBadFormat, Message: "cannot read archive", Entries: []string{err.Error()}}
		}
		if info.Size() > maxBytes {
			return nil, 0, nil, &PackageError{Code: CodePackageTooLarge, Message: "archive exceeds the package size limit"}
		}
		// The reader's owner removes it: the handler's defer, or the URL transport's Close.
		return src, info.Size(), nil, nil
	}
	file, size, cleanup, err := spoolPackage(r, maxBytes)
	if err != nil {
		return nil, 0, nil, err
	}
	return file, size, cleanup, nil
}

// spoolPackage copies r into one temporary file, refusing more than maxBytes. The returned handle
// is rewound for random access and stays open for the whole import; cleanup closes it and removes
// the file, and the caller must defer it on every path.
func spoolPackage(r io.Reader, maxBytes int64) (source *os.File, size int64, cleanup func(), err error) {
	file, err := os.CreateTemp("", "engram-package-*")
	if err != nil {
		return nil, 0, nil, &PackageError{Code: CodePackageBadFormat, Message: "cannot read archive", Entries: []string{err.Error()}}
	}
	cleanup = func() {
		file.Close()
		os.Remove(file.Name())
	}
	written, err := io.Copy(file, io.LimitReader(r, maxBytes+1))
	if err != nil {
		cleanup()
		return nil, 0, nil, &PackageError{Code: CodePackageBadFormat, Message: "cannot read archive", Entries: []string{err.Error()}}
	}
	if written > maxBytes {
		cleanup()
		return nil, 0, nil, &PackageError{Code: CodePackageTooLarge, Message: "archive exceeds the package size limit"}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, 0, nil, &PackageError{Code: CodePackageBadFormat, Message: "cannot read archive", Entries: []string{err.Error()}}
	}
	return file, written, cleanup, nil
}

// readArchiveBytes reads the whole archive into memory under the total-size cap. It remains for
// ReadPackageArchive, the in-memory path used when a caller already holds the package bytes.
// ParsePackageTargetRef parses the kind and the raw deck reference out of a target string (clients supply the public id);
// an empty string equals new_deck. The reference's validity is the caller's call -- only it knows whether the reference is a public id or an internal primary key.
func ParsePackageTargetRef(target string) (kind string, ref string, err error) {
	target = strings.TrimSpace(target)
	if target == "" || target == PackageTargetNewDeck {
		return PackageTargetNewDeck, "", nil
	}
	for _, prefix := range []string{"into_deck:", "replace_deck:"} {
		if strings.HasPrefix(target, prefix) {
			ref := strings.TrimSpace(strings.TrimPrefix(target, prefix))
			if ref == "" {
				return "", "", &PackageError{Code: CodePackageBadFormat, Message: "target deck id is empty"}
			}
			return strings.TrimSuffix(prefix, ":"), ref, nil
		}
	}
	return "", "", &PackageError{Code: CodePackageBadFormat, Message: "target must be new_deck, into_deck:<id> or replace_deck:<id>"}
}

// ParsePackageTarget parses the target string used inside the store (the deck reference is a numeric primary key); an empty string equals new_deck.
func ParsePackageTarget(target string) (kind string, deckID uint64, err error) {
	kind, ref, err := ParsePackageTargetRef(target)
	if err != nil || kind == PackageTargetNewDeck {
		return kind, 0, err
	}
	id, perr := strconv.ParseUint(ref, 10, 64)
	if perr != nil || id == 0 {
		return "", 0, &PackageError{Code: CodePackageBadFormat, Message: "target deck id must be a positive integer"}
	}
	return kind, id, nil
}

// FormatPackageTarget assembles the target string in the store's internal form; the transport layer calls it after resolving the public id into a numeric primary key.
func FormatPackageTarget(kind string, deckID uint64) string {
	if kind == "" || kind == PackageTargetNewDeck {
		return PackageTargetNewDeck
	}
	return kind + ":" + strconv.FormatUint(deckID, 10)
}

// deckPublicID fetches a deck's public id; the import report carries the public id rather than the numeric primary key.
func deckPublicID(ctx context.Context, tx *gorm.DB, deckID uint64) (string, error) {
	var pid string
	if err := tx.WithContext(ctx).Table("decks").Select("public_id").Where("id = ?", deckID).Scan(&pid).Error; err != nil {
		return "", fmt.Errorf("load deck public id: %w", err)
	}
	return pid, nil
}

// ImportPackage imports a deck package.
//
// The caller (API/Web/MCP/CLI) is responsible for the deck-level permission check before calling; this method only does package-level validation,
// de-duplication, id remapping, the progress ownership check and transactional writes.
func (s *DeckStore) ImportPackage(ctx context.Context, actorUserID uint64, r io.Reader, opts PackageImportOptions) (*PackageImportReport, error) {
	// The actor of this import: note write validation uses it to decide "is a newly introduced reference readable by the importer";
	// the media bytes the package provides are registered in media_uploaders before validation.
	ctx = WithActor(ctx, actorUserID)
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.OnConflict == "" {
		opts.OnConflict = "update"
	}
	if opts.OnConflict != "skip" && opts.OnConflict != "update" && opts.OnConflict != "fail" {
		return nil, &PackageError{Code: CodePackageBadFormat, Message: "on_conflict must be one of skip, update, fail"}
	}
	targetKind, targetDeckID, err := ParsePackageTarget(opts.Target)
	if err != nil {
		return nil, err
	}
	if err := s.ValidateImportPreset(ctx, actorUserID, targetKind, &opts); err != nil {
		return nil, err
	}

	importLimits := opts.Limits.effective()
	source, size, cleanup, err := packageSource(r, importLimits.MaxPackageBytes)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	return s.importAnkiPackage(ctx, actorUserID, source, size, importLimits, opts, targetKind, targetDeckID)
}

// ValidateImportPreset validates the selection before import or ticket issuance.
// For new_deck, it writes new into opts.PresetMode when the mode is empty, because
// selectImportPreset calls the create function only for new and otherwise looks up PresetID.
// For into_deck and replace_deck, only an empty mode or new is accepted, and PresetID must be zero.
func (s *DeckStore) ValidateImportPreset(ctx context.Context, owner uint64, targetKind string, opts *PackageImportOptions) error {
	if targetKind != PackageTargetNewDeck && opts.PresetID != 0 {
		return &PackageError{Code: CodePresetSelectionInvalid, Message: "invalid preset selection"}
	}
	if opts.PresetMode == "" {
		if targetKind != PackageTargetNewDeck {
			return nil
		}
		opts.PresetMode = PackagePresetNew
	}
	switch opts.PresetMode {
	case PackagePresetNew:
		if opts.PresetID != 0 {
			return &PackageError{Code: CodePresetSelectionInvalid, Message: "invalid preset selection"}
		}
		return nil
	case PackagePresetReuse, PackagePresetCopy:
	default:
		return &PackageError{Code: CodePresetSelectionInvalid, Message: "invalid preset selection"}
	}
	// Reuse and copy need the caller's own preset, a new deck to attach it to, and no package
	// weights: writing those into a preset would change every deck that already points at it.
	if targetKind != PackageTargetNewDeck || opts.PresetID == 0 || opts.ApplyWeights {
		return &PackageError{Code: CodePresetSelectionInvalid, Message: "invalid preset selection"}
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&Preset{}).Where("id = ? AND owner_user_id = ?", opts.PresetID, owner).Count(&count).Error; err != nil {
		return fmt.Errorf("count import presets: %w", err)
	}
	if count != 1 {
		return &PackageError{Code: CodePresetSelectionInvalid, Message: "invalid preset selection"}
	}
	return nil
}

// mediaWrite is one media file newly written by this import (sha + absolute path).
type mediaWrite struct {
	sha string
	abs string
}

// cleanupMediaWrites deletes media files this import wrote to disk but which, because the transaction rolled back, have no metadata referencing them.
//
// It only deletes files this process wrote this time; if a (concurrent) other committed transaction already references the sha's metadata row,
// the file is kept -- it is no longer an orphan. A failed delete is simply ignored (a file already gone or a permission problem does not affect correctness,
// the worst case is falling back to the behaviour from before the fix).
func (s *DeckStore) cleanupMediaWrites(ctx context.Context, writes []mediaWrite) {
	if len(writes) == 0 {
		return
	}
	store := NewMediaStore(s.db)
	for _, w := range writes {
		if row, err := store.BySha256(ctx, w.sha); err == nil && row != nil {
			continue
		}
		if err := os.Remove(w.abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = err
		}
	}
}

// packageModel is the parsed package content.
func checkMediaQuota(ctx context.Context, db *gorm.DB, actorUserID uint64, sizeBySha map[string]int64, quota int64) error {
	if quota <= 0 {
		return nil
	}
	usage, err := UserMediaUsage(ctx, db, actorUserID)
	if err != nil {
		return fmt.Errorf("import package: media usage: %w", err)
	}
	var extra int64
	for sha, size := range sizeBySha {
		if usage.Sha256[sha] {
			continue
		}
		extra += size
	}
	if usage.Bytes+extra <= quota {
		return nil
	}
	return &PackageError{Code: CodePackageQuotaExceeded, Message: "package media would exceed this user's media quota"}
}

// importInTx performs all the writes in one transaction; under dry_run the caller rolls it back with ErrPackageDryRun.
func selectImportPreset(ctx context.Context, tx *gorm.DB, owner uint64, opts PackageImportOptions, create func() (Preset, error)) (Preset, error) {
	if opts.PresetMode == PackagePresetNew {
		return create()
	}
	var p Preset
	if err := tx.WithContext(ctx).Where("id = ? AND owner_user_id = ?", opts.PresetID, owner).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Preset{}, &PackageError{Code: CodePresetSelectionInvalid, Message: "invalid preset selection"}
		}
		return Preset{}, fmt.Errorf("look up import preset: %w", err)
	}
	if opts.PresetMode == PackagePresetReuse {
		return p, nil
	}
	name, err := uniqueOwnerName(ctx, tx, &Preset{}, owner, p.Name+" (copy)")
	if err != nil {
		return Preset{}, fmt.Errorf("choose import preset copy name: %w", err)
	}
	p.ID, p.PublicID, p.Name = 0, "", name
	p.CreatedAt = opts.Now().UTC()
	p.UpdatedAt = p.CreatedAt
	if err := tx.WithContext(ctx).Create(&p).Error; err != nil {
		return Preset{}, fmt.Errorf("create import preset copy: %w", err)
	}
	return p, nil
}

// uniqueDeckName appends a (2), (3)... suffix on a name clash.
func uniqueDeckName(ctx context.Context, tx *gorm.DB, owner uint64, name string) (string, error) {
	unique, err := uniqueOwnerName(ctx, tx, &Deck{}, owner, name)
	if err != nil {
		return "", fmt.Errorf("list deck names: %w", err)
	}
	return unique, nil
}

// uniqueOwnerName returns name itself when no row of model owned by owner uses it, otherwise the first free "name (N)" candidate with N >= 2.
// Deck and preset names share this rule, so a name that avoids a clash does so in the same way for both.
func uniqueOwnerName(ctx context.Context, tx *gorm.DB, model any, owner uint64, name string) (string, error) {
	var names []string
	if err := tx.WithContext(ctx).Model(model).Where("owner_user_id = ?", owner).
		Pluck("name", &names).Error; err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, n := range names {
		taken[n] = true
	}
	if !taken[name] {
		return name, nil
	}
	return nextFreeSuffixedName(name, taken), nil
}

// nextFreeSuffixedName returns the first "base (N)" candidate with N starting at 2 that taken does not hold.
// It never returns base itself: callers use it when base is already taken or must not be written as the final name.
func nextFreeSuffixedName(base string, taken map[string]bool) string {
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// createPresetFromPackage creates a new preset owned by actorUserID from the package's parameters.
//
// The package's preset name is only a hint, never the stored name: the exporter writes its own preset name verbatim, so a deck on the default
// preset exports the literal "Default". Copying that name would create a second preset called "Default" in the importer's account, where the name
// is the default preset's identity -- the duplicate would be presented as a default preset and could not be deleted. An absent, blank or
// reserved name therefore becomes "Imported — <deck name>", and the result is made unique among the owner's presets like any other name.
//
// The two weight metadata fields are written only when valid weights are applied. Otherwise,
// the preset would show an optimization record without the weights that it describes.
func createPresetFromPackage(ctx context.Context, tx *gorm.DB, owner uint64, pp *PackagePreset, deckName string, applyWeights bool, report *PackageImportReport) (Preset, error) {
	now := time.Now().UTC()
	base := strings.TrimSpace(pp.Name)
	if base == "" || base == DefaultPresetName {
		importedFrom := strings.TrimSpace(deckName)
		if importedFrom == "" {
			// resolveTargetDeck always passes a non-empty deck name; this keeps the fallback self-contained rather than relying on the caller.
			importedFrom = "Imported"
		}
		base = "Imported — " + importedFrom
	}
	// base is never DefaultPresetName (the rule above replaces it) and the suffix candidates extend base, so the final name cannot be that literal either.
	name, err := uniqueOwnerName(ctx, tx, &Preset{}, owner, base)
	if err != nil {
		return Preset{}, fmt.Errorf("list preset names: %w", err)
	}
	p := Preset{
		OwnerUserID: owner, Name: name, DesiredRetention: pp.DesiredRetention,
		LearningSteps: pp.LearningSteps, RelearningSteps: pp.RelearningSteps,
		MaximumIntervalDays: pp.MaximumIntervalDays, EnableFuzz: Ptr(pp.EnableFuzz),
		CreatedAt: now, UpdatedAt: now,
	}
	if p.DesiredRetention <= 0 || p.DesiredRetention > 1 {
		p.DesiredRetention = DefaultDesiredRetention
	}
	if p.MaximumIntervalDays <= 0 {
		p.MaximumIntervalDays = DefaultMaximumIntervalDays
	}
	p.MaximumIntervalDays = min(p.MaximumIntervalDays, DefaultMaximumIntervalDays)
	if !validPackageSteps(p.LearningSteps) {
		p.LearningSteps = DefaultLearningSteps
	}
	if !validPackageSteps(p.RelearningSteps) {
		p.RelearningSteps = DefaultRelearningSteps
	}
	p.WeightsJSON = importPresetWeights(owner, pp.Weights, applyWeights, report)
	if p.WeightsJSON != nil {
		p.WeightsReviewCount = pp.WeightsReviewCount
		if pp.WeightsOptimizedAt != nil {
			if t, err := time.Parse(time.RFC3339, *pp.WeightsOptimizedAt); err == nil {
				p.WeightsOptimizedAt = &t
			}
		}
	}
	if err := tx.WithContext(ctx).Create(&p).Error; err != nil {
		return Preset{}, fmt.Errorf("import package: create preset: %w", err)
	}
	if p.WeightsJSON != nil {
		report.WeightsApplied = true
	}
	return p, nil
}

func validPackageSteps(steps string) bool {
	for _, part := range strings.Split(steps, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		minutes, ok := ankiStepMinutes(part)
		if !ok || minutes < 0 || math.IsNaN(minutes) || math.IsInf(minutes, 0) {
			return false
		}
	}
	return true
}

// existingNoteIndex builds an index of the target deck's existing notes: external_ref -> note, fingerprint -> note.
func errorEntries(errs []PackageImportError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Entry+": "+e.Reason)
	}
	return out
}

// asPackageMediaForbidden translates the *MediaWriteError from a note write (a pre-write validation rejection) into
// *PackageError, so consumers of the package import (REST/MCP/Web/CLI) map the stable code and
// per-entry reasons with their existing PackageError branch; other errors are returned unchanged (keeping existing semantics).
func asPackageMediaForbidden(err error) error {
	var mwe *MediaWriteError
	if errors.As(err, &mwe) {
		return &PackageError{
			Code:    CodePackageMediaForbidden,
			Message: "the package references media you cannot read",
			Entries: mwe.Entries,
		}
	}
	return err
}
