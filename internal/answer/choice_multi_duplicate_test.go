package answer

import "testing"

// A correct-index list that repeats an index names one correct option, not two,
// so the score denominator must count the distinct correct options.
func TestChoiceMultiDuplicateCorrectIndexScoresFullCredit(t *testing.T) {
	fields := map[string]string{"options": "a<br>b<br>c", "answers": "0<br>0"}
	result, err := runGrade(t, "choice_multi", fields, `[0]`, 0)
	if err != nil {
		t.Fatalf("Grade: %v", err)
	}
	if got, want := result.Score, 1.0; got != want {
		t.Fatalf("score = %v, want %v (detail %v)", got, want, result.Detail)
	}
	if got, want := result.Rating, RatingGood; got != want {
		t.Fatalf("rating = %d, want %d (detail %v)", got, want, result.Detail)
	}
	assertDetail(t, result.Detail, map[string]any{
		"answers": []int{0}, "selected": []int{0}, "hits": 1, "misses": 0,
	})
}
