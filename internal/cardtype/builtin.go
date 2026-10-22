package cardtype

// Registration point for built-in card types. Adding a card type only needs one line here; the core pipeline (cardtype.go) is not touched
// (AGENTS.md §2.3 item 7).
func init() {
	mustRegister("basic", basicType{})
	mustRegister("basic_both", basicBothType{})
	mustRegister("cloze", clozeType{})
	mustRegister("list", listType{})
	// Answer-based card types: all implement the optional Grader.
	mustRegister("typed", typedType{})
	mustRegister("numeric", numericType{})
	mustRegister("choice_single", choiceSingleType{})
	mustRegister("choice_multi", choiceMultiType{})
	mustRegister("true_false", trueFalseType{})
	// Subjective: self-graded free-text card types; LLM grading is left for later.
	mustRegister("short_answer", shortAnswerType{})
}

// mustRegister panics when registration fails: duplicate registration or an empty kind are programming errors that surface at startup.
func mustRegister(kind string, t CardType) {
	if err := Default.Register(kind, t); err != nil {
		panic(err)
	}
}
