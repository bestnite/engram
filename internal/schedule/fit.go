package schedule

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"

	"git.nite07.com/nite/engram/internal/store"
)

// This file implements: replaying review logs with go-fsrs in Go to compute the "before/after optimisation" fit metrics.
//
// Motivation: the job row is required to carry a before/after fit comparison and the presets page renders "improved/not improved" from it, but previously
// no code computed it, so FitBefore/FitAfter were always zero, Improved() was always false, and the page told users "not improved"
// even when the weights genuinely got better. So both metrics are computed here:
//
//   - replay each card's review sequence with the weight set under evaluation (fsrs.NewFSRS);
//   - take the predicted recall probability with (*FSRS).Retrievability "before" each review;
//   - compare against the actual outcome (rating > 1 counts as recalled);
//   - LogLoss = mean negative log-likelihood, RMSE = root mean square error, both covering the same set of items;
//   - the first review of each card is skipped (it has no prior memory state, its predicted probability is 0 and is not evaluable);
//   - the same batch of items is evaluated under two weight sets: the preset's current old weights and the new weights produced by the adapter.

// FitMetrics are the fit metrics from one "replay a fixed review set under a given weight set" run.
// Items is the number of items actually evaluated (first reviews and unpredictable points excluded), proving both weight sets cover the same items.
type FitMetrics struct {
	// LogLoss is the mean negative log-likelihood; smaller means a better fit to the actual outcomes.
	LogLoss float64
	// RMSE is the root mean square error between predicted probability and the actual outcome (0/1); smaller means a better fit.
	RMSE float64
	// Items is the number of items in the computation (the metric semantics must match across the two weight sets).
	Items int
}

// CompareFit evaluates the preset's current weights (old) and newWeights (new) once each on the same batch of predictable items,
// returning (before, after). Both weight sets share the same preset parameter assembly (target retention, learning steps, maximum interval,
// fuzz); only the weights differ, so the replay semantics match the real scheduling in the app.
//
// Guaranteeing the same batch of items: build the item list once grouped by card, then replay each weight set; an item is only counted
// when it is predictable under both weight sets (Retrievability does not error and the probability > 0), otherwise it is skipped for both.
func CompareFit(preset *store.Preset, newWeights []float64, logs []store.OptimizerReviewLog) (before, after FitMetrics, err error) {
	if len(newWeights) != len(fsrs.DefaultWeights()) {
		return FitMetrics{}, FitMetrics{}, fmt.Errorf("schedule: new weights have %d values, want %d", len(newWeights), len(fsrs.DefaultWeights()))
	}
	beforeParam, err := Parameters(preset)
	if err != nil {
		return FitMetrics{}, FitMetrics{}, err
	}
	afterParam, err := ParametersWithWeights(preset, fsrs.Weights(newWeights))
	if err != nil {
		return FitMetrics{}, FitMetrics{}, err
	}

	groups := groupReviews(logs)
	items := buildFitItems(groups)
	beforePreds := replayPredictions(fsrs.NewFSRS(beforeParam), groups)
	afterPreds := replayPredictions(fsrs.NewFSRS(afterParam), groups)
	before, after = scoreItems(items, beforePreds, afterPreds)
	return before, after, nil
}

// fitKey uniquely locates one "predictable item": the seq-th review in the review sequence of some card (group index).
type fitKey struct {
	card int
	seq  int
}

// fitItem is one evaluable prediction point: the prediction for some card "before" its seq-th review (seq>=1), and its true outcome.
type fitItem struct {
	key      fitKey
	observed float64 // 1 means recalled (rating > 1), 0 means forgotten (rated Again)
}

// groupReviews groups review logs by card, each group in ascending review time.
// The input logs are already ascending by (reviewed_at, id) (the ReviewStore.ExportOptimizerLog contract),
// but an explicit stable sort is still applied here so the replay semantics cannot quietly change if the caller switches data sources.
func groupReviews(logs []store.OptimizerReviewLog) [][]store.OptimizerReviewLog {
	index := make(map[uint64]int)
	var groups [][]store.OptimizerReviewLog
	for _, lg := range logs {
		gi, ok := index[lg.CardID]
		if !ok {
			gi = len(groups)
			index[lg.CardID] = gi
			groups = append(groups, nil)
		}
		groups[gi] = append(groups[gi], lg)
	}
	for _, g := range groups {
		sort.SliceStable(g, func(i, j int) bool { return g[i].ReviewTime < g[j].ReviewTime })
	}
	return groups
}

// buildFitItems builds the canonical item list: skipping the first review of each card (no prior memory state)
// and skipping rows with an invalid rating (the outcome cannot be judged and the state cannot be advanced).
func buildFitItems(groups [][]store.OptimizerReviewLog) []fitItem {
	var items []fitItem
	for gi, reviews := range groups {
		for i, lg := range reviews {
			if i == 0 {
				continue
			}
			if _, ok := ratingFromInt(lg.ReviewRating); !ok {
				continue
			}
			items = append(items, fitItem{
				key:      fitKey{card: gi, seq: i},
				observed: observedOutcome(lg.ReviewRating),
			})
		}
	}
	return items
}

// replayPredictions replays all cards with the given FSRS instance and returns the predicted recall probability for each predictable item.
// The prediction is computed on the state "before" that review; when a review is unpredictable (New state, stability<=0,
// invalid rating) that item is absent, and the rest of that card cannot be replayed reliably either, so it stops there.
func replayPredictions(f *fsrs.FSRS, groups [][]store.OptimizerReviewLog) map[fitKey]float64 {
	preds := make(map[fitKey]float64)
	for gi, reviews := range groups {
		if len(reviews) < 2 {
			continue
		}
		card := fsrs.NewCard(time.UnixMilli(reviews[0].ReviewTime).UTC())
		for i, lg := range reviews {
			rating, ok := ratingFromInt(lg.ReviewRating)
			if !ok {
				break
			}
			at := time.UnixMilli(lg.ReviewTime).UTC()
			if i > 0 {
				p, err := f.Retrievability(card, at)
				if err == nil && p > 0 {
					preds[fitKey{card: gi, seq: i}] = p
				}
			}
			next, err := f.Next(card, at, rating)
			if err != nil {
				break
			}
			card = next.Card
		}
	}
	return preds
}

// scoreItems aggregates the metrics of both weight sets over the same batch of items. An item counts only when it has a prediction
// under both weight sets, guaranteeing before/after cover exactly the same item set with a consistent Items count.
func scoreItems(items []fitItem, beforePreds, afterPreds map[fitKey]float64) (FitMetrics, FitMetrics) {
	var b, a fitAccumulator
	for _, it := range items {
		pb, okBefore := beforePreds[it.key]
		pa, okAfter := afterPreds[it.key]
		if !okBefore || !okAfter {
			continue
		}
		b.add(pb, it.observed)
		a.add(pa, it.observed)
	}
	return b.metrics(), a.metrics()
}

// fitAccumulator accumulates the negative log-likelihood and squared error of a single weight set, normalising them into the two metrics at the end.
type fitAccumulator struct {
	items int
	loss  float64
	sqErr float64
}

// add records one item's predicted probability and true outcome (0/1).
func (a *fitAccumulator) add(p, y float64) {
	p = clampProbability(p)
	a.items++
	if y > 0 {
		a.loss += -math.Log(p)
	} else {
		a.loss += -math.Log(1 - p)
	}
	d := p - y
	a.sqErr += d * d
}

// metrics returns the averaged metrics; the zero value when there are no items (equivalent to "no metrics available").
func (a *fitAccumulator) metrics() FitMetrics {
	if a.items == 0 {
		return FitMetrics{}
	}
	n := float64(a.items)
	return FitMetrics{
		LogLoss: a.loss / n,
		RMSE:    math.Sqrt(a.sqErr / n),
		Items:   a.items,
	}
}

// clampProbability clamps the predicted probability into the open interval (0,1), so ln(0) cannot produce ±Inf.
func clampProbability(p float64) float64 {
	const eps = 1e-15
	if p < eps {
		return eps
	}
	if p > 1-eps {
		return 1 - eps
	}
	return p
}

// observedOutcome maps a rating to an outcome: rating > 1 (Hard/Good/Easy) counts as recalled, Again counts as forgotten.
func observedOutcome(rating int) float64 {
	if rating > 1 {
		return 1
	}
	return 0
}

// ratingFromInt validates the integer rating in a review log; an invalid value cannot advance the replay.
func ratingFromInt(rating int) (fsrs.Rating, bool) {
	if rating < int(fsrs.Again) || rating > int(fsrs.Easy) {
		return 0, false
	}
	return fsrs.Rating(rating), true
}
