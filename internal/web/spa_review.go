package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/api"
	"git.nite07.com/nite/engram/internal/auth"
	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

type spaReviewRequest struct {
	CardID          uint64   `json:"card_id"`
	Rating          int      `json:"rating"`
	ExpectedVersion int      `json:"expected_version"`
	ElapsedMS       *int     `json:"elapsed_ms"`
	Deck            []uint64 `json:"deck"`
}

// spaGradeRequest 是 SPA 判分入口的请求体。作答类题型的评分由服务端判分器产生，
// 客户端只提交原始作答（Answer），绝不提交档位——否则就是自评冒充机器判分（DESIGN.md §6.2、§8.2）。
//
// Action 决定这次请求的语义：
//   - ""：正常判分，写 reviews（grade_source=typed）；
//   - "reveal"：只返回清洗后的正确答案，不判分、不写库（揭示前的只读预览）；
//   - "give_up"：已揭示答案后放弃作答，按 Again 记一条自评日志（grade_source=self）。
type spaGradeRequest struct {
	CardID          uint64          `json:"card_id"`
	ExpectedVersion int             `json:"expected_version"`
	ElapsedMS       *int            `json:"elapsed_ms"`
	Deck            []uint64        `json:"deck"`
	Action          string          `json:"action"`
	Answer          json.RawMessage `json:"answer"`
}

// spaReviewAnswer 为 SPA 提供会话 CSRF 保护的答题入口，业务提交与队列仍复用 API service。
func (s *Server) spaReviewAnswer(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || s.api == nil {
		writeSPARenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req spaReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.CardID == 0 {
		writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	_, note, ok := s.spaReviewCard(c, user, req.Deck, req.CardID)
	if !ok {
		return
	}
	// 作答类题型不得走自评入口：那会把机器判分的评分权交回客户端（DESIGN.md §8.2）。
	// SPA 对这类卡改走 /api/v1/review/grade。
	if _, graded := graderFor(note.Kind); graded {
		writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	result, err := s.api.SubmitReview(c.Request.Context(), user, nil, api.SubmitReviewInput{
		CardID: req.CardID, Rating: req.Rating, ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS, GradeSource: schedule.GradeSourceSelf,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeSPAReviewResult(c, user, req.Deck, result)
}

// spaReviewGrade 是作答类题型的 SPA 判分入口：会话 + CSRF 保护，服务端用题型判分器
// 计算档位并按 preset 的分数→档位映射写入 reviews（grade_source=typed、grade_detail_json）。
// 客户端提交原始作答，不提交档位；判分规则与 SSR 共用 graderFor / buildGradeInput（DESIGN.md §3.4、§6.2）。
func (s *Server) spaReviewGrade(c *gin.Context) {
	user, ok := auth.CurrentUser(c)
	if !ok || s.api == nil {
		writeSPARenderError(c, http.StatusUnauthorized, api.CodeUnauthorized)
		return
	}
	var req spaGradeRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.CardID == 0 {
		writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	card, note, ok := s.spaReviewCard(c, user, req.Deck, req.CardID)
	if !ok {
		return
	}
	if _, graded := graderFor(note.Kind); !graded {
		// 自评类题型走错入口：拒绝，避免把四档评分伪装成机器判分。
		writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	switch req.Action {
	case spaGradeActionReveal:
		s.spaGradeReveal(c, user, card)
		return
	case spaGradeActionGiveUp:
		s.spaGradeGiveUp(c, user, req)
		return
	case "":
	default:
		writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	s.spaGradeSubmit(c, user, card, note, req)
}

// spaGradeSubmit 执行一次真正的机器判分：构造判分输入、判分、按映射得档位，单事务写库。
func (s *Server) spaGradeSubmit(c *gin.Context, user *store.User, card *store.Card, note *store.Note, req spaGradeRequest) {
	ctx := c.Request.Context()
	g, _ := graderFor(note.Kind)
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		s.logger.Error("parse note fields for spa grading failed", "note_id", note.ID, "error", err)
		writeSPARenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	mapping := s.gradeMappingFor(ctx, note.DeckID)
	form, err := spaGradeAnswerToForm(note.Kind, req.Answer)
	if err != nil {
		writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	input, err := buildGradeInput(note.Kind, fields, mapping, form)
	if err != nil {
		writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	rating, detail, ok := g.Grade(input)
	if !ok {
		// 输入无法判分（如索引越界）不写库，让用户重新作答。
		writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
		return
	}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		s.logger.Error("marshal spa grade detail failed", "note_id", note.ID, "error", err)
		writeSPARenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	raw := string(detailJSON)
	result, err := s.api.SubmitReview(ctx, user, nil, api.SubmitReviewInput{
		CardID: req.CardID, Rating: rating, ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS, GradeSource: schedule.GradeSourceTyped, GradeDetailJSON: &raw,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	feedback, err := s.spaGradeFeedback(ctx, user, note, card, fields, detail, mapping)
	if err != nil {
		s.logger.Error("build spa grade feedback failed", "card_id", card.ID, "error", err)
		writeSPARenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	s.writeSPAReviewResult(c, user, req.Deck, result, gin.H{"feedback": feedback})
}

// spaGradeReveal 返回清洗后的正确答案，不判分、不写库（揭示是只读预览）。
// 揭示后由前端切到「放弃作答」态，用户再点按钮才走 give_up 记 Again（DESIGN.md §8.2）。
func (s *Server) spaGradeReveal(c *gin.Context, user *store.User, card *store.Card) {
	answerHTML, err := s.spaSanitizedBack(c.Request.Context(), user, card)
	if err != nil {
		s.logger.Error("render spa graded answer failed", "card_id", card.ID, "error", err)
		writeSPARenderError(c, http.StatusInternalServerError, api.CodeInternal)
		return
	}
	c.JSON(http.StatusOK, gin.H{"revealed": true, "card_id": card.ID, "answer_html": answerHTML})
}

// spaGradeGiveUp 处理「已揭示答案，记 0 分并继续」：不判分，按 Again 记一条自评日志。
// grade_source 记 self —— 这次评分来自用户放弃作答，没有任何机器判分发生（DESIGN.md §8.2）。
func (s *Server) spaGradeGiveUp(c *gin.Context, user *store.User, req spaGradeRequest) {
	result, err := s.api.SubmitReview(c.Request.Context(), user, nil, api.SubmitReviewInput{
		CardID: req.CardID, Rating: int(schedule.Again), ExpectedVersion: req.ExpectedVersion,
		ElapsedMS: req.ElapsedMS, GradeSource: schedule.GradeSourceSelf,
	})
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	s.writeSPAReviewResult(c, user, req.Deck, result, gin.H{"gave_up": true})
}

// spaReviewCard 校验 SPA 请求的卡组范围与目标卡：范围里每个卡组都要可读（缺一即整次失败，
// 不静默丢弃），目标卡必须存在且落在范围内。失败时已写出响应并返回 false。
func (s *Server) spaReviewCard(c *gin.Context, user *store.User, deckIDs []uint64, cardID uint64) (*store.Card, *store.Note, bool) {
	for _, deckID := range deckIDs {
		if deckID == 0 {
			writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
			return nil, nil, false
		}
		if _, ok := s.loadDeckForRole(c, user, deckID, store.RoleReader); !ok {
			return nil, nil, false
		}
	}
	card, err := s.cards.ByID(c.Request.Context(), cardID)
	if err != nil {
		writeSPARenderError(c, http.StatusNotFound, api.CodeNotFound)
		return nil, nil, false
	}
	note, err := s.notes.ByID(c.Request.Context(), card.NoteID)
	if err != nil {
		writeSPARenderError(c, http.StatusNotFound, api.CodeNotFound)
		return nil, nil, false
	}
	if len(deckIDs) > 0 {
		inScope := false
		for _, deckID := range deckIDs {
			if note.DeckID == deckID {
				inScope = true
				break
			}
		}
		if !inScope {
			writeSPARenderError(c, http.StatusBadRequest, api.CodeInvalidRequest)
			return nil, nil, false
		}
	}
	return card, note, true
}

// writeSPAReviewResult 写出一次评分后的统一响应：新状态 + 同范围队列（预取下一张），
// 外加调用方附加的字段（判分反馈 / 放弃标记）。范围原样带回，队列不会退化成单卡组。
func (s *Server) writeSPAReviewResult(c *gin.Context, user *store.User, deckIDs []uint64, result api.SubmitReviewResult, extra ...gin.H) {
	cards, err := s.api.DueCards(c.Request.Context(), user, deckIDs, 500)
	if err != nil {
		se := apiError(err)
		c.AbortWithStatusJSON(se.Status, gin.H{"error": gin.H{"code": se.Code, "message": se.Message}})
		return
	}
	body := gin.H{"card_id": result.CardID, "review_id": result.ReviewID, "state": result.State,
		"due_at": result.DueAt, "version": result.Version, "stability": result.Stability,
		"cards": cards, "remaining": len(cards)}
	for _, item := range extra {
		for k, v := range item {
			body[k] = v
		}
	}
	c.JSON(http.StatusOK, body)
}

// spaGradeFeedback 组装判分反馈：判定来自分数与映射阈值，正确答案用服务端清洗后的 HTML，
// 作答文本按题型还原成可读串（选项索引转成选项文本，判分细节不直接暴露给前端）。
func (s *Server) spaGradeFeedback(ctx context.Context, user *store.User, note *store.Note, card *store.Card, fields map[string]any, detail map[string]any, mapping *cardtype.GradeMapping) (gin.H, error) {
	answerHTML, err := s.spaSanitizedBack(ctx, user, card)
	if err != nil {
		return nil, err
	}
	m := cardtype.DefaultGradeMapping()
	if mapping != nil {
		m = *mapping
	}
	score := 0.0
	if v, ok := detail["score"].(float64); ok {
		score = v
	}
	verdict := "incorrect"
	if score >= m.FullThreshold {
		verdict = "correct"
	} else if score > m.NoneThreshold {
		verdict = "partial"
	}
	rating := 0
	if v, ok := detail["rating"].(int); ok {
		rating = v
	}
	out := gin.H{
		"verdict":     verdict,
		"score":       score,
		"rating":      rating,
		"answer_html": answerHTML,
		"given":       spaGradeGivenText(note.Kind, fields, detail),
	}
	if parsed, ok := detail["parsed_answer"]; ok {
		out["parsed"] = numberText(parsed)
	}
	return out, nil
}

// spaSanitizedBack 渲染一张卡的背面并经 internal/render 清洗，供 SPA 展示正确答案。
// 复用 cardView，保证与 SSR 走同一条清洗路径（DESIGN.md §6.1）。
func (s *Server) spaSanitizedBack(ctx context.Context, user *store.User, card *store.Card) (string, error) {
	view, err := s.cardView(ctx, user, schedule.QueueItem{CardID: card.ID})
	if err != nil {
		return "", err
	}
	return view.BackHTML, nil
}

// spaGradeGivenText 把判分细节还原成展示用的作答文本：选项索引转成选项文本，
// 判断题返回 "true"/"false"（前端负责本地化），其余直接取判分器记录的 given。
func spaGradeGivenText(kind string, fields map[string]any, detail map[string]any) string {
	switch kind {
	case "typed", "numeric":
		given, _ := detail["given"].(string)
		return given
	case "choice_single":
		opts := optionTexts(fields)
		if idx, ok := detail["selected"].(int); ok && idx >= 0 && idx < len(opts) {
			return opts[idx]
		}
		return ""
	case "choice_multi":
		opts := optionTexts(fields)
		picked := make([]string, 0, len(opts))
		if idxs, ok := detail["selected"].([]int); ok {
			for _, idx := range idxs {
				if idx >= 0 && idx < len(opts) {
					picked = append(picked, opts[idx])
				}
			}
		}
		return strings.Join(picked, ", ")
	case "true_false":
		if b, ok := detail["selected"].(bool); ok {
			return strconv.FormatBool(b)
		}
		return ""
	default:
		return ""
	}
}

// spaGradeAction* 是判分请求的三种语义（见 spaGradeRequest）。
const (
	spaGradeActionReveal = "reveal"
	spaGradeActionGiveUp = "give_up"
)

// spaGradeAnswerToForm 把 SPA 的 JSON 作答归一化成表单值，使 SPA 与 SSR 共用同一份
// buildGradeInput 映射（判分输入规则只有一处）。缺作答时返回空表单：对 typed/choice_multi
// 而言空作答是合法的错误答案（记 0 分），对 choice_single/true_false 则由 buildGradeInput 拒绝。
func spaGradeAnswerToForm(kind string, raw json.RawMessage) (url.Values, error) {
	form := url.Values{}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return form, nil
	}
	switch kind {
	case "typed":
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("typed answer must be a string")
		}
		form.Set("answer", s)
	case "numeric":
		// 数值作答通常是字符串（可能带单位）；也接受裸 JSON 数字，原样保留文本。
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			form.Set("answer", s)
			break
		}
		if !json.Valid(trimmed) {
			return nil, fmt.Errorf("numeric answer must be a string or number")
		}
		form.Set("answer", string(trimmed))
	case "choice_single":
		var idx int
		if err := json.Unmarshal(trimmed, &idx); err == nil {
			form.Set("answer", strconv.Itoa(idx))
			break
		}
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("choice_single answer must be an option index")
		}
		form.Set("answer", s)
	case "choice_multi":
		var idxs []int
		if err := json.Unmarshal(trimmed, &idxs); err != nil {
			return nil, fmt.Errorf("choice_multi answer must be an array of option indices")
		}
		for _, idx := range idxs {
			form.Add("answer", strconv.Itoa(idx))
		}
	case "true_false":
		var b bool
		if err := json.Unmarshal(trimmed, &b); err != nil {
			return nil, fmt.Errorf("true_false answer must be a boolean")
		}
		form.Set("answer", strconv.FormatBool(b))
	default:
		return nil, fmt.Errorf("card type %q has no grading input", kind)
	}
	return form, nil
}

func apiError(err error) *api.ServiceError {
	var se *api.ServiceError
	if errors.As(err, &se) {
		return se
	}
	return &api.ServiceError{Status: http.StatusInternalServerError, Code: api.CodeInternal, Message: api.ErrorMessage(context.Background(), api.CodeInternal)}
}
