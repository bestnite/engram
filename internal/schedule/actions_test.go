package schedule

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

func TestUndoImportedReviewMemory(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	for _, name := range []string{"source", "importer"} {
		if err := db.Create(&store.User{Username: name, Email: name + "@example.com", DisplayName: name, Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	reviewed := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, reviewed)
	cardID := seedCard(t, db, deckID, "forward", reviewed)
	due := reviewed.AddDate(0, 0, 5)
	if err := db.Create(&store.CardState{CardID: cardID, UserID: 1, State: "review", DueAt: &due, Stability: store.Ptr(4.5), Difficulty: store.Ptr(6.2), Reps: 1, ScheduledDays: 5, LastReviewAt: &reviewed}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&store.Review{CardID: cardID, UserID: 1, Rating: 3, GradeSource: "self", ReviewedAt: reviewed, ReviewDay: "2026-10-01", StateBefore: 2, IntervalDays: store.Ptr(5.0)}).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := store.NewDeckStore(db).ExportAnkiPackage(ctx, 1, deckID, store.PackageOptions{IncludeProgress: true, IncludeReviews: true})
	if err != nil {
		t.Fatal(err)
	}
	importReport, err := store.NewDeckStore(db).ImportPackage(ctx, 2, bytes.NewReader(raw), store.PackageImportOptions{Target: store.PackageTargetNewDeck, IncludeProgress: true, MediaRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var imported store.CardState
	if err := db.Where("user_id = ?", 2).First(&imported).Error; err != nil {
		t.Fatal(err)
	}
	s := mustScheduler(t, testPreset(t))
	now := due.Add(24 * time.Hour)
	result := mustSubmit(t, db, SubmitInput{CardID: imported.CardID, UserID: 2, Rating: Good, ExpectedVersion: imported.Version, Scheduler: s, Now: now, Location: time.UTC})
	reimport, err := store.NewDeckStore(db).ImportPackage(ctx, 2, bytes.NewReader(raw), store.PackageImportOptions{Target: store.FormatPackageTarget("into_deck", importReport.DeckID), OnConflict: "update", IncludeProgress: true, MediaRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if reimport.StatesSkipped != 1 || reimport.ReviewsSkipped != 1 || reimport.ProgressApplied != 0 || reimport.ReviewsApplied != 0 {
		t.Fatalf("reimport report: %+v", reimport)
	}
	tx := db.Begin()
	restored, err := Rollback(ctx, tx, UndoInput{CardID: imported.CardID, UserID: 2, ExpectedVersion: result.State.Version, Scheduler: s, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	var ownCount, importedCount int64
	if err := db.Model(&store.Review{}).Where("id = ?", result.ReviewID).Count(&ownCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&store.Review{}).Where("card_id = ? AND user_id = ?", imported.CardID, 2).Count(&importedCount).Error; err != nil {
		t.Fatal(err)
	}
	if ownCount != 0 || importedCount != 1 {
		t.Fatalf("undo: own reviews=%d imported reviews=%d", ownCount, importedCount)
	}
	t.Logf("reimport: state kept, reviews skipped=%d; undo removed own review and kept imported review", reimport.ReviewsSkipped)
	if restored.Stability == nil || restored.Difficulty == nil || restored.DueAt == nil || imported.Stability == nil || imported.Difficulty == nil || imported.DueAt == nil || *restored.Stability != *imported.Stability || *restored.Difficulty != *imported.Difficulty || !restored.DueAt.Equal(*imported.DueAt) {
		t.Fatalf("restored=%+v imported=%+v", restored, imported)
	}
}

// mustSubmit submits one grade in a self-opened transaction, commits it, and returns the result.
func mustSubmit(t *testing.T, db *gorm.DB, in SubmitInput) SubmitResult {
	t.Helper()
	tx := db.Begin()
	res, err := Submit(context.Background(), tx, in)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}
	return res
}

// TestUndoRestoresPreviousDueAndInterval asserts Undo restores exactly the due date and interval after the previous grade
// Approach: grade twice, snapshot the state after the first grade, then compare field by field after Undo.
func TestUndoRestoresPreviousDueAndInterval(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now1 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now1)
	cardID := seedCard(t, db, deckID, "forward", now1)
	s := mustScheduler(t, testPreset(t))

	mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now1, Location: time.UTC,
	})
	var afterFirst store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, 1).First(&afterFirst).Error; err != nil {
		t.Fatalf("load state after first review: %v", err)
	}

	now2 := now1.Add(15 * time.Minute)
	mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: afterFirst.Version,
		Scheduler: s, Now: now2, Location: time.UTC,
	})

	tx := db.Begin()
	// The second grade bumps the version to 2; the undo targets that version.
	restored, err := Rollback(ctx, tx, UndoInput{CardID: cardID, UserID: 1, ExpectedVersion: 2, Scheduler: s, Now: now2})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit undo: %v", err)
	}

	if restored.State != afterFirst.State {
		t.Errorf("undo state = %q, want %q", restored.State, afterFirst.State)
	}
	if restored.DueAt == nil || afterFirst.DueAt == nil {
		t.Fatalf("undo due_at = %v, want %v", restored.DueAt, afterFirst.DueAt)
	}
	if restored.DueAt.UnixMilli() != afterFirst.DueAt.UnixMilli() {
		t.Errorf("undo due_at = %v, want %v (exact restore)", restored.DueAt, afterFirst.DueAt)
	}
	if restored.ScheduledDays != afterFirst.ScheduledDays {
		t.Errorf("undo scheduled_days = %d, want %d", restored.ScheduledDays, afterFirst.ScheduledDays)
	}
	if restored.Reps != afterFirst.Reps || restored.Lapses != afterFirst.Lapses {
		t.Errorf("undo reps/lapses = %d/%d, want %d/%d",
			restored.Reps, restored.Lapses, afterFirst.Reps, afterFirst.Lapses)
	}
	if restored.Stability == nil || afterFirst.Stability == nil || *restored.Stability != *afterFirst.Stability {
		t.Errorf("undo stability = %v, want %v", restored.Stability, afterFirst.Stability)
	}
	if restored.Difficulty == nil || afterFirst.Difficulty == nil || *restored.Difficulty != *afterFirst.Difficulty {
		t.Errorf("undo difficulty = %v, want %v", restored.Difficulty, afterFirst.Difficulty)
	}
	if restored.LastReviewAt == nil || !restored.LastReviewAt.Equal(*afterFirst.LastReviewAt) {
		t.Errorf("undo last_review_at = %v, want %v", restored.LastReviewAt, afterFirst.LastReviewAt)
	}

	// The log being undone must be deleted, leaving only the first grade's log.
	var logs int64
	if err := db.Model(&store.Review{}).Where("card_id = ? AND user_id = ?", cardID, 1).Count(&logs).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if logs != 1 {
		t.Errorf("reviews = %d, want 1 after undo", logs)
	}
	// Write one review.undo audit entry.
	var audits int64
	if err := db.Model(&store.AuditLog{}).Where("action = ?", store.ActionReviewUndo).Count(&audits).Error; err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audits != 1 {
		t.Errorf("review.undo audit rows = %d, want 1", audits)
	}
}

// TestUndoRestoresScheduledDaysExactly asserts Undo restores the whole-day interval the scheduler stored rather
// than a value rounded back from interval_days.
//
// A 1.5-day learning step stores 1 day (the scheduler floors a step's days) while rounding interval_days gives 2,
// so the fixture must carry a step of at least one day: with the default 1m/10m steps both values are 0 and the
// assertion cannot fail.
func TestUndoRestoresScheduledDaysExactly(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	preset := testPreset(t)
	preset.LearningSteps = "10m,1.5d"
	preset.RelearningSteps = "10m,1.5d"
	s := mustScheduler(t, preset)

	first := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now, Location: time.UTC,
	})
	var afterFirst store.CardState
	if err := db.Where("card_id = ? AND user_id = ?", cardID, 1).First(&afterFirst).Error; err != nil {
		t.Fatalf("load state after first review: %v", err)
	}
	if afterFirst.ScheduledDays != 1 {
		t.Fatalf("scheduled_days after the 1.5-day step = %d, want 1 (the fixture must differ from rounding)", afterFirst.ScheduledDays)
	}
	var firstReview store.Review
	if err := db.First(&firstReview, first.ReviewID).Error; err != nil {
		t.Fatalf("load first review: %v", err)
	}
	if firstReview.ScheduledDays == nil || *firstReview.ScheduledDays != 1 {
		t.Errorf("review scheduled_days = %v, want 1 (the post-grade snapshot)", firstReview.ScheduledDays)
	}

	second := mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: first.State.Version,
		Scheduler: s, Now: now.Add(time.Hour), Location: time.UTC,
	})
	restored := mustUndo(t, db, cardID, 1, s, now.Add(time.Hour))
	if restored.ScheduledDays != afterFirst.ScheduledDays {
		t.Errorf("undo scheduled_days = %d, want %d (the previous review's snapshot)", restored.ScheduledDays, afterFirst.ScheduledDays)
	}
	var logs int64
	if err := db.Model(&store.Review{}).Where("card_id = ? AND user_id = ?", cardID, 1).Count(&logs).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if logs != 1 || second.ReviewID == 0 {
		t.Errorf("reviews = %d, want 1 after undoing the second grade", logs)
	}
}

// TestUndoFirstReviewReturnsCardToNew asserts undoing the first grade restores the card to a brand-new state (no due date, no log).
func TestUndoFirstReviewReturnsCardToNew(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	s := mustScheduler(t, testPreset(t))

	mustSubmit(t, db, SubmitInput{
		CardID: cardID, UserID: 1, Rating: Good, ExpectedVersion: 0,
		Scheduler: s, Now: now, Location: time.UTC,
	})

	tx := db.Begin()
	// The first grade bumps the version to 1; the undo targets that version.
	restored, err := Rollback(ctx, tx, UndoInput{CardID: cardID, UserID: 1, ExpectedVersion: 1, Scheduler: s, Now: now})
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit: %v", err)
	}
	if restored.State != StateNew.String() {
		t.Errorf("undo state = %q, want %q", restored.State, StateNew)
	}
	if restored.DueAt != nil {
		t.Errorf("undo due_at = %v, want nil", restored.DueAt)
	}
	if restored.Stability != nil || restored.Difficulty != nil {
		t.Errorf("undo stability/difficulty = %v/%v, want nil", restored.Stability, restored.Difficulty)
	}
	var logs int64
	if err := db.Model(&store.Review{}).Count(&logs).Error; err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	if logs != 0 {
		t.Errorf("reviews = %d, want 0", logs)
	}
}

// TestUndoWithoutLogReturnsError asserts Undo reports an English error rather than silently succeeding when there is no log.
func TestUndoWithoutLogReturnsError(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, cardID, "review", now, 5, 5, &now)
	s := mustScheduler(t, testPreset(t))

	tx := db.Begin()
	defer tx.Rollback()
	_, err := Rollback(context.Background(), tx, UndoInput{CardID: cardID, UserID: 1, ExpectedVersion: 0, Scheduler: s, Now: now})
	if !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("Rollback() error = %v, want ErrNothingToUndo", err)
	}
}

// TestSuspendIsPerUser asserts suspension only affects the suspender: after user 1 suspends it, the card disappears from their queue,
// the same card is still in user 2's queue (the negative case: a card-level suspend would hide it from everyone); after unsuspending it is back in user 1's queue,
// with progress figures and due date unchanged. A new card with no state row can be suspended too.
func TestSuspendIsPerUser(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	cardID := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, cardID, "review", now.Add(-time.Hour), 5, 5, &now)
	seedState(t, db, 2, cardID, "review", now.Add(-time.Hour), 5, 5, &now)
	fresh := seedCard(t, db, deckID, "forward", now)

	s := mustScheduler(t, testPreset(t))
	queue := func(user uint64) map[uint64]bool {
		t.Helper()
		items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(ctx, user,
			QueueOptions{DeckIDs: []uint64{deckID}, Now: now, Location: time.UTC, NewPerDayOverride: new(int)})
		if err != nil {
			t.Fatalf("Build(%d) error = %v", user, err)
		}
		out := map[uint64]bool{}
		for _, it := range items {
			out[it.CardID] = true
		}
		return out
	}
	set := func(card uint64, suspended bool) store.CardState {
		t.Helper()
		var st store.CardState
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			st, err = SetSuspended(ctx, tx, SuspendInput{CardID: card, UserID: 1, Suspended: suspended, Now: now})
			return err
		})
		if err != nil {
			t.Fatalf("SetSuspended(%d, %v) error = %v", card, suspended, err)
		}
		return st
	}

	st := set(cardID, true)
	if st.SuspendedAt == nil || st.State != "review" || st.Stability == nil || *st.Stability != 5 {
		t.Fatalf("suspended state = %+v, want suspended_at set and progress untouched", st)
	}
	if queue(1)[cardID] {
		t.Error("user 1 still sees the card after suspending it")
	}
	if !queue(2)[cardID] {
		t.Error("user 2 lost the card because user 1 suspended it")
	}
	if st := set(fresh, true); st.State != "new" || st.SuspendedAt == nil {
		t.Errorf("suspending a new card = %+v, want a new state row with suspended_at", st)
	}
	if queue(1)[fresh] {
		t.Error("user 1 still sees the suspended new card")
	}

	st = set(cardID, false)
	if st.SuspendedAt != nil || st.DueAt == nil || !st.DueAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("unsuspended state = %+v, want suspended_at cleared and due_at unchanged", st)
	}
	if !queue(1)[cardID] {
		t.Error("user 1 does not see the card after unsuspending it")
	}
}

// TestBuryDefersCardToNextReviewDay asserts burying pushes the due date to the next review day, so both review cards and new cards
// consequently disappear from today's queue.
func TestBuryDefersCardToNextReviewDay(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)

	reviewCard := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, reviewCard, "review", now.Add(-time.Hour), 5, 5, &now)
	newCard := seedCard(t, db, deckID, "reverse", now)

	buried, err := Bury(ctx, db, BuryInput{CardID: reviewCard, UserID: 1, Now: now, Location: time.UTC})
	if err != nil {
		t.Fatalf("Bury(review card) error = %v", err)
	}
	wantDue := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC) // cutoff 4 → 04:00 the next day
	if buried.DueAt == nil || !buried.DueAt.Equal(wantDue) {
		t.Errorf("buried due_at = %v, want %v", buried.DueAt, wantDue)
	}
	if _, err := Bury(ctx, db, BuryInput{CardID: newCard, UserID: 1, Now: now, Location: time.UTC}); err != nil {
		t.Fatalf("Bury(new card) error = %v", err)
	}

	s := mustScheduler(t, testPreset(t))
	items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(ctx, 1, QueueOptions{
		DeckID: deckID, Now: now, Location: time.UTC, NewPerDay: 50, ReviewsPerDay: 200,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Build() = %+v, want empty (both cards buried)", items)
	}
}
