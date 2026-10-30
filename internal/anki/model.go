package anki

import "io"

// Package is one decoded .apkg package. Slices are ordered deterministically:
// note types, decks, notes and cards by ID, media by the archive index.
type Package struct {
	Format    Format
	NoteTypes []NoteType
	Decks     []Deck
	Notes     []Note
	Cards     []Card
	Media     []MediaEntry
	// UnpackedBytes is the decompressed size of what the reader itself produced: the
	// collection, the meta entry and the media table. Media payloads are streamed by the
	// caller, so their bytes are added by the caller when it enforces the total cap.
	UnpackedBytes int64
}

// Note type kinds match Anki's own numbering in both package layouts.
const (
	KindNormal = 0
	KindCloze  = 1
)

// NoteType is one Anki note type (called a model in schema 11).
type NoteType struct {
	ID                int64
	Name              string
	Kind              int
	SortFieldIndex    int
	Fields            []Field
	Templates         []Template
	CSS               string
	LaTeXPre          string
	LaTeXPost         string
	LaTeXSVG          bool
	OriginalID        int64
	OriginalStockKind int
}

// Field is one note type field, in field order.
type Field struct {
	Ord               int
	Name              string
	ID                int64
	Sticky            bool
	RTL               bool
	Font              string
	FontSize          int
	Description       string
	PlainText         bool
	Collapsed         bool
	ExcludeFromSearch bool
	Tag               *int
	PreventDeletion   bool
}

// Template is one note type card template, in template order.
type Template struct {
	Ord             int
	Name            string
	ID              int64
	QFormat         string
	AFormat         string
	BQFormat        string
	BAFormat        string
	BrowserFont     string
	BrowserFontSize int
	TargetDeckID    int64
}

// Deck is one deck. Description and MarkdownDescription are only populated
// by layouts that carry them.
type Deck struct {
	ID                  int64
	Name                string
	Description         string
	MarkdownDescription bool
}

// Note is one note. Fields holds the field values in the note type's field
// order; Tags holds the whitespace-separated tags with the separators
// removed. Modified is Anki's modification timestamp in seconds.
type Note struct {
	ID         int64
	GUID       string
	NoteTypeID int64
	Fields     []string
	Tags       []string
	Modified   int64
}

// Card is one card. Ordinal is the template index, or for a cloze note the
// cloze number minus one.
type Card struct {
	ID      int64
	NoteID  int64
	DeckID  int64
	Ordinal int
}

// MediaEntry is one media file. Name is the file name as stored by Anki, not
// an archive entry name. Size is the size the package declares; a layout that
// declares none leaves it zero. SHA1 is the digest the package advertises, or
// nil when the layout advertises none. Open returns the decompressed stream;
// the caller closes it and keeps the package's source open while reading.
type MediaEntry struct {
	Name string
	Size uint32
	SHA1 []byte
	Open func() (io.ReadCloser, error)
}
