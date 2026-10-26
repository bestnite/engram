package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Migration is a one-off explicit migration for destructive changes AutoMigrate cannot perform
// (changing a column type, dropping a column, adding a non-null constraint, AGENTS.md §2.3 item 5).
// Name is a required stable English identifier: an unnamed destructive change cannot be tracked and must be rejected.
type Migration struct {
	Name string
	Up   func(tx *gorm.DB) error
}

// BuiltinMigrations is the ordered migration list; the order is the version (the i-th entry is version i+1).
// A new migration may only be appended at the end; never modify or reorder published entries.
var BuiltinMigrations = []Migration{
	{
		// The media primary key moves from an auto-increment id to the content sha256: dropping a column is outside
		// AutoMigrate's additive scope and needs an explicit migration. Only the primary key changes here; old references are not migrated.
		Name: "0001_media_primary_key_sha256",
		Up:   mediaPrimaryKeySha256,
	},
	{
		Name: "0002_nullable_day_cutoff",
		Up:   nullableDayCutoff,
	},
	{
		// Deck visibility (private / unlisted / public) is removed wholesale: a deck is visible only to its owner and to explicit grantees.
		// Dropping a column is outside AutoMigrate's additive scope and needs an explicit migration. Existing public/unlisted rows lose
		// that implicit visibility along with it — which is exactly the point of this change, so no data is rewritten, only the column dropped.
		Name: "0003_drop_deck_visibility",
		Up:   dropDeckVisibility,
	},
	{
		// Public ids move from the auto-increment primary key to public_id: backfill UUIDv7 for existing rows.
		// The column itself is added by AutoMigrate (nullable + unique index); this only fills in the values.
		// Neither SQLite nor PostgreSQL has a SQL function that generates UUIDv7, so they are generated row by row on the Go side.
		Name: "0004_backfill_public_ids",
		Up:   backfillPublicIDs,
	},
	{
		// The media_notes mapping table shipped without backfilling existing notes: on upgraded instances, media referenced by old notes
		// cannot be found in the mapping, so shared-deck members cannot read those images, and media reclamation treats them as unreferenced.
		// This scans every note (soft-deleted ones included, since they can be restored) and fills in the mapping, adding only.
		Name: "0005_backfill_media_notes",
		Up:   backfillMediaNotes,
	},
	{
		// Shared-deck members now use their own study settings: backfill deck_member_settings rows for existing grants.
		// The preset is the member's own default preset (that is the point of this change: no longer inheriting the owner's); the daily caps
		// keep the deck's current values, so a member sees the same daily card count after upgrading, and can change it later in deck settings.
		Name: "0006_backfill_member_settings",
		Up:   backfillMemberSettings,
	},
	{
		// Suspension moves from the card level (affecting everyone) to per user (card_states.suspended_at).
		// Existing card-level suspensions are migrated only to the deck owner: suspension was only ever possible for the owner, so it was the owner's decision;
		// shared members decide for themselves from now on and no longer inherit. After the migration cards.suspended_at is dropped, leaving no second source.
		Name: "0007_per_user_suspension",
		Up:   perUserSuspension,
	},
	{
		// Share links are removed outright, so the two tables that backed them are dropped rather than left behind empty.
		// AutoMigrate only adds tables, so dropping them needs an explicit versioned migration; DROP TABLE IF EXISTS keeps it safe on a fresh
		// install where AutoMigrate never created them and on a second run.
		Name: "0008_drop_share_link_tables",
		Up:   dropLegacyShareTables,
	},
	{
		// Import used to copy the exporter's preset name verbatim, so every import of a deck on the default preset added another preset called
		// "Default". The name is the default preset's identity: each duplicate was presented as a default preset by every reader that checks the
		// name, and the delete protection refuses that name, so the user could not remove any of them.
		// This renames the duplicates; it never deletes a row (see uniqueDefaultPresetNames).
		Name: "0009_unique_default_preset_names",
		Up:   uniqueDefaultPresetNames,
	},
}

// uniqueDefaultPresetNames renames duplicate presets named DefaultPresetName, keeping one per owner.
//
// The oldest row of each owner keeps the name: it is the account's own default preset, created before any import, while every later row with that
// name is a copy left by an import. The later rows are renamed, not deleted: decks and deck_member_settings still point at them by id, so deleting
// would either orphan those references or force a repoint that changes a user's study settings. A renamed row stays attached to the decks that
// already use it, and the user can delete it afterwards if those decks are repointed.
//
// Each rename takes the first free "Imported (N)" name for that owner, N starting at 2, considering names renamed earlier in the same run, so no
// owner is left with two presets that share a name. Running the migration again finds no owner with more than one Default and renames nothing.
func uniqueDefaultPresetNames(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("presets") {
		return nil
	}
	var owners []uint64
	if err := tx.Model(&Preset{}).Where("name = ?", DefaultPresetName).
		Group("owner_user_id").Having("COUNT(*) > 1").
		Pluck("owner_user_id", &owners).Error; err != nil {
		return fmt.Errorf("unique default preset names: list owners with duplicate defaults: %w", err)
	}
	now := time.Now().UTC()
	for _, owner := range owners {
		var rows []Preset
		if err := tx.Select("id", "name", "created_at").
			Where("owner_user_id = ?", owner).
			Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
			return fmt.Errorf("unique default preset names: list presets of owner %d: %w", owner, err)
		}
		taken := make(map[string]bool, len(rows))
		defaults := make([]Preset, 0, 2)
		for _, p := range rows {
			taken[p.Name] = true
			if p.Name == DefaultPresetName {
				defaults = append(defaults, p)
			}
		}
		if len(defaults) < 2 {
			continue
		}
		for _, p := range defaults[1:] {
			name := nextFreeSuffixedName("Imported", taken)
			if err := tx.Model(&Preset{}).Where("id = ?", p.ID).
				Updates(map[string]any{"name": name, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("unique default preset names: rename preset %d: %w", p.ID, err)
			}
			taken[name] = true
		}
	}
	return nil
}

// dropLegacyShareTables drops the two tables of the removed anonymous share-link transport.
// Both databases accept DROP TABLE IF EXISTS, so a fresh database (no such tables) and an upgrade take the same path.
func dropLegacyShareTables(tx *gorm.DB) error {
	switch tx.Dialector.Name() {
	case "sqlite", "postgres":
		for _, table := range []string{"share_links", "share_session_decks"} {
			if err := tx.Exec("DROP TABLE IF EXISTS " + table).Error; err != nil {
				return fmt.Errorf("drop share link tables: drop %s: %w", table, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("drop share link tables: unsupported dialect %q", tx.Dialector.Name())
	}
}

// perUserSuspension moves cards.suspended_at onto the owner's card_states row, then drops that column.
// Cards the owner has no state row for get a new card state row; existing rows only get suspended_at written, with progress untouched.
func perUserSuspension(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("cards") || !m.HasColumn("cards", "suspended_at") {
		return nil
	}
	var rows []struct {
		CardID      uint64
		OwnerID     uint64
		SuspendedAt time.Time
	}
	if err := tx.Table("cards AS c").
		Select("c.id AS card_id, d.owner_user_id AS owner_id, c.suspended_at AS suspended_at").
		Joins("JOIN notes AS n ON n.id = c.note_id").
		Joins("JOIN decks AS d ON d.id = n.deck_id").
		Where("c.suspended_at IS NOT NULL").
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("per-user suspension: list suspended cards: %w", err)
	}
	for _, r := range rows {
		at := r.SuspendedAt.UTC()
		state := CardState{CardID: r.CardID, UserID: r.OwnerID, State: "new", SuspendedAt: &at}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "card_id"}, {Name: "user_id"}},
			DoUpdates: clause.Assignments(map[string]any{"suspended_at": at}),
		}).Create(&state).Error; err != nil {
			return fmt.Errorf("per-user suspension: card %d: %w", r.CardID, err)
		}
	}
	switch tx.Dialector.Name() {
	case "sqlite":
		return tx.Exec("ALTER TABLE cards DROP COLUMN suspended_at").Error
	case "postgres":
		return tx.Exec("ALTER TABLE cards DROP COLUMN IF EXISTS suspended_at").Error
	default:
		return fmt.Errorf("per-user suspension: unsupported dialect %q", tx.Dialector.Name())
	}
}

// backfillMemberSettings adds one member settings row per grant; existing rows (OnConflict) are left alone.
func backfillMemberSettings(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("deck_grants") || !m.HasTable("deck_member_settings") {
		return nil
	}
	var grants []struct {
		DeckID        uint64
		UserID        uint64
		NewPerDay     int
		ReviewsPerDay int
	}
	if err := tx.Table("deck_grants AS g").
		Select("g.deck_id, g.user_id, d.new_per_day, d.reviews_per_day").
		Joins("JOIN decks AS d ON d.id = g.deck_id").
		Where("d.owner_user_id <> g.user_id").
		Scan(&grants).Error; err != nil {
		return fmt.Errorf("backfill member settings: list grants: %w", err)
	}
	ctx := tx.Statement.Context
	presetByUser := map[uint64]uint64{}
	now := time.Now().UTC()
	for _, g := range grants {
		presetID, ok := presetByUser[g.UserID]
		if !ok {
			initial, err := defaultStudySettings(ctx, tx, g.UserID)
			if err != nil {
				return fmt.Errorf("backfill member settings: user %d: %w", g.UserID, err)
			}
			presetID = initial.PresetID
			presetByUser[g.UserID] = presetID
		}
		row := DeckMemberSetting{DeckID: g.DeckID, UserID: g.UserID, PresetID: presetID,
			NewPerDay: g.NewPerDay, ReviewsPerDay: g.ReviewsPerDay, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return fmt.Errorf("backfill member settings: deck %d user %d: %w", g.DeckID, g.UserID, err)
		}
	}
	return nil
}

// backfillMediaNotes fills in media_notes from the note fields; existing mapping rows are ignored by OnConflict.
// Rows are read in primary-key batches, so memory use is independent of the note count; notes whose fields fail to parse are skipped (same rule as quota accounting).
func backfillMediaNotes(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("notes") || !m.HasTable("media_notes") {
		return nil
	}
	const batchSize = 500
	var last uint64
	now := time.Now().UTC()
	for {
		var batch []Note
		if err := tx.Unscoped().Select("id", "fields_json").Where("id > ?", last).
			Order("id ASC").Limit(batchSize).Find(&batch).Error; err != nil {
			return fmt.Errorf("backfill media notes: scan notes after id %d: %w", last, err)
		}
		if len(batch) == 0 {
			return nil
		}
		var rows []MediaNote
		for _, n := range batch {
			last = n.ID
			fields, err := ParseFields(n.FieldsJSON)
			if err != nil {
				continue
			}
			for sha := range mediaRefsIn(fields) {
				rows = append(rows, MediaNote{MediaSha: sha, NoteID: n.ID, CreatedAt: now})
			}
		}
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&rows, batchSize).Error; err != nil {
				return fmt.Errorf("backfill media notes: insert rows up to note %d: %w", last, err)
			}
		}
	}
}

// dropDeckVisibility drops the decks.visibility column.
//
// Both databases use a plain DROP COLUMN: the column is not in the primary key and carries no index or constraint, so SQLite (≥3.35) and PostgreSQL
// both support dropping it in place, with no need to rebuild the table the way 0001 does. A DB already in the new shape (AutoMigrate builds decks
// without that column) is skipped here.
func dropDeckVisibility(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("decks") || !m.HasColumn("decks", "visibility") {
		return nil
	}
	var stmt string
	switch tx.Dialector.Name() {
	case "sqlite":
		stmt = "ALTER TABLE decks DROP COLUMN visibility"
	case "postgres":
		stmt = "ALTER TABLE decks DROP COLUMN IF EXISTS visibility"
	default:
		return fmt.Errorf("drop deck visibility migration: unsupported dialect %q", tx.Dialector.Name())
	}
	return tx.Exec(stmt).Error
}

// publicIDTables are the tables that need public_id backfilled, one to one with externally visible entities.
// Tables not exposed externally (sessions, outbox, etc.) are not listed here and carry no public_id column.
// Table names come only from this fixed list and contain no external input, so they are safe to interpolate into SQL.
var publicIDTables = []string{
	"users", "identities", "invites", "presets", "decks",
	"notes", "cards", "reviews", "api_keys", "jobs", "audit_log",
}

// backfillPublicIDs gives a UUIDv7 to every row with an empty public_id in each target table.
// Updating row by row is deliberate: neither database can generate v7 in SQL, and the row counts are not large enough for that to be a bottleneck.
func backfillPublicIDs(tx *gorm.DB) error {
	m := tx.Migrator()
	for _, table := range publicIDTables {
		if !m.HasTable(table) || !m.HasColumn(table, "public_id") {
			continue
		}
		var ids []uint64
		// Rows are fetched with Table (not the model) to bypass the soft-delete scope: soft-deleted notes/cards need ids too.
		if err := tx.Table(table).Where("public_id IS NULL OR public_id = ''").Pluck("id", &ids).Error; err != nil {
			return fmt.Errorf("backfill public ids: scan %s: %w", table, err)
		}
		for _, id := range ids {
			if err := tx.Exec("UPDATE "+table+" SET public_id = ? WHERE id = ?", NewPublicID(), id).Error; err != nil {
				return fmt.Errorf("backfill public ids: update %s id=%d: %w", table, id, err)
			}
		}
	}
	return nil
}

// mediaPrimaryKeySha256 moves the media primary key from an auto-increment id to the content sha256.
//
// The two databases differ: SQLite does not allow DROP COLUMN on a primary key column and can only rebuild the table; PostgreSQL
// does a plain DROP COLUMN (removing the old primary key constraint along with it) and then adds the primary key. A new DB already in the
// target shape (AutoMigrate creates the sha primary key directly, with no id column) is skipped here.
func mediaPrimaryKeySha256(tx *gorm.DB) error {
	m := tx.Migrator()
	if !m.HasTable("media") {
		return nil
	}
	// no id column means the table is already in the sha primary key shape, so there is nothing to do.
	if !m.HasColumn("media", "id") {
		return nil
	}
	switch tx.Dialector.Name() {
	case "sqlite":
		return rebuildMediaForShaPrimaryKey(tx)
	case "postgres":
		return alterMediaForShaPrimaryKey(tx)
	default:
		return fmt.Errorf("media primary key migration: unsupported dialect %q", tx.Dialector.Name())
	}
}

// rebuildMediaForShaPrimaryKey rebuilds media with 'create new table → copy data → rename', sidestepping SQLite's
// inability to drop a primary key column. The column definitions match the shape GORM generates for that model (in particular created_at is datetime:
// writing it as TEXT would stop the driver scanning it back into time.Time). The column list matches the media model.
func rebuildMediaForShaPrimaryKey(tx *gorm.DB) error {
	stmts := []string{
		`CREATE TABLE media_sha_pk (
			sha256     text PRIMARY KEY,
			rel_path   text NOT NULL,
			mime       text NOT NULL,
			bytes      integer NOT NULL,
			width      integer,
			height     integer,
			created_by integer,
			created_at datetime NOT NULL
		)`,
		`INSERT INTO media_sha_pk (sha256, rel_path, mime, bytes, width, height, created_by, created_at)
			SELECT sha256, rel_path, mime, bytes, width, height, created_by, created_at FROM media`,
		`DROP TABLE media`,
		`ALTER TABLE media_sha_pk RENAME TO media`,
	}
	for _, stmt := range stmts {
		if err := tx.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// alterMediaForShaPrimaryKey changes the primary key in place on PostgreSQL: drop the id column (the old primary key constraint
// on it is dropped by PG together with the column), drop the sha256 unique index AutoMigrate created, then make sha256 the primary key.
func alterMediaForShaPrimaryKey(tx *gorm.DB) error {
	stmts := []string{
		`ALTER TABLE media DROP COLUMN IF EXISTS id`,
		`DROP INDEX IF EXISTS idx_media_sha256`,
		`ALTER TABLE media ADD PRIMARY KEY (sha256)`,
	}
	for _, stmt := range stmts {
		if err := tx.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// dayCutoffColumn is used only by explicit DDL; the domain still uses the same User model.
// The incremental-DDL-only shape masks this column so AutoMigrate does not remove NOT NULL/default before the versioned migration.
type dayCutoffColumn struct {
	DayCutoffHour *int `gorm:"column:day_cutoff_hour"`
}

func (dayCutoffColumn) TableName() string { return "users" }

// Used only in incremental DDL, not as a domain model: the remaining user fields still embed the single User definition directly.
type additiveUserSchema struct {
	User
	DayCutoffHour *int `gorm:"column:day_cutoff_hour;-:migration"`
}

func (additiveUserSchema) TableName() string { return "users" }

// nullableDayCutoff only relaxes the column constraint and converts no existing value: the old 0 now explicitly means midnight, and the old 4 stays as it is.
func nullableDayCutoff(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("users") || !tx.Migrator().HasColumn("users", "day_cutoff_hour") {
		return nil
	}
	switch tx.Dialector.Name() {
	case "sqlite":
		// the driver does not keep external indexes/triggers when rebuilding a table, so save them and restore them in the same transaction, so no unique constraint is lost.
		var objects []struct{ SQL string }
		if err := tx.Raw("SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL", "users").Scan(&objects).Error; err != nil {
			return err
		}
		if err := tx.Migrator().AlterColumn(&dayCutoffColumn{}, "DayCutoffHour"); err != nil {
			return err
		}
		for _, object := range objects {
			if err := tx.Exec(object.SQL).Error; err != nil {
				return err
			}
		}
		return nil
	case "postgres":
		if err := tx.Exec("ALTER TABLE users ALTER COLUMN day_cutoff_hour DROP NOT NULL").Error; err != nil {
			return err
		}
		return tx.Exec("ALTER TABLE users ALTER COLUMN day_cutoff_hour DROP DEFAULT").Error
	default:
		return fmt.Errorf("day cutoff migration: unsupported dialect %q", tx.Dialector.Name())
	}
}

// AutoMigrate makes only additive changes (add table/column/index).
// The model list exists in one place only: AllModels(). This used to merge the two TOTP tables in separately, and two lists necessarily diverge,
// so they were merged back into one.
func AutoMigrate(ctx context.Context, db *gorm.DB) error {
	models := AllModels()
	for i, model := range models {
		if _, ok := model.(*User); ok {
			models[i] = &additiveUserSchema{}
		}
	}
	if err := db.WithContext(ctx).AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	// Adding nullable columns on a new DB is additive; constraints on old columns can only be handled by the explicit migration 0002.
	if !db.Migrator().HasColumn("users", "day_cutoff_hour") {
		if err := db.WithContext(ctx).Migrator().AddColumn(&dayCutoffColumn{}, "DayCutoffHour"); err != nil {
			return fmt.Errorf("add nullable day cutoff: %w", err)
		}
	}
	return nil
}

// Sync first runs the additive AutoMigrate, then applies the registered destructive migrations by schema version, returning the number of migrations run this time.
func Sync(ctx context.Context, db *gorm.DB, migrations []Migration) (int, error) {
	if err := AutoMigrate(ctx, db); err != nil {
		return 0, err
	}
	applied, err := Apply(ctx, db, migrations)
	if err != nil || applied == 0 {
		return applied, err
	}
	// An explicit migration may have rebuilt a table (for example 0001 rebuilds media on SQLite from a fixed column list),
	// dropping the new columns the first AutoMigrate had just added; a second additive pass adds them back,
	// otherwise those columns would not exist until the next start.
	if err := AutoMigrate(ctx, db); err != nil {
		return applied, err
	}
	return applied, nil
}

// Apply validates the migration list and runs the entries not yet applied. Each migration commits together with its version number in one transaction,
// so a successful migration can never end up in the state of \"ran, but the version was not recorded\".
func Apply(ctx context.Context, db *gorm.DB, migrations []Migration) (int, error) {
	seen := make(map[string]int, len(migrations))
	for i, m := range migrations {
		if strings.TrimSpace(m.Name) == "" {
			return 0, fmt.Errorf("migration at index %d has no name: destructive changes must be named and registered", i)
		}
		if m.Up == nil {
			return 0, fmt.Errorf("migration %q has no Up function", m.Name)
		}
		if prev, dup := seen[m.Name]; dup {
			return 0, fmt.Errorf("duplicate migration name %q at index %d and %d", m.Name, prev, i)
		}
		seen[m.Name] = i
	}
	if err := db.WithContext(ctx).AutoMigrate(&SchemaVersion{}); err != nil {
		return 0, fmt.Errorf("auto migrate schema_version: %w", err)
	}
	current, err := CurrentVersion(ctx, db)
	if err != nil {
		return 0, err
	}
	applied := 0
	for i, m := range migrations {
		version := i + 1
		if version <= current {
			continue
		}
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := m.Up(tx); err != nil {
				return fmt.Errorf("migration %q failed: %w", m.Name, err)
			}
			return setVersion(tx, version)
		})
		if err != nil {
			return applied, err
		}
		current = version
		applied++
	}
	return applied, nil
}

// CurrentVersion returns the applied schema version; 0 when there is no record at all.
func CurrentVersion(ctx context.Context, db *gorm.DB) (int, error) {
	var row SchemaVersion
	err := db.WithContext(ctx).Where("id = ?", 1).Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return row.Version, nil
}

// setVersion writes the single-row version table with Save, and both databases generate the correct upsert.
func setVersion(tx *gorm.DB, version int) error {
	row := SchemaVersion{ID: 1, Version: version, UpdatedAt: time.Now().UTC()}
	if err := tx.Save(&row).Error; err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return nil
}
