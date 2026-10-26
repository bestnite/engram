package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "migrate.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

// TestApplyRefusesUnnamedMigration covers the counter-case:
// a destructive change registered without a name must be rejected by schema sync.
func TestApplyRefusesUnnamedMigration(t *testing.T) {
	db := newSQLite(t)
	ran := false
	migrations := []Migration{{Name: "   ", Up: func(tx *gorm.DB) error { ran = true; return nil }}}
	applied, err := Apply(context.Background(), db, migrations)
	if err == nil {
		t.Fatal("Apply() accepted an unnamed migration, want a refusal")
	}
	if !strings.Contains(err.Error(), "has no name") {
		t.Errorf("Apply() error = %q, want it to mention a missing name", err.Error())
	}
	if applied != 0 || ran {
		t.Errorf("applied = %d, ran = %v; want 0 and false", applied, ran)
	}
}

func TestApplyRefusesDuplicateMigrationNames(t *testing.T) {
	db := newSQLite(t)
	migrations := []Migration{
		{Name: "0001_dup", Up: func(tx *gorm.DB) error { return nil }},
		{Name: "0001_dup", Up: func(tx *gorm.DB) error { return nil }},
	}
	if _, err := Apply(context.Background(), db, migrations); err == nil {
		t.Fatal("Apply() accepted duplicate names, want a refusal")
	}
}

// TestApplyRunsRegisteredMigrationExactlyOnce covers the positive case: a registered migration runs exactly once.
func TestApplyRunsRegisteredMigrationExactlyOnce(t *testing.T) {
	db := newSQLite(t)
	ctx := context.Background()
	runs := 0
	migrations := []Migration{
		{Name: "0001_create_widgets", Up: func(tx *gorm.DB) error {
			runs++
			return tx.Exec("CREATE TABLE widgets (id INTEGER PRIMARY KEY)").Error
		}},
	}

	applied, err := Apply(ctx, db, migrations)
	if err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	if applied != 1 || runs != 1 {
		t.Fatalf("first Apply(): applied = %d, runs = %d; want 1 and 1", applied, runs)
	}
	version, err := CurrentVersion(ctx, db)
	if err != nil {
		t.Fatalf("CurrentVersion() error = %v", err)
	}
	if version != 1 {
		t.Errorf("CurrentVersion() = %d, want 1", version)
	}

	// the second run must not re-run it: the version is already in place.
	applied, err = Apply(ctx, db, migrations)
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if applied != 0 || runs != 1 {
		t.Errorf("second Apply(): applied = %d, runs = %d; want 0 and 1", applied, runs)
	}
}

func TestSyncAppliesMigrationsAfterAutoMigrate(t *testing.T) {
	db := newSQLite(t)
	ctx := context.Background()
	runs := 0
	migrations := []Migration{{Name: "0001_mark", Up: func(tx *gorm.DB) error { runs++; return nil }}}
	applied, err := Sync(ctx, db, migrations)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if applied != 1 || runs != 1 {
		t.Errorf("Sync(): applied = %d, runs = %d; want 1 and 1", applied, runs)
	}
	if !db.Migrator().HasTable("schema_version") {
		t.Error("Sync() did not create the schema_version table")
	}
	if !db.Migrator().HasTable("users") {
		t.Error("Sync() did not run AutoMigrate")
	}
}

// TestBuiltinMediaPrimaryKeyMigrationPreservesRows covers a destructive migration: the media primary key moves from an auto-increment id
// to the content sha256. First build the **old schema** (id primary key + sha256 unique index) in a temp DB and write one row;
// after Sync, assert the row count is preserved, the id column is gone, sha256 is the primary key (duplicate sha rejected), and the new code can read it by sha.
func TestBuiltinMediaPrimaryKeyMigrationPreservesRows(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t)

	const oldSha = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	// the old schema matches the DDL GORM generates for the old model (id auto-increment primary key + sha256 not null uniqueIndex):
	// a single line, back-quoted, created_at as datetime. This is exactly the shape in production.
	oldSchema := []string{
		"CREATE TABLE `media` (`id` integer PRIMARY KEY AUTOINCREMENT,`sha256` text NOT NULL,`rel_path` text NOT NULL,`mime` text NOT NULL,`bytes` integer NOT NULL,`width` integer,`height` integer,`created_by` integer,`created_at` datetime NOT NULL)",
		"CREATE UNIQUE INDEX `idx_media_sha256` ON `media`(`sha256`)",
		`INSERT INTO media (sha256, rel_path, mime, bytes, created_by, created_at)
		 VALUES ('` + oldSha + `', 'cc/` + oldSha + `.png', 'image/png', 42, NULL, '2026-01-01 00:00:00')`,
	}
	for _, stmt := range oldSchema {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("seed old schema: %v", err)
		}
	}
	if !db.Migrator().HasColumn("media", "id") {
		t.Fatal("fixture is not the old schema: media has no id column")
	}

	applied, err := Sync(ctx, db, BuiltinMigrations)
	if err != nil {
		t.Fatalf("Sync(BuiltinMigrations) error = %v", err)
	}
	if applied == 0 {
		t.Fatal("Sync() applied no migration, want the media primary-key migration")
	}
	// The table rebuild goes by a fixed column list and drops the new columns the first AutoMigrate added; Sync must add them back after the migration.
	if !db.Migrator().HasColumn("media", "orphaned_at") {
		t.Error("media lost the orphaned_at column after the rebuild migration")
	}

	// the row count is preserved, the id column is gone, the sha256 column is still there.
	var rows int64
	if err := db.Table("media").Count(&rows).Error; err != nil {
		t.Fatalf("count media rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("media rows after migration = %d, want 1 (must preserve existing rows)", rows)
	}
	if db.Migrator().HasColumn("media", "id") {
		t.Error("media still has an id column after the migration")
	}
	if !db.Migrator().HasColumn("media", "sha256") {
		t.Error("media lost its sha256 column")
	}

	// read the row back by sha to confirm the data itself was not lost.
	var got Media
	if err := db.First(&got, "sha256 = ?", oldSha).Error; err != nil {
		t.Fatalf("read migrated media by sha: %v", err)
	}
	if got.RelPath != "cc/"+oldSha+".png" || got.Bytes != 42 || got.Mime != "image/png" {
		t.Fatalf("migrated row differs: %+v", got)
	}

	// sha256 is the new primary key: inserting another row with the same sha must be rejected; a different sha inserts fine (no id needed).
	dup := Media{Sha256: oldSha, RelPath: "cc/other.png", Mime: "image/png", Bytes: 1}
	if err := db.Create(&dup).Error; err == nil {
		t.Error("duplicate sha256 was accepted after the migration, want primary-key violation")
	}
	otherSha := "ddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if err := db.Create(&Media{Sha256: otherSha, RelPath: "dd/" + otherSha + ".png", Mime: "image/png", Bytes: 2}).Error; err != nil {
		t.Fatalf("insert a new sha after the migration: %v", err)
	}

	// migration idempotence: the second Sync is no longer 'run this time', and does not error because the table is already in the new shape.
	applied, err = Sync(ctx, db, BuiltinMigrations)
	if err != nil {
		t.Fatalf("second Sync(BuiltinMigrations) error = %v", err)
	}
	if applied != 0 {
		t.Errorf("second Sync() applied = %d, want 0", applied)
	}
}

// legacyDeck is used only by migration tests: it is the shape decks had while it still carried a visibility column.
// The domain model has dropped that column, so store.Deck cannot be used to build the old table.
type legacyDeck struct {
	ID            uint64    `gorm:"primaryKey"`
	OwnerUserID   uint64    `gorm:"not null;index"`
	Name          string    `gorm:"not null"`
	Description   string    `gorm:"not null"`
	Visibility    string    `gorm:"not null"`
	NewPerDay     int       `gorm:"not null;default:20"`
	ReviewsPerDay int       `gorm:"not null;default:200"`
	PresetID      uint64    `gorm:"not null;index"`
	CreatedAt     time.Time `gorm:"not null"`
}

func (legacyDeck) TableName() string { return "decks" }

// TestBuiltinDropDeckVisibilityMigration covers a destructive migration: decks.visibility is dropped.
// First build the **old schema** (with the visibility column) in a temp DB and write one owned deck and one 'public' deck;
// after Sync, assert the column is gone, both rows are still there, and the old public row is no longer visible to other users — which is exactly the purpose of dropping the column.
func TestBuiltinDropDeckVisibilityMigration(t *testing.T) {
	ctx := context.Background()
	db := newSQLite(t)
	if err := db.AutoMigrate(&legacyDeck{}); err != nil {
		t.Fatalf("create legacy decks table: %v", err)
	}
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []legacyDeck{
		{OwnerUserID: 1, Name: "mine", Visibility: "private", PresetID: 1, CreatedAt: ts},
		{OwnerUserID: 2, Name: "theirs", Visibility: "public", PresetID: 1, CreatedAt: ts},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("seed legacy deck %q: %v", row.Name, err)
		}
	}
	if !db.Migrator().HasColumn("decks", "visibility") {
		t.Fatal("fixture is not the old schema: decks has no visibility column")
	}

	applied, err := Sync(ctx, db, BuiltinMigrations)
	if err != nil {
		t.Fatalf("Sync(BuiltinMigrations) error = %v", err)
	}
	if applied == 0 {
		t.Fatal("Sync() applied no migration, want the deck-visibility drop")
	}
	if db.Migrator().HasColumn("decks", "visibility") {
		t.Error("decks still has a visibility column after the migration")
	}

	// both rows are kept (dropping a column does not change data), and the public deck's name was not touched either.
	var decks []Deck
	if err := db.Order("owner_user_id ASC").Find(&decks).Error; err != nil {
		t.Fatalf("read decks after migration: %v", err)
	}
	if len(decks) != 2 || decks[0].Name != "mine" || decks[1].Name != "theirs" {
		t.Fatalf("decks after migration = %+v, want the two seeded rows intact", decks)
	}

	// key behavior: the deck that used to be visible to everyone via public now belongs only to its owner.
	visible, err := NewDeckStore(db).ListVisible(ctx, 1)
	if err != nil {
		t.Fatalf("ListVisible() error = %v", err)
	}
	if len(visible) != 1 || visible[0].Name != "mine" {
		t.Fatalf("ListVisible(1) = %+v, want only the caller's own deck", visible)
	}

	// idempotent: the second Sync is no longer 'run this time', and does not error because the column is already dropped.
	applied, err = Sync(ctx, db, BuiltinMigrations)
	if err != nil {
		t.Fatalf("second Sync(BuiltinMigrations) error = %v", err)
	}
	if applied != 0 {
		t.Errorf("second Sync() applied = %d, want 0", applied)
	}
}

// TestBuiltinBackfillMediaNotesMigration simulates data from before the mapping table existed: note fields reference media,
// but media_notes has no rows (including one soft-deleted note). After the migration both notes' references are in the mapping,
// existing mapping rows are neither duplicated nor errored on, and another run does not execute again.
func TestBuiltinBackfillMediaNotesMigration(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			// treat the first four migrations as applied, verifying only this one.
			if err := setVersion(db, 4); err != nil {
				t.Fatalf("set version: %v", err)
			}
			owner := seedUsers(t, db, "backfill_owner")[0]
			deckID := seedDeck(t, db, owner)
			shaA := strings.Repeat("a", 64)
			shaB := strings.Repeat("b", 64)
			now := time.Now().UTC()
			live := Note{DeckID: deckID, Kind: "basic", TagsJSON: "[]", CreatedAt: now, UpdatedAt: now,
				FieldsJSON: `{"front":"![](/media/` + shaA + `)","back":"![](media/` + shaB + `.png)"}`}
			gone := Note{DeckID: deckID, Kind: "basic", TagsJSON: "[]", CreatedAt: now, UpdatedAt: now,
				FieldsJSON: `{"front":"![](/media/` + shaB + `)","back":"x"}`}
			plain := Note{DeckID: deckID, Kind: "basic", TagsJSON: "[]", CreatedAt: now, UpdatedAt: now,
				FieldsJSON: `{"front":"no media","back":"x"}`}
			for _, n := range []*Note{&live, &gone, &plain} {
				if err := db.Create(n).Error; err != nil {
					t.Fatalf("seed note: %v", err)
				}
			}
			if err := db.Delete(&gone).Error; err != nil {
				t.Fatalf("soft-delete note: %v", err)
			}
			// one existing mapping row: the backfill must not fail on a unique index conflict.
			if err := db.Create(&MediaNote{MediaSha: shaA, NoteID: live.ID, CreatedAt: now}).Error; err != nil {
				t.Fatalf("seed existing mapping: %v", err)
			}

			// run only up to this migration (the 5th): migrations appended later are not part of this case.
			upTo := BuiltinMigrations[:5]
			applied, err := Apply(ctx, db, upTo)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if applied != 1 {
				t.Fatalf("Apply() applied = %d, want 1", applied)
			}
			var rows []MediaNote
			if err := db.Order("note_id ASC, media_sha ASC").Find(&rows).Error; err != nil {
				t.Fatalf("read media notes: %v", err)
			}
			got := map[string]bool{}
			for _, r := range rows {
				got[fmt.Sprintf("%d:%s", r.NoteID, r.MediaSha[:1])] = true
			}
			want := []string{
				fmt.Sprintf("%d:a", live.ID), fmt.Sprintf("%d:b", live.ID), fmt.Sprintf("%d:b", gone.ID),
			}
			if len(rows) != len(want) {
				t.Fatalf("media notes = %v, want exactly %v", got, want)
			}
			for _, w := range want {
				if !got[w] {
					t.Errorf("missing mapping %s; have %v", w, got)
				}
			}
			if applied, err := Apply(ctx, db, upTo); err != nil || applied != 0 {
				t.Errorf("second Apply() = %d, %v; want 0, nil", applied, err)
			}
		})
	}
}

// TestBuiltinBackfillMemberSettingsMigration simulates shared decks from before member settings existed: grant rows exist, member settings rows
// do not. After the migration members get their own default preset (not the owner's) and the deck's current caps; existing rows are not overwritten.
func TestBuiltinBackfillMemberSettingsMigration(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			if err := setVersion(db, 5); err != nil {
				t.Fatalf("set version: %v", err)
			}
			ids := seedUsers(t, db, "mig_owner", "mig_member", "mig_kept")
			owner, member, kept := ids[0], ids[1], ids[2]
			deckID := seedDeck(t, db, owner)
			if err := db.Model(&Deck{}).Where("id = ?", deckID).
				Updates(map[string]any{"new_per_day": 3, "reviews_per_day": 0}).Error; err != nil {
				t.Fatalf("set caps: %v", err)
			}
			now := time.Now().UTC()
			// write the grant row directly, bypassing GrantStore (which would create member settings on the side) — simulating old data.
			for _, u := range []uint64{member, kept} {
				if err := db.Create(&DeckGrant{DeckID: deckID, UserID: u, Role: RoleReader, CreatedAt: now}).Error; err != nil {
					t.Fatalf("seed grant: %v", err)
				}
			}
			keptPreset := seedPresetRow(t, db, kept)
			if err := db.Create(&DeckMemberSetting{DeckID: deckID, UserID: kept, PresetID: keptPreset, NewPerDay: 9, ReviewsPerDay: 9, UpdatedAt: now}).Error; err != nil {
				t.Fatalf("seed existing member settings: %v", err)
			}

			if applied, err := Apply(ctx, db, BuiltinMigrations[:6]); err != nil || applied != 1 {
				t.Fatalf("Apply() = %d, %v; want 1, nil", applied, err)
			}
			var got DeckMemberSetting
			if err := db.First(&got, "deck_id = ? AND user_id = ?", deckID, member).Error; err != nil {
				t.Fatalf("member settings not backfilled: %v", err)
			}
			memberDefault := DefaultPreset(mustPresets(t, db, member))
			if memberDefault == nil || got.PresetID != memberDefault.ID {
				t.Errorf("backfilled preset = %d, want the member's own default preset", got.PresetID)
			}
			if got.NewPerDay != 3 || got.ReviewsPerDay != 0 {
				t.Errorf("backfilled caps = %d/%d, want the deck's 3/0", got.NewPerDay, got.ReviewsPerDay)
			}
			var existing DeckMemberSetting
			if err := db.First(&existing, "deck_id = ? AND user_id = ?", deckID, kept).Error; err != nil {
				t.Fatalf("load existing settings: %v", err)
			}
			if existing.PresetID != keptPreset || existing.NewPerDay != 9 {
				t.Errorf("existing member settings were overwritten: %+v", existing)
			}
			var ownerRows int64
			db.Model(&DeckMemberSetting{}).Where("user_id = ?", owner).Count(&ownerRows)
			if ownerRows != 0 {
				t.Errorf("owner got %d member settings rows, want 0 (owner settings live on the deck)", ownerRows)
			}
		})
	}
}

// TestBuiltinPerUserSuspensionMigration simulates an old DB with card-level suspension: cards has suspended_at.
// After the migration suspension lands only on the deck owner's state row (a card the owner has no state row for gets a new card state), shared members do not inherit,
// the original column is dropped, and existing progress is unchanged.
func TestBuiltinPerUserSuspensionMigration(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			if err := setVersion(db, 6); err != nil {
				t.Fatalf("set version: %v", err)
			}
			colType := "datetime"
			if db.Dialector.Name() == "postgres" {
				colType = "timestamptz"
			}
			if err := db.Exec("ALTER TABLE cards ADD COLUMN suspended_at " + colType).Error; err != nil {
				t.Fatalf("add legacy column: %v", err)
			}
			ids := seedUsers(t, db, "susp_owner", "susp_member")
			owner, member := ids[0], ids[1]
			deckID := seedDeck(t, db, owner)
			now := time.Now().UTC().Truncate(time.Second)
			mk := func() uint64 {
				note := Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
				if err := db.Create(&note).Error; err != nil {
					t.Fatal(err)
				}
				card := Card{NoteID: note.ID, Template: "forward", CreatedAt: now}
				if err := db.Create(&card).Error; err != nil {
					t.Fatal(err)
				}
				return card.ID
			}
			withState, noState, active := mk(), mk(), mk()
			for _, id := range []uint64{withState, noState} {
				if err := db.Exec("UPDATE cards SET suspended_at = ? WHERE id = ?", now, id).Error; err != nil {
					t.Fatalf("suspend legacy card: %v", err)
				}
			}
			for _, st := range []CardState{
				{CardID: withState, UserID: owner, State: "review", Reps: 4, Version: 4},
				{CardID: withState, UserID: member, State: "review", Reps: 2, Version: 2},
			} {
				if err := db.Create(&st).Error; err != nil {
					t.Fatalf("seed state: %v", err)
				}
			}

			if applied, err := Apply(ctx, db, BuiltinMigrations[:7]); err != nil || applied != 1 {
				t.Fatalf("Apply() = %d, %v; want 1, nil", applied, err)
			}
			if db.Migrator().HasColumn("cards", "suspended_at") {
				t.Error("cards.suspended_at still exists after the migration")
			}
			load := func(card, user uint64) *CardState {
				var st CardState
				if err := db.Where("card_id = ? AND user_id = ?", card, user).Take(&st).Error; err != nil {
					return nil
				}
				return &st
			}
			cases := []struct {
				name      string
				card      uint64
				user      uint64
				wantRow   bool
				suspended bool
				reps      int
			}{
				{"owner's existing state is suspended, progress kept", withState, owner, true, true, 4},
				{"member does not inherit the suspension", withState, member, true, false, 2},
				{"owner without a state row gets a suspended new state", noState, owner, true, true, 0},
				{"unsuspended card is untouched", active, owner, false, false, 0},
			}
			for _, tc := range cases {
				st := load(tc.card, tc.user)
				if (st != nil) != tc.wantRow {
					t.Errorf("%s: state row present = %v, want %v", tc.name, st != nil, tc.wantRow)
					continue
				}
				if st == nil {
					continue
				}
				if (st.SuspendedAt != nil) != tc.suspended || st.Reps != tc.reps {
					t.Errorf("%s: state = suspended %v reps %d, want %v and %d", tc.name, st.SuspendedAt != nil, st.Reps, tc.suspended, tc.reps)
				}
			}
		})
	}
}

// TestBuiltinDropLegacyShareTablesFreshDatabase proves a fresh install ends up without the two share-link tables:
// AutoMigrate no longer creates them, and running every builtin migration neither fails on their absence nor recreates them.
func TestBuiltinDropLegacyShareTablesFreshDatabase(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			applied, err := Sync(ctx, db, BuiltinMigrations)
			if err != nil {
				t.Fatalf("Sync(BuiltinMigrations) error = %v", err)
			}
			if applied == 0 {
				t.Fatal("Sync() applied no migration, want the share-link table drop")
			}
			for _, table := range []string{"share_links", "share_session_decks"} {
				if db.Migrator().HasTable(table) {
					t.Errorf("table %q exists after Sync on a fresh database", table)
				}
			}
			for _, table := range []string{"deck_grants", "deck_share_invites", "decks", "notes"} {
				if !db.Migrator().HasTable(table) {
					t.Errorf("table %q missing after Sync; the migration must not touch other tables", table)
				}
			}
		})
	}
}

// TestBuiltinDropLegacyShareTablesDropsPopulatedTables simulates an older database that still holds rows in both
// share-link tables. After only migration 0008 runs, both tables are gone while deck_grants, deck_share_invites, decks and
// notes keep their rows, and a second Sync neither runs the migration again nor errors on the already-absent tables.
func TestBuiltinDropLegacyShareTablesDropsPopulatedTables(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			if err := setVersion(db, 7); err != nil {
				t.Fatalf("set version: %v", err)
			}
			for _, stmt := range []string{
				`CREATE TABLE share_links (
					token         text PRIMARY KEY,
					deck_id       bigint NOT NULL,
					role          text NOT NULL,
					password_hash text,
					expires_at    timestamp,
					created_by    bigint NOT NULL,
					created_at    timestamp NOT NULL,
					revoked_at    timestamp
				)`,
				`CREATE TABLE share_session_decks (
					session_id text NOT NULL,
					deck_id    bigint NOT NULL,
					expires_at timestamp NOT NULL,
					PRIMARY KEY (session_id, deck_id)
				)`,
			} {
				if err := db.Exec(stmt).Error; err != nil {
					t.Fatalf("create legacy share-link table: %v", err)
				}
			}
			ids := seedUsers(t, db, "share_drop_owner", "share_drop_member")
			owner, member := ids[0], ids[1]
			deckID := seedDeck(t, db, owner)
			now := time.Now().UTC()
			if err := db.Create(&DeckGrant{DeckID: deckID, UserID: member, Role: RoleReader, CreatedBy: Ptr(owner), CreatedAt: now}).Error; err != nil {
				t.Fatalf("seed deck grant: %v", err)
			}
			if err := db.Create(&DeckShareInvite{DeckID: deckID, UserID: member, Role: RoleReader, InvitedBy: owner, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}).Error; err != nil {
				t.Fatalf("seed deck share invite: %v", err)
			}
			if err := db.Exec(`INSERT INTO share_links (token, deck_id, role, created_by, created_at) VALUES (?, ?, ?, ?, ?)`,
				strings.Repeat("a", 64), deckID, RoleReader, owner, now).Error; err != nil {
				t.Fatalf("seed share link row: %v", err)
			}
			if err := db.Exec(`INSERT INTO share_session_decks (session_id, deck_id, expires_at) VALUES (?, ?, ?)`,
				"legacy-session", deckID, now.Add(time.Hour)).Error; err != nil {
				t.Fatalf("seed share session row: %v", err)
			}

			// Only migration 0008 is in scope here: later entries in the list have their own cases, and applying them would make
			// "applied" count something other than the table drop this test asserts.
			applied, err := Sync(ctx, db, BuiltinMigrations[:8])
			if err != nil {
				t.Fatalf("Sync(BuiltinMigrations[:8]) error = %v", err)
			}
			if applied != 1 {
				t.Fatalf("Sync() applied = %d, want 1", applied)
			}
			for _, table := range []string{"share_links", "share_session_decks"} {
				if db.Migrator().HasTable(table) {
					t.Errorf("table %q still exists after the migration", table)
				}
			}
			checks := []struct {
				table string
				want  int64
			}{
				{"decks", 1},
				{"deck_grants", 1},
				{"deck_share_invites", 1},
			}
			for _, check := range checks {
				var got int64
				if err := db.Table(check.table).Count(&got).Error; err != nil {
					t.Fatalf("count %s: %v", check.table, err)
				}
				if got != check.want {
					t.Errorf("%s rows = %d, want %d", check.table, got, check.want)
				}
			}

			applied, err = Sync(ctx, db, BuiltinMigrations[:8])
			if err != nil {
				t.Fatalf("second Sync(BuiltinMigrations[:8]) error = %v", err)
			}
			if applied != 0 {
				t.Errorf("second Sync() applied = %d, want 0", applied)
			}
		})
	}
}

// TestBuiltinUniqueDefaultPresetNamesMigration seeds an owner whose account accumulated three presets named Default (the shape an import left behind).
// After the migration the oldest row keeps the name, the other two are renamed to free "Imported (N)" names, no row is deleted, the deck that
// referenced the oldest keeps its preset_id, a different owner with a single Default is untouched, and a second run changes nothing.
func TestBuiltinUniqueDefaultPresetNamesMigration(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			if err := setVersion(db, 8); err != nil {
				t.Fatalf("set version: %v", err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			mk := func(owner uint64, name string, createdAt time.Time) uint64 {
				p := Preset{OwnerUserID: owner, Name: name, DesiredRetention: 0.9, LearningSteps: "1m,10m",
					RelearningSteps: "10m", MaximumIntervalDays: 100, EnableFuzz: Ptr(true),
					CreatedAt: createdAt, UpdatedAt: createdAt}
				if err := db.Create(&p).Error; err != nil {
					t.Fatalf("create preset %q: %v", name, err)
				}
				return p.ID
			}
			owners := seedUsers(t, db, "preset_dup_owner", "preset_single_owner")
			owner, singleOwner := owners[0], owners[1]
			oldest := mk(owner, DefaultPresetName, now.Add(-3*time.Hour))
			second := mk(owner, DefaultPresetName, now.Add(-2*time.Hour))
			third := mk(owner, DefaultPresetName, now.Add(-time.Hour))
			// A non-default preset already holding the first candidate name: the renames must step over it instead of colliding with it.
			reserved := mk(owner, "Imported (2)", now)
			single := mk(singleOwner, DefaultPresetName, now)
			d := Deck{OwnerUserID: owner, Name: "dup deck", PresetID: oldest, CreatedAt: now}
			if err := db.Create(&d).Error; err != nil {
				t.Fatalf("create deck: %v", err)
			}

			if applied, err := Apply(ctx, db, BuiltinMigrations); err != nil || applied != 1 {
				t.Fatalf("Apply(BuiltinMigrations) = %d, %v; want 1, nil", applied, err)
			}

			load := func(id uint64) Preset {
				var p Preset
				if err := db.First(&p, "id = ?", id).Error; err != nil {
					t.Fatalf("load preset %d: %v", id, err)
				}
				return p
			}
			cases := []struct {
				name  string
				id    uint64
				want  string
				moved bool
			}{
				{"oldest default keeps the name", oldest, DefaultPresetName, false},
				{"second default is renamed past the taken candidate", second, "Imported (3)", true},
				{"third default is renamed to the next free candidate", third, "Imported (4)", true},
				{"an existing non-default name is left alone", reserved, "Imported (2)", false},
				{"single default of another owner is untouched", single, DefaultPresetName, false},
			}
			for _, tc := range cases {
				p := load(tc.id)
				if p.Name != tc.want {
					t.Errorf("%s: name = %q, want %q", tc.name, p.Name, tc.want)
				}
				if tc.moved && p.UpdatedAt.Before(now) {
					t.Errorf("%s: updated_at = %v, want it refreshed at or after %v", tc.name, p.UpdatedAt, now)
				}
			}

			var total int64
			if err := db.Model(&Preset{}).Where("owner_user_id = ?", owner).Count(&total).Error; err != nil {
				t.Fatalf("count presets: %v", err)
			}
			if total != 4 {
				t.Fatalf("owner preset rows = %d, want 4: the migration renames rows, it does not delete them", total)
			}
			var after Deck
			if err := db.First(&after, "id = ?", d.ID).Error; err != nil {
				t.Fatalf("load deck: %v", err)
			}
			if after.PresetID != oldest {
				t.Errorf("deck preset_id = %d, want the unchanged %d", after.PresetID, oldest)
			}

			if applied, err := Apply(ctx, db, BuiltinMigrations); err != nil || applied != 0 {
				t.Fatalf("second Apply(BuiltinMigrations) = %d, %v; want 0, nil", applied, err)
			}
			before := []string{load(second).Name, load(third).Name}
			if err := uniqueDefaultPresetNames(db); err != nil {
				t.Fatalf("rerun uniqueDefaultPresetNames: %v", err)
			}
			again := []string{load(second).Name, load(third).Name}
			for i := range before {
				if again[i] != before[i] {
					t.Errorf("rerun renamed preset %d: %q -> %q, want no change", i, before[i], again[i])
				}
			}
		})
	}
}
