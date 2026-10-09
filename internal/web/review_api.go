package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

type reviewRequest struct {
	CardID          string   `json:"card_id"`
	Rating          int      `json:"rating"`
	ExpectedVersion int      `json:"expected_version"`
	ElapsedMS       *int     `json:"elapsed_ms"`
	Deck            []string `json:"deck"`
}

// gradeRequest 是 SPA 判分入口的请求体。作答类题型的评分由服务端判分器产生，
// 客户端只提交原始作答（Answer），绝不提交档位——否则就是自评冒充机器判分。
//
// Action 决定这次请求的语义：
//   - ""：正常判分，写 reviews（grade_source=typed）；
//   - "reveal"：只返回清洗后的正确答案，不判分、不写库（揭示前的只读预览）；
//   - "give_up"：已揭示答案后放弃作答，按 Again 记一条自评日志（grade_source=self）。
type gradeRequest struct {
	CardID          string          `json:"card_id"`
	ExpectedVersion int             `json:"expected_version"`
	ElapsedMS       *int            `json:"elapsed_ms"`
	Deck            []string        `json:"deck"`
	Action          string          `json:"action"`
	Answer          json.RawMessage `json:"answer"`
}

// reviewCardRequest 是 SPA 复习页两个只读/单动作入口的请求体：埋藏与卡面渲染。
// 两者都只带目标卡与卡组范围；范围原样带回，服务端据此重建队列。
type reviewCardRequest struct {
	CardID string   `json:"card_id"`
	Deck   []string `json:"deck"`
}

// reviewAnswer 为 SPA 提供会话 CSRF 保护的答题入口，业务提交与队列仍复用 API service。
func (s *Server) reviewAnswer(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || s.api == nil {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	_, _, deckIDs, ok := s.reviewCard(c, user, req.Deck, req.CardID)
	if !ok {
		return
	}
	// 作答类题型的自评分由 service 拒绝（grading_required）：SPA 对这类卡走 /api/v1/review/grade。
	result, err := s.api.SubmitReview(c.Request.Context(), user, nil, api.SubmitReviewInput{
		CardID: req.CardID, Rating: req.Rating, ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeReviewResult(c, user, deckIDs, result)
}

// reviewGrade 是作答类题型的 SPA 判分入口：会话 + CSRF 保护，服务端用题型判分器
// 计算档位并按 preset 的分数→档位映射写入 reviews（grade_source=typed、grade_detail_json）。
// 客户端提交原始作答，不提交档位；判分规则与 SSR 共用 graderFor / buildGradeInput。
func (s *Server) reviewGrade(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || s.api == nil {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req gradeRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	card, note, deckIDs, ok := s.reviewCard(c, user, req.Deck, req.CardID)
	if !ok {
		return
	}
	if _, graded := graderFor(note.Kind); !graded {
		// 自评类题型走错入口：拒绝，避免把四档评分伪装成机器判分。
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	switch req.Action {
	case gradeActionReveal:
		s.gradeReveal(c, user, card)
		return
	case gradeActionGiveUp:
		s.gradeGiveUp(c, user, req, deckIDs)
		return
	case "":
	default:
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	s.gradeSubmit(c, user, card, note, req, deckIDs)
}

// gradeSubmit 把作答交给 service 判分并写库，再把判分结果组装成页面反馈。
// 判分规则（作答解码、判分、分数→档位映射）全部在 service 与题型里，这里只做传输。
func (s *Server) gradeSubmit(c *gin.Context, user *store.User, card *store.Card, note *store.Note, req gradeRequest, deckIDs []uint64) {
	ctx := c.Request.Context()
	result, err := s.api.SubmitReview(ctx, user, nil, api.SubmitReviewInput{
		CardID: req.CardID, Answer: req.Answer, ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	feedback, err := s.gradeFeedback(ctx, user, card, result.Grade)
	if err != nil {
		s.logger.Error("build spa grade feedback failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	s.writeReviewResult(c, user, deckIDs, result, gin.H{"feedback": feedback})
}

// gradeReveal 返回清洗后的正确答案，不判分、不写库（揭示是只读预览）。
// 揭示后由前端切到「放弃作答」态，用户再点按钮才走 give_up 记 Again。
func (s *Server) gradeReveal(c *gin.Context, user *store.User, card *store.Card) {
	answerHTML, err := s.sanitizedBack(c.Request.Context(), user, card)
	if err != nil {
		s.logger.Error("render spa graded answer failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"revealed": true, "card_id": card.PublicID, "answer_html": answerHTML})
}

// gradeGiveUp 处理「已揭示答案，记 0 分并继续」：不判分，按 Again 记一条自评日志。
// grade_source 记 self —— 这次评分来自用户放弃作答，没有任何机器判分发生。
func (s *Server) gradeGiveUp(c *gin.Context, user *store.User, req gradeRequest, deckIDs []uint64) {
	result, err := s.api.SubmitReview(c.Request.Context(), user, nil, api.SubmitReviewInput{
		CardID: req.CardID, GiveUp: true, ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeReviewResult(c, user, deckIDs, result, gin.H{"gave_up": true})
}

// reviewBury 是埋藏的 SPA 入口：会话 + CSRF 保护，写本人 card_states.due_at（推到下一个
// 复习日），调度逻辑仍在 internal/schedule（schedule.Bury），transport 只做参数校验与事务边界
// （AGENTS.md §2.3.6：一种业务逻辑、两条传输）。埋藏只需 reader——它写的是 (card_id, user_id)
// 的本人进度，共享卡组的读者可自行复习。响应带同范围重建后的队列。
func (s *Server) reviewBury(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewCardRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	card, _, deckIDs, ok := s.reviewCard(c, user, req.Deck, req.CardID)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		s.logger.Error("begin spa bury transaction failed", "user_id", user.ID, "error", tx.Error)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	// 「下一个复习日」必须按用户自己的时区与日切点算：按 UTC 算时，UTC+8 的上午埋藏的卡
	// 当天中午就会回到队列。
	if _, err := schedule.Bury(ctx, tx, schedule.BuryInput{
		CardID: card.ID, UserID: user.ID, Now: time.Now().UTC(),
		Timezone: user.Timezone, DayCutoffHour: user.DayCutoffHour,
	}); err != nil {
		_ = tx.Rollback().Error
		s.logger.Error("bury review card failed", "user_id", user.ID, "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	if err := tx.Commit().Error; err != nil {
		s.logger.Error("commit spa bury failed", "user_id", user.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	s.writeQueue(c, user, deckIDs)
}

// reviewSuspend 是复习页「暂停这张卡」的 SPA 入口：会话 + CSRF 保护，业务在 service 的
// SetCardSuspended（只写本人进度，reader 即可）；响应带同范围重建后的队列。
func (s *Server) reviewSuspend(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewCardRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	_, _, deckIDs, ok := s.reviewCard(c, user, req.Deck, req.CardID)
	if !ok {
		return
	}
	if _, err := s.api.SetCardSuspended(c.Request.Context(), user, nil, req.CardID, true); err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeQueue(c, user, deckIDs)
}

// reviewUndo 是「撤销刚提交的评分」的 SPA 入口：会话 + CSRF 保护，业务在 service 的
// UndoReview（内部调 schedule.Rollback）。判权用 reader —— 撤销写的是本人的
// (card_id, user_id) 进度，共享卡组的读者可撤销自己刚做的评分；卡组范围里任一卡组
// 不可读即整次失败（复用 reviewCard，与埋藏同口径）。
//
// 响应契约：除同范围重建的队列（cards/remaining）外，显式带上被撤销卡的对外 id
// （undone_card_id）。队列按 due_at 排序，被撤销的卡不保证排在首位，前端必须据
// undone_card_id 把当前卡定位回它，不能假定它出现在队列首位。
func (s *Server) reviewUndo(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewCardRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	_, _, deckIDs, ok := s.reviewCard(c, user, req.Deck, req.CardID)
	if !ok {
		return
	}
	result, err := s.api.UndoReview(c.Request.Context(), user, api.UndoReviewInput{CardID: req.CardID})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeQueue(c, user, deckIDs, gin.H{"undone_card_id": result.CardID})
}

// reviewRender 返回一张卡正反面的服务端清洗 HTML：SPA 只把这里返回的
// HTML 交给 {@html}，绝不把 fields 原文当 Markdown 送进 HTML 汇。复用 cardView，保证与 SSR
// 走同一条 goldmark → bluemonday 清洗路径；edit_href 供复习页的编辑入口跳转。
func (s *Server) reviewRender(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok {
		writeRenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req reviewCardRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.CardID) == "" {
		writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	card, _, _, ok := s.reviewCard(c, user, req.Deck, req.CardID)
	if !ok {
		return
	}
	view, err := s.cardView(c.Request.Context(), user, schedule.QueueItem{CardID: card.ID})
	if err != nil {
		s.logger.Error("render spa review card failed", "card_id", card.ID, "error", err)
		writeRenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"card_id":    card.PublicID,
		"front_html": view.FrontHTML,
		"back_html":  view.BackHTML,
		"edit_href":  view.EditHref,
	})
}

// reviewCard 校验 SPA 请求的卡组范围与目标卡：范围里每个卡组都要可读（缺一即整次失败，
// 不静默丢弃），目标卡必须存在且落在范围内。cardPublicID 与 deckPublicIDs 都是对外 id，
// 内部一律换成数字主键再判定；失败时已写出响应并返回 false，成功时返回范围对应的数字卡组 id。
func (s *Server) reviewCard(c *gin.Context, user *store.User, deckPublicIDs []string, cardPublicID string) (*store.Card, *store.Note, []uint64, bool) {
	scope, ok := s.deckScopeFromValues(c, user, deckPublicIDs)
	if !ok {
		return nil, nil, nil, false
	}
	card, err := s.cards.ByPublicID(c.Request.Context(), strings.TrimSpace(cardPublicID))
	if err != nil {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return nil, nil, nil, false
	}
	note, err := s.notes.ByID(c.Request.Context(), card.NoteID)
	if err != nil {
		writeRenderError(c, http.StatusNotFound, api.CodeNotFound)
		return nil, nil, nil, false
	}
	if len(scope.deckIDs) > 0 {
		inScope := false
		for _, deckID := range scope.deckIDs {
			if note.DeckID == deckID {
				inScope = true
				break
			}
		}
		if !inScope {
			writeRenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
			return nil, nil, nil, false
		}
	}
	return card, note, scope.deckIDs, true
}

// writeReviewResult 写出一次评分后的统一响应：新状态 + 同范围队列（预取下一张），
// 外加调用方附加的字段（判分反馈 / 放弃标记）。范围原样带回，队列不会退化成单卡组。
func (s *Server) writeReviewResult(c *gin.Context, user *store.User, deckIDs []uint64, result api.SubmitReviewResult, extra ...gin.H) {
	cards, err := s.api.DueCards(c.Request.Context(), user, deckIDs, 500)
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	body := gin.H{"card_id": result.CardID, "state": result.State,
		"due_at": result.DueAt, "version": result.Version, "stability": result.Stability,
		"cards": cards, "remaining": len(cards)}
	for _, item := range extra {
		for k, v := range item {
			body[k] = v
		}
	}
	c.JSON(http.StatusOK, body)
}

// writeQueue 只返回同范围重建后的队列（无评分状态字段），供埋藏这类不产生 reviews 行的
// 动作使用：客户端据此换到下一张卡，队列范围不会退化成单卡组。
// extra 是调用方附加的字段（如撤销时的 undone_card_id），按写入顺序并入响应体。
func (s *Server) writeQueue(c *gin.Context, user *store.User, deckIDs []uint64, extra ...gin.H) {
	cards, err := s.api.DueCards(c.Request.Context(), user, deckIDs, 500)
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	body := gin.H{"cards": cards, "remaining": len(cards)}
	for _, item := range extra {
		for k, v := range item {
			body[k] = v
		}
	}
	c.JSON(http.StatusOK, body)
}

// gradeFeedback 组装判分反馈：判定、得分与作答文本来自 service 的判分结果，正确答案用
// 服务端清洗后的 HTML；数值题额外带解析出的数值。
func (s *Server) gradeFeedback(ctx context.Context, user *store.User, card *store.Card, grade *api.GradeResult) (gin.H, error) {
	if grade == nil {
		return nil, errors.New("graded submission returned no grade result")
	}
	answerHTML, err := s.sanitizedBack(ctx, user, card)
	if err != nil {
		return nil, err
	}
	out := gin.H{
		"verdict":     grade.Verdict,
		"score":       grade.Score,
		"rating":      grade.Rating,
		"answer_html": answerHTML,
		"given":       grade.Given,
	}
	if parsed, ok := grade.Detail["parsed_answer"]; ok {
		out["parsed"] = numberText(parsed)
	}
	return out, nil
}

// sanitizedBack 渲染一张卡的背面并经 internal/render 清洗，供 SPA 展示正确答案。
// 复用 cardView，保证与 SSR 走同一条清洗路径。
func (s *Server) sanitizedBack(ctx context.Context, user *store.User, card *store.Card) (string, error) {
	view, err := s.cardView(ctx, user, schedule.QueueItem{CardID: card.ID})
	if err != nil {
		return "", err
	}
	return view.BackHTML, nil
}

// gradeAction* 是判分请求的三种语义（见 gradeRequest）。
const (
	gradeActionReveal = "reveal"
	gradeActionGiveUp = "give_up"
)

func apiError(err error) *api.ServiceError {
	var se *api.ServiceError
	if errors.As(err, &se) {
		return se
	}
	return &api.ServiceError{Status: http.StatusInternalServerError, Code: api.CodeInternal, Message: api.ErrorMessage(context.Background(), api.CodeInternal)}
}
