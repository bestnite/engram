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

// optimizerLogRequiredFields 是上游 review_logs schema 要求的字段集合（AGENTS.md M9-3）。
// 逐个断言存在性，避免漏写字段却仍能解析成零值的假通过。
var optimizerLogRequiredFields = []string{
	"card_id", "review_time", "review_rating", "review_state",
	"review_duration", "timezone", "day_start",
}

// decodeOptimizerLogLine 解析一行 JSONL 并逐字段断言其存在与取值范围，返回解码结果。
// 这是 M9-3 验收「导出文件能通过上游文档 schema 的解析」的可执行形式：
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
		Locale: "en", Timezone: tz, DayCutoffHour: cutoff, CreatedAt: time.Now().UTC(),
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// TestExportOptimizerLogMatchesUpstreamSchema 是 M9-3 的验收：把若干复习行导出为 JSONL，
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

	// 一条已知时刻的行，用来断言 review_time 恰好是 reviewed_at 的 UTC 毫秒。
	known := time.Date(2026, 10, 2, 12, 0, 0, 123_000_000, time.UTC) // .123s 精度
	zero, neg := 0, -1
	nilMS := []*int{nil, &zero, &neg}
	for i, ms := range nilMS {
		rv := Review{
			CardID: uint64(200 + i), UserID: 1, Rating: i + 1, GradeSource: "self",
			ReviewedAt: known.Add(time.Duration(i) * time.Hour), ReviewDay: "2026-10-02",
			ElapsedMS: ms, StateBefore: i, // state_before 依次 0,1,2
		}
		if err := db.Create(&rv).Error; err != nil {
			t.Fatalf("seed review: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := NewReviewStore(db).ExportOptimizerLog(ctx, 1, &buf); err != nil {
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
	if rows[0].CardID != 200 || rows[2].CardID != 202 {
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
	for i := 0; i < 5; i++ {
		rv := Review{
			CardID: uint64(300 + i), UserID: 1, Rating: 3, GradeSource: "self",
			ReviewedAt: time.Now().UTC().Add(time.Duration(i) * time.Minute),
			ReviewDay:  "2026-10-02", StateBefore: 2,
		}
		if err := db.Create(&rv).Error; err != nil {
			t.Fatalf("seed review: %v", err)
		}
	}

	w := &failAfterFirstWrite{}
	err = NewReviewStore(db).ExportOptimizerLog(ctx, 1, w)
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
