package mediatype

import "testing"

// TestClassifyEveryClass pins the classification and the mime-to-extension table: bytes decide the type, and the
// extension table must agree with the mime Classify returns so an upload and a package import land on one path.
func TestClassifyEveryClass(t *testing.T) {
	svgWithBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte("  \n<svg xmlns=\"http://example.com\"><script>alert(1)</script></svg>")...)
	avif := append([]byte("\x00\x00\x00\x20ftypavif"), []byte("\x00\x00\x00\x00avif")...)
	opus := append([]byte("OggS"), []byte("\x00\x02OpusHead\x01\x02")...)
	webm := append([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte("\x9f\x42\x86\x81\x01\x42\x82\x84webm")...)
	cases := []struct {
		name  string
		head  []byte
		fname string
		mime  string
		class Class
		ext   string
	}{
		{"png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "a.png", "image/png", ClassImage, "png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0}, "a.jpg", "image/jpeg", ClassImage, "jpg"},
		{"gif", []byte("GIF89a"), "a.gif", "image/gif", ClassImage, "gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBP"), "a.webp", "image/webp", ClassImage, "webp"},
		{"svg with BOM and whitespace", svgWithBOM, "a.svg", "image/svg+xml", ClassImage, "svg"},
		{"avif", avif, "a.avif", "image/avif", ClassImage, "avif"},
		{"mp3 id3", []byte("ID3"), "a.mp3", "audio/mpeg", ClassAudio, "mp3"},
		{"mp3 frame sync", []byte{0xFF, 0xFB}, "a.mp3", "audio/mpeg", ClassAudio, "mp3"},
		{"ogg", []byte("OggS\x00\x02vorbis"), "a.ogg", "audio/ogg", ClassAudio, "ogg"},
		{"opus", opus, "a.opus", "audio/opus", ClassAudio, "opus"},
		{"m4a", []byte("\x00\x00\x00\x18ftypM4A \x00\x00\x00\x00"), "a.m4a", "audio/mp4", ClassAudio, "m4a"},
		{"wav", []byte("RIFF\x00\x00\x00\x00WAVEfmt "), "a.wav", "audio/wav", ClassAudio, "wav"},
		{"flac", []byte("fLaC\x00\x00\x00\x22"), "a.flac", "audio/flac", ClassAudio, "flac"},
		{"mp4", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x00\x00"), "a.mp4", "video/mp4", ClassVideo, "mp4"},
		{"webm", webm, "a.webm", "video/webm", ClassVideo, "webm"},
		{"woff", []byte("wOFF\x00\x01\x00\x00"), "a.woff", "font/woff", ClassFont, "woff"},
		{"woff2", []byte("wOF2\x00\x01\x00\x00"), "a.woff2", "font/woff2", ClassFont, "woff2"},
		{"ttf", []byte("\x00\x01\x00\x00\x00"), "a.ttf", "font/ttf", ClassFont, "ttf"},
		{"otf", []byte("OTTO\x00\x00"), "a.otf", "font/otf", ClassFont, "otf"},
		{"js", []byte("function f() {}"), "tool.js", "text/javascript", ClassScript, "js"},
		{"mjs", []byte("export default 1"), "tool.mjs", "text/javascript", ClassScript, "js"},
		{"css", []byte("body { color: red }"), "style.css", "text/css", ClassStyle, "css"},
		{"unknown", []byte("plain text"), "notes.bin", "application/octet-stream", ClassOther, "bin"},
		// The name never overrides recognised bytes: a .js name on a PNG still yields the image class.
		{"name does not override bytes", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "fake.js", "image/png", ClassImage, "png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mime, class := Classify(tc.head, tc.fname)
			if mime != tc.mime || class != tc.class {
				t.Fatalf("Classify = (%q, %q), want (%q, %q)", mime, class, tc.mime, tc.class)
			}
			if got := Ext(tc.mime); got != tc.ext {
				t.Fatalf("Ext(%q) = %q, want %q", tc.mime, got, tc.ext)
			}
			if class.IsInline() != (tc.class == ClassImage || tc.class == ClassAudio || tc.class == ClassVideo) {
				t.Fatalf("IsInline(%q) disagrees with the class", class)
			}
		})
	}
}

// TestClassOfMime covers the read-side mapping, including a stored type Classify no longer produces.
func TestClassOfMime(t *testing.T) {
	cases := map[string]Class{
		"image/svg+xml":   ClassImage,
		"audio/opus":      ClassAudio,
		"video/webm":      ClassVideo,
		"font/woff2":      ClassFont,
		"text/javascript": ClassScript,
		"text/css":        ClassStyle,
		"text/html":       ClassOther,
		"":                ClassOther,
	}
	for mime, want := range cases {
		if got := ClassOfMime(mime); got != want {
			t.Errorf("ClassOfMime(%q) = %q, want %q", mime, got, want)
		}
	}
}

// TestExtNormalisesAliasesAndFallsBack covers mime aliases and unknown types.
func TestExtNormalisesAliasesAndFallsBack(t *testing.T) {
	cases := map[string]string{
		"image/jpg":                "jpg",
		"audio/x-m4a":              "m4a",
		"audio/x-wav":              "wav",
		"application/javascript":   "js",
		"IMAGE/PNG; charset=utf-8": "png",
		"text/html":                "bin",
		"":                         "bin",
	}
	for mime, want := range cases {
		if got := Ext(mime); got != want {
			t.Errorf("Ext(%q) = %q, want %q", mime, got, want)
		}
	}
}
