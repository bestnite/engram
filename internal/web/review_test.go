package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedReviewDeck 建一个使用合法调度预设的卡组（M3-5 复习测试专用）。
// 与 seedDeck 的区别：走 store.NewPreset + PresetStore.Create，得到有效的保留率与最大间隔，
// 否则 FSRS 会在零值参数上产生无意义的调度。
func seedReviewDeck(t *testing.T, db *gorm.DB, ownerID uint64, name string) *store.Deck {
	t.Helper()
	preset := store.NewPreset(ownerID, "review preset")
	if err := store.NewPresetStore(db).Create(context.Background(), &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{OwnerUserID: ownerID, Name: name, Visibility: store.DeckVisibilityPrivate, PresetID: preset.ID}
	if err := store.NewDeckStore(db).Create(context.Background(), &deck); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return &deck
}

// attrValue 从 HTML 片段里取 name="<name>" 紧随的 value="..."。
// 复习页的隐藏字段是模板固定顺序渲染的，这里只需第一个匹配。
func attrValue(body, name string) string {
	marker := `name="` + name + `" value="`
	idx := strings.Index(body, marker)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// TestReviewWalkTwentyCardsWithServerReturnedNext 是 M3-5 的核心验收：
// 20 张新卡只靠 1 次 GET + 每张 1 次评分 POST 走完，评分响应本身带下一张卡，
// 计数器在每步都正确，且没有额外交互往返。
func TestReviewWalkTwentyCardsWithServerReturnedNext(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deck := seedReviewDeck(t, db, ownerID, "Walk deck")
	const total = 20
	for i := 1; i <= total; i++ {
		seedBasic(t, db, deck.ID, "Front"+pad3(i), "Back"+pad3(i))
	}

	requests := 0
	page := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
	requests++
	if page.Code != http.StatusOK {
		t.Fatalf("GET /review status = %d, want 200 (body %s)", page.Code, snippet(page.Body.String()))
	}
	body := page.Body.String()
	if !strings.Contains(body, `id="review-remaining" class="font-semibold text-slate-900">20<`) {
		t.Errorf("initial remaining counter is not 20: %s", snippet(body))
	}
	if !strings.Contains(body, `id="review-done" class="font-semibold text-slate-900">0<`) {
		t.Errorf("initial done counter is not 0: %s", snippet(body))
	}
	if !strings.Contains(body, "Front") || !strings.Contains(body, "review-form") {
		t.Fatalf("initial page does not render a card: %s", snippet(body))
	}

	seen := make(map[string]bool, total)
	for i := 1; i <= total; i++ {
		cardID := attrValue(body, "card_id")
		expected := attrValue(body, "expected_version")
		doneValue := attrValue(body, "done")
		if cardID == "" {
			t.Fatalf("step %d: page has no card_id (body %s)", i, snippet(body))
		}
		if seen[cardID] {
			t.Fatalf("step %d: card %s was already rated; the queue did not advance", i, cardID)
		}
		seen[cardID] = true

		rec := postForm(t, srv, "/review/answer", url.Values{
			"csrf_token":       {csrf},
			"card_id":          {cardID},
			"deck":             {u64str(deck.ID)},
			"rating":           {"3"},
			"expected_version": {expected},
			"done":             {doneValue},
			"elapsed_ms":       {"420"},
		}, cookies)
		requests++
		if rec.Code != http.StatusOK {
			t.Fatalf("step %d: POST /review/answer status = %d, want 200 (body %s)", i, rec.Code, snippet(rec.Body.String()))
		}
		body = rec.Body.String()
		if strings.Contains(body, "<!DOCTYPE") || strings.Contains(body, "<html") {
			t.Fatalf("step %d: rating response is a full page, want a main-area fragment", i)
		}
		if !strings.Contains(body, `id="review-area"`) {
			t.Fatalf("step %d: rating response has no review area fragment: %s", i, snippet(body))
		}

		wantRemaining := total - i
		if !strings.Contains(body, `id="review-remaining" class="font-semibold text-slate-900">`+itoa(wantRemaining)+`<`) {
			t.Errorf("step %d: remaining counter is not %d: %s", i, wantRemaining, snippet(body))
		}
		if !strings.Contains(body, `id="review-done" class="font-semibold text-slate-900">`+itoa(i)+`<`) {
			t.Errorf("step %d: done counter is not %d: %s", i, i, snippet(body))
		}
		t.Logf("step %d: rated card %s -> remaining=%d done=%d next=%s", i, cardID, wantRemaining, i, attrValue(body, "card_id"))

		if i < total {
			next := attrValue(body, "card_id")
			if next == "" || next == cardID {
				t.Fatalf("step %d: rating response did not carry the next card (card_id %q)", i, next)
			}
			if !strings.Contains(body, "Back") {
				t.Errorf("step %d: next card has no answer side in the fragment: %s", i, snippet(body))
			}
		} else {
			if attrValue(body, "card_id") != "" {
				t.Errorf("after the last rating the fragment still offers a card: %s", snippet(body))
			}
			if !strings.Contains(body, `id="review-remaining" class="font-semibold text-slate-900">0<`) {
				t.Errorf("after the last rating the remaining counter is not 0: %s", snippet(body))
			}
		}
	}

	if requests != total+1 {
		t.Errorf("HTTP requests = %d, want %d (1 GET + one rating POST per card, no extra round trip)", requests, total+1)
	}
	var reviews, states int64
	db.Model(&store.Review{}).Where("user_id = ?", ownerID).Count(&reviews)
	db.Model(&store.CardState{}).Where("user_id = ?", ownerID).Count(&states)
	if reviews != total || states != total {
		t.Errorf("persisted review/state rows = (%d, %d), want (%d, %d)", reviews, states, total, total)
	}
	t.Logf("walk complete: http_requests=%d (1 GET + %d rating POSTs), reviews=%d states=%d, final remaining=0 done=%d", requests, total, reviews, states, total)
}

// TestReviewAnswerRejectsStaleVersionWithoutSilence 覆盖负例：乐观锁冲突不得静默，
// 也不能改动任何进度；响应仍是主区域片段并带明确的错误提示。
func TestReviewAnswerRejectsStaleVersionWithoutSilence(t *testing.T) {
	srv, db, ownerID, cookies, csrf := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deck := seedReviewDeck(t, db, ownerID, "Conflict deck")
	seedBasic(t, db, deck.ID, "Front001", "Back001")

	page := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
	cardID := attrValue(page.Body.String(), "card_id")
	if cardID == "" {
		t.Fatalf("no card rendered: %s", snippet(page.Body.String()))
	}
	rec := postForm(t, srv, "/review/answer", url.Values{
		"csrf_token":       {csrf},
		"card_id":          {cardID},
		"deck":             {u64str(deck.ID)},
		"rating":           {"3"},
		"expected_version": {"99"},
		"done":             {"0"},
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("stale-version response status = %d, want 200 with an error fragment (body %s)", rec.Code, snippet(rec.Body.String()))
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="review-error"`) {
		t.Errorf("stale-version response does not surface an error: %s", snippet(body))
	}
	var reviews, states int64
	db.Model(&store.Review{}).Where("user_id = ?", ownerID).Count(&reviews)
	db.Model(&store.CardState{}).Where("user_id = ?", ownerID).Count(&states)
	if reviews != 0 || states != 0 {
		t.Errorf("a rejected submission wrote progress: reviews=%d states=%d, want 0/0", reviews, states)
	}
}

// TestReviewPageEmptyQueueShowsLocalizedHint 断言无卡可复习时给出提示而不是空白主区域。
func TestReviewPageEmptyQueueShowsLocalizedHint(t *testing.T) {
	srv, db, ownerID, cookies, _ := newNotesServer(t)
	// GET /review 已切到 SPA 应用壳；禁用 SPA 以覆盖 SSR 复习页（DESIGN.md §8.5）。
	srv.spa = nil
	deck := seedReviewDeck(t, db, ownerID, "Empty deck")

	rec := getWithCookies(t, srv, "/review?deck="+u64str(deck.ID), cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /review (empty) status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="review-remaining" class="font-semibold text-slate-900">0<`) {
		t.Errorf("empty queue remaining counter is not 0: %s", snippet(body))
	}
	if strings.Contains(body, "review-form") {
		t.Errorf("empty queue still renders the rating form: %s", snippet(body))
	}
	if !strings.Contains(body, "review-area") {
		t.Errorf("empty queue has no main area: %s", snippet(body))
	}
}
