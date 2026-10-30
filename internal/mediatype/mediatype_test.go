package mediatype

import "testing"

// TestExtMatchesDetect pins Ext as the only mime-to-extension table: every type Detect recognises must name the extension
// Ext gives for its mime, otherwise an upload and a package import of the same bytes would land on different paths.
func TestExtMatchesDetect(t *testing.T) {
	cases := []struct {
		name    string
		head    []byte
		mime    string
		wantExt string
	}{
		{"png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "image/png", "png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg", "jpg"},
		{"gif", []byte("GIF89a"), "image/gif", "gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBP"), "image/webp", "webp"},
		{"ogg", []byte("OggS"), "audio/ogg", "ogg"},
		{"mp3 id3", []byte("ID3"), "audio/mpeg", "mp3"},
		{"mp3 frame sync", []byte{0xFF, 0xFB}, "audio/mpeg", "mp3"},
		{"m4a", []byte("\x00\x00\x00\x18ftypM4A \x00\x00\x00\x00"), "audio/mp4", "m4a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mime, ext, ok := Detect(tc.head)
			if !ok || mime != tc.mime || ext != tc.wantExt {
				t.Fatalf("Detect = (%q, %q, %v), want (%q, %q, true)", mime, ext, ok, tc.mime, tc.wantExt)
			}
			if got := Ext(tc.mime); got != tc.wantExt {
				t.Fatalf("Ext(%q) = %q, want %q", tc.mime, got, tc.wantExt)
			}
		})
	}
}

// TestExtNormalisesAliasesAndFallsBack covers mime aliases and unknown types.
func TestExtNormalisesAliasesAndFallsBack(t *testing.T) {
	cases := map[string]string{
		"image/jpg":                "jpg",
		"audio/x-m4a":              "m4a",
		"IMAGE/PNG; charset=utf-8": "png",
		"text/html":                "bin",
		"":                         "bin",
	}
	for mime, want := range cases {
		if got := Ext(mime); got != want {
			t.Errorf("Ext(%q) = %q, want %q", mime, got, want)
		}
	}
	if _, _, ok := Detect([]byte("<html>")); ok {
		t.Errorf("Detect accepted HTML bytes")
	}
}
