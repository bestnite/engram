package store

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Deck package (.edeck) export/import.
//
// Format: one zip holding manifest.json / notes.json / cards.json / preset.json,
// optionally progress.json and media/ + media.json. It turns \"one deck\" into a self-contained file,
// for backup, migration between instances and offline handover; the semantics match clone (copy within one instance): content travels, progress does not follow.

// PackageFormatVersion is the current package format version; the import side is compatible with at least N-1.
const PackageFormatVersion = 1

// Stable import error codes (English identifiers so callers can branch programmatically; AGENTS.md §2.1).
const (
	CodePackageUnsafeEntry = "package_unsafe_entry"
	CodePackageTooLarge    = "package_too_large"
	CodePackageBadFormat   = "package_bad_format"
	// CodePackageUnknownKind means the package holds a kind this instance does not know (or has not registered); it must error and list every one.
	CodePackageUnknownKind = "package_unknown_kind"
	// CodePackageUnsafeMedia means the real bytes of package media are not in the allowlist or disagree with the media.json declaration; listed one by one.
	CodePackageUnsafeMedia = "package_unsafe_media"
	// CodePackageDeckMetaInvalid means the manifest's deck name/description crosses a bound (length, control characters or invalid UTF-8);
	// the offending fields and reasons are listed one by one.
	CodePackageDeckMetaInvalid = "package_deck_meta_invalid"
	// CodePackageQuotaExceeded means the imported media would exceed the importer's quota; it reuses the upload chain's stable code
	// media_quota_exceeded (the same value as internal/media.CodeQuotaExceeded), and all four import entry points use it.
	CodePackageQuotaExceeded = "media_quota_exceeded"
	// CodePackageMediaForbidden means a note in the package references media the importer cannot read (rejected by pre-write validation);
	// the rejected references are listed one by one. It is the same rule as media_not_readable on single-card writes,
	// carried as a stable code on the package import chain.
	CodePackageMediaForbidden = "package_media_forbidden"
)

// PackageError is a deck package error carrying a stable code.
type PackageError struct {
	Code    string
	Message string
	// Entries lists the offending entries in the package (e.g. the note indexes of unknown kinds); may be empty.
	Entries []string
}

func (e *PackageError) Error() string {
	if len(e.Entries) == 0 {
		return e.Code + ": " + e.Message
	}
	return e.Code + ": " + e.Message + ": " + strings.Join(e.Entries, ", ")
}

// PackageOptions controls what one export contains (four switches).
type PackageOptions struct {
	// IncludeProgress defaults to off: the main use of a deck package is giving content to someone else or moving it to another instance.
	IncludeProgress bool
	// IncludeMedia defaults to on.
	IncludeMedia bool
	// IncludeReviews depends on IncludeProgress; the review log is written only when both are true.
	IncludeReviews bool
	// IncludeWeights defaults to off: FSRS weights are fitted from the exporter's own review log and describe that person's
	// memory curve, which is not part of the deck content. When off, the weights, weights_optimized_at and
	// weights_review_count fields of preset.json are all written as null; otherwise the import side keeps a preset claiming "optimized from N reviews" whose
	// reviews cannot be found. It does not depend on IncludeProgress.
	IncludeWeights bool
	// MediaRoot is the local root of media bytes (media.Store.Root()); empty means media cannot be inlined.
	MediaRoot string
	// AppVersion is written into manifest.app_version as a hint only.
	AppVersion string
	// Now is an injectable clock; the zero value uses the system UTC time.
	Now func() time.Time
}

// PackageManifest is the in-package manifest.json (field names are frozen).
type PackageManifest struct {
	FormatVersion   int          `json:"format_version"`
	ExportedAt      string       `json:"exported_at"`
	AppVersion      string       `json:"app_version,omitempty"`
	Deck            PackageDeck  `json:"deck"`
	IncludeProgress bool         `json:"include_progress"`
	IncludeMedia    bool         `json:"include_media"`
	IncludeReviews  bool         `json:"include_reviews"`
	Counts          PackageCount `json:"counts"`
	// ExportedBy is the exporter's opaque identifier (the login name, not the database id),
	// which the import side uses to decide whether the progress in the package belongs to the importer ("progress ownership check").
	ExportedBy string `json:"exported_by,omitempty"`
	// IncludeWeights declares whether preset.json carries non-null weights; packages older than this field lack it.
	// The import side judges only by the weights in preset.json itself; this is a declaration so the UI can warn ahead of time.
	IncludeWeights bool `json:"include_weights"`
}

// PackageDeck is the deck metadata that travels with the package; instance-local state such as authorization and audit is never exported.
type PackageDeck struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// PackageCount holds the manifest's entry counts.
type PackageCount struct {
	Notes int `json:"notes"`
	Cards int `json:"cards"`
	Media int `json:"media,omitempty"`
}

// PackageNote is one entry of notes.json; its shape matches the bulk import body.
type PackageNote struct {
	Kind        string         `json:"kind"`
	Fields      map[string]any `json:"fields"`
	ExternalRef string         `json:"external_ref,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
	SourceURL   string         `json:"source_url,omitempty"`
}

// PackageCard uses note_index (an index into notes[]) instead of an ID, so ID semantics do not entangle export and import.
type PackageCard struct {
	NoteIndex int    `json:"note_index"`
	Template  string `json:"template"`
	Ordinal   int    `json:"ordinal"`
}

// PackagePreset is the scheduling parameters that travel with the package; weights are 21 numbers or null (null = use the default weights).
type PackagePreset struct {
	Name                string    `json:"name,omitempty"`
	DesiredRetention    float64   `json:"desired_retention"`
	LearningSteps       string    `json:"learning_steps"`
	RelearningSteps     string    `json:"relearning_steps"`
	MaximumIntervalDays int       `json:"maximum_interval_days"`
	EnableFuzz          bool      `json:"enable_fuzz"`
	Weights             []float64 `json:"weights"`
	WeightsOptimizedAt  *string   `json:"weights_optimized_at"`
	WeightsReviewCount  *int      `json:"weights_review_count"`
}

// PackageCardState is one progress entry in progress.json; the note is located by note_index or external_ref.
type PackageCardState struct {
	NoteIndex     *int     `json:"note_index,omitempty"`
	ExternalRef   string   `json:"external_ref,omitempty"`
	Template      string   `json:"template,omitempty"`
	State         string   `json:"state"`
	DueAt         *string  `json:"due_at"`
	StepIndex     int      `json:"step_index"`
	Stability     *float64 `json:"stability"`
	Difficulty    *float64 `json:"difficulty"`
	Reps          int      `json:"reps"`
	Lapses        int      `json:"lapses"`
	ScheduledDays int      `json:"scheduled_days"`
	ElapsedDays   int      `json:"elapsed_days"`
	LastReviewAt  *string  `json:"last_review_at"`
	Version       int      `json:"version"`
}

// PackageProgress holds only the exporter's own progress; it is discarded by default when someone else's package is imported.
type PackageProgress struct {
	CardStates []PackageCardState `json:"card_states"`
	Reviews    []PackageReview    `json:"reviews,omitempty"`
}

// PackageReview is one optional review-log entry in progress.json.
type PackageReview struct {
	NoteIndex    *int     `json:"note_index,omitempty"`
	ExternalRef  string   `json:"external_ref,omitempty"`
	Template     string   `json:"template,omitempty"`
	Rating       int      `json:"rating"`
	GradeSource  string   `json:"grade_source,omitempty"`
	ReviewedAt   string   `json:"reviewed_at"`
	ReviewDay    string   `json:"review_day,omitempty"`
	ElapsedMS    *int     `json:"elapsed_ms"`
	DurationDays *float64 `json:"duration_days"`
	StateBefore  int      `json:"state_before"`
	// StepIndexBefore / DueBefore are the state snapshot from before the rating (remaining learning-step cursor, due date).
	// Undo can restore the pre-rating state exactly only through them: due_at is changed by burying without a log entry, and step_index has no other source.
	// Nullable: old packages and old rows lack them, and undo falls back to deriving from the previous log entry when it meets NULL.
	StepIndexBefore *int     `json:"step_index_before,omitempty"`
	DueBefore       *string  `json:"due_before,omitempty"`
	IntervalDays    *float64 `json:"interval_days"`
	Stability       *float64 `json:"stability"`
	Difficulty      *float64 `json:"difficulty"`
}

// PackageMediaEntry is one entry of media.json: sha256 -> in-package relative path + mime.
type PackageMediaEntry struct {
	Path   string `json:"path"`
	Mime   string `json:"mime"`
	Bytes  int64  `json:"bytes,omitempty"`
	Width  *int   `json:"width,omitempty"`
	Height *int   `json:"height,omitempty"`
}

// DeckPackage is an assembled deck package (in-memory form). Media bytes are indexed by sha256,
// which makes writing them into the zip on export easy, or rendering the whole package as one JSON document for schema validation.
type DeckPackage struct {
	Manifest PackageManifest
	Notes    []PackageNote
	Cards    []PackageCard
	Preset   PackagePreset
	Progress *PackageProgress
	// Media is sha256 -> entry; MediaBytes is the same sha256 -> bytes.
	Media      map[string]PackageMediaEntry
	MediaBytes map[string][]byte
}

var (
	// ErrPackageDeckRequired means the export named no deck.
	ErrPackageDeckRequired = errors.New("deck package: deck id is required")
	// ErrPackageProgressRequiresReviews means the review log was requested without progress enabled.
	ErrPackageProgressRequiresReviews = errors.New("deck package: include_reviews requires include_progress")
	// ErrPackageMediaMissing means the package references media whose bytes are unavailable.
	ErrPackageMediaMissing = errors.New("deck package: media bytes are unavailable")
)

// mediaRefRE matches a media reference inside a note field: the note type model writes the deck-scoped file name
// <sha256>.<ext>, and the package document's own convention prefixes it with media/. Both forms name the same bytes.
var mediaRefRE = regexp.MustCompile(`(?:^|[^0-9a-f])(?:media/)?([0-9a-f]{64})\.([A-Za-z0-9]+)`)

// ExportPackage assembles a deck package.
//
// Boundaries (content and progress kept apart):
//   - progress comes only from actorUserID's own card_states; when exporting a deck someone else shared, the package never contains their progress.
//   - instance-local state such as authorization and audit is never exported.
//   - with include_media=0, Media is empty and manifest.include_media=false, declaring it honestly.
func (s *DeckStore) ExportPackage(ctx context.Context, actorUserID, deckID uint64, opts PackageOptions) (*DeckPackage, error) {
	if deckID == 0 {
		return nil, ErrPackageDeckRequired
	}
	if opts.IncludeReviews && !opts.IncludeProgress {
		return nil, ErrPackageProgressRequiresReviews
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	deck, err := s.ByID(ctx, deckID)
	if err != nil {
		return nil, fmt.Errorf("export package: load deck: %w", err)
	}
	// The package carries the preset in effect for the exporter on this deck: a member exporting a shared deck does not carry out the owner's parameters.
	settings, err := s.StudySettings(ctx, actorUserID, deck)
	if err != nil {
		return nil, fmt.Errorf("export package: load study settings: %w", err)
	}
	preset, err := NewPresetStore(s.db).ByID(ctx, settings.PresetID)
	if err != nil {
		return nil, fmt.Errorf("export package: load preset: %w", err)
	}

	// Only notes that are not soft-deleted are exported (GORM's default scope); cards are collected by note along the way.
	var notes []Note
	if err := s.db.WithContext(ctx).Where("deck_id = ?", deckID).Order("id ASC").Find(&notes).Error; err != nil {
		return nil, fmt.Errorf("export package: list notes: %w", err)
	}

	pkg := &DeckPackage{
		Preset: presetToPackage(preset, opts.IncludeWeights),
		Media:  map[string]PackageMediaEntry{},
	}
	if !opts.IncludeMedia {
		pkg.MediaBytes = nil
	} else {
		pkg.MediaBytes = map[string][]byte{}
	}

	// note_index -> note id, used by the progress mapping.
	noteIDByIndex := make([]uint64, 0, len(notes))
	refs := map[string]bool{} // referenced media sha256 -> first appearance
	for i := range notes {
		n := &notes[i]
		values, err := ParseFieldValues(n.FieldsJSON)
		if err != nil {
			return nil, fmt.Errorf("export package: note %d: %w", n.ID, err)
		}
		tags, err := ParseTags(n.TagsJSON)
		if err != nil {
			return nil, fmt.Errorf("export package: note %d: %w", n.ID, err)
		}
		kind := NewNoteStore(s.db).KindOf(ctx, n)
		fields := LegacyNoteFields(kind, values)
		// source_url and extra live in their own columns in the new model; the format-1 document carries them as fields.
		if n.SourceURL != nil && *n.SourceURL != "" {
			fields["source_url"] = *n.SourceURL
		}
		if n.ExtraJSON != nil {
			var extra any
			if err := json.Unmarshal([]byte(*n.ExtraJSON), &extra); err == nil {
				fields["extra"] = extra
			}
		}
		pn := PackageNote{Kind: kind, Fields: fields, Tags: tags}
		if n.ExternalRef != nil {
			pn.ExternalRef = *n.ExternalRef
		}
		pkg.Notes = append(pkg.Notes, pn)
		noteIDByIndex = append(noteIDByIndex, n.ID)

		var cards []Card
		if err := s.db.WithContext(ctx).Where("note_id = ?", n.ID).
			Order("ordinal ASC, id ASC").Find(&cards).Error; err != nil {
			return nil, fmt.Errorf("export package: list cards for note %d: %w", n.ID, err)
		}
		for _, c := range cards {
			pkg.Cards = append(pkg.Cards, PackageCard{NoteIndex: i, Template: c.Template, Ordinal: c.Ordinal})
		}
		if opts.IncludeMedia {
			collectMediaRefs(fields, refs)
		}
	}

	// Media: de-duplicated by sha256 and then inlined; the bytes come from the local media root.
	if opts.IncludeMedia && len(refs) > 0 {
		mstore := NewMediaStore(s.db)
		for sha := range refs {
			m, err := mstore.BySha256(ctx, sha)
			if err != nil {
				return nil, fmt.Errorf("export package: media metadata %s: %w", sha, err)
			}
			if m == nil {
				return nil, fmt.Errorf("%w: sha256=%s", ErrPackageMediaMissing, sha)
			}
			ext := strings.TrimPrefix(filepath.Ext(m.RelPath), ".")
			rel := "media/" + sha + "." + ext
			raw, err := os.ReadFile(filepath.Join(opts.MediaRoot, filepath.FromSlash(m.RelPath)))
			if err != nil {
				return nil, fmt.Errorf("%w: sha256=%s: %v", ErrPackageMediaMissing, sha, err)
			}
			pkg.Media[sha] = PackageMediaEntry{Path: rel, Mime: m.Mime, Bytes: m.Bytes, Width: m.Width, Height: m.Height}
			pkg.MediaBytes[sha] = raw
		}
	}

	pkg.Manifest = PackageManifest{
		FormatVersion:   PackageFormatVersion,
		ExportedAt:      opts.Now().UTC().Format(time.RFC3339),
		AppVersion:      opts.AppVersion,
		Deck:            PackageDeck{Name: deck.Name, Description: deck.Description},
		IncludeProgress: opts.IncludeProgress,
		IncludeMedia:    opts.IncludeMedia,
		IncludeReviews:  opts.IncludeReviews,
		IncludeWeights:  pkg.Preset.Weights != nil,
		Counts:          PackageCount{Notes: len(pkg.Notes), Cards: len(pkg.Cards), Media: len(pkg.Media)},
	}

	if opts.IncludeProgress {
		prog, err := s.exportProgress(ctx, actorUserID, noteIDByIndex, opts.IncludeReviews)
		if err != nil {
			return nil, err
		}
		pkg.Progress = prog
		if u, err := s.lookupUsername(ctx, actorUserID); err == nil {
			pkg.Manifest.ExportedBy = u
		}
	}
	return pkg, nil
}

// exportProgress exports only actorUserID's own card_states (+ optional reviews).
// This is where "never include someone else's progress" is implemented: the query hard-codes user_id = actorUserID.
func (s *DeckStore) exportProgress(ctx context.Context, actorUserID uint64, noteIDs []uint64, withReviews bool) (*PackageProgress, error) {
	indexByNoteID := make(map[uint64]int, len(noteIDs))
	for i, id := range noteIDs {
		indexByNoteID[id] = i
	}
	prog := &PackageProgress{CardStates: []PackageCardState{}}

	// The cards join restricts it to this note's deck.
	type row struct {
		CardState
		NoteID   uint64
		Template string
	}
	var rows []row
	if err := s.db.WithContext(ctx).Table("card_states").
		Select("card_states.*, cards.note_id AS note_id, cards.template AS template").
		Joins("JOIN cards ON cards.id = card_states.card_id AND cards.deleted_at IS NULL").
		Where("card_states.user_id = ? AND cards.note_id IN ?", actorUserID, noteIDs).
		Order("card_states.card_id ASC").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("export package: list progress: %w", err)
	}
	cardIDs := make([]uint64, 0, len(rows))
	stateByCardID := make(map[uint64]PackageCardState, len(rows))
	for _, r := range rows {
		idx := indexByNoteID[r.NoteID]
		ps := cardStateToPackage(r.CardState, &idx, r.Template)
		prog.CardStates = append(prog.CardStates, ps)
		cardIDs = append(cardIDs, r.CardID)
		stateByCardID[r.CardID] = ps
	}

	if withReviews && len(cardIDs) > 0 {
		var reviews []Review
		if err := s.db.WithContext(ctx).Where("user_id = ? AND card_id IN ?", actorUserID, cardIDs).
			Order("id ASC").Find(&reviews).Error; err != nil {
			return nil, fmt.Errorf("export package: list reviews: %w", err)
		}
		// card_id -> note_index/template (reusing the state mapping above).
		cardIndex := map[uint64]int{}
		cardTemplate := map[uint64]string{}
		for _, r := range rows {
			cardIndex[r.CardID] = indexByNoteID[r.NoteID]
			cardTemplate[r.CardID] = r.Template
		}
		prog.Reviews = make([]PackageReview, 0, len(reviews))
		for i := range reviews {
			rv := reviews[i]
			idx := cardIndex[rv.CardID]
			prog.Reviews = append(prog.Reviews, PackageReview{
				NoteIndex:    &idx,
				Template:     cardTemplate[rv.CardID],
				Rating:       rv.Rating,
				GradeSource:  rv.GradeSource,
				ReviewedAt:   rv.ReviewedAt.UTC().Format(time.RFC3339),
				ReviewDay:    rv.ReviewDay,
				ElapsedMS:    rv.ElapsedMS,
				DurationDays: rv.DurationDays,
				StateBefore:  rv.StateBefore,
				// The pre-rating snapshot must travel with the package: only then can undo restore exactly after a backup is restored, otherwise it restores NULL or a wrong value.
				StepIndexBefore: rv.StepIndexBefore,
				DueBefore:       formatTimePtr(rv.DueBefore),
				IntervalDays:    rv.IntervalDays,
				Stability:       rv.Stability,
				Difficulty:      rv.Difficulty,
			})
		}
	}
	return prog, nil
}

func (s *DeckStore) lookupUsername(ctx context.Context, userID uint64) (string, error) {
	var u User
	if err := s.db.WithContext(ctx).Select("id", "username").First(&u, "id = ?", userID).Error; err != nil {
		return "", err
	}
	return u.Username, nil
}

// WriteZip writes the package as an .edeck zip; the entry order is fixed, which makes diffing and verification easy.
func (p *DeckPackage) WriteZip(w io.Writer) error {
	zw := zip.NewWriter(w)
	write := func(name string, v any) error {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("encode %s: %w", name, err)
		}
		f, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("create %s: %w", name, err)
		}
		_, err = f.Write(raw)
		return err
	}
	if err := write("manifest.json", p.Manifest); err != nil {
		return err
	}
	if err := write("notes.json", p.Notes); err != nil {
		return err
	}
	if err := write("cards.json", p.Cards); err != nil {
		return err
	}
	if err := write("preset.json", p.Preset); err != nil {
		return err
	}
	if p.Manifest.IncludeProgress && p.Progress != nil {
		if err := write("progress.json", p.Progress); err != nil {
			return err
		}
	}
	if p.Manifest.IncludeMedia && len(p.MediaBytes) > 0 {
		if err := write("media.json", p.Media); err != nil {
			return err
		}
		shas := make([]string, 0, len(p.MediaBytes))
		for sha := range p.MediaBytes {
			shas = append(shas, sha)
		}
		sort.Strings(shas)
		for _, sha := range shas {
			name := p.Media[sha].Path
			f, err := zw.Create(name)
			if err != nil {
				return fmt.Errorf("create %s: %w", name, err)
			}
			if _, err := f.Write(p.MediaBytes[sha]); err != nil {
				return fmt.Errorf("write %s: %w", name, err)
			}
		}
	}
	return zw.Close()
}

// Document renders the whole package as one JSON document keyed by in-package entry name (media/ entries are base64 strings),
// with a shape matching the logical content described by schema/deck-package.schema.json, for use by schema validation.
func (p *DeckPackage) Document() map[string]any {
	doc := map[string]any{
		"manifest.json": p.Manifest,
		"notes.json":    p.Notes,
		"cards.json":    p.Cards,
		"preset.json":   p.Preset,
	}
	if p.Manifest.IncludeProgress && p.Progress != nil {
		doc["progress.json"] = p.Progress
	}
	if p.Manifest.IncludeMedia && len(p.MediaBytes) > 0 {
		doc["media.json"] = p.Media
		shas := make([]string, 0, len(p.MediaBytes))
		for sha := range p.MediaBytes {
			shas = append(shas, sha)
		}
		sort.Strings(shas)
		for _, sha := range shas {
			doc[p.Media[sha].Path] = base64.StdEncoding.EncodeToString(p.MediaBytes[sha])
		}
	}
	return doc
}

// presetToPackage turns a store.Preset into the in-package preset (weights_json -> []float64 or null).
// When withWeights is false the weights and their two metadata fields all stay null: the three describe the same optimization, and carrying only some of them would contradict itself.
func presetToPackage(p *Preset, withWeights bool) PackagePreset {
	out := PackagePreset{
		Name:                p.Name,
		DesiredRetention:    p.DesiredRetention,
		LearningSteps:       p.LearningSteps,
		RelearningSteps:     p.RelearningSteps,
		MaximumIntervalDays: p.MaximumIntervalDays,
		EnableFuzz:          p.FuzzEnabled(),
	}
	if !withWeights || p.WeightsJSON == nil {
		return out
	}
	var w []float64
	if json.Unmarshal([]byte(*p.WeightsJSON), &w) != nil || w == nil {
		// Undecodable weights (including a stored "null") count as using the default weights: the metadata is left out too, for the reason above.
		return out
	}
	out.Weights = w
	out.WeightsReviewCount = p.WeightsReviewCount
	if p.WeightsOptimizedAt != nil {
		s := p.WeightsOptimizedAt.UTC().Format(time.RFC3339)
		out.WeightsOptimizedAt = &s
	}
	return out
}

func cardStateToPackage(cs CardState, noteIndex *int, template string) PackageCardState {
	out := PackageCardState{
		NoteIndex:     noteIndex,
		Template:      template,
		State:         cs.State,
		StepIndex:     cs.StepIndex,
		Stability:     cs.Stability,
		Difficulty:    cs.Difficulty,
		Reps:          cs.Reps,
		Lapses:        cs.Lapses,
		ScheduledDays: cs.ScheduledDays,
		ElapsedDays:   cs.ElapsedDays,
		Version:       cs.Version,
	}
	out.DueAt = formatTimePtr(cs.DueAt)
	out.LastReviewAt = formatTimePtr(cs.LastReviewAt)
	return out
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// collectMediaRefs recursively scans note fields for media references (<sha256>.<ext> or media/<sha256>.<ext>).
func collectMediaRefs(v any, out map[string]bool) {
	switch x := v.(type) {
	case string:
		for _, m := range mediaRefRE.FindAllStringSubmatch(x, -1) {
			out[m[1]] = true
		}
	case map[string]any:
		for _, child := range x {
			collectMediaRefs(child, out)
		}
	case []any:
		for _, child := range x {
			collectMediaRefs(child, out)
		}
	}
}

// NoteFingerprint is the content fingerprint of a note: sha256 over kind + canonicalized fields (the de-duplication rule).
// Canonicalization = parse fields_json and re-serialize (encoding/json sorts map keys),
// so the same content yields the same fingerprint across different exports/imports.
func NoteFingerprint(kind, fieldsJSON string) string {
	canonical := CanonicalFieldsJSON(fieldsJSON)
	sum := sha256.Sum256([]byte(kind + "\n" + canonical))
	return hex.EncodeToString(sum[:])
}

// CanonicalFieldsJSON normalizes fields_json into compact JSON with ordered keys; on a parse failure it returns the input unchanged.
func CanonicalFieldsJSON(fieldsJSON string) string {
	fields, err := ParseFields(fieldsJSON)
	if err != nil {
		return strings.TrimSpace(fieldsJSON)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return strings.TrimSpace(fieldsJSON)
	}
	return string(raw)
}

// readZipEntry reads the content of a zip entry (with a size cap, see package_import.go).
func readZipEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(rc, limit+1)); err != nil {
		return nil, err
	}
	if int64(buf.Len()) > limit {
		return nil, fmt.Errorf("%w: entry %s", ErrPackageTooLargeSentinel, f.Name)
	}
	return buf.Bytes(), nil
}

// ErrPackageTooLargeSentinel is readZipEntry's size-limit sentinel; ReadPackageArchive wraps it into a PackageError.
var ErrPackageTooLargeSentinel = errors.New("package entry exceeds the size limit")
