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

// optimizerLogRequiredFields 是上游 review_logs schema 要求的字段集合。
// 逐个断言存在性，避免漏写字段却仍能解析成零值的假通过。
var optimizerLogRequiredFields = []string{
	"card_id", "review_time", "review_rating", "review_state",
	"review_duration", "timezone", "day_start",
}

// decodeOptimizerLogLine 解析一行 JSONL 并逐字段断言其存在与取值范围，返回解码结果。
// 这是验收「导出文件能通过上游文档 schema 的解析」的可执行形式：
// 上游只给了表结构文档、没有机器可读 JSON Schema，所以这里把文档里的类型/范围约束固化成断言。
// 依据的文档 URL 见 OptimizerReviewLog 的注释。
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
	// review_time 是 UTC 毫秒：用 2001-09-09 与 2100 年夹逼，拦住秒/纳秒单位或本地时区的误用。
	const minMS = int64(1_000_000_000_000) // 2001-09-09T01:46:40Z
	const maxMS = int64(4_102_444_800_000) // 2100-01-01T00:00:00Z
	if row.ReviewTime < minMS || row.ReviewTime > maxMS {
		t.Errorf("review_time = %d, want UTC milliseconds in [%d,%d]: %s", row.ReviewTime, minMS, maxMS, line)
	}
	return row
}

// seedExportUser 写入一条用户记录，导出时读取其 timezone/day_cutoff_hour。
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

// seedPresetCards 建一个属于 ownerID 的新预设、挂着它的卡组与卡组里的 n 张卡，返回预设与卡的主键。
// 优化日志按「挂着该预设的卡组」取复习，所以夹具的复习必须落在真实的卡上。
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
		note := Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
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

// TestExportOptimizerLogMatchesUpstreamSchema 是验收：把若干复习行导出为 JSONL，
// 逐行断言它们满足上游 review_logs schema 的类型与范围约束，并核对字段映射与 UTC 毫秒。
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

	// 一条已知时刻的行，用来断言 review_time 恰好是 reviewed_at 的 UTC 毫秒。
	known := time.Date(2026, 10, 2, 12, 0, 0, 123_000_000, time.UTC) // .123s 精度
	zero, neg := 0, -1
	nilMS := []*int{nil, &zero, &neg}
	for i, ms := range nilMS {
		rv := Review{
			CardID: cards[i], UserID: 1, Rating: i + 1, GradeSource: "self",
			ReviewedAt: known.Add(time.Duration(i) * time.Hour), ReviewDay: "2026-10-02",
			ElapsedMS: ms, StateBefore: i, // state_before 依次 0,1,2
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

	// 字段映射：card_id/rating/state_before 原样带出，时区与日切取自用户记录。
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
	// 已知时刻必须是 UTC 毫秒，且保留毫秒精度。
	if got, want := rows[0].ReviewTime, known.UnixMilli(); got != want {
		t.Errorf("review_time = %d, want %d (UTC milliseconds of %s)", got, want, known.Format(time.RFC3339Nano))
	}
	// NULL/零/负的 elapsed_ms 一律落成非负的 0。
	for i, r := range rows {
		if r.ReviewDuration != 0 {
			t.Errorf("row %d review_duration = %d, want 0 for nil/zero/negative elapsed_ms", i, r.ReviewDuration)
		}
	}
	t.Logf("exported %d lines; first line: %s", len(lines), string(lines[0]))
}

// TestExportOptimizerLogSampleParses 断言仓库里的 JSONL 样例本身满足同一套字段断言，
// 这样样例文件就是格式的可执行文档，不会随代码漂移。
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

// TestExportOptimizerLogStreams 证明导出是流式的：写端在第 2 次写入时报错时，导出必须立即
// 中止、返回错误，且此前只写出了第 1 行——说明实现是逐行 Write，而不是先把全部日志拼进内存。
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

// nonEmptyLines 按行切分并丢弃空行（JSONL 允许末尾换行）。
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

// TestOptimizerLogCoversOnlyDecksUsingThePreset 是 FX 验收：一个用户有两个预设、各挂一个自己的卡组，
// 另在别人的共享卡组上用第一个预设。优化第一个预设只用它自己卡组与那个共享卡组上的复习；
// 第二个预设只用它自己卡组上的复习（反面：按用户全量取会让两个预设得到同一份日志）。
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
			review(sharer, sharedCards[0]) // 别人的复习永远不算进来

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
