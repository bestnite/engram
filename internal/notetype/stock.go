package notetype

// The stockConfigs value below is generated from testdata/anki/stock.golden.json,
// which records what Anki 26.09.3 returns for its six stock note types. Every
// field option, template option, CSS and LaTeX string is produced from that file
// rather than retyped, and stock_test.go compares the whole value with it again.

// stockConfigs holds Anki's six stock note types. Field ids are f<index> and
// template ids are t<index> in declaration order; these values are the ids a note
// type is created with, and a later rename or reorder does not change them.
var stockConfigs = []Config{
	{
		Kind: KindNormal,
		Fields: []Field{
			{
				ID:                "f0",
				Name:              "Front",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               nil,
				PreventDeletion:   false,
			},
			{
				ID:                "f1",
				Name:              "Back",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               nil,
				PreventDeletion:   false,
			},
		},
		Templates: []Template{
			{
				ID:              "t0",
				Name:            "Card 1",
				QFormat:         "{{Front}}",
				AFormat:         "{{FrontSide}}\n\n<hr id=answer>\n\n{{Back}}",
				BQFormat:        "",
				BAFormat:        "",
				BrowserFont:     "",
				BrowserFontSize: 0,
			},
		},
		CSS:               ".card {\n    font-family: arial;\n    font-size: 20px;\n    line-height: 1.5;\n    text-align: center;\n    color: black;\n    background-color: white;\n}\n",
		SortFieldID:       "f0",
		LaTeXPre:          "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage[utf8]{inputenc}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n",
		LaTeXPost:         "\\end{document}",
		LaTeXSVG:          false,
		OriginalStockKind: ptr(StockBasic),
	},
	{
		Kind: KindNormal,
		Fields: []Field{
			{
				ID:                "f0",
				Name:              "Front",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               nil,
				PreventDeletion:   false,
			},
			{
				ID:                "f1",
				Name:              "Back",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               nil,
				PreventDeletion:   false,
			},
		},
		Templates: []Template{
			{
				ID:              "t0",
				Name:            "Card 1",
				QFormat:         "{{Front}}",
				AFormat:         "{{FrontSide}}\n\n<hr id=answer>\n\n{{Back}}",
				BQFormat:        "",
				BAFormat:        "",
				BrowserFont:     "",
				BrowserFontSize: 0,
			},
			{
				ID:              "t1",
				Name:            "Card 2",
				QFormat:         "{{Back}}",
				AFormat:         "{{FrontSide}}\n\n<hr id=answer>\n\n{{Front}}",
				BQFormat:        "",
				BAFormat:        "",
				BrowserFont:     "",
				BrowserFontSize: 0,
			},
		},
		CSS:               ".card {\n    font-family: arial;\n    font-size: 20px;\n    line-height: 1.5;\n    text-align: center;\n    color: black;\n    background-color: white;\n}\n",
		SortFieldID:       "f0",
		LaTeXPre:          "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage[utf8]{inputenc}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n",
		LaTeXPost:         "\\end{document}",
		LaTeXSVG:          false,
		OriginalStockKind: ptr(StockBasicAndReversed),
	},
	{
		Kind: KindNormal,
		Fields: []Field{
			{
				ID:                "f0",
				Name:              "Front",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               nil,
				PreventDeletion:   false,
			},
			{
				ID:                "f1",
				Name:              "Back",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               nil,
				PreventDeletion:   false,
			},
			{
				ID:                "f2",
				Name:              "Add Reverse",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               nil,
				PreventDeletion:   false,
			},
		},
		Templates: []Template{
			{
				ID:              "t0",
				Name:            "Card 1",
				QFormat:         "{{Front}}",
				AFormat:         "{{FrontSide}}\n\n<hr id=answer>\n\n{{Back}}",
				BQFormat:        "",
				BAFormat:        "",
				BrowserFont:     "",
				BrowserFontSize: 0,
			},
			{
				ID:              "t1",
				Name:            "Card 2",
				QFormat:         "{{#Add Reverse}}{{Back}}{{/Add Reverse}}",
				AFormat:         "{{FrontSide}}\n\n<hr id=answer>\n\n{{Front}}",
				BQFormat:        "",
				BAFormat:        "",
				BrowserFont:     "",
				BrowserFontSize: 0,
			},
		},
		CSS:               ".card {\n    font-family: arial;\n    font-size: 20px;\n    line-height: 1.5;\n    text-align: center;\n    color: black;\n    background-color: white;\n}\n",
		SortFieldID:       "f0",
		LaTeXPre:          "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage[utf8]{inputenc}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n",
		LaTeXPost:         "\\end{document}",
		LaTeXSVG:          false,
		OriginalStockKind: ptr(StockBasicOptionalReversed),
	},
	{
		Kind: KindNormal,
		Fields: []Field{
			{
				ID:                "f0",
				Name:              "Front",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               nil,
				PreventDeletion:   false,
			},
			{
				ID:                "f1",
				Name:              "Back",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               nil,
				PreventDeletion:   false,
			},
		},
		Templates: []Template{
			{
				ID:              "t0",
				Name:            "Card 1",
				QFormat:         "{{Front}}\n\n{{type:Back}}",
				AFormat:         "{{Front}}\n\n<hr id=answer>\n\n{{type:Back}}",
				BQFormat:        "",
				BAFormat:        "",
				BrowserFont:     "",
				BrowserFontSize: 0,
			},
		},
		CSS:               ".card {\n    font-family: arial;\n    font-size: 20px;\n    line-height: 1.5;\n    text-align: center;\n    color: black;\n    background-color: white;\n}\n",
		SortFieldID:       "f0",
		LaTeXPre:          "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage[utf8]{inputenc}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n",
		LaTeXPost:         "\\end{document}",
		LaTeXSVG:          false,
		OriginalStockKind: ptr(StockBasicTyping),
	},
	{
		Kind: KindCloze,
		Fields: []Field{
			{
				ID:                "f0",
				Name:              "Text",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               ptr(0),
				PreventDeletion:   true,
			},
			{
				ID:                "f1",
				Name:              "Back Extra",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               ptr(1),
				PreventDeletion:   false,
			},
		},
		Templates: []Template{
			{
				ID:              "t0",
				Name:            "Cloze",
				QFormat:         "{{cloze:Text}}",
				AFormat:         "{{cloze:Text}}<br>\n{{Back Extra}}",
				BQFormat:        "",
				BAFormat:        "",
				BrowserFont:     "",
				BrowserFontSize: 0,
			},
		},
		CSS:               ".card {\n    font-family: arial;\n    font-size: 20px;\n    line-height: 1.5;\n    text-align: center;\n    color: black;\n    background-color: white;\n}\n.cloze {\n    font-weight: bold;\n    color: blue;\n}\n.nightMode .cloze {\n    color: lightblue;\n}\n",
		SortFieldID:       "f0",
		LaTeXPre:          "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage[utf8]{inputenc}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n",
		LaTeXPost:         "\\end{document}",
		LaTeXSVG:          false,
		OriginalStockKind: ptr(StockCloze),
	},
	{
		Kind: KindCloze,
		Fields: []Field{
			{
				ID:                "f0",
				Name:              "Occlusion",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               ptr(0),
				PreventDeletion:   true,
			},
			{
				ID:                "f1",
				Name:              "Image",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               ptr(1),
				PreventDeletion:   true,
			},
			{
				ID:                "f2",
				Name:              "Header",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               ptr(2),
				PreventDeletion:   true,
			},
			{
				ID:                "f3",
				Name:              "Back Extra",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               ptr(3),
				PreventDeletion:   true,
			},
			{
				ID:                "f4",
				Name:              "Comments",
				Sticky:            false,
				RTL:               false,
				Font:              "Arial",
				FontSize:          20,
				Description:       "",
				PlainText:         false,
				Collapsed:         false,
				ExcludeFromSearch: false,
				Tag:               ptr(4),
				PreventDeletion:   false,
			},
		},
		Templates: []Template{
			{
				ID:              "t0",
				Name:            "Image Occlusion",
				QFormat:         "{{#Header}}<div>{{Header}}</div>{{/Header}}\n<div style=\"display: none\">{{cloze:Occlusion}}</div>\n<div id=\"err\"></div>\n<div id=\"image-occlusion-container\">\n    {{Image}}\n    <canvas id=\"image-occlusion-canvas\"></canvas>\n</div>\n<script>\ntry {\n    anki.imageOcclusion.setup();\n} catch (exc) {\n    document.getElementById(\"err\").innerHTML = `Error loading image occlusion. Is your Anki version up to date?<br><br>${exc}`;\n}\n</script>\n",
				AFormat:         "{{#Header}}<div>{{Header}}</div>{{/Header}}\n<div style=\"display: none\">{{cloze:Occlusion}}</div>\n<div id=\"err\"></div>\n<div id=\"image-occlusion-container\">\n    {{Image}}\n    <canvas id=\"image-occlusion-canvas\"></canvas>\n</div>\n<script>\ntry {\n    anki.imageOcclusion.setup();\n} catch (exc) {\n    document.getElementById(\"err\").innerHTML = `Error loading image occlusion. Is your Anki version up to date?<br><br>${exc}`;\n}\n</script>\n\n<div><button id=\"toggle\">Toggle Masks</button></div>\n{{#Back Extra}}<div>{{Back Extra}}</div>{{/Back Extra}}\n",
				BQFormat:        "",
				BAFormat:        "",
				BrowserFont:     "",
				BrowserFontSize: 0,
			},
		},
		CSS:               "#image-occlusion-canvas {\n    --inactive-shape-color: #ffeba2;\n    --active-shape-color: #ff8e8e;\n    --inactive-shape-border: 1px #212121;\n    --active-shape-border: 1px #212121;\n    --highlight-shape-color: #ff8e8e00;\n    --highlight-shape-border: 1px #ff8e8e;\n}\n\n.card {\n    font-family: arial;\n    font-size: 20px;\n    text-align: center;\n    color: black;\n    background-color: white;\n}\n",
		SortFieldID:       "f0",
		LaTeXPre:          "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage[utf8]{inputenc}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n",
		LaTeXPost:         "\\end{document}",
		LaTeXSVG:          false,
		OriginalStockKind: ptr(StockImageOcclusion),
	},
}

// StockName returns the display name of a stock note type, or the empty string
// when kind is not one of Anki's stock kinds. The name is a table column rather
// than part of Config, so it is looked up separately.
func StockName(kind OriginalStockKind) string {
	switch kind {
	case StockBasic:
		return "Basic"
	case StockBasicAndReversed:
		return "Basic (and reversed card)"
	case StockBasicOptionalReversed:
		return "Basic (optional reversed card)"
	case StockBasicTyping:
		return "Basic (type in the answer)"
	case StockCloze:
		return "Cloze"
	case StockImageOcclusion:
		return "Image Occlusion"
	default:
		return ""
	}
}

// StockConfigs returns Anki's six stock note types. Each call returns fresh
// values, so a caller may edit the result without changing the next call.
func StockConfigs() []Config {
	configs := make([]Config, len(stockConfigs))
	for i, config := range stockConfigs {
		configs[i] = config.clone()
	}
	return configs
}

func (c Config) clone() Config {
	c.Fields = append([]Field(nil), c.Fields...)
	for i := range c.Fields {
		if c.Fields[i].Tag != nil {
			tag := *c.Fields[i].Tag
			c.Fields[i].Tag = &tag
		}
	}
	c.Templates = append([]Template(nil), c.Templates...)
	for i := range c.Templates {
		if c.Templates[i].Answer == nil {
			continue
		}
		answer := *c.Templates[i].Answer
		if answer.Bindings != nil {
			bindings := make(map[string]string, len(answer.Bindings))
			for role, fieldID := range answer.Bindings {
				bindings[role] = fieldID
			}
			answer.Bindings = bindings
		}
		c.Templates[i].Answer = &answer
	}
	if c.OriginalStockKind != nil {
		kind := *c.OriginalStockKind
		c.OriginalStockKind = &kind
	}
	if c.AnkiOrigin != nil {
		origin := *c.AnkiOrigin
		c.AnkiOrigin = &origin
	}
	return c
}

func ptr[T any](value T) *T {
	return &value
}
