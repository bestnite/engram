package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/i18n"
)

// TestIntervalLabelPicksSensibleUnit 覆盖单位切换，以及"取整顶过单位边界"的修正
// （59.7 分不该渲染成「60 分钟」）。
func TestIntervalLabelPicksSensibleUnit(t *testing.T) {
	tr, err := i18n.New()
	if err != nil {
		t.Fatalf("i18n.New: %v", err)
	}
	loc := tr.Localizer(tr.Pick("zh-CN", "", ""))
	cases := []struct {
		wait time.Duration
		want string
	}{
		{30 * time.Second, "<1 分钟"},
		{time.Minute, "1 分钟"},
		{10 * time.Minute, "10 分钟"},
		{59*time.Minute + 40*time.Second, "1 小时"},
		{2 * time.Hour, "2 小时"},
		{23*time.Hour + 50*time.Minute, "1 天"},
		{3 * 24 * time.Hour, "3 天"},
		{49 * 24 * time.Hour, "2 个月"},
		{400 * 24 * time.Hour, "1 年"},
		{800 * 24 * time.Hour, "2 年"},
	}
	for _, c := range cases {
		if got := intervalLabel(loc, c.wait); got != c.want {
			t.Errorf("intervalLabel(%v) = %q, want %q", c.wait, got, c.want)
		}
	}
}

// TestReviewPageShowsNextIntervalOnRatingButtons 是这条改动的验收（DESIGN.md §8.2）：
// 自评类题型的四档按钮各自显示"下次什么时候回来"。默认预设的学习步骤是 1m,10m，
// 所以一张全新卡片的 Again 应显示 1 分钟、Good 应显示 10 分钟 —— 正是"刚答完几分钟后
// 又冒出来"这件事，现在在答题之前就能看到。
func TestReviewPageShowsNextIntervalOnRatingButtons(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	deck := seedReviewDeck(t, db, ownerID, "Interval deck")
	seedBasic(t, db, deck.ID, "Front", "Back")

	page := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /review status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	body := page.Body.String()
	if !strings.Contains(body, `id="review-ratings"`) {
		t.Fatalf("review page rendered no rating block: %s", snippet(body))
	}
	// 四档按钮各带一句时长。
	if n := strings.Count(body, `class="mt-0.5 text-[11px] font-normal text-zinc-500 dark:text-zinc-400"`); n != 4 {
		t.Errorf("rating buttons carrying an interval = %d, want 4", n)
	}
	for _, want := range []string{"1 分钟", "10 分钟"} {
		if !strings.Contains(body, want) {
			t.Errorf("rating buttons are missing the %q interval: %s", want, ratingBlock(body))
		}
	}
}

// ratingBlock 截出评分按钮区块，便于失败时看清渲染了什么。
func ratingBlock(body string) string {
	i := strings.Index(body, `id="review-ratings"`)
	if i < 0 {
		return body
	}
	end := i + 900
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}
