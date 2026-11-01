package store

import (
	"strings"
	"testing"
)

// TestImportRenamesClashingMedia is the Anki-fidelity acceptance for a name clash: the target deck already has img.png
// pointing at different bytes, so the new bytes must be registered under img-<sha1>.png and every reference in the
// package (note fields and note-type template/CSS) must be rewritten to the new name. The old name keeps its old content.
func TestAddHashSuffixToFileStem(t *testing.T) {
	// Anki's own test vector: sha1("hello").
	const hello = "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
	if got, want := addHashSuffixToFileStem("test.jpg", hello), "test-"+hello+".jpg"; got != want {
		t.Fatalf("addHashSuffixToFileStem = %q, want %q", got, want)
	}
	// A very long stem is truncated so the whole name stays within 120 bytes, extension capped at 10 bytes.
	long := strings.Repeat("x", 200) + ".verylongext"
	got := addHashSuffixToFileStem(long, hello)
	if len(got) > maxMediaFilenameLength {
		t.Fatalf("renamed name is %d bytes, want at most %d", len(got), maxMediaFilenameLength)
	}
	if !strings.HasSuffix(got, "-"+hello+".verylongex") {
		t.Fatalf("renamed name = %q, want the hash suffix and a 10-byte extension", got)
	}
}

// TestRewriteMediaRefsRewritesOnlyTheReference pins that a clash rename lands inside the reference value. A short
// name also occurs inside the attribute name (src, href) or the CSS function (url); replacing the first occurrence
// in the whole match corrupts the markup instead of the reference.
func TestRewriteMediaRefsRewritesOnlyTheReference(t *testing.T) {
	renames := map[string]string{
		"s":       "s-abc.png",
		"h":       "h-abc.png",
		"u":       "u-abc.png",
		"src":     "src-abc.png",
		"href":    "href-abc.png",
		"url":     "url-abc.png",
		"img.png": "img-abc.png",
	}
	cases := []struct{ name, in, want string }{
		{"double quoted src", `<img src="s">`, `<img src="s-abc.png">`},
		{"single quoted src", `<img src='s'>`, `<img src='s-abc.png'>`},
		{"unquoted src", `<img src=s>`, `<img src=s-abc.png>`},
		{"href", `<a href="h">s</a>`, `<a href="h-abc.png">s</a>`},
		{"css double quoted url", `<div style="background:url(u)"></div>`, `<div style="background:url(u-abc.png)"></div>`},
		{"css single quoted url", `<div style="background:url('u')"></div>`, `<div style="background:url('u-abc.png')"></div>`},
		{"css quoted url", `<div style="background:url("u")"></div>`, `<div style="background:url("u-abc.png")"></div>`},
		{"sound", `[sound:s]`, `[sound:s-abc.png]`},
		{"src name equals the attribute", `<img src="src">`, `<img src="src-abc.png">`},
		{"href name equals the attribute", `<a href="href">x</a>`, `<a href="href-abc.png">x</a>`},
		{"css name equals the function", `<div style="background:url(url)"></div>`, `<div style="background:url(url-abc.png)"></div>`},
		{"only the reference is rewritten", `<img src="img.png" alt="img.png">`, `<img src="img-abc.png" alt="img.png">`},
		{"ordinary text is untouched", `<p>s</p>`, `<p>s</p>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rewriteMediaRefs(tc.in, renames); got != tc.want {
				t.Fatalf("rewriteMediaRefs(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
