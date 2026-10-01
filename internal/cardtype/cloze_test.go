package cardtype

import (
	"strings"
	"testing"
)

// TestClozeCardsFromTwoIndices 断言一个 note 因两个序号生成两张卡（M2-4 验收）。
func TestClozeCardsFromTwoIndices(t *testing.T) {
	fields := map[string]any{"text": "The {{c1::mitochondrion}} carries its own {{c2::DNA::genetic material}}."}
	if err := Validate("cloze", fields); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	cards, err := Cards(Note{Kind: "cloze", Fields: fields})
	if err != nil {
		t.Fatalf("Cards: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("len(cards) = %d, want 2", len(cards))
	}
	if cards[0].Template != "cloze:1" || cards[0].Ordinal != 0 {
		t.Errorf("card[0] = %+v, want template cloze:1 ordinal 0", cards[0])
	}
	if cards[1].Template != "cloze:2" || cards[1].Ordinal != 1 {
		t.Errorf("card[1] = %+v, want template cloze:2 ordinal 1", cards[1])
	}
}

// TestClozeRepeatedIndexProducesOneCard 断言重复序号只产出一张卡，且该序号全部被掩盖。
func TestClozeRepeatedIndexProducesOneCard(t *testing.T) {
	fields := map[string]any{"text": "{{c1::a}} and {{c1::b}} but {{c2::c}}"}
	cards, _ := Cards(Note{Kind: "cloze", Fields: fields})
	if len(cards) != 2 {
		t.Fatalf("len(cards) = %d, want 2 (c1 once, c2 once)", len(cards))
	}
	res, err := clozeType{}.Render(cards[0], SideFront)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if res.Body != "[…] and […] but {{c2::c}}" {
		t.Errorf("front = %q, want both c1 hidden", res.Body)
	}
}

// TestClozeNestedBraces 断言嵌套花括号按深度匹配，不会提前闭合。
func TestClozeNestedBraces(t *testing.T) {
	dels, err := ParseCloze("x {{c1::outer {{inner}} text}} y")
	if err != nil {
		t.Fatalf("ParseCloze: %v", err)
	}
	if len(dels) != 1 {
		t.Fatalf("len(dels) = %d, want 1", len(dels))
	}
	if dels[0].Text != "outer {{inner}} text" {
		t.Errorf("text = %q, want %q", dels[0].Text, "outer {{inner}} text")
	}
	if got := "x {{c1::outer {{inner}} text}} y"[dels[0].Start:dels[0].End]; got != "{{c1::outer {{inner}} text}}" {
		t.Errorf("span = %q", got)
	}
}

// TestClozeNestedMarkerIsNotCarded 断言嵌套在内容里的 {{cM::…}} 只当普通内容，不单独成卡。
func TestClozeNestedMarkerIsNotCarded(t *testing.T) {
	fields := map[string]any{"text": "{{c1::a {{c2::b}} c}}"}
	cards, _ := Cards(Note{Kind: "cloze", Fields: fields})
	if len(cards) != 1 || cards[0].Template != "cloze:1" {
		t.Fatalf("cards = %+v, want only cloze:1", cards)
	}
	dels, _ := ParseCloze(fields["text"].(string))
	if dels[0].Text != "a {{c2::b}} c" {
		t.Errorf("text = %q", dels[0].Text)
	}
}

// TestClozeEscapes 断言转义抑制结构解析，但原文保留（TeX 安全）。
func TestClozeEscapes(t *testing.T) {
	t.Run("escaped opener yields no deletion", func(t *testing.T) {
		for _, text := range []string{`\{{c1::x}}`, `\{\{c1::x\}\}`, `\{{c1::x\}\}`} {
			dels, err := ParseCloze(text)
			if err != nil {
				t.Fatalf("ParseCloze(%q): %v", text, err)
			}
			if len(dels) != 0 {
				t.Errorf("ParseCloze(%q) = %d deletions, want 0", text, len(dels))
			}
			if err := Validate("cloze", map[string]any{"text": text}); err == nil {
				t.Errorf("Validate(%q): want error for no deletion", text)
			}
		}
	})

	t.Run("escaped braces inside content are preserved", func(t *testing.T) {
		dels, err := ParseCloze(`{{c1::a \{b\} c}}`)
		if err != nil {
			t.Fatalf("ParseCloze: %v", err)
		}
		if len(dels) != 1 || dels[0].Text != `a \{b\} c` {
			t.Fatalf("dels = %+v, want text %q", dels, `a \{b\} c`)
		}
	})
}

// TestClozeHint 断言 {{cN::text::hint}} 的解析与渲染。
func TestClozeHint(t *testing.T) {
	fields := map[string]any{"text": "The {{c1::mitochondrion::organelle}} is the powerhouse."}
	dels, err := ParseCloze(fields["text"].(string))
	if err != nil {
		t.Fatalf("ParseCloze: %v", err)
	}
	if len(dels) != 1 || dels[0].Text != "mitochondrion" || dels[0].Hint != "organelle" {
		t.Fatalf("dels = %+v, want text mitochondrion hint organelle", dels)
	}
	card := Card{Template: "cloze:1", Fields: fields}
	front, _ := clozeType{}.Render(card, SideFront)
	if front.Body != "The [organelle] is the powerhouse." {
		t.Errorf("front = %q, want hint placeholder", front.Body)
	}
	back, _ := clozeType{}.Render(card, SideBack)
	if back.Body != fields["text"] {
		t.Errorf("back = %q, want original text", back.Body)
	}
}

// TestClozeRenderRevealsOtherIndices 断言渲染目标序号时其它序号按原文显示。
func TestClozeRenderRevealsOtherIndices(t *testing.T) {
	fields := map[string]any{"text": "{{c1::one}} {{c2::two}}"}
	card := Card{Template: "cloze:1", Fields: fields}
	front, _ := clozeType{}.Render(card, SideFront)
	if front.Body != "[…] {{c2::two}}" {
		t.Errorf("front = %q, want c1 hidden and c2 shown", front.Body)
	}
	back, _ := clozeType{}.Render(card, SideBack)
	if back.Body != fields["text"] {
		t.Errorf("back = %q, want full text", back.Body)
	}
}

// TestClozeInvalid 断言各类非法输入返回可读英文错误。
func TestClozeInvalid(t *testing.T) {
	cases := map[string]string{
		"missing text":  `missing required field "text"`,
		"no deletion":   `must contain at least one cloze deletion`,
		"unclosed":      `unclosed cloze deletion`,
		"empty content": `has empty text`,
		"bad text type": `field "text" must be a string`,
	}
	fieldsByCase := map[string]map[string]any{
		"missing text":  {},
		"no deletion":   {"text": "plain text without markers"},
		"unclosed":      {"text": "start {{c1::never closed"},
		"empty content": {"text": "before {{c1::}} after"},
		"bad text type": {"text": 42},
	}
	for name, wantSubstr := range cases {
		err := Validate("cloze", fieldsByCase[name])
		if err == nil {
			t.Errorf("%s: want error", name)
			continue
		}
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Errorf("%s: error = %q, want substring %q", name, err, wantSubstr)
		}
	}
}

// TestClozeTemplateMismatch 断言不属于该题型的模板被拒绝。
func TestClozeTemplateMismatch(t *testing.T) {
	_, err := clozeType{}.Render(Card{Template: "forward", Fields: map[string]any{"text": "{{c1::x}}"}}, SideFront)
	if err == nil || !strings.Contains(err.Error(), "unknown card template") {
		t.Fatalf("Render = %v, want unknown template error", err)
	}
}
