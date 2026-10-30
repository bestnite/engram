package anki

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Limits are the caps applied while reading one package. They mirror the deck
// package reader's caps so both archive readers defend against the same zip
// bombs; DefaultLimits is asserted against that reader by a test.
type Limits struct {
	MaxEntries    int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// DefaultLimits returns the caps shared with the deck package reader.
func DefaultLimits() Limits {
	return Limits{MaxEntries: 2000, MaxFileBytes: 32 << 20, MaxTotalBytes: 128 << 20}
}

// ReadWithLimits reads a package under explicit caps.
func ReadWithLimits(r io.ReaderAt, size int64, limits Limits) (*Package, error) {
	entries, err := readEntries(r, size, limits)
	if err != nil {
		return nil, err
	}
	format, collectionEntry, err := detectFormat(entries)
	if err != nil {
		return nil, err
	}
	// readEntries capped the zip payloads; each decompressed zstd payload is
	// charged to the same total so a many-entry zstd bomb cannot exceed the
	// cap either.
	total := int64(0)
	for _, data := range entries {
		total += int64(len(data))
	}
	collection, err := collectionBytes(format, collectionEntry, entries, limits, &total)
	if err != nil {
		return nil, err
	}
	db, cleanup, err := openCollection(collection)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	pkg := &Package{Format: format}
	switch format {
	case FormatModern:
		pkg.NoteTypes, pkg.Decks, pkg.Notes, pkg.Cards, err = readModernCollection(db)
	default:
		pkg.NoteTypes, pkg.Decks, pkg.Notes, pkg.Cards, err = readLegacyCollection(db)
	}
	if err != nil {
		return nil, err
	}
	pkg.Media, err = readMedia(format, entries, limits, &total)
	if err != nil {
		return nil, err
	}
	return pkg, nil
}

// readEntries reads every archive entry into memory under the caps. Reading
// all entries before decoding keeps one total-size accounting point and makes
// the detection step independent of the SQLite payload.
func readEntries(r io.ReaderAt, size int64, limits Limits) (map[string][]byte, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	if len(zr.File) > limits.MaxEntries {
		return nil, fmt.Errorf("%w: archive has %d entries, limit is %d", ErrTooLarge, len(zr.File), limits.MaxEntries)
	}
	entries := make(map[string][]byte, len(zr.File))
	var total int64
	for _, f := range zr.File {
		if !safeEntryName(f.Name) {
			return nil, fmt.Errorf("%w: %q", ErrUnsafeEntry, f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %q is a symlink", ErrUnsafeEntry, f.Name)
		}
		if _, dup := entries[f.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate entry %q", ErrBadArchive, f.Name)
		}
		if f.UncompressedSize64 > uint64(limits.MaxFileBytes) {
			return nil, fmt.Errorf("%w: entry %q is over the per-file cap", ErrTooLarge, f.Name)
		}
		data, err := readZipEntry(f, limits.MaxFileBytes)
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return nil, fmt.Errorf("%w: entry %q decompresses over the per-file cap", ErrTooLarge, f.Name)
			}
			return nil, fmt.Errorf("%w: entry %q: %v", ErrBadArchive, f.Name, err)
		}
		total += int64(len(data))
		if total > limits.MaxTotalBytes {
			return nil, fmt.Errorf("%w: archive is over the total cap", ErrTooLarge)
		}
		entries[f.Name] = data
	}
	return entries, nil
}

// readZipEntry reads one entry, refusing to allocate past the per-file cap.
func readZipEntry(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: decompressed over the per-file cap", ErrTooLarge)
	}
	return data, nil
}

// safeEntryName reports whether name is exactly one normal path component.
// An archive name is only ever used as a map key and never as a filesystem
// path, but a name with a separator or a traversal segment signals a package
// built to place files outside the extraction root by a reader that does use
// names as paths, so the whole archive is rejected.
func safeEntryName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	if len(name) >= 2 && name[1] == ':' && isASCIILetter(name[0]) {
		return false
	}
	return true
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// detectFormat selects the layout and the collection entry it uses.
func detectFormat(entries map[string][]byte) (Format, string, error) {
	meta, hasMeta := entries["meta"]
	if hasMeta {
		version, err := decodeMetaVersion(meta)
		if err != nil {
			return 0, "", err
		}
		switch version {
		case 3:
			if _, ok := entries["collection.anki21b"]; !ok {
				return 0, "", fmt.Errorf("%w: %q", ErrNoCollection, "collection.anki21b")
			}
			return FormatModern, "collection.anki21b", nil
		case 1, 2:
			return legacyFormat(entries, version)
		default:
			return 0, "", fmt.Errorf("%w: meta version %d", ErrUnsupportedVersion, version)
		}
	}
	return legacyFormat(entries, 0)
}

// legacyFormat picks the collection entry of a schema-11 package. A version 2
// package names its collection collection.anki21; version 1 packages and
// packages without meta use collection.anki2.
func legacyFormat(entries map[string][]byte, version uint64) (Format, string, error) {
	names := []string{"collection.anki2"}
	format := FormatLegacy1
	if version == 2 {
		names = []string{"collection.anki21", "collection.anki2"}
		format = FormatLegacy2
	} else if _, ok := entries["collection.anki21"]; ok {
		names = []string{"collection.anki21", "collection.anki2"}
		format = FormatLegacy2
	}
	for _, name := range names {
		if _, ok := entries[name]; ok {
			return format, name, nil
		}
	}
	return 0, "", fmt.Errorf("%w: neither %q nor %q", ErrNoCollection, "collection.anki2", "collection.anki21")
}

// collectionBytes returns the uncompressed SQLite payload. The modern layout
// stores it as a zstd frame; the legacy layouts store raw SQLite bytes.
func collectionBytes(format Format, name string, entries map[string][]byte, limits Limits, total *int64) ([]byte, error) {
	if format != FormatModern {
		return entries[name], nil
	}
	return inflateZstd(name, entries[name], limits, total)
}

// inflateZstd decompresses one zstd payload under the per-file cap. The
// decoder's window is bounded too, so a hostile frame header cannot reserve
// more memory than a payload of that size could legitimately need.
func inflateZstd(name string, data []byte, limits Limits, total *int64) ([]byte, error) {
	window := limits.MaxFileBytes
	if window < 8<<20 {
		window = 8 << 20
	}
	dec, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderMaxMemory(uint64(window)))
	if err != nil {
		return nil, fmt.Errorf("%w: entry %q: %v", ErrCorruptZstd, name, err)
	}
	defer dec.Close()
	out, err := io.ReadAll(io.LimitReader(dec, limits.MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: entry %q: %v", ErrCorruptZstd, name, err)
	}
	if int64(len(out)) > limits.MaxFileBytes {
		return nil, fmt.Errorf("%w: entry %q decompresses over the per-file cap", ErrTooLarge, name)
	}
	if err := accountTotal(total, len(out), limits); err != nil {
		return nil, err
	}
	return out, nil
}

// accountTotal charges n decompressed bytes to the archive total.
func accountTotal(total *int64, n int, limits Limits) error {
	*total += int64(n)
	if *total > limits.MaxTotalBytes {
		return fmt.Errorf("%w: archive is over the total cap", ErrTooLarge)
	}
	return nil
}

// readMedia decodes the media table and the numbered media payloads. The two
// layouts differ in both: legacy packages map a JSON index to a file name and
// store raw bytes, while modern packages map an index to a protobuf record
// and store a zstd frame per file.
func readMedia(format Format, entries map[string][]byte, limits Limits, total *int64) ([]MediaEntry, error) {
	raw, ok := entries["media"]
	if !ok {
		return nil, nil
	}
	if format == FormatModern {
		return readModernMedia(raw, entries, limits, total)
	}
	return readLegacyMedia(raw, entries)
}

func readLegacyMedia(raw []byte, entries map[string][]byte) ([]MediaEntry, error) {
	var index map[string]string
	if err := json.Unmarshal(raw, &index); err != nil {
		return nil, fmt.Errorf("%w: media index: %v", ErrCorruptMedia, err)
	}
	names := make([]string, 0, len(index))
	for key := range index {
		names = append(names, key)
	}
	sort.Slice(names, func(i, j int) bool { return lessNumeric(names[i], names[j]) })
	media := make([]MediaEntry, 0, len(names))
	for _, key := range names {
		name := index[key]
		if !safeEntryName(name) {
			return nil, fmt.Errorf("%w: media name %q", ErrUnsafeEntry, name)
		}
		data, ok := entries[key]
		if !ok {
			return nil, fmt.Errorf("%w: media entry %q for %q is missing", ErrCorruptMedia, key, name)
		}
		sum := sha1.Sum(data)
		media = append(media, MediaEntry{Name: name, Size: uint32(len(data)), SHA1: sum[:], Data: data})
	}
	return media, nil
}

func readModernMedia(raw []byte, entries map[string][]byte, limits Limits, total *int64) ([]MediaEntry, error) {
	metas, err := decodeMediaEntries(raw)
	if err != nil {
		return nil, err
	}
	media := make([]MediaEntry, 0, len(metas))
	for i, meta := range metas {
		if !safeEntryName(meta.Name) {
			return nil, fmt.Errorf("%w: media name %q", ErrUnsafeEntry, meta.Name)
		}
		key := strconv.Itoa(i)
		compressed, ok := entries[key]
		if !ok {
			return nil, fmt.Errorf("%w: media entry %q for %q is missing", ErrCorruptMedia, key, meta.Name)
		}
		data, err := inflateZstd(key, compressed, limits, total)
		if err != nil {
			return nil, err
		}
		if len(meta.SHA1) != 0 && len(meta.SHA1) != sha1.Size {
			return nil, fmt.Errorf("%w: media entry %q has a malformed sha1", ErrCorruptMedia, meta.Name)
		}
		if len(meta.SHA1) == sha1.Size && !bytes.Equal(meta.SHA1, sha1Of(data)) {
			return nil, fmt.Errorf("%w: media entry %q does not match its sha1", ErrCorruptMedia, meta.Name)
		}
		size := meta.Size
		if size == 0 {
			size = uint32(len(data))
		}
		if int(size) != len(data) {
			return nil, fmt.Errorf("%w: media entry %q declares %d bytes, got %d", ErrCorruptMedia, meta.Name, size, len(data))
		}
		media = append(media, MediaEntry{Name: meta.Name, Size: size, SHA1: meta.SHA1, Data: data})
	}
	return media, nil
}

func sha1Of(data []byte) []byte {
	sum := sha1.Sum(data)
	return sum[:]
}

// lessNumeric orders decimal index keys numerically, falling back to a plain
// string comparison for keys that are not numbers.
func lessNumeric(a, b string) bool {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	if aerr == nil && berr == nil {
		return ai < bi
	}
	return a < b
}
