package store

import (
	"errors"
	"strings"
	"time"
)

// Stable import error codes (English identifiers so callers can branch programmatically; AGENTS.md §2.1).
const (
	CodePackageUnsafeEntry     = "package_unsafe_entry"
	CodePackageTooLarge        = "package_too_large"
	CodePackageBadFormat       = "package_bad_format"
	CodePresetSelectionInvalid = "preset_selection_invalid"
	// CodePackageUnknownKind means a note names a note type reference that note_types.json does not declare;
	// the package must error and list every offending note. The code value is kept for API compatibility.
	CodePackageUnknownKind = "package_unknown_kind"
	// CodePackageFormatObsolete means the package uses a format this release cannot read. The message tells the
	// user to import it with an older release and export it again in the current format.
	CodePackageFormatObsolete = "package_format_obsolete"
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
	// CodeInvalidRequest is the request-validation code the HTTP API and the MCP tools share. A package import uses it
	// for a malformed import option, which is a client mistake about the request, not about the archive's content.
	CodeInvalidRequest = "invalid_request"
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
	// IncludeMedia defaults to on for the transports; the zero value writes none.
	IncludeMedia bool
	// IncludeReviews is independent of IncludeProgress: the review log is written whenever
	// it is on, including when the cards themselves stay new.
	IncludeReviews bool
	// IncludeDeckConfigs defaults to on: nil means the package carries the caller's
	// effective deck configuration. An explicit false leaves dconf empty.
	//
	// It is a pointer because its default is on while the zero value of a bool is off;
	// nil selects the default.
	IncludeDeckConfigs *bool
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

// DeckConfigsEnabled reports the effective IncludeDeckConfigs switch: nil means on.
func (o PackageOptions) DeckConfigsEnabled() bool {
	return o.IncludeDeckConfigs == nil || *o.IncludeDeckConfigs
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

var (
	// ErrPackageDeckRequired means the export named no deck.
	ErrPackageDeckRequired = errors.New("deck package: deck id is required")
	// ErrPackageMediaMissing means the package references media whose bytes are unavailable.
	ErrPackageMediaMissing = errors.New("deck package: media bytes are unavailable")
)
