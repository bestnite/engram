package web

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"

	"git.nite07.com/nite/engram/internal/reviewday"
)

// TestStatsDetailMatchesStoreMeasures asserts every number from the SPA detail endpoint can be recomputed independently
// from the seedStatsFixture fixture: review volume, due, retention buckets, time spent, streak, deck/tag dimensions and grading source.
// The fixture semantics are in internal/web/stats_test.go (UTC, midnight cutoff, matching the handler's review day).
func TestStatsDetailMatchesStoreMeasures(t *testing.T) {
	srv, _, _, cookies := newStatsServer(t)
	rec := getWithCookies(t, srv, "/api/v1/stats/detail", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/stats/detail = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var detail statsDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode stats detail: %v", err)
	}

	if detail.GeneratedAt == "" {
		t.Errorf("generated_at is empty; every metric must share one now snapshot")
	}
	if detail.Empty {
		t.Errorf("empty = true, want false for the seeded fixture")
	}

	if got := detail.Volume; got.Today != 2 || got.Last7Days != 3 || got.Last30Days != 3 {
		t.Errorf("volume = %+v, want {2 3 3}", got)
	}
	if got := detail.Due; got.Today != 1 || got.New != 0 {
		t.Errorf("due = %+v, want today=1 new=0", got)
	}

	if got := detail.Retention; got.Total != 2 || got.Passed != 2 || math.Abs(got.Rate-1) > 1e-9 {
		t.Errorf("retention = %+v, want total=2 passed=2 rate=1", got)
	}
	if len(detail.Retention.Buckets) != 7 {
		t.Fatalf("retention buckets = %d, want 7", len(detail.Retention.Buckets))
	}
	bucket := detail.Retention.Buckets[1] // stability 5 lands in 1-7d
	if bucket.Label != "1-7d" || bucket.Total != 2 || bucket.Passed != 2 {
		t.Errorf("retention bucket[1] = %+v, want 1-7d total=2 passed=2", bucket)
	}

	if got := detail.TimeSpent; got.TotalMS != 3000 || got.Count != 2 || got.MedianMS != 1500 || math.Abs(got.AvgMS-1500) > 1e-9 {
		t.Errorf("time_spent = %+v, want total=3000 count=2 median=1500 avg=1500", got)
	}

	if got := detail.Streak; got.Current != 2 || got.Longest != 2 {
		t.Errorf("streak = %+v, want current=2 longest=2", got)
	}

	if len(detail.Curve) != 2 {
		t.Fatalf("curve points = %d, want 2", len(detail.Curve))
	}
	var newTotal, reviewTotal int64
	for _, p := range detail.Curve {
		newTotal += p.New
		reviewTotal += p.Review
	}
	if newTotal != 1 || reviewTotal != 2 {
		t.Errorf("curve totals = new:%d review:%d, want new=1 review=2", newTotal, reviewTotal)
	}
	// The window is 30 review days inclusive, and every curve point falls inside it.
	if detail.CurveFrom == "" || detail.CurveTo == "" {
		t.Fatalf("curve window = [%q, %q], want both bounds", detail.CurveFrom, detail.CurveTo)
	}
	if got := reviewday.Shift(detail.CurveTo, -29); got != detail.CurveFrom {
		t.Errorf("curve_from = %q, want %q (29 days before curve_to)", detail.CurveFrom, got)
	}
	for _, p := range detail.Curve {
		if p.Day < detail.CurveFrom || p.Day > detail.CurveTo {
			t.Errorf("curve point %q outside window [%q, %q]", p.Day, detail.CurveFrom, detail.CurveTo)
		}
	}

	if len(detail.Decks) != 1 {
		t.Fatalf("decks = %d, want 1", len(detail.Decks))
	}
	deck := detail.Decks[0]
	if deck.Name != "Stats deck" || deck.Review != 1 || deck.New != 0 || deck.Reviews != 3 || deck.ElapsedMS != 3000 {
		t.Errorf("deck = %+v, want Stats deck review=1 new=0 reviews=3 elapsed=3000", deck)
	}
	// Retention is the true retention rate: of three reviews only two were scheduled reviews (neither Again), and the new card's Again is not counted,
	// so it is 2/2 rather than the 2/3 of "non-Again / all reviews".
	if math.Abs(deck.Retention-1) > 1e-9 || deck.RetentionTotal != 2 {
		t.Errorf("deck retention = %v over %d, want 1 over 2 due reviews", deck.Retention, deck.RetentionTotal)
	}

	if len(detail.Tags) != 1 {
		t.Fatalf("tags = %d, want 1", len(detail.Tags))
	}
	tag := detail.Tags[0]
	if tag.Tag != "algebra" || tag.Reviews != 3 || tag.RetentionTotal != 2 || math.Abs(tag.Retention-1) > 1e-9 {
		t.Errorf("tag = %+v, want algebra reviews=3 retention=1 over 2 due reviews", tag)
	}

	grades := map[string]int64{}
	for _, g := range detail.Grades {
		grades[g.Source] = g.Count
	}
	if grades["self"] != 2 || grades["typed"] != 1 {
		t.Errorf("grades = %+v, want self=2 typed=1", grades)
	}
}

// TestStatsDetailRequiresSession is a negative case: an anonymous request returns 401 with a stable English code,
// not the 303 redirect the SSR page used (a JSON client should not receive an HTML redirect).
func TestStatsDetailRequiresSession(t *testing.T) {
	srv, _, _, _ := newStatsServer(t)
	rec := getWithCookies(t, srv, "/api/v1/stats/detail", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /api/v1/stats/detail = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.Code != "unauthorized" {
		t.Errorf("anonymous error code = %q, want unauthorized", body.Error.Code)
	}
}
