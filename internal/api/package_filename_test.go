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
		{"plain ascii", "Biology", "Biology.apkg"},
		{"chinese title", "日语 N2 词汇", "日语 N2 词汇.apkg"},
		{"path separators and reserved characters", `a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j.apkg"},
		{"whitespace collapses", "  Linear \t\n  Algebra  ", "Linear Algebra.apkg"},
		{"trailing dots trimmed", "Notes...", "Notes.apkg"},
		{"control characters replaced", "a\x00b\x7fc", "a_b_c.apkg"},
		{"empty falls back to public id", "", "deck-abc123.apkg"},
		{"only dots falls back to public id", " ... ", "deck-abc123.apkg"},
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
	if n := len([]rune(strings.TrimSuffix(got, ".apkg"))); n != packageFilenameMaxRunes {
		t.Errorf("name body has %d runes, want %d", n, packageFilenameMaxRunes)
	}
}

// TestAttachmentDispositionRoundTrips asserts an encoded non-ASCII filename can be restored by a standard parser.
func TestAttachmentDispositionRoundTrips(t *testing.T) {
	for _, name := range []string{"Biology.apkg", "日语 N2 词汇.apkg", `quote".apkg`} {
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
