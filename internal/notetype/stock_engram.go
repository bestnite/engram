package notetype

// The engram stock note types are the ten note types the data migration creates
// from today's ten card kinds. They are not Anki's stock note types: engram's
// kinds have their own fields and their own answer controls, so
// OriginalStockKind stays nil and AnkiOrigin is nil. The only thing borrowed
// from Anki is the shape of a normal note type (one card per template) and the
// cloze note type's single template.
//
// Field ids equal today's field names. The old model addressed fields by name,
// so keeping the name as the id means the migration only has to add the id to
// the field map and the converted values keep the keys they had. A rename after
// the migration never changes the id, which is what makes the id stable.
//
// source_url and extra are deliberately absent: the new model moves them to
// notes.source_url and notes.extra_json, so they are not note-type fields.

// EngramStock is one engram stock note type: the kind it replaces and the note
// type configuration created for it. The answer control is part of the
// template's Answer spec, so a caller reads it from Config rather than from a
// second field that could drift.
type EngramStock struct {
	// Kind is the kind value today's notes carry in notes.kind.
	Kind string
	// Name is the note type name written to note_types.name.
	Name string
	// Config is the note type configuration written to note_types.config_json.
	Config Config
}

// engramCSS is the one style shared by all ten stock note types.
//
// It is Anki's Basic style: a centred, readable card on a white background.
// Sharing one style keeps the migration output predictable and gives every kind
// the same baseline before the deck editor starts customising. The frame applies
// the user's dark theme separately, so the background is not forced with
// !important.
const engramCSS = ".card {\n    font-family: arial;\n    font-size: 20px;\n    line-height: 1.5;\n    text-align: center;\n    color: black;\n    background-color: white;\n}\n"

// engramLaTeXPre and engramLaTeXPost are Anki's default LaTeX document
// wrapper. They are carried because the note type model exposes them to the
// renderer; today's browser-side MathJax path never used them, so their only
// role in the migrated data is to give a future LaTeX renderer the same
// defaults Anki ships.
const (
	engramLaTeXPre  = "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage[utf8]{inputenc}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n"
	engramLaTeXPost = "\\end{document}"
)

// engramField builds one field with the display defaults Anki's stock note
// types use. The id and the name are the same value: today's field name.
func engramField(id string) Field {
	return Field{ID: id, Name: id, Font: "Arial", FontSize: 20}
}

// engramPlainTextField builds a field whose value is machine syntax rather than
// user-visible rich text. PlainText tells the editor not to render the value as
// HTML and tells the migration not to parse it as Markdown, so characters such
// as *, _ and backticks keep their literal meaning.
func engramPlainTextField(id string) Field {
	field := engramField(id)
	field.PlainText = true
	return field
}

// engramAnswer builds an answer spec that binds the named roles to the fields
// of the same name. A role with no matching today's field is left unbound.
func engramAnswer(control string, roles ...string) *AnswerSpec {
	bindings := make(map[string]string, len(roles))
	for _, role := range roles {
		bindings[role] = role
	}
	return &AnswerSpec{Control: control, Bindings: bindings}
}

// engramConfig assembles a stock config. Every stock type shares the CSS, the
// LaTeX wrapper and the first field as the sort field.
func engramConfig(kind Kind, fields []Field, templates []Template) Config {
	return Config{
		Kind:        kind,
		Fields:      fields,
		Templates:   templates,
		CSS:         engramCSS,
		SortFieldID: fields[0].ID,
		LaTeXPre:    engramLaTeXPre,
		LaTeXPost:   engramLaTeXPost,
	}
}

// EngramStockConfigs returns the ten engram stock note types in a fixed order.
//
// The order is the order of today's kinds in the backlog table, not a map
// order, so a migration creates the same note types in the same sequence on
// every run and a test can compare the whole slice.
func EngramStockConfigs() []EngramStock {
	return []EngramStock{
		engramBasicStock(),
		engramBasicBothStock(),
		engramClozeStock(),
		engramTypedStock(),
		engramNumericStock(),
		engramChoiceSingleStock(),
		engramChoiceMultiStock(),
		engramTrueFalseStock(),
		engramListStock(),
		engramShortAnswerStock(),
	}
}

// EngramStockByKind returns the stock definition for one of today's kinds.
func EngramStockByKind(kind string) (EngramStock, bool) {
	for _, stock := range EngramStockConfigs() {
		if stock.Kind == kind {
			return stock, true
		}
	}
	return EngramStock{}, false
}

// engramBasicStock is today's basic card: front on the question side, back on
// the answer side. The back template repeats only the answer because the review
// view already shows the front above the back; adding {{FrontSide}} would show
// the question twice.
func engramBasicStock() EngramStock {
	return EngramStock{
		Kind: "basic",
		Name: "Basic",
		Config: engramConfig(KindNormal,
			[]Field{engramField("front"), engramField("back")},
			[]Template{{
				ID:      "forward",
				Name:    "Card 1",
				QFormat: "{{front}}",
				AFormat: "{{back}}",
			}},
		),
	}
}

// engramBasicBothStock is today's basic_both card. The two templates keep the
// template ids the old cards already carry (forward and reverse). The reverse
// back template is the front field because the reverse card asks the back and
// answers with the front.
func engramBasicBothStock() EngramStock {
	return EngramStock{
		Kind: "basic_both",
		Name: "Basic (both directions)",
		Config: engramConfig(KindNormal,
			[]Field{engramField("front"), engramField("back")},
			[]Template{
				{
					ID:      "forward",
					Name:    "Card 1",
					QFormat: "{{front}}",
					AFormat: "{{back}}",
				},
				{
					ID:      "reverse",
					Name:    "Card 2",
					QFormat: "{{back}}",
					AFormat: "{{front}}",
				},
			},
		),
	}
}

// engramClozeStock is today's cloze card. The front masks the card's cloze
// number and then renders the blank inputs; the back is the full text with the
// active deletion revealed, which is exactly what the old cloze renderer
// produced. The answer spec binds the cloze control to the text field, and the
// card ordinal is the cloze number, so the control knows which blank it owns.
//
// The back template carries no {{type:}} placeholder: the revealed text is the
// whole answer face, and the cloze control's per-blank breakdown would repeat
// that text as a list. The front still has exactly one type placeholder, which
// is what note type validation requires.
func engramClozeStock() EngramStock {
	return EngramStock{
		Kind: "cloze",
		Name: "Cloze",
		Config: engramConfig(KindCloze,
			[]Field{engramField("text")},
			[]Template{{
				ID:      "cloze",
				Name:    "Cloze",
				QFormat: "{{cloze:text}}{{type:cloze:text}}",
				AFormat: "{{cloze:text}}",
				Answer:  engramAnswer("cloze_blanks", "text"),
			}},
		),
	}
}

// engramTypedStock is today's typed card. The text control reads the canonical
// answer plus the accepted wordings, the two comparison flags and the optional
// regular expression. The regex field is real in today's grading path even
// though the old editor's field table omitted it, so it has to be a field here:
// dropping it would turn a regex answer into a literal comparison.
func engramTypedStock() EngramStock {
	return EngramStock{
		Kind: "typed",
		Name: "Typed answer",
		Config: engramConfig(KindNormal,
			[]Field{
				engramField("prompt"),
				engramField("answer"),
				engramField("accept"),
				engramField("ignore_case"),
				engramField("ignore_whitespace"),
				// The regex is machine syntax: PlainText keeps it out of the
				// HTML renderer and out of the Markdown migration pass.
				engramPlainTextField("regex"),
			},
			[]Template{{
				ID:      "forward",
				Name:    "Card 1",
				QFormat: "{{prompt}}{{type:answer}}",
				AFormat: "{{type:answer}}",
				Answer: engramAnswer("text", "answer", "accept", "ignore_case",
					"ignore_whitespace", "regex"),
			}},
		),
	}
}

// engramNumericStock is today's numeric card. The numeric control reads the
// value, the two tolerances and the unit.
func engramNumericStock() EngramStock {
	return EngramStock{
		Kind: "numeric",
		Name: "Numeric answer",
		Config: engramConfig(KindNormal,
			[]Field{
				engramField("prompt"),
				engramField("value"),
				engramField("unit"),
				engramField("tolerance_absolute"),
				engramField("tolerance_relative"),
			},
			[]Template{{
				ID:      "forward",
				Name:    "Card 1",
				QFormat: "{{prompt}}{{type:value}}",
				AFormat: "{{type:value}}",
				Answer: engramAnswer("numeric", "value", "tolerance_absolute",
					"tolerance_relative", "unit"),
			}},
		),
	}
}

// engramChoiceSingleStock is today's single-choice card. The front renders the
// question and then one radio input per option at the type placeholder; the
// back renders the correct option's text.
func engramChoiceSingleStock() EngramStock {
	return EngramStock{
		Kind: "choice_single",
		Name: "Single choice",
		Config: engramConfig(KindNormal,
			[]Field{
				engramField("question"),
				engramField("options"),
				engramField("answer"),
			},
			[]Template{{
				ID:      "forward",
				Name:    "Card 1",
				QFormat: "{{question}}{{type:options}}",
				AFormat: "{{type:options}}",
				Answer:  engramAnswer("choice_single", "options", "answer"),
			}},
		),
	}
}

// engramChoiceMultiStock is today's multiple-choice card. The front renders one
// checkbox per option; the back lists the correct options in the display order
// the learner saw, which is what the old back did.
func engramChoiceMultiStock() EngramStock {
	return EngramStock{
		Kind: "choice_multi",
		Name: "Multiple choice",
		Config: engramConfig(KindNormal,
			[]Field{
				engramField("question"),
				engramField("options"),
				engramField("answers"),
			},
			[]Template{{
				ID:      "forward",
				Name:    "Card 1",
				QFormat: "{{question}}{{type:options}}",
				AFormat: "{{type:options}}",
				Answer:  engramAnswer("choice_multi", "options", "answers"),
			}},
		),
	}
}

// engramTrueFalseStock is today's true/false card. The control renders the two
// radio inputs on the front and the correct boolean on the back.
func engramTrueFalseStock() EngramStock {
	return EngramStock{
		Kind: "true_false",
		Name: "True / false",
		Config: engramConfig(KindNormal,
			[]Field{
				engramField("statement"),
				engramField("answer"),
			},
			[]Template{{
				ID:      "forward",
				Name:    "Card 1",
				QFormat: "{{statement}}{{type:answer}}",
				AFormat: "{{type:answer}}",
				Answer:  engramAnswer("true_false", "answer"),
			}},
		),
	}
}

// engramListStock is today's list card. The control renders one input per item
// and lists the items on the back, numbered when ordered is true.
func engramListStock() EngramStock {
	return EngramStock{
		Kind: "list",
		Name: "List",
		Config: engramConfig(KindNormal,
			[]Field{
				engramField("prompt"),
				engramField("items"),
				engramField("ordered"),
			},
			[]Template{{
				ID:      "forward",
				Name:    "Card 1",
				QFormat: "{{prompt}}{{type:items}}",
				AFormat: "{{type:items}}",
				Answer:  engramAnswer("list", "items", "ordered"),
			}},
		),
	}
}

// engramShortAnswerStock is today's short-answer card. The control renders a
// free-form text area on the front and the optional reference answer on the
// back; grading stays self-assessed, as it was before.
func engramShortAnswerStock() EngramStock {
	return EngramStock{
		Kind: "short_answer",
		Name: "Short answer",
		Config: engramConfig(KindNormal,
			[]Field{
				engramField("prompt"),
				engramField("reference"),
			},
			[]Template{{
				ID:      "forward",
				Name:    "Card 1",
				QFormat: "{{prompt}}{{type:reference}}",
				AFormat: "{{type:reference}}",
				Answer:  engramAnswer("short_answer", "reference"),
			}},
		),
	}
}
