package notetype

import "testing"

// TestRenderLatex pins Anki's three LaTeX markers, the newline handling that
// happens before the TeX source is hashed, and the image reference Anki writes.
// The expected file names are the sha1 of the stripped TeX source, which is what
// makes the name stable across renders.
func TestRenderLatex(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		latexSVG bool
		want     string
	}{
		{
			name:  "standard latex turns block boundaries into newlines",
			input: "a[latex]one<br>and<div>two[/latex]b",
			want:  `a<img class=latex alt="one&#x0A;and&#x0A;two" src="latex-ef30b3f4141c33a5bf7044b0d1961d3399c05d50.png">b`,
		},
		{
			name:     "inline math is wrapped in dollars and uses the svg extension",
			input:    "[$]<b>hello</b>&nbsp; world[/$]",
			latexSVG: true,
			want:     `<img class=latex alt="&#x24;hello&#x20;&#x20;world&#x24;" src="latex-060219fbf3ddb74306abddaf4504276ad793b029.svg">`,
		},
		{
			name:  "display math is wrapped in a displaymath environment",
			input: "[$$]math &amp; stuff[/$$]",
			want:  `<img class=latex alt="&#x5C;begin&#x7B;displaymath&#x7D;math&#x20;&amp;&#x20;stuff&#x5C;end&#x7B;displaymath&#x7D;" src="latex-8899f3f849ffdef6e4e9f2f34a923a1f608ebc07.png">`,
		},
		{
			name:  "no markers leaves the html alone",
			input: "plain <b>html</b>",
			want:  "plain <b>html</b>",
		},
		{
			name:  "an empty pair is not an expression",
			input: "[latex][/latex]",
			want:  "[latex][/latex]",
		},
		{
			name:  "the keyword is case insensitive",
			input: "[LATEX]x^2[/LATEX]",
			want:  `<img class=latex alt="x&#x5E;2" src="latex-7046d961a8144b7b2c2da6066849a9f889ff2ac9.png">`,
		},
		{
			name:  "a newline in the source is kept",
			input: "[latex]a\nb[/latex]",
			want:  `<img class=latex alt="a&#x0A;b" src="latex-fcd127ffa1016069006ad91f3f361248f9bdf272.png">`,
		},
		{
			name:  "an uppercase div is a newline too",
			input: "[latex]a<DIV>b[/latex]",
			want:  `<img class=latex alt="a&#x0A;b" src="latex-fcd127ffa1016069006ad91f3f361248f9bdf272.png">`,
		},
		{
			name:  "every expression on the line is replaced",
			input: "[$]a[/$] [$]b[/$]",
			want: `<img class=latex alt="&#x24;a&#x24;" src="latex-c7d457e388298246adb06c587bccd419ea67f7e8.png"> ` +
				`<img class=latex alt="&#x24;b&#x24;" src="latex-8136a7ef6a03334a7246df9097e5bcc31ba33fd2.png">`,
		},
		{
			name:  "the display pair is not read as two inline pairs",
			input: "[$$]x[/$$]",
			want:  `<img class=latex alt="&#x5C;begin&#x7B;displaymath&#x7D;x&#x5C;end&#x7B;displaymath&#x7D;" src="latex-291ba45a7c0f5521a78ef3a6e04f25000a3a3f5c.png">`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RenderLatex(test.input, test.latexSVG); got != test.want {
				t.Errorf("html:\n got %q\nwant %q", got, test.want)
			}
		})
	}
}
