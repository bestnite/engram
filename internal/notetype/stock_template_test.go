package notetype

import (
	"strings"
	"testing"
)

// TestTemplateStockConfigs pins the common templates: every config passes the validation a created note type goes
// through, kinds are unique, and the ids follow the f<index>/t<index> scheme a later rename never changes.
func TestTemplateStockConfigs(t *testing.T) {
	stocks := TemplateStockConfigs()
	if len(stocks) != 4 {
		t.Fatalf("template count = %d, want 4", len(stocks))
	}
	seen := map[string]bool{}
	for _, stock := range stocks {
		if seen[stock.Kind] {
			t.Errorf("duplicate template kind %q", stock.Kind)
		}
		seen[stock.Kind] = true
		if err := ValidateName(stock.Name); err != nil {
			t.Errorf("template %q name: %v", stock.Kind, err)
		}
		if err := stock.Config.Validate(); err != nil {
			t.Errorf("template %q config: %v", stock.Kind, err)
		}
		if stock.Config.OriginalStockKind != nil || stock.Config.AnkiOrigin != nil {
			t.Errorf("template %q claims an Anki origin", stock.Kind)
		}
		for i, field := range stock.Config.Fields {
			if want := "f" + string(rune('0'+i)); field.ID != want {
				t.Errorf("template %q field %d id = %q, want %q", stock.Kind, i, field.ID, want)
			}
		}
	}
	// Each call returns a fresh value: a caller that edits its copy cannot change the next one.
	stocks[0].Config.Fields[0].Name = "changed"
	if TemplateStockConfigs()[0].Config.Fields[0].Name == "changed" {
		t.Error("TemplateStockConfigs shares its fields between calls")
	}
}

// TestTemplateStockRendering renders each common template with a filled note and checks the parts a learner sees.
func TestTemplateStockRendering(t *testing.T) {
	byKind := map[string]TemplateStock{}
	for _, stock := range TemplateStockConfigs() {
		byKind[stock.Kind] = stock
	}
	cases := []struct {
		kind      string
		fields    map[string]string
		wantFront []string
		denyFront []string
		wantBack  []string
		denyBack  []string
	}{
		{
			kind:      "vocabulary",
			fields:    map[string]string{"f0": "赓续", "f1": "gēng xù", "f2": "继续、延续", "f3": "赓续传统"},
			wantFront: []string{"<div class=word>赓续</div>"},
			denyFront: []string{"gēng xù", "继续"},
			denyBack:  []string{"<div class=word>"},
			wantBack:  []string{"<div class=reading>gēng xù</div>", "<div class=meaning>继续、延续</div>", "class=hint", "赓续传统"},
		},
		{
			kind:     "vocabulary",
			fields:   map[string]string{"f0": "赓续", "f1": "gēng xù", "f2": "继续", "f3": ""},
			wantBack: []string{"继续"},
			denyBack: []string{"class=example", "class=hint"},
		},
		{
			kind:      "japanese_furigana",
			fields:    map[string]string{"f0": "日本語[にほんご]を 勉強[べんきょう]", "f1": "study Japanese"},
			wantFront: []string{"日本語を勉強"},
			denyFront: []string{"にほんご", "<ruby>"},
			wantBack:  []string{"<ruby><rb>日本語</rb><rt>にほんご</rt></ruby>", "<div class=meaning>study Japanese</div>"},
		},
		{
			kind:      "listening",
			fields:    map[string]string{"f0": "你好", "f1": "hello"},
			wantFront: []string{"[anki:tts lang=zh_CN]你好[/anki:tts]"},
			denyFront: []string{"hello", "class=text"},
			wantBack:  []string{"<div class=text>你好</div>", "hello"},
		},
		{
			kind:      "basic_extra",
			fields:    map[string]string{"f0": "Q", "f1": "A", "f2": "from the textbook"},
			wantFront: []string{"Q"},
			wantBack:  []string{"A", "<div class=extra>from the textbook</div>"},
		},
		{
			kind:     "basic_extra",
			fields:   map[string]string{"f0": "Q", "f1": "A", "f2": ""},
			denyBack: []string{"class=extra"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			stock := byKind[tc.kind]
			tmpl := stock.Config.Templates[0]
			data := RenderData{Fields: tc.fields}
			front, err := Render(stock.Config, tmpl, SideFront, data)
			if err != nil {
				t.Fatalf("render front: %v", err)
			}
			back, err := RenderAnswer(stock.Config, tmpl, data, front)
			if err != nil {
				t.Fatalf("render back: %v", err)
			}
			for _, want := range tc.wantFront {
				if !strings.Contains(front, want) {
					t.Errorf("front = %q, want it to contain %q", front, want)
				}
			}
			for _, deny := range tc.denyFront {
				if strings.Contains(front, deny) {
					t.Errorf("front = %q, must not contain %q", front, deny)
				}
			}
			for _, want := range tc.wantBack {
				if !strings.Contains(back, want) {
					t.Errorf("back = %q, want it to contain %q", back, want)
				}
			}
			// The review page keeps the question above the answer; a template repeating it would show it twice.
			if strings.Contains(stock.Config.Templates[0].AFormat, "{{FrontSide}}") {
				t.Errorf("answer template repeats the question: %q", stock.Config.Templates[0].AFormat)
			}
			for _, deny := range tc.denyBack {
				if strings.Contains(back, deny) {
					t.Errorf("back = %q, must not contain %q", back, deny)
				}
			}
		})
	}
}

// TestListeningTemplateEmptiness: a front that only speaks its text is still a card when the text is filled, because
// emptiness follows the fields the front references (as in Anki), not the visible text it renders.
func TestListeningTemplateEmptiness(t *testing.T) {
	var listening TemplateStock
	for _, stock := range TemplateStockConfigs() {
		if stock.Kind == "listening" {
			listening = stock
		}
	}
	tmpl := listening.Config.Templates[0]
	if FrontIsEmpty(listening.Config, tmpl, RenderData{Fields: map[string]string{"f0": "你好", "f1": ""}}) {
		t.Error("listening card with text is reported empty")
	}
	if !FrontIsEmpty(listening.Config, tmpl, RenderData{Fields: map[string]string{"f0": "", "f1": "hello"}}) {
		t.Error("listening card without text is not reported empty")
	}
}
