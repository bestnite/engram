package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.nite07.com/nite/engram/internal/mediatype"
	"git.nite07.com/nite/engram/internal/notetype"
)

// Deck package import.
//
// Safety is the focus: reject path traversal and symlinks before extraction, cap the total extracted size and the entry count (zip bomb defence);
// all business writes happen in one transaction (a failure leaves nothing half done); repeated imports de-duplicate by external_ref or content fingerprint.

// Three kinds of import target.
const (
	// PackageTargetNewDeck creates a new deck under the package's deck name (a suffix is added on a name clash).
	PackageTargetNewDeck = "new_deck"
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
	// Target takes new_deck (the default), into_deck:<id>, replace_deck:<id>.
	Target string
	DryRun bool
	// OnConflict takes skip / update (the default) / fail (roll back the whole package).
	OnConflict string
	// AllowOthersProgress is an admin switch (off by default): allow someone else's progress in the package to be imported into one's own account.
	AllowOthersProgress bool
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
	Target string `json:"target"`
	DryRun bool   `json:"dry_run"`
	// DeckID is the internal numeric primary key, for internal uses such as audit; what is exposed externally is DeckPublicID.
	DeckID            uint64               `json:"-"`
	DeckPublicID      string               `json:"deck_id,omitempty"`
	NotesCreated      int                  `json:"notes_created"`
	NotesUpdated      int                  `json:"notes_updated"`
	NotesSkipped      int                  `json:"notes_skipped"`
	CardsCreated      int                  `json:"cards_created"`
	MediaNew          int                  `json:"media_new"`
	MediaMissing      int                  `json:"media_missing"`
	ProgressApplied   int                  `json:"progress_applied"`
	ProgressSkipped   int                  `json:"progress_skipped"`
	ProgressDiscarded bool                 `json:"progress_discarded"`
	MatchRule         string               `json:"match_rule,omitempty"`
	Errors            []PackageImportError `json:"errors"`
	// WeightsApplied means the package's weights were written into the new preset.
	WeightsApplied bool `json:"weights_applied"`
	// WeightsDiscarded means the package carried weights that were not adopted: ApplyWeights was off, or the target was not new_deck.
	WeightsDiscarded bool `json:"weights_discarded"`
}

// ErrPackageDryRun is the internal dry_run sentinel: once planning is done it rolls the transaction back, leaving no writes.
var ErrPackageDryRun = errors.New("deck package: dry run rollback")

// ReadPackageArchive reads a zip safely: it rejects path traversal, absolute paths and symlinks,
// and caps the entry count and the total extracted size ("safety").
func ReadPackageArchive(r io.Reader, limits PackageLimits) (map[string][]byte, error) {
	// Read it all into memory first (with a total cap), because zip.NewReader needs a ReaderAt.
	raw, err := io.ReadAll(io.LimitReader(r, limits.MaxTotalBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read archive: %v", &PackageError{Code: CodePackageBadFormat, Message: "cannot read archive"}, err)
	}
	if int64(len(raw)) > limits.MaxTotalBytes {
		return nil, &PackageError{Code: CodePackageTooLarge, Message: "archive exceeds the total size limit"}
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, &PackageError{Code: CodePackageBadFormat, Message: "not a valid zip archive"}
	}
	if len(zr.File) > limits.MaxEntries {
		return nil, &PackageError{Code: CodePackageTooLarge, Message: fmt.Sprintf("archive has %d entries, limit is %d", len(zr.File), limits.MaxEntries)}
	}
	out := make(map[string][]byte, len(zr.File))
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if strings.HasSuffix(name, "/") || f.FileInfo().IsDir() {
			continue
		}
		// A duplicate entry name (repeated path) must be rejected. zip allows the same name several times, and here entries are stored into a
		// map by name -- without this check the later entry silently overwrites the earlier one, letting a package hide an overlay layer that disagrees with the index.
		if _, dup := out[name]; dup {
			return nil, &PackageError{Code: CodePackageBadFormat, Message: "archive contains a duplicate entry", Entries: []string{name}}
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return nil, &PackageError{Code: CodePackageUnsafeEntry, Message: "symlink entries are not allowed", Entries: []string{name}}
		}
		if unsafeZipName(name) {
			return nil, &PackageError{Code: CodePackageUnsafeEntry, Message: "unsafe entry path", Entries: []string{name}}
		}
		buf, err := readZipEntry(f, limits.MaxFileBytes)
		if err != nil {
			if errors.Is(err, ErrPackageTooLargeSentinel) {
				return nil, &PackageError{Code: CodePackageTooLarge, Message: "entry exceeds the size limit", Entries: []string{name}}
			}
			return nil, &PackageError{Code: CodePackageBadFormat, Message: "cannot read entry", Entries: []string{name}}
		}
		total += int64(len(buf))
		if total > limits.MaxTotalBytes {
			return nil, &PackageError{Code: CodePackageTooLarge, Message: "archive exceeds the total decompressed size limit"}
		}
		out[name] = buf
	}
	return out, nil
}

// unsafeZipName reports whether a zip entry name is dangerous: an absolute path, a backslash, a drive letter, or a ".." segment.
func unsafeZipName(name string) bool {
	if name == "" {
		return true
	}
	if strings.Contains(name, "\\") {
		return true
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) || path.IsAbs(name) {
		return true
	}
	if len(name) >= 2 && name[1] == ':' {
		return true
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

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

	entries, err := ReadPackageArchive(r, DefaultPackageLimits())
	if err != nil {
		return nil, err
	}
	pkg, err := parsePackage(entries)
	if err != nil {
		return nil, err
	}

	// Every media class is accepted: the package's bytes are classified when written, and nothing here needs to reject a
	// file before the transaction. The quota check below is the only pre-write gate on media.
	// The media bytes the import adds count against the **importer's** quota and exceeding it fails the whole package. This check likewise happens before the transaction and before any
	// disk write, so a rejection leaves no deck/card/media rows in the database and no bytes on disk.
	if err := checkImportMediaQuota(ctx, s.db, actorUserID, pkg, opts.MediaQuotaBytes); err != nil {
		return nil, err
	}

	username, _ := s.lookupUsername(ctx, actorUserID)

	report := &PackageImportReport{Target: targetKind, DryRun: opts.DryRun, Errors: []PackageImportError{}}
	mediaMissing := missingMediaRefs(pkg)
	if len(mediaMissing) > 0 && !opts.SkipMissingMedia && !opts.DryRun {
		return nil, &PackageError{Code: CodePackageBadFormat, Message: "package references media that is not included", Entries: mediaMissing}
	}
	report.MediaMissing = len(mediaMissing)

	// writtenMedia records the media files this transaction actually wrote to disk; a failed rollback uses it to clean them up,
	// so the metadata rows do not roll back while the bytes stay on disk as orphan files.
	var writtenMedia []mediaWrite
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.importInTx(ctx, tx, actorUserID, username, pkg, targetKind, targetDeckID, opts, report, &writtenMedia)
	})
	if err != nil {
		s.cleanupMediaWrites(ctx, writtenMedia)
	}
	if err != nil && !errors.Is(err, ErrPackageDryRun) {
		return nil, err
	}
	if err == nil && opts.DryRun {
		return nil, ErrPackageDryRun
	}
	return report, nil
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
type packageModel struct {
	Manifest  PackageManifest
	NoteTypes []PackageNoteType
	Notes     []PackageNote
	Cards     []PackageCard
	Preset    PackagePreset
	Progress  *PackageProgress
	Media     map[string]PackageMediaEntry
	MediaRaw  map[string][]byte
}

func parseJSONEntry(entries map[string][]byte, name string, out any) error {
	raw, ok := entries[name]
	if !ok {
		return &PackageError{Code: CodePackageBadFormat, Message: "missing required entry", Entries: []string{name}}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &PackageError{Code: CodePackageBadFormat, Message: "invalid JSON", Entries: []string{name + ": " + err.Error()}}
	}
	return nil
}

func parsePackage(entries map[string][]byte) (*packageModel, error) {
	pkg := &packageModel{Media: map[string]PackageMediaEntry{}, MediaRaw: map[string][]byte{}}
	// The raw bytes of manifest.json must be valid UTF-8: encoding/json silently replaces invalid bytes with
	// U+FFFD (per the docs "not an error"), so looking only at the decoded string never catches it; the check has to be on the raw bytes.
	if raw, ok := entries["manifest.json"]; ok && !utf8.Valid(raw) {
		return nil, &PackageError{Code: CodePackageBadFormat, Message: "invalid UTF-8 in manifest", Entries: []string{"manifest.json: invalid UTF-8"}}
	}
	if err := parseJSONEntry(entries, "manifest.json", &pkg.Manifest); err != nil {
		return nil, err
	}
	// This release reads exactly one format. The message names the way out: an older release can still read the
	// package, and exporting it again from there produces the current format (schema/README.md records this one-time
	// exception to the N-1 compatibility rule).
	if pkg.Manifest.FormatVersion != PackageFormatVersion {
		return nil, &PackageError{
			Code: CodePackageFormatObsolete,
			Message: fmt.Sprintf(
				"format_version %d is not supported; import this package with an older engram release and export it again, then import the new package",
				pkg.Manifest.FormatVersion),
		}
	}
	// The length and character bounds of deck name/description: validated right after the manifest is parsed, rejecting the whole package before the transaction starts.
	if err := packageDeckMetaError(deckMetaErrors(pkg.Manifest.Deck.Name, pkg.Manifest.Deck.Description)); err != nil {
		return nil, err
	}
	if err := parseJSONEntry(entries, "note_types.json", &pkg.NoteTypes); err != nil {
		return nil, err
	}
	if err := validatePackageNoteTypes(pkg.NoteTypes); err != nil {
		return nil, err
	}
	if err := parseJSONEntry(entries, "notes.json", &pkg.Notes); err != nil {
		return nil, err
	}
	// Every note must name a note type the package declares. An unknown reference must be listed entry by entry:
	// silently dropping the note would make the user think the import succeeded while cards are missing.
	var unknown []string
	for i := range pkg.Notes {
		if !pkg.hasNoteType(pkg.Notes[i].NoteType) {
			unknown = append(unknown, fmt.Sprintf("notes[%d] note_type=%s", i, pkg.Notes[i].NoteType))
		}
	}
	if len(unknown) > 0 {
		return nil, &PackageError{Code: CodePackageUnknownKind, Message: "unknown note type", Entries: unknown}
	}
	if err := parseJSONEntry(entries, "cards.json", &pkg.Cards); err != nil {
		return nil, err
	}
	if err := parseJSONEntry(entries, "preset.json", &pkg.Preset); err != nil {
		return nil, err
	}
	if raw, ok := entries["progress.json"]; ok {
		var p PackageProgress
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &PackageError{Code: CodePackageBadFormat, Message: "invalid JSON", Entries: []string{"progress.json: " + err.Error()}}
		}
		pkg.Progress = &p
	}
	if raw, ok := entries["media.json"]; ok {
		if err := json.Unmarshal(raw, &pkg.Media); err != nil {
			return nil, &PackageError{Code: CodePackageBadFormat, Message: "invalid JSON", Entries: []string{"media.json: " + err.Error()}}
		}
		for sha, entry := range pkg.Media {
			// Only entries with the media/ prefix inside the package whose file name matches the sha256 are accepted, blocking path traversal from media.json.
			if unsafeZipName(entry.Path) || !strings.HasPrefix(entry.Path, "media/") {
				return nil, &PackageError{Code: CodePackageUnsafeEntry, Message: "media path is unsafe", Entries: []string{entry.Path}}
			}
			// The file name must be `<sha256>.<ext>`, otherwise the declared sha and the entry name disagree (index decoupled from content).
			base := path.Base(entry.Path)
			if !strings.HasPrefix(base, sha+".") || len(base) <= len(sha)+1 {
				return nil, &PackageError{Code: CodePackageBadFormat, Message: "media entry name does not match the declared sha256", Entries: []string{entry.Path}}
			}
			raw, ok := entries[entry.Path]
			if !ok {
				continue
			}
			// The declared sha256 must equal the real sha256 of the bytes, blocking a "fake sha" (declaration not matching content).
			// This check completes before any disk write or database write, so a rejected package has zero side effects.
			sum := sha256.Sum256(raw)
			if hex.EncodeToString(sum[:]) != sha {
				return nil, &PackageError{Code: CodePackageBadFormat, Message: "media bytes do not match the declared sha256", Entries: []string{entry.Path}}
			}
			pkg.MediaRaw[sha] = raw
		}
	}
	return pkg, nil
}

// hasNoteType reports whether note_types.json declares ref.
func (pkg *packageModel) hasNoteType(ref string) bool {
	for i := range pkg.NoteTypes {
		if pkg.NoteTypes[i].Ref == ref {
			return true
		}
	}
	return false
}

// validatePackageNoteTypes rejects a note_types.json whose references are empty or not unique, and whose names or
// configs the note type rules reject. All of it is checked before the transaction opens, so a malformed package has no
// side effects. The config is decoded by the JSON parser into notetype.Config; Validate and validateAnswerSpecs are the
// same rules a note type created through the API runs.
func validatePackageNoteTypes(types []PackageNoteType) error {
	seen := make(map[string]bool, len(types))
	for i := range types {
		ref := strings.TrimSpace(types[i].Ref)
		if ref == "" {
			return &PackageError{Code: CodePackageBadFormat, Message: "note type reference is empty", Entries: []string{fmt.Sprintf("note_types[%d]", i)}}
		}
		if seen[ref] {
			return &PackageError{Code: CodePackageBadFormat, Message: "duplicate note type reference", Entries: []string{fmt.Sprintf("note_types[%d] ref=%s", i, ref)}}
		}
		seen[ref] = true
		if err := notetype.ValidateName(types[i].Name); err != nil {
			return &PackageError{Code: CodePackageBadFormat, Message: "invalid note type name", Entries: []string{fmt.Sprintf("note_types[%d]: %v", i, err)}}
		}
		if err := types[i].Config.Validate(); err != nil {
			return &PackageError{Code: CodePackageBadFormat, Message: "invalid note type config", Entries: []string{fmt.Sprintf("note_types[%d]: %v", i, err)}}
		}
		// The answer-spec rule runs here as well as in SetConfig, so a package that violates it is rejected before
		// the transaction opens and the report names the offending entry. Both calls reach the same implementation.
		if err := validateAnswerSpecs(types[i].Config); err != nil {
			return &PackageError{Code: CodePackageBadFormat, Message: "invalid note type answer spec", Entries: []string{fmt.Sprintf("note_types[%d]: %v", i, err)}}
		}
	}
	return nil
}

// noteTypeKey identifies a note type by the pair that defines its content: its name and its config.
// The importer reuses a target-deck note type with the same key instead of adding a duplicate on every import.
func noteTypeKey(name string, cfg notetype.Config) (string, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("encode note type config: %w", err)
	}
	return name + "\n" + string(raw), nil
}

// resolvePackageNoteTypes maps every package-local reference to a note type row of the target deck.
//
// A type the deck already holds with the same name and config is reused; any other type is created. Reuse is what
// keeps re-importing a package (and importing into a deck that already holds the same type) from piling up duplicates.
func resolvePackageNoteTypes(ctx context.Context, tx *gorm.DB, deckID uint64, types []PackageNoteType) (map[string]uint64, error) {
	existing, err := NewNoteTypeStore(tx).ListByDeck(ctx, deckID)
	if err != nil {
		return nil, fmt.Errorf("list note types of deck %d: %w", deckID, err)
	}
	byKey := make(map[string]uint64, len(existing))
	for i := range existing {
		cfg, err := existing[i].Config()
		if err != nil {
			continue
		}
		key, err := noteTypeKey(existing[i].Name, cfg)
		if err != nil {
			continue
		}
		byKey[key] = existing[i].ID
	}
	out := make(map[string]uint64, len(types))
	now := time.Now().UTC()
	for i := range types {
		entry := &types[i]
		key, err := noteTypeKey(entry.Name, entry.Config)
		if err != nil {
			return nil, err
		}
		if id, ok := byKey[key]; ok {
			out[entry.Ref] = id
			continue
		}
		nt := NoteType{DeckID: deckID}
		if err := nt.SetConfig(entry.Name, entry.Config); err != nil {
			return nil, fmt.Errorf("import note type %q: %w", entry.Name, err)
		}
		nt.CreatedAt = now
		nt.UpdatedAt = now
		if err := tx.WithContext(ctx).Create(&nt).Error; err != nil {
			return nil, fmt.Errorf("create note type %q: %w", entry.Name, err)
		}
		// A newly created type records its `_`-prefixed media references; the reuse path leaves the existing set alone,
		// because the package config and the existing config are identical there.
		if err := NewMediaNoteTypeStore(tx).Replace(ctx, nt.ID, noteTypeMediaNames(entry.Config)); err != nil {
			return nil, fmt.Errorf("record media of note type %q: %w", entry.Name, err)
		}
		byKey[key] = nt.ID
		out[entry.Ref] = nt.ID
	}
	return out, nil
}

// noteFromPackage builds the note row from a package entry: the note type row, the tag list and the two optional
// content columns. source_url and extra are columns, not fields, unless the note type declares fields of those names;
// the note write path applies that rule.
func noteFromPackage(pn *PackageNote, deckID, noteTypeID uint64) Note {
	n := Note{DeckID: deckID, NoteTypeID: Ptr(noteTypeID), TagsJSON: TagsJSON(pn.Tags)}
	if pn.ExternalRef != "" {
		n.ExternalRef = Ptr(pn.ExternalRef)
	}
	if pn.SourceURL != "" {
		n.SourceURL = Ptr(pn.SourceURL)
	}
	if pn.Extra != nil {
		if raw, err := json.Marshal(pn.Extra); err == nil && string(raw) != "null" {
			n.ExtraJSON = Ptr(string(raw))
		}
	}
	return n
}

// stringFieldsToAny converts the package's field map into the shape the note write path accepts.
// A string is already the stored HTML value, so no conversion happens beyond the map type.
func stringFieldsToAny(fields map[string]string) map[string]any {
	out := make(map[string]any, len(fields))
	for id, html := range fields {
		out[id] = html
	}
	return out
}

// registerPackageMediaNames records every deck-scoped name the package declares for a sha, before the note writes
// resolve those names. An entry without names registers its package file name, so a hand-written package still works.
func registerPackageMediaNames(ctx context.Context, tx *gorm.DB, deckID uint64, pkg *packageModel) error {
	for sha, entry := range pkg.Media {
		names := entry.Names
		if len(names) == 0 {
			names = []string{path.Base(entry.Path)}
		}
		for _, name := range names {
			name = normaliseMediaName(name)
			if name == "" {
				continue
			}
			row := MediaName{DeckID: deckID, Name: name, Sha256: sha}
			if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "deck_id"}, {Name: "name"}},
				DoUpdates: clause.AssignmentColumns([]string{"sha256"}),
			}).Create(&row).Error; err != nil {
				return fmt.Errorf("register media name %q: %w", name, err)
			}
		}
	}
	return nil
}

// packageMediaNameIndex maps every deck-scoped media name the package declares to its sha256.
// The package file name is also accepted, so a hand-written package without names still resolves.
func packageMediaNameIndex(pkg *packageModel) map[string]string {
	idx := make(map[string]string, len(pkg.Media))
	for sha, entry := range pkg.Media {
		idx[normaliseMediaName(path.Base(entry.Path))] = sha
		for _, name := range entry.Names {
			idx[normaliseMediaName(name)] = sha
		}
	}
	return idx
}

// packageMediaRefs resolves every media reference in a note's HTML against the package's own name index.
func packageMediaRefs(idx map[string]string, values map[string]string) map[string]bool {
	refs := map[string]bool{}
	for _, html := range values {
		for _, name := range mediaNamesInHTML(html) {
			if match := mediaShaNameRE.FindStringSubmatch(name); match != nil {
				refs[match[1]] = true
				continue
			}
			if sha, ok := idx[name]; ok {
				refs[sha] = true
			}
		}
	}
	return refs
}

// missingMediaRefs finds media referenced in note fields but without bytes in the package.
func missingMediaRefs(pkg *packageModel) []string {
	idx := packageMediaNameIndex(pkg)
	refs := map[string]bool{}
	for i := range pkg.Notes {
		for sha := range packageMediaRefs(idx, pkg.Notes[i].Fields) {
			refs[sha] = true
		}
	}
	var missing []string
	for sha := range refs {
		if _, ok := pkg.MediaRaw[sha]; !ok {
			missing = append(missing, "media/"+sha)
		}
	}
	sort.Strings(missing)
	return missing
}

// headBytes returns the head of the file, at most mediatype.HeadBytes; it gives as much as there is up to the length detection needs.
func headBytes(raw []byte) []byte {
	if len(raw) > mediatype.HeadBytes {
		return raw[:mediatype.HeadBytes]
	}
	return raw
}

// checkImportMediaQuota checks, before any file or database row is written, whether the media added by the import exceeds the importer's quota.
//
// The accounting matches the upload chain (internal/store.UserMediaUsage):
//   - used = media referenced by that user's notes ∪ that user's uploads nobody references yet, summed with sha256 de-duplication;
//   - added this time = the bytes of the package media whose sha the user is **not yet charged** for (a blob already charged is not charged again,
//     the same rule as the upload side's "added amount for an existing sha is 0").
//
// quota<=0 means unlimited and passes straight through. Exceeding it returns CodePackageQuotaExceeded and the caller returns before the transaction,
// so no deck/card/media row or disk file is left behind.
func checkImportMediaQuota(ctx context.Context, db *gorm.DB, actorUserID uint64, pkg *packageModel, quota int64) error {
	if quota <= 0 {
		return nil
	}
	usage, err := UserMediaUsage(ctx, db, actorUserID)
	if err != nil {
		return fmt.Errorf("import package: media usage: %w", err)
	}
	var extra int64
	for sha, raw := range pkg.MediaRaw {
		if usage.Sha256[sha] {
			continue
		}
		extra += int64(len(raw))
	}
	if usage.Bytes+extra <= quota {
		return nil
	}
	return &PackageError{Code: CodePackageQuotaExceeded, Message: "package media would exceed this user's media quota"}
}

// importInTx performs all the writes in one transaction; under dry_run the caller rolls it back with ErrPackageDryRun.
func (s *DeckStore) importInTx(ctx context.Context, tx *gorm.DB, actorUserID uint64, username string, pkg *packageModel, targetKind string, targetDeckID uint64, opts PackageImportOptions, report *PackageImportReport, writtenMedia *[]mediaWrite) error {
	// The package's media bytes count as "provided by the importer this time": they are registered in media_uploaders before the note writes'
	// pre-write validation runs (register the shas provided this time in media_uploaders first, then validate). Otherwise
	// "importing the bytes you just provided" would be misjudged by pre-write validation as an unauthorized reference. dry_run writes no row.
	if !opts.DryRun && opts.MediaRoot != "" {
		for sha := range pkg.MediaRaw {
			if err := RecordMediaUploader(ctx, tx, sha, actorUserID); err != nil {
				return fmt.Errorf("import package: record media uploader: %w", err)
			}
		}
	}
	deckID, err := s.resolveTargetDeck(ctx, tx, actorUserID, pkg, targetKind, targetDeckID, opts)
	if err != nil {
		return err
	}
	report.DeckID = deckID
	if len(pkg.Preset.Weights) > 0 {
		report.WeightsApplied = targetKind == PackageTargetNewDeck && opts.ApplyWeights
		report.WeightsDiscarded = !report.WeightsApplied
	}
	pid, err := deckPublicID(ctx, tx, deckID)
	if err != nil {
		return err
	}
	report.DeckPublicID = pid

	// replace_deck is destructive: it first soft-deletes every existing note of the target deck (progress stays on the cards, no rows are cascade-deleted).
	if targetKind == "replace_deck" {
		if err := tx.WithContext(ctx).Where("deck_id = ?", deckID).Delete(&Note{}).Error; err != nil {
			return fmt.Errorf("replace deck: clear notes: %w", err)
		}
	}

	// Anki's clash rule: a package name whose bytes differ from an existing name in the target deck is renamed with a
	// SHA-1 suffix, and this package's references are rewritten before note types and notes are written, so both resolve
	// the renamed name. Without a media root no bytes are imported, so no name is registered and no rename is needed.
	if opts.MediaRoot != "" {
		if err := renamePackageMedia(ctx, tx, deckID, pkg); err != nil {
			return err
		}
	}

	// The package's note types become rows of the target deck (or reuse an identical existing row), and every note
	// refers to them through the package-local reference from here on.
	noteTypeIDs, err := resolvePackageNoteTypes(ctx, tx, deckID, pkg.NoteTypes)
	if err != nil {
		return err
	}
	typeKeyByRef := make(map[string]string, len(pkg.NoteTypes))
	for i := range pkg.NoteTypes {
		key, err := noteTypeKey(pkg.NoteTypes[i].Name, pkg.NoteTypes[i].Config)
		if err != nil {
			return err
		}
		typeKeyByRef[pkg.NoteTypes[i].Ref] = key
	}
	// Media names must exist before the note writes: each write rebuilds its media_notes mapping by resolving
	// the names in its fields, so an unregistered name would silently contribute no reference.
	// Without a media root the import stores no bytes, so it registers no name either.
	if opts.MediaRoot != "" {
		if err := registerPackageMediaNames(ctx, tx, deckID, pkg); err != nil {
			return err
		}
	}

	// The existing content index: external_ref first, then the content fingerprint (the de-duplication rule).
	byRef, byFingerprint, err := s.existingNoteIndex(ctx, tx, deckID)
	if err != nil {
		return err
	}
	if len(byRef) > 0 {
		report.MatchRule = "external_ref"
	} else {
		report.MatchRule = "content_fingerprint"
	}

	notesStore := NewNoteStore(s.db)
	noteIDByIndex := make([]uint64, len(pkg.Notes))
	createdNote := make([]bool, len(pkg.Notes))
	// touchedNote marks notes this import wrote (create or update); a skipped note keeps its cards unchanged,
	// including the deck path, because skip means "leave the existing content alone".
	touchedNote := make([]bool, len(pkg.Notes))

	for i := range pkg.Notes {
		pn := &pkg.Notes[i]
		noteTypeID, ok := noteTypeIDs[pn.NoteType]
		if !ok {
			// parsePackage rejects this already; keep the guard so a future caller cannot insert a note without a type.
			return &PackageError{Code: CodePackageUnknownKind, Message: "unknown note type", Entries: []string{fmt.Sprintf("notes[%d] note_type=%s", i, pn.NoteType)}}
		}
		typeKey := typeKeyByRef[pn.NoteType]
		fp := fingerprintFromFields(typeKey, pn.Fields)
		var existing *Note
		if pn.ExternalRef != "" {
			existing = byRef[pn.ExternalRef]
		}
		if existing == nil {
			existing = byFingerprint[fp]
		}
		fields := stringFieldsToAny(pn.Fields)

		if existing != nil {
			switch opts.OnConflict {
			case "skip":
				report.NotesSkipped++
				noteIDByIndex[i] = existing.ID
				continue
			case "fail":
				report.Errors = append(report.Errors, PackageImportError{fmt.Sprintf("notes[%d]", i), "a matching note already exists"})
				continue
			}
			// update: keep the card rows and everyone's progress, change the content only (NoteStore.SaveInTx keeps the ids unchanged).
			n := noteFromPackage(pn, deckID, noteTypeID)
			n.ID = existing.ID
			if _, err := notesStore.SaveInTx(ctx, tx, &n, fields); err != nil {
				return fmt.Errorf("import note %d: %w", i, asPackageMediaForbidden(err))
			}
			report.NotesUpdated++
			noteIDByIndex[i] = existing.ID
			touchedNote[i] = true
			continue
		}

		n := noteFromPackage(pn, deckID, noteTypeID)
		n.CreatedBy = Ptr(actorUserID)
		n.Source = Ptr("import")
		cards, err := notesStore.SaveInTx(ctx, tx, &n, fields)
		if err != nil {
			return fmt.Errorf("import note %d: %w", i, asPackageMediaForbidden(err))
		}
		report.NotesCreated++
		report.CardsCreated += len(cards)
		noteIDByIndex[i] = n.ID
		createdNote[i] = true
		touchedNote[i] = true
	}

	// cards.json keeps the card set consistent after an import: verify that every template declared in the package was generated.
	cardIDByNote := map[uint64]map[string]uint64{}
	for i := range pkg.Notes {
		if noteIDByIndex[i] == 0 {
			continue
		}
		m, err := cardTemplatesTx(ctx, tx, noteIDByIndex[i])
		if err != nil {
			return err
		}
		cardIDByNote[noteIDByIndex[i]] = m
	}
	for _, pc := range pkg.Cards {
		if pc.NoteIndex < 0 || pc.NoteIndex >= len(pkg.Notes) {
			report.Errors = append(report.Errors, PackageImportError{fmt.Sprintf("cards[%s]", pc.Template), "note_index out of range"})
			continue
		}
		m := cardIDByNote[noteIDByIndex[pc.NoteIndex]]
		cardID, ok := m[pc.Template]
		if !ok {
			report.Errors = append(report.Errors, PackageImportError{fmt.Sprintf("cards[%s]", pc.Template), "declared template was not produced"})
			continue
		}
		// The subdeck path is card state, not note content: the generated card carries no path, so the package's
		// value is written onto the card that matched by template.
		if !touchedNote[pc.NoteIndex] {
			continue
		}
		if err := tx.WithContext(ctx).Model(&Card{}).Where("id = ?", cardID).
			Updates(map[string]any{"deck_path": pc.DeckPath}).Error; err != nil {
			return fmt.Errorf("import package: set deck path of card %d: %w", cardID, err)
		}
	}

	// Media: written to disk by sha256; bytes already stored are not rewritten but still get the dedup-hit rules of
	// MediaStore.Put (uploader record, orphan mark cleared). dry_run only counts.
	//
	// It must go through the transaction handle tx, not s.db: s.db is a different connection and on SQLite the outer write transaction's
	// file lock shuts it out (SQLITE_BUSY: database is locked) -- this is exactly why package import with media always failed on SQLite
	// (PostgreSQL passed by luck because it allows concurrent writes from several connections, masking the defect).
	// With tx, media metadata and deck content become visible atomically in one transaction; under dry_run not even a file is written.
	for sha, raw := range pkg.MediaRaw {
		if opts.MediaRoot == "" {
			continue
		}
		mstore := NewMediaStore(tx)
		existing, err := mstore.BySha256(ctx, sha)
		if err != nil {
			return fmt.Errorf("import package: check media %s: %w", sha, err)
		}
		if existing != nil {
			if opts.DryRun {
				continue
			}
			if _, _, err := mstore.reuse(ctx, existing, Ptr(actorUserID)); err != nil {
				return err
			}
			continue
		}
		report.MediaNew++
		if opts.DryRun {
			continue
		}
		// The mime stored is the one classified from the bytes and, for text classes, the file name; the media.json
		// declaration is informational and is never written into the media table as truth.
		detected, _ := mediatype.Classify(headBytes(raw), path.Base(pkg.Media[sha].Path))
		_, abs, err := mstore.SaveBytesTracked(ctx, opts.MediaRoot, detected, raw, Ptr(actorUserID))
		if abs != "" {
			// Register the path before checking the error: SaveBytesTracked may return both a path and an error when the file write succeeded but the metadata write failed,
			// and in that case the file still needs cleaning up on the failure path.
			*writtenMedia = append(*writtenMedia, mediaWrite{sha: sha, abs: abs})
		}
		if err != nil {
			return err
		}
	}

	// Progress rule: importing is only allowed into one's own account; when the package's exported_by differs from the current user, it is discarded by default and reported.
	if pkg.Progress != nil && len(pkg.Progress.CardStates) > 0 {
		sameOwner := pkg.Manifest.ExportedBy == "" || pkg.Manifest.ExportedBy == username
		if !sameOwner && !opts.AllowOthersProgress {
			report.ProgressDiscarded = true
			report.ProgressSkipped = len(pkg.Progress.CardStates)
		} else {
			if err := s.applyProgress(ctx, tx, actorUserID, pkg, noteIDByIndex, cardIDByNote, createdNote, report); err != nil {
				return err
			}
		}
	}

	if opts.OnConflict == "fail" && len(report.Errors) > 0 {
		return &PackageError{Code: CodePackageBadFormat, Message: "import aborted: one or more entries conflict or are invalid", Entries: errorEntries(report.Errors)}
	}
	if opts.DryRun {
		return ErrPackageDryRun
	}
	return nil
}

// resolveTargetDeck resolves the three targets and returns the target deck id; new_deck creates a deck and a preset under the package's deck name.
func (s *DeckStore) resolveTargetDeck(ctx context.Context, tx *gorm.DB, actorUserID uint64, pkg *packageModel, targetKind string, targetDeckID uint64, opts PackageImportOptions) (uint64, error) {
	switch targetKind {
	case PackageTargetNewDeck:
		name := opts.NewDeckName
		if name == "" {
			name = pkg.Manifest.Deck.Name
		}
		if name == "" {
			name = "Imported deck"
		}
		// The override (opts.NewDeckName) may differ from the manifest: the bounds apply to the value finally written into decks.name as well,
		// otherwise a caller could use the override to bypass manifest validation.
		if err := packageDeckMetaError(textFieldErrors("deck.name", name, maxPackageDeckNameChars)); err != nil {
			return 0, err
		}
		name, err := uniqueDeckName(ctx, tx, actorUserID, name)
		if err != nil {
			return 0, err
		}
		presetID, err := createPresetFromPackage(ctx, tx, actorUserID, &pkg.Preset, name, opts.ApplyWeights)
		if err != nil {
			return 0, err
		}
		d := Deck{
			OwnerUserID: actorUserID, Name: name, Description: pkg.Manifest.Deck.Description,
			PresetID: presetID, CreatedAt: opts.Now().UTC(),
		}
		if err := tx.WithContext(ctx).Create(&d).Error; err != nil {
			return 0, fmt.Errorf("import package: create deck: %w", err)
		}
		return d.ID, nil
	case "into_deck", "replace_deck":
		// The caller (REST/MCP/Web/CLI) must already have checked permissions on the target deck: merging needs editor,
		// replacing is destructive and needs owner. The store layer does not re-check permissions and has no actor role to check with.
		var d Deck
		if err := tx.WithContext(ctx).First(&d, "id = ?", targetDeckID).Error; err != nil {
			return 0, &PackageError{Code: CodePackageBadFormat, Message: "target deck not found"}
		}
		return d.ID, nil
	default:
		return 0, &PackageError{Code: CodePackageBadFormat, Message: "unknown target"}
	}
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
// With applyWeights false the weights and their two metadata fields are not written: the importer's review log lacks the reviews that fitted these weights,
// and keeping weights_review_count would make the preset show an optimization record with no traceable source in this account.
func createPresetFromPackage(ctx context.Context, tx *gorm.DB, owner uint64, pp *PackagePreset, deckName string, applyWeights bool) (uint64, error) {
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
		return 0, fmt.Errorf("list preset names: %w", err)
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
	if applyWeights && len(pp.Weights) > 0 {
		raw, err := json.Marshal(pp.Weights)
		if err != nil {
			return 0, err
		}
		s := string(raw)
		p.WeightsJSON = &s
		p.WeightsReviewCount = pp.WeightsReviewCount
		if pp.WeightsOptimizedAt != nil {
			if t, err := time.Parse(time.RFC3339, *pp.WeightsOptimizedAt); err == nil {
				p.WeightsOptimizedAt = &t
			}
		}
	}
	if err := tx.WithContext(ctx).Create(&p).Error; err != nil {
		return 0, fmt.Errorf("import package: create preset: %w", err)
	}
	return p.ID, nil
}

// existingNoteIndex builds an index of the target deck's existing notes: external_ref -> note, fingerprint -> note.
func (s *DeckStore) existingNoteIndex(ctx context.Context, tx *gorm.DB, deckID uint64) (map[string]*Note, map[string]*Note, error) {
	var notes []Note
	if err := tx.WithContext(ctx).Where("deck_id = ?", deckID).Find(&notes).Error; err != nil {
		return nil, nil, fmt.Errorf("load existing notes: %w", err)
	}
	byRef := map[string]*Note{}
	byFP := map[string]*Note{}
	ids := make([]uint64, 0, len(notes))
	for i := range notes {
		if notes[i].NoteTypeID != nil {
			ids = append(ids, *notes[i].NoteTypeID)
		}
	}
	types, err := NewNoteTypeStore(tx).ByIDs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	typeKeys := make(map[uint64]string, len(types))
	for id, nt := range types {
		cfg, err := nt.Config()
		if err != nil {
			continue
		}
		key, err := noteTypeKey(nt.Name, cfg)
		if err != nil {
			continue
		}
		typeKeys[id] = key
	}
	for i := range notes {
		n := &notes[i]
		if n.ExternalRef != nil && *n.ExternalRef != "" {
			byRef[*n.ExternalRef] = n
		}
		if n.NoteTypeID == nil {
			continue
		}
		key, ok := typeKeys[*n.NoteTypeID]
		if !ok {
			continue
		}
		// The fingerprint compares the content the package form carries, so a note exported from this deck and
		// imported again matches the row it came from.
		raw, err := json.Marshal(NoteFieldValues(n.FieldsJSON))
		if err != nil {
			return nil, nil, fmt.Errorf("encode existing fields of note %d: %w", n.ID, err)
		}
		byFP[NoteFingerprint(key, string(raw))] = n
	}
	return byRef, byFP, nil
}

// fingerprintFromFields computes the content fingerprint of a note in the package (the same algorithm as NoteFingerprint).
func fingerprintFromFields(noteTypeKey string, fields map[string]string) string {
	raw, err := json.Marshal(fields)
	if err != nil {
		raw = []byte("{}")
	}
	return NoteFingerprint(noteTypeKey, string(raw))
}

// cardTemplatesTx returns a note's template -> card id mapping (soft-deleted rows included).
func cardTemplatesTx(ctx context.Context, tx *gorm.DB, noteID uint64) (map[string]uint64, error) {
	var cards []Card
	if err := tx.WithContext(ctx).Unscoped().Where("note_id = ?", noteID).Find(&cards).Error; err != nil {
		return nil, fmt.Errorf("list cards for note %d: %w", noteID, err)
	}
	m := make(map[string]uint64, len(cards))
	for _, c := range cards {
		m[c.Template] = c.ID
	}
	return m, nil
}

// applyProgress maps the package's progress onto the new cards and only writes into actorUserID's own account.
func (s *DeckStore) applyProgress(ctx context.Context, tx *gorm.DB, actorUserID uint64, pkg *packageModel, noteIDByIndex []uint64, cardIDByNote map[uint64]map[string]uint64, createdNote []bool, report *PackageImportReport) error {
	// external_ref -> note_index (progress may also locate a note by external_ref alone).
	refIndex := map[string]int{}
	for i := range pkg.Notes {
		if pkg.Notes[i].ExternalRef != "" {
			refIndex[pkg.Notes[i].ExternalRef] = i
		}
	}
	resolve := func(noteIndex *int, ref, template string) (uint64, bool) {
		idx := -1
		if noteIndex != nil {
			idx = *noteIndex
		} else if ref != "" {
			if v, ok := refIndex[ref]; ok {
				idx = v
			}
		}
		if idx < 0 || idx >= len(noteIDByIndex) || noteIDByIndex[idx] == 0 {
			return 0, false
		}
		m := cardIDByNote[noteIDByIndex[idx]]
		if m == nil {
			return 0, false
		}
		// When template is omitted the first card is used (progress entries may leave template out).
		if template == "" {
			for _, id := range m {
				return id, true
			}
			return 0, false
		}
		id, ok := m[template]
		return id, ok
	}

	for _, ps := range pkg.Progress.CardStates {
		cardID, ok := resolve(ps.NoteIndex, ps.ExternalRef, ps.Template)
		if !ok {
			report.ProgressSkipped++
			continue
		}
		row := CardState{
			CardID: cardID, UserID: actorUserID, State: ps.State,
			StepIndex: ps.StepIndex, Stability: ps.Stability, Difficulty: ps.Difficulty,
			Reps: ps.Reps, Lapses: ps.Lapses, ScheduledDays: ps.ScheduledDays,
			ElapsedDays: ps.ElapsedDays, Version: ps.Version,
		}
		row.DueAt = parseTimePtr(ps.DueAt)
		row.LastReviewAt = parseTimePtr(ps.LastReviewAt)
		if err := tx.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "card_id"}, {Name: "user_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"state", "due_at", "step_index", "stability", "difficulty", "reps", "lapses", "scheduled_days", "elapsed_days", "last_review_at", "version"}),
		}).Create(&row).Error; err != nil {
			return fmt.Errorf("apply progress for card %d: %w", cardID, err)
		}
		report.ProgressApplied++
	}

	// The review log is written only with newly created notes, so importing the same package twice does not insert duplicate append-only rows.
	for _, pr := range pkg.Progress.Reviews {
		idx := -1
		if pr.NoteIndex != nil {
			idx = *pr.NoteIndex
		} else if pr.ExternalRef != "" {
			if v, ok := refIndex[pr.ExternalRef]; ok {
				idx = v
			}
		}
		if idx < 0 || idx >= len(pkg.Notes) || !createdNote[idx] {
			continue
		}
		cardID, ok := resolve(&idx, "", pr.Template)
		if !ok {
			continue
		}
		reviewedAt, err := time.Parse(time.RFC3339, pr.ReviewedAt)
		if err != nil {
			continue
		}
		src := pr.GradeSource
		if src == "" {
			src = "self"
		}
		row := Review{
			CardID: cardID, UserID: actorUserID, Rating: pr.Rating, GradeSource: src,
			ReviewedAt: reviewedAt.UTC(), ReviewDay: pr.ReviewDay, ElapsedMS: pr.ElapsedMS,
			DurationDays: pr.DurationDays, StateBefore: pr.StateBefore,
			StepIndexBefore: pr.StepIndexBefore, DueBefore: parseTimePtr(pr.DueBefore),
			IntervalDays: pr.IntervalDays, Stability: pr.Stability, Difficulty: pr.Difficulty,
		}
		if row.ReviewDay == "" {
			row.ReviewDay = reviewedAt.UTC().Format("2006-01-02")
		}
		if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
			return fmt.Errorf("apply review for card %d: %w", cardID, err)
		}
	}
	return nil
}

func parseTimePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil
	}
	return &t
}

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
