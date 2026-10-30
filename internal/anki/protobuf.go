package anki

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

// Protobuf messages are decoded field by field. Unknown fields, including
// fields from a newer Anki release, are skipped; a truncated or malformed
// message is reported rather than guessed at.

// errMalformedProto marks a protobuf message that protowire could not consume.
var errMalformedProto = fmt.Errorf("malformed protobuf message")

// walkProto calls fn for every field of the message in b. fn receives the
// field number, the wire type, the varint value (for varint fields) and the
// payload (for bytes fields).
func walkProto(b []byte, fn func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return errMalformedProto
		}
		b = b[n:]
		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return errMalformedProto
			}
			if err := fn(num, typ, v, nil); err != nil {
				return err
			}
			b = b[n:]
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return errMalformedProto
			}
			if err := fn(num, typ, 0, v); err != nil {
				return err
			}
			b = b[n:]
		case protowire.Fixed32Type:
			v, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return errMalformedProto
			}
			if err := fn(num, typ, uint64(v), nil); err != nil {
				return err
			}
			b = b[n:]
		case protowire.Fixed64Type:
			v, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return errMalformedProto
			}
			if err := fn(num, typ, v, nil); err != nil {
				return err
			}
			b = b[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return errMalformedProto
			}
			b = b[n:]
		}
	}
	return nil
}

// decodeMetaVersion reads PackageMetadata.version, field 1.
func decodeMetaVersion(b []byte) (uint64, error) {
	var version uint64
	err := walkProto(b, func(num protowire.Number, typ protowire.Type, v uint64, _ []byte) error {
		if num == 1 && typ == protowire.VarintType {
			version = v
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrCorruptMeta, err)
	}
	return version, nil
}

// noteTypeConfig is the subset of anki.notetypes.Notetype.Config this package
// needs. Zero values are the protobuf defaults, so an absent field keeps its
// Anki semantics.
type noteTypeConfig struct {
	Kind              int
	SortFieldIndex    int
	CSS               string
	LaTeXPre          string
	LaTeXPost         string
	LaTeXSVG          bool
	OriginalStockKind int
	HasOriginalID     bool
	OriginalID        int64
}

// decodeNoteTypeConfig reads Notetype.Config by field number.
func decodeNoteTypeConfig(b []byte) (noteTypeConfig, error) {
	var cfg noteTypeConfig
	err := walkProto(b, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
		if typ == protowire.VarintType {
			switch num {
			case 1:
				cfg.Kind = int(v)
			case 2:
				cfg.SortFieldIndex = int(v)
			case 7:
				cfg.LaTeXSVG = v != 0
			case 9:
				cfg.OriginalStockKind = int(v)
			case 10:
				cfg.HasOriginalID = true
				cfg.OriginalID = int64(v)
			}
			return nil
		}
		if typ != protowire.BytesType {
			return nil
		}
		switch num {
		case 3:
			cfg.CSS = string(raw)
		case 5:
			cfg.LaTeXPre = string(raw)
		case 6:
			cfg.LaTeXPost = string(raw)
		}
		return nil
	})
	if err != nil {
		return noteTypeConfig{}, err
	}
	return cfg, nil
}

// fieldConfig is the subset of Notetype.Field.Config this package needs.
type fieldConfig struct {
	Sticky            bool
	RTL               bool
	Font              string
	FontSize          int
	Description       string
	PlainText         bool
	Collapsed         bool
	ExcludeFromSearch bool
	ID                int64
	HasTag            bool
	Tag               int
	PreventDeletion   bool
}

// decodeFieldConfig reads Notetype.Field.Config by field number.
func decodeFieldConfig(b []byte) (fieldConfig, error) {
	var cfg fieldConfig
	err := walkProto(b, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
		if typ == protowire.VarintType {
			switch num {
			case 1:
				cfg.Sticky = v != 0
			case 2:
				cfg.RTL = v != 0
			case 4:
				cfg.FontSize = int(v)
			case 6:
				cfg.PlainText = v != 0
			case 7:
				cfg.Collapsed = v != 0
			case 8:
				cfg.ExcludeFromSearch = v != 0
			case 9:
				cfg.ID = int64(v)
			case 10:
				cfg.HasTag = true
				cfg.Tag = int(v)
			case 11:
				cfg.PreventDeletion = v != 0
			}
			return nil
		}
		if typ == protowire.BytesType {
			switch num {
			case 3:
				cfg.Font = string(raw)
			case 5:
				cfg.Description = string(raw)
			}
		}
		return nil
	})
	if err != nil {
		return fieldConfig{}, err
	}
	return cfg, nil
}

// templateConfig is the subset of Notetype.Template.Config this package needs.
type templateConfig struct {
	QFormat         string
	AFormat         string
	BQFormat        string
	BAFormat        string
	TargetDeckID    int64
	BrowserFont     string
	BrowserFontSize int
	ID              int64
}

// decodeTemplateConfig reads Notetype.Template.Config by field number.
func decodeTemplateConfig(b []byte) (templateConfig, error) {
	var cfg templateConfig
	err := walkProto(b, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
		if typ == protowire.VarintType {
			switch num {
			case 5:
				cfg.TargetDeckID = int64(v)
			case 7:
				cfg.BrowserFontSize = int(v)
			case 8:
				cfg.ID = int64(v)
			}
			return nil
		}
		if typ == protowire.BytesType {
			switch num {
			case 1:
				cfg.QFormat = string(raw)
			case 2:
				cfg.AFormat = string(raw)
			case 3:
				cfg.BQFormat = string(raw)
			case 4:
				cfg.BAFormat = string(raw)
			case 6:
				cfg.BrowserFont = string(raw)
			}
		}
		return nil
	})
	if err != nil {
		return templateConfig{}, err
	}
	return cfg, nil
}

// deckConfig is the description parts of anki.decks.Deck.
type deckConfig struct {
	Description         string
	MarkdownDescription bool
}

// decodeDeckKind reads Deck.kind. The oneof carries Normal in field 1 and
// Filtered in field 2; only Normal has a description.
func decodeDeckKind(b []byte) (deckConfig, error) {
	var cfg deckConfig
	err := walkProto(b, func(num protowire.Number, typ protowire.Type, _ uint64, raw []byte) error {
		if num != 1 || typ != protowire.BytesType {
			return nil
		}
		return walkProto(raw, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
			if typ == protowire.BytesType && num == 4 {
				cfg.Description = string(raw)
			}
			if typ == protowire.VarintType && num == 5 {
				cfg.MarkdownDescription = v != 0
			}
			return nil
		})
	})
	if err != nil {
		return deckConfig{}, err
	}
	return cfg, nil
}

// mediaMeta is one MediaEntry of the modern media protobuf.
type mediaMeta struct {
	Name string
	Size uint32
	SHA1 []byte
}

// decodeMediaEntries reads MediaEntries.entry, field 1, repeated.
func decodeMediaEntries(b []byte) ([]mediaMeta, error) {
	var entries []mediaMeta
	err := walkProto(b, func(num protowire.Number, typ protowire.Type, _ uint64, raw []byte) error {
		if num != 1 || typ != protowire.BytesType {
			return nil
		}
		entry, err := decodeMediaEntry(raw)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: media table: %v", ErrCorruptMedia, err)
	}
	return entries, nil
}

// decodeMediaEntry reads one MediaEntry by field number.
func decodeMediaEntry(b []byte) (mediaMeta, error) {
	var entry mediaMeta
	err := walkProto(b, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
		if typ == protowire.VarintType && num == 2 {
			entry.Size = uint32(v)
		}
		if typ == protowire.BytesType {
			switch num {
			case 1:
				entry.Name = string(raw)
			case 3:
				entry.SHA1 = append([]byte(nil), raw...)
			}
		}
		return nil
	})
	if err != nil {
		return mediaMeta{}, err
	}
	return entry, nil
}
