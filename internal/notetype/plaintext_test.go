package notetype

import "testing"

// TestPlainText pins Anki's html_to_text_line behaviour case by case: one line,
// entities resolved, and only <br>, an opening <div> and a literal newline acting
// as word separators. Audio and TTS tags contribute no text.
func TestPlainText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text is unchanged",
			input: "text",
			want:  "text",
		},
		{
			name:  "an empty string stays empty",
			input: "",
			want:  "",
		},
		{
			name:  "ampersand entity",
			input: "a &amp; b",
			want:  "a & b",
		},
		{
			name:  "apostrophe hex entity",
			input: "it&#x27;s",
			want:  "it's",
		},
		{
			name:  "non-breaking space becomes a space",
			input: "a&nbsp;b",
			want:  "a b",
		},
		{
			name:  "entities are decoded once",
			input: "&amp;lt;",
			want:  "&lt;",
		},
		{
			name:  "br is one space",
			input: "line<br>break",
			want:  "line break",
		},
		{
			name:  "self-closing br is one space",
			input: "line<br />break",
			want:  "line break",
		},
		{
			name:  "opening div is one space",
			input: "<div>one</div><div>two</div>",
			want:  "one two",
		},
		{
			name:  "a paragraph is not a separator",
			input: "<p>one</p><p>two</p>",
			want:  "onetwo",
		},
		{
			name:  "a literal newline is one space",
			input: "one\ntwo",
			want:  "one two",
		},
		{
			name:  "script content is dropped",
			input: "<script>var x = 1;</script>text",
			want:  "text",
		},
		{
			name:  "style content is dropped",
			input: "<style>.card { color: red; }</style>text",
			want:  "text",
		},
		{
			name:  "comments are dropped",
			input: "a<!-- hidden -->b",
			want:  "ab",
		},
		{
			name:  "an image contributes no text",
			input: "<img src=placeholder.png>tail",
			want:  "tail",
		},
		{
			name:  "an anchor keeps its text",
			input: `<a href="https://example.com">link</a>`,
			want:  "link",
		},
		{
			name:  "a sound tag vanishes",
			input: "before [sound:placeholder.wav] after",
			want:  "before  after",
		},
		{
			name:  "a tts directive vanishes with its body",
			input: "[anki:tts lang=en_US]spoken[/anki:tts] rest",
			want:  "rest",
		},
		{
			name:  "html inside a tts body does not resurface",
			input: "a[anki:tts lang=en_US]spoken <b>text</b>[/anki:tts]b",
			want:  "ab",
		},
		{
			name:  "another directive is left in place",
			input: "a[anki:foo bar=baz]x[/anki:foo]b",
			want:  "a[anki:foo bar=baz]x[/anki:foo]b",
		},
		{
			name:  "the type-in placeholder vanishes",
			input: "answer: [[type:Front]]",
			want:  "answer:",
		},
		{
			name:  "the ends are trimmed",
			input: "  \n <div>text</div> \t ",
			want:  "text",
		},
		{
			name:  "a card with only markup is empty",
			input: "<style>.card{}</style><br><div></div>",
			want:  "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := PlainText(test.input); got != test.want {
				t.Errorf("plain text:\n got %q\nwant %q", got, test.want)
			}
		})
	}
}
