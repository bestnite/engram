package schedule

import (
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// boolPtr 供测试给 store.Preset 的 *bool 字段显式赋值。
func boolPtr(b bool) *bool { return &b }

// testPreset 返回一个关闭 fuzz 的默认预设：fuzz 会随机化间隔，测试需要确定性。
func testPreset(t *testing.T) *store.Preset {
	t.Helper()
	p := store.NewPreset(1, "test")
	p.EnableFuzz = boolPtr(false)
	return &p
}

// newTestCardState 返回一张全新的卡（state=new）。
func newTestCardState() *store.CardState {
	return &store.CardState{CardID: 1, UserID: 1, State: "new"}
}

func mustScheduler(t *testing.T, preset *store.Preset) *Scheduler {
	t.Helper()
	s, err := NewScheduler(preset)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	return s
}

// TestPreviewReturnsFourRatings 断言 Repeat 预览返回四档选项，且新卡各档的状态推进符合状态机。
func TestPreviewReturnsFourRatings(t *testing.T) {
	s := mustScheduler(t, testPreset(t))
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	opts, err := s.Preview(newTestCardState(), now)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(opts) != 4 {
		t.Fatalf("Preview() returned %d options, want 4", len(opts))
	}
	wantRatings := []Rating{Again, Hard, Good, Easy}
	for i, want := range wantRatings {
		if opts[i].Rating != want {
			t.Errorf("Preview()[%d].Rating = %v, want %v", i, opts[i].Rating, want)
		}
	}
	// 学习步骤 1m,10m：Again/Hard/Good 留在 learning，Easy 直接毕业到 review。
	wantStates := []State{StateLearning, StateLearning, StateLearning, StateReview}
	for i, want := range wantStates {
		if opts[i].State != want {
			t.Errorf("Preview()[%d] (%v) state = %v, want %v", i, opts[i].Rating, opts[i].State, want)
		}
	}
	if !opts[0].Due.Equal(now.Add(time.Minute)) {
		t.Errorf("Again due = %v, want %v", opts[0].Due, now.Add(time.Minute))
	}
	if !opts[2].Due.Equal(now.Add(10 * time.Minute)) {
		t.Errorf("Good due = %v, want %v", opts[2].Due, now.Add(10*time.Minute))
	}
}

// TestNextAdvancesStateMachine 逐条覆盖状态机的转移：new / learning / review / relearning。
func TestNextAdvancesStateMachine(t *testing.T) {
	s := mustScheduler(t, testPreset(t))
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	reviewState := func() *store.CardState {
		stability := 5.0
		difficulty := 5.0
		last := now.Add(-10 * 24 * time.Hour)
		due := now.Add(-time.Hour)
		return &store.CardState{
			CardID: 2, UserID: 1, State: "review", Stability: &stability,
			Difficulty: &difficulty, LastReviewAt: &last, DueAt: &due,
			Reps: 3, ScheduledDays: 10,
		}
	}
	// 学习/再学习阶段的卡已有稳定性（首次评分后产生），否则 FSRS 会以非法状态拒绝。
	learningState := func(state string, stepIndex int) *store.CardState {
		stability := 1.0
		difficulty := 5.0
		last := now.Add(-10 * time.Minute)
		due := now.Add(time.Minute)
		return &store.CardState{
			CardID: 1, State: state, Stability: &stability, Difficulty: &difficulty,
			LastReviewAt: &last, DueAt: &due, StepIndex: stepIndex,
		}
	}

	tests := []struct {
		name      string
		state     *store.CardState
		rating    Rating
		wantState State
	}{
		{"new+Good stays learning", newTestCardState(), Good, StateLearning},
		{"new+Easy graduates to review", newTestCardState(), Easy, StateReview},
		{"new+Again stays learning", newTestCardState(), Again, StateLearning},
		{"learning last step +Good graduates", learningState("learning", 1), Good, StateReview},
		{"learning +Again restarts", learningState("learning", 2), Again, StateLearning},
		{"learning +Easy graduates", learningState("learning", 2), Easy, StateReview},
		{"relearning +Good graduates", learningState("relearning", 1), Good, StateReview},
		{"review +Again enters relearning", reviewState(), Again, StateRelearning},
		{"review +Good stays review", reviewState(), Good, StateReview},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Next(tc.state, now, tc.rating)
			if err != nil {
				t.Fatalf("Next() error = %v", err)
			}
			if got.State != tc.wantState {
				t.Errorf("Next(%v) state = %v, want %v", tc.rating, got.State, tc.wantState)
			}
			if !got.Due.After(now) {
				t.Errorf("Next(%v) due = %v, want a future time", tc.rating, got.Due)
			}
		})
	}
}

// TestNextRejectsInvalidRating 断言越界评分被英文错误拒绝，不做静默归一化。
func TestNextRejectsInvalidRating(t *testing.T) {
	s := mustScheduler(t, testPreset(t))
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for _, r := range []Rating{0, 5, -1} {
		if _, err := s.Next(newTestCardState(), now, r); err == nil {
			t.Errorf("Next(rating=%d) error = nil, want error", int(r))
		}
	}
}

// TestNewSchedulerUsesPresetWeights 断言 preset 的权重与学习步骤确实进了调度器。
func TestNewSchedulerUsesPresetWeights(t *testing.T) {
	p := testPreset(t)
	weights := "[0.5,1.5,2.5,3.5,4.5,5.5,6.5,0.05,1.0,0.2,0.8,1.5,0.06,0.3,1.6,0.6,1.9,0.5,0.1,0.07,0.15]"
	p.WeightsJSON = &weights
	s := mustScheduler(t, p)
	if got := s.fsrs.W[0]; got != 0.5 {
		t.Errorf("W[0] = %v, want 0.5 (from weights_json)", got)
	}

	// 空串学习步骤 = 关闭学习步骤，新卡直接进长间隔排程。
	off := testPreset(t)
	off.LearningSteps = ""
	sOff := mustScheduler(t, off)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	out, err := sOff.Next(newTestCardState(), now, Again)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if out.State != StateReview {
		t.Errorf("with learning steps off, new+Again state = %v, want review", out.State)
	}
}

// TestParseSteps 覆盖步骤解析的容错与拒绝路径。
func TestParseSteps(t *testing.T) {
	cases := []struct {
		in      string
		want    []float64
		wantErr bool
	}{
		{"1m,10m", []float64{1, 10}, false},
		{"", nil, false},
		{" 30s ", []float64{0.5}, false},
		{"1h", []float64{60}, false},
		{"1d", []float64{1440}, false},
		{"5", []float64{5}, false},
		{"abc", nil, true},
		{"1x", nil, true},
	}
	for _, tc := range cases {
		got, err := parseSteps(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseSteps(%q) error = nil, want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSteps(%q) error = %v", tc.in, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("parseSteps(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseSteps(%q)[%d] = %v, want %v", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}
