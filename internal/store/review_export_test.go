package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

// optimizerLogRequiredFields is the set of fields the upstream review_logs schema requires.
// Each one's presence is asserted, so a missing field cannot pass falsely by parsing as a zero value.
var optimizerLogRequiredFields = []string{
	"card_id", "review_time", "review_rating", "review_state",
	"review_duration", "timezone", "day_start",
}

// decodeOptimizerLogLine parses one JSONL line, asserts each field's presence and value range, and returns the decoded result.
// This is the executable form of the acceptance "the exported file parses against the upstream documented schema":
// upstream provides only a table-structure document, no machine-readable JSON Schema, so the type/range constraints from the document are frozen here as assertions.
// The source document URL is in the OptimizerReviewLog comment.
func decodeOptimizerLogLine(t *testing.T, line []byte) OptimizerReviewLog {
	t.Helper()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatalf("line is not valid JSON: %v (line=%s)", err, line)
	}
	for _, field := range optimizerLogRequiredFields {
		if _, ok := raw[field]; !ok {
			t.Fatalf("line missing required field %q: %s", field, line)
		}
	}

	var row OptimizerReviewLog
	if err := json.Unmarshal(line, &row); err != nil {
		t.Fatalf("decode optimizer log line: %v (line=%s)", err, line)
	}
	if row.CardID == 0 {
		t.Errorf("card_id = 0, want a positive identifier: %s", line)
	}
	if row.ReviewRating < 1 || row.ReviewRating > 4 {
		t.Errorf("review_rating = %d, want 1-4: %s", row.ReviewRating, line)
	}
	if row.ReviewState < 0 || row.ReviewState > 3 {
		t.Errorf("review_state = %d, want 0-3: %s", row.ReviewState, line)
	}
	if row.ReviewDuration < 0 {
		t.Errorf("review_duration = %d, want non-negative: %s", row.ReviewDuration, line)
	}
	if row.Timezone == "" {
		t.Errorf("timezone is empty, want an IANA name: %s", line)
	}
	if row.DayStart < 0 || row.DayStart > 23 {
		t.Errorf("day_start = %d, want 0-23: %s", row.DayStart, line)
	}
	// review_time is UTC milliseconds: bracketed by 2001-09-09 and the year 2100, it catches seconds/nanoseconds units or a local timezone.
	const minMS = int64(1_000_000_000_000) // 2001-09-09T01:46:40Z
	const maxMS = int64(4_102_444_800_000) // 2100-01-01T00:00:00Z
	if row.ReviewTime < minMS || row.ReviewTime > maxMS {
		t.Errorf("review_time = %d, want UTC milliseconds in [%d,%d]: %s", row.ReviewTime, minMS, maxMS, line)
	}
	return row
}

// seedExportUser writes one user row; the export reads its timezone/day_cutoff_hour.
func seedExportUser(t *testing.T, db *gorm.DB, id uint64, tz string, cutoff int) {
	t.Helper()
	u := User{
		ID: id, Username: "exporter", Email: "exporter@example.com",
		DisplayName: "Exporter", Role: RoleUser, Status: StatusActive,
		Locale: "en", Timezone: tz, DayCutoffHour: Ptr(cutoff), CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// seedPresetCards creates a new preset owned by ownerID, a deck carrying it and n cards in that deck, returning the preset and card primary keys.
// The optimizer log takes reviews by "decks carrying the preset", so the fixture's reviews must land on real cards.
func seedPresetCards(t *testing.T, db *gorm.DB, ownerID uint64, n int) (uint64, []uint64) {
	t.Helper()
	deckID := seedDeck(t, db, ownerID)
	var deck Deck
	if err := db.First(&deck, deckID).Error; err != nil {
		t.Fatalf("load deck: %v", err)
	}
	now := time.Now().UTC()
	ids := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		note := Note{DeckID: deckID, FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&note).Error; err != nil {
			t.Fatalf("create note: %v", err)
		}
		card := Card{NoteID: note.ID, Template: "forward", CreatedAt: now}
		if err := db.Create(&card).Error; err != nil {
			t.Fatalf("create card: %v", err)
		}
		ids = append(ids, card.ID)
	}
	return deck.PresetID, ids
}

// TestExportOptimizerLogMatchesUpstreamSchema is the acceptance: several review rows are exported as JSONL,
// each line is asserted to satisfy the upstream review_logs schema's type and range constraints, and the field mapping and UTC milliseconds are checked.
func TestExportOptimizerLogMatchesUpstreamSchema(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "reviews.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	ctx := context.Background()
	seedExportUser(t, db, 1, "America/New_York", 4)
	presetID, cards := seedPresetCards(t, db, 1, 3)

	// One row with a known timestamp, used to assert review_time is exactly reviewed_at in UTC milliseconds.
	known := time.Date(2026, 10, 2, 12, 0, 0, 123_000_000, time.UTC) // .123s precision
	zero, neg := 0, -1
	nilMS := []*int{nil, &zero, &neg}
	for i, ms := range nilMS {
		rv := Review{
			CardID: cards[i], UserID: 1, Rating: i + 1, GradeSource: "self",
			ReviewedAt: known.Add(time.Duration(i) * time.Hour), ReviewDay: "2026-10-02",
			ElapsedMS: ms, StateBefore: i, // state_before runs 0,1,2
		}
		if err := db.Create(&rv).Error; err != nil {
			t.Fatalf("seed review: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := NewReviewStore(db).ExportOptimizerLog(ctx, 1, presetID, &buf); err != nil {
		t.Fatalf("ExportOptimizerLog: %v", err)
	}

	lines := nonEmptyLines(buf.Bytes())
	if len(lines) != 3 {
		t.Fatalf("exported %d lines, want 3:\n%s", len(lines), buf.String())
	}
	rows := make([]OptimizerReviewLog, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, decodeOptimizerLogLine(t, line))
	}

	// Field mapping: card_id/rating/state_before come out as they are, and timezone and day cutoff come from the user row.
	if rows[0].CardID != cards[0] || rows[2].CardID != cards[2] {
		t.Errorf("card_id mapping wrong: %+v", rows)
	}
	if rows[0].ReviewRating != 1 || rows[2].ReviewRating != 3 {
		t.Errorf("review_rating mapping wrong: %+v", rows)
	}
	if rows[0].ReviewState != 0 || rows[2].ReviewState != 2 {
		t.Errorf("review_state mapping wrong: %+v", rows)
	}
	for _, r := range rows {
		if r.Timezone != "America/New_York" || r.DayStart != 4 {
			t.Fatalf("timezone/day_start = %q/%d, want America/New_York/4", r.Timezone, r.DayStart)
		}
	}
	// The known timestamp must be UTC milliseconds and keep millisecond precision.
	if got, want := rows[0].ReviewTime, known.UnixMilli(); got != want {
		t.Errorf("review_time = %d, want %d (UTC milliseconds of %s)", got, want, known.Format(time.RFC3339Nano))
	}
	// A NULL/zero/negative elapsed_ms always becomes a non-negative 0.
	for i, r := range rows {
		if r.ReviewDuration != 0 {
			t.Errorf("row %d review_duration = %d, want 0 for nil/zero/negative elapsed_ms", i, r.ReviewDuration)
		}
	}
	t.Logf("exported %d lines; first line: %s", len(lines), string(lines[0]))
}

// TestExportOptimizerLogSampleParses asserts the JSONL sample in the repo satisfies the same field assertions,
// so the sample file is executable documentation of the format and cannot drift away from the code.
func TestExportOptimizerLogSampleParses(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "optimizer-review-log.jsonl"))
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	lines := nonEmptyLines(data)
	if len(lines) == 0 {
		t.Fatal("sample file has no lines")
	}
	for _, line := range lines {
		decodeOptimizerLogLine(t, line)
	}
	t.Logf("sample parses: %d line(s)", len(lines))
}

// TestExportOptimizerLogStreams proves the export streams: when the writer errors on the 2nd write, the export must
// stop at once and return an error, having written only the 1st line before that -- which shows the implementation writes line by line rather than assembling the whole log in memory first.
type failAfterFirstWrite struct {
	writes int
	buf    bytes.Buffer
}

func (f *failAfterFirstWrite) Write(p []byte) (int, error) {
	f.writes++
	if f.writes > 1 {
		return 0, errors.New("disk full")
	}
	return f.buf.Write(p)
}

func TestExportOptimizerLogStreams(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "reviews.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(context.Background(), db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	ctx := context.Background()
	seedExportUser(t, db, 1, "UTC", 4)
	presetID, cards := seedPresetCards(t, db, 1, 5)
	for i := 0; i < 5; i++ {
		rv := Review{
			CardID: cards[i], UserID: 1, Rating: 3, GradeSource: "self",
			ReviewedAt: time.Now().UTC().Add(time.Duration(i) * time.Minute),
			ReviewDay:  "2026-10-02", StateBefore: 2,
		}
		if err := db.Create(&rv).Error; err != nil {
			t.Fatalf("seed review: %v", err)
		}
	}

	w := &failAfterFirstWrite{}
	err = NewReviewStore(db).ExportOptimizerLog(ctx, 1, presetID, w)
	if err == nil {
		t.Fatal("ExportOptimizerLog returned nil, want the write error to surface")
	}
	gotLines := nonEmptyLines(w.buf.Bytes())
	if len(gotLines) != 1 {
		t.Fatalf("wrote %d line(s) before failing, want exactly 1 (streaming, not buffered)", len(gotLines))
	}
	decodeOptimizerLogLine(t, gotLines[0])
	t.Logf("write failed after %d write(s); %d complete line(s) emitted before the error: %v",
		w.writes, len(gotLines), err)
}

// nonEmptyLines splits into lines and drops the empty ones (JSONL allows a trailing newline).
func nonEmptyLines(data []byte) [][]byte {
	var out [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			out = append(out, line)
		}
	}
	return out
}

// TestOptimizerLogCoversOnlyDecksUsingThePreset is the FX acceptance: one user has two presets, each carried by one of their own decks,
// and also uses the first preset on someone else's shared deck. Optimizing the first preset uses only the reviews on its own deck and on that shared deck;
// the second preset uses only the reviews on its own deck (the counter-case: taking all of a user's logs would give both presets the same log).
func TestOptimizerLogCoversOnlyDecksUsingThePreset(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate: %v", err)
			}
			ids := seedUsers(t, db, "opt_user", "opt_sharer")
			user, sharer := ids[0], ids[1]
			presetA, cardsA := seedPresetCards(t, db, user, 2)
			presetB, cardsB := seedPresetCards(t, db, user, 1)
			_, sharedCards := seedPresetCards(t, db, sharer, 1)
			var shared Card
			if err := db.First(&shared, sharedCards[0]).Error; err != nil {
				t.Fatal(err)
			}
			var sharedNote Note
			if err := db.First(&sharedNote, shared.NoteID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&DeckMemberSetting{DeckID: sharedNote.DeckID, UserID: user, PresetID: presetA,
				NewPerDay: 20, ReviewsPerDay: 200, UpdatedAt: time.Now().UTC()}).Error; err != nil {
				t.Fatalf("seed member settings: %v", err)
			}
			review := func(userID, cardID uint64) {
				t.Helper()
				if err := db.Create(&Review{CardID: cardID, UserID: userID, Rating: 3, GradeSource: "self",
					ReviewedAt: time.Now().UTC(), ReviewDay: "2026-10-02", StateBefore: 2}).Error; err != nil {
					t.Fatalf("seed review: %v", err)
				}
			}
			for _, c := range append(append(append([]uint64{}, cardsA...), cardsB...), sharedCards...) {
				review(user, c)
			}
			review(sharer, sharedCards[0]) // someone else's reviews never count

			cases := []struct {
				name   string
				preset uint64
				want   map[uint64]bool
			}{
				{"preset A: own deck and the shared deck", presetA, map[uint64]bool{cardsA[0]: true, cardsA[1]: true, sharedCards[0]: true}},
				{"preset B: only its own deck", presetB, map[uint64]bool{cardsB[0]: true}},
			}
			for _, tc := range cases {
				var buf bytes.Buffer
				if err := NewReviewStore(db).ExportOptimizerLog(ctx, user, tc.preset, &buf); err != nil {
					t.Fatalf("%s: ExportOptimizerLog: %v", tc.name, err)
				}
				lines := nonEmptyLines(buf.Bytes())
				got := map[uint64]bool{}
				for _, line := range lines {
					got[decodeOptimizerLogLine(t, line).CardID] = true
				}
				if len(lines) != len(tc.want) || len(got) != len(tc.want) {
					t.Errorf("%s: exported cards %v, want %v", tc.name, got, tc.want)
				}
				for id := range tc.want {
					if !got[id] {
						t.Errorf("%s: card %d missing from %v", tc.name, id, got)
					}
				}
				n, err := NewReviewStore(db).CountForPreset(ctx, user, tc.preset)
				if err != nil || n != int64(len(tc.want)) {
					t.Errorf("%s: CountForPreset = %d, %v; want %d", tc.name, n, err, len(tc.want))
				}
			}
		})
	}
}
