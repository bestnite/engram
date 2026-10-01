package cardtype

// 内置题型的注册点。新增题型只需在此处加一行，核心管线（cardtype.go）不改动
// （AGENTS.md §2.3 第 7 条）。
func init() {
	mustRegister("basic", basicType{})
	mustRegister("basic_both", basicBothType{})
	mustRegister("cloze", clozeType{})
	mustRegister("list", listType{})
	// 作答类题型（M3-6）：都实现可选的 Grader。
	mustRegister("typed", typedType{})
	mustRegister("numeric", numericType{})
	mustRegister("choice_single", choiceSingleType{})
	mustRegister("choice_multi", choiceMultiType{})
	mustRegister("true_false", trueFalseType{})
}

// mustRegister 在注册失败时 panic：重复注册或空 kind 都是编程错误，启动即暴露。
func mustRegister(kind string, t CardType) {
	if err := Default.Register(kind, t); err != nil {
		panic(err)
	}
}
