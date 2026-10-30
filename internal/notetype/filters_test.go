package notetype

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/zeebo/blake3"
)

func filterContext(cardOrdinal int, onFront bool) *renderContext {
	return &renderContext{cardOrdinal: cardOrdinal, onFront: onFront}
}

func TestApplyFilters(t *testing.T) {
	ctx := filterContext(1, true)
	tests := []struct {
		name    string
		text    string
		filters []string
		field   string
		want    string
	}{
		{
			name:  "no filters",
			text:  "<b>a</b>",
			field: "Front",
			want:  "<b>a</b>",
		},
		{
			name:    "text strips markup",
			text:    `<b>bold</b> <img src="placeholder.png"> tail`,
			filters: []string{"text"},
			field:   "Front",
			want:    "bold  tail",
		},
		{
			name:    "text strips comments and script bodies",
			text:    `a<!-- hidden -->b<script>var x = "<b>";</script>c<style>.x{}</style>`,
			filters: []string{"text"},
			field:   "Front",
			want:    "abc",
		},
		{
			name:    "text decodes entities",
			text:    "&lt;tag&gt; &amp; &nbsp;end",
			filters: []string{"text"},
			field:   "Front",
			want:    "<tag> &  end",
		},
		{
			name:    "empty filter name is ignored",
			text:    "a",
			filters: []string{""},
			field:   "Front",
			want:    "a",
		},
		{
			name:    "unknown filter is skipped",
			text:    "<b>a</b>",
			filters: []string{"unknown"},
			field:   "Front",
			want:    "<b>a</b>",
		},
		{
			name:    "unknown filter keeps the chain working",
			text:    "<b>a</b>",
			filters: []string{"unknown", "text"},
			field:   "Front",
			want:    "a",
		},
		{
			name:    "type filter",
			text:    "ignored",
			filters: []string{"type"},
			field:   "Front",
			want:    "[[type:Front]]",
		},
		{
			name:    "type-cloze filter",
			text:    "ignored",
			filters: []string{"type-cloze"},
			field:   "Text",
			want:    "[[type:cloze:Text]]",
		},
		{
			name:    "type-nc filter",
			text:    "ignored",
			filters: []string{"type-nc"},
			field:   "Text",
			want:    "[[type:nc:Text]]",
		},
		{
			name:    "cloze modifier folds into type",
			text:    "ignored",
			filters: []string{"cloze", "type"},
			field:   "Text",
			want:    "[[type:cloze:Text]]",
		},
		{
			name:    "nc modifier folds into type",
			text:    "ignored",
			filters: []string{"nc", "type"},
			field:   "Text",
			want:    "[[type:nc:Text]]",
		},
		{
			name:    "unrelated filter before type is dropped",
			text:    "ignored",
			filters: []string{"some", "unknown", "type"},
			field:   "Text",
			want:    "[[type:Text]]",
		},
		{
			name:    "tts filter",
			text:    "foo",
			filters: []string{"tts en_US voices=Bob,Jane"},
			field:   "Front",
			want:    "[anki:tts lang=en_US voices=Bob,Jane]foo[/anki:tts]",
		},
		{
			name:    "filter chain applies right to left",
			text:    "<b>chained</b> text",
			filters: []string{"hint", "text"},
			field:   "Front",
			want:    "\n\nFront\nchained text\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := applyFilters(test.text, test.filters, test.field, ctx); got != test.want {
				t.Errorf("applyFilters() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTextFilter(t *testing.T) {
	// A tag split over a line break is still a tag, because the pattern is
	// case-insensitive and matches across newlines.
	text := "<B\nclass=x>a</B>"
	if got := applyFilters(text, []string{"text"}, "Front", filterContext(1, true)); got != "a" {
		t.Errorf("applyFilters() = %q, want %q", got, "a")
	}
}

func TestJapaneseFilters(t *testing.T) {
	text := "test first[second] third[fourth]"
	ctx := filterContext(1, true)
	tests := []struct {
		filter string
		want   string
	}{
		{"kana", "testsecondfourth"},
		{"kanji", "testfirstthird"},
		{"furigana", "test<ruby><rb>first</rb><rt>second</rt></ruby><ruby><rb>third</rb><rt>fourth</rt></ruby>"},
	}
	for _, test := range tests {
		t.Run(test.filter, func(t *testing.T) {
			if got := applyFilters(text, []string{test.filter}, "Front", ctx); got != test.want {
				t.Errorf("applyFilters() = %q, want %q", got, test.want)
			}
		})
	}

	t.Run("sound tags are kept", func(t *testing.T) {
		text := "word[sound:word.mp3]"
		for _, filter := range []string{"furigana", "kana", "kanji"} {
			if got := applyFilters(text, []string{filter}, "Front", ctx); got != "word[sound:word.mp3]" {
				t.Errorf("%s = %q, want the original text", filter, got)
			}
		}
	})

	t.Run("non-breaking spaces become spaces", func(t *testing.T) {
		// The reading replaces the space before the word as well, because the
		// pattern treats that space as part of the ruby pair.
		if got := applyFilters("a&nbsp;b[reading]", []string{"kana"}, "Front", ctx); got != "areading" {
			t.Errorf("kana = %q, want %q", got, "areading")
		}
	})
}

func TestHintFilter(t *testing.T) {
	// The id is Anki's BLAKE3 hash of the text followed by the field name.
	tests := []struct {
		name  string
		text  string
		field string
		want  string
	}{
		{
			name:  "text and field",
			text:  "foo",
			field: "field",
			want: `
<a class=hint href="#"
onclick="this.style.display='none';
document.getElementById('hint83fe48607f0f3a66').style.display='block';
return false;" draggable=false>
field</a>
<div id="hint83fe48607f0f3a66" class=hint style="display: none">foo</div>
`,
		},
		{
			name:  "golden fixture text",
			text:  "<b>hinted</b> text",
			field: "Front",
			want: `
<a class=hint href="#"
onclick="this.style.display='none';
document.getElementById('hintfe2d154c35ff3d09').style.display='block';
return false;" draggable=false>
Front</a>
<div id="hintfe2d154c35ff3d09" class=hint style="display: none"><b>hinted</b> text</div>
`,
		},
		{
			name:  "blank text is left alone",
			text:  "  ",
			field: "Front",
			want:  "  ",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := applyFilters(test.text, []string{"hint"}, test.field, filterContext(1, true)); got != test.want {
				t.Errorf("hint = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClozeFilters(t *testing.T) {
	text := "{{c1::one}} {{c2::two::hint}}"
	tests := []struct {
		name          string
		text          string
		ordinal       int
		onFront       bool
		want          string
		wantClozeOnly string
	}{
		{
			name:          "front hides the active cloze",
			text:          text,
			ordinal:       1,
			onFront:       true,
			want:          `<span class="cloze" data-cloze="one" data-ordinal="1">[...]</span> <span class="cloze-inactive" data-ordinal="2">two</span>`,
			wantClozeOnly: "...",
		},
		{
			name:          "back reveals the active cloze",
			text:          text,
			ordinal:       1,
			onFront:       false,
			want:          `<span class="cloze" data-ordinal="1">one</span> <span class="cloze-inactive" data-ordinal="2">two</span>`,
			wantClozeOnly: "one",
		},
		{
			name:          "hint on the front",
			text:          text,
			ordinal:       2,
			onFront:       true,
			want:          `<span class="cloze-inactive" data-ordinal="1">one</span> <span class="cloze" data-cloze="two" data-ordinal="2">[hint]</span>`,
			wantClozeOnly: "hint",
		},
		{
			name:          "text is escaped in the cloze attribute",
			text:          `{{c1::<b>"one"</b>}}`,
			ordinal:       1,
			onFront:       true,
			want:          `<span class="cloze" data-cloze="&lt;b&gt;&quot;one&quot;&lt;/b&gt;" data-ordinal="1">[...]</span>`,
			wantClozeOnly: "...",
		},
		{
			name:          "missing cloze number renders nothing",
			text:          text,
			ordinal:       3,
			onFront:       true,
			want:          "",
			wantClozeOnly: "",
		},
		{
			name:          "text without clozes is dropped by the cloze filter",
			text:          "plain",
			ordinal:       1,
			onFront:       true,
			want:          "",
			wantClozeOnly: "",
		},
		{
			name:          "multiple numbers in one marker",
			text:          "{{c1,2::both}}",
			ordinal:       2,
			onFront:       false,
			want:          `<span class="cloze" data-ordinal="1,2">both</span>`,
			wantClozeOnly: "both",
		},
		{
			name:          "markup inside mathjax is kept",
			text:          `\(x < y\) {{c1::two}}`,
			ordinal:       1,
			onFront:       false,
			want:          `\(x < y\) <span class="cloze" data-ordinal="1">two</span>`,
			wantClozeOnly: "two",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := applyFilters(test.text, []string{"cloze"}, "Text", filterContext(test.ordinal, test.onFront))
			if got != test.want {
				t.Errorf("cloze = %q, want %q", got, test.want)
			}
			only := applyFilters(test.text, []string{"cloze-only"}, "Text", filterContext(test.ordinal, test.onFront))
			if only != test.wantClozeOnly {
				t.Errorf("cloze-only = %q, want %q", only, test.wantClozeOnly)
			}
		})
	}
}

func TestClozeOnlyJoinsMatches(t *testing.T) {
	text := "{{c1::one}} {{c1::two}}"
	if got := applyFilters(text, []string{"cloze-only"}, "Text", filterContext(1, false)); got != "one, two" {
		t.Errorf("cloze-only = %q, want %q", got, "one, two")
	}
}

func TestImageOcclusionMarkup(t *testing.T) {
	text := "{{c1::image-occlusion:rect:left=0.1:top=0.2:width=0.3:height=0.4:oi=1}}"
	shape := `data-shape="rect" data-left="0.1" data-top="0.2" data-width="0.3" data-height="0.4" data-occludeInactive="1" `
	tests := []struct {
		name    string
		text    string
		ordinal int
		onFront bool
		want    string
	}{
		{
			name:    "front hides the shape",
			text:    text,
			ordinal: 1,
			onFront: true,
			want:    `<div class="cloze" data-ordinal="1" ` + shape + `></div>`,
		},
		{
			name:    "back highlights the shape",
			text:    text,
			ordinal: 1,
			onFront: false,
			want:    `<div class="cloze-highlight" data-ordinal="1" ` + shape + `></div>`,
		},
		{
			name:    "a different card leaves the shape inactive",
			text:    text,
			ordinal: 2,
			onFront: true,
			want:    ``,
		},
		{
			// A shape for another card is not shown, and the cloze filter drops
			// a field with no active cloze entirely.
			name:    "a shape of another card renders nothing",
			text:    "{{c0::image-occlusion:ellipse:left=0.5}}",
			ordinal: 1,
			onFront: false,
			want:    ``,
		},
		{
			name:    "ordinal zero alongside the card's number",
			text:    "{{c0,1::image-occlusion:ellipse:left=0.5}}",
			ordinal: 1,
			onFront: true,
			want:    `<div class="cloze" data-ordinal="0,1" data-shape="ellipse" data-left="0.5" ></div>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := applyFilters(test.text, []string{"cloze"}, "Occlusion", filterContext(test.ordinal, test.onFront)); got != test.want {
				t.Errorf("cloze = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseImageCloze(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		shape      string
		properties []imageOcclusionProperty
	}{
		{
			name:  "rect",
			text:  "rect:left=0.1:top=0.2",
			shape: "rect",
			properties: []imageOcclusionProperty{
				{name: "left", value: "0.1"},
				{name: "top", value: "0.2"},
			},
		},
		{
			name:  "escaped colon",
			text:  `rect:left=0.1:text=a\:b`,
			shape: "rect",
			properties: []imageOcclusionProperty{
				{name: "left", value: "0.1"},
				{name: "text", value: "a:b"},
			},
		},
		{
			name:  "not an image occlusion",
			text:  "rect",
			shape: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shape, properties, ok := parseImageCloze(test.text)
			if shape == "" {
				if ok {
					t.Fatalf("parseImageCloze(%q) accepted a shape without a colon", test.text)
				}
				return
			}
			if !ok || shape != test.shape {
				t.Fatalf("shape = %q, %v, want %q, true", shape, ok, test.shape)
			}
			if len(properties) != len(test.properties) {
				t.Fatalf("properties = %v, want %v", properties, test.properties)
			}
			for i, property := range properties {
				if property != test.properties[i] {
					t.Errorf("property %d = %v, want %v", i, property, test.properties[i])
				}
			}
		})
	}
}

func TestClozeNumbersIn(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"test", ""},
		{"{{c2::te}}{{c1::s}}t{{", "1,2"},
		{"{{c0::te}}s{{c2::t}}s", "2"},
		{"{{c1,3::both}}", "1,3"},
		{"{{c1::outer {{c2::inner}}}}", "1,2"},
		{"{{c1::unclosed", ""},
		{"{{c1::text::with::hint}}", "1"},
	}
	for _, test := range tests {
		numbers := clozeNumbersIn(test.text)
		var got []string
		for _, ordinal := range []int{0, 1, 2, 3} {
			if numbers[ordinal] {
				got = append(got, string(rune('0'+ordinal)))
			}
		}
		if joined := strings.Join(got, ","); joined != test.want {
			t.Errorf("clozeNumbersIn(%q) = %q, want %q", test.text, joined, test.want)
		}
	}
}

func TestParseClozes(t *testing.T) {
	t.Run("hint defaults to three dots", func(t *testing.T) {
		nodes := parseClozes("{{c1::one}}")
		if len(nodes) != 1 || nodes[0].cloze == nil {
			t.Fatalf("parseClozes() = %v, want one cloze", nodes)
		}
		if got := nodes[0].cloze.hintText(); got != "..." {
			t.Errorf("hint = %q, want %q", got, "...")
		}
	})

	t.Run("stray closing marker stays text", func(t *testing.T) {
		nodes := parseClozes("a}}b")
		if len(nodes) != 3 {
			t.Fatalf("parseClozes() = %v, want text, marker, text", nodes)
		}
		if nodes[1].text != "}}" {
			t.Errorf("middle node = %q, want %q", nodes[1].text, "}}")
		}
	})

	t.Run("nested clozes", func(t *testing.T) {
		nodes := parseClozes("{{c1::outer {{c2::inner}}}}")
		if len(nodes) != 1 || nodes[0].cloze == nil || len(nodes[0].cloze.children) != 2 {
			t.Fatalf("parseClozes() = %v, want one cloze with two children", nodes)
		}
		if nodes[0].cloze.children[1].cloze == nil {
			t.Error("the nested cloze was not attached to its parent")
		}
	})
}

// The hint element id is what makes the hash observable, so it is pinned twice:
// against the ids Anki produced, which the golden fixtures also contain, and
// against the published BLAKE3 vectors, so swapping the hash function fails the
// test instead of silently changing every hint id.
func TestHintID(t *testing.T) {
	t.Run("ids Anki produces", func(t *testing.T) {
		tests := []struct {
			text  string
			field string
			want  string
		}{
			{"foo", "field", "83fe48607f0f3a66"},
			{"<b>hinted</b> text", "Front", "fe2d154c35ff3d09"},
		}
		for _, test := range tests {
			if got := hintID(test.text, test.field); got != test.want {
				t.Errorf("hintID(%q, %q) = %s, want %s", test.text, test.field, got, test.want)
			}
		}
	})

	t.Run("published BLAKE3 vectors", func(t *testing.T) {
		tests := []struct {
			name  string
			input []byte
			want  string // first eight bytes, the part a hint id exposes
		}{
			{"empty", nil, "af1349b9f5f9a1a6"},
			{"abc", []byte("abc"), "6437b3ac38465133"},
			{
				// 1025 bytes crosses the first chunk boundary, so this covers
				// the tree hashing a long field goes through.
				name:  "one chunk and one byte",
				input: patternBytes(1025),
				want:  "d00278ae47eb27b3",
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				sum := blake3.Sum256(test.input)
				if got := hex.EncodeToString(sum[:8]); got != test.want {
					t.Errorf("Sum256(...)[:8] = %s, want %s", got, test.want)
				}
			})
		}
	})
}

// patternBytes is the repeating byte pattern the published BLAKE3 test vectors
// use.
func patternBytes(length int) []byte {
	input := make([]byte, length)
	for i := range input {
		input[i] = byte(i % 251)
	}
	return input
}
