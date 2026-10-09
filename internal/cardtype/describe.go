package cardtype

// 本文件定义题型的自描述契约：题型把编辑页与复习页需要的元数据（字段表、正反面字段、
// 作答控件、题面/选项字段）一并声明出来，前端因此不必再各自硬编码一张题型表
// （AGENTS.md §2.3 第 7 条：新增题型 = 加一个文件并注册，核心代码不改）。
//
// 契约里的字段名与取值是冻结的：下一个泳道的前端按它们逐字实现，改名或改取值都是
// 破坏性变更。Graded 不在这里声明：它是运行时事实，由注册表在枚举时对 Grader 接口
// 做断言得出，题型自报会与实现漂移。

// 字段控件取值：与前端编辑表单的控件一一对应。
const (
	ControlText     = "text"     // 单行文本
	ControlTextarea = "textarea" // 多行文本（Markdown / LaTeX）
	ControlLines    = "lines"    // 多行文本，每行一个数组元素
	ControlNumber   = "number"   // 数值
	ControlBool     = "bool"     // 布尔
	ControlIndex    = "index"    // 单选：取 options 的下标
	ControlIndexes  = "indexes"  // 多选：取 options 的下标集合
)

// 作答控件取值：决定复习页给输入控件还是四档自评。
const (
	AnswerNone   = "none"   // 自评题型，无作答控件
	AnswerText   = "text"   // 文本作答
	AnswerNumber = "number" // 数值作答
	AnswerSingle = "single" // 单选
	AnswerMulti  = "multi"  // 多选
	AnswerBool   = "bool"   // 判断题
)

// FieldSpec 描述题型的一个字段在编辑表单里的形态；Control 的取值集合与前端控件一一对应。
type FieldSpec struct {
	Key      string `json:"key"`
	Control  string `json:"control"`
	Required bool   `json:"required"`
	// Default 是带库默认值的布尔字段的服务端默认值（例如 true）；无默认时省略。
	Default any `json:"default,omitempty"`
}

// Description 是一个题型的自描述：编辑页与复习页据此渲染，无需按题型分支。
type Description struct {
	Kind     string `json:"kind"`
	LabelKey string `json:"label_key"`
	// Graded 表示该题型是否实现 Grader；由注册表在枚举时断言得出，题型不得自行声明。
	Graded        bool        `json:"graded"`
	AnswerControl string      `json:"answer_control"`
	FrontField    string      `json:"front_field"`
	BackField     string      `json:"back_field"`
	PromptField   string      `json:"prompt_field"`
	OptionsField  string      `json:"options_field"`
	Fields        []FieldSpec `json:"fields"`
}

// commonOptionalFields 是所有题型共用的可选字段，与 cardtype.validateCommonOptional 一致。
var commonOptionalFields = []FieldSpec{
	{Key: "source_url", Control: ControlText},
	{Key: "extra", Control: ControlTextarea},
}

// fieldsWithCommon 返回「题型自有字段 + 通用可选字段」的新切片。
// 每次都新建切片，避免调用方改动一个题型的字段表时影响到另一个题型。
func fieldsWithCommon(own ...FieldSpec) []FieldSpec {
	out := make([]FieldSpec, 0, len(own)+len(commonOptionalFields))
	out = append(out, own...)
	out = append(out, commonOptionalFields...)
	return out
}

// Descriptions 按 kind 字典序返回 Default 注册表里全部题型的自描述。
func Descriptions() []Description { return Default.Descriptions() }

// Descriptions 按 kind 字典序返回注册表里全部题型的自描述。
//
// Kind 以注册键为准（而非题型自报的 Kind），Graded 由 Grader 接口断言得出：
// 两者都是运行时事实，以注册表为准可保证结果稳定且不与实现漂移。
func (r *Registry) Descriptions() []Description {
	kinds := r.Kinds()
	out := make([]Description, 0, len(kinds))
	for _, kind := range kinds {
		t := r.types[kind]
		d := t.Describe()
		d.Kind = kind
		_, graded := t.(Grader)
		d.Graded = graded
		out = append(out, d)
	}
	return out
}
