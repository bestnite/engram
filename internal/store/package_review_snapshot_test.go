package store

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// TestPackageRoundTripKeepsReviewUndoSnapshots is an AUDIT regression: a deck package must carry the reviews'
// pre-rating snapshots (step_index_before / due_before). Before the fix neither export nor import carried them, so after "export a backup,
// restore it" undo restored NULL or a wrong value.
func TestPackageRoundTripKeepsReviewUndoSnapshots(t *testing.T) {
	srcs := packageDatabases(t)
	dsts := packageDatabases(t)
	for driver, src := range srcs {
		dst := dsts[driver]
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, src, "pkg_snap_owner")[0]
			deckID, noteIDs := seedPackageDeck(t, src, owner)
			cards, err := NewCardStore(src).ByNote(context.Background(), noteIDs[0])
			if err != nil || len(cards) == 0 {
				t.Fatalf("load cards: %v", err)
			}

			dueBefore := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
			stepBefore := 1
			row := Review{
				CardID: cards[0].ID, UserID: owner, Rating: 3, GradeSource: "self",
				ReviewedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), ReviewDay: "2026-10-03",
				StateBefore: 1, StepIndexBefore: &stepBefore, DueBefore: &dueBefore,
			}
			if err := src.Create(&row).Error; err != nil {
				t.Fatalf("seed review: %v", err)
			}

			raw := exportZip(t, src, owner, deckID, PackageOptions{IncludeProgress: true, IncludeMedia: false, IncludeReviews: true})

			// The exported package itself must carry the snapshots.
			pkg, err := NewDeckStore(src).ExportPackage(context.Background(), owner, deckID,
				PackageOptions{IncludeProgress: true, IncludeMedia: false, IncludeReviews: true})
			if err != nil {
				t.Fatalf("ExportPackage: %v", err)
			}
			if pkg.Progress == nil || len(pkg.Progress.Reviews) != 1 {
				t.Fatalf("exported reviews = %+v, want exactly one", pkg.Progress)
			}
			exported := pkg.Progress.Reviews[0]
			if exported.StepIndexBefore == nil || *exported.StepIndexBefore != stepBefore {
				t.Errorf("exported step_index_before = %v, want %d", exported.StepIndexBefore, stepBefore)
			}
			if exported.DueBefore == nil || *exported.DueBefore != dueBefore.Format(time.RFC3339) {
				t.Errorf("exported due_before = %v, want %s", exported.DueBefore, dueBefore.Format(time.RFC3339))
			}

			// The same user importing their own package: the progress is applied and the snapshots land in the database with it.
			self := seedUsers(t, dst, "pkg_snap_owner")[0]
			if _, err := NewDeckStore(dst).ImportPackage(context.Background(), self, bytes.NewReader(raw),
				PackageImportOptions{Target: PackageTargetNewDeck}); err != nil {
				t.Fatalf("ImportPackage: %v", err)
			}
			var imported Review
			if err := dst.Where("user_id = ?", self).First(&imported).Error; err != nil {
				t.Fatalf("load imported review: %v", err)
			}
			if imported.StepIndexBefore == nil || *imported.StepIndexBefore != stepBefore {
				t.Errorf("imported step_index_before = %v, want %d", imported.StepIndexBefore, stepBefore)
			}
			if imported.DueBefore == nil {
				t.Fatalf("imported due_before = nil, want %s (the undo snapshot must survive a backup restore)", dueBefore)
			}
			if !imported.DueBefore.Equal(dueBefore) {
				t.Errorf("imported due_before = %v, want %v", imported.DueBefore, dueBefore)
			}
		})
	}
}
