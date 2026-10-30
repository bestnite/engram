// Package anki reads Anki deck packages (.apkg) into an in-memory model.
//
// Anki has shipped three package layouts. The reader recognises the layout
// from the archive entries, never from the file suffix:
//
//   - modern: a "meta" protobuf whose version is 3,
//     a zstd-compressed "collection.anki21b" holding SQLite schema 18, note
//     types and decks in tables of their own with protobuf configuration
//     blobs, and a zstd-compressed "media" protobuf. The archive also carries
//     a "collection.anki2" entry for older clients; it is ignored.
//   - legacy 2: a "collection.anki21" SQLite file using schema 11; its
//     "meta" version, when that entry is present, is below 3.
//   - legacy 1: a "collection.anki2" SQLite file using schema 11 and no
//     "collection.anki21".
//
// The wire details this reader depends on are stated where they are decoded:
// meta carries its version as varint field 1, the modern note type, field,
// template and deck configurations are protobuf messages read by field
// number, and the schema-11 layout stores note types and decks as JSON in the
// col table. Protobuf is decoded with protowire instead of generated code,
// because the reader needs a handful of fields and generating code would tie
// the build to Anki's proto tree.
//
// Safety: every archive is read under Limits, each entry name must be a
// single normal path component, and the SQLite payload is streamed into a
// temporary file under a generated name, never under a name taken from the
// archive. Media payloads are exposed as openers over their zip entries, so
// reading a package never materialises its media in memory.
//
// The layout and the wire details are those of ankitects/anki at tag 26.09.3:
// proto/anki/import_export.proto (meta and media), proto/anki/notetypes.proto
// and proto/anki/decks.proto (the configuration messages),
// rslib/src/import_export/package/ (archive order and compression),
// rslib/src/storage/schema11.sql and schema15_upgrade.sql (the two SQLite
// schemas). These references are the only place the field numbers come from;
// they cannot be derived from this repository.
package anki

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

// Format identifies the package layout that was read.
type Format int

const (
	// FormatLegacy1 is the original .apkg layout with collection.anki2.
	FormatLegacy1 Format = 1
	// FormatLegacy2 is the layout with collection.anki21.
	FormatLegacy2 Format = 2
	// FormatModern is the layout with a zstd-compressed collection.anki21b
	// and meta version 3.
	FormatModern Format = 3
)

// String returns the format's stable English name.
func (f Format) String() string {
	switch f {
	case FormatLegacy1:
		return "legacy1"
	case FormatLegacy2:
		return "legacy2"
	case FormatModern:
		return "modern"
	default:
		return fmt.Sprintf("format(%d)", int(f))
	}
}

// ReadFile reads the package at path. It is a convenience for small files
// (fixtures and tests): the file is loaded whole so the returned media openers
// stay usable after this call. The import path instead keeps the spooled file
// open and calls Read, so it never holds the archive in memory.
func ReadFile(path string) (*Package, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("anki package: read %s: %w", path, err)
	}
	return Read(bytes.NewReader(raw), int64(len(raw)))
}

// Read reads a package from r, which must provide size bytes.
func Read(r io.ReaderAt, size int64) (*Package, error) {
	return ReadWithLimits(r, size, DefaultLimits())
}
