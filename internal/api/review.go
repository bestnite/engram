package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"example.com/flashcard/internal/schedule"
	"example.com/flashcard/internal/store"
)

// schedulerForDeck 取卡组的调度器（由卡组预设构造）。
func (a *API) schedulerForDeck(ctx context.Context, deck *store.Deck) (*schedule.Scheduler, error) {
	preset, err := a.presets.ByID(ctx, deck.PresetID)
	if err != nil {
		return nil, err
	}
	return schedule.NewScheduler(preset)
}

// userLocation 按用户时区加载 Location；名字非法时退回 UTC（不阻断复习）。
func userLocation(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// dueCards 返回到期卡（含字段原文），scope: review（DESIGN.md §7.3）。
func (a *API) dueCards(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()

	var deck *store.Deck
	if raw := c.Query("deck"); raw != "" {
		deckID, ok := pathID(c, "deck")
		if !ok {
			return
		}
		d, ok := a.ownedDeck(c, deckID)
		if !ok {
			return
		}
		deck = d
	}
	limit := queryInt(c, "limit", 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}

	// 队列构建需要调度器（只为复习卡算 retrievability）；无卡组时用默认预设。
	var sched *schedule.Scheduler
	if deck != nil {
		s, err := a.schedulerForDeck(ctx, deck)
		if err != nil {
			abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load deck scheduler")
			return
		}
		sched = s
	} else {
		presetID, err := a.ensureDefaultPreset(ctx, u.ID)
		if err != nil {
			abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load default preset")
			return
		}
		preset, err := a.presets.ByID(ctx, presetID)
		if err != nil {
			abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load default preset")
			return
		}
		s, err := schedule.NewScheduler(preset)
		if err != nil {
			abortError(c, http.StatusInternalServerError, CodeInternal, "failed to build scheduler")
			return
		}
		sched = s
	}

	builder := schedule.NewQueueBuilder(a.db, sched)
	opts := schedule.QueueOptions{
		Now:           a.now(),
		Timezone:      u.Timezone,
		DayCutoffHour: u.DayCutoffHour,
		ReviewOrder:   schedule.OrderByDueAt,
		NewOrder:      schedule.NewOrderRandom,
	}
	if deck != nil {
		opts.DeckID = deck.ID
	}
	items, err := builder.Build(ctx, u.ID, opts)
	if err != nil {
		a.logger.Error("build due queue failed", "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to build review queue")
		return
	}
	if len(items) > limit {
		items = items[:limit]
	}

	out := make([]gin.H, 0, len(items))
	for _, it := range items {
		note, err := a.notes.ByID(ctx, it.NoteID)
		if err != nil {
			continue
		}
		entry := gin.H{
			"card_id":        it.CardID,
			"note_id":        it.NoteID,
			"deck_id":        it.DeckID,
			"state":          it.State.String(),
			"due_at":         it.DueAt,
			"retrievability": it.Retrievability,
			"kind":           note.Kind,
			"fields":         fieldsOrEmpty(note),
			"tags":           tagsOrEmpty(note),
		}
		if card, err := a.cards.ByID(ctx, it.CardID); err == nil {
			entry["template"] = card.Template
		}
		out = append(out, entry)
	}
	c.JSON(http.StatusOK, gin.H{"cards": out})
}

// submitReviewRequest 是评分提交请求体（DESIGN.md §3.4）。
type submitReviewRequest struct {
	CardID          uint64 `json:"card_id"`
	Rating          int    `json:"rating"`
	ExpectedVersion int    `json:"expected_version"`
	ElapsedMS       *int   `json:"elapsed_ms"`
	GradeSource     string `json:"grade_source"`
}

// submitReview 提交一次评分；乐观锁不匹配返回 409（DESIGN.md §3.4），scope: review。
func (a *API) submitReview(c *gin.Context) {
	u, _ := CurrentUser(c)
	ctx := c.Request.Context()
	var req submitReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "request body must be valid JSON")
		return
	}
	if req.CardID == 0 {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "card_id is required")
		return
	}
	if !schedule.Rating(req.Rating).Valid() {
		abortError(c, http.StatusBadRequest, CodeInvalidRequest, "rating must be between 1 and 4")
		return
	}

	card, err := a.cards.ByID(ctx, req.CardID)
	if err != nil {
		abortError(c, http.StatusNotFound, CodeNotFound, "card not found")
		return
	}
	note, err := a.notes.ByID(ctx, card.NoteID)
	if err != nil {
		abortError(c, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	deck, ok := a.ownedDeck(c, note.DeckID)
	if !ok {
		return
	}
	sched, err := a.schedulerForDeck(ctx, deck)
	if err != nil {
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to load deck scheduler")
		return
	}

	var result schedule.SubmitResult
	err = a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inner error
		result, inner = schedule.Submit(ctx, tx, schedule.SubmitInput{
			CardID:          req.CardID,
			UserID:          u.ID,
			Rating:          schedule.Rating(req.Rating),
			ExpectedVersion: req.ExpectedVersion,
			ElapsedMS:       req.ElapsedMS,
			GradeSource:     req.GradeSource,
			Scheduler:       sched,
			Now:             a.now(),
			Location:        userLocation(u.Timezone),
			Timezone:        u.Timezone,
			DayCutoffHour:   u.DayCutoffHour,
		})
		return inner
	})
	if err != nil {
		if errors.Is(err, schedule.ErrVersionConflict) {
			abortError(c, http.StatusConflict, CodeVersionConflict, "card state version conflict")
			return
		}
		a.logger.Error("submit review failed", "card_id", req.CardID, "user_id", u.ID, "error", err)
		abortError(c, http.StatusInternalServerError, CodeInternal, "failed to submit review")
		return
	}
	a.audit(ctx, store.AuditEntry{
		UserID:     store.Ptr(u.ID),
		APIKeyID:   CurrentAPIKeyID(c),
		Action:     "review.submit",
		TargetType: "card",
		TargetID:   store.Ptr(req.CardID),
		Detail:     map[string]any{"rating": req.Rating, "review_id": result.ReviewID},
	})
	c.JSON(http.StatusOK, gin.H{
		"card_id":   req.CardID,
		"review_id": result.ReviewID,
		"state":     result.State.State,
		"due_at":    result.State.DueAt,
		"version":   result.State.Version,
		"stability": result.State.Stability,
	})
}

// fieldsOrEmpty 解码 note.fields_json；解析失败返回空对象，避免响应里出现 null。
func fieldsOrEmpty(n *store.Note) map[string]any {
	fields, err := store.ParseFields(n.FieldsJSON)
	if err != nil || fields == nil {
		return map[string]any{}
	}
	return fields
}

// tagsOrEmpty 解码 note.tags_json；解析失败返回空数组。
func tagsOrEmpty(n *store.Note) []string {
	tags, err := store.ParseTags(n.TagsJSON)
	if err != nil || tags == nil {
		return []string{}
	}
	return tags
}
