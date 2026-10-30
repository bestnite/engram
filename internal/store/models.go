// Package store holds the GORM models and data access.
// The same set of models serves the domain, GORM and JSON/CSV serialization, with no DTO mapping layer (AGENTS.md §2.4).
//
// Dual-database compatibility: no PG-proprietary types (jsonb / serial / array) are used,
// JSON is always stored as TEXT; string column defaults (such as 'user' / 'basic') are not written into the DDL — an unquoted
// default may be taken for a same-named function or keyword in PostgreSQL, so the two databases behave differently; those defaults are supplied
// explicitly by the store layer on the Go side at write time. Numeric and boolean defaults mean the same on both databases and may be written in the DDL.
package store

import (
	"time"

	"gorm.io/gorm"
)

// User is a local account; the built-in account is the default identity source, external identities live in Identity.
type User struct {
	ID              uint64     `gorm:"primaryKey" json:"-"`
	PublicID        string     `gorm:"column:public_id;uniqueIndex" json:"id"`
	Username        string     `gorm:"not null;uniqueIndex" json:"username"`
	Email           string     `gorm:"not null;uniqueIndex" json:"email"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	// argon2id; a pure OIDC account may be NULL.
	PasswordHash *string `gorm:"column:password_hash" json:"-"`
	DisplayName  string  `gorm:"not null" json:"display_name"`
	Role         string  `gorm:"not null" json:"role"`   // admin | user
	Status       string  `gorm:"not null" json:"status"` // active | disabled
	Locale       string  `gorm:"not null" json:"locale"`
	Timezone     string  `gorm:"not null" json:"timezone"`
	// NULL uses the default 04:00; the pointer preserves an explicit midnight 0 and keeps the database default from overriding it.
	DayCutoffHour *int `gorm:"column:day_cutoff_hour" json:"day_cutoff_hour"`
	// ReminderHour is the local send hour the user picked (0–23, shared by the review reminder and the weekly digest); NULL means unset,
	// falling back to the global default reminder.DefaultSendHour. It must be a nullable pointer: 0 is a legal value (midnight), and a plain
	// int with a default cannot tell 'unset' from 'midnight', while GORM also takes the zero value as not provided (AGENTS.md §2.3 item 9
	// records the same pitfall: for a column with a default tag, a zero-valued field is overwritten by the database default).
	ReminderHour *int `gorm:"column:reminder_hour" json:"reminder_hour,omitempty"`
	// LearnAheadMinutes is the 'learn ahead' window (minutes): when no other card can be reviewed, learning / relearning cards due within
	// that many minutes are pulled forward, so a session does not end over a card that only comes due a minute later. NULL uses the default, 0 disables it.
	// The reason for the nullable pointer is the same as ReminderHour: 0 is a legal value.
	LearnAheadMinutes *int `gorm:"column:learn_ahead_minutes" json:"learn_ahead_minutes,omitempty"`
	// ShareAcceptFrom is 'who may share decks with me': anyone | whitelist | nobody.
	// NULL is treated as anyone (matching the behavior before this shipped: back then anyone could be shared with).
	ShareAcceptFrom *string    `gorm:"column:share_accept_from" json:"share_accept_from,omitempty"`
	CreatedAt       time.Time  `gorm:"not null" json:"created_at"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
}

// TableName pins the table name so pluralization rules cannot drift across GORM versions.
func (User) TableName() string { return "users" }

// Identity is a linked external identity (provider + subject unique).
type Identity struct {
	ID       uint64    `gorm:"primaryKey" json:"-"`
	PublicID string    `gorm:"column:public_id;uniqueIndex" json:"id"`
	UserID   uint64    `gorm:"not null;index" json:"user_id"`
	Provider string    `gorm:"not null;uniqueIndex:idx_identities_provider_subject" json:"provider"`
	Subject  string    `gorm:"not null;uniqueIndex:idx_identities_provider_subject" json:"subject"`
	Email    *string   `json:"email,omitempty"`
	LinkedAt time.Time `gorm:"not null" json:"linked_at"`
}

func (Identity) TableName() string { return "identities" }

// Invite is a one-time invite (used when the signup policy is invite).
type Invite struct {
	ID        uint64     `gorm:"primaryKey" json:"-"`
	PublicID  string     `gorm:"column:public_id;uniqueIndex" json:"id"`
	Token     string     `gorm:"not null;uniqueIndex" json:"token"`
	Email     *string    `json:"email,omitempty"`
	Role      string     `gorm:"not null" json:"role"`
	CreatedBy *uint64    `json:"created_by,omitempty"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	UsedBy    *uint64    `json:"used_by,omitempty"`
}

func (Invite) TableName() string { return "invites" }

// Setting is a system setting an admin can change; value is JSON-encoded text.
type Setting struct {
	Key       string    `gorm:"primaryKey" json:"key"`
	Value     string    `gorm:"not null" json:"value"`
	UpdatedBy *uint64   `json:"updated_by,omitempty"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (Setting) TableName() string { return "settings" }

// Preset is a set of scheduling parameters, attached to a deck; several decks may share one.
type Preset struct {
	ID                  uint64  `gorm:"primaryKey" json:"-"`
	PublicID            string  `gorm:"column:public_id;uniqueIndex" json:"id"`
	OwnerUserID         uint64  `gorm:"not null;index" json:"owner_user_id"`
	Name                string  `gorm:"not null" json:"name"`
	DesiredRetention    float64 `gorm:"not null;default:0.9" json:"desired_retention"`
	LearningSteps       string  `gorm:"not null" json:"learning_steps"`
	RelearningSteps     string  `gorm:"not null" json:"relearning_steps"`
	MaximumIntervalDays int     `gorm:"not null;default:36500" json:"maximum_interval_days"`
	// EnableFuzz has a database default of true, so per AGENTS.md §2.3 item 9 it is a *bool:
	// nil means unspecified (stored via the database default true), non-nil is written explicitly — including an explicit false.
	// For the effective value use FuzzEnabled(), do not dereference directly.
	EnableFuzz         *bool      `gorm:"not null;default:true" json:"enable_fuzz"`
	WeightsJSON        *string    `gorm:"column:weights_json" json:"weights_json,omitempty"`
	WeightsOptimizedAt *time.Time `json:"weights_optimized_at,omitempty"`
	WeightsReviewCount *int       `json:"weights_review_count,omitempty"`
	// GradeMappingJSON is the 'score → grade' mapping for machine grading (JSON).
	// NULL means the built-in default mapping (all correct Good / partly correct Hard / all wrong Again).
	// Nullable TEXT with no database default: the default behavior is supplied by answer.DefaultGradeMapping on the Go side.
	GradeMappingJSON *string   `gorm:"column:grade_mapping_json" json:"grade_mapping_json,omitempty"`
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt        time.Time `gorm:"not null" json:"updated_at"`
}

func (Preset) TableName() string { return "presets" }

// Deck is a flat deck (no deck tree).
// PresetID / NewPerDay / ReviewsPerDay are **the owner's own** study settings (scheduling preset and daily caps, 0 means unlimited);
// other members' settings live in deck_member_settings and are resolved per user through StudySettings (see deck_member_settings.go).
// Both cap columns are integers with database defaults, so a zero value is omitted by GORM and filled in by the database default;
// to set a cap to 0 explicitly, go through DeckStore.SetCaps (a map update writes 0).
type Deck struct {
	ID          uint64 `gorm:"primaryKey" json:"-"`
	PublicID    string `gorm:"column:public_id;uniqueIndex" json:"id"`
	OwnerUserID uint64 `gorm:"not null;index" json:"owner_user_id"`
	Name        string `gorm:"not null" json:"name"`
	Description string `gorm:"not null" json:"description"`
	// DescriptionMarkdown selects how Description is read: false shows it as HTML, true renders it as Markdown.
	// The column carries the database default false and is a pointer for the same reason as Autoplay: a non-pointer bool
	// with a default tag would silently drop an explicit false on every write.
	DescriptionMarkdown *bool  `gorm:"column:description_markdown;not null;default:false" json:"description_markdown"`
	NewPerDay           int    `gorm:"not null;default:20" json:"new_per_day"`
	ReviewsPerDay       int    `gorm:"not null;default:200" json:"reviews_per_day"`
	PresetID            uint64 `gorm:"not null;index" json:"preset_id"`
	// Autoplay controls whether a card face starts its audio tags without a click. It is a deck-wide presentation
	// setting, so the column carries the database default true and is a pointer: a non-pointer bool with a default tag
	// would silently drop an explicit false on every write.
	Autoplay  *bool     `gorm:"not null;default:true" json:"autoplay"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (Deck) TableName() string { return "decks" }

// AutoplayEnabled reports the deck's autoplay setting, treating a missing value as the column default.
// AutoMigrate adds the column to existing rows with the database default, so nil only appears before that pass.
func (d Deck) AutoplayEnabled() bool { return d.Autoplay == nil || *d.Autoplay }

// DescriptionIsMarkdown reports whether the deck description is Markdown rather than HTML, treating a missing value as the
// column default. AutoMigrate adds the column to existing rows with the database default, so nil only appears before that pass.
func (d Deck) DescriptionIsMarkdown() bool {
	return d.DescriptionMarkdown != nil && *d.DescriptionMarkdown
}

// Note describes \"a fact\" and holds content only, with no user progress at all.
type Note struct {
	ID       uint64 `gorm:"primaryKey" json:"-"`
	PublicID string `gorm:"column:public_id;uniqueIndex" json:"id"`
	DeckID   uint64 `gorm:"not null;uniqueIndex:idx_notes_deck_external_ref" json:"deck_id"`
	// NoteTypeID names the note type that defines this note's fields and templates, and its field ids are the keys of FieldsJSON.
	// It stays a pointer because a note may be built before its type is resolved, but the column is NOT NULL: migration 0013
	// gives every existing note a type and tightens it, and the write path rejects a note that names no type. The first
	// AutoMigrate pass of Sync cannot use this shape while the database is below that migration, so it substitutes a
	// nullable one (see preNoteTypeNotNullNote in migrate.go).
	NoteTypeID *uint64 `gorm:"column:note_type_id;not null;index" json:"note_type_id,omitempty"`
	// FieldsJSON maps a field id to that field's HTML value; internal/notetype.Config declares the ids.
	FieldsJSON string `gorm:"column:fields_json;not null" json:"fields_json"`
	TagsJSON   string `gorm:"column:tags_json;not null" json:"tags_json"`
	// SearchText is a derived column that note search matches against: the plain text of the note's searchable fields,
	// lowered (see NoteStore.prepareNoteFields). The single source of content stays FieldsJSON: every note write
	// recomputes this column, and no payload carries it. The column is nullable because AutoMigrate adds it to existing
	// tables, and migration 0011 fills in the existing rows.
	SearchText string `gorm:"column:search_text" json:"-"`
	// an idempotency key defined by the caller; NULLs do not participate in uniqueness (both databases allow several NULLs).
	ExternalRef   *string `gorm:"uniqueIndex:idx_notes_deck_external_ref" json:"external_ref,omitempty"`
	Source        *string `json:"source,omitempty"` // manual | api | import
	ReferenceRefs *string `gorm:"column:reference_refs" json:"reference_refs,omitempty"`
	// SourceURL is the optional origin link of the content, moved out of the field map by a later migration.
	SourceURL *string `gorm:"column:source_url" json:"source_url,omitempty"`
	// ExtraJSON is the remaining optional content that has no column of its own, stored as TEXT like every JSON column.
	ExtraJSON *string        `gorm:"column:extra_json" json:"extra_json,omitempty"`
	CreatedBy *uint64        `json:"created_by,omitempty"`
	CreatedAt time.Time      `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time      `gorm:"not null" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

func (Note) TableName() string { return "notes" }

// Card is an instance of a note in some presentation form; scheduling acts on cards.
type Card struct {
	ID       uint64 `gorm:"primaryKey" json:"-"`
	PublicID string `gorm:"column:public_id;uniqueIndex" json:"id"`
	NoteID   uint64 `gorm:"not null;index;uniqueIndex:idx_cards_note_template" json:"note_id"`
	Template string `gorm:"not null;uniqueIndex:idx_cards_note_template" json:"template"`
	// DeckPath is the Anki subdeck path relative to the imported root deck, for example "Lesson 1::Verbs";
	// NULL means the card sits directly in the note's deck. Subdecks exist only as this path, never as deck rows.
	DeckPath  *string        `gorm:"column:deck_path" json:"deck_path,omitempty"`
	Ordinal   int            `gorm:"not null;default:0" json:"ordinal"`
	CreatedAt time.Time      `gorm:"not null" json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

func (Card) TableName() string { return "cards" }

// NoteType is the deck-scoped definition of a note's fields and card templates.
//
// ConfigJSON holds internal/notetype.Config as JSON text and is the single source of the note type's shape.
// The choice of a TEXT column over a gorm serializer: JSON must stay TEXT on both databases (no jsonb), and a string column
// serializes identically under SQLite and PostgreSQL with no driver-specific behaviour. The store marshals and unmarshals it
// (see NoteTypeStore); code outside the store works with notetype.Config through Config and SetConfig.
type NoteType struct {
	ID         uint64 `gorm:"primaryKey" json:"-"`
	PublicID   string `gorm:"column:public_id;uniqueIndex" json:"id"`
	DeckID     uint64 `gorm:"not null;index" json:"deck_id"`
	Name       string `gorm:"not null" json:"name"`
	ConfigJSON string `gorm:"column:config_json;not null" json:"config_json"`
	// Version is the optimistic lock: every update names the version it read and increments it, so a stale writer is rejected
	// instead of overwriting a newer edit.
	Version   int       `gorm:"not null;default:0" json:"version"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (NoteType) TableName() string { return "note_types" }

// MediaName maps a deck-scoped file name to content-addressed media, the way Anki refers to media: by file name.
//
// (deck_id, name) is the primary key, so the uniqueness is enforced by the schema, not only by the store. name is stored
// NFC-normalised: the same file name written with a composed or a decomposed character must resolve to one row.
type MediaName struct {
	DeckID uint64 `gorm:"primaryKey;uniqueIndex:idx_media_names_deck_name,priority:1" json:"deck_id"`
	Name   string `gorm:"primaryKey;uniqueIndex:idx_media_names_deck_name,priority:2" json:"name"`
	Sha256 string `gorm:"column:sha256;not null;index" json:"sha256"`
}

func (MediaName) TableName() string { return "media_names" }

// MediaNoteType records that a note type's templates or CSS refer to a media file name.
//
// It is the reference set behind read access checks and media garbage collection: media named here cannot be reclaimed
// while the note type exists. media_name is NFC-normalised like MediaName.Name. The pair is the primary key, so a
// replaced set can never hold duplicates.
type MediaNoteType struct {
	NoteTypeID uint64 `gorm:"column:note_type_id;primaryKey;uniqueIndex:idx_media_note_types_pair,priority:1" json:"note_type_id"`
	MediaName  string `gorm:"column:media_name;primaryKey;uniqueIndex:idx_media_note_types_pair,priority:2" json:"media_name"`
}

func (MediaNoteType) TableName() string { return "media_note_types" }

// CardState is one user's FSRS state for one card; the primary key (card_id, user_id) is the cornerstone of shared decks.
type CardState struct {
	CardID        uint64     `gorm:"primaryKey" json:"card_id"`
	UserID        uint64     `gorm:"primaryKey;index:idx_states_user_due,priority:1" json:"user_id"`
	State         string     `gorm:"not null" json:"state"` // new | learning | review | relearning
	DueAt         *time.Time `gorm:"index:idx_states_user_due,priority:2" json:"due_at,omitempty"`
	StepIndex     int        `gorm:"not null;default:0" json:"step_index"`
	Stability     *float64   `json:"stability,omitempty"`
	Difficulty    *float64   `json:"difficulty,omitempty"`
	Reps          int        `gorm:"not null;default:0" json:"reps"`
	Lapses        int        `gorm:"not null;default:0" json:"lapses"`
	ScheduledDays int        `gorm:"not null;default:0" json:"scheduled_days"`
	ElapsedDays   int        `gorm:"not null;default:0" json:"elapsed_days"`
	LastReviewAt  *time.Time `json:"last_review_at,omitempty"`
	Version       int        `gorm:"not null;default:0" json:"version"` // optimistic lock
	// SuspendedAt non-null means this user suspended this card: it leaves that user's queue and stats, and other users are unaffected.
	// Suspension is each user's own study decision, so it lives on the progress row rather than on the card.
	SuspendedAt *time.Time `gorm:"column:suspended_at" json:"suspended_at,omitempty"`
}

func (CardState) TableName() string { return "card_states" }

// Review is an append-only review log, the only fuel for parameter optimization; every field is written from day one.
type Review struct {
	ID              uint64    `gorm:"primaryKey" json:"-"`
	PublicID        string    `gorm:"column:public_id;uniqueIndex" json:"id"`
	CardID          uint64    `gorm:"not null;index:idx_reviews_card,priority:1" json:"card_id"`
	UserID          uint64    `gorm:"not null;index:idx_reviews_user_day,priority:1" json:"user_id"`
	Rating          int       `gorm:"not null" json:"rating"`       // 1=Again 2=Hard 3=Good 4=Easy
	GradeSource     string    `gorm:"not null" json:"grade_source"` // self | typed | llm
	GradeDetailJSON *string   `gorm:"column:grade_detail_json" json:"grade_detail_json,omitempty"`
	ReviewedAt      time.Time `gorm:"not null;index:idx_reviews_card,priority:2" json:"reviewed_at"`
	ReviewDay       string    `gorm:"not null;index:idx_reviews_user_day,priority:2" json:"review_day"`
	ElapsedMS       *int      `gorm:"column:elapsed_ms" json:"elapsed_ms,omitempty"`
	DurationDays    *float64  `json:"duration_days,omitempty"`
	StateBefore     int       `gorm:"not null" json:"state_before"` // 0=New 1=Learning 2=Review 3=Relearning
	// StepIndexBefore is a snapshot of card_states.step_index before this rating (the number of remaining learning steps).
	// The FSRS learning-step cursor before the rating has no other source, and fsrs.Rollback zeroes step_index;
	// storing it lets Undo restore step progress exactly.
	// Nullable: old rows have no snapshot, and Undo falls back to the zeroing behavior on NULL. An integer, not a boolean with a default (AGENTS.md §2.3 item 9).
	StepIndexBefore *int `gorm:"column:step_index_before" json:"step_index_before,omitempty"`
	// DueBefore is a snapshot of card_states.due_at before this rating.
	//
	// The due date before the rating cannot be derived from the previous log entry: burying only postpones due_at and writes no log entry, so 'the previous
	// entry's due date' and 'the due date before this rating' disagree, and an undo that rebuilt only from the previous entry would turn the buried
	// due date back into NULL or a wrong value. Nullable: a brand-new card has no due date before its first rating (NULL), and old rows have no snapshot either,
	// so undo falls back to deriving it from the previous entry on NULL.
	DueBefore    *time.Time `gorm:"column:due_before" json:"due_before,omitempty"`
	IntervalDays *float64   `json:"interval_days,omitempty"`
	Stability    *float64   `json:"stability,omitempty"`
	Difficulty   *float64   `json:"difficulty,omitempty"`
}

func (Review) TableName() string { return "reviews" }

// DeckGrant is a deck grant; the role set is deliberately only three.
type DeckGrant struct {
	DeckID    uint64    `gorm:"primaryKey" json:"deck_id"`
	UserID    uint64    `gorm:"primaryKey" json:"user_id"`
	Role      string    `gorm:"not null" json:"role"` // owner | editor | reader
	CreatedBy *uint64   `json:"created_by,omitempty"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (DeckGrant) TableName() string { return "deck_grants" }

// Media stores metadata only; the bytes live on the local filesystem, with the path determined by sha256.
// Primary key = content sha256: the external identifier is the hash itself, and there is no auto-increment id any more.
type Media struct {
	Sha256    string    `gorm:"primaryKey" json:"sha256"`
	RelPath   string    `gorm:"not null" json:"rel_path"`
	Mime      string    `gorm:"not null" json:"mime"`
	Bytes     int64     `gorm:"not null" json:"bytes"`
	Width     *int      `json:"width,omitempty"`
	Height    *int      `json:"height,omitempty"`
	CreatedBy *uint64   `json:"created_by,omitempty"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	// OrphanedAt is the moment media reclamation first found 'no note references this'; NULL while it is referenced.
	// Reclamation only deletes media orphaned past the grace period, leaving a window for 'deleted a reference by mistake, then put it back' (see MarkOrphanedMedia).
	OrphanedAt *time.Time `gorm:"column:orphaned_at;index" json:"-"`
}

func (Media) TableName() string { return "media" }

// MediaNote is the explicit media ↔ note reference mapping (settled -10-06).
//
// It is a **derived index**: the single source of content is always notes.fields_json, and the mapping is
// **rebuilt** by the note write path after every write (delete old, insert new, see NoteStore). Read authorization uses it to decide 'is this media referenced
// by a non-soft-deleted note in a deck I can read', and no longer scans fields_json.
//
// Why there is no 'who created the reference' column: unauthorized references are stopped by a single write-side check (evaluated against the pre-write state),
// provided every note write path converges on the same method.
type MediaNote struct {
	MediaSha string `gorm:"column:media_sha;not null;uniqueIndex:idx_media_notes_media_note,priority:1" json:"media_sha"`
	NoteID   uint64 `gorm:"column:note_id;not null;uniqueIndex:idx_media_notes_media_note,priority:2" json:"note_id"`
	// CreatedAt is when this mapping was created; a rebuild replaces the whole row, so it equals the most recent write time.
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (MediaNote) TableName() string { return "media_notes" }

// MediaUploader records 'who supplied these bytes' (settled -10-06).
//
// Why not a media.created_by column: bytes are deduped by sha256, with one row per blob instance-wide, so a single column could only record
// the first uploader; then when 'bytes B uploaded happen to equal what C already had (a dedup hit)', B gets no attribution — once A revokes
// the share, B cannot read the file B itself supplied. Recording every provider many-to-many is what makes 'can supply the bytes ⇒ can read' hold under dedup too.
//
// Upload and import (every path that supplies bytes here) write idempotently by (media_sha, user_id) (clause.OnConflict
// ignores duplicates). **It cannot be undone after the write**: deleting would make 'supplied it' untrue.
type MediaUploader struct {
	MediaSha  string    `gorm:"primaryKey;column:media_sha" json:"media_sha"`
	UserID    uint64    `gorm:"primaryKey;column:user_id" json:"user_id"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (MediaUploader) TableName() string { return "media_uploaders" }

// APIKey is a user-level credential; the plaintext is returned only once at creation and the DB stores only the sha256.
type APIKey struct {
	ID       uint64 `gorm:"primaryKey;column:id" json:"-"`
	PublicID string `gorm:"column:public_id;uniqueIndex" json:"id"`
	// UserID is an internal foreign key: deck/key listings are filtered by caller and the client does not need it, so it is not in the JSON.
	UserID     uint64     `gorm:"not null;index" json:"-"`
	Name       string     `gorm:"not null" json:"name"`
	Prefix     string     `gorm:"not null" json:"prefix"`
	KeyHash    string     `gorm:"column:key_hash;not null;uniqueIndex" json:"-"`
	Scopes     string     `gorm:"not null" json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `gorm:"not null" json:"created_at"`
}

func (APIKey) TableName() string { return "api_keys" }

// Job is a long-running background task (currently only parameter optimization); triggered by the web, executed by a subprocess, polled by the page.
type Job struct {
	ID         uint64     `gorm:"primaryKey" json:"-"`
	PublicID   string     `gorm:"column:public_id;uniqueIndex" json:"id"`
	Kind       string     `gorm:"not null" json:"kind"`
	TargetID   *uint64    `json:"target_id,omitempty"`
	Status     string     `gorm:"not null" json:"status"` // queued | running | succeeded | failed
	Stage      *string    `json:"stage,omitempty"`
	LogTail    *string    `gorm:"column:log_tail" json:"log_tail,omitempty"`
	ResultJSON *string    `gorm:"column:result_json" json:"result_json,omitempty"`
	Error      *string    `json:"error,omitempty"`
	CreatedAt  time.Time  `gorm:"not null" json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (Job) TableName() string { return "jobs" }

// AuditLog records who changed what, and when.
type AuditLog struct {
	ID         uint64    `gorm:"primaryKey" json:"-"`
	PublicID   string    `gorm:"column:public_id;uniqueIndex" json:"id"`
	UserID     *uint64   `json:"user_id,omitempty"`
	APIKeyID   *uint64   `gorm:"column:api_key_id" json:"api_key_id,omitempty"`
	Action     string    `gorm:"not null" json:"action"`
	TargetType *string   `gorm:"column:target_type" json:"target_type,omitempty"`
	TargetID   *uint64   `gorm:"column:target_id" json:"target_id,omitempty"`
	DetailJSON *string   `gorm:"column:detail_json" json:"detail_json,omitempty"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
}

func (AuditLog) TableName() string { return "audit_log" }

// SchemaVersion is a single-row table (id is always 1) recording the applied destructive migration version.
// The table name is singular, matching that wording.
type SchemaVersion struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Version   int       `gorm:"not null" json:"version"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (SchemaVersion) TableName() string { return "schema_version" }

// User and account status values; string defaults are supplied explicitly by the store layer on the Go side (see the package comment).
const (
	RoleAdmin      = "admin"
	RoleUser       = "user"
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Session is a server-side session record: the cookie holds only an unreadable session ID and a signature, and revocation is governed by the row state here.
// Keeping it in the DB rather than in a purely stateless cookie is what lets logout, password change and disable all really \"revoke server-side\".
// csrf_token binds to the session, for the CSRF middleware to verify.
type Session struct {
	ID         string     `gorm:"primaryKey;column:id" json:"id"`
	UserID     uint64     `gorm:"not null;index" json:"user_id"`
	CSRFToken  string     `gorm:"column:csrf_token;not null" json:"-"`
	CreatedAt  time.Time  `gorm:"not null" json:"created_at"`
	ExpiresAt  time.Time  `gorm:"not null;index" json:"expires_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func (Session) TableName() string { return "sessions" }

// AllModels is the single list of **all** GORM models; AutoMigrate reads it, and the tests' table-list assertions check against it too.
// Model definitions may live in separate files (for example the two TOTP tables in totp.go), but **a new model must be registered here**:
// forgetting it does not error, it silently leaves AutoMigrate one table short — exposed only when a query hits the missing table.
func AllModels() []any {
	return []any{
		&User{}, &Identity{}, &Invite{}, &Setting{}, &Preset{}, &Deck{}, &Note{}, &Card{},
		&CardState{}, &Review{}, &DeckGrant{}, &Media{}, &APIKey{}, &Job{},
		&MediaNote{}, &MediaUploader{},
		// Note types, deck-scoped media names and the note type's media references (M17 data model).
		&NoteType{}, &MediaName{}, &MediaNoteType{},
		&AuditLog{}, &SchemaVersion{}, &Session{},
		// TOTP: model defined in totp.go, but it must appear here (see the comment above).
		&UserTOTP{}, &TOTPRecoveryCode{},
		// SMTP: the outbox queue table, model defined in outbox.go.
		&OutboxMessage{},
		&EmailPref{},
		&ReminderLog{},
		// Send ledger for the weekly study digest: model defined in digest.go.
		&DigestLog{},
		&ActionToken{}, &LoginFingerprint{}, // A-class security mail: one-time tokens and login fingerprints.
		// Admin-defined mail templates: when missing, fall back to the built-in body.
		&MailTemplate{},
		// L3 share consent model: pending invites and the receive whitelist.
		&DeckShareInvite{}, &ShareAllow{},
		// Shared deck members' own study settings: model defined in deck_member_settings.go.
		&DeckMemberSetting{},
		// New-card quota appended per review day by 'one more round': model defined in deck_daily_extra.go.
		&DeckDailyExtra{},
		// Decks a user paused for themselves: model defined in deck_pause.go.
		&DeckPause{},
	}
}

// MailTemplate is an admin-defined mail template.
//
// The unique key is (type, locale): one per language of each type. **No row in the table is a normal state** — it means that type
// in that language uses the built-in body (the one the sender assembles), so 'deleting the customization' is deleting this row, and the built-in default can never
// be lost. The body stores HTML template text rather than the rendered result: variable substitution and the shell both happen at send time, so a template
// change only affects mail enqueued afterwards. Migration 0014 converted the previously stored Markdown bodies to this form.
type MailTemplate struct {
	Type      string    `gorm:"primaryKey;size:64" json:"type"`
	Locale    string    `gorm:"primaryKey;size:16" json:"locale"`
	Subject   string    `gorm:"not null" json:"subject"`
	BodyMD    string    `gorm:"not null" json:"body_md"`
	UpdatedBy *uint64   `json:"updated_by,omitempty"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (MailTemplate) TableName() string { return "mail_templates" }

// DeckShareInvite is a deck share invite that has been 'sent but not yet accepted'.
//
// How the consent model lands: **grants (deck_grants) are written only when the invitee accepts**, and the visibility set predicate reads exactly
// deck_grants — so a pending invite enters no visibility set, and the queue, stats, history and media authorization all need no change.
// That is the only reason for choosing 'a new table' over 'adding a status column to deck_grants'.
//
// There is one row per (deck, user): inviting again refreshes that row's role and expiry instead of stacking.
type DeckShareInvite struct {
	DeckID    uint64    `gorm:"primaryKey" json:"deck_id"`
	UserID    uint64    `gorm:"primaryKey" json:"user_id"`
	Role      string    `gorm:"not null" json:"role"`
	InvitedBy uint64    `gorm:"not null" json:"invited_by"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	// The invite is void after ExpiresAt (expired rows are reclaimed hourly by internal/retention).
	ExpiresAt time.Time `gorm:"not null;index" json:"expires_at"`
}

func (DeckShareInvite) TableName() string { return "deck_share_invites" }

// ShareAcceptPolicy is a user's answer to 'may others share decks with me'.
//
// Three tiers rather than 'a whitelist boolean': refusing everyone is the most common wish (not wanting to be pulled in by anyone), and expressing it as
// 'the whitelist is empty' would conflate it with 'the whitelist is not configured yet'.
type ShareAcceptPolicy string

const (
	// ShareAcceptAnyone is the default: anyone may invite me.
	ShareAcceptAnyone ShareAcceptPolicy = "anyone"
	// ShareAcceptWhitelist only accepts the people listed in share_allow.
	ShareAcceptWhitelist ShareAcceptPolicy = "whitelist"
	// ShareAcceptNobody refuses every share invite.
	ShareAcceptNobody ShareAcceptPolicy = "nobody"
)

// ShareAllow is one 'from may share a deck with to' permission row (configured by to themselves).
type ShareAllow struct {
	FromUserID uint64    `gorm:"primaryKey" json:"from_user_id"`
	ToUserID   uint64    `gorm:"primaryKey" json:"to_user_id"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
}

func (ShareAllow) TableName() string { return "share_allow" }
