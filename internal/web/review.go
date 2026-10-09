package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"git.nite07.com/nite/engram/internal/cardtype"
	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// 复习页的规范路径。评分与动作走 SPA 的同源 JSON 端点（/api/v1/review/*）。
const reviewPagePath = "/review"

// registerReviewRoutes 挂载复习页。
// 依赖未装配时跳过，保证 M0 阶段的测试仍能构造 Server。
func (s *Server) registerReviewRoutes(router *gin.Engine) {
	if s.sessions == nil || s.decks == nil || s.notes == nil || s.cards == nil || s.presets == nil {
		return
	}
	// GET /review 返回应用壳：reviewPageRoute 先按原有的会话与卡组范围判定鉴权，再交出应用壳，
	// 由客户端路由渲染复习页。评分与动作走 SPA 的 JSON 端点
	// （PATCH/POST /api/v1/review/*），不再有分离的 htmx 评分端点。
	router.GET(reviewPagePath, s.reviewPageRoute)
}

// reviewPageRoute 提供 GET /review：返回应用壳，由客户端路由渲染复习页，
// 数据仍走既有 JSON 端点。会话与卡组范围判定先于切壳执行——
// 非法 deck 参数 400、范围里出现读不到的卡组 403/404，不因返回应用壳而放行。
func (s *Server) reviewPageRoute(c *gin.Context) {
	user, ok := s.requireUser(c)
	if !ok {
		return
	}
	if _, ok := s.parseDeckScope(c, user); !ok {
		return
	}
	s.shell.ServeIndex(c)
}

// reviewScope 是一次复习请求的卡组范围：deckIDs 为空表示全库（不按卡组过滤）。
// 范围由 URL 的可重复 deck 参数决定，并由 SPA 的 JSON 请求原样带回。
type reviewScope struct{ deckIDs []uint64 }

// deckScopeFromValues 解析可重复的 deck 参数值（对外 id 字符串）：去重、跳过空串；任一值
// 形状非法即 400、形状合法但库里不存在即 404。随后逐个校验 loadDeckForRole(..., RoleReader)：
// 任何缺失或无权限的卡组都让整次请求失败（404/403，由该 helper 写出），绝不静默丢弃某个卡组。
func (s *Server) deckScopeFromValues(c *gin.Context, user *store.User, raw []string) (reviewScope, bool) {
	ids := make([]uint64, 0, len(raw))
	seen := make(map[uint64]bool, len(raw))
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if !looksLikePublicID(v) {
			c.AbortWithStatus(http.StatusBadRequest)
			return reviewScope{}, false
		}
		deck, err := s.decks.ByPublicID(c.Request.Context(), v)
		if err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return reviewScope{}, false
		}
		if seen[deck.ID] {
			continue
		}
		seen[deck.ID] = true
		ids = append(ids, deck.ID)
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
// 多卡组与全库回退到文档化默认参数。无卡组/预设缺失同样回退默认。
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

// ReviewCardView 是当前卡片的两面渲染结果与标识。
// FrontHTML / BackHTML 一定来自 internal/render 的白名单清洗，SPA 可安全地按原样嵌入。
type ReviewCardView struct {
	CardID string
	NoteID string
	DeckID string
	// ExpectedVersion 是提交时携带的乐观锁版本（新卡为 "0"）。
	ExpectedVersion string
	Template        string
	FrontHTML       string
	BackHTML        string
	// EditHref 指向该 note 的编辑页，供 e 键跳转。
	EditHref string
}

// cardView 渲染一张卡的正反面并取乐观锁版本。
func (s *Server) cardView(ctx context.Context, user *store.User, item schedule.QueueItem) (*ReviewCardView, error) {
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
	deckPublic := s.deckPublicID(ctx, note.DeckID)
	return &ReviewCardView{
		CardID:          card.PublicID,
		NoteID:          note.PublicID,
		DeckID:          deckPublic,
		ExpectedVersion: strconv.Itoa(s.stateVersion(ctx, user.ID, card.ID)),
		Template:        card.Template,
		FrontHTML:       frontHTML,
		BackHTML:        backHTML,
		EditHref:        "/decks/" + deckPublic + "/notes/" + note.PublicID,
	}, nil
}

// stateVersion 读取 (card_id, user_id) 的乐观锁版本；没有状态行的新卡返回 0。
func (s *Server) stateVersion(ctx context.Context, userID, cardID uint64) int {
	var st store.CardState
	err := s.db.WithContext(ctx).Where("card_id = ? AND user_id = ?", cardID, userID).Take(&st).Error
	if err != nil {
		return 0
	}
	return st.Version
}

// graderFor 用可选窄接口断言判断题型是否支持机器判分。
// 判分能力是可选能力，核心管线不依赖它：未实现 Grader 的题型返回 false，仍走四档自评。
func graderFor(kind string) (cardtype.Grader, bool) {
	t, ok := cardtype.Lookup(kind)
	if !ok {
		return nil, false
	}
	g, ok := t.(cardtype.Grader)
	return g, ok
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
