// Package cardtype 是题型注册表与核心管线。
//
// 设计目标：新增题型 = 新增一个实现文件 + 注册，核心代码不改动
// （AGENTS.md §2.3 第 7 条）。为此核心只依赖一个窄接口 CardType；作答类判分、
// 未来 LLM 评分等能力用可选窄接口（Grader / PromptContexter / ReferenceRefer）断言，
// 不预先塞进大接口。
//
// 字段名是冻结的，与 schema/note-import.schema.json 逐字一致（basic/basic_both: front/back；
// cloze: text；list: prompt/items[]/ordered；通用可选 extra/source_url）。
package cardtype

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Note 是题型看到的内容视图：Kind 决定 Fields 的结构。
// 与 store.Note 分离，因为注册表只关心内容，不关心持久化
// （内容与进度分离）。
type Note struct {
	Kind   string
	Fields map[string]any
}

// Card 既承载呈现标识，也承载渲染所需的字段。
//
// Template 必须落在 cards.template 的约定内：
// forward / reverse / cloze:<index>。Ordinal 在同一 note 内从 0 起单调递增。
// Fields 是所属 note 的字段，渲染前由调用方填充（生成卡片标识时它可为空，不落库）。
type Card struct {
	Template string
	Ordinal  int
	Fields   map[string]any
}

// Side 指明请求渲染的是正面还是背面。
type Side int

const (
	// SideFront 是问题面。
	SideFront Side = iota
	// SideBack 是答案面。
	SideBack
)

// String 返回侧名的英文标识，供日志与错误信息使用。
func (s Side) String() string {
	if s == SideBack {
		return "back"
	}
	return "front"
}

// RenderResult 是某一侧的渲染输入：Markdown + TeX 原文，
// HTML 化与清洗由渲染管线负责。
type RenderResult struct {
	// Body 是这一侧的主内容。
	Body string
	// Extra 是这一侧额外按顺序展示的片段（list 逐项揭示的条目）；
	// 没有附加片段时为空。
	Extra []string
}

// CardType 是题型必须实现的接口，方法集合故意保持最小。
type CardType interface {
	// Validate 校验 fields。错误必须是可读的英文，并点名字段。
	Validate(fields map[string]any) error
	// Cards 依据 note 产出卡片标识；同一 note 的 Template 必须唯一。
	Cards(note Note) []Card
	// Render 返回 card 在指定侧要展示的内容。
	Render(card Card, side Side) (RenderResult, error)
	// Label 返回 i18n 语言包的键名，而不是用户可见文案。
	Label() string
}

// Grader 是可选能力：作答类题型实现机器判分。
// 未实现的题型不做断言，核心管线不依赖它；实现了的题型由服务端判分，客户端不能自评。
//
// 三个方法覆盖判分的全部题型相关知识，传输层与服务层因此不需要按题型分支：
// 作答怎么从 JSON 解码（ParseAnswer）、怎么判（Grade）、判完怎么把作答还原成可读文本（GivenText）。
type Grader interface {
	// ParseAnswer 把客户端提交的 JSON 作答解码成本题型 Grade 的输入。raw 为空或 JSON null
	// 表示「没有作答」：能把空作答当作错误答案判分的题型返回对应输入，否则返回错误。
	ParseAnswer(gc GradeContext, raw json.RawMessage) (any, error)
	// Grade 对用户输入判分。rating 是 1–4（Again/Hard/Good/Easy），
	// detail 落库 reviews.grade_detail_json，ok=false 表示该输入无法判分。
	Grade(input any) (rating int, detail map[string]any, ok bool)
	// GivenText 把判分细节还原成展示用的作答文本（例如选项索引换成选项文本）；
	// 判断题返回 "true"/"false"，本地化由前端负责。
	GivenText(fields map[string]any, detail map[string]any) string
}

// PromptContext 是 PromptContexter 返回的题目上下文，供未来 LLM 评分使用。
type PromptContext struct {
	Question  string
	Reference string
}

// PromptContexter 是可选能力：为 LLM 评分提供题目与参考答案。
type PromptContexter interface {
	PromptContext(note Note) PromptContext
}

// ReferenceRefer 是可选能力：返回该 note 依赖的知识库片段引用。
type ReferenceRefer interface {
	ReferenceRefs(note Note) []string
}

// Registry 是 kind → 题型实现的注册表。零值不可用，请用 NewRegistry 构造。
type Registry struct {
	types map[string]CardType
}

// NewRegistry 返回一个空的注册表。
func NewRegistry() *Registry {
	return &Registry{types: make(map[string]CardType)}
}

// Register 以 kind 为键注册题型；空 kind、nil 实现、重复注册都会返回错误。
func (r *Registry) Register(kind string, t CardType) error {
	if kind == "" {
		return errors.New("cardtype: kind must not be empty")
	}
	if t == nil {
		return fmt.Errorf("cardtype: type %q must not be nil", kind)
	}
	if _, ok := r.types[kind]; ok {
		return fmt.Errorf("cardtype: type %q is already registered", kind)
	}
	r.types[kind] = t
	return nil
}

// Lookup 按 kind 查找题型。
func (r *Registry) Lookup(kind string) (CardType, bool) {
	t, ok := r.types[kind]
	return t, ok
}

// Kinds 返回已注册的 kind，按字典序排列。
func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.types))
	for k := range r.types {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Validate 校验 kind 对应的字段；未知 kind 返回可读错误。
func (r *Registry) Validate(kind string, fields map[string]any) error {
	t, ok := r.Lookup(kind)
	if !ok {
		return UnknownKindError(kind)
	}
	return t.Validate(fields)
}

// Cards 用注册表把 note 展开成卡片；未知 kind 返回可读错误。
func (r *Registry) Cards(note Note) ([]Card, error) {
	t, ok := r.Lookup(note.Kind)
	if !ok {
		return nil, UnknownKindError(note.Kind)
	}
	return t.Cards(note), nil
}

// UnknownKindError 构造统一的未知题型错误；调用方无需关心具体措辞。
func UnknownKindError(kind string) error {
	return fmt.Errorf("unknown card type %q", kind)
}

// Default 是题型注册表；内置题型在 builtin.go 的 init 中注册，包级函数都作用于它。
var Default = NewRegistry()

// Validate 校验 Default 注册表中 kind 对应的字段。
func Validate(kind string, fields map[string]any) error { return Default.Validate(kind, fields) }

// Cards 用 Default 注册表把 note 展开成卡片。
func Cards(note Note) ([]Card, error) { return Default.Cards(note) }

// Lookup 在 Default 注册表中按 kind 查找题型。
func Lookup(kind string) (CardType, bool) { return Default.Lookup(kind) }

// Kinds 返回 Default 注册表中已注册的 kind。
func Kinds() []string { return Default.Kinds() }

// errUnknownTemplate 构造模板不属于该题型时的错误。
func errUnknownTemplate(template string) error {
	return fmt.Errorf("unknown card template %q", template)
}
