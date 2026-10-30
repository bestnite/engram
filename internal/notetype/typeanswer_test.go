package notetype

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// typeAnswerGolden is the part of an oracle golden the type-in comparison
// consumes: the recorded comparison inputs and outputs, plus the note fields and
// cloze extraction that produce the expected text.
type typeAnswerGolden struct {
	Fixture        string                  `json:"fixture"`
	CompareAnswers []typeAnswerGoldenEntry `json:"compare_answers"`
	Notes          []typeAnswerGoldenNote  `json:"notes"`
}

type typeAnswerGoldenEntry struct {
	NoteTypeName   string `json:"notetype_name"`
	GUID           string `json:"guid"`
	Expected       string `json:"expected"`
	ExpectedSource string `json:"expected_source"`
	Provided       string `json:"provided"`
	Combining      bool   `json:"combining"`
	Output         string `json:"output"`
}

type typeAnswerGoldenNote struct {
	GUID        string                  `json:"guid"`
	Fields      map[string]string       `json:"fields"`
	Cards       []typeAnswerGoldenCard  `json:"cards"`
	ClozeTyping []typeAnswerClozeTyping `json:"cloze_typing"`
}

type typeAnswerGoldenCard struct {
	Ordinal int `json:"ordinal"`
}

// typeAnswerClozeTyping is the text Anki extracted for one cloze number of the
// note, recorded by the oracle so the extraction itself is pinned.
type typeAnswerClozeTyping struct {
	Ordinal int    `json:"ordinal"`
	Text    string `json:"text"`
}

func loadTypeAnswerGolden(t *testing.T, fixture string) typeAnswerGolden {
	t.Helper()
	encoded, err := os.ReadFile("testdata/anki/" + fixture + ".golden.json")
	if err != nil {
		t.Fatalf("read %s golden: %v", fixture, err)
	}
	var golden typeAnswerGolden
	if err := json.Unmarshal(encoded, &golden); err != nil {
		t.Fatalf("decode %s golden: %v", fixture, err)
	}
	return golden
}

// TestCompareAnswerGolden compares every recorded type-in answer with Anki's
// output. field:<Name> entries compare against the golden's expected text; for
// cloze:<Name> entries the expected text is re-derived from the note's field and
// the card's cloze number, and the derivation must equal the golden text first.
func TestCompareAnswerGolden(t *testing.T) {
	for _, fixture := range []string{"typeanswer", "stock"} {
		golden := loadTypeAnswerGolden(t, fixture)
		if fixture == "typeanswer" && len(golden.CompareAnswers) != 14 {
			t.Fatalf("%s golden holds %d comparison entries, want 14", fixture, len(golden.CompareAnswers))
		}
		notes := make(map[string]typeAnswerGoldenNote, len(golden.Notes))
		for _, note := range golden.Notes {
			notes[note.GUID] = note
		}

		t.Run(fixture, func(t *testing.T) {
			for index, entry := range golden.CompareAnswers {
				entry := entry
				t.Run(fmt.Sprintf("%02d-%s", index, entry.GUID), func(t *testing.T) {
					expected, err := expectedTypeAnswer(notes, entry)
					if err != nil {
						t.Fatal(err)
					}
					got := CompareAnswer(expected, entry.Provided, entry.Combining)
					if got != entry.Output {
						t.Errorf("output:\n got %q\nwant %q", got, entry.Output)
					}
					t.Logf("%s: expected %q, typed %q, combining=%v, matched",
						entry.GUID, expected, entry.Provided, entry.Combining)
				})
			}
			t.Logf("%s: %d comparison entries matched", fixture, len(golden.CompareAnswers))
		})
	}
}

// expectedTypeAnswer returns the expected text of one golden entry, deriving it
// from the note for a cloze source and checking the derived value against the
// recorded one.
func expectedTypeAnswer(notes map[string]typeAnswerGoldenNote, entry typeAnswerGoldenEntry) (string, error) {
	kind, fieldName, ok := strings.Cut(entry.ExpectedSource, ":")
	if !ok {
		return "", fmt.Errorf("expected source %q has no field", entry.ExpectedSource)
	}
	switch kind {
	case "field":
		return entry.Expected, nil
	case "cloze":
		note, ok := notes[entry.GUID]
		if !ok {
			return "", fmt.Errorf("golden has no note %q", entry.GUID)
		}
		field, ok := note.Fields[fieldName]
		if !ok {
			return "", fmt.Errorf("note %q has no field %q", entry.GUID, fieldName)
		}
		for _, typing := range note.ClozeTyping {
			if got := ExtractClozeForTyping(field, typing.Ordinal); got != typing.Text {
				return "", fmt.Errorf("cloze number %d of %q extracts to %q, want %q",
					typing.Ordinal, entry.GUID, got, typing.Text)
			}
		}
		for _, card := range note.Cards {
			if got := ExtractClozeForTyping(field, card.Ordinal+1); got == entry.Expected {
				return got, nil
			}
		}
		return "", fmt.Errorf("no cloze number of %q extracts to %q", entry.GUID, entry.Expected)
	default:
		return "", fmt.Errorf("unknown expected source kind %q", kind)
	}
}

// TestCompareAnswer pins the comparison itself, including the Unicode rules: NFC
// equality, combining-mark removal in the non-combining variant, and the
// non-breaking space that keeps a leading mark from joining the previous token.
// Every expected string here was captured from Anki 26.09.3.
func TestCompareAnswer(t *testing.T) {
	tests := []struct {
		name      string
		expected  string
		typed     string
		combining bool
		want      string
	}{
		{
			name:      "an exact answer is one good run",
			expected:  "42",
			typed:     "42",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>42</span></code>",
		},
		{
			name:      "a missing character is shown as a dash and in the expected line",
			expected:  "42",
			typed:     "4",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>4</span><span class=typeMissed>-</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>4</span><span class=typeMissed>2</span></code>",
		},
		{
			name:      "extra input is bad on the typed line only",
			expected:  "42",
			typed:     "142",
			combining: true,
			want:      "<code id=typeans><span class=typeBad>1</span><span class=typeGood>42</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>42</span></code>",
		},
		{
			name:      "an extra space is bad input",
			expected:  "a b",
			typed:     "a  b",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>a </span><span class=typeBad> </span><span class=typeGood>b</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>a </span><span class=typeGood>b</span></code>",
		},
		{
			name:      "a combining mark on the expected side is isolated with a non-breaking space",
			expected:  "\u05d7\u05d5\u05b9\u05e3",
			typed:     "\u05d7\u05d5\u05e4",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>\u05d7\u05d5</span><span class=typeBad>\u05e4</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>\u05d7\u05d5</span><span class=typeMissed>\u00a0\u05b9\u05e3</span></code>",
		},
		{
			name:      "the non-combining variant ignores the missing mark but still shows it",
			expected:  "\u05d7\u05d5\u05b9\u05e3",
			typed:     "\u05d7\u05d5\u05e4",
			combining: false,
			want:      "<code id=typeans><span class=typeGood>\u05d7\u05d5</span><span class=typeBad>\u05e4</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>\u05d7\u05d5\u05b9</span><span class=typeMissed>\u05e3</span></code>",
		},
		{
			name:      "a decomposed typed answer equals a composed expected one",
			expected:  "\u00e9",
			typed:     "e\u0301",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>\u00e9</span></code>",
		},
		{
			name:      "a decomposed expected answer equals a composed typed one",
			expected:  "e\u0301",
			typed:     "\u00e9",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>\u00e9</span></code>",
		},
		{
			name:      "a leading combining mark is shown after a non-breaking space",
			expected:  "\u0301x",
			typed:     "x",
			combining: true,
			want:      "<code id=typeans><span class=typeMissed>-</span><span class=typeGood>x</span><br><span id=typearrow>&darr;</span><br><span class=typeMissed>\u00a0\u0301</span><span class=typeGood>x</span></code>",
		},
		{
			name:      "the non-combining variant keeps a leading mark when both sides match",
			expected:  "\u0301x",
			typed:     "x",
			combining: false,
			want:      "<code id=typeans><span class=typeGood>\u0301x</span></code>",
		},
		{
			name:      "markup is stripped from the expected text",
			expected:  "<b>1</b> &nbsp;2",
			typed:     "1  2",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>1  2</span></code>",
		},
		{
			name:      "a sound tag contributes no expected text",
			expected:  "[sound:foo.mp3]42",
			typed:     "42",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>42</span></code>",
		},
		{
			name:      "a tts directive and its body contribute no expected text",
			expected:  "[anki:tts lang=en_US]spoken[/anki:tts]42",
			typed:     "42",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>42</span></code>",
		},
		{
			name:      "an expected answer that is only a sound tag is empty",
			expected:  "[sound:a.mp3]",
			typed:     "",
			combining: true,
			want:      "<code id=typeans></code>",
		},
		{
			name:      "an empty expected answer marks the whole input bad",
			expected:  "",
			typed:     "extra",
			combining: true,
			want:      "<code id=typeans><span class=typeBad>extra</span><br><span id=typearrow>&darr;</span><br></code>",
		},
		{
			name:      "an empty typed answer shows only the expected text",
			expected:  "42",
			typed:     "",
			combining: true,
			want:      "<code id=typeans>42</code>",
		},
		{
			name:      "both sides empty",
			expected:  "",
			typed:     "",
			combining: true,
			want:      "<code id=typeans></code>",
		},
		{
			name:      "a typed answer of only combining marks is escaped as content",
			expected:  "\u0301\u0302",
			typed:     "",
			combining: true,
			want:      "<code id=typeans>\u0301\u0302</code>",
		},
		{
			name:      "a line break separates words",
			expected:  "one<br>two",
			typed:     "one two",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>one two</span></code>",
		},
		{
			name:      "a spaced line break separator separates words",
			expected:  "one<br />two",
			typed:     "one two",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>one two</span></code>",
		},
		{
			name:      "div boundaries separate words",
			expected:  "<div>one</div><div>two</div>",
			typed:     "one two",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>one two</span></code>",
		},
		{
			name:      "a paragraph is not a word separator",
			expected:  "<p>one</p><p>two</p>",
			typed:     "one two",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>one</span><span class=typeBad> </span><span class=typeGood>two</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>one</span><span class=typeGood>two</span></code>",
		},
		{
			name:      "a newline in the expected text is a space",
			expected:  "one\ntwo",
			typed:     "one two",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>one two</span></code>",
		},
		{
			name:      "the expected text is trimmed",
			expected:  "  42  ",
			typed:     "42",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>42</span></code>",
		},
		{
			name:      "an entity becomes the character the reader types",
			expected:  "a &amp; b",
			typed:     "a & b",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>a &amp; b</span></code>",
		},
		{
			name:      "a comparison of angle brackets is escaped",
			expected:  "&lt;dir&gt;",
			typed:     "<dir>",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>&lt;dir&gt;</span></code>",
		},
		{
			name:      "a non-breaking space is trimmed like a space",
			expected:  "&nbsp;42&nbsp;",
			typed:     "42",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>42</span></code>",
		},
		{
			name:      "the non-combining variant does not separate words either",
			expected:  "a b",
			typed:     "ab",
			combining: false,
			want:      "<code id=typeans><span class=typeGood>a</span><span class=typeMissed>-</span><span class=typeGood>b</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>a</span><span class=typeMissed> </span><span class=typeGood>b</span></code>",
		},
		{
			name:      "the plain variant shows the same missing space",
			expected:  "a b",
			typed:     "ab",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>a</span><span class=typeMissed>-</span><span class=typeGood>b</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>a</span><span class=typeMissed> </span><span class=typeGood>b</span></code>",
		},
		{
			name:      "letters are compared case-sensitively",
			expected:  "Abc",
			typed:     "abc",
			combining: true,
			want:      "<code id=typeans><span class=typeBad>a</span><span class=typeGood>bc</span><br><span id=typearrow>&darr;</span><br><span class=typeMissed>A</span><span class=typeGood>bc</span></code>",
		},
		{
			name:      "korean characters are compared as characters",
			expected:  "쓰다듬다",
			typed:     "스다뜸다",
			combining: true,
			want:      "<code id=typeans><span class=typeBad>스</span><span class=typeGood>다</span><span class=typeBad>뜸</span><span class=typeGood>다</span><br><span id=typearrow>&darr;</span><br><span class=typeMissed>쓰</span><span class=typeGood>다</span><span class=typeMissed>듬</span><span class=typeGood>다</span></code>",
		},
		{
			name:      "an emoji is one character on each line",
			expected:  "a🌏b",
			typed:     "a🌎b",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>a</span><span class=typeBad>🌎</span><span class=typeGood>b</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>a</span><span class=typeMissed>🌏</span><span class=typeGood>b</span></code>",
		},
		{
			name:      "the non-combining variant compares emoji the same way",
			expected:  "a🌏b",
			typed:     "a🌎b",
			combining: false,
			want:      "<code id=typeans><span class=typeGood>a</span><span class=typeBad>🌎</span><span class=typeGood>b</span><br><span id=typearrow>&darr;</span><br><span class=typeGood>a</span><span class=typeMissed>🌏</span><span class=typeGood>b</span></code>",
		},
		{
			name:      "the non-combining variant matches a Japanese kana written without its mark",
			expected:  "ば",
			typed:     "は",
			combining: false,
			want:      "<code id=typeans><span class=typeGood>ば</span></code>",
		},
		{
			name:      "the non-combining variant matches Hebrew written without vowel points",
			expected:  "\u05e9\u05c1\u05b4\u05e0\u05bc\u05d5\u05bc\u05df",
			typed:     "\u05e9\u05e0\u05d5\u05df",
			combining: false,
			want:      "<code id=typeans><span class=typeGood>\u05e9\u05c1\u05b4\u05e0\u05bc\u05d5\u05bc\u05df</span></code>",
		},
		{
			name:      "the type-in marker is part of the expected text, because Anki does not strip it",
			expected:  "x [[type:Back]]",
			typed:     "x [[type:Back]]",
			combining: true,
			want:      "<code id=typeans><span class=typeGood>x [[type:Back]]</span></code>",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CompareAnswer(test.expected, test.typed, test.combining); got != test.want {
				t.Errorf("output:\n got %q\nwant %q", got, test.want)
			}
		})
	}
}

// TestExtractClozeForTypingExported pins the exported extraction wrapper against
// the fixture's field and a number that occurs nowhere.
func TestExtractClozeForTypingExported(t *testing.T) {
	const field = "one {{c1::two}} three {{c2::four::a hint}}"
	tests := []struct {
		clozeNumber int
		want        string
	}{
		{1, "two"},
		{2, "four"},
		{3, ""},
	}
	for _, test := range tests {
		if got := ExtractClozeForTyping(field, test.clozeNumber); got != test.want {
			t.Errorf("cloze number %d: got %q, want %q", test.clozeNumber, got, test.want)
		}
	}
}

// TestCompareAnswerLongExpectedUsesAnkiCandidateFilter pins the one place where
// Anki's matcher differs from Python's difflib: once the expected text reaches
// 200 characters, Anki keeps only characters that occur more than len/100+1
// times as match candidates. An answer of 250 distinct characters therefore
// produces one bad run and one missed run, with no good run at all. A
// Python-compatible matcher would find the 249-character run the two answers
// share; this test fails if such a matcher is substituted.
func TestCompareAnswerLongExpectedUsesAnkiCandidateFilter(t *testing.T) {
	var expected strings.Builder
	for offset := 0; offset < 250; offset++ {
		expected.WriteRune(rune(0x4e00 + offset))
	}
	expectedText := expected.String()
	_, firstSize := utf8.DecodeRuneInString(expectedText)
	typedText := expectedText[firstSize:]

	want := "<code id=typeans><span class=typeBad>" + typedText + "</span>" +
		"<br><span id=typearrow>&darr;</span><br>" +
		"<span class=typeMissed>" + expectedText + "</span></code>"
	if got := CompareAnswer(expectedText, typedText, true); got != want {
		t.Errorf("output:\n got %q\nwant %q", got, want)
	}
}
