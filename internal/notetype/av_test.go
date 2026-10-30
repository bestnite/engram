package notetype

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// latexDisabledNotice is the text Anki's collection layer appends when LaTeX
// image generation is switched off. The oracle fixture runs with generation off,
// so the LaTeX card's golden HTML ends with it on both sides; it is produced
// after the audio and LaTeX layer and is not part of what this package renders.
const latexDisabledNotice = "LaTeX image generation is disabled in the preferences."

// avGolden is the part of an oracle golden this package's audio, TTS and LaTeX
// ports consume.
type avGolden struct {
	Fixture   string             `json:"fixture"`
	NoteTypes []avGoldenNoteType `json:"note_types"`
	Notes     []avGoldenNote     `json:"notes"`
}

type avGoldenNoteType struct {
	Name     string `json:"name"`
	LaTeXSVG bool   `json:"latex_svg"`
}

type avGoldenNote struct {
	GUID         string         `json:"guid"`
	NoteTypeName string         `json:"notetype_name"`
	Cards        []avGoldenCard `json:"cards"`
}

type avGoldenCard struct {
	QuestionNodesText string              `json:"question_nodes_text"`
	AnswerNodesText   string              `json:"answer_nodes_text"`
	QuestionHTML      string              `json:"question_html"`
	AnswerHTML        string              `json:"answer_html"`
	QuestionCSS       string              `json:"question_css"`
	QuestionAVTags    []string            `json:"question_av_tags"`
	AnswerAVTags      []string            `json:"answer_av_tags"`
	QuestionParsed    []avGoldenParsedTag `json:"question_av_tags_parsed"`
	AnswerParsed      []avGoldenParsedTag `json:"answer_av_tags_parsed"`
}

type avGoldenParsedTag struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	FieldText string   `json:"field_text"`
	Lang      string   `json:"lang"`
	Voices    []string `json:"voices"`
	Speed     float64  `json:"speed"`
	OtherArgs []string `json:"other_args"`
}

func loadAVGolden(t *testing.T, fixture string) avGolden {
	t.Helper()
	encoded, err := os.ReadFile("testdata/anki/" + fixture + ".golden.json")
	if err != nil {
		t.Fatalf("read %s golden: %v", fixture, err)
	}
	var golden avGolden
	if err := json.Unmarshal(encoded, &golden); err != nil {
		t.Fatalf("decode %s golden: %v", fixture, err)
	}
	return golden
}

// TestAVLatexGolden rewrites every card of the audio/TTS/LaTeX fixture and
// compares the result with Anki's recorded output. The question side must equal
// the golden's question HTML, and the answer side is rebuilt the way Anki's
// Python layer does it: the already-rewritten question text stands in for
// {{FrontSide}}, then the answer nodes are rewritten as the answer side.
func TestAVLatexGolden(t *testing.T) {
	golden := loadAVGolden(t, "audio-tts-latex")
	svgByNoteType := make(map[string]bool, len(golden.NoteTypes))
	for _, noteType := range golden.NoteTypes {
		svgByNoteType[noteType.Name] = noteType.LaTeXSVG
	}

	cards := 0
	for _, note := range golden.Notes {
		svg := svgByNoteType[note.NoteTypeName]
		for _, card := range note.Cards {
			cards++
			t.Run(note.GUID, func(t *testing.T) {
				style := "<style>" + card.QuestionCSS + "</style>"
				wantQuestionHTML, ok := strings.CutPrefix(card.QuestionHTML, style)
				if !ok {
					t.Fatalf("question_html does not start with the card style")
				}
				wantAnswerHTML, ok := strings.CutPrefix(card.AnswerHTML, style)
				if !ok {
					t.Fatalf("answer_html does not start with the card style")
				}

				questionText, questionTags := ExtractAVTags(card.QuestionNodesText, "q")
				if got := RenderLatex(questionText, svg); got != trimLatexNotice(wantQuestionHTML) {
					t.Errorf("question html:\n got %q\nwant %q", got, trimLatexNotice(wantQuestionHTML))
				}
				checkAVTagSyntax(t, questionTags, card.QuestionAVTags, "question")
				checkAVTagStructs(t, questionTags, card.QuestionParsed, "question")

				if !strings.HasPrefix(card.AnswerNodesText, card.QuestionNodesText) {
					t.Fatalf("answer nodes do not start with the question nodes")
				}
				frontSide := questionText
				answerText, answerTags := ExtractAVTags(
					frontSide+strings.TrimPrefix(card.AnswerNodesText, card.QuestionNodesText), "a",
				)
				if got := RenderLatex(answerText, svg); got != trimLatexNotice(wantAnswerHTML) {
					t.Errorf("answer html:\n got %q\nwant %q", got, trimLatexNotice(wantAnswerHTML))
				}
				checkAVTagSyntax(t, answerTags, card.AnswerAVTags, "answer")
				checkAVTagStructs(t, answerTags, card.AnswerParsed, "answer")

				t.Logf("%s: %d question tags, %d answer tags, html matched",
					note.GUID, len(questionTags), len(answerTags))
			})
		}
	}
	if cards != 5 {
		t.Fatalf("golden holds %d cards, want 5", cards)
	}
}

// trimLatexNotice removes the collection layer's LaTeX-disabled notice from a
// golden HTML string. The notice is appended once per side when generation is
// off, so only the exact trailing sentence is removed.
func trimLatexNotice(html string) string {
	return strings.TrimSuffix(html, latexDisabledNotice)
}

// avTagSyntax renders a tag in the form Anki's oracle uses for the golden tag
// lists: the Anki tag syntax with the language, the voices (when present), the
// speed and then the uninterpreted options.
func avTagSyntax(tag AVTag) string {
	if tag.Kind == "sound" {
		return "[sound:" + tag.Name + "]"
	}
	options := []string{"lang=" + tag.Lang}
	if len(tag.Voices) > 0 {
		options = append(options, "voices="+strings.Join(tag.Voices, ","))
	}
	options = append(options, "speed="+avSpeedSyntax(tag.Speed))
	options = append(options, tag.OtherArgs...)
	return "[anki:tts " + strings.Join(options, " ") + "]" + tag.FieldText + "[/anki:tts]"
}

// avSpeedSyntax formats a speed the way Python's str() does, so 1.0 keeps its
// decimal point.
func avSpeedSyntax(speed float64) string {
	formatted := strconv.FormatFloat(speed, 'g', -1, 64)
	if !strings.ContainsAny(formatted, ".eE") {
		formatted += ".0"
	}
	return formatted
}

func checkAVTagSyntax(t *testing.T, got []AVTag, want []string, side string) {
	t.Helper()
	syntax := make([]string, 0, len(got))
	for _, tag := range got {
		syntax = append(syntax, avTagSyntax(tag))
	}
	if !slices.Equal(syntax, want) {
		t.Errorf("%s tags:\n got %q\nwant %q", side, syntax, want)
	}
}

func checkAVTagStructs(t *testing.T, got []AVTag, want []avGoldenParsedTag, side string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s parsed tags: got %d, want %d", side, len(got), len(want))
	}
	for index := range got {
		tag, expected := got[index], want[index]
		if tag.Kind != expected.Kind {
			t.Errorf("%s tag %d kind: got %q, want %q", side, index, tag.Kind, expected.Kind)
		}
		if tag.Name != expected.Name {
			t.Errorf("%s tag %d name: got %q, want %q", side, index, tag.Name, expected.Name)
		}
		if tag.Lang != expected.Lang {
			t.Errorf("%s tag %d lang: got %q, want %q", side, index, tag.Lang, expected.Lang)
		}
		if !slices.Equal(tag.Voices, expected.Voices) {
			t.Errorf("%s tag %d voices: got %q, want %q", side, index, tag.Voices, expected.Voices)
		}
		if tag.Speed != expected.Speed {
			t.Errorf("%s tag %d speed: got %v, want %v", side, index, tag.Speed, expected.Speed)
		}
		if !slices.Equal(tag.OtherArgs, expected.OtherArgs) {
			t.Errorf("%s tag %d other args: got %q, want %q", side, index, tag.OtherArgs, expected.OtherArgs)
		}
		if tag.FieldText != expected.FieldText {
			t.Errorf("%s tag %d field text: got %q, want %q", side, index, tag.FieldText, expected.FieldText)
		}
	}
}

// TestExtractAVTags covers the parser's boundaries directly: what is a tag, what
// stays text, which options are interpreted, and how the marker numbers run.
func TestExtractAVTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		side     string
		wantHTML string
		wantTags []string
	}{
		{
			name:     "plain text is unchanged",
			input:    "foo",
			side:     "q",
			wantHTML: "foo",
		},
		{
			name:     "sound tag becomes a question marker",
			input:    "foo [sound:placeholder.wav] baz",
			side:     "q",
			wantHTML: "foo [anki:play:q:0] baz",
			wantTags: []string{"[sound:placeholder.wav]"},
		},
		{
			name:     "empty sound tag is text",
			input:    "[sound:]",
			side:     "q",
			wantHTML: "[sound:]",
		},
		{
			name:     "sound tags number in document order",
			input:    "[sound:a.wav][sound:b.wav]",
			side:     "q",
			wantHTML: "[anki:play:q:0][anki:play:q:1]",
			wantTags: []string{"[sound:a.wav]", "[sound:b.wav]"},
		},
		{
			name:     "answer side numbers from zero",
			input:    "[sound:a.wav][sound:b.wav]",
			side:     "a",
			wantHTML: "[anki:play:a:0][anki:play:a:1]",
			wantTags: []string{"[sound:a.wav]", "[sound:b.wav]"},
		},
		{
			name:     "tts tag defaults the speed",
			input:    "[anki:tts lang=en_US]speech[/anki:tts]",
			side:     "q",
			wantHTML: "[anki:play:q:0]",
			wantTags: []string{"[anki:tts lang=en_US speed=1.0]speech[/anki:tts]"},
		},
		{
			name:     "unknown tts options keep document order",
			input:    "[anki:tts lang=en_US z=1 a=2]speech[/anki:tts]",
			side:     "q",
			wantHTML: "[anki:play:q:0]",
			wantTags: []string{"[anki:tts lang=en_US speed=1.0 z=1 a=2]speech[/anki:tts]"},
		},
		{
			name:     "unparseable speed falls back to one",
			input:    "[anki:tts lang=en_US speed=fast]speech[/anki:tts]",
			side:     "q",
			wantHTML: "[anki:play:q:0]",
			wantTags: []string{"[anki:tts lang=en_US speed=1.0]speech[/anki:tts]"},
		},
		{
			name:     "tts html is stripped from the field text",
			input:    "[anki:tts lang=ja_JP voices=alpha,beta speed=0.75]spoken <b>text</b>[/anki:tts]",
			side:     "q",
			wantHTML: "[anki:play:q:0]",
			wantTags: []string{"[anki:tts lang=ja_JP voices=alpha,beta speed=0.75]spoken text[/anki:tts]"},
		},
		{
			name:     "sound inside tts is not a separate tag",
			input:    "[anki:tts lang=en_US]say [sound:placeholder.wav][/anki:tts]",
			side:     "q",
			wantHTML: "[anki:play:q:0]",
			wantTags: []string{"[anki:tts lang=en_US speed=1.0]say [sound:placeholder.wav][/anki:tts]"},
		},
		{
			name:     "block boundary inside tts is a space",
			input:    "[anki:tts lang=en_US]one<br>two[/anki:tts]",
			side:     "q",
			wantHTML: "[anki:play:q:0]",
			wantTags: []string{"[anki:tts lang=en_US speed=1.0]one two[/anki:tts]"},
		},
		{
			name:     "mismatched directive is text",
			input:    "[anki:foo][/anki:bar]",
			side:     "q",
			wantHTML: "[anki:foo][/anki:bar]",
		},
		{
			name:     "other directive keeps its body",
			input:    "a[anki:foo bar=baz]x[/anki:foo]b",
			side:     "q",
			wantHTML: "a[anki:foo bar=baz]x[/anki:foo]b",
		},
		{
			name:     "other directive is normalized",
			input:    "[anki:foo\nbar=baz ]x[/anki:foo]",
			side:     "q",
			wantHTML: "[anki:foo bar=baz]x[/anki:foo]",
		},
		{
			name:     "non-option text fails the opening tag",
			input:    "[anki:foo bar]x[/anki:foo]",
			side:     "q",
			wantHTML: "[anki:foo bar]x[/anki:foo]",
		},
		{
			name:     "sound prefix is case sensitive",
			input:    "[SOUND:x.wav]",
			side:     "q",
			wantHTML: "[SOUND:x.wav]",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			html, tags := ExtractAVTags(test.input, test.side)
			if html != test.wantHTML {
				t.Errorf("html: got %q, want %q", html, test.wantHTML)
			}
			syntax := make([]string, 0, len(tags))
			for _, tag := range tags {
				syntax = append(syntax, avTagSyntax(tag))
			}
			if !slices.Equal(syntax, test.wantTags) {
				t.Errorf("tags: got %q, want %q", syntax, test.wantTags)
			}
		})
	}
}

// TestExtractAVTagsClozeBlank pins the one TTS option this package substitutes
// itself instead of keeping it in OtherArgs.
func TestExtractAVTagsClozeBlank(t *testing.T) {
	html, tags := ExtractAVTags("[anki:tts lang=en_US cloze_blank=World]Hello [...]![/anki:tts]", "q")
	if html != "[anki:play:q:0]" {
		t.Errorf("html: got %q", html)
	}
	if len(tags) != 1 {
		t.Fatalf("tags: got %d, want 1", len(tags))
	}
	if tags[0].ClozeBlank == nil || *tags[0].ClozeBlank != "World" {
		t.Errorf("cloze blank: got %v, want World", tags[0].ClozeBlank)
	}
	if tags[0].FieldText != "Hello World!" {
		t.Errorf("field text: got %q, want %q", tags[0].FieldText, "Hello World!")
	}
	if len(tags[0].OtherArgs) != 0 {
		t.Errorf("other args: got %q, want none", tags[0].OtherArgs)
	}

	_, withoutBlank := ExtractAVTags("[anki:tts lang=en_US]Hello [...]![/anki:tts]", "q")
	if len(withoutBlank) != 1 {
		t.Fatalf("tags: got %d, want 1", len(withoutBlank))
	}
	if withoutBlank[0].ClozeBlank != nil {
		t.Errorf("cloze blank: got %v, want nil", withoutBlank[0].ClozeBlank)
	}
	if withoutBlank[0].FieldText != "Hello [...]!" {
		t.Errorf("field text: got %q, want %q", withoutBlank[0].FieldText, "Hello [...]!")
	}
}

// TestReplayButtonHTML pins the markup Anki's desktop frame puts in place of a
// play marker. The tag itself does not change the markup.
func TestReplayButtonHTML(t *testing.T) {
	const questionSound = `
<a class="replay-button soundLink" href=# onclick="pycmd('play:q:0'); return false;" draggable="false">
    <svg class="playImage" viewBox="0 0 64 64" version="1.1">
        <circle cx="32" cy="32" r="29" />
        <path d="M56.502,32.301l-37.502,20.101l0.329,-40.804l37.173,20.703Z" />
    </svg>
</a>`
	const answerTTS = `
<a class="replay-button soundLink" href=# onclick="pycmd('play:a:2'); return false;" draggable="false">
    <svg class="playImage" viewBox="0 0 64 64" version="1.1">
        <circle cx="32" cy="32" r="29" />
        <path d="M56.502,32.301l-37.502,20.101l0.329,-40.804l37.173,20.703Z" />
    </svg>
</a>`

	tests := []struct {
		name  string
		tag   AVTag
		side  string
		index int
		want  string
	}{
		{
			name:  "sound on the question",
			tag:   AVTag{Kind: "sound", Name: "placeholder.wav"},
			side:  "q",
			index: 0,
			want:  questionSound,
		},
		{
			name:  "tts on the answer",
			tag:   AVTag{Kind: "tts", Lang: "en_US", FieldText: "speech"},
			side:  "a",
			index: 2,
			want:  answerTTS,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ReplayButtonHTML(test.tag, test.side, test.index); got != test.want {
				t.Errorf("markup:\n got %q\nwant %q", got, test.want)
			}
		})
	}
}

// TestReplayButtonMatchesMarker checks that a button's pycmd names the marker it
// replaces: ExtractAVTags writes [anki:play:<side>:<index>] and the button hands
// the same "play:<side>:<index>" back to the frame.
func TestReplayButtonMatchesMarker(t *testing.T) {
	html, tags := ExtractAVTags("[sound:a.wav] mid [anki:tts lang=en_US]speech[/anki:tts]", "q")
	if html != "[anki:play:q:0] mid [anki:play:q:1]" {
		t.Fatalf("markers: got %q", html)
	}
	for index, tag := range tags {
		button := ReplayButtonHTML(tag, "q", index)
		want := "pycmd('play:q:" + strconv.Itoa(index) + "')"
		if !strings.Contains(button, want) {
			t.Errorf("tag %d button %q does not contain %q", index, button, want)
		}
	}
}
