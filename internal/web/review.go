package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
	"git.nite07.com/nite/engram/internal/web/views"
)

// 复习页的规范路径（M3-5）。评分与动作走 SPA 的同源 JSON 端点（/api/v1/review/*）。
const reviewPagePath = "/review"

// registerReviewRoutes 挂载复习页。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerReviewRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil || s.cards == nil || s.presets == nil {
		return
	}
	// GET /review 返回应用壳：reviewPageRoute 先按原有的会话与卡组范围判定鉴权，再交出应用壳，
	// 由客户端路由渲染复习页（DESIGN.md §8.1、§8.2、§8.5）。评分与动作走 SPA 的 JSON 端点
	// （PATCH/POST /api/v1/review/*），不再有分离的 htmx 评分端点。
	router.GET(reviewPagePath, s.reviewPageRoute)
}

// reviewPageRoute 提供 GET /review：返回应用壳，由客户端路由渲染复习页，
// 数据仍走既有 JSON 端点（DESIGN.md §8.1、§8.2）。会话与卡组范围判定先于切壳执行——
// 非法 deck 参数 400、范围里出现读不到的卡组 403/404，不因返回应用壳而放行（DESIGN.md §8.2）。
func (s *Server) reviewPageRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	if _, ok := s.parseDeckScope(c, user); !ok {
		return
	}
	s.spa.ServeIndex(c)
}

// reviewScope 是一次复习请求的卡组范围：deckIDs 为空表示全库（不按卡组过滤）。
// 范围由 URL 的可重复 deck 参数决定，并由 SPA 的 JSON 请求原样带回（DESIGN.md §8.2）。
type reviewScope struct{ deckIDs []uint64 }

// deckScopeFromValues 解析可重复的 deck 参数值：去重、跳过空串；任一值非数字或为 0
// 即写 400 并返回 false。随后逐个校验 loadDeckForRole(..., RoleReader)：任何缺失或
// 无权限的卡组都让整次请求失败（404/403，由该 helper 写出），绝不静默丢弃某个卡组。
func (s *Server) deckScopeFromValues(c *gin.Context, user *store.User, raw []string) (reviewScope, bool) {
	ids := make([]uint64, 0, len(raw))
	seen := make(map[uint64]bool, len(raw))
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil || id == 0 {
			c.AbortWithStatus(http.StatusBadRequest)
			return reviewScope{}, false
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, id := range ids {
		if _, ok := s.loadDeckForRole(c, user, id, store.RoleReader); !ok {
			return reviewScope{}, false
		}
	}
	return reviewScope{deckIDs: ids}, true
}

// parseDeckScope 解析 GET 查询串里的可重复 deck 参数（复习页整页请求）。
func (s *Server) parseDeckScope(c *gin.Context, user *store.User) (reviewScope, bool) {
	return s.deckScopeFromValues(c, user, c.QueryArray("deck"))
}

// schedulerFor 取卡组预设构造调度器；只有恰好指定一个卡组时才用它的预设，
// 多卡组与全库回退到文档化默认参数（DESIGN.md §3.3）。无卡组/预设缺失同样回退默认。
func (s *Server) schedulerFor(ctx context.Context, userID uint64, deckIDs []uint64) (*schedule.Scheduler, error) {
	var preset *store.Preset
	if len(deckIDs) == 1 {
		if deck, err := s.decks.ByID(ctx, deckIDs[0]); err == nil && deck.PresetID != 0 {
			if p, err := s.presets.ByID(ctx, deck.PresetID); err == nil {
				preset = p
			}
		}
	}
	if preset == nil {
		def := store.NewPreset(userID, store.DefaultPresetName)
		preset = &def
	}
	return schedule.NewScheduler(preset)
}

// cardView 渲染一张卡的正反面并取乐观锁版本。
func (s *Server) cardView(ctx context.Context, user *store.User, item schedule.QueueItem) (*views.ReviewCardView, error) {
	card, err := s.cards.ByID(ctx, item.CardID)
	if err != nil {
		return nil, err
	}
	note, err := s.notes.ByID(ctx, card.NoteID)
	if err != nil {
		return nil, err
	}
	t, ok := cardtype.Lookup(note.Kind)
	if !ok {
		return nil, cardtype.UnknownKindError(note.Kind)
	}
	fields, err := store.ParseFields(note.FieldsJSON)
	if err != nil {
		return nil, err
	}
	ct := cardtype.Card{Template: card.Template, Ordinal: card.Ordinal, Fields: fields}
	front, err := t.Render(ct, cardtype.SideFront)
	if err != nil {
		return nil, err
	}
	back, err := t.Render(ct, cardtype.SideBack)
	if err != nil {
		return nil, err
	}
	frontHTML, err := renderSide(front)
	if err != nil {
		return nil, err
	}
	backHTML, err := renderSide(back)
	if err != nil {
		return nil, err
	}
	return &views.ReviewCardView{
		CardID:          strconv.FormatUint(card.ID, 10),
		NoteID:          strconv.FormatUint(note.ID, 10),
		DeckID:          strconv.FormatUint(note.DeckID, 10),
		ExpectedVersion: strconv.Itoa(s.stateVersion(ctx, user.ID, card.ID)),
		Template:        card.Template,
		FrontHTML:       frontHTML,
		BackHTML:        backHTML,
		EditHref:        "/decks/" + strconv.FormatUint(note.DeckID, 10) + "/notes/" + strconv.FormatUint(note.ID, 10),
	}, nil
}

// stateVersion 读取 (card_id, user_id) 的乐观锁版本；没有状态行的新卡返回 0（DESIGN.md §3.4）。
func (s *Server) stateVersion(ctx context.Context, userID, cardID uint64) int {
	var st store.CardState
	err := s.db.WithContext(ctx).Where("card_id = ? AND user_id = ?", cardID, userID).Take(&st).Error
	if err != nil {
		return 0
	}
	return st.Version
}

// graderFor 用可选窄接口断言判断题型是否支持机器判分（DESIGN.md §6.2、M3-12）。
// 判分能力是可选能力，核心管线不依赖它：未实现 Grader 的题型返回 false，仍走四档自评。
func graderFor(kind string) (cardtype.Grader, bool) {
	t, ok := cardtype.Lookup(kind)
	if !ok {
		return nil, false
	}
	g, ok := t.(cardtype.Grader)
	return g, ok
}

// gradeMappingFor 取卡组预设的「分数→评分档位」映射；缺失时返回 nil（判分器回退默认映射）。
func (s *Server) gradeMappingFor(ctx context.Context, deckID uint64) *cardtype.GradeMapping {
	if deckID == 0 {
		return nil
	}
	deck, err := s.decks.ByID(ctx, deckID)
	if err != nil || deck.PresetID == 0 {
		return nil
	}
	p, err := s.presets.ByID(ctx, deck.PresetID)
	if err != nil {
		return nil
	}
	m, err := p.GradeMapping()
	if err != nil {
		return nil
	}
	return &m
}

// optionTexts 读取 options 字段，兼容 JSON 反序列化的 []any 与 Go 侧构造的 []string。
func optionTexts(fields map[string]any) []string {
	switch v := fields["options"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// buildGradeInput 按题型构造判分器需要的输入结构。判分器各自断言自己的输入类型；
// 只有 web 层知道表单字段如何映射到题型输入，因此映射集中在这里（不改核心管线）。
// SPA 端把 JSON 作答归一化成同样的 url.Values 后调用它，规则只有一处。
func buildGradeInput(kind string, fields map[string]any, mapping *cardtype.GradeMapping, form url.Values) (any, error) {
	gc := cardtype.GradeContext{Fields: fields, Mapping: mapping}
	switch kind {
	case "typed":
		return cardtype.TypedInput{GradeContext: gc, Answer: form.Get("answer")}, nil
	case "numeric":
		return cardtype.NumericInput{GradeContext: gc, Answer: form.Get("answer")}, nil
	case "choice_single":
		idx, err := strconv.Atoi(strings.TrimSpace(form.Get("answer")))
		if err != nil {
			return nil, fmt.Errorf("choice_single answer is not an option index")
		}
		return cardtype.ChoiceSingleInput{GradeContext: gc, Selected: idx}, nil
	case "choice_multi":
		selected, err := parseSelectedIndices(form["answer"])
		if err != nil {
			return nil, err
		}
		return cardtype.ChoiceMultiInput{GradeContext: gc, Selected: selected}, nil
	case "true_false":
		answer, err := parseBoolAnswer(form.Get("answer"))
		if err != nil {
			return nil, err
		}
		return cardtype.TrueFalseInput{GradeContext: gc, Answer: &answer}, nil
	default:
		return nil, fmt.Errorf("card type %q has no grading input", kind)
	}
}

// parseSelectedIndices 解析多选的 0 基索引集合；空选择是合法作答（记 0 分）。
func parseSelectedIndices(raw []string) ([]int, error) {
	out := make([]int, 0, len(raw))
	for _, item := range raw {
		idx, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil {
			return nil, fmt.Errorf("choice_multi answer is not an option index")
		}
		out = append(out, idx)
	}
	return out, nil
}

// parseBoolAnswer 解析判断题作答；SPA 提交 "true"/"false"。
func parseBoolAnswer(raw string) (bool, error) {
	b, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("true_false answer is not a boolean")
	}
	return b, nil
}

// numberText 以最简形式输出数值（解析后的作答），避免 50 显示成 50.000000。
func numberText(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(n), 'g', -1, 64)
	case int:
		return strconv.Itoa(n)
	default:
		return fmt.Sprintf("%v", v)
	}
}
