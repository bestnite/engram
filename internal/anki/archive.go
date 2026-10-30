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

// maxEntryBytes is the per-entry decompressed cap. It guards a single allocation while one
// entry is streamed, so it is a constant rather than a deployment setting.
const maxEntryBytes = 512 << 20

// Limits are the caps applied while reading one package. MaxEntries and MaxTotalBytes are
// deployment settings; MaxFileBytes is the per-entry allocation guard and production always
// leaves it at the DefaultLimits value.
type Limits struct {
	MaxEntries    int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// DefaultLimits returns the caps of the Anki reader.
func DefaultLimits() Limits {
	return Limits{MaxEntries: 100_000, MaxFileBytes: maxEntryBytes, MaxTotalBytes: 4 << 30}
}

// ReadWithLimits reads a package under explicit caps. No archive entry is held in memory:
// the collection is streamed into a temporary file and every media entry is exposed as an
// opener over its zip entry.
func ReadWithLimits(r io.ReaderAt, size int64, limits Limits) (*Package, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadArchive, err)
	}
	entries, err := indexEntries(zr.File, limits)
	if err != nil {
		return nil, err
	}
	total := int64(0)
	format, collectionName, err := detectFormat(entries, limits, &total)
	if err != nil {
		return nil, err
	}
	collectionPath, err := extractCollection(format, collectionName, entries, limits, &total)
	if err != nil {
		return nil, err
	}
	defer os.Remove(collectionPath)

	db, cleanup, err := openCollection(collectionPath)
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
	pkg.UnpackedBytes = total
	return pkg, nil
}

// indexEntries validates every entry name and cap without decompressing anything. The
// declared per-entry and total sizes reject a zip bomb before its payload is read.
func indexEntries(files []*zip.File, limits Limits) (map[string]*zip.File, error) {
	if len(files) > limits.MaxEntries {
		return nil, fmt.Errorf("%w: archive has %d entries, limit is %d", ErrTooLarge, len(files), limits.MaxEntries)
	}
	entries := make(map[string]*zip.File, len(files))
	var declared uint64
	for _, f := range files {
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
		declared += f.UncompressedSize64
		if declared > uint64(limits.MaxTotalBytes) {
			return nil, fmt.Errorf("%w: archive is over the total cap", ErrTooLarge)
		}
		entries[f.Name] = f
	}
	return entries, nil
}

// readZipEntryBytes reads one entry into memory, refusing to allocate past max.
func readZipEntryBytes(f *zip.File, max int64) ([]byte, error) {
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
func detectFormat(entries map[string]*zip.File, limits Limits, total *int64) (Format, string, error) {
	f, hasMeta := entries["meta"]
	if hasMeta {
		meta, err := readZipEntryBytes(f, limits.MaxFileBytes)
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return 0, "", fmt.Errorf("%w: entry %q decompresses over the per-file cap", ErrTooLarge, "meta")
			}
			return 0, "", fmt.Errorf("%w: %v", ErrCorruptMeta, err)
		}
		if err := accountTotal(total, len(meta), limits); err != nil {
			return 0, "", err
		}
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
func legacyFormat(entries map[string]*zip.File, version uint64) (Format, string, error) {
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

// extractCollection streams the SQLite payload into a temporary file and returns its path.
// The modern layout stores the payload as a zstd frame; the legacy layouts store raw SQLite
// bytes. The caller removes the file.
func extractCollection(format Format, name string, entries map[string]*zip.File, limits Limits, total *int64) (string, error) {
	f, ok := entries[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrNoCollection, name)
	}
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("%w: entry %q: %v", ErrBadArchive, name, err)
	}
	defer rc.Close()

	file, err := os.CreateTemp("", "engram-anki-*.sqlite")
	if err != nil {
		return "", fmt.Errorf("%w: create temporary file: %v", ErrCorruptCollection, err)
	}
	path := file.Name()
	fail := func(cause error) (string, error) {
		file.Close()
		os.Remove(path)
		return "", cause
	}

	var src io.Reader = rc
	if format == FormatModern {
		dec, err := zstd.NewReader(rc, zstd.WithDecoderMaxMemory(uint64(windowBytes(limits))))
		if err != nil {
			return fail(fmt.Errorf("%w: entry %q: %v", ErrCorruptZstd, name, err))
		}
		defer dec.Close()
		src = dec
	}
	n, err := io.Copy(file, io.LimitReader(src, limits.MaxFileBytes+1))
	if err != nil {
		kind := ErrCorruptCollection
		if format == FormatModern {
			kind = ErrCorruptZstd
		}
		return fail(fmt.Errorf("%w: entry %q: %v", kind, name, err))
	}
	if n > limits.MaxFileBytes {
		return fail(fmt.Errorf("%w: entry %q decompresses over the per-file cap", ErrTooLarge, name))
	}
	if err := accountTotal(total, int(n), limits); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("%w: close temporary file: %v", ErrCorruptCollection, err)
	}
	return path, nil
}

// windowBytes bounds the zstd decoder window. A hostile frame header cannot reserve more
// memory than a payload of the per-entry cap could legitimately need.
func windowBytes(limits Limits) int64 {
	window := limits.MaxFileBytes
	if window < 8<<20 {
		window = 8 << 20
	}
	return window
}

// inflateZstd decompresses one in-memory zstd payload under the per-file cap.
func inflateZstd(name string, data []byte, limits Limits, total *int64) ([]byte, error) {
	dec, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderMaxMemory(uint64(windowBytes(limits))))
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

// readMedia decodes the media table and exposes the numbered media payloads. The two
// layouts differ in both: legacy packages map a JSON index to a file name and store raw
// bytes, while modern packages map an index to a protobuf record and store a zstd frame per
// file. The payloads are not read here; each entry carries an opener.
func readMedia(format Format, entries map[string]*zip.File, limits Limits, total *int64) ([]MediaEntry, error) {
	f, ok := entries["media"]
	if !ok {
		return nil, nil
	}
	raw, err := readZipEntryBytes(f, limits.MaxFileBytes)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			return nil, fmt.Errorf("%w: entry %q decompresses over the per-file cap", ErrTooLarge, "media")
		}
		return nil, fmt.Errorf("%w: media table: %v", ErrCorruptMedia, err)
	}
	if format == FormatModern {
		return readModernMedia(raw, entries, limits, total)
	}
	if err := accountTotal(total, len(raw), limits); err != nil {
		return nil, err
	}
	return readLegacyMedia(raw, entries, limits)
}

func readLegacyMedia(raw []byte, entries map[string]*zip.File, limits Limits) ([]MediaEntry, error) {
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
		f, ok := entries[key]
		if !ok {
			return nil, fmt.Errorf("%w: media entry %q for %q is missing", ErrCorruptMedia, key, name)
		}
		if f.UncompressedSize64 > uint64(limits.MaxFileBytes) {
			return nil, fmt.Errorf("%w: media entry %q is over the per-file cap", ErrTooLarge, key)
		}
		media = append(media, MediaEntry{
			Name: name,
			Size: uint32(f.UncompressedSize64),
			Open: rawOpener(f),
		})
	}
	return media, nil
}

func readModernMedia(raw []byte, entries map[string]*zip.File, limits Limits, total *int64) ([]MediaEntry, error) {
	// The modern layout stores the media table itself as a zstd frame, like
	// the collection; only the numbered per-file payloads have their own
	// frames. A table read as protobuf directly happens to parse for the
	// all-zero stock frame, which hid the missing decompression.
	table, err := inflateZstd("media", raw, limits, total)
	if err != nil {
		return nil, err
	}
	metas, err := decodeMediaEntries(table)
	if err != nil {
		return nil, err
	}
	media := make([]MediaEntry, 0, len(metas))
	for i, meta := range metas {
		if !safeEntryName(meta.Name) {
			return nil, fmt.Errorf("%w: media name %q", ErrUnsafeEntry, meta.Name)
		}
		key := strconv.Itoa(i)
		f, ok := entries[key]
		if !ok {
			return nil, fmt.Errorf("%w: media entry %q for %q is missing", ErrCorruptMedia, key, meta.Name)
		}
		if len(meta.SHA1) != 0 && len(meta.SHA1) != sha1.Size {
			return nil, fmt.Errorf("%w: media entry %q has a malformed sha1", ErrCorruptMedia, meta.Name)
		}
		media = append(media, MediaEntry{
			Name: meta.Name,
			Size: meta.Size,
			SHA1: meta.SHA1,
			Open: zstdOpener(key, f, limits),
		})
	}
	return media, nil
}

// rawOpener exposes a zip entry's stored stream unchanged.
func rawOpener(f *zip.File) func() (io.ReadCloser, error) {
	return f.Open
}

// zstdOpener exposes a numbered media payload as a decompressed stream.
func zstdOpener(name string, f *zip.File, limits Limits) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		dec, err := zstd.NewReader(rc, zstd.WithDecoderMaxMemory(uint64(windowBytes(limits))))
		if err != nil {
			rc.Close()
			return nil, fmt.Errorf("%w: entry %q: %v", ErrCorruptZstd, name, err)
		}
		return &zstdStream{dec: dec, raw: rc}, nil
	}
}

// zstdStream couples the zstd decoder with the zip entry stream it reads.
type zstdStream struct {
	dec *zstd.Decoder
	raw io.ReadCloser
}

func (z *zstdStream) Read(p []byte) (int, error) {
	n, err := z.dec.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("%w: %v", ErrCorruptZstd, err)
	}
	return n, err
}

func (z *zstdStream) Close() error {
	z.dec.Close()
	return z.raw.Close()
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
