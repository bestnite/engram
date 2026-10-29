package web

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
)

// This file implements the SPA stats detail endpoint (GET /api/v1/stats/detail).
//
// There is only one implementation of the stats semantics (collectStatsMeasures): the review day, timezone, day cutoff, visible deck scope and every
// aggregate function are all concentrated inside it, and the handler writes no stats SQL, so frontend display and server semantics cannot diverge.
// It returns raw counts, ratios and milliseconds; localization and bar widths are left to the frontend.
//
// Field names and numeric semantics are stable once published, for the SPA and later clients to reuse; no language-pack copy is returned.

// statsMeasures is the raw aggregation result of one stats request; Now is the single moment snapshot shared by all metrics.
type statsMeasures struct {
	Now       time.Time
	Volume    store.ReviewVolume
	Due       store.DueForecast
	Retention store.RetentionStats
	TimeSpent store.TimeSpent
	Streak    store.StreakStats
	Curve     []store.LearningCurvePoint
	// CurveFrom / CurveTo are the first and last review day of the learning curve window (both inclusive, in the user's timezone and by their day cutoff).
	CurveFrom string
	CurveTo   string
	Decks     []store.DeckStat
	Tags      []store.TagStat
	Grades    []store.GradeSourceStat
}

// collectStatsMeasures calls all the existing aggregate queries of the stats page; the handler writes no stats SQL.
//
// now is taken once only: every metric shares the same moment and the same review day window (last 30 days = [today-29, today]).
// Both the stats page and the detail endpoint take their numbers from here, so the two cannot report different
// semantics for the same request, nor duplicate the orchestration logic each.
func (s *Server) collectStatsMeasures(ctx context.Context, user *store.User) (statsMeasures, error) {
	now := time.Now()
	locTZ := reviewday.Location(user.Timezone)
	cutoff := reviewday.ResolveCutoff(user.DayCutoffHour)
	today := reviewday.Day(now, locTZ, cutoff)
	from30 := reviewday.Shift(today, -29)

	stats := store.NewStatsStore(s.db)

	volume, err := stats.ReviewVolume(ctx, user.ID, today)
	if err != nil {
		return statsMeasures{}, err
	}
	due, err := stats.DueForecast(ctx, user.ID, 0, now, locTZ, cutoff)
	if err != nil {
		return statsMeasures{}, err
	}
	retention, err := stats.RetentionByStability(ctx, user.ID, 0)
	if err != nil {
		return statsMeasures{}, err
	}
	timeSpent, err := stats.TimeSpent(ctx, user.ID, from30, today)
	if err != nil {
		return statsMeasures{}, err
	}
	streak, err := stats.Streak(ctx, user.ID, now, locTZ, cutoff)
	if err != nil {
		return statsMeasures{}, err
	}
	curve, err := stats.LearningCurve(ctx, user.ID, from30, today)
	if err != nil {
		return statsMeasures{}, err
	}
	decks, err := stats.DeckBreakdown(ctx, user.ID, now)
	if err != nil {
		return statsMeasures{}, err
	}
	tags, err := stats.TagBreakdown(ctx, user.ID, from30, today)
	if err != nil {
		return statsMeasures{}, err
	}
	grades, err := stats.GradeSourceDistribution(ctx, user.ID)
	if err != nil {
		return statsMeasures{}, err
	}

	return statsMeasures{
		Now:       now,
		Volume:    volume,
		Due:       due,
		Retention: retention,
		TimeSpent: timeSpent,
		Streak:    streak,
		Curve:     curve,
		CurveFrom: from30,
		CurveTo:   today,
		Decks:     decks,
		Tags:      tags,
		Grades:    grades,
	}, nil
}

// statsDetail is the response body of GET /api/v1/stats/detail.
type statsDetail struct {
	// GeneratedAt is the now snapshot shared by all metrics (RFC3339, UTC).
	GeneratedAt string `json:"generated_at"`
	// Empty uses the same criterion as the SSR stats page: no reviews in the window, no due cards, no deck data.
	Empty     bool              `json:"empty"`
	Volume    statsVolume       `json:"volume"`
	Due       statsDue          `json:"due"`
	Retention statsRetention    `json:"retention"`
	TimeSpent statsTimeSpent    `json:"time_spent"`
	Streak    statsStreak       `json:"streak"`
	Curve     []statsCurvePoint `json:"curve"`
	// CurveFrom / CurveTo give the first and last review day of the curve window: curve only contains days with reviews,
	// and the client needs the window bounds to draw a continuous date axis, since it cannot compute "today" under the user's timezone and day cutoff.
	CurveFrom string             `json:"curve_from"`
	CurveTo   string             `json:"curve_to"`
	Decks     []statsDeck        `json:"decks"`
	Tags      []statsTag         `json:"tags"`
	Grades    []statsGradeSource `json:"grades"`
}

// statsVolume is the review volume metric (today / last 7 days / last 30 days).
type statsVolume struct {
	Today      int64 `json:"today"`
	Last7Days  int64 `json:"last_7_days"`
	Last30Days int64 `json:"last_30_days"`
}

// statsDue holds mutually exclusive buckets of the due forecast; NewNotDue means new cards not yet scheduled.
type statsDue struct {
	Today     int64 `json:"today"`
	Tomorrow  int64 `json:"tomorrow"`
	Within7   int64 `json:"within_7_days"`
	Within30  int64 `json:"within_30_days"`
	Later     int64 `json:"later"`
	NewNotDue int64 `json:"new_not_due"`
}

// statsRetentionBucket is the retention of one stability bucket; Rate is Passed/Total (0 when Total is 0).
type statsRetentionBucket struct {
	Label  string  `json:"label"`
	Total  int64   `json:"total"`
	Passed int64   `json:"passed"`
	Rate   float64 `json:"rate"`
}

// statsRetention is the overall retention rate plus each stability bucket.
type statsRetention struct {
	Total   int64                  `json:"total"`
	Passed  int64                  `json:"passed"`
	Rate    float64                `json:"rate"`
	Buckets []statsRetentionBucket `json:"buckets"`
}

// statsTimeSpent is the time-spent metric (milliseconds).
type statsTimeSpent struct {
	TotalMS  int64   `json:"total_ms"`
	Count    int64   `json:"count"`
	AvgMS    float64 `json:"avg_ms"`
	MedianMS int64   `json:"median_ms"`
}

// statsStreak is the number of consecutive study days.
type statsStreak struct {
	Current int `json:"current"`
	Longest int `json:"longest"`
}

// statsCurvePoint is one review day on the learning curve: New is newly introduced, Review is the review volume.
type statsCurvePoint struct {
	Day    string `json:"day"`
	New    int64  `json:"new"`
	Review int64  `json:"review"`
}

// statsDeck is the stats row of one deck. Reviews is the total number of reviews; Retention is the true retention rate,
// whose denominator RetentionTotal counts only scheduled (due) reviews, and when it is 0 the client shows "no data" rather than 0%.
type statsDeck struct {
	DeckID         string  `json:"deck_id"`
	Name           string  `json:"name"`
	DueCount       int64   `json:"due_count"`
	Reviews        int64   `json:"reviews"`
	Retention      float64 `json:"retention"`
	RetentionTotal int64   `json:"retention_total"`
	ElapsedMS      int64   `json:"elapsed_ms"`
}

// statsTag is the stats row of one tag; Reviews / Retention / RetentionTotal mean the same as in statsDeck.
type statsTag struct {
	Tag            string  `json:"tag"`
	Reviews        int64   `json:"reviews"`
	Retention      float64 `json:"retention"`
	RetentionTotal int64   `json:"retention_total"`
}

// statsGradeSource is the count for one grading source (self/typed/llm).
type statsGradeSource struct {
	Source string `json:"source"`
	Count  int64  `json:"count"`
}

// statsDetail is a session-only read-only endpoint: it accepts browser sessions only, never bearer / API keys.
//
// It is mounted outside /api/v1 (see registerStatsRoutes), so the API key group middleware does not apply; here it explicitly
// requires an active session and answers 401 when logged out, which is semantically consistent with the SSR page's "redirect to the login page" (a JSON client
// should not receive an HTML redirect). GET is a safe method and needs no CSRF.
func (s *Server) statsDetail(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || user.Status != store.StatusActive {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
			"code": api.CodeUnauthorized, "message": api.ErrorMessage(c.Request.Context(), api.CodeUnauthorized),
		}})
		return
	}
	detail, err := s.buildStatsDetail(c.Request.Context(), user)
	if err != nil {
		s.logger.Error("build SPA stats detail failed", "user_id", user.ID, "error", err)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"code": api.CodeInternal, "message": api.ErrorMessage(c.Request.Context(), api.CodeInternal),
		}})
		return
	}
	c.JSON(http.StatusOK, detail)
}

// buildStatsDetail maps the raw aggregation result into the stable JSON shape; slices are always initialized non-nil,
// so empty results serialize as [] rather than null and the frontend needs no separate null branch.
func (s *Server) buildStatsDetail(ctx context.Context, user *store.User) (statsDetail, error) {
	m, err := s.collectStatsMeasures(ctx, user)
	if err != nil {
		return statsDetail{}, err
	}
	detail := statsDetail{
		GeneratedAt: m.Now.UTC().Format(time.RFC3339),
		// Empty-state criterion: no review volume at all, nothing due today, no deck data.
		Empty: m.Volume.Today == 0 && m.Volume.Last30Days == 0 &&
			m.Due.Today == 0 && m.Due.NewNotDue == 0 && len(m.Decks) == 0,
		CurveFrom: m.CurveFrom,
		CurveTo:   m.CurveTo,
		Volume: statsVolume{
			Today:      m.Volume.Today,
			Last7Days:  m.Volume.Last7Days,
			Last30Days: m.Volume.Last30Days,
		},
		Due: statsDue{
			Today:     m.Due.Today,
			Tomorrow:  m.Due.Tomorrow,
			Within7:   m.Due.Within7,
			Within30:  m.Due.Within30,
			Later:     m.Due.Later,
			NewNotDue: m.Due.NewNotDue,
		},
		Retention: statsRetention{
			Total:   m.Retention.Total,
			Passed:  m.Retention.Passed,
			Rate:    m.Retention.Rate,
			Buckets: make([]statsRetentionBucket, 0, len(m.Retention.Buckets)),
		},
		TimeSpent: statsTimeSpent{
			TotalMS:  m.TimeSpent.TotalMS,
			Count:    m.TimeSpent.Count,
			AvgMS:    m.TimeSpent.AvgMS,
			MedianMS: m.TimeSpent.MedianMS,
		},
		Streak: statsStreak{Current: m.Streak.Current, Longest: m.Streak.Longest},
		Curve:  make([]statsCurvePoint, 0, len(m.Curve)),
		Decks:  make([]statsDeck, 0, len(m.Decks)),
		Tags:   make([]statsTag, 0, len(m.Tags)),
		Grades: make([]statsGradeSource, 0, len(m.Grades)),
	}
	for _, b := range m.Retention.Buckets {
		detail.Retention.Buckets = append(detail.Retention.Buckets, statsRetentionBucket{
			Label: b.Label, Total: b.Total, Passed: b.Passed, Rate: b.Rate,
		})
	}
	for _, p := range m.Curve {
		detail.Curve = append(detail.Curve, statsCurvePoint{Day: p.Day, New: p.New, Review: p.Review})
	}
	for _, d := range m.Decks {
		detail.Decks = append(detail.Decks, statsDeck{
			DeckID: d.PublicID, Name: d.Name, DueCount: d.DueCount,
			Reviews: d.Reviews, Retention: d.Retention, RetentionTotal: d.RetentionTotal, ElapsedMS: d.ElapsedMS,
		})
	}
	for _, tg := range m.Tags {
		detail.Tags = append(detail.Tags, statsTag{Tag: tg.Tag, Reviews: tg.Reviews, Retention: tg.Retention, RetentionTotal: tg.RetentionTotal})
	}
	for _, g := range m.Grades {
		detail.Grades = append(detail.Grades, statsGradeSource{Source: g.Source, Count: g.Count})
	}
	return detail, nil
}
