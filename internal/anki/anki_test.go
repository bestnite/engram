package anki_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/klauspost/compress/zstd"

	"git.nite07.com/nite/engram/internal/anki"
)

// The three stock fixtures are exports of one collection: note type, field,
// template, note, card and deck IDs are identical in all three, so the
// comparison below requires the decoded models to be exactly equal (apart
// from Format, which names the layout).
var stockFixtures = []struct {
	file   string
	format anki.Format
}{
	{"stock.anki2.apkg", anki.FormatLegacy1},
	{"stock.anki21.apkg", anki.FormatLegacy2},
	{"stock.anki21b.apkg", anki.FormatModern},
}

func fixturePath(name string) string {
	return filepath.Join("..", "notetype", "testdata", "anki", name)
}

func TestFixturesDecodeToSameModel(t *testing.T) {
	var want *anki.Package
	for _, fixture := range stockFixtures {
		pkg, err := anki.ReadFile(fixturePath(fixture.file))
		if err != nil {
			t.Fatalf("%s: %v", fixture.file, err)
		}
		if pkg.Format != fixture.format {
			t.Fatalf("%s: format = %s, want %s", fixture.file, pkg.Format, fixture.format)
		}
		if got := len(pkg.NoteTypes); got != 6 {
			t.Fatalf("%s: %d note types, want 6", fixture.file, got)
		}
		if got := len(pkg.Notes); got != 8 {
			t.Fatalf("%s: %d notes, want 8", fixture.file, got)
		}
		if got := len(pkg.Cards); got == 0 {
			t.Fatalf("%s: no cards", fixture.file)
		}
		if got := len(pkg.Decks); got != 1 || pkg.Decks[0].Name != "Default" {
			t.Fatalf("%s: decks = %+v, want one Default deck", fixture.file, pkg.Decks)
		}
		if got := len(pkg.Media); got != 1 || pkg.Media[0].Name != "placeholder.png" {
			t.Fatalf("%s: media = %+v, want placeholder.png", fixture.file, pkg.Media)
		}
		if want == nil {
			want = pkg
			continue
		}
		assertSameModel(t, fixture.file, want, pkg)
	}
}

// assertSameModel compares every model part separately so a failure names the
// part instead of printing the whole package.
func assertSameModel(t *testing.T, name string, want, got *anki.Package) {
	t.Helper()
	// Media entries carry an opener, which reflect cannot compare, and the legacy layout
	// advertises no sha1. Compare the media model by name and declared size instead.
	wantCopy := *want
	wantCopy.Format = 0
	wantCopy.Media = nil
	wantCopy.UnpackedBytes = 0
	gotCopy := *got
	gotCopy.Format = 0
	gotCopy.Media = nil
	gotCopy.UnpackedBytes = 0
	if len(want.Media) != len(got.Media) {
		t.Fatalf("%s: media entries = %d, want %d", name, len(got.Media), len(want.Media))
	}
	for i := range want.Media {
		if want.Media[i].Name != got.Media[i].Name || want.Media[i].Size != got.Media[i].Size {
			t.Fatalf("%s: media %d = {%q %d}, want {%q %d}", name, i,
				got.Media[i].Name, got.Media[i].Size, want.Media[i].Name, want.Media[i].Size)
		}
	}
	if reflect.DeepEqual(wantCopy, gotCopy) {
		return
	}
	wantJSON, _ := json.MarshalIndent(wantCopy, "", "  ")
	gotJSON, _ := json.MarshalIndent(gotCopy, "", "  ")
	t.Fatalf("%s does not decode to the same model as %s\nwant: %s\ngot:  %s", name, stockFixtures[0].file, wantJSON, gotJSON)
}

func TestModernMediaMatchesMetaAndEntry(t *testing.T) {
	pkg, err := anki.ReadFile(fixturePath("stock.anki21b.apkg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Media) != 1 {
		t.Fatalf("media = %d entries, want 1", len(pkg.Media))
	}
	entry := pkg.Media[0]
	if entry.Name != "placeholder.png" {
		t.Fatalf("media name = %q", entry.Name)
	}
	data := readMediaStream(t, entry)
	if entry.Size != uint32(len(data)) {
		t.Fatalf("media size = %d, stream length = %d", entry.Size, len(data))
	}
	if !bytes.Equal(entry.SHA1, sha1Sum(data)) {
		t.Fatalf("media sha1 does not match its data")
	}

	// The media protobuf names the file; the numbered zip entry holds its
	// zstd frame. Resolving the name must pick entry "0".
	raw, err := os.ReadFile(fixturePath("stock.anki21b.apkg"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var compressed []byte
	for _, f := range zr.File {
		if f.Name != "0" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		compressed, err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if compressed == nil {
		t.Fatal("archive has no entry 0")
	}
	dec, err := zstd.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	data, err = io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, readMediaStream(t, entry)) {
		t.Fatalf("media %q resolved to the wrong entry bytes", entry.Name)
	}
}

// readMediaStream reads one entry's decompressed stream through its opener.
func readMediaStream(t *testing.T, entry anki.MediaEntry) []byte {
	t.Helper()
	rc, err := entry.Open()
	if err != nil {
		t.Fatalf("open media %q: %v", entry.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read media %q: %v", entry.Name, err)
	}
	return data
}

func TestCorruptZstdStream(t *testing.T) {
	// A zstd magic number followed by garbage decodes as a corrupt frame.
	archive := zipBytes(t, map[string][]byte{
		"meta":               {0x08, 0x03},
		"collection.anki21b": {0x28, 0xb5, 0x2f, 0xfd, 0x00, 0x11, 0x22},
	})
	_, err := anki.Read(bytes.NewReader(archive), int64(len(archive)))
	if !errors.Is(err, anki.ErrCorruptZstd) {
		t.Fatalf("err = %v, want ErrCorruptZstd", err)
	}
}

func TestMissingCollection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries map[string][]byte
	}{
		{"modern without collection.anki21b", map[string][]byte{
			"meta":  {0x08, 0x03},
			"media": {0x0a, 0x00},
		}},
		{"legacy without collection", map[string][]byte{
			"media": []byte(`{}`),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := zipBytes(t, tc.entries)
			_, err := anki.Read(bytes.NewReader(archive), int64(len(archive)))
			if !errors.Is(err, anki.ErrNoCollection) {
				t.Fatalf("err = %v, want ErrNoCollection", err)
			}
		})
	}
}

func TestUnsafeEntryPath(t *testing.T) {
	archive := zipBytes(t, map[string][]byte{
		"../evil":          []byte("x"),
		"collection.anki2": []byte("not opened"),
	})
	_, err := anki.Read(bytes.NewReader(archive), int64(len(archive)))
	if !errors.Is(err, anki.ErrUnsafeEntry) {
		t.Fatalf("err = %v, want ErrUnsafeEntry", err)
	}
}

func TestUnsafeMediaName(t *testing.T) {
	archive := rewriteFixture(t, "stock.anki2.apkg", map[string][]byte{
		"media": []byte(`{"0":"../evil.png"}`),
	})
	_, err := anki.Read(bytes.NewReader(archive), int64(len(archive)))
	if !errors.Is(err, anki.ErrUnsafeEntry) {
		t.Fatalf("err = %v, want ErrUnsafeEntry", err)
	}
}

func TestCorruptMeta(t *testing.T) {
	archive := zipBytes(t, map[string][]byte{
		"meta":             {0xff},
		"collection.anki2": []byte("not opened"),
	})
	_, err := anki.Read(bytes.NewReader(archive), int64(len(archive)))
	if !errors.Is(err, anki.ErrCorruptMeta) {
		t.Fatalf("err = %v, want ErrCorruptMeta", err)
	}
}

func TestUnknownMetaVersion(t *testing.T) {
	archive := zipBytes(t, map[string][]byte{
		"meta":             {0x08, 0x04},
		"collection.anki2": []byte("not opened"),
	})
	_, err := anki.Read(bytes.NewReader(archive), int64(len(archive)))
	if !errors.Is(err, anki.ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
}

func TestPerFileLimit(t *testing.T) {
	archive := zipBytes(t, map[string][]byte{
		"collection.anki2": bytes.Repeat([]byte("x"), 64),
	})
	limits := anki.Limits{MaxEntries: 10, MaxFileBytes: 16, MaxTotalBytes: 1024}
	_, err := anki.ReadWithLimits(bytes.NewReader(archive), int64(len(archive)), limits)
	if !errors.Is(err, anki.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestTotalLimit(t *testing.T) {
	archive := zipBytes(t, map[string][]byte{
		"collection.anki2": bytes.Repeat([]byte("x"), 10),
		"media":            bytes.Repeat([]byte("y"), 10),
	})
	limits := anki.Limits{MaxEntries: 10, MaxFileBytes: 100, MaxTotalBytes: 15}
	_, err := anki.ReadWithLimits(bytes.NewReader(archive), int64(len(archive)), limits)
	if !errors.Is(err, anki.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestDecompressedTotalLimit(t *testing.T) {
	// The compressed collection fits under the total cap; its decompressed
	// size does not, so the cap must apply after decompression.
	archive := zipBytes(t, map[string][]byte{
		"meta":               {0x08, 0x03},
		"collection.anki21b": zstdCompress(t, bytes.Repeat([]byte("x"), 64)),
	})
	limits := anki.Limits{MaxEntries: 10, MaxFileBytes: 100, MaxTotalBytes: 40}
	_, err := anki.ReadWithLimits(bytes.NewReader(archive), int64(len(archive)), limits)
	if !errors.Is(err, anki.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestEntryLimit(t *testing.T) {
	archive := zipBytes(t, map[string][]byte{
		"collection.anki2": []byte("x"),
		"media":            []byte(`{}`),
	})
	limits := anki.Limits{MaxEntries: 1, MaxFileBytes: 100, MaxTotalBytes: 100}
	_, err := anki.ReadWithLimits(bytes.NewReader(archive), int64(len(archive)), limits)
	if !errors.Is(err, anki.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestBadArchive(t *testing.T) {
	garbage := []byte("this is not a zip file")
	_, err := anki.Read(bytes.NewReader(garbage), int64(len(garbage)))
	if !errors.Is(err, anki.ErrBadArchive) {
		t.Fatalf("err = %v, want ErrBadArchive", err)
	}
}

func zipBytes(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range entries {
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		fw, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rewriteFixture rebuilds a fixture archive with some entries replaced.
func rewriteFixture(t *testing.T, name string, replacements map[string][]byte) []byte {
	t.Helper()
	raw, err := os.ReadFile(fixturePath(name))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[f.Name] = data
	}
	for key, data := range replacements {
		entries[key] = data
	}
	return zipBytes(t, entries)
}

func zstdCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha1Sum(data []byte) []byte {
	sum := sha1.Sum(data)
	return sum[:]
}
