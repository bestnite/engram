// Copyright: Ankitects Pty Ltd and contributors
// License: GNU AGPL, version 3 or later; http://www.gnu.org/licenses/agpl.html
// Adapted from rslib/src/scheduler/fsrs/params.rs, memory_state.rs,
// rslib/src/revlog/mod.rs and rslib/src/scheduler/timing.rs at Anki 26.09.3.
package store

import (
	"time"

	"git.nite07.com/nite/engram/internal/apkg"
	"github.com/open-spaced-repetition/go-fsrs/v4"
)

// Anki uses its target collection's current offset and rollover, not the source
// collection's settings. Engram substitutes the importing user's timezone and
// DayCutoffHour at now, with a default cutoff of 4; reviewday.Location resolves
// invalid timezone names to UTC. These existing user settings supply the target
// collection context. The fixed offset avoids applying historical DST to old reviews.
func replayNextDay(now time.Time, loc *time.Location, cutoff int) int64 {
	_, offset := now.In(loc).Zone()
	fixed := time.FixedZone("", offset)
	local := now.In(fixed)
	next := time.Date(local.Year(), local.Month(), local.Day(), cutoff, 0, 0, 0, fixed)
	if !now.Before(next) {
		next = next.Add(24 * time.Hour)
	}
	return next.Unix()
}

// replayAge truncates revlog milliseconds to seconds and counts 86400-second
// buckets backward from next_day_at. The exact rollover second and its 0..999 ms
// belong to the earlier bucket. Future reviews clamp to age zero.
func replayAge(next, millis int64) int64 {
	return max(0, (next-millis/1000)/86400)
}

// selectReplay scans backwards to the last learning run or the earliest
// interday grade after a reset; cramming is ignored and manual non-reset rows
// do not cut history. Anki's ignore-before option has no importer equivalent.
func selectReplay(rows []apkg.Review, next int64) ([]apkg.Review, fsrs.ReviewEntries, bool) {
	learning, first := -1, -1
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if r.Type == 3 && r.Factor == 0 {
			continue
		}
		graded := r.Ease >= 1 && r.Ease <= 4
		if graded && r.ID > 0 && (r.Interval >= 1 || r.Interval <= -86400) {
			first = i
		}
		if graded && r.Type == 0 {
			learning = i
		} else if r.Type == 4 && r.Factor == 0 {
			break
		} else if learning >= 0 {
			break
		}
	}
	start, complete := first, false
	if learning >= 0 {
		start, complete = learning, true
	}
	if start < 0 {
		return nil, nil, false
	}
	var selected []apkg.Review
	var history fsrs.ReviewEntries
	for _, r := range rows[start:] {
		if r.Ease < 1 || r.Ease > 4 || (r.Type == 3 && r.Factor == 0) {
			continue
		}
		var delta int64
		if len(selected) > 0 {
			delta = replayAge(next, selected[len(selected)-1].ID) - replayAge(next, r.ID)
		}
		selected = append(selected, r)
		history = append(history, fsrs.ReviewEntry{Rating: fsrs.Rating(r.Ease), DeltaT: float64(delta)})
	}
	return selected, history, complete
}
