package notetype

import "strconv"

// The common templates are ready-made note types for frequent study shapes that neither Anki's six stock types nor
// engram's ten question types cover: vocabulary with a reading, Japanese with furigana, listening through speech, and
// a basic card with a side note. They use only Anki template syntax (field references, conditionals and the hint,
// kanji, furigana and tts filters), so a deck built from them exports to Anki and reviews the same there. They are not
// Anki stock types: OriginalStockKind and AnkiOrigin stay nil.
//
// Field ids are f<index> and template ids t<index> in declaration order, as for Anki's stock types. Those ids are what
// a note type is created with, and a later rename or reorder does not change them.
//
// No answer template starts with {{FrontSide}}. An answer side that adds only the answer keeps the question above it
// in review, as engram's own stock types do; one that repeats the question's fields (furigana, listening) is shown
// alone on reveal, as Anki shows every answer side (notetype.AnswerIncludesQuestion).

// TemplateStock is one common template: a stable kind that names it in the stock id, the canonical English name a
// created note type takes when the caller sends none, and the configuration it creates.
type TemplateStock struct {
	Kind   string
	Name   string
	Config Config
}

// templateField builds one field with the display defaults of Anki's stock types.
func templateField(index int, name, description string) Field {
	return Field{ID: "f" + strconv.Itoa(index), Name: name, Font: "Arial", FontSize: 20, Description: description}
}

// templateConfig assembles a normal note type with one card template. Every common template starts
// from DefaultCSS and adds classes of its own, so the face follows the card frame's type scale and a
// deck can still restyle any of it. The added classes dim secondary lines with opacity rather than a
// grey colour, so they stay readable when the frame shows the card in night mode.
func templateConfig(fields []Field, qfmt, afmt, css string) Config {
	return Config{
		Kind:        KindNormal,
		Fields:      fields,
		Templates:   []Template{{ID: "t0", Name: "Card 1", QFormat: qfmt, AFormat: afmt}},
		CSS:         DefaultCSS + css,
		SortFieldID: fields[0].ID,
		LaTeXPre:    engramLaTeXPre,
		LaTeXPost:   engramLaTeXPost,
	}
}

// TemplateStockConfigs returns the common templates in a fixed order, so the picker shows the same list on every call.
func TemplateStockConfigs() []TemplateStock {
	return []TemplateStock{
		{
			// The word is asked; the answer shows its reading and meaning, and the example sentence waits behind a
			// hint link so it does not give the meaning away before the learner has tried.
			Kind: "vocabulary",
			Name: "Vocabulary",
			Config: templateConfig(
				[]Field{
					templateField(0, "Word", ""),
					templateField(1, "Reading", ""),
					templateField(2, "Meaning", ""),
					templateField(3, "Example", ""),
				},
				"<div class=word>{{Word}}</div>",
				"<div class=reading>{{Reading}}</div>\n<div class=meaning>{{Meaning}}</div>\n{{#Example}}<div class=example>{{hint:Example}}</div>{{/Example}}",
				".word {\n    font-size: 32px;\n}\n.reading {\n    opacity: 0.7;\n}\n.example {\n    margin-top: 1em;\n    font-size: 16px;\n}\n",
			),
		},
		{
			// The expression is written with Anki's reading syntax, 漢字[かんじ]. The question shows the kanji alone, the
			// answer the same text with the readings set above it.
			Kind: "japanese_furigana",
			Name: "Japanese (furigana)",
			Config: templateConfig(
				[]Field{
					templateField(0, "Expression", "漢字[かんじ]"),
					templateField(1, "Meaning", ""),
				},
				"<div class=expression>{{kanji:Expression}}</div>",
				"<div class=expression>{{furigana:Expression}}</div>\n<div class=meaning>{{Meaning}}</div>",
				".expression {\n    font-size: 32px;\n}\n.meaning {\n    margin-top: 0.5em;\n}\n",
			),
		},
		{
			// The question only speaks the text; the answer shows the written text and its meaning. The language is part
			// of the template, so a deck in another language changes zh_CN there.
			Kind: "listening",
			Name: "Listening (TTS)",
			Config: templateConfig(
				[]Field{
					templateField(0, "Text", ""),
					templateField(1, "Meaning", ""),
				},
				"{{tts zh_CN:Text}}",
				"<div class=text>{{Text}}</div>\n<div class=meaning>{{Meaning}}</div>",
				".text {\n    font-size: 28px;\n}\n.meaning {\n    opacity: 0.7;\n}\n",
			),
		},
		{
			// Anki's Basic with a third field for an explanation or a source, shown smaller below the answer and only
			// when it holds something.
			Kind: "basic_extra",
			Name: "Basic (with extra)",
			Config: templateConfig(
				[]Field{
					templateField(0, "Front", ""),
					templateField(1, "Back", ""),
					templateField(2, "Extra", ""),
				},
				"{{Front}}",
				"{{Back}}\n{{#Extra}}<div class=extra>{{Extra}}</div>{{/Extra}}",
				".extra {\n    margin-top: 1em;\n    font-size: 16px;\n    opacity: 0.75;\n}\n",
			),
		},
	}
}
