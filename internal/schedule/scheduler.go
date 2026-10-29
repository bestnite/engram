// Package schedule is a thin wrapper around go-fsrs: it converts the integer states/ratings in the database to and from FSRS v6 types,
// and builds queues on top of it. The service only does three things — persist state, construct the queue, feed the ratings to the scheduler
// so this package deliberately introduces no abstractions beyond the business rules.
//
// Integer conventions (AGENTS.md §2.3 item 3): ratings use 1–4 (Again/Hard/Good/Easy),
// states use 0–3 (New/Learning/Review/Relearning), consistent with the review log format of the FSRS ecosystem,
// so plugging in the optimiser later needs zero conversion. The database stores state as a string (new/learning/review/relearning),
// converted to integers at the boundary.
package schedule

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"

	"git.nite07.com/nite/engram/internal/store"
)

// Rating is the four-level self-grade; values 1–4, matching reviews.rating.
type Rating int

// The four rating levels. The numeric values are part of the contract and must not be adjusted (the database columns and the optimiser export depend on them).
const (
	Again Rating = 1
	Hard  Rating = 2
	Good  Rating = 3
	Easy  Rating = 4
)

// String returns the English name, used only for logs and error messages (machine-facing text is English).
func (r Rating) String() string {
	switch r {
	case Again:
		return "again"
	case Hard:
		return "hard"
	case Good:
		return "good"
	case Easy:
		return "easy"
	default:
		return "unknown"
	}
}

// Valid reports whether the rating falls within 1–4.
func (r Rating) Valid() bool { return r >= Again && r <= Easy }

// State is the scheduling phase the card is in; values 0–3, matching reviews.state_before.
type State int

// The four scheduling phases; the numeric values match FSRS's State and the review log convention.
const (
	StateNew        State = 0
	StateLearning   State = 1
	StateReview     State = 2
	StateRelearning State = 3
)

// ParseState converts the string state from the database into an integer. Unknown values report an English error rather than silently normalising:
// silence would hide a migration bug or a caller's typo.
func ParseState(s string) (State, error) {
	switch s {
	case store.CardStateNew:
		return StateNew, nil
	case store.CardStateLearning:
		return StateLearning, nil
	case store.CardStateReview:
		return StateReview, nil
	case store.CardStateRelearning:
		return StateRelearning, nil
	default:
		return 0, fmt.Errorf("schedule: unknown card state %q", s)
	}
}

// StateFromInt validates an integer state from the review log (reviews.state_before). Unknown values report an English
// error for the same reason ParseState does.
func StateFromInt(v int) (State, error) {
	if s := State(v); s >= StateNew && s <= StateRelearning {
		return s, nil
	}
	return 0, fmt.Errorf("schedule: unknown card state %d", v)
}

// String returns the string state used in the database; this is the single translation point between this package and store.
func (s State) String() string {
	switch s {
	case StateNew:
		return store.CardStateNew
	case StateLearning:
		return store.CardStateLearning
	case StateReview:
		return store.CardStateReview
	case StateRelearning:
		return store.CardStateRelearning
	default:
		return "unknown"
	}
}

// fsrsState converts an integer state into go-fsrs's State.
func (s State) fsrsState() fsrs.State { return fsrs.State(s) }

// stateFromFSRS converts go-fsrs's State back into the integer state.
func stateFromFSRS(s fsrs.State) State { return State(s) }

// Outcome is the scheduling result of one grade (or preview); the fields align with what is written to card_states / reviews,
// so the submit transaction can use it directly without computing it a second time.
type Outcome struct {
	Rating         Rating
	Due            time.Time // UTC due time
	State          State
	Stability      float64
	Difficulty     float64
	ScheduledDays  int
	Reps           int
	Lapses         int
	RemainingSteps int
	// IntervalDays is the actual interval produced by this grade (days, may be fractional; learning steps are counted in minutes).
	IntervalDays float64
}

// Scheduler holds the FSRS parameters built from some preset; it is stateless and safe to reuse concurrently.
type Scheduler struct {
	fsrs *fsrs.FSRS
	// preview has the same parameters as fsrs but with fuzz turned off, used by Preview (reason in Preview).
	preview *fsrs.FSRS
}

// NewScheduler builds a scheduler from a preset: weights come from preset.WeightsJSON (with automatic 17/19/21-dimension migration),
// falling back to fsrs.DefaultWeights() when NULL; learning/relearning steps, target retention, maximum interval and fuzz all come from the preset.
func NewScheduler(preset *store.Preset) (*Scheduler, error) {
	p, err := Parameters(preset)
	if err != nil {
		return nil, err
	}
	unfuzzed := p
	unfuzzed.EnableFuzz = false
	return &Scheduler{fsrs: fsrs.NewFSRS(p), preview: fsrs.NewFSRS(unfuzzed)}, nil
}

// Parameters assembles go-fsrs parameters from a preset and is the single source of "where the scheduling parameters come from":
// weights (automatic 17/19/21-dimension migration, default when NULL), target retention, learning/relearning steps, maximum interval and fuzz.
// NewScheduler and the fit replay both go through here, so the assembly is not written twice and cannot slowly drift apart.
func Parameters(preset *store.Preset) (fsrs.Parameters, error) {
	if preset == nil {
		return fsrs.Parameters{}, fmt.Errorf("schedule: preset is required")
	}
	weights, err := presetWeights(preset.WeightsJSON)
	if err != nil {
		return fsrs.Parameters{}, err
	}
	return parametersWithWeights(preset, weights)
}

// ParametersWithWeights is the same as Parameters but overrides the preset's weights with the given ones.
// The fit comparison must evaluate the old/new weights under "the same batch of reviews and the same other parameters", and this entry point exists for exactly that.
func ParametersWithWeights(preset *store.Preset, weights fsrs.Weights) (fsrs.Parameters, error) {
	if preset == nil {
		return fsrs.Parameters{}, fmt.Errorf("schedule: preset is required")
	}
	return parametersWithWeights(preset, weights)
}

// parametersWithWeights is the shared implementation of Parameters / ParametersWithWeights.
func parametersWithWeights(preset *store.Preset, weights fsrs.Weights) (fsrs.Parameters, error) {
	learning, err := parseSteps(preset.LearningSteps)
	if err != nil {
		return fsrs.Parameters{}, fmt.Errorf("schedule: parse learning steps: %w", err)
	}
	relearning, err := parseSteps(preset.RelearningSteps)
	if err != nil {
		return fsrs.Parameters{}, fmt.Errorf("schedule: parse relearning steps: %w", err)
	}

	p := fsrs.DefaultParam()
	p.RequestRetention = preset.DesiredRetention
	p.MaximumInterval = float64(preset.MaximumIntervalDays)
	p.EnableFuzz = preset.FuzzEnabled()
	p.W = weights
	p.LearningSteps = learning
	p.RelearningSteps = relearning
	// DefaultParam keeps EnableShortTerm=true, i.e. the "learning steps + short-term memory" behaviour.

	return p, nil
}

// presetWeights parses preset.WeightsJSON. NULL / empty / "null" are all treated as using the default weights.
func presetWeights(raw *string) (fsrs.Weights, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return fsrs.DefaultWeights(), nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "null" {
		return fsrs.DefaultWeights(), nil
	}
	var arr []float64
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		return fsrs.Weights{}, fmt.Errorf("schedule: decode preset weights: %w", err)
	}
	w, err := fsrs.MigrateWeights(arr)
	if err != nil {
		return fsrs.Weights{}, fmt.Errorf("schedule: migrate preset weights: %w", err)
	}
	return w, nil
}

// parseSteps parses learning steps such as "1m,10m"; an empty string means learning steps are disabled (returns nil).
// Supports s/m/h/d suffixes; a missing suffix is treated as minutes, matching the documented defaults of the preset.
func parseSteps(spec string) ([]float64, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	parts := strings.Split(spec, ",")
	steps := make([]float64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := stepMinutes(part)
		if err != nil {
			return nil, err
		}
		steps = append(steps, value)
	}
	if len(steps) == 0 {
		return nil, nil
	}
	return steps, nil
}

// stepMinutes converts a single step (e.g. "10m") into minutes.
func stepMinutes(part string) (float64, error) {
	idx := len(part)
	for idx > 0 {
		c := part[idx-1]
		if (c >= '0' && c <= '9') || c == '.' {
			break
		}
		idx--
	}
	num, unit := part[:idx], strings.ToLower(part[idx:])
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid step %q", part)
	}
	switch unit {
	case "", "m", "min":
		return v, nil
	case "s", "sec":
		return v / 60, nil
	case "h", "hr":
		return v * 60, nil
	case "d":
		return v * 1440, nil
	default:
		return 0, fmt.Errorf("invalid step unit in %q", part)
	}
}

// cardFromState converts a database row into go-fsrs's Card.
//
// Learning-step cursor: go-fsrs's RemainingSteps counts down "how many steps remain", and card_states.step_index has the same semantics
// (it is frozen as "remaining" steps) — do not read it as "steps already taken", otherwise the state machine runs backwards overall.
// After the first grade, FSRS takes over this field.
func cardFromState(st *store.CardState) (fsrs.Card, error) {
	state, err := ParseState(st.State)
	if err != nil {
		return fsrs.Card{}, err
	}
	c := fsrs.Card{
		Due:            derefTime(st.DueAt),
		Stability:      derefFloat(st.Stability),
		Difficulty:     derefFloat(st.Difficulty),
		ScheduledDays:  uint64(max(st.ScheduledDays, 0)),
		Reps:           uint64(max(st.Reps, 0)),
		Lapses:         uint64(max(st.Lapses, 0)),
		State:          state.fsrsState(),
		LastReview:     derefTime(st.LastReviewAt),
		RemainingSteps: st.StepIndex,
	}
	return c, nil
}

// Preview previews the scheduling result of the four ratings without changing any state; the order is always Again/Hard/Good/Easy.
//
// Preview does not apply fuzz: the go-fsrs fuzz seed includes the millisecond-level grading instant, and the preview computed when the review page is flipped
// and the one computed at actual submission are separate runs, so with fuzz the results would necessarily differ. Preview gives the central value before fuzz; the actual interval at submission may
// drift slightly around it (fuzz only applies to intervals of 2.5 days and above); with fuzz disabled in the preset the two are identical.
func (s *Scheduler) Preview(st *store.CardState, now time.Time) ([]Outcome, error) {
	card, err := cardFromState(st)
	if err != nil {
		return nil, err
	}
	log, err := s.preview.Repeat(card, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("schedule: preview card %d: %w", st.CardID, err)
	}
	order := []Rating{Again, Hard, Good, Easy}
	out := make([]Outcome, 0, len(order))
	for _, rating := range order {
		out = append(out, outcomeFrom(rating, log[fsrs.Rating(rating)], now))
	}
	return out, nil
}

// Next submits a grade and returns the advanced state.
func (s *Scheduler) Next(st *store.CardState, now time.Time, rating Rating) (Outcome, error) {
	if !rating.Valid() {
		return Outcome{}, fmt.Errorf("schedule: invalid rating %d", int(rating))
	}
	card, err := cardFromState(st)
	if err != nil {
		return Outcome{}, err
	}
	info, err := s.fsrs.Next(card, now.UTC(), fsrs.Rating(rating))
	if err != nil {
		return Outcome{}, fmt.Errorf("schedule: next card %d: %w", st.CardID, err)
	}
	return outcomeFrom(rating, info, now), nil
}

// Retrievability returns the card's recall probability at instant now, used for queue ordering; learning/new cards return 0.
func (s *Scheduler) Retrievability(st *store.CardState, now time.Time) (float64, error) {
	card, err := cardFromState(st)
	if err != nil {
		return 0, err
	}
	return s.fsrs.Retrievability(card, now.UTC())
}

// outcomeFrom flattens the go-fsrs result into the integer-oriented Outcome.
func outcomeFrom(rating Rating, info fsrs.SchedulingInfo, now time.Time) Outcome {
	due := info.Card.Due.UTC()
	interval := due.Sub(now.UTC()).Hours() / 24
	if interval < 0 {
		interval = 0
	}
	return Outcome{
		Rating:         rating,
		Due:            due,
		State:          stateFromFSRS(info.Card.State),
		Stability:      info.Card.Stability,
		Difficulty:     info.Card.Difficulty,
		ScheduledDays:  int(info.Card.ScheduledDays),
		Reps:           int(info.Card.Reps),
		Lapses:         int(info.Card.Lapses),
		RemainingSteps: info.Card.RemainingSteps,
		IntervalDays:   interval,
	}
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func derefFloat(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
