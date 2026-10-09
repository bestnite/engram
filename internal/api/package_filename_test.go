package api

import (
	"mime"
	"strings"
	"testing"
)

func TestDeckPackageFilename(t *testing.T) {
	cases := []struct {
		name     string
		deckName string
		want     string
	}{
		{"plain ascii", "Biology", "Biology.edeck"},
		{"chinese title", "日语 N2 词汇", "日语 N2 词汇.edeck"},
		{"path separators and reserved characters", `a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j.edeck"},
		{"whitespace collapses", "  Linear \t\n  Algebra  ", "Linear Algebra.edeck"},
		{"trailing dots trimmed", "Notes...", "Notes.edeck"},
		{"control characters replaced", "a\x00b\x7fc", "a_b_c.edeck"},
		{"empty falls back to public id", "", "deck-abc123.edeck"},
		{"only dots falls back to public id", " ... ", "deck-abc123.edeck"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeckPackageFilename(tc.deckName, "abc123"); got != tc.want {
				t.Errorf("DeckPackageFilename(%q) = %q, want %q", tc.deckName, got, tc.want)
			}
		})
	}
}

func TestDeckPackageFilenameCapsLength(t *testing.T) {
	got := DeckPackageFilename(strings.Repeat("词", 200), "abc123")
	if n := len([]rune(strings.TrimSuffix(got, ".edeck"))); n != packageFilenameMaxRunes {
		t.Errorf("name body has %d runes, want %d", n, packageFilenameMaxRunes)
	}
}

// TestAttachmentDispositionRoundTrips 断言非 ASCII 文件名编码后能被标准解析器还原。
func TestAttachmentDispositionRoundTrips(t *testing.T) {
	for _, name := range []string{"Biology.edeck", "日语 N2 词汇.edeck", `quote".edeck`} {
		header := AttachmentDisposition(name)
		disposition, params, err := mime.ParseMediaType(header)
		if err != nil {
			t.Fatalf("parse %q: %v", header, err)
		}
		if disposition != "attachment" || params["filename"] != name {
			t.Errorf("AttachmentDisposition(%q) = %q, parsed back as %q/%q", name, header, disposition, params["filename"])
		}
	}
}
