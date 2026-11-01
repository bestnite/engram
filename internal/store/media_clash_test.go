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
