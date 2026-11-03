package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// recorder collects the rounds a Registry hands to its record function.
type recorder struct {
	mu     sync.Mutex
	rounds []Round
}

func (r *recorder) record(_ context.Context, round Round) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rounds = append(r.rounds, round)
}

func (r *recorder) snapshot() []Round {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Round(nil), r.rounds...)
}

// TestRoundRecorded pins the history rule: a round is persisted when it did work, failed, or was started by hand.
func TestRoundRecorded(t *testing.T) {
	cases := []struct {
		name  string
		round Round
		want  bool
	}{
		{"auto round that did nothing", Round{Trigger: TriggerAuto, Summary: Summary{"sessions": 0}}, false},
		{"auto round with a nil summary", Round{Trigger: TriggerAuto}, false},
		{"auto round with a positive count", Round{Trigger: TriggerAuto, Summary: Summary{"sessions": 0, "invites": 2}}, true},
		{"failed auto round", Round{Trigger: TriggerAuto, Err: errors.New("boom")}, true},
		{"manual round that did nothing", Round{Trigger: TriggerManual, Summary: Summary{}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.round.Recorded(); got != tc.want {
				t.Fatalf("Recorded() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRegistryRecordsOnlyHistoryRounds runs real rounds through a registered lifecycle: a no-op timer round is kept
// as the latest round but not persisted, a round with work is persisted, and a manual no-op round is persisted
// with its trigger.
func TestRegistryRecordsOnlyHistoryRounds(t *testing.T) {
	results := make(chan Summary, 4)
	results <- Summary{"removed": 0}
	results <- Summary{"removed": 3}
	l, err := New(Config{
		Kind:        KindMediaGC,
		RunOnce:     func(context.Context) (Summary, error) { return <-results, nil },
		Logger:      slog.New(discardHandler{}),
		FailMessage: "test run failed",
		Interval:    time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := &recorder{}
	reg := NewRegistry(rec.record)
	if err := reg.Add(l); err != nil {
		t.Fatalf("Add: %v", err)
	}
	l.Start(context.Background())
	defer l.Stop()

	// The startup round did nothing: it is the latest round, and nothing is recorded.
	waitFor(t, "the startup round", func() bool { return l.Status().Last != nil })
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("no-op round recorded: %+v", got)
	}
	st := reg.Statuses()
	if len(st) != 1 || st[0].Kind != KindMediaGC || st[0].Last == nil || st[0].Last.Summary.DidWork() {
		t.Fatalf("Statuses() = %+v, want the media_gc no-op round", st)
	}
	if st[0].NextRunAt.IsZero() || st[0].Interval != time.Hour {
		t.Fatalf("status timing = next %v interval %v", st[0].NextRunAt, st[0].Interval)
	}

	// A manual round that did work is recorded.
	if err := l.RunNow(); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	waitFor(t, "the manual round with work", func() bool { return len(rec.snapshot()) == 1 })
	got := rec.snapshot()[0]
	if got.Kind != KindMediaGC || got.Trigger != TriggerManual || got.Summary["removed"] != 3 || got.Err != nil {
		t.Fatalf("recorded round = %+v", got)
	}
	if got.FinishedAt.Before(got.StartedAt) {
		t.Fatalf("finished %v before started %v", got.FinishedAt, got.StartedAt)
	}

	// A manual round that did nothing is still recorded.
	results <- Summary{"removed": 0}
	waitFor(t, "the manual round to be accepted", func() bool { return l.RunNow() == nil })
	waitFor(t, "the manual no-op round", func() bool { return len(rec.snapshot()) == 2 })
	if r := rec.snapshot()[1]; r.Trigger != TriggerManual || r.Summary.DidWork() {
		t.Fatalf("second recorded round = %+v", r)
	}
}

// TestRegistryRecordsFailedRound: a failed timer round is recorded with its error.
func TestRegistryRecordsFailedRound(t *testing.T) {
	l, err := New(Config{
		Kind:        KindDigest,
		RunOnce:     func(context.Context) (Summary, error) { return nil, errors.New("boom") },
		Logger:      slog.New(discardHandler{}),
		FailMessage: "test run failed",
		Interval:    time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := &recorder{}
	if err := NewRegistry(rec.record).Add(l); err != nil {
		t.Fatalf("Add: %v", err)
	}
	l.Start(context.Background())
	defer l.Stop()
	waitFor(t, "the failed round", func() bool { return len(rec.snapshot()) == 1 })
	if r := rec.snapshot()[0]; r.Trigger != TriggerAuto || r.Err == nil || r.Summary == nil {
		t.Fatalf("recorded round = %+v, want an auto failure with an empty summary", r)
	}
}

// TestRunNowRefusals: before Start and after Stop there is no loop, and while a round runs or a manual round is
// pending a second request is refused.
func TestRunNowRefusals(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	l, err := New(Config{
		Kind: KindReminder,
		RunOnce: func(context.Context) (Summary, error) {
			entered <- struct{}{}
			<-release
			return nil, nil
		},
		Logger:      slog.New(discardHandler{}),
		FailMessage: "test run failed",
		Interval:    time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.RunNow(); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("RunNow before Start = %v, want ErrNotStarted", err)
	}
	l.Start(context.Background())
	<-entered // the startup round is running
	if !l.Status().Running {
		t.Fatal("Status().Running = false during a round")
	}
	if err := l.RunNow(); !errors.Is(err, ErrBusy) {
		t.Fatalf("RunNow while running = %v, want ErrBusy", err)
	}
	release <- struct{}{}
	waitFor(t, "the startup round to finish", func() bool { return !l.Status().Running })

	// The idle loop picks a manual request up at once, so the refusal is asserted while that manual round runs;
	// TestRunNowPending covers the refusal while a request is still pending.
	if err := l.RunNow(); err != nil {
		t.Fatalf("RunNow when idle = %v", err)
	}
	<-entered
	if err := l.RunNow(); !errors.Is(err, ErrBusy) {
		t.Fatalf("RunNow during the manual round = %v, want ErrBusy", err)
	}
	release <- struct{}{}
	waitFor(t, "the manual round to finish", func() bool {
		st := l.Status()
		return !st.Running && st.Last != nil && st.Last.Trigger == TriggerManual
	})

	l.Stop()
	if err := l.RunNow(); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("RunNow after Stop = %v, want ErrNotStarted", err)
	}
	if !l.Status().NextRunAt.IsZero() {
		t.Fatal("NextRunAt is set after Stop")
	}
}

// TestRunNowPending: a request made while the loop is busy elsewhere is pending until the loop picks it up, and a
// second request in that window is refused.
func TestRunNowPending(t *testing.T) {
	l, err := New(Config{
		Kind:        KindRetention,
		RunOnce:     func(context.Context) (Summary, error) { return nil, nil },
		Logger:      slog.New(discardHandler{}),
		FailMessage: "test run failed",
		Interval:    time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Mark the lifecycle started without a loop, so the request stays pending deterministically.
	l.mu.Lock()
	l.started = true
	l.mu.Unlock()
	if err := l.RunNow(); err != nil {
		t.Fatalf("first RunNow = %v", err)
	}
	if !l.Status().Pending {
		t.Fatal("Status().Pending = false after RunNow")
	}
	if err := l.RunNow(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second RunNow = %v, want ErrBusy", err)
	}
}

// TestRegistryAddRefusesBadKinds: an unknown kind and a duplicate kind are assembly errors.
func TestRegistryAddRefusesBadKinds(t *testing.T) {
	build := func(kind string) *Lifecycle {
		l, err := New(Config{
			Kind:        kind,
			RunOnce:     func(context.Context) (Summary, error) { return nil, nil },
			Logger:      slog.New(discardHandler{}),
			FailMessage: "x",
		})
		if err != nil {
			t.Fatalf("New(%q): %v", kind, err)
		}
		return l
	}
	reg := NewRegistry(nil)
	if err := reg.Add(build("optimize")); err == nil {
		t.Fatal("Add accepted a kind outside Kinds")
	}
	if err := reg.Add(build(KindDigest)); err != nil {
		t.Fatalf("Add(digest): %v", err)
	}
	if err := reg.Add(build(KindDigest)); err == nil {
		t.Fatal("Add accepted a duplicate kind")
	}
	if _, ok := reg.Get(KindDigest); !ok {
		t.Fatal("Get(digest) missing")
	}
	if _, ok := reg.Get(KindReminder); ok {
		t.Fatal("Get(reminder) found an unregistered kind")
	}
}

// TestRegistryStatusesOrder: statuses follow Kinds, not the order of Add.
func TestRegistryStatusesOrder(t *testing.T) {
	reg := NewRegistry(nil)
	for _, kind := range []string{KindMailDelivery, KindRetention, KindDigest} {
		l, err := New(Config{
			Kind:        kind,
			RunOnce:     func(context.Context) (Summary, error) { return nil, nil },
			Logger:      slog.New(discardHandler{}),
			FailMessage: "x",
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := reg.Add(l); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	var got []string
	for _, st := range reg.Statuses() {
		got = append(got, st.Kind)
	}
	want := []string{KindRetention, KindDigest, KindMailDelivery}
	if len(got) != len(want) {
		t.Fatalf("Statuses kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Statuses kinds = %v, want %v", got, want)
		}
	}
}
